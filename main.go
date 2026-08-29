package main

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"farouter/internal/analytics"
	"farouter/internal/kiro"
	"farouter/internal/opencode"
	"farouter/internal/rtk"

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
	ID                 string  `json:"id"`
	Label              string  `json:"label"`
	RefreshToken       string  `json:"refreshToken"`
	ProfileArn         string  `json:"profileArn"`
	AuthMethod         string  `json:"authMethod"`
	MachineId          string  `json:"machineId,omitempty"`
	KiroToolCallRepair *bool   `json:"kiroToolCallRepair,omitempty"`
	Exhausted          bool    `json:"exhausted,omitempty"`
	Suspended          bool    `json:"suspended,omitempty"`
	ResetAt            string  `json:"resetAt,omitempty"`
	LastRefreshedAt    string  `json:"lastRefreshedAt,omitempty"`
}

type SessionConfig struct {
	Token   string `json:"token"`
	Expires string `json:"expires"`
}

type Config struct {
	Password        string          `json:"password,omitempty"`
	ActiveBatchIds  []string        `json:"activeBatchIds,omitempty"`
	CurrentSlot     int             `json:"currentSlot,omitempty"`
	StickyCount     int             `json:"stickyCount,omitempty"`
	RTKEnabled      bool            `json:"rtkEnabled"`
	KiroThrottleMs  int             `json:"kiroThrottleMs,omitempty"`
	CavemanLevel    string          `json:"cavemanLevel,omitempty"`
	PonytailLevel   string          `json:"ponytailLevel,omitempty"`
	Accounts        []AccountConfig `json:"accounts"`
	TokensUsed      int64           `json:"tokensUsed,omitempty"`
	TokensGenerated int64           `json:"tokensGenerated,omitempty"`
	RequestCount    int             `json:"requestCount,omitempty"`
	FailedCount     int             `json:"failedCount,omitempty"`
	Sessions        []SessionConfig `json:"sessions,omitempty"`
}

type sessionEntry struct {
	expires time.Time
}

var (
	analyticsData *analytics.Analytics
	sessionsMu    sync.RWMutex
	sessions      = map[string]sessionEntry{}
	cfgPassword   string
	rtkEnabled    = true
	rtkMu         sync.RWMutex
	cavemanLevel  string
	ponytailLevel string
	tokenSaverMu  sync.RWMutex
	startTime     = time.Now()
)

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
	r.Get("/v1/models", handleModels)

	opencode.InitPool()
	r.Post("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)

		rtkMu.RLock()
		rtkOn := rtkEnabled
		rtkMu.RUnlock()
		if rtkOn {
			var bodyMap map[string]any
			if json.Unmarshal(body, &bodyMap) == nil {
				stats := &rtk.Stats{}
				if rtk.CompressOpenAIMessages(bodyMap, stats) {
					if compressed, err := json.Marshal(bodyMap); err == nil {
						body = compressed
						if line := rtk.FormatRtkLog(stats); line != "" {
							log.Print(line)
						}
					}
				}
			}
		}

		r.Body = io.NopCloser(bytes.NewReader(body))

		var modelCheck struct {
			Model string `json:"model"`
		}
		json.Unmarshal(body, &modelCheck)

		if strings.Contains(strings.ToLower(modelCheck.Model), "deepseek") {
			opencode.Handle(w, r)
			return
		}

		handleChatCompletions(w, r)
	})

	r.Group(func(r chi.Router) {
		r.Use(authMiddleware)
		r.Get("/status", handleStatus)
		r.Get("/api/rtk", handleRTK)
		r.Post("/api/rtk", handleRTK)
		r.Get("/api/caveman", handleCaveman)
		r.Post("/api/caveman", handleCaveman)
		r.Get("/api/ponytail", handlePonytail)
		r.Post("/api/ponytail", handlePonytail)
		r.Post("/accounts/reset", handleReset)
		r.Post("/auth/kiro/refresh", handleKiroRefresh)
		r.Get("/api/analytics/metrics", handleMetrics)
		r.Get("/api/analytics/logs", handleLogs)
		r.Delete("/api/analytics/logs", handleClearLogs)
		r.Get("/api/uptime", func(w http.ResponseWriter, r *http.Request) {
			d := time.Since(startTime)
			days := int(d.Hours()) / 24
			hours := int(d.Hours()) % 24
			mins := int(d.Minutes()) % 60
			var s string
			if days > 0 {
				s = fmt.Sprintf("%dd %dh %dm", days, hours, mins)
			} else if hours > 0 {
				s = fmt.Sprintf("%dh %dm", hours, mins)
			} else {
				s = fmt.Sprintf("%dm", mins)
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"uptime": s, "uptimeSeconds": int(d.Seconds())})
		})
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
