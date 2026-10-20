package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"farouter/internal/kiro"

	"github.com/go-resty/resty/v2"
)

// countingTransport answers every refresh with a canned status and counts hits.
type countingTransport struct {
	hits    int
	status  int
	delay   time.Duration
	rotated bool
	mu      sync.Mutex
}

func (c *countingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c.mu.Lock()
	c.hits++
	d := c.delay
	c.mu.Unlock()
	if d > 0 {
		time.Sleep(d) // widen the race window
	}

	// Echo the token back (optionally rotated), like a real refresh response.
	// Quota GETs carry no body, so guard the read.
	var sent []byte
	if r.Body != nil {
		sent, _ = io.ReadAll(r.Body)
	}
	var req struct {
		RefreshToken string `json:"refreshToken"`
	}
	_ = json.Unmarshal(sent, &req)
	rt := req.RefreshToken
	if c.rotated {
		rt += "-rot"
	}

	status := c.status
	body := fmt.Sprintf(`{"accessToken":"aoaTest","refreshToken":%q,"expiresIn":3600,"profileArn":"arn:test"}`, rt)
	if strings.Contains(r.URL.Path, "getUsageLimits") {
		body = `{"usageBreakdownList":[{"resourceType":"AGENTIC_REQUEST","currentUsage":0,"usageLimit":50}],"subscriptionInfo":{"subscriptionTitle":"KIRO FREE"}}`
	}
	if status != http.StatusOK && status != 0 {
		body = `{"message":"Bad credentials"}`
	} else {
		status = http.StatusOK
	}
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    r,
	}, nil
}

func (c *countingTransport) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hits
}

// stubKiroHTTP points the kiro HTTP client at cap and stubs the on-disk save.
// The returned counter counts persist requests.
func stubKiroHTTP(t *testing.T, cap *countingTransport) *int {
	t.Helper()
	kiro.SetHTTPClient(resty.NewWithClient(&http.Client{Transport: cap}))
	persisted := new(int)
	var mu sync.Mutex
	old := saveConfigAsync
	saveConfigAsync = func() {
		mu.Lock()
		*persisted++
		mu.Unlock()
	}
	t.Cleanup(func() {
		kiro.SetHTTPClient(resty.New())
		saveConfigAsync = old
	})
	return persisted
}

func newTestAccount() *accountState {
	return &accountState{cfg: AccountConfig{ID: "acc-1", Label: "test", RefreshToken: "aorAAAA:sig"}, remaining: tokenLimit}
}

func snapshot(a *accountState) (exhausted bool, cooldown time.Time, blocked int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.exhausted, a.cooldownUntil, a.blocked
}

// A dead token (401) must be marked once and never re-hit: the old code retried
// every account every minute, which is the log flood and the upstream burst.
func TestRefreshTokenIfNeeded_RejectedTokenHitOnce(t *testing.T) {
	cap := &countingTransport{status: http.StatusUnauthorized}
	stubKiroHTTP(t, cap)

	a := newTestAccount()
	for i := 0; i < 3; i++ {
		a.refreshTokenIfNeeded()
	}

	if got := cap.count(); got != 1 {
		t.Errorf("upstream hits = %d, want 1", got)
	}
	a.mu.Lock()
	invalid := a.invalid
	a.mu.Unlock()
	if !invalid {
		t.Error("401 token should mark the account invalid")
	}
	if exhausted, _, _ := snapshot(a); exhausted {
		t.Error("a dead token is not a quota exhaustion")
	}
}

// A 403/429 means upstream pushed back on a burst, not that the token is dead:
// back off and keep the account usable.
func TestRefreshTokenIfNeeded_ThrottledBacksOff(t *testing.T) {
	cap := &countingTransport{status: http.StatusForbidden}
	stubKiroHTTP(t, cap)

	a := newTestAccount()
	a.refreshTokenIfNeeded()
	a.refreshTokenIfNeeded()

	if got := cap.count(); got != 1 {
		t.Errorf("upstream hits = %d, want 1 (second call inside cooldown)", got)
	}
	exhausted, cooldown, _ := snapshot(a)
	if exhausted {
		t.Error("throttled account must not be marked exhausted")
	}
	if !cooldown.After(time.Now()) {
		t.Errorf("cooldownUntil = %v, want in the future", cooldown)
	}
}

// Only a third consecutive throttle is treated as a real rejection.
func TestRefreshTokenIfNeeded_ThrottledEscalatesAfterThree(t *testing.T) {
	cap := &countingTransport{status: http.StatusForbidden}
	stubKiroHTTP(t, cap)

	a := newTestAccount()
	a.blocked = 2
	a.refreshTokenIfNeeded()

	a.mu.Lock()
	invalid, blocked := a.invalid, a.blocked
	a.mu.Unlock()
	if !invalid || blocked != 3 {
		t.Errorf("invalid=%v blocked=%d, want true/3", invalid, blocked)
	}
}

