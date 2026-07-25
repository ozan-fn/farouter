package kiro

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"
)

func TestParseSSELine(t *testing.T) {
	tests := []struct {
		name    string
		line    string
		want    string
		wantDone bool
	}{
		{"data chunk", "data: {\"id\":\"1\"}\n", "1", false},
		{"data with space", "data: {\"id\":\"2\"}\n", "2", false},
		{"done sentinel", "data: [DONE]\n", "", true},
		{"empty line", "", "", false},
		{"non-data line", "event: ping\n", "", false},
		{"short line", "abc\n", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed, done := ParseSSELine(tt.line)
			if done != tt.wantDone {
				t.Errorf("done=%v want=%v", done, tt.wantDone)
			}
			if tt.want != "" {
				id, _ := parsed["id"].(string)
				if id != tt.want {
					t.Errorf("id=%q want=%q", id, tt.want)
				}
			}
		})
	}
}

func TestFormatSSE(t *testing.T) {
	data := map[string]any{"id": "test"}
	got := formatSSE(data)
	want := "data: {\"id\":\"test\"}\n\n"
	if got != want {
		t.Errorf("got=%q want=%q", got, want)
	}
}

func TestFormatSSENil(t *testing.T) {
	got := formatSSE(nil)
	want := "data: null\n\n"
	if got != want {
		t.Errorf("got=%q want=%q", got, want)
	}
}

func TestHasValuableContent(t *testing.T) {
	tests := []struct {
		name string
		chunk map[string]any
		want bool
	}{
		{"empty delta", map[string]any{
			"choices": []any{map[string]any{"delta": map[string]any{}}},
		}, false},
		{"content", map[string]any{
			"choices": []any{map[string]any{"delta": map[string]any{"content": "hi"}}},
		}, true},
		{"reasoning", map[string]any{
			"choices": []any{map[string]any{"delta": map[string]any{"reasoning_content": "think"}}},
		}, true},
		{"finish reason", map[string]any{
			"choices": []any{map[string]any{"delta": map[string]any{}, "finish_reason": "stop"}},
		}, true},
		{"tool calls", map[string]any{
			"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{map[string]any{"id": "call_1"}}}}},
		}, true},
		{"no choices", map[string]any{"id": "test"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasValuableContent(tt.chunk); got != tt.want {
				t.Errorf("got=%v want=%v", got, tt.want)
			}
		})
	}
}

func TestFixInvalidID(t *testing.T) {
	tests := []struct {
		name  string
		input map[string]any
		want  bool
	}{
		{"empty id", map[string]any{"id": ""}, true},
		{"chat id", map[string]any{"id": "chat"}, true},
		{"short id", map[string]any{"id": "ab"}, true},
		{"valid id", map[string]any{"id": "chatcmpl-abc123"}, false},
		{"no id", map[string]any{}, true}, // empty string should be fixed
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := fixInvalidID(tt.input)
			if got != tt.want {
				t.Errorf("got=%v want=%v", got, tt.want)
			}
		})
	}
}

func TestBuildErrorBody(t *testing.T) {
	tests := []struct {
		status int
		msg    string
	}{
		{400, "Bad request"},
		{401, "Invalid API key"},
		{403, "Quota exceeded"},
		{502, "Upstream error"},
		{999, "Fallback"},
	}
	for _, tt := range tests {
		t.Run(tt.msg, func(t *testing.T) {
			body := buildErrorBody(tt.status, tt.msg)
			err, _ := body["error"].(map[string]any)
			if err == nil {
				t.Fatal("missing error key")
			}
			msg, _ := err["message"].(string)
			if msg != tt.msg {
				t.Errorf("msg=%q want=%q", msg, tt.msg)
			}
		})
	}
}

func TestWriteStreamError(t *testing.T) {
	var buf bytes.Buffer
	writeStreamError(&buf, 502, "bad gateway")
	got := buf.String()
	if !strings.Contains(got, "bad_gateway") {
		t.Errorf("missing error code in: %s", got)
	}
	if !strings.Contains(got, "data:") {
		t.Errorf("missing data: prefix in: %s", got)
	}
}

func TestBuildErrorBodyDefault(t *testing.T) {
	body := buildErrorBody(0, "")
	err, _ := body["error"].(map[string]any)
	msg, _ := err["message"].(string)
	if msg != "An error occurred" {
		t.Errorf("msg=%q", msg)
	}
}

