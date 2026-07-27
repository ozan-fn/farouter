package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"farouter/internal/analytics"
	"farouter/internal/kiro"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

//go:embed web/dist
var distFS embed.FS

const (
	kiroAuthService = "https://prod.us-east-1.auth.desktop.kiro.dev"
	tokenLimit      = 60
	poolSize        = 3
	configPath      = "config.json"
)

type AccountConfig struct {
	ID              string `json:"id"`
	Label           string `json:"label"`
	RefreshToken    string `json:"refreshToken"`
	ProfileArn      string `json:"profileArn"`
	AuthMethod      string `json:"authMethod"`
	Exhausted       bool   `json:"exhausted,omitempty"`
	Suspended       bool   `json:"suspended,omitempty"`
	ResetAt         string `json:"resetAt,omitempty"`
	LastRefreshedAt string `json:"lastRefreshedAt,omitempty"`
}

type SessionConfig struct {
	Token   string `json:"token"`
	Expires string `json:"expires"`
}

type Config struct {
	Password        string            `json:"password,omitempty"`
	ActiveBatchIds  []string          `json:"activeBatchIds,omitempty"`
	CurrentSlot     int               `json:"currentSlot,omitempty"`
	StickyCount     int               `json:"stickyCount,omitempty"`
	RTKEnabled      bool              `json:"rtkEnabled"`
	Accounts        []AccountConfig   `json:"accounts"`
	TokensUsed      int64             `json:"tokensUsed,omitempty"`
	TokensGenerated int64             `json:"tokensGenerated,omitempty"`
	RequestCount    int               `json:"requestCount,omitempty"`
	FailedCount     int               `json:"failedCount,omitempty"`
	Sessions        []SessionConfig   `json:"sessions,omitempty"`
}

type sessionEntry struct {
	expires time.Time
}

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

const stickyTarget = 3

var (
	accounts      []*accountState
	configMu      sync.Mutex
	activeBatch   [3]*accountState
	standbyQueue  []*accountState
	currentSlot   int
	stickyCount   int
	rotationMu    sync.Mutex
	bootReady     = make(chan struct{})
	sessionsMu    sync.RWMutex
	sessions      = map[string]sessionEntry{}
	cfgPassword   string
	rtkEnabled    = true
	rtkMu         sync.RWMutex
	analyticsData *analytics.Analytics
)

