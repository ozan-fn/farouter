package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"time"

	"farouter/internal/kiro"
)

// bootReady signals when config loading is complete and accounts are initialized.
var bootReady = make(chan struct{})

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
	for _, a := range accounts {
		a.mu.Lock()
		entry := a.cfg
		entry.Exhausted = a.exhausted
		entry.Suspended = a.suspended
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
	for _, a := range cfg.Accounts {
		if a.RefreshToken == "" {
			continue
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
		accounts = append(accounts, state)
	}
	log.Printf("loaded %d kiro accounts", len(accounts))

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
		total := len(accounts)
		log.Printf("bg refresh: start — %d accounts", total)

		for i, a := range accounts {
			a.mu.Lock()
			alreadyExhausted := a.exhausted
			lastRefreshed := a.cfg.LastRefreshedAt
			a.mu.Unlock()
			if alreadyExhausted {
				continue
			}
			if lastRefreshed != "" {
				t, err := time.Parse(time.RFC3339, lastRefreshed)
				if err == nil && time.Since(t) < 6*24*time.Hour {
					continue
				}
			}
			creds, err := a.getCreds(context.Background())
			if err != nil {
				log.Printf("[%d/%d] %s: refresh failed — %v", i+1, total, a.cfg.Label, err)
				continue
			}
			quota, err := kiro.FetchQuota(creds.AccessToken, a.cfg.ProfileArn, a.cfg.AuthMethod)
			if err != nil {
				log.Printf("[%d/%d] %s: quota check failed — %v", i+1, total, a.cfg.Label, err)
				continue
			}
			a.mu.Lock()
			if quota.Limit > 0 {
				a.remaining = quota.Remaining
			}
			if quota.Exhausted {
				a.exhausted = true
				a.remaining = 0
				a.cfg.Exhausted = true
				a.cfg.ResetAt = quota.ResetAt
				log.Printf("[%d/%d] %s: exhausted — %d/%d reset=%s", i+1, total, a.cfg.Label, quota.Used, quota.Limit, quota.ResetAt)
			} else {
				log.Printf("[%d/%d] %s: quota %d/%d", i+1, total, a.cfg.Label, quota.Remaining, quota.Limit)
			}
			a.mu.Unlock()
		}

		log.Printf("bg refresh: phase 1 done, refilling batch + saving")
		fillActiveBatch()
		saveConfig()

		log.Printf("bg refresh: phase 2 — rotating old tokens")
		for _, a := range accounts {
			a.refreshTokenIfNeeded()
		}
		saveConfig()

		active := 0
		exhausted := 0
		suspended := 0
		failedRefresh := 0
		for _, a := range accounts {
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