// A successful refresh must clear the backoff so the account rejoins the pool.
func TestRefreshTokenIfNeeded_SuccessClearsBackoff(t *testing.T) {
	cap := &countingTransport{status: http.StatusOK}
	stubKiroHTTP(t, cap)

	a := newTestAccount()
	a.blocked = 2
	a.cooldownUntil = time.Now()
	a.refreshTokenIfNeeded()

	exhausted, cooldown, blocked := snapshot(a)
	if exhausted || blocked != 0 || !cooldown.IsZero() {
		t.Errorf("exhausted=%v blocked=%d cooldown=%v, want false/0/zero", exhausted, blocked, cooldown)
	}
	if got := cap.count(); got != 1 {
		t.Errorf("upstream hits = %d, want 1", got)
	}
}

// A cooling-down account must not be handed out to traffic.
func TestAvailable_HidesCoolingAccount(t *testing.T) {
	a := newTestAccount()
	if !a.available() {
		t.Fatal("fresh account should be available")
	}
	a.cooldownUntil = time.Now().Add(time.Minute)
	if a.available() {
		t.Error("cooling-down account must not be available")
	}
	a.cooldownUntil = time.Now().Add(-time.Second)
	if !a.available() {
		t.Error("account should be available after cooldown expires")
	}
}

// Concurrent refreshes for one account must collapse into a single upstream
// call: racing the same refresh token makes one caller fail with 401 reused.
func TestRefreshTokenIfNeeded_SingleFlight(t *testing.T) {
	cap := &countingTransport{status: http.StatusOK, delay: 40 * time.Millisecond}
	stubKiroHTTP(t, cap)

	a := newTestAccount()
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a.refreshTokenIfNeeded()
		}()
	}
	wg.Wait()

	if got := cap.count(); got != 1 {
		t.Errorf("upstream hits = %d, want 1 (single flight)", got)
	}
}

// Two requests asking for credentials at the same time must share one refresh.
func TestGetCreds_SingleFlight(t *testing.T) {
	cap := &countingTransport{status: http.StatusOK, delay: 40 * time.Millisecond}
	stubKiroHTTP(t, cap)

	a := newTestAccount()
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := a.getCreds(context.Background()); err != nil {
				t.Errorf("getCreds: %v", err)
			}
		}()
	}
	wg.Wait()

	if got := cap.count(); got != 1 {
		t.Errorf("upstream hits = %d, want 1 (single flight)", got)
	}
}

// A rotated refresh token is useless until it is on disk: persist immediately.
func TestRefreshTokenIfNeeded_PersistsRotatedToken(t *testing.T) {
	cap := &countingTransport{status: http.StatusOK, rotated: true}
	persisted := stubKiroHTTP(t, cap)

	a := newTestAccount()
	a.refreshTokenIfNeeded()

	a.mu.Lock()
	got := a.cfg.RefreshToken
	a.mu.Unlock()
	if got != "aorAAAA:sig-rot" {
		t.Errorf("stored refresh token = %q, want aorAAAA:sig-rot", got)
	}
	if *persisted != 1 {
		t.Errorf("persist calls = %d, want 1", *persisted)
	}
}

// An account refreshed recently must not be touched again.
func TestRefreshTokenIfNeeded_SkipsWhenNotDue(t *testing.T) {
	cap := &countingTransport{status: http.StatusOK}
	stubKiroHTTP(t, cap)

	a := newTestAccount()
	a.cfg.LastRefreshedAt = time.Now().UTC().Format(time.RFC3339)
	a.refreshTokenIfNeeded()

	if got := cap.count(); got != 0 {
		t.Errorf("upstream hits = %d, want 0 (not due)", got)
	}
}

// Keepalive timing is spread per account instead of everyone on one tick.
func TestRefreshDue_SpreadPerAccount(t *testing.T) {
	now := time.Now().UTC()
	if refreshDue(now.Format(time.RFC3339), "acc-1") {
		t.Error("just-refreshed account must not be due")
	}
	if !refreshDue(now.Add(-8*24*time.Hour).Format(time.RFC3339), "acc-1") {
		t.Error("8-day-old token must be due")
	}
	if !refreshDue("", "acc-1") {
		t.Error("account without lastRefreshedAt must be due")
	}

	seen := map[int]bool{}
	for _, id := range []string{"a", "b", "c", "acc-1", "acc-2", "x9"} {
		interval, slot := refreshSpread(id)
		if interval < 5*24*time.Hour || interval > 7*24*time.Hour-time.Hour {
			t.Errorf("interval(%s) = %v, want 5-7d", id, interval)
		}
		if slot < 0 || slot > 59 {
			t.Errorf("slot(%s) = %d, want 0-59", id, slot)
		}
		seen[slot] = true
	}
	if len(seen) == 1 {
		t.Error("all sampled accounts share one minute slot; spread is not working")
	}
	d1, s1 := refreshSpread("same")
	d2, s2 := refreshSpread("same")
	if d1 != d2 || s1 != s2 {
		t.Error("refreshSpread must be deterministic")
	}
}
