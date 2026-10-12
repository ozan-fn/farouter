//go:build online

// Integration test: drives the production /v1/chat/completions handler with a
// full client body — agent-style tools (edit / read / bash), a system message
// and sampling settings — for each advertised model, and asserts the streamed
// output matches the expected tool call.
//
// Uses ONE account (the first usable entry in config.json). No opencode, no
// proxy pool, plain Go.
//
//	go test -tags online -run TestIntegration ./internal/server/ -v
package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"farouter/internal/analytics"

	"github.com/google/uuid"
)

// integrationTools is an agent-style tool, the same shape an editor/agent client
// sends — the `edit` tool only, so the expected tool call is unambiguous.
const integrationTools = `[
	{
		"type": "function",
		"function": {
			"name": "edit",
			"description": "Edit a file by replacing an exact string with a new string",
			"parameters": {
				"type": "object",
				"properties": {
					"filePath": {"type": "string", "description": "Path of the file to edit"},
					"oldString": {"type": "string", "description": "Exact text to replace"},
					"newString": {"type": "string", "description": "Replacement text"}
				},
				"required": ["filePath", "oldString", "newString"]
			}
		}
	}
]`

// readTools declares the `read` tool used by the multi-turn body.
const readTools = `[
	{
		"type": "function",
		"function": {
			"name": "read",
			"description": "Read the contents of a file",
			"parameters": {
				"type": "object",
				"properties": {"filePath": {"type": "string"}},
				"required": ["filePath"]
			}
		}
	}
]`

// multiTurnBody exercises all four roles in one request: system, user,
// assistant (with tool_calls) and tool (with tool_call_id).
func multiTurnBody(model string) string {
	return fmt.Sprintf(`{
		"model": %q,
		"stream": true,
		"temperature": 0,
		"max_tokens": 200,
		"messages": [
			{"role": "system", "content": "You are a coding agent. Use the provided tools when asked."},
			{"role": "user", "content": "What is in internal/server/server.go? Use the read tool."},
			{"role": "assistant", "content": "", "tool_calls": [{"id": "call_1", "type": "function", "function": {"name": "read", "arguments": "{\"filePath\": \"internal/server/server.go\"}"}}]},
			{"role": "tool", "tool_call_id": "call_1", "content": "package server\n\nfunc Run(distFS embed.FS) {\n}\n\nfunc helper() {}\n"},
			{"role": "user", "content": "The file content is in the tool result above. Do not call any tool. Reply with just the name of the first function defined in that file."}
		],
		"tools": %s
	}`, model, readTools)
}

// integrationBody is what a client POSTs to /v1/chat/completions.
func integrationBody(model, prompt string) string {
	return fmt.Sprintf(`{
		"model": %q,
		"stream": true,
		"temperature": 0,
		"top_p": 1,
		"max_tokens": 300,
		"messages": [
			{"role": "system", "content": "You are a coding agent. Use the provided tools to modify files."},
			{"role": "user", "content": %q}
		],
		"tools": %s
	}`, model, prompt, integrationTools)
}

type itToolCall struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type itChunk struct {
	Choices []struct {
		Delta struct {
			Content          string       `json:"content"`
			ReasoningContent string       `json:"reasoning_content"`
			ToolCalls        []itToolCall `json:"tool_calls"`
		} `json:"delta"`
		FinishReason any `json:"finish_reason"`
	} `json:"choices"`
}

type itResult struct {
	content      string
	reasoning    string
	toolName     string
	toolArgs     string
	toolID       string
	toolType     string
	finishReason string
	sawDone      bool
}

// collectSSE folds the OpenAI SSE stream into the assembled completion.
func collectSSE(raw string) itResult {
	var r itResult
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "data: [DONE]" {
			r.sawDone = true
			continue
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var c itChunk
		if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &c) != nil {
			continue
		}
		for _, ch := range c.Choices {
			r.content += ch.Delta.Content
			r.reasoning += ch.Delta.ReasoningContent
			for _, tc := range ch.Delta.ToolCalls {
				if tc.Function.Name != "" {
					r.toolName = tc.Function.Name
				}
				if tc.ID != "" {
					r.toolID = tc.ID
				}
				if tc.Type != "" {
					r.toolType = tc.Type
				}
				r.toolArgs += tc.Function.Arguments
			}
			if s, ok := ch.FinishReason.(string); ok && s != "" {
				r.finishReason = s
			}
		}
	}
	return r
}

