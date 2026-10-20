package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"farouter/internal/kiro"

	"github.com/google/uuid"
)

// bootReady signals when config loading is complete and accounts are initialized.
var bootReady = make(chan struct{})

// mergeExternalAccounts keeps accounts that were pasted into config.json while
// the server was running. saveConfig writes the in-memory pool, so without this
// a manual entry disappears on the next save (once a minute). Entries the pool
// already knows keep their in-memory state, which holds the rotated token.
func mergeExternalAccounts(memory []AccountConfig) []AccountConfig {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return memory
	}
	var disk Config
	if err := json.Unmarshal(data, &disk); err != nil {
		return memory
	}

	knownID := map[string]bool{}
	knownToken := map[string]bool{}
	for _, a := range memory {
		if a.ID != "" {
			knownID[a.ID] = true
		}
		if a.RefreshToken != "" {
			knownToken[a.RefreshToken] = true
		}
	}

	var added []AccountConfig
	for _, a := range disk.Accounts {
		if a.RefreshToken == "" {
			continue
		}
		if a.ID != "" && knownID[a.ID] {
			continue
		}
		if knownToken[a.RefreshToken] {
			continue
		}
		if accountWasRemoved(a.ID, a.RefreshToken) {
			continue
		}
		added = append(added, a)
	}
	if len(added) == 0 {
		return memory
	}

	// Filled here too, so the kept entry is complete when it is loaded.
	next := nextKiroLabel(memory)
	for i := range added {
		added[i], _ = fillAccountDefaults(added[i], &next)
	}
	log.Printf("config: kept %d account(s) added while running — loaded on next start", len(added))
	return append(memory, added...)
}

// nextKiroLabel returns the next free "Kiro N" number.
func nextKiroLabel(accs []AccountConfig) int {
	next := 1
	for _, a := range accs {
		if n, err := strconv.Atoi(strings.TrimPrefix(a.Label, "Kiro ")); err == nil && n >= next {
			next = n + 1
		}
	}
	return next
}

// fillAccountDefaults completes a hand-written entry that only carries a
// refreshToken, so a manual paste works without knowing the other fields.
// profileArn is left empty on purpose: the refresh response fills it in.
func fillAccountDefaults(acc AccountConfig, nextLabel *int) (AccountConfig, bool) {
	changed := false
	if acc.ID == "" {
		acc.ID = uuid.New().String()
		changed = true
	}
	if acc.Label == "" {
		acc.Label = fmt.Sprintf("Kiro %d", *nextLabel)
		*nextLabel++
		changed = true
	}
	if acc.AuthMethod == "" {
		acc.AuthMethod = "social"
		changed = true
	}
	if acc.MachineId == "" {
		acc.MachineId = uuid.New().String()
		changed = true
	}
	return acc, changed
}

func saveConfig() {
	configMu.Lock()
	defer configMu.Unlock()
	rtkMu.RLock()
	rtkVal := rtkEnabled
	rtkMu.RUnlock()
	tokenSaverMu.RLock()
	caveman := cavemanLevel
	ponytail := ponytailLevel
	tokenSaverMu.RUnlock()
	cfg := Config{
		Password:      cfgPassword,
		RTKEnabled:    rtkVal,
		CavemanLevel:  caveman,
		PonytailLevel: ponytail,
	}

	if analyticsData != nil {
		stats := analyticsData.GetStats()
		if tokensUsed, ok := stats["tokensUsed"].(int64); ok {
			cfg.TokensUsed = tokensUsed
		}
		if tokensGen, ok := stats["tokensGenerated"].(int64); ok {
			cfg.TokensGenerated = tokensGen
		}
		if reqCount, ok := stats["requestCount"].(int); ok {
			cfg.RequestCount = reqCount
		}
		if failCount, ok := stats["failedCount"].(int); ok {
			cfg.FailedCount = failCount
		}
	}

	rotationMu.Lock()
	cfg.CurrentSlot = currentSlot
	cfg.StickyCount = stickyCount
	for i := 0; i < 3; i++ {
		if activeBatch[i] != nil {
			cfg.ActiveBatchIds = append(cfg.ActiveBatchIds, activeBatch[i].cfg.ID)
		}
	}
	rotationMu.Unlock()
	for _, a := range snapshotAccounts() {
		a.mu.Lock()
		entry := a.cfg
		entry.Exhausted = a.exhausted
		entry.Suspended = a.suspended
		entry.Invalid = a.invalid
		a.mu.Unlock()
		cfg.Accounts = append(cfg.Accounts, entry)
	}

	sessionsMu.RLock()
	for token, sess := range sessions {
		cfg.Sessions = append(cfg.Sessions, SessionConfig{
			Token:   token,
			Expires: sess.expires.Format(time.RFC3339),
		})
	}
	sessionsMu.RUnlock()

	cfg.Accounts = mergeExternalAccounts(cfg.Accounts)

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		log.Printf("saveConfig marshal: %v", err)
		return
	}
	if err := os.WriteFile(configPath, data, 0600); err != nil {
		log.Printf("saveConfig write: %v", err)
	}
}