func (a *accountState) getCreds() (kiro.Credentials, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.accessToken == "" || time.Now().Add(5*time.Minute).After(a.expiry) {
		var result *kiro.TokenResult
		var err error
		for i := 0; i < 3; i++ {
			result, err = kiro.RefreshToken(a.cfg.RefreshToken, kiro.ProviderSpecificData{
				AuthMethod: a.cfg.AuthMethod,
				ProfileArn: a.cfg.ProfileArn,
			})
			if err == nil {
				break
			}
			if err.Error() == "upstream returned 403 Forbidden" {
				return kiro.Credentials{}, err
			}
			log.Printf("refresh retry %d [%s]: %v", i+1, a.cfg.Label, err)
			time.Sleep(time.Duration(i+1) * time.Second)
		}
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
	}
	return kiro.Credentials{
		AccessToken:  a.accessToken,
		RefreshToken: a.cfg.RefreshToken,
		ProfileArn:   a.cfg.ProfileArn,
		PSD:          kiro.ProviderSpecificData{AuthMethod: a.cfg.AuthMethod, ProfileArn: a.cfg.ProfileArn},
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

	result, err := kiro.RefreshToken(a.cfg.RefreshToken, kiro.ProviderSpecificData{
		AuthMethod: a.cfg.AuthMethod,
		ProfileArn: a.cfg.ProfileArn,
	})
	if err != nil {
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

	// Build standby queue from all non-suspended, non-exhausted accounts
	standbyQueue = nil
	for _, a := range accounts {
		if a.available() {
			standbyQueue = append(standbyQueue, a)
		}
	}

	// Fill activeBatch[0:3] from standby
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

	// Try current slot with stickiness
	acc := activeBatch[currentSlot]
	if acc != nil && acc.available() {
		if stickyCount < stickyTarget {
			stickyCount++
			return acc
		}
		// sticky target reached → rotate
		currentSlot = (currentSlot + 1) % 3
		stickyCount = 0
	}

	// Scan remaining slots for available account
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

func saveConfig() {
	configMu.Lock()
	defer configMu.Unlock()
	rtkMu.RLock()
	rtkVal := rtkEnabled
	rtkMu.RUnlock()
	cfg := Config{
		Password:   cfgPassword,
		RTKEnabled: rtkVal,
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

	// Only override rtkEnabled from config if explicitly set
	var raw map[string]any
	json.Unmarshal(data, &raw)
	if _, ok := raw["rtkEnabled"]; ok {
		rtkEnabled = cfg.RTKEnabled
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

	// Restore activeBatch from persisted state
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
			creds, err := a.getCreds()
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

func main() {
	analyticsData = analytics.New()
	loadConfig()
	kiro.GlobalTokenCallback = func(inputTokens, outputTokens int64) {
		if analyticsData != nil {
			analyticsData.AddTokens(inputTokens, outputTokens)
		}
	}
	startTokenRefreshLoop()
	startResetWatcher()
	startMetricsCollector()

	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Post("/api/login", handleLogin)
	r.Get("/api/verify", handleVerify)
	r.Post("/v1/chat/completions", handleChatCompletions)

	r.Group(func(r chi.Router) {
		r.Use(authMiddleware)
		r.Get("/status", handleStatus)
		r.Get("/api/rtk", handleRTK)
		r.Post("/api/rtk", handleRTK)
		r.Post("/accounts/reset", handleReset)
		r.Post("/auth/kiro/refresh", handleKiroRefresh)
		r.Get("/api/analytics/metrics", handleMetrics)
		r.Get("/api/analytics/logs", handleLogs)
		r.Delete("/api/analytics/logs", handleClearLogs)
	})

	serveSPA(r, distFS, "web/dist")

	port := os.Getenv("PORT")
	if port == "" {
		port = "20180"
	}
	addr := "localhost:" + port
	log.Println("listening on http://" + addr)
	log.Fatal(http.ListenAndServe(":"+port, r))
}

func authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := ""
		if c, err := r.Cookie("session"); err == nil {
			token = c.Value
		}
		if token == "" {
			token = r.Header.Get("Authorization")
			if strings.HasPrefix(token, "Bearer ") {
				token = token[7:]
			}
		}
		sessionsMu.RLock()
		s, ok := sessions[token]
		if ok && time.Now().After(s.expires) {
			delete(sessions, token)
			ok = false
		}
		sessionsMu.RUnlock()
		if !ok {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func handleLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid body"}`, http.StatusBadRequest)
		return
	}
	if cfgPassword == "" || body.Password != cfgPassword {
		http.Error(w, `{"error":"wrong password"}`, http.StatusUnauthorized)
		return
	}
	b := make([]byte, 32)
	rand.Read(b)
	token := hex.EncodeToString(b)
	expires := time.Now().Add(7 * 24 * time.Hour)

	sessionsMu.Lock()
	sessions[token] = sessionEntry{expires: expires}
	sessionsMu.Unlock()
	
	go saveConfig()

	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  expires,
	})
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"token": token})
}

func handleVerify(w http.ResponseWriter, r *http.Request) {
	token := ""
	if c, err := r.Cookie("session"); err == nil {
		token = c.Value
	}
	if token == "" {
		token = r.Header.Get("Authorization")
		if strings.HasPrefix(token, "Bearer ") {
			token = token[7:]
		}
	}
	sessionsMu.RLock()
	s, ok := sessions[token]
	if ok && time.Now().After(s.expires) {
		delete(sessions, token)
		ok = false
	}
	sessionsMu.RUnlock()
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]any{"ok": false})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

