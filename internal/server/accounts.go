package server

import (
	"context"
	"errors"
	"hash/fnv"
	"log"
	"strconv"
	"sync"
	"time"

	"farouter/internal/kiro"
)

// accountState holds runtime state for a single Kiro account.
type accountState struct {
	cfg         AccountConfig
	accessToken string
	expiry      time.Time
	remaining   int
	exhausted   bool
	suspended   bool
	// invalid marks a refresh token the upstream refuses (401). It is a dead
	// token, not an empty quota, so it must not look like a quota exhaustion.
	invalid       bool
	failedRefresh int
	blocked       int
	cooldownUntil time.Time
	mu            sync.Mutex
	// refreshMu serialises token refreshes for this account: two goroutines
	// racing the same refresh token make one of them fail with 401 reused.
	refreshMu sync.Mutex
}

var (
	accountsMu sync.RWMutex
	accounts   []*accountState
	// Accounts deleted at runtime must not be resurrected by the next save,
	// which merges the pool over whatever is still on disk.
	removedIDs    = map[string]bool{}
	removedTokens = map[string]bool{}
	configMu      sync.Mutex
	activeBatch   [3]*accountState
	standbyQueue  []*accountState
	currentSlot   int
	stickyCount   int
	rotationMu    sync.Mutex
)

const stickyTarget = 3

// refreshCooldown backs off an account whose refresh the upstream throttled
// (403/429), instead of marking a still-good token dead.
const refreshCooldown = 10 * time.Minute

// saveConfigAsync persists runtime state. Tests replace it to stay off disk.
var saveConfigAsync = func() { go saveConfig() }

// snapshotAccounts returns a stable copy of the pool. The slice itself is only
// replaced by the CRUD handlers, so callers must not iterate it directly.
func snapshotAccounts() []*accountState {
	accountsMu.RLock()
	defer accountsMu.RUnlock()
	out := make([]*accountState, len(accounts))
	copy(out, accounts)
	return out
}

// accountWasRemoved reports whether an account with this id or token was
// deleted in this process, so a stale file entry is not merged back in.
func accountWasRemoved(id, token string) bool {
	accountsMu.RLock()
	defer accountsMu.RUnlock()
	return (id != "" && removedIDs[id]) || (token != "" && removedTokens[token])
}

func snapshotAccountConfigs() []AccountConfig {
	accountsMu.RLock()
	defer accountsMu.RUnlock()
	out := make([]AccountConfig, 0, len(accounts))
	for _, a := range accounts {
		a.mu.Lock()
		out = append(out, a.cfg)
		a.mu.Unlock()
	}
	return out
}

func addAccountState(acc AccountConfig) *accountState {
	st := &accountState{cfg: acc, remaining: tokenLimit}
	accountsMu.Lock()
	accounts = append(accounts, st)
	accountsMu.Unlock()
	return st
}

// removeAccountState drops an account from the pool and from the rotation
// batch. Reports false when the id is unknown.
func removeAccountState(id string) bool {
	accountsMu.Lock()
	idx := -1
	for i, a := range accounts {
		if a.cfg.ID == id {
			idx = i
			break
		}
	}
	if idx >= 0 {
		removedIDs[accounts[idx].cfg.ID] = true
		removedTokens[accounts[idx].cfg.RefreshToken] = true
		accounts = append(accounts[:idx], accounts[idx+1:]...)
	}
	accountsMu.Unlock()
	if idx < 0 {
		return false
	}

	rotationMu.Lock()
	for i := 0; i < 3; i++ {
		if activeBatch[i] != nil && activeBatch[i].cfg.ID == id {
			activeBatch[i] = nil
		}
	}
	rotationMu.Unlock()
	return true
}

// refreshSpread gives each account its own keepalive interval (5-7 days) and a
// minute slot, so a large pool never refreshes in one burst.
func refreshSpread(id string) (interval time.Duration, minuteSlot int) {
	h := fnv.New32a()
	_, _ = h.Write([]byte(id))
	sum := h.Sum32()
	return 5*24*time.Hour + time.Duration(sum%48)*time.Hour, int(sum % 60)
}