func TestFormatProviderError(t *testing.T) {
	err := context.Canceled
	got := formatProviderError(err, "kiro", "test", 499)
	if !strings.Contains(got, "499") {
		t.Errorf("missing status code: %s", got)
	}
}

func TestNewStreamControllerBasic(t *testing.T) {
	ctx := context.Background()
	sc := NewStreamController(ctx)
	if !sc.IsConnected() {
		t.Error("should be connected initially")
	}
	sc.HandleComplete()
	if sc.IsConnected() {
		t.Error("should be disconnected after complete")
	}
}

func TestStreamControllerDisconnect(t *testing.T) {
	ctx := context.Background()
	sc := NewStreamController(ctx)
	sc.HandleDisconnect("test")
	if sc.IsConnected() {
		t.Error("should be disconnected")
	}
	time.Sleep(600 * time.Millisecond)
	if sc.Signal().Err() == nil {
		t.Error("context should be cancelled after disconnect delay")
	}
}

func TestStreamControllerDoubleDisconnect(t *testing.T) {
	ctx := context.Background()
	sc := NewStreamController(ctx)
	sc.HandleDisconnect("first")
	sc.HandleDisconnect("second")
	select {
	case <-sc.Signal().Done():
	case <-time.After(600 * time.Millisecond):
		t.Error("context should be cancelled")
	}
}

func TestStreamControllerError(t *testing.T) {
	ctx := context.Background()
	sc := NewStreamController(ctx)
	sc.HandleError(io.ErrUnexpectedEOF)
	if sc.IsConnected() {
		t.Error("should be disconnected after error")
	}
}

func TestEmptySSE(t *testing.T) {
	var buf bytes.Buffer
	pr, pw := io.Pipe()
	go func() {
		pw.Write([]byte("data: {\"id\":\"1\",\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\ndata: [DONE]\n\n"))
		pw.Close()
	}()
	sc := NewStreamController(context.Background())
	r := createPassthroughTransform(pr, sc, "test-model")
	_, err := io.Copy(&buf, r)
	if err != nil {
		t.Fatalf("copy: %v", err)
	}
	output := buf.String()
	if !strings.Contains(output, "hi") {
		t.Errorf("missing content in output: %s", output)
	}
}

func TestExtractUsageFromChunk(t *testing.T) {
	chunk := map[string]any{
		"usage": map[string]any{
			"prompt_tokens":     float64(10),
			"completion_tokens": float64(20),
			"total_tokens":      float64(30),
		},
	}
	u := extractUsageFromChunk(chunk)
	if u == nil {
		t.Fatal("nil usage")
	}
	if u["prompt_tokens"] != 10 {
		t.Errorf("prompt_tokens=%v", u["prompt_tokens"])
	}
}

func TestMergeUsage(t *testing.T) {
	a := map[string]any{"prompt_tokens": 10, "completion_tokens": 5}
	b := map[string]any{"completion_tokens": 3, "total_tokens": 8}
	r := mergeUsage(a, b)
	if r["prompt_tokens"] != 10 {
		t.Errorf("prompt_tokens=%v", r["prompt_tokens"])
	}
	if r["completion_tokens"] != 8 {
		t.Errorf("completion_tokens=%v", r["completion_tokens"])
	}
}

func TestEstimateUsage(t *testing.T) {
	u := estimateUsage(100)
	if u["completion_tokens"] != 25 {
		t.Errorf("completion_tokens=%v", u["completion_tokens"])
	}
}

func TestHasValuableContentWithToolCalls(t *testing.T) {
	chunk := map[string]any{
		"choices": []any{map[string]any{
			"delta": map[string]any{
				"tool_calls": []any{},
			},
		}},
	}
	if hasValuableContent(chunk) {
		t.Error("empty tool_calls should not be valuable")
	}
}

func TestFixInvalidIDValid(t *testing.T) {
	m := map[string]any{"id": "chatcmpl-abc123"}
	if fixInvalidID(m) {
		t.Error("valid id should not be fixed")
	}
	if m["id"] != "chatcmpl-abc123" {
		t.Errorf("id changed to %v", m["id"])
	}
}

func TestCleanUsagePayload(t *testing.T) {
	data := map[string]any{
		"id":    "test",
		"usage": nil,
	}
	cleaned := cleanUsagePayload(data)
	if _, ok := cleaned["usage"]; ok {
		t.Error("nil usage should be removed")
	}
}
