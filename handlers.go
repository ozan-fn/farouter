package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"farouter/internal/kiro"
)

func authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := ""
		if c, err := r.Cookie("session"); err == nil {
			token = c.Value
		}
		if token == "" {
			token = r.Header.Get("Authorization")
			token = strings.TrimPrefix(token, "Bearer ")
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
		token = strings.TrimPrefix(token, "Bearer ")
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

func handleCaveman(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		tokenSaverMu.RLock()
		level := cavemanLevel
		tokenSaverMu.RUnlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"cavemanLevel": level})

	case http.MethodPost:
		var body struct {
			Level *string `json:"level"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, `{"error":"invalid body"}`, http.StatusBadRequest)
			return
		}
		tokenSaverMu.Lock()
		if body.Level == nil {
			cavemanLevel = ""
		} else {
			cavemanLevel = *body.Level
		}
		level := cavemanLevel
		tokenSaverMu.Unlock()
		go saveConfig()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"cavemanLevel": level})

	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func handlePonytail(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		tokenSaverMu.RLock()
		level := ponytailLevel
		tokenSaverMu.RUnlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"ponytailLevel": level})

	case http.MethodPost:
		var body struct {
			Level *string `json:"level"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, `{"error":"invalid body"}`, http.StatusBadRequest)
			return
		}
		tokenSaverMu.Lock()
		if body.Level == nil {
			ponytailLevel = ""
		} else {
			ponytailLevel = *body.Level
		}
		level := ponytailLevel
		tokenSaverMu.Unlock()
		go saveConfig()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"ponytailLevel": level})

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

	tokenSaverMu.RLock()
	globalCaveman := cavemanLevel
	globalPonytail := ponytailLevel
	tokenSaverMu.RUnlock()
	if req.CavemanLevel == "" && globalCaveman != "" {
		req.CavemanLevel = globalCaveman
	}
	if req.PonytailLevel == "" && globalPonytail != "" {
		req.PonytailLevel = globalPonytail
	}

	conversationID := r.Header.Get("X-Session-Id")
	if conversationID == "" {
		conversationID = r.Header.Get("X-Conversation-Id")
	}
	connectionID := r.Header.Get("X-Connection-Id")

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go func() {
		<-r.Context().Done()
		cancel()
	}()

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

	credsErrCount := 0
	resetDone := false
	for attempt := 0; attempt < 10; attempt++ {
		acc := pickAccount()
		if acc == nil {
			if !resetDone {
				resetDone = true
				log.Println("all accounts in pool exhausted, refreshing pool")
				fillActiveBatch()
				credsErrCount = 0
				continue
			}
			writeJSONError(w, "all accounts exhausted", "service_unavailable", http.StatusServiceUnavailable)
			return
		}

		creds, err := acc.getCreds(ctx)
		if err != nil {
			credsErrCount++
			if credsErrCount >= 3 {
				log.Printf("creds errors on all accounts, giving up")
				writeJSONError(w, "credentials error on all accounts", "auth_error", http.StatusInternalServerError)
				return
			}
			log.Printf("creds error [%s]: %v", acc.cfg.Label, err)
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
			log.Printf("account exhausted [%s], replacing immediately", acc.cfg.Label)
			acc.markExhausted()

			replacement := getNextAvailableAccount()
			if replacement != nil {
				rotationMu.Lock()
				for i := 0; i < 3; i++ {
					if activeBatch[i] == acc {
						activeBatch[i] = replacement
						log.Printf("replaced exhausted %s with available %s", acc.cfg.Label, replacement.cfg.Label)
						stickyCount = 0
						break
					}
				}
				rotationMu.Unlock()
				go saveConfig()
				continue
			} else {
				log.Println("no available accounts remaining, request failed")
				writeJSONError(w, "all accounts exhausted", "exhausted", http.StatusPaymentRequired)
				return
			}
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

	writeJSONError(w, "all accounts exhausted after max retries", "service_unavailable", http.StatusServiceUnavailable)
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

func handleModels(w http.ResponseWriter, r *http.Request) {
	type modelObj struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		Created int64  `json:"created"`
		OwnedBy string `json:"owned_by"`
	}
	models := []string{
		"kr/auto",
		"kr/auto-thinking",
		"kr/claude-haiku-4.5",
		"kr/claude-haiku-4.5-thinking",
		"kr/claude-sonnet-4.5",
		"kr/claude-sonnet-4.5-thinking",
	}
	data := make([]modelObj, 0, len(models))
	for _, id := range models {
		data = append(data, modelObj{
			ID:      id,
			Object:  "model",
			Created: 0,
			OwnedBy: "kiro",
		})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"object": "list",
		"data":   data,
	})
}
