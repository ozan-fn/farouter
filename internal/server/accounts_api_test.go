package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func withURLParam(ctx context.Context, key, val string) context.Context {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, val)
	return context.WithValue(ctx, chi.RouteCtxKey, rctx)
}

func resetPool(t *testing.T) {
	t.Helper()
	accountsMu.Lock()
	accounts = nil
	removedIDs = map[string]bool{}
	removedTokens = map[string]bool{}
	accountsMu.Unlock()
	rotationMu.Lock()
	activeBatch[0], activeBatch[1], activeBatch[2] = nil, nil, nil
	standbyQueue = nil
	currentSlot, stickyCount = 0, 0
	rotationMu.Unlock()
	t.Cleanup(func() {
		accountsMu.Lock()
		accounts = nil
		removedIDs = map[string]bool{}
		removedTokens = map[string]bool{}
		accountsMu.Unlock()
		rotationMu.Lock()
		activeBatch[0], activeBatch[1], activeBatch[2] = nil, nil, nil
		standbyQueue = nil
		rotationMu.Unlock()
	})
}

func readConfig(t *testing.T) Config {
	t.Helper()
	var cfg Config
	data := mustReadFile(t, configPath)
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse config: %v", err)
	}
	return cfg
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}

// Bulk add: one token per line, validated, stored with filled defaults, and
// duplicates rejected without a second upstream call.
func TestAccountsAdd_Bulk(t *testing.T) {
	t.Chdir(t.TempDir())
	resetPool(t)
	cap := &countingTransport{status: http.StatusOK}
	stubKiroHTTP(t, cap)

	body := `{"refreshTokens":"  aorA:1 \n\n aorB:2 \n aorA:1 \n"}`
	req := httptest.NewRequest(http.MethodPost, "/accounts", strings.NewReader(body))
	rec := httptest.NewRecorder()
	handleAccountsAdd(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var res struct {
		Added []struct {
			ID    string `json:"id"`
			Label string `json:"label"`
		} `json:"added"`
		Failed []struct {
			Token string `json:"token"`
			Error string `json:"error"`
		} `json:"failed"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(res.Added) != 2 {
		t.Fatalf("added = %d, want 2 (%s)", len(res.Added), rec.Body.String())
	}
	if len(res.Failed) != 1 || !strings.Contains(res.Failed[0].Error, "already") {
		t.Errorf("failed = %+v, want one duplicate", res.Failed)
	}
	if res.Added[0].Label != "Kiro 1" || res.Added[1].Label != "Kiro 2" {
		t.Errorf("labels = %q,%q, want Kiro 1,Kiro 2", res.Added[0].Label, res.Added[1].Label)
	}
	if res.Added[0].ID == "" || res.Added[0].ID == res.Added[1].ID {
		t.Errorf("ids not unique: %q %q", res.Added[0].ID, res.Added[1].ID)
	}

	cfg := readConfig(t)
	if len(cfg.Accounts) != 2 {
		t.Fatalf("persisted accounts = %d, want 2", len(cfg.Accounts))
	}
	for _, a := range cfg.Accounts {
		if a.AuthMethod != "social" || a.MachineId == "" || a.ID == "" {
			t.Errorf("persisted entry incomplete: %+v", a)
		}
		if a.RefreshToken != "aorA:1" && a.RefreshToken != "aorB:2" {
			t.Errorf("persisted token = %q, want the added one", a.RefreshToken)
		}
		if a.ProfileArn == "" {
			t.Error("persisted entry missing profileArn from refresh")
		}
	}
}

// A rejected token must not enter the pool.
func TestAccountsAdd_Rejected(t *testing.T) {
	t.Chdir(t.TempDir())
	resetPool(t)
	cap := &countingTransport{status: http.StatusUnauthorized}
	stubKiroHTTP(t, cap)

	req := httptest.NewRequest(http.MethodPost, "/accounts", strings.NewReader(`{"refreshTokens":"aorDead:1"}`))
	rec := httptest.NewRecorder()
	handleAccountsAdd(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if len(snapshotAccounts()) != 0 {
		t.Error("rejected token must not join the pool")
	}
	if !strings.Contains(rec.Body.String(), "rejected") {
		t.Errorf("body = %s, want the rejection reason", rec.Body.String())
	}
}

func TestAccountsAdd_InvalidBody(t *testing.T) {
	resetPool(t)
	req := httptest.NewRequest(http.MethodPost, "/accounts", strings.NewReader(`not json`))
	rec := httptest.NewRecorder()
	handleAccountsAdd(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// Deleting removes the account from the pool and from the saved config.
func TestAccountsDelete(t *testing.T) {
	t.Chdir(t.TempDir())
	resetPool(t)
	cap := &countingTransport{status: http.StatusOK}
	stubKiroHTTP(t, cap)

	add := httptest.NewRequest(http.MethodPost, "/accounts", strings.NewReader(`{"refreshTokens":"aorA:1\naorB:2"}`))
	handleAccountsAdd(httptest.NewRecorder(), add)

	pool := snapshotAccounts()
	if len(pool) != 2 {
		t.Fatalf("pool = %d, want 2", len(pool))
	}
	id := pool[0].cfg.ID

	req := httptest.NewRequest(http.MethodDelete, "/accounts/"+id, nil)
	req = req.WithContext(withURLParam(req.Context(), "id", id))
	rec := httptest.NewRecorder()
	handleAccountsDelete(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := len(snapshotAccounts()); got != 1 {
		t.Errorf("pool = %d, want 1", got)
	}
	if len(readConfig(t).Accounts) != 1 {
		t.Error("deleted account still persisted")
	}
}

// Renaming and swapping the token must take effect and survive a save.
func TestAccountsUpdate(t *testing.T) {
	t.Chdir(t.TempDir())
	resetPool(t)
	cap := &countingTransport{status: http.StatusOK}
	stubKiroHTTP(t, cap)

	handleAccountsAdd(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/accounts", strings.NewReader(`{"refreshTokens":"aorA:1"}`)))
	pool := snapshotAccounts()
	if len(pool) != 1 {
		t.Fatalf("pool = %d, want 1", len(pool))
	}
	id := pool[0].cfg.ID

	req := httptest.NewRequest(http.MethodPatch, "/accounts/"+id, strings.NewReader(`{"label":"Kiro Baru","refreshToken":"aorB:2"}`))
	req = req.WithContext(withURLParam(req.Context(), "id", id))
	rec := httptest.NewRecorder()
	handleAccountsUpdate(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	pool = snapshotAccounts()
	pool[0].mu.Lock()
	label, token, cached := pool[0].cfg.Label, pool[0].cfg.RefreshToken, pool[0].accessToken
	pool[0].mu.Unlock()
	if label != "Kiro Baru" {
		t.Errorf("label = %q, want Kiro Baru", label)
	}
	if token != "aorB:2" {
		t.Errorf("token = %q, want aorB:2", token)
	}
	if cached != "" {
		t.Error("cached access token must be dropped so the new token is used")
	}
	if cfg := readConfig(t); len(cfg.Accounts) != 1 || cfg.Accounts[0].RefreshToken != "aorB:2" {
		t.Errorf("persisted config not updated: %+v", cfg.Accounts)
	}
}

// The manual check must re-verify with Kiro and mark a revoked token invalid.
func TestAccountsCheck_MarksInvalid(t *testing.T) {
	t.Chdir(t.TempDir())
	resetPool(t)
	cap := &countingTransport{status: http.StatusUnauthorized}
	stubKiroHTTP(t, cap)

	st := addAccountState(AccountConfig{ID: "id-c", Label: "Kiro 9", RefreshToken: "aorC:1", AuthMethod: "social"})

	req := httptest.NewRequest(http.MethodPost, "/accounts/id-c/check", nil)
	req = req.WithContext(withURLParam(req.Context(), "id", "id-c"))
	rec := httptest.NewRecorder()
	handleAccountsCheck(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !st.invalid {
		t.Error("check must mark a 401 token invalid")
	}
	if !strings.Contains(rec.Body.String(), "invalid token") {
		t.Errorf("body = %s, want invalid token", rec.Body.String())
	}
	if len(readConfig(t).Accounts) == 0 {
		t.Error("check result must be persisted")
	}
}

// A healthy check clears a previous invalid mark so the account is usable again.
func TestAccountsCheck_OkClearsInvalid(t *testing.T) {
	t.Chdir(t.TempDir())
	resetPool(t)
	cap := &countingTransport{status: http.StatusOK}
	stubKiroHTTP(t, cap)

	st := addAccountState(AccountConfig{ID: "id-ok", Label: "Kiro 10", RefreshToken: "aorD:1", AuthMethod: "social"})
	st.invalid = true
	st.remaining = 0

	req := httptest.NewRequest(http.MethodPost, "/accounts/id-ok/check", nil)
	req = req.WithContext(withURLParam(req.Context(), "id", "id-ok"))
	rec := httptest.NewRecorder()
	handleAccountsCheck(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if st.invalid {
		t.Error("healthy check must clear the invalid mark")
	}
	if !st.available() {
		t.Errorf("account should be available again after a healthy check (remaining=%d)", st.remaining)
	}
	if st.remaining != 50 {
		t.Errorf("remaining = %d, want 50 from the quota read", st.remaining)
	}
	if !strings.Contains(rec.Body.String(), "KIRO FREE") {
		t.Errorf("body = %s, want the plan name", rec.Body.String())
	}
}

func TestAccountsCheck_Unknown(t *testing.T) {
	t.Chdir(t.TempDir())
	resetPool(t)

	req := httptest.NewRequest(http.MethodPost, "/accounts/nope/check", nil)
	req = req.WithContext(withURLParam(req.Context(), "id", "nope"))
	rec := httptest.NewRecorder()
	handleAccountsCheck(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

// Swapping the token must also clear dead-token/quota markers.
func TestAccountsUpdate_ClearsDeadState(t *testing.T) {
	t.Chdir(t.TempDir())
	resetPool(t)
	cap := &countingTransport{status: http.StatusOK}
	stubKiroHTTP(t, cap)

	st := addAccountState(AccountConfig{ID: "id-d", Label: "Kiro 11", RefreshToken: "aorE:1", AuthMethod: "social"})
	st.invalid = true
	st.exhausted = true
	st.remaining = 0

	req := httptest.NewRequest(http.MethodPatch, "/accounts/id-d", strings.NewReader(`{"refreshToken":"aorF:2"}`))
	req = req.WithContext(withURLParam(req.Context(), "id", "id-d"))
	if rec := httptest.NewRecorder(); true {
		handleAccountsUpdate(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}
	}

	if st.invalid || st.exhausted || st.remaining == 0 {
		t.Errorf("state not reset: invalid=%v exhausted=%v remaining=%d", st.invalid, st.exhausted, st.remaining)
	}
	if !st.available() {
		t.Error("account should rejoin the pool after a token swap")
	}
}

func TestAccountsUpdate_Unknown(t *testing.T) {
	t.Chdir(t.TempDir())
	resetPool(t)

	req := httptest.NewRequest(http.MethodPatch, "/accounts/nope", strings.NewReader(`{"label":"x"}`))
	req = req.WithContext(withURLParam(req.Context(), "id", "nope"))
	rec := httptest.NewRecorder()
	handleAccountsUpdate(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestAccountsDelete_Unknown(t *testing.T) {
	t.Chdir(t.TempDir())
	resetPool(t)

	req := httptest.NewRequest(http.MethodDelete, "/accounts/nope", nil)
	req = req.WithContext(withURLParam(req.Context(), "id", "nope"))
	rec := httptest.NewRecorder()
	handleAccountsDelete(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}