// refreshDue reports whether the keepalive for this account is due.
func refreshDue(lastRefreshed, id string) bool {
	t, err := time.Parse(time.RFC3339, lastRefreshed)
	if err != nil {
		return true
	}
	interval, _ := refreshSpread(id)
	return time.Since(t) >= interval
}

// recordRefreshError updates account state after a failed refresh and reports
// whether the caller should log it. A dead token is logged once, not on every
// loop tick, and a throttled account stays quiet until it escalates.
func (a *accountState) recordRefreshError(err error) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.exhausted {
		return false
	}
	switch {
	case errors.Is(err, kiro.ErrRefreshThrottled):
		a.blocked++
		a.cooldownUntil = time.Now().Add(refreshCooldown)
		if a.blocked < 3 {
			return false
		}
		a.invalid = true
		a.cfg.Invalid = true
	case errors.Is(err, kiro.ErrTokenRejected):
		a.invalid = true
		a.cfg.Invalid = true
	default:
		a.failedRefresh++
		if a.failedRefresh < 3 {
			return true
		}
		a.invalid = true
		a.cfg.Invalid = true
	}
	a.remaining = 0
	return true
}

func (a *accountState) getCreds(ctx context.Context) (kiro.Credentials, error) {
	a.mu.Lock()
	psd := kiro.ProviderSpecificData{
		AuthMethod: a.cfg.AuthMethod,
		ProfileArn: a.cfg.ProfileArn,
	}
	if a.cfg.KiroToolCallRepair != nil {
		psd.KiroToolCallRepair = *a.cfg.KiroToolCallRepair
		psd.KiroToolCallRepairSet = true
	}
	if a.accessToken != "" && time.Now().Add(5*time.Minute).Before(a.expiry) {
		creds := kiro.Credentials{
			AccessToken:  a.accessToken,
			RefreshToken: a.cfg.RefreshToken,
			ProfileArn:   a.cfg.ProfileArn,
			MachineId:    a.cfg.MachineId,
			PSD:          psd,
		}
		a.mu.Unlock()
		return creds, nil
	}
	a.mu.Unlock()

	// One refresh at a time per account. Two goroutines sharing the same
	// refresh token race upstream, and the loser gets 401 reused.
	a.refreshMu.Lock()
	defer a.refreshMu.Unlock()

	// Another caller may have refreshed while we waited for the lock.
	a.mu.Lock()
	if a.accessToken != "" && time.Now().Add(5*time.Minute).Before(a.expiry) {
		creds := kiro.Credentials{
			AccessToken:  a.accessToken,
			RefreshToken: a.cfg.RefreshToken,
			ProfileArn:   a.cfg.ProfileArn,
			MachineId:    a.cfg.MachineId,
			PSD:          psd,
		}
		a.mu.Unlock()
		return creds, nil
	}
	refreshToken := a.cfg.RefreshToken
	a.mu.Unlock()

	refreshCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var result *kiro.TokenResult
	var err error
	for i := 0; i < 3; i++ {
		select {
		case <-refreshCtx.Done():
			return kiro.Credentials{}, refreshCtx.Err()
		default:
		}
		result, err = kiro.RefreshToken(refreshCtx, refreshToken, psd)
		if err == nil {
			break
		}
		if errors.Is(err, kiro.ErrTokenRejected) || errors.Is(err, kiro.ErrRefreshThrottled) {
			a.recordRefreshError(err)
			return kiro.Credentials{}, err
		}
		log.Printf("refresh retry %d [%s]: %v", i+1, a.cfg.Label, err)
		if i < 2 {
			select {
			case <-refreshCtx.Done():
				return kiro.Credentials{}, refreshCtx.Err()
			case <-time.After(time.Duration(i+1) * time.Second):
			}
		}
	}

	if err != nil {
		a.recordRefreshError(err)
		return kiro.Credentials{}, err
	}

	a.mu.Lock()
	a.failedRefresh = 0
	a.blocked = 0
	a.cooldownUntil = time.Time{}
	a.accessToken = result.AccessToken
	a.expiry = time.Now().Add(time.Duration(result.ExpiresIn) * time.Second)
	a.cfg.LastRefreshedAt = time.Now().UTC().Format(time.RFC3339)
	if result.ProfileArn != "" {
		a.cfg.ProfileArn = result.ProfileArn
	}
	// A rotated refresh token must hit disk at once: keeping the old one after
	// a crash loses the account for good.
	rotated := result.RefreshToken != "" && result.RefreshToken != a.cfg.RefreshToken
	if result.RefreshToken != "" {
		a.cfg.RefreshToken = result.RefreshToken
	}
	creds := kiro.Credentials{
		AccessToken:  a.accessToken,
		RefreshToken: a.cfg.RefreshToken,
		ProfileArn:   a.cfg.ProfileArn,
		MachineId:    a.cfg.MachineId,
		PSD:          psd,
	}
	a.mu.Unlock()

	if rotated {
		saveConfigAsync()
	}
	return creds, nil
}

