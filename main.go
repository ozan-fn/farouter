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

type Config struct {
	Password       string          `json:"password,omitempty"`
	ActiveBatchIds []string        `json:"activeBatchIds,omitempty"`
	CurrentSlot    int             `json:"currentSlot,omitempty"`
	StickyCount    int             `json:"stickyCount,omitempty"`
	Accounts       []AccountConfig `json:"accounts"`
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

var (
	accounts      []*accountState
	configMu      sync.Mutex
	activeBatch   [3]*accountState
	standbyQueue  []*accountState
	currentSlot   int
	stickyCount   int
	rotationMu    sync.Mutex
	bootReady     = make(chan struct{})

	sessionsMu sync.RWMutex
	sessions   = map[string]sessionEntry{}

	cfgPassword string
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
			return kiro.Credentials{}, err
		}
		a.failedRefresh = 0
		a.accessToken = result.AccessToken
		a.expiry = time.Now().Add(time.Duration(result.ExpiresIn) * time.Second)
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
	a.mu.Unlock()
}

func (a *accountState) markSuspended() {
	a.mu.Lock()
	a.suspended = true
	a.exhausted = true
	a.remaining = 0
	a.cfg.Suspended = true
	a.cfg.Exhausted = true
	a.mu.Unlock()
}

func (a *accountState) reset() {
	a.mu.Lock()
	a.remaining = tokenLimit
	a.exhausted = false
	a.mu.Unlock()
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
		for range time.Tick(24 * time.Hour) {
			for _, a := range accounts {
				a.refreshTokenIfNeeded()
			}
			saveConfig()
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

func refillSlot(slot int) {
	rotationMu.Lock()
	defer rotationMu.Unlock()

	if len(standbyQueue) == 0 {
		activeBatch[slot] = nil
		saveConfig()
		return
	}

	activeBatch[slot] = standbyQueue[0]
	standbyQueue = standbyQueue[1:]
	saveConfig()
}

func pickAccount(exclude map[*accountState]bool) *accountState {
	rotationMu.Lock()
	defer rotationMu.Unlock()

	// Try current slot
	maxAttempts := 3
	for attempt := 0; attempt < maxAttempts; attempt++ {
		acc := activeBatch[currentSlot]
		if acc != nil && !exclude[acc] && acc.available() {
			return acc
		}
		// Move to next slot
		currentSlot = (currentSlot + 1) % 3
	}

	// All slots exhausted/excluded
	return nil
}

func saveConfig() {
	configMu.Lock()
	defer configMu.Unlock()
	cfg := Config{}
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
	cfgPassword = cfg.Password
	now := time.Now()
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

	var wg sync.WaitGroup
	for _, a := range accounts {
		wg.Add(1)
		go func(acc *accountState) {
			defer wg.Done()
			acc.mu.Lock()
			alreadyExhausted := acc.exhausted
			acc.mu.Unlock()
			if alreadyExhausted {
				return
			}
			creds, err := acc.getCreds()
			if err != nil {
				log.Printf("warn: boot refresh failed [%s]: %v", acc.cfg.Label, err)
				return
			}
			quota, err := kiro.FetchQuota(creds.AccessToken, acc.cfg.ProfileArn, acc.cfg.AuthMethod)
			if err != nil {
				log.Printf("warn: quota check failed [%s]: %v", acc.cfg.Label, err)
				return
			}
			acc.mu.Lock()
			if quota.Limit > 0 {
				acc.remaining = quota.Remaining
			}
			if quota.Exhausted {
				acc.exhausted = true
				acc.remaining = 0
				acc.cfg.Exhausted = true
				acc.cfg.ResetAt = quota.ResetAt
				log.Printf("exhausted at boot [%s]: %d/%d reset=%s", acc.cfg.Label, quota.Used, quota.Limit, quota.ResetAt)
			}
			acc.mu.Unlock()
		}(a)
	}
	wg.Wait()

	fillActiveBatch()
	saveConfig()

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

	log.Printf("boot done: %d active, %d exhausted, %d suspended, %d invalid token, pool=%d",
		active, exhausted, suspended, failedRefresh, batchSize)
	close(bootReady)
}

func main() {
	go loadConfig()
	startTokenRefreshLoop()

	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Post("/api/login", handleLogin)
	r.Get("/api/verify", handleVerify)
	r.Post("/v1/chat/completions", handleChatCompletions)

	r.Group(func(r chi.Router) {
		r.Use(authMiddleware)
		r.Get("/status", handleStatus)
		r.Post("/accounts/reset", handleReset)
		r.Post("/auth/kiro/refresh", handleKiroRefresh)
	})

	serveSPA(r, distFS, "web/dist")

	port := os.Getenv("PORT")
	if port == "" {
		port = "20180"
	}
	addr := "localhost:" + port
	log.Println("listening on http://" + addr)
	log.Fatal(http.ListenAndServe(addr, r))
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

	sessionsMu.Lock()
	sessions[token] = sessionEntry{expires: time.Now().Add(24 * time.Hour)}
	sessionsMu.Unlock()

	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   false,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   86400,
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

func handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	<-bootReady

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

	tried := make(map[*accountState]bool)
	resetDone := false
	for {
		acc := pickAccount(tried)
		if acc == nil {
			if !resetDone {
				resetDone = true
				log.Println("all accounts exhausted, resetting and retrying once")
				for _, a := range accounts {
					if !a.suspended {
						a.reset()
					}
				}
				fillActiveBatch()
				tried = make(map[*accountState]bool)
				continue
			}
			writeJSONError(w, "all accounts exhausted", "service_unavailable", http.StatusServiceUnavailable)
			return
		}

		creds, err := acc.getCreds()
		if err != nil {
			tried[acc] = true
			log.Printf("creds error [%s]: %v", acc.cfg.Label, err)
			continue
		}

		acc.consume()
		if os.Getenv("KIRO_INTEGRITY_CHECK") == "true" {
			err = kiro.ExecuteWithIntegrityCheck(ctx, creds, req, w, conversationID)
		} else {
			err = kiro.Execute(ctx, creds, req, w, conversationID)
		}
		if err == nil {
			return
		}

		if errors.Is(err, kiro.ErrExhausted) {
			log.Printf("account exhausted [%s], retrying next", acc.cfg.Label)
			acc.markExhausted()
			tried[acc] = true
			go saveConfig()
			continue
		}

		if errors.Is(err, kiro.ErrSuspended) {
			log.Printf("account suspended [%s], skipping permanently", acc.cfg.Label)
			acc.markSuspended()
			tried[acc] = true
			go saveConfig()
			continue
		}

		log.Printf("execute error [%s]: %v", acc.cfg.Label, err)
		return
	}
}

func handleStatus(w http.ResponseWriter, r *http.Request) {
	type entry struct {
		ID        string `json:"id"`
		Label     string `json:"label"`
		Remaining int    `json:"remaining"`
		Exhausted bool   `json:"exhausted"`
		Suspended bool   `json:"suspended"`
		ResetAt   string `json:"resetAt"`
		HasToken  bool   `json:"hasToken"`
		InPool    bool   `json:"inPool"`
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
			ID:        a.cfg.ID,
			Label:     a.cfg.Label,
			Remaining: a.remaining,
			Exhausted: a.exhausted,
			Suspended: a.suspended,
			ResetAt:   a.cfg.ResetAt,
			HasToken:  a.accessToken != "",
			InPool:    batchSet[a],
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