// onlineSingleAccount returns exactly one account: the first entry in
// config.json that is not flagged suspended/exhausted and has a refresh token.
func onlineSingleAccount(t *testing.T) *accountState {
	t.Helper()
	configFile := findOnlineConfig()
	if configFile == "" {
		t.Skipf("online test needs %s", configPath)
	}
	data, err := os.ReadFile(configFile)
	if err != nil {
		t.Skipf("read %s: %v", configFile, err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse %s: %v", configFile, err)
	}
	for _, acc := range cfg.Accounts {
		if acc.RefreshToken == "" || acc.Suspended || acc.Exhausted {
			continue
		}
		if acc.MachineId == "" {
			acc.MachineId = uuid.New().String()
		}
		t.Logf("using account: %s", acc.Label)
		return &accountState{cfg: acc, remaining: tokenLimit}
	}
	t.Skip("no usable kiro account in config.json")
	return nil
}

// setupChatHandler primes the globals handleChatCompletions depends on and
// points the rotation pool at the single test account.
func setupChatHandler(t *testing.T, acc *accountState) {
	t.Helper()
	analyticsData = analytics.New()

	rotationMu.Lock()
	activeBatch[0], activeBatch[1], activeBatch[2] = acc, nil, nil
	currentSlot, stickyCount = 0, 0
	rotationMu.Unlock()
	t.Cleanup(func() {
		rotationMu.Lock()
		activeBatch[0], activeBatch[1], activeBatch[2] = nil, nil, nil
		rotationMu.Unlock()
	})

	select {
	case <-bootReady:
	default:
		close(bootReady)
	}
}

func TestIntegration_ToolCallOutput(t *testing.T) {
	acc := onlineSingleAccount(t)
	setupChatHandler(t, acc)

	const (
		wantTool = "edit"
		wantPath = "internal/server/server.go"
	)

	for _, model := range onlineModels {
		t.Run(model, func(t *testing.T) {
			prompt := fmt.Sprintf("Use the edit tool on %s: replace the text `foo` with `bar`.", wantPath)
			body := integrationBody(model, prompt)

			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			handleChatCompletions(rec, req)

			raw := rec.Body.String()
			got := collectSSE(raw)
			t.Logf("model=%s status=%d tool=%q id=%q type=%q args=%q finish=%q content=%q reasoning=%dB",
				model, rec.Code, got.toolName, got.toolID, got.toolType, got.toolArgs, got.finishReason, got.content, len(got.reasoning))

			if !strings.Contains(rec.Header().Get("Content-Type"), "text/event-stream") {
				t.Errorf("Content-Type = %q, want text/event-stream", rec.Header().Get("Content-Type"))
			}
			if !got.sawDone {
				t.Errorf("stream missing [DONE]\nraw:\n%s", raw)
			}
			if got.toolName != wantTool {
				t.Fatalf("tool call name = %q, want %q\nraw:\n%s", got.toolName, wantTool, raw)
			}
			if got.toolType != "function" {
				t.Errorf("tool call type = %q, want function", got.toolType)
			}
			if got.toolID == "" {
				t.Errorf("tool call id is empty\nraw:\n%s", raw)
			}
			if got.finishReason != "tool_calls" {
				t.Errorf("finish_reason = %q, want tool_calls", got.finishReason)
			}

			var args struct {
				FilePath  string `json:"filePath"`
				OldString string `json:"oldString"`
				NewString string `json:"newString"`
			}
			if err := json.Unmarshal([]byte(got.toolArgs), &args); err != nil {
				t.Fatalf("tool arguments are not valid JSON: %q (%v)", got.toolArgs, err)
			}
			if args.FilePath != wantPath {
				t.Errorf("edit filePath = %q, want %q", args.FilePath, wantPath)
			}
			if !strings.Contains(args.OldString, "foo") {
				t.Errorf("edit oldString = %q, want to contain foo", args.OldString)
			}
			if !strings.Contains(args.NewString, "bar") {
				t.Errorf("edit newString = %q, want to contain bar", args.NewString)
			}
			if strings.HasSuffix(model, "-thinking") && got.reasoning == "" {
				t.Errorf("%s: expected reasoning_content in output, got none", model)
			}
		})
	}
}

func TestIntegration_MultiTurnAllRoles(t *testing.T) {
	acc := onlineSingleAccount(t)
	setupChatHandler(t, acc)

	for _, model := range onlineModels {
		t.Run(model, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(multiTurnBody(model)))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			handleChatCompletions(rec, req)

			raw := rec.Body.String()
			got := collectSSE(raw)
			t.Logf("model=%s status=%d tool=%q finish=%q content=%q reasoning=%dB",
				model, rec.Code, got.toolName, got.finishReason, got.content, len(got.reasoning))

			if !got.sawDone {
				t.Errorf("stream missing [DONE]\nraw:\n%s", raw)
			}
			if got.toolName != "" {
				t.Errorf("expected a text answer after the tool result, got tool call %q (args %s)", got.toolName, got.toolArgs)
			}
			if !strings.Contains(got.content, "Run") {
				t.Errorf("answer = %q, want it to name the first function (Run)\nraw:\n%s", got.content, raw)
			}
		})
	}
}
