package main

import (
	"context"
	"errors"
	"log"
	"strconv"
	"sync"
	"time"

	"farouter/internal/kiro"
)

// accountState holds runtime state for a single Kiro account.
type accountState struct {
	cfg           AccountConfig
	accessToken   string
	expiry        time.Time
	remaining     int
	exhausted     bool
	suspended     bool
	failedRefresh int
	mu            sync.Mutex
}

var (
	accounts     []*accountState
	configMu     sync.Mutex
	activeBatch  [3]*accountState
	standbyQueue []*accountState
	currentSlot  int
	stickyCount  int
	rotationMu   sync.Mutex
)

const stickyTarget = 3

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
		if err.Error() == "upstream returned 403 Forbidden" {
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

	a.mu.Lock()
	defer a.mu.Unlock()
	if err != nil {
		a.failedRefresh++
		if a.failedRefresh >= 3 {
			a.exhausted = true
			a.remaining = 0
			a.cfg.Exhausted = true
		}
		return kiro.Credentials{}, err
	}
	a.failedRefresh = 0
	a.accessToken = result.AccessToken
	a.expiry = time.Now().Add(time.Duration(result.ExpiresIn) * time.Second)
	a.cfg.LastRefreshedAt = time.Now().UTC().Format(time.RFC3339)
	if result.ProfileArn != "" {
		a.cfg.ProfileArn = result.ProfileArn
	}
	return kiro.Credentials{
		AccessToken:  a.accessToken,
		RefreshToken: a.cfg.RefreshToken,
		ProfileArn:   a.cfg.ProfileArn,
		PSD:          psd,
	}, nil
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
	wasExhausted := a.exhausted
	a.remaining = tokenLimit
	a.exhausted = false
	a.mu.Unlock()
	if wasExhausted && analyticsData != nil {
		go analyticsData.AddLog("Account reactivated: "+a.cfg.Label, "success", a.cfg.ID)
	}
}

func (a *accountState) available() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return !a.exhausted && !a.suspended && a.remaining > 0
}

func (a *accountState) consume() {
	a.mu.Lock()
	if a.remaining > 0 {
		a.remaining--
	}
	a.mu.Unlock()
}

func (a *accountState) refreshTokenIfNeeded() {
	a.mu.Lock()
	last := a.cfg.LastRefreshedAt
	suspended := a.suspended
	a.mu.Unlock()

	if suspended {
		return
	}

	if last != "" {
		t, err := time.Parse(time.RFC3339, last)
		if err == nil && time.Since(t) < 6*24*time.Hour {
			return
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, err := kiro.RefreshToken(ctx, a.cfg.RefreshToken, kiro.ProviderSpecificData{
		AuthMethod: a.cfg.AuthMethod,
		ProfileArn: a.cfg.ProfileArn,
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			log.Printf("refresh token skipped [%s]: %v", a.cfg.Label, err)
			return
		}
		a.mu.Lock()
		a.failedRefresh++
		if a.failedRefresh >= 3 {
			a.exhausted = true
			a.remaining = 0
			a.cfg.Exhausted = true
		}
		a.mu.Unlock()
		log.Printf("refresh token update failed [%s]: %v", a.cfg.Label, err)
		return
	}

	a.mu.Lock()
	a.cfg.RefreshToken = result.RefreshToken
	a.cfg.LastRefreshedAt = time.Now().UTC().Format(time.RFC3339)
	a.accessToken = result.AccessToken
	a.expiry = time.Now().Add(time.Duration(result.ExpiresIn) * time.Second)
	if result.ProfileArn != "" {
		a.cfg.ProfileArn = result.ProfileArn
	}
	a.mu.Unlock()
	log.Printf("refresh token updated [%s]", a.cfg.Label)
}

func startTokenRefreshLoop() {
	go func() {
		for range time.Tick(1 * time.Minute) {
			for _, a := range accounts {
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
			for _, a := range accounts {
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
	for _, a := range accounts {
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
	for _, a := range accounts {
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