// applyQuota stores a quota reading: remaining count, and the exhausted flag
// with its reset time. A reading with headroom clears a previous exhaustion.
func applyQuota(a *accountState, q *kiro.QuotaResult) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if q.Limit > 0 {
		a.remaining = q.Remaining
	}
	if q.Exhausted {
		a.exhausted = true
		a.remaining = 0
		a.cfg.Exhausted = true
		a.cfg.ResetAt = q.ResetAt
		return
	}
	a.exhausted = false
	a.cfg.Exhausted = false
	a.cfg.ResetAt = ""
}

func (a *accountState) markExhausted() {
	a.mu.Lock()
	a.exhausted = true
	a.remaining = 0
	a.cfg.Exhausted = true
	a.mu.Unlock()
	if analyticsData != nil {
		go analyticsData.AddLog("Account exhausted: "+a.cfg.Label, "warning", a.cfg.ID)
	}
}

func (a *accountState) markSuspended() {
	a.mu.Lock()
	a.suspended = true
	a.exhausted = true
	a.remaining = 0
	a.cfg.Suspended = true
	a.cfg.Exhausted = true
	a.mu.Unlock()
	if analyticsData != nil {
		go analyticsData.AddLog("Account suspended: "+a.cfg.Label, "error", a.cfg.ID)
	}
}

func (a *accountState) reset() {
	a.mu.Lock()
	wasExhausted := a.exhausted || a.invalid
	a.remaining = tokenLimit
	a.exhausted = false
	a.invalid = false
	a.cfg.Exhausted = false
	a.cfg.Invalid = false
	a.mu.Unlock()
	if wasExhausted && analyticsData != nil {
		go analyticsData.AddLog("Account reactivated: "+a.cfg.Label, "success", a.cfg.ID)
	}
}

func (a *accountState) available() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return !a.exhausted && !a.suspended && !a.invalid && a.remaining > 0 && !time.Now().Before(a.cooldownUntil)
}

func (a *accountState) consume() {
	a.mu.Lock()
	if a.remaining > 0 {
		a.remaining--
	}
	a.mu.Unlock()
}

// refreshIsDue reports whether this account should rotate its token now:
// not suspended/exhausted/cooling down, and past its spread-out keepalive
// interval.
func (a *accountState) refreshIsDue() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.suspended || a.exhausted || a.invalid || time.Now().Before(a.cooldownUntil) {
		return false
	}
	return refreshDue(a.cfg.LastRefreshedAt, a.cfg.ID)
}

func (a *accountState) refreshTokenIfNeeded() {
	if !a.refreshIsDue() {
		return
	}

	// Serialise with request-path refreshes for the same account.
	a.refreshMu.Lock()
	defer a.refreshMu.Unlock()

	if !a.refreshIsDue() { // another caller refreshed while we waited
		return
	}

	a.mu.Lock()
	refreshToken := a.cfg.RefreshToken
	psd := kiro.ProviderSpecificData{AuthMethod: a.cfg.AuthMethod, ProfileArn: a.cfg.ProfileArn}
	a.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, err := kiro.RefreshToken(ctx, refreshToken, psd)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			log.Printf("refresh token skipped [%s]: %v", a.cfg.Label, err)
			return
		}
		if a.recordRefreshError(err) {
			log.Printf("refresh token update failed [%s]: %v", a.cfg.Label, err)
		}
		return
	}

	a.mu.Lock()
	rotated := result.RefreshToken != "" && result.RefreshToken != a.cfg.RefreshToken
	if result.RefreshToken != "" {
		a.cfg.RefreshToken = result.RefreshToken
	}
	a.cfg.LastRefreshedAt = time.Now().UTC().Format(time.RFC3339)
	a.accessToken = result.AccessToken
	a.expiry = time.Now().Add(time.Duration(result.ExpiresIn) * time.Second)
	a.failedRefresh = 0
	a.blocked = 0
	a.cooldownUntil = time.Time{}
	if result.ProfileArn != "" {
		a.cfg.ProfileArn = result.ProfileArn
	}
	a.mu.Unlock()

	if rotated {
		saveConfigAsync()
	}
	log.Printf("refresh token updated [%s]", a.cfg.Label)
}

