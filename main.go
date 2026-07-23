package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"farouter/internal/kiro"
)

const (
	kiroAuthService = "https://prod.us-east-1.auth.desktop.kiro.dev"
	tokenLimit      = 60
	stickyLimit     = 3
	configPath      = "config.json"
)

type AccountConfig struct {
	ID           string `json:"id"`
	Label        string `json:"label"`
	RefreshToken string `json:"refreshToken"`
	ProfileArn   string `json:"profileArn"`
	AuthMethod   string `json:"authMethod"`
	Exhausted    bool   `json:"exhausted,omitempty"`
	Suspended    bool   `json:"suspended,omitempty"`
	ResetAt      string `json:"resetAt,omitempty"`
}

type Config struct {
	StickyID string          `json:"stickyId,omitempty"`
	Accounts []AccountConfig `json:"accounts"`
}

type accountState struct {
	cfg         AccountConfig
	accessToken string
	expiry      time.Time
	remaining   int
	exhausted   bool
	suspended   bool
	mu          sync.Mutex
}

var (
	accounts     []*accountState
	configMu     sync.Mutex
	cursor       atomic.Int64
	stickyIdx    atomic.Int64
	stickyCount  atomic.Int64
	bootReady    = make(chan struct{})
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
			// 403 = token revoked/invalid — no point retrying
			if err.Error() == "upstream returned 403 Forbidden" {
				return kiro.Credentials{}, err
			}
			log.Printf("refresh retry %d [%s]: %v", i+1, a.cfg.Label, err)
			time.Sleep(time.Duration(i+1) * time.Second)
		}
		if err != nil {
			return kiro.Credentials{}, err
		}
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

func (a *accountState) markSuspended() {
	a.mu.Lock()
	a.suspended = true
	a.exhausted = true
	a.remaining = 0
	a.cfg.Suspended = true
	a.cfg.Exhausted = true
	a.mu.Unlock()
}

func (a *accountState) markExhausted() {
	a.mu.Lock()
	a.exhausted = true
	a.remaining = 0
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

// parseResetAt parses resetAt string (unix timestamp or RFC3339)
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

func saveConfig() {
	configMu.Lock()
	defer configMu.Unlock()
	cfg := Config{}
	idx := stickyIdx.Load()
	if idx >= 0 && idx < int64(len(accounts)) {
		cfg.StickyID = accounts[idx].cfg.ID
	}
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

func pickAccount(exclude map[*accountState]bool) *accountState {
	n := int64(len(accounts))
	if n == 0 {
		return nil
	}

	// Try sticky account first
	idx := stickyIdx.Load()
	if idx < n {
		acc := accounts[idx]
		if !exclude[acc] && acc.available() {
			cnt := stickyCount.Add(1)
			if cnt <= int64(stickyLimit) {
				return acc
			}
			// Sticky limit reached — advance to next
			stickyCount.Store(1)
			next := (idx + 1) % n
			stickyIdx.Store(next)
			return accounts[next]
		}
	}

	// Sticky account unavailable — find next available
	for i := int64(0); i < n; i++ {
		next := (idx + 1 + i) % n
		acc := accounts[next]
		if !exclude[acc] && acc.available() {
			stickyIdx.Store(next)
			stickyCount.Store(1)
			return acc
		}
	}
	return nil
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
	now := time.Now()
	for _, a := range cfg.Accounts {
		if a.RefreshToken == "" {
			continue
		}
		state := &accountState{cfg: a, remaining: tokenLimit}
		// Restore exhausted from config, but check if reset date has passed
		if a.Exhausted {
			resetTime := parseResetAt(a.ResetAt)
			if !resetTime.IsZero() && now.After(resetTime) {
				// Reset date passed — mark active again
				state.exhausted = false
				state.cfg.Exhausted = false
				state.cfg.ResetAt = ""
			} else {
				state.exhausted = true
				state.remaining = 0
			}
		}
		accounts = append(accounts, state)
	}
	log.Printf("loaded %d kiro accounts", len(accounts))

	// Restore stickyId from config
	if cfg.StickyID != "" {
		for i, a := range accounts {
			if a.cfg.ID == cfg.StickyID && a.available() {
				stickyIdx.Store(int64(i))
				break
			}
		}
	}

	var wg sync.WaitGroup
	for _, a := range accounts {
		wg.Add(1)
		go func(acc *accountState) {
			defer wg.Done()
			// Skip quota check if already known exhausted
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

	// Persist exhausted state to config
	saveConfig()

	active := 0
	for _, a := range accounts {
		a.mu.Lock()
		if !a.exhausted {
			active++
		}
		a.mu.Unlock()
	}
	log.Printf("boot done: %d/%d accounts active", active, len(accounts))
	close(bootReady)
}

func main() {
	go loadConfig()

	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("farouter ok"))
	})
	r.Get("/status", handleStatus)
	r.Post("/accounts/reset", handleReset)
	r.Post("/auth/kiro/refresh", handleKiroRefresh)
	r.Post("/v1/chat/completions", handleChatCompletions)

	port := os.Getenv("PORT")
	if port == "" {
		port = "3000"
	}
	log.Println("listening on :" + port)
	log.Fatal(http.ListenAndServe(":"+port, r))
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

	tried := make(map[*accountState]bool)
	resetDone := false
	for {
		acc := pickAccount(tried)
		if acc == nil {
			if !resetDone {
				resetDone = true
				log.Println("all accounts exhausted, resetting and retrying once")
				for _, a := range accounts {
					a.reset()
				}
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
		err = kiro.Execute(creds, req, w, conversationID)
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
		ResetAt   string `json:"resetAt"`
		HasToken  bool   `json:"hasToken"`
	}
	var out []entry
	for _, a := range accounts {
		a.mu.Lock()
		out = append(out, entry{
			ID:        a.cfg.ID,
			Label:     a.cfg.Label,
			Remaining: a.remaining,
			Exhausted: a.exhausted,
			ResetAt:   a.cfg.ResetAt,
			HasToken:  a.accessToken != "",
		})
		a.mu.Unlock()
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

func handleReset(w http.ResponseWriter, r *http.Request) {
	for _, a := range accounts {
		a.reset()
	}
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
