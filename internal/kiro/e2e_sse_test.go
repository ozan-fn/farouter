package kiro

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// encodeFrame builds one AWS EventStream binary frame for testing.
func encodeFrame(eventType string, payload map[string]any) []byte {
	payloadBytes, _ := json.Marshal(payload)
	var hdr bytes.Buffer
	writeHdr := func(name, val string) {
		hdr.WriteByte(byte(len(name)))
		hdr.WriteString(name)
		hdr.WriteByte(7)
		binary.Write(&hdr, binary.BigEndian, uint16(len(val)))
		hdr.WriteString(val)
	}
	writeHdr(":event-type", eventType)
	writeHdr(":message-type", "event")
	writeHdr(":content-type", "application/json")
	hb := hdr.Bytes()
	totalLen := uint32(16 + len(hb) + len(payloadBytes))
	var prelude [8]byte
	binary.BigEndian.PutUint32(prelude[0:4], totalLen)
	binary.BigEndian.PutUint32(prelude[4:8], uint32(len(hb)))
	crc := crc32.ChecksumIEEE(prelude[:])
	var frame bytes.Buffer
	frame.Write(prelude[:])
	binary.Write(&frame, binary.BigEndian, crc)
	frame.Write(hb)
	frame.Write(payloadBytes)
	msgCrc := crc32.ChecksumIEEE(frame.Bytes())
	binary.Write(&frame, binary.BigEndian, msgCrc)
	return frame.Bytes()
}

func frames(r ...[]byte) io.Reader { return bytes.NewReader(bytes.Join(r, nil)) }

func parseSSEChunks(t *testing.T, raw string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, block := range strings.Split(raw, "\n\n") {
		block = strings.TrimSpace(block)
		if block == "" || strings.HasPrefix(block, ":") {
			continue
		}
		if block == "data: [DONE]" {
			continue
		}
		if !strings.HasPrefix(block, "data: ") {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(strings.TrimPrefix(block, "data: ")), &m); err != nil {
			t.Fatalf("unmarshal SSE chunk %q: %v", block, err)
		}
		out = append(out, m)
	}
	return out
}

func TestE2E_UserReq_To_OpenAI_SSE_Complete(t *testing.T) {
	req := ChatRequest{
		Model: "kr/claude-sonnet-4.5",
		Messages: []Message{
			{Role: "user", Content: "hello"},
		},
		Stream: true,
	}
	resolved := ResolveModel(req.Model)
	kr, err := buildKiroRequest(req, resolved, DefaultProfileArn("builderId"), "conv-1", "conn-1")
	if err != nil {
		t.Fatalf("buildKiroRequest: %v", err)
	}
	if len(kr.Body) == 0 {
		t.Fatal("empty kiro body")
	}
	f1 := encodeFrame("assistantResponseEvent", map[string]any{"content": "Hello "})
	f2 := encodeFrame("assistantResponseEvent", map[string]any{"content": "world"})
	f3 := encodeFrame("messageStopEvent", map[string]any{"stopReason": "end_turn"})
	var buf bytes.Buffer
	if err := transformKiroToSSE(frames(f1, f2, f3), "kr/claude-sonnet-4.5", false, &buf, nil); err != nil {
		t.Fatalf("transformKiroToSSE: %v", err)
	}
	raw := buf.String()
	if !strings.Contains(raw, "data: ") {
		t.Fatal("no SSE data lines")
	}
	if !strings.Contains(raw, "data: [DONE]") {
		t.Error("missing data: [DONE] sentinel")
	}
	chunks := parseSSEChunks(t, raw)
	if len(chunks) == 0 {
		t.Fatal("no chunks parsed")
	}
	firstChoices, _ := chunks[0]["choices"].([]any)
	if len(firstChoices) > 0 {
		if c, ok := firstChoices[0].(map[string]any); ok {
			if d, ok := c["delta"].(map[string]any); ok && d["role"] != "assistant" {
				t.Errorf("first delta role = %v want assistant", d["role"])
			}
		}
	}
	found := false
	for _, ch := range chunks {
		choices, _ := ch["choices"].([]any)
		if len(choices) == 0 {
			continue
		}
		c, _ := choices[0].(map[string]any)
		d, _ := c["delta"].(map[string]any)
		if d["content"] != nil && d["content"] != "" {
			found = true
			break
		}
	}
	if !found {
		t.Error("no content delta found")
	}
	last := chunks[len(chunks)-1]
	choices, _ := last["choices"].([]any)
	if len(choices) > 0 {
		if c, ok := choices[0].(map[string]any); ok && c["finish_reason"] == nil {
			t.Error("last chunk missing finish_reason")
		}
	}
}

