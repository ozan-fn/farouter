//go:build online

// Online tests hit the real Kiro upstream. Run explicitly:
//
//	go test -tags online -run TestOnline ./internal/server/ -v
//
// They need a valid config.json with at least one refresh token, and they
// consume real quota. Accounts are tried bottom-up so the top of the pool
// stays untouched.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"farouter/internal/kiro"

	"github.com/google/uuid"
)

var onlineModels = []string{"kr/auto", "kr/auto-thinking", "kr/claude-sonnet-4.5"}

// findOnlineConfig locates config.json regardless of the test working
// directory (package dir under `go test`, repo root for the built binary).
func findOnlineConfig() string {
	for _, p := range []string{configPath, "../../" + configPath, "../../../" + configPath} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// onlineCandidates returns usable accounts, starting from the bottom of
// config.json and walking up.
func onlineCandidates(t *testing.T) []*accountState {
	t.Helper()
	configFile := findOnlineConfig()
	if configFile == "" {
		t.Skipf("online test needs %s (run from repo root or internal/server)", configPath)
	}
	data, err := os.ReadFile(configFile)
	if err != nil {
		t.Skipf("online test needs %s: %v", configFile, err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse %s: %v", configPath, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var out []*accountState
	for i := len(cfg.Accounts) - 1; i >= 0; i-- {
		acc := cfg.Accounts[i]
		if acc.RefreshToken == "" || acc.Suspended || acc.Exhausted {
			continue
		}
		if acc.MachineId == "" {
			acc.MachineId = uuid.New().String()
		}
		a := &accountState{cfg: acc, remaining: tokenLimit}
		if _, err := a.getCreds(ctx); err != nil {
			t.Logf("online: skip [%s]: %v", acc.Label, err)
			continue
		}
		out = append(out, a)
	}
	if len(out) == 0 {
		t.Skip("no usable kiro account in config.json")
	}
	t.Logf("online: %d candidate account(s), starting from bottom [%s]", len(out), out[0].cfg.Label)
	return out
}

// onlineGenerate runs one short chat request and returns the concatenated content.
func onlineGenerate(a *accountState, model string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	creds, err := a.getCreds(ctx)
	if err != nil {
		return "", err
	}
	req := kiro.ChatRequest{
		Model:    model,
		Messages: []kiro.Message{{Role: "user", Content: "Reply with exactly: OK"}},
		Stream:   true,
	}
	rec := httptest.NewRecorder()
	if err := kiro.Execute(ctx, creds, req, rec, "online-conv", "online-conn", false); err != nil {
		return "", err
	}
	body := rec.Body.String()
	if !strings.Contains(body, "data: [DONE]") {
		return "", fmt.Errorf("stream missing [DONE]: %s", body)
	}
	return onlineContent(body), nil
}

// onlineContent concatenates delta.content from OpenAI-style SSE data lines.
func onlineContent(body string) string {
	var sb strings.Builder
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data: ") || line == "data: [DONE]" {
			continue
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &chunk) != nil {
			continue
		}
		for _, c := range chunk.Choices {
			sb.WriteString(c.Delta.Content)
		}
	}
	return sb.String()
}

func TestOnline_ModelOutputs(t *testing.T) {
	candidates := onlineCandidates(t)

	// The bottom-most account that answers is reused for the later models, so
	// only the first model pays the cost of walking past dead accounts.
	var working *accountState

	for _, model := range onlineModels {
		t.Run(model, func(t *testing.T) {
			order := candidates
			if working != nil {
				order = append([]*accountState{working}, candidates...)
			}
			var lastErr error
			for _, a := range order {
				out, err := onlineGenerate(a, model)
				if err != nil {
					lastErr = err
					if errors.Is(err, kiro.ErrExhausted) || errors.Is(err, kiro.ErrSuspended) {
						t.Logf("%s: account [%s] %v, next account", model, a.cfg.Label, err)
						continue
					}
					break
				}
				out = strings.TrimSpace(out)
				if out == "" {
					lastErr = fmt.Errorf("empty output from [%s]", a.cfg.Label)
					continue
				}
				working = a
				t.Logf("%s [%s] -> %q", model, a.cfg.Label, out)
				return
			}
			t.Fatalf("%s: %v", model, lastErr)
		})
	}
}