func loadConfig() {
	data, err := os.ReadFile(configPath)
	if err != nil {
		log.Fatalf("read config: %v", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		log.Fatalf("parse config: %v", err)
	}

	var raw map[string]any
	json.Unmarshal(data, &raw)
	if _, ok := raw["rtkEnabled"]; ok {
		rtkEnabled = cfg.RTKEnabled
	}
	if cfg.CavemanLevel != "" {
		cavemanLevel = cfg.CavemanLevel
	}
	if cfg.PonytailLevel != "" {
		ponytailLevel = cfg.PonytailLevel
	}
	if cfg.KiroThrottleMs > 0 {
		kiro.SetKiroThrottleMs(cfg.KiroThrottleMs)
	}

	cfgPassword = cfg.Password

	if analyticsData != nil && (cfg.TokensUsed > 0 || cfg.TokensGenerated > 0 || cfg.RequestCount > 0) {
		analyticsData.LoadStats(cfg.TokensUsed, cfg.TokensGenerated, cfg.RequestCount, cfg.FailedCount)
	}

	sessionsMu.Lock()
	now := time.Now()
	for _, s := range cfg.Sessions {
		if expires, err := time.Parse(time.RFC3339, s.Expires); err == nil {
			if now.Before(expires) {
				sessions[s.Token] = sessionEntry{expires: expires}
				log.Printf("restored session: %s (expires: %s)", s.Token[:8]+"...", expires.Format(time.RFC3339))
			}
		}
	}
	sessionsMu.Unlock()
	needsSave := false
	nextLabel := nextKiroLabel(cfg.Accounts)
	for _, a := range cfg.Accounts {
		if a.RefreshToken == "" {
			continue
		}
		if filled, changed := fillAccountDefaults(a, &nextLabel); changed {
			a = filled
			needsSave = true
			log.Printf("filled defaults for account %s", a.Label)
		}
		state := &accountState{cfg: a, remaining: tokenLimit}
		if a.Exhausted {
			resetTime := parseResetAt(a.ResetAt)
			if !resetTime.IsZero() && now.After(resetTime) {
				state.exhausted = false
				state.cfg.Exhausted = false
				state.cfg.ResetAt = ""
			} else {
				state.exhausted = true
				state.remaining = 0
			}
		}
		if a.Suspended {
			state.suspended = true
			state.exhausted = true
			state.remaining = 0
		}
		if a.Invalid {
			state.invalid = true
			state.remaining = 0
		}
		accounts = append(accounts, state)
	}
	log.Printf("loaded %d kiro accounts", len(accounts))

	if needsSave {
		log.Printf("saving config with filled account defaults")
	}

	if len(cfg.ActiveBatchIds) > 0 {
		rotationMu.Lock()
		for i, id := range cfg.ActiveBatchIds {
			if i >= 3 {
				break
			}
			for _, a := range accounts {
				if a.cfg.ID == id && a.available() {
					activeBatch[i] = a
					break
				}
			}
		}
		currentSlot = cfg.CurrentSlot
		stickyCount = cfg.StickyCount
		rotationMu.Unlock()
	}

	fillActiveBatch()
	saveConfig()
	close(bootReady)

	go func() {
		pool := snapshotAccounts()
		total := len(pool)
		log.Printf("bg refresh: start — %d accounts", total)

		var refreshed, rejected, throttled, failed int
		for i, a := range pool {
			a.mu.Lock()
			alreadyExhausted := a.exhausted
			lastRefreshed := a.cfg.LastRefreshedAt
			a.mu.Unlock()
			if alreadyExhausted {
				continue
			}
			if !refreshDue(lastRefreshed, a.cfg.ID) {
				continue
			}
			creds, err := a.getCreds(context.Background())
			if err != nil {
				switch {
				case errors.Is(err, kiro.ErrTokenRejected):
					rejected++
				case errors.Is(err, kiro.ErrRefreshThrottled):
					throttled++
				default:
					failed++
					log.Printf("[%d/%d] %s: refresh failed — %v", i+1, total, a.cfg.Label, err)
				}
				continue
			}
			quota, err := kiro.FetchQuota(creds.AccessToken, a.cfg.ProfileArn, a.cfg.AuthMethod)
			if err != nil {
				failed++
				log.Printf("[%d/%d] %s: quota check failed — %v", i+1, total, a.cfg.Label, err)
				continue
			}
			applyQuota(a, quota)
			refreshed++
			if (i+1)%50 == 0 {
				log.Printf("bg refresh: %d/%d checked", i+1, total)
			}
		}

		log.Printf("bg refresh: phase 1 done — %d ok, %d token rejected, %d throttled, %d failed; refilling batch + saving",
			refreshed, rejected, throttled, failed)
		fillActiveBatch()
		saveConfig()

		log.Printf("bg refresh: phase 2 — rotating old tokens")
		for _, a := range snapshotAccounts() {
			a.refreshTokenIfNeeded()
		}
		saveConfig()

		active := 0
		exhausted := 0
		suspended := 0
		failedRefresh := 0
		for _, a := range snapshotAccounts() {
			a.mu.Lock()
			switch {
			case a.suspended:
				suspended++
			case a.failedRefresh >= 3:
				failedRefresh++
			case a.exhausted:
				exhausted++
			default:
				active++
			}
			a.mu.Unlock()
		}
		rotationMu.Lock()
		batchSize := 0
		for i := 0; i < 3; i++ {
			if activeBatch[i] != nil {
				batchSize++
			}
		}
		rotationMu.Unlock()

		log.Printf("bg refresh: done — %d active, %d exhausted, %d suspended, %d invalid token, pool=%d",
			active, exhausted, suspended, failedRefresh, batchSize)
	}()
}