func TestE2E_SSE_ToolCall_Streaming(t *testing.T) {
	fTool := encodeFrame("toolUseEvent", map[string]any{
		"toolUseId": "call_1", "name": "executeBash", "input": `{"command":"ls"}`,
		"stop": true,
	})
	fStop := encodeFrame("messageStopEvent", map[string]any{"stopReason": "tool_use"})
	var buf bytes.Buffer
	if err := transformKiroToSSE(frames(fTool, fStop), "kr/claude-sonnet-4.5", false, &buf, nil); err != nil {
		t.Fatalf("transform: %v", err)
	}
	raw := buf.String()
	if !strings.Contains(raw, "tool_calls") {
		t.Errorf("expected tool_calls in SSE, got:\n%s", raw)
	}
	if !strings.Contains(raw, "data: [DONE]") {
		t.Error("missing [DONE]")
	}
}

func TestE2E_SSE_Thinking(t *testing.T) {
	fThink := encodeFrame("reasoningContentEvent", map[string]any{"reasoningContentEvent": map[string]any{"text": "thinking step"}})
	fContent := encodeFrame("assistantResponseEvent", map[string]any{"content": "answer"})
	fStop := encodeFrame("messageStopEvent", map[string]any{"stopReason": "end_turn"})
	var buf bytes.Buffer
	if err := transformKiroToSSE(frames(fThink, fContent, fStop), "kr/claude-sonnet-4.5-thinking", true, &buf, nil); err != nil {
		t.Fatalf("transform: %v", err)
	}
	if !strings.Contains(buf.String(), "reasoning_content") {
		t.Error("missing reasoning_content delta")
	}
}

func TestE2E_DeepSearch_MultiTurn_With_Tools(t *testing.T) {
	req := ChatRequest{
		Model: "kr/claude-sonnet-4.5",
		Messages: []Message{
			{Role: "user", Content: "deep search: find all TODO in repo"},
			{Role: "assistant", Content: "", ToolCalls: []ToolCall{{ID: "call_1", Type: "function", Function: ToolCallFunction{Name: "executeBash", Arguments: `{"command":"grep -r TODO --include=*.go"}`}}}},
			{Role: "tool", Content: "main.go:10 TODO fix", ToolCallID: "call_1"},
			{Role: "user", Content: "summarize"},
		},
		Tools: []Tool{{Type: "function", Function: ToolFunction{Name: "executeBash", Description: "bash", Parameters: map[string]any{"type": "object"}}}},
	}
	resolved := ResolveModel(req.Model)
	kr, err := buildKiroRequest(req, resolved, DefaultProfileArn("builderId"), "conv-deep", "conn-deep")
	if err != nil {
		t.Fatalf("buildKiroRequest deep: %v", err)
	}
	_ = kr
	f1 := encodeFrame("assistantResponseEvent", map[string]any{"content": "Found 1 TODO in main.go:10"})
	f2 := encodeFrame("messageStopEvent", map[string]any{"stopReason": "end_turn"})
	var buf bytes.Buffer
	if err := transformKiroToSSE(frames(f1, f2), req.Model, false, &buf, nil); err != nil {
		t.Fatalf("transform deep: %v", err)
	}
	if !strings.Contains(buf.String(), "TODO") {
		t.Error("deep search SSE missing expected content")
	}
}

