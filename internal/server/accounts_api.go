package server

// Account CRUD for the dashboard: a manual entry only needs a refresh token,
// and every field the pool needs is filled in here.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"farouter/internal/kiro"

	"github.com/go-chi/chi/v5"
)

// maskToken keeps enough of a token to tell entries apart without logging the
// secret.
func maskToken(tok string) string {
	if len(tok) <= 12 {
		return tok
	}
	return tok[:12] + "…"
}

func poolHasToken(tok string) bool {
	for _, a := range snapshotAccountConfigs() {
		if a.RefreshToken == tok {
			return true
		}
	}
	return false
}

// handleAccountsAdd adds accounts from newline-separated refresh tokens. Each
// token is validated against Kiro first, so a dead token is reported instead of
// silently joining the pool.
func handleAccountsAdd(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RefreshTokens string `json:"refreshTokens"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid body"}`, http.StatusBadRequest)
		return
	}

	type addedAccount struct {
		ID    string `json:"id"`
		Label string `json:"label"`
	}
	type failedAccount struct {
		Token string `json:"token"`
		Error string `json:"error"`
	}
	added := []addedAccount{}
	failed := []failedAccount{}

	next := nextKiroLabel(snapshotAccountConfigs())
	for _, line := range strings.Split(body.RefreshTokens, "\n") {
		tok := strings.TrimSpace(line)
		if tok == "" {
			continue
		}
		if poolHasToken(tok) {
			failed = append(failed, failedAccount{maskToken(tok), "already in pool"})
			continue
		}

		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		res, err := kiro.RefreshToken(ctx, tok, kiro.ProviderSpecificData{AuthMethod: "social"})
		cancel()
		if err != nil {
			failed = append(failed, failedAccount{maskToken(tok), err.Error()})
			continue
		}

		acc, _ := fillAccountDefaults(AccountConfig{RefreshToken: tok}, &next)
		acc.AuthMethod = "social"
		if res.ProfileArn != "" {
			acc.ProfileArn = res.ProfileArn
		}
		if res.RefreshToken != "" {
			acc.RefreshToken = res.RefreshToken
		}
		st := addAccountState(acc)
		added = append(added, addedAccount{ID: st.cfg.ID, Label: st.cfg.Label})
		time.Sleep(100 * time.Millisecond) // gentle on upstream during a bulk add
	}

	if len(added) > 0 {
		fillActiveBatch()
		saveConfig()
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"added": added, "failed": failed})
}

// handleAccountsUpdate renames an account or swaps its refresh token. A new
// token drops the cached access token, so the next request refreshes with it.
func handleAccountsUpdate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var body struct {
		Label        *string `json:"label"`
		RefreshToken *string `json:"refreshToken"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid body"}`, http.StatusBadRequest)
		return
	}

	accountsMu.RLock()
	var target *accountState
	for _, a := range accounts {
		if a.cfg.ID == id {
			target = a
			break
		}
	}
	accountsMu.RUnlock()
	if target == nil {
		http.Error(w, `{"error":"account not found"}`, http.StatusNotFound)
		return
	}

	target.mu.Lock()
	if body.Label != nil {
		if label := strings.TrimSpace(*body.Label); label != "" {
			target.cfg.Label = label
		}
	}
	if body.RefreshToken != nil {
		if tok := strings.TrimSpace(*body.RefreshToken); tok != "" {
			target.cfg.RefreshToken = tok
			// A new token means a fresh start: clear every dead-token marker so
			// the account can actually rejoin the pool.
			target.accessToken = ""
			target.expiry = time.Time{}
			target.invalid = false
			target.exhausted = false
			target.suspended = false
			target.failedRefresh = 0
			target.blocked = 0
			target.cooldownUntil = time.Time{}
			target.remaining = tokenLimit
			target.cfg.Invalid = false
			target.cfg.Exhausted = false
			target.cfg.Suspended = false
			target.cfg.ResetAt = ""
			accountsMu.Lock()
			delete(removedTokens, tok)
			accountsMu.Unlock()
		}
	}
	target.mu.Unlock()
	fillActiveBatch()
	saveConfig()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"updated": id})
}

// handleAccountsCheck re-verifies one account against Kiro right now: refresh
// the token, read the quota, and mark the result. This is the manual "what is
// this account's status" button.
func handleAccountsCheck(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	accountsMu.RLock()
	var target *accountState
	for _, a := range accounts {
		if a.cfg.ID == id {
			target = a
			break
		}
	}
	accountsMu.RUnlock()
	if target == nil {
		http.Error(w, `{"error":"account not found"}`, http.StatusNotFound)
		return
	}

	// Force a real refresh instead of trusting the cached access token.
	target.mu.Lock()
	target.accessToken = ""
	target.expiry = time.Time{}
	target.mu.Unlock()

	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()

	status := "ok"
	creds, err := target.getCreds(ctx)
	if err != nil {
		switch {
		case errors.Is(err, kiro.ErrTokenRejected):
			status = "invalid token"
		case errors.Is(err, kiro.ErrRefreshThrottled):
			status = "throttled"
		default:
			status = "refresh failed: " + err.Error()
		}
	} else if quota, qerr := kiro.FetchQuota(creds.AccessToken, creds.ProfileArn, creds.PSD.AuthMethod); qerr != nil {
		status = "refresh ok, quota unreadable"
	} else {
		applyQuota(target, quota)
		target.mu.Lock()
		target.invalid = false
		target.cfg.Invalid = false
		target.mu.Unlock()
		status = quota.Plan
	}

	target.mu.Lock()
	lastRefresh := target.cfg.LastRefreshedAt
	invalid, exhausted := target.invalid, target.exhausted
	remaining, suspended := target.remaining, target.suspended
	target.mu.Unlock()

	fillActiveBatch()
	saveConfig()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"id":              id,
		"status":          status,
		"invalid":         invalid,
		"exhausted":       exhausted,
		"suspended":       suspended,
		"remaining":       remaining,
		"lastRefreshedAt": lastRefresh,
	})
}

func handleAccountsDelete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		http.Error(w, `{"error":"missing id"}`, http.StatusBadRequest)
		return
	}
	if !removeAccountState(id) {
		http.Error(w, `{"error":"account not found"}`, http.StatusNotFound)
		return
	}
	fillActiveBatch()
	saveConfig()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"removed": id})
}