func handleRTK(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		rtkMu.RLock()
		enabled := rtkEnabled
		rtkMu.RUnlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"rtkEnabled": enabled})

	case http.MethodPost:
		var body struct {
			RTKEnabled *bool `json:"rtkEnabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, `{"error":"invalid body"}`, http.StatusBadRequest)
			return
		}
		if body.RTKEnabled == nil {
			// Toggle if no explicit value
			rtkMu.Lock()
			rtkEnabled = !rtkEnabled
			newVal := rtkEnabled
			rtkMu.Unlock()
			go saveConfig()
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"rtkEnabled": newVal})
		} else {
			rtkMu.Lock()
			rtkEnabled = *body.RTKEnabled
			rtkMu.Unlock()
			go saveConfig()
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"rtkEnabled": *body.RTKEnabled})
		}

	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	<-bootReady
	
	if analyticsData != nil {
		analyticsData.IncrementRequests()
	}

	var req kiro.ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, "invalid JSON body", "invalid_request_error", http.StatusBadRequest)
		return
	}
	if req.Model == "" {
		writeJSONError(w, "missing model", "invalid_request_error", http.StatusBadRequest)
		return
	}
	if len(req.Messages) == 0 {
		writeJSONError(w, "missing messages", "invalid_request_error", http.StatusBadRequest)
		return
	}

	conversationID := r.Header.Get("X-Session-Id")
	if conversationID == "" {
		conversationID = r.Header.Get("X-Conversation-Id")
	}
	connectionID := r.Header.Get("X-Connection-Id")

	// Client disconnect detection (streamHandler.js createDisconnectAwareStream)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go func() {
		<-r.Context().Done()
		cancel()
	}()

	// SSE heartbeat (sseConstants.js style keepalive)
	heartbeatStop := make(chan struct{})
	defer close(heartbeatStop)
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				select {
				case <-heartbeatStop:
					return
				default:
				}
				io.WriteString(w, ": heartbeat\n\n")
				if flusher, ok := w.(http.Flusher); ok {
					flusher.Flush()
				}
			case <-heartbeatStop:
				return
			}
		}
	}()

	exhaustRetries := 0
	credsErrCount := 0
	resetDone := false
	for {
		acc := pickAccount()
		if acc == nil {
			if !resetDone {
				resetDone = true
				log.Println("all accounts in pool exhausted, resetting and retrying once")
				for _, a := range accounts {
					if !a.suspended {
						a.reset()
					}
				}
				fillActiveBatch()
				exhaustRetries = 0
				credsErrCount = 0
				continue
			}
			writeJSONError(w, "all accounts exhausted", "service_unavailable", http.StatusServiceUnavailable)
			return
		}

		creds, err := acc.getCreds()
		if err != nil {
			credsErrCount++
			if credsErrCount >= 3 {
				log.Printf("creds errors on all accounts, giving up")
				writeJSONError(w, "credentials error on all accounts", "auth_error", http.StatusInternalServerError)
				return
			}
			log.Printf("creds error [%s]: %v", acc.cfg.Label, err)
			// Rotate slot so next pickAccount skips this account
			rotationMu.Lock()
			currentSlot = (currentSlot + 1) % 3
			rotationMu.Unlock()
			continue
		}
		credsErrCount = 0
		

		acc.consume()
		if os.Getenv("KIRO_INTEGRITY_CHECK") == "true" {
			err = kiro.ExecuteWithIntegrityCheck(ctx, creds, req, w, conversationID, connectionID, rtkEnabled)
		} else {
			err = kiro.Execute(ctx, creds, req, w, conversationID, connectionID, rtkEnabled)
		}
		if err == nil {
			return
		}

		if errors.Is(err, kiro.ErrExhausted) {
			log.Printf("account exhausted [%s]", acc.cfg.Label)
			acc.markExhausted()
			// Refill this slot from standby, reset sticky count for replacement
			rotationMu.Lock()
			replaced := false
			for i := 0; i < 3; i++ {
				if activeBatch[i] == acc {
					if len(standbyQueue) > 0 {
						activeBatch[i] = standbyQueue[0]
						standbyQueue = standbyQueue[1:]
						replaced = true
					} else {
						activeBatch[i] = nil
					}
					stickyCount = 0
					break
				}
			}
			rotationMu.Unlock()
			go saveConfig()
			if replaced {
				exhaustRetries = 0 // reset — we got a fresh account from standby
			} else {
				exhaustRetries++
				if exhaustRetries >= 3 {
					log.Println("exhausted 3 accounts and no standby left, giving up")
					writeJSONError(w, "all accounts exhausted", "exhausted", http.StatusPaymentRequired)
					return
				}
			}
			continue
		}

		if errors.Is(err, kiro.ErrSuspended) {
			log.Printf("account suspended [%s], skipping permanently", acc.cfg.Label)
			acc.markSuspended()
			go saveConfig()
			continue
		}

		log.Printf("execute error [%s]: %v", acc.cfg.Label, err)
		if analyticsData != nil {
			analyticsData.IncrementFailed()
		}
		return
	}
}

func handleStatus(w http.ResponseWriter, r *http.Request) {
	type entry struct {
		ID         string `json:"id"`
		Label      string `json:"label"`
		Remaining  int    `json:"remaining"`
		Exhausted  bool   `json:"exhausted"`
		Suspended  bool   `json:"suspended"`
		ResetAt    string `json:"resetAt"`
		HasToken   bool   `json:"hasToken"`
		InPool     bool   `json:"inPool"`
		AuthMethod string `json:"authMethod,omitempty"`
		Region     string `json:"region,omitempty"`
	}
	rotationMu.Lock()
	batchSet := make(map[*accountState]bool)
	for i := 0; i < 3; i++ {
		if activeBatch[i] != nil {
			batchSet[activeBatch[i]] = true
		}
	}
	rotationMu.Unlock()

	var out []entry
	for _, a := range accounts {
		a.mu.Lock()
		out = append(out, entry{
			ID:         a.cfg.ID,
			Label:      a.cfg.Label,
			Remaining:  a.remaining,
			Exhausted:  a.exhausted,
			Suspended:  a.suspended,
			ResetAt:    a.cfg.ResetAt,
			HasToken:   a.accessToken != "",
			InPool:     batchSet[a],
			AuthMethod: a.cfg.AuthMethod,
			Region:     a.cfg.ProfileArn,
		})
		a.mu.Unlock()
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

func handleReset(w http.ResponseWriter, r *http.Request) {
	for _, a := range accounts {
		if !a.suspended {
			a.reset()
		}
	}
	fillActiveBatch()
	go saveConfig()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"reset": len(accounts)})
}

func writeJSONError(w http.ResponseWriter, msg, errType string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{"message": msg, "type": errType},
	})
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	Region       string `json:"region"`
}

func handleKiroRefresh(w http.ResponseWriter, r *http.Request) {
	var body refreshRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.RefreshToken == "" {
		http.Error(w, "missing refresh_token", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if body.ClientID != "" && body.ClientSecret != "" {
		region := body.Region
		if region == "" {
			region = "us-east-1"
		}
		proxyJSON(w, "https://oidc."+region+".amazonaws.com/token", mustMarshal(map[string]string{
			"clientId": body.ClientID, "clientSecret": body.ClientSecret,
			"refreshToken": body.RefreshToken, "grantType": "refresh_token",
		}))
		return
	}
	proxyJSON(w, kiroAuthService+"/refreshToken", mustMarshal(map[string]string{"refreshToken": body.RefreshToken}))
}

func proxyJSON(w http.ResponseWriter, url string, payload []byte) {
	resp, err := http.Post(url, "application/json", bytes.NewReader(payload))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		http.Error(w, resp.Status, resp.StatusCode)
		return
	}
	var data map[string]any
	json.NewDecoder(resp.Body).Decode(&data)
	json.NewEncoder(w).Encode(data)
}

func mustMarshal(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func serveSPA(r chi.Router, embeddedFS embed.FS, targetDir string) {
	contentFS, err := fs.Sub(embeddedFS, targetDir)
	if err != nil {
		log.Fatal(err)
	}

	fileServer := http.FileServer(http.FS(contentFS))

	r.Get("/*", func(w http.ResponseWriter, r *http.Request) {
		filePath := path.Clean(r.URL.Path)
		filePath = strings.TrimPrefix(filePath, "/")

		if filePath == "" {
			if acceptsBrotli(r) {
				if f, err := contentFS.Open("index.html.br"); err == nil {
					f.Close()
					w.Header().Set("Content-Encoding", "br")
					w.Header().Set("Content-Type", "text/html")
					http.ServeFileFS(w, r, contentFS, "index.html.br")
					return
				}
			}
			fileServer.ServeHTTP(w, r)
			return
		}

		// Try brotli for existing files
		if acceptsBrotli(r) {
			if f, err := contentFS.Open(filePath + ".br"); err == nil {
				f.Close()
				w.Header().Set("Content-Encoding", "br")
				w.Header().Set("Content-Type", mimeTypeByExt(filePath))
				http.ServeFileFS(w, r, contentFS, filePath+".br")
				return
			}
		}

		f, err := contentFS.Open(filePath)
		if err == nil {
			f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}

		// SPA fallback — serve index.html
		if acceptsBrotli(r) {
			if f, err := contentFS.Open("index.html.br"); err == nil {
				f.Close()
				w.Header().Set("Content-Encoding", "br")
				w.Header().Set("Content-Type", "text/html")
				http.ServeFileFS(w, r, contentFS, "index.html.br")
				return
			}
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		http.ServeFileFS(w, r, contentFS, "index.html")
	})
}

func mimeTypeByExt(name string) string {
	switch {
	case strings.HasSuffix(name, ".html"):
		return "text/html"
	case strings.HasSuffix(name, ".css"):
		return "text/css"
	case strings.HasSuffix(name, ".js"):
		return "application/javascript"
	case strings.HasSuffix(name, ".svg"):
		return "image/svg+xml"
	case strings.HasSuffix(name, ".json"):
		return "application/json"
	case strings.HasSuffix(name, ".png"):
		return "image/png"
	case strings.HasSuffix(name, ".ico"):
		return "image/x-icon"
	default:
		return ""
	}
}

func acceptsBrotli(r *http.Request) bool {
	for _, v := range r.Header.Values("Accept-Encoding") {
		for _, e := range strings.Split(v, ",") {
			if strings.TrimSpace(e) == "br" {
				return true
			}
		}
	}
	return false
}

func startMetricsCollector() {
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for range ticker.C {
			active := 0
			exhausted := 0
			suspended := 0
			totalRemaining := 0

			for _, a := range accounts {
				a.mu.Lock()
				if a.suspended {
					suspended++
				} else if a.exhausted {
					exhausted++
				} else {
					active++
				}
				totalRemaining += a.remaining
				a.mu.Unlock()
			}

			rotationMu.Lock()
			poolSize := 0
			for i := 0; i < 3; i++ {
				if activeBatch[i] != nil {
					poolSize++
				}
			}
			rotationMu.Unlock()

			analyticsData.RecordMetric(active, exhausted, suspended, totalRemaining, poolSize)
		}
	}()
}

func handleMetrics(w http.ResponseWriter, r *http.Request) {
	metrics := analyticsData.GetMetrics()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(metrics)
}

func handleLogs(w http.ResponseWriter, r *http.Request) {
	logs := analyticsData.GetLogs()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(logs)
}

func handleClearLogs(w http.ResponseWriter, r *http.Request) {
	analyticsData.ClearLogs()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "cleared"})
}