func startTokenRefreshLoop() {
	go func() {
		for range time.Tick(1 * time.Minute) {
			minute := time.Now().Minute()
			for _, a := range snapshotAccounts() {
				// Each account has its own minute slot, so a big pool is spread
				// over the hour instead of refreshed on the same tick.
				if _, slot := refreshSpread(a.cfg.ID); slot != minute {
					continue
				}
				a.refreshTokenIfNeeded()
			}
			saveConfig()
		}
	}()
}

func startResetWatcher() {
	go func() {
		<-bootReady
		for range time.Tick(1 * time.Minute) {
			now := time.Now()
			reactivated := 0
			for _, a := range snapshotAccounts() {
				a.mu.Lock()
				if a.exhausted && a.cfg.ResetAt != "" {
					resetTime := parseResetAt(a.cfg.ResetAt)
					if !resetTime.IsZero() && now.After(resetTime) {
						a.exhausted = false
						a.remaining = tokenLimit
						a.cfg.Exhausted = false
						a.cfg.ResetAt = ""
						reactivated++
						log.Printf("reset watcher: reactivated [%s]", a.cfg.Label)
					}
				}
				a.mu.Unlock()
			}
			if reactivated > 0 {
				fillActiveBatch()
				saveConfig()
				log.Printf("reset watcher: reactivated %d accounts, pool refilled", reactivated)
			}
		}
	}()
}

func parseResetAt(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return time.Unix(int64(f), 0)
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	return time.Time{}
}

func fillActiveBatch() {
	rotationMu.Lock()
	defer rotationMu.Unlock()

	now := time.Now()
	standbyQueue = nil
	for _, a := range snapshotAccounts() {
		if a.exhausted && a.cfg.ResetAt != "" {
			resetTime := parseResetAt(a.cfg.ResetAt)
			if !resetTime.IsZero() && now.After(resetTime) {
				a.exhausted = false
				a.remaining = tokenLimit
				a.cfg.Exhausted = false
				a.cfg.ResetAt = ""
				log.Printf("fillActiveBatch: auto-reactivated [%s]", a.cfg.Label)
			}
		}
		if a.available() {
			standbyQueue = append(standbyQueue, a)
		}
	}

	for i := 0; i < 3 && len(standbyQueue) > 0; i++ {
		activeBatch[i] = standbyQueue[0]
		standbyQueue = standbyQueue[1:]
	}

	currentSlot = 0
	stickyCount = 0
}

func pickAccount() *accountState {
	rotationMu.Lock()
	defer rotationMu.Unlock()

	acc := activeBatch[currentSlot]
	if acc != nil && acc.available() {
		if stickyCount < stickyTarget {
			stickyCount++
			return acc
		}
		currentSlot = (currentSlot + 1) % 3
		stickyCount = 0
	}

	for attempt := 0; attempt < 3; attempt++ {
		acc = activeBatch[currentSlot]
		if acc != nil && acc.available() {
			stickyCount = 1
			return acc
		}
		currentSlot = (currentSlot + 1) % 3
	}

	return nil
}

func getNextAvailableAccount() *accountState {
	rotationMu.Lock()
	defer rotationMu.Unlock()

	inBatch := map[*accountState]bool{}
	for i := 0; i < 3; i++ {
		if activeBatch[i] != nil {
			inBatch[activeBatch[i]] = true
		}
	}
	standbyQueue = nil
	for _, a := range snapshotAccounts() {
		if a.available() && !inBatch[a] {
			standbyQueue = append(standbyQueue, a)
		}
	}

	if len(standbyQueue) > 0 {
		acc := standbyQueue[0]
		standbyQueue = standbyQueue[1:]
		return acc
	}

	return nil
}