func TestE2E_NonStreaming_Usage(t *testing.T) {
	fContent := encodeFrame("assistantResponseEvent", map[string]any{"content": "hi"})
	fMetrics := encodeFrame("metricsEvent", map[string]any{"inputTokens": 10, "outputTokens": 5})
	fStop := encodeFrame("messageStopEvent", map[string]any{"stopReason": "end_turn"})
	var buf bytes.Buffer
	if err := transformKiroToSSE(frames(fContent, fMetrics, fStop), "kr/claude-sonnet-4.5", false, &buf, nil); err != nil {
		t.Fatalf("transform: %v", err)
	}
	chunks := parseSSEChunks(t, buf.String())
	if len(chunks) == 0 {
		t.Fatal("no chunks")
	}
}

func TestE2E_OpenAICompat_SSE_Format(t *testing.T) {
	f1 := encodeFrame("assistantResponseEvent", map[string]any{"content": "x"})
	f2 := encodeFrame("messageStopEvent", map[string]any{"stopReason": "end_turn"})
	var buf bytes.Buffer
	transformKiroToSSE(frames(f1, f2), "kr/claude-sonnet-4.5", false, &buf, nil)
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	for _, l := range lines {
		if l == "" || strings.HasPrefix(l, ":") {
			continue
		}
		if l != "data: [DONE]" && !strings.HasPrefix(l, "data: {") {
			t.Errorf("invalid SSE line: %q", l)
		}
	}
}

// --- Upstream Kiro HTTP -> SSE (covers sendToKiroEndpoint + pipeKiroResponse) ---

func TestE2E_UpstreamKiro_HTTP_To_SSE(t *testing.T) {
	// Real EventStream bytes the Kiro server would return
	f1 := encodeFrame("assistantResponseEvent", map[string]any{"content": "from kiro upstream"})
	f2 := encodeFrame("messageStopEvent", map[string]any{"stopReason": "end_turn"})
	payload := bytes.Join([][]byte{f1, f2}, nil)

	// Mock Kiro upstream server
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify client sends Kiro headers
		if got := r.Header.Get("Authorization"); !strings.HasPrefix(got, "Bearer ") {
			t.Errorf("upstream: missing Authorization, got %q", got)
		}
		if r.Method != "POST" {
			t.Errorf("method = %s want POST", r.Method)
		}
		w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
		w.WriteHeader(200)
		w.Write(payload)
	}))
	defer srv.Close()

	creds := Credentials{AccessToken: "test-token", PSD: ProviderSpecificData{AuthMethod: "builderId"}}
	ctx := context.Background()

	// Direct upstream call (like Execute does internally)
	resp, err := sendToKiroEndpoint(ctx, creds, srv.URL, []byte(`{}`))
	if err != nil {
		t.Fatalf("sendToKiroEndpoint: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}

	// Pipe EventStream -> OpenAI SSE -> client
	rr := httptest.NewRecorder()
	resolved := ResolveModel("kr/claude-sonnet-4.5")
	if err := pipeKiroResponse(ctx, resp, rr, resolved, nil); err != nil {
		t.Fatalf("pipeKiroResponse: %v", err)
	}
	body := rr.Body.String()
	if ct := rr.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q want text/event-stream", ct)
	}
	if !strings.Contains(body, "from kiro upstream") {
		t.Errorf("missing upstream content in SSE: %s", body)
	}
	if !strings.Contains(body, "data: [DONE]") {
		t.Error("missing data: [DONE]")
	}
	chunks := parseSSEChunks(t, body)
	if len(chunks) == 0 {
		t.Fatal("no chunks")
	}
	// finish_reason must be present on last chunk
	last := chunks[len(chunks)-1]
	choices, _ := last["choices"].([]any)
	if c, ok := choices[0].(map[string]any); ok && c["finish_reason"] == nil {
		t.Error("missing finish_reason")
	}
}

func TestE2E_UpstreamKiro_ErrorPropagation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(502)
		w.Write([]byte(`bad gateway`))
	}))
	defer srv.Close()
	creds := Credentials{AccessToken: "tok", PSD: ProviderSpecificData{AuthMethod: "builderId"}}
	resp, err := sendToKiroEndpoint(context.Background(), creds, srv.URL, []byte(`{}`))
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 502 {
		t.Fatalf("want 502 got %d", resp.StatusCode)
	}
}
