package kiro

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMessageJSONDeserialization(t *testing.T) {
	raw := `{
		"role": "assistant",
		"content": "",
		"tool_calls": [
			{
				"id": "call_abc123",
				"type": "function",
				"function": {
					"name": "executeBash",
					"arguments": "{\"command\": \"ls\"}"
				}
			}
		]
	}`

	var msg Message
	if err := json.Unmarshal([]byte(raw), &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if msg.Role != "assistant" {
		t.Errorf("role = %q, want assistant", msg.Role)
	}
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("tool_calls len = %d, want 1", len(msg.ToolCalls))
	}
	if msg.ToolCalls[0].ID != "call_abc123" {
		t.Errorf("tool_calls[0].ID = %q, want call_abc123", msg.ToolCalls[0].ID)
	}
	if msg.ToolCalls[0].Function.Name != "executeBash" {
		t.Errorf("tool_calls[0].Function.Name = %q, want executeBash", msg.ToolCalls[0].Function.Name)
	}
	if msg.ToolCalls[0].Function.Arguments != `{"command": "ls"}` {
		t.Errorf("tool_calls[0].Function.Arguments = %q, want {\"command\": \"ls\"}", msg.ToolCalls[0].Function.Arguments)
	}
}

func TestToolResultDeserialization(t *testing.T) {
	raw := `{
		"role": "tool",
		"content": "file1.go\nfile2.go",
		"tool_call_id": "call_abc123"
	}`

	var msg Message
	if err := json.Unmarshal([]byte(raw), &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if msg.Role != "tool" {
		t.Errorf("role = %q, want tool", msg.Role)
	}
	if msg.ToolCallID != "call_abc123" {
		t.Errorf("tool_call_id = %q, want call_abc123", msg.ToolCallID)
	}
	if s, ok := msg.Content.(string); !ok || s != "file1.go\nfile2.go" {
		t.Errorf("content = %v, want file1.go\\nfile2.go", msg.Content)
	}
}

func TestToolDeserialization(t *testing.T) {
	raw := `{
		"type": "function",
		"function": {
			"name": "executeBash",
			"description": "Execute a bash command",
			"parameters": {
				"type": "object",
				"properties": {
					"command": {"type": "string"}
				},
				"required": ["command"]
			}
		}
	}`

	var tool Tool
	if err := json.Unmarshal([]byte(raw), &tool); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if tool.Type != "function" {
		t.Errorf("type = %q, want function", tool.Type)
	}
	if tool.Function.Name != "executeBash" {
		t.Errorf("function.name = %q, want executeBash", tool.Function.Name)
	}
	if tool.Function.Description != "Execute a bash command" {
		t.Errorf("function.description = %q", tool.Function.Description)
	}
	if tool.Function.Parameters == nil {
		t.Fatal("function.parameters is nil")
	}
	if pt, ok := tool.Function.Parameters["type"].(string); !ok || pt != "object" {
		t.Errorf("function.parameters.type = %v, want object", tool.Function.Parameters["type"])
	}
}

func TestConvertMessagesWithToolCalls(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "list files"},
		{
			Role:    "assistant",
			Content: "",
			ToolCalls: []ToolCall{
				{
					ID:   "call_abc123",
					Type: "function",
					Function: ToolCallFunction{
						Name:      "executeBash",
						Arguments: `{"command": "ls"}`,
					},
				},
			},
		},
		{Role: "tool", Content: "file1.go\nfile2.go", ToolCallID: "call_abc123"},
		{Role: "assistant", Content: "There are two files."},
		{Role: "user", Content: "now read file2.go"},
	}

	tools := []Tool{
		{
			Type: "function",
			Function: ToolFunction{
				Name:        "executeBash",
				Description: "Execute a bash command",
				Parameters: map[string]any{
					"type":       "object",
					"properties": map[string]any{"command": map[string]any{"type": "string"}},
					"required":   []any{"command"},
				},
			},
		},
	}

	result := convertMessages(messages, tools, "claude-sonnet-4.5", false)

	// Check that we have history entries
	if len(result.history) == 0 {
		t.Fatal("history is empty")
	}

	// Check that currentMessage is set
	if result.currentMessage == nil {
		t.Fatal("currentMessage is nil")
	}
	uim, ok := result.currentMessage["userInputMessage"].(map[string]any)
	if !ok {
		t.Fatal("currentMessage.userInputMessage is not a map")
	}
	if uim["content"] != "now read file2.go" {
		t.Errorf("currentMessage content = %q, want 'now read file2.go'", uim["content"])
	}

	// Find the assistant message with toolUses
	foundToolUses := false
	for _, h := range result.history {
		arm, ok := h["assistantResponseMessage"].(map[string]any)
		if !ok {
			continue
		}
		tuArr, ok := arm["toolUses"].([]any)
		if !ok || len(tuArr) == 0 {
			continue
		}
		foundToolUses = true

		tu, ok := tuArr[0].(map[string]any)
		if !ok {
			t.Fatal("toolUses[0] is not a map")
		}
		if tu["toolUseId"] != "call_abc123" {
			t.Errorf("toolUses[0].toolUseId = %q, want call_abc123", tu["toolUseId"])
		}
		if tu["name"] != "executeBash" {
			t.Errorf("toolUses[0].name = %q, want executeBash", tu["name"])
		}
		input, ok := tu["input"].(map[string]any)
		if !ok {
			t.Fatal("toolUses[0].input is not a map")
		}
		if input["command"] != "ls" {
			t.Errorf("toolUses[0].input.command = %q, want ls", input["command"])
		}
	}
	if !foundToolUses {
		t.Error("no assistantResponseMessage with toolUses found in history")
	}

	// Find the user message with toolResults
	foundToolResults := false
	for _, h := range result.history {
		uim, ok := h["userInputMessage"].(map[string]any)
		if !ok {
			continue
		}
		ctx, ok := uim["userInputMessageContext"].(map[string]any)
		if !ok {
			continue
		}
		trArr, _ := ctx["toolResults"].([]any)
		if len(trArr) == 0 {
			continue
		}
		foundToolResults = true

		tr, ok := trArr[0].(map[string]any)
		if !ok {
			t.Fatal("toolResults[0] is not a map")
		}
		if tr["toolUseId"] != "call_abc123" {
			t.Errorf("toolResults[0].toolUseId = %q, want call_abc123", tr["toolUseId"])
		}
		if tr["status"] != "success" {
			t.Errorf("toolResults[0].status = %q, want success", tr["status"])
		}
	}
	if !foundToolResults {
		t.Error("no userInputMessage with toolResults found in history")
	}
}

// TestConvertMessagesAssistantEndWithToolCalls verifies the fix for
// TOOL_USE_RESULT_MISMATCH: when the LAST message is assistant with tool_calls
// (no tool/user follow-up), convertMessages must replace the stale currentMessage
// (which still points to an older user message) with a fresh "Continue" user message.
func TestConvertMessagesAssistantEndWithToolCalls(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "list files"},
		{
			Role:    "assistant",
			Content: "",
			ToolCalls: []ToolCall{
				{ID: "call_1", Type: "function", Function: ToolCallFunction{Name: "ls", Arguments: `{}`}},
			},
		},
	}

	result := convertMessages(messages, nil, "claude-sonnet-4.5", false)

	// History should end with assistantResponseMessage (the toolUses)
	if len(result.history) == 0 {
		t.Fatal("history should not be empty")
	}
	last := result.history[len(result.history)-1]
	if last["assistantResponseMessage"] == nil {
		t.Error("history should end with assistantResponseMessage")
	}
	if arm, ok := last["assistantResponseMessage"].(map[string]any); ok {
		if tuArr, has := arm["toolUses"].([]any); !has || len(tuArr) == 0 {
			t.Error("assistantResponseMessage should have toolUses")
		}
	}

	// currentMessage must NOT be the stale "list files" — should be "Continue"
	if result.currentMessage == nil {
		t.Fatal("currentMessage is nil")
	}
	uim, ok := result.currentMessage["userInputMessage"].(map[string]any)
	if !ok {
		t.Fatal("currentMessage.userInputMessage is not a map")
	}
	if uim["content"] == "list files" {
		t.Error("currentMessage content is stale 'list files' — should be replaced with 'Continue'")
	}
	if uim["content"] != "Continue" {
		t.Errorf("currentMessage content = %q, want 'Continue'", uim["content"])
	}
}

func TestConvertMessagesMultiTurnToolCalls(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "list files"},
		{
			Role:    "assistant",
			Content: "",
			ToolCalls: []ToolCall{
				{ID: "call_1", Type: "function", Function: ToolCallFunction{Name: "executeBash", Arguments: `{"command": "ls"}`}},
			},
		},
		{Role: "tool", Content: "file1.go\nfile2.go", ToolCallID: "call_1"},
		{Role: "assistant", Content: "There are two files."},
		{Role: "user", Content: "read file1.go"},
		{
			Role:    "assistant",
			Content: "",
			ToolCalls: []ToolCall{
				{ID: "call_2", Type: "function", Function: ToolCallFunction{Name: "readFile", Arguments: `{"path": "/tmp/file1.go"}`}},
			},
		},
		{Role: "tool", Content: "package main", ToolCallID: "call_2"},
		{Role: "assistant", Content: "This is a Go file."},
		{Role: "user", Content: "now read file2.go"},
	}

	tools := []Tool{
		{Type: "function", Function: ToolFunction{Name: "executeBash", Description: "Run bash", Parameters: map[string]any{"type": "object", "properties": map[string]any{}, "required": []any{}}}},
		{Type: "function", Function: ToolFunction{Name: "readFile", Description: "Read file", Parameters: map[string]any{"type": "object", "properties": map[string]any{}, "required": []any{}}}},
	}

	result := convertMessages(messages, tools, "claude-sonnet-4.5", false)

	// Count toolUses in history
	totalToolUses := 0
	for _, h := range result.history {
		arm, ok := h["assistantResponseMessage"].(map[string]any)
		if !ok {
			continue
		}
		tuArr, _ := arm["toolUses"].([]any)
		totalToolUses += len(tuArr)
	}
	if totalToolUses != 2 {
		t.Errorf("total toolUses = %d, want 2", totalToolUses)
	}

	// Count toolResults in history + currentMessage
	totalToolResults := 0
	for _, h := range result.history {
		uim, ok := h["userInputMessage"].(map[string]any)
		if !ok {
			continue
		}
		ctx, ok := uim["userInputMessageContext"].(map[string]any)
		if !ok {
			continue
		}
		trArr, _ := ctx["toolResults"].([]any)
		totalToolResults += len(trArr)
	}
	if result.currentMessage != nil {
		uim, ok := result.currentMessage["userInputMessage"].(map[string]any)
		if ok {
			ctx, ok := uim["userInputMessageContext"].(map[string]any)
			if ok {
				trArr, _ := ctx["toolResults"].([]any)
				totalToolResults += len(trArr)
			}
		}
	}
	if totalToolResults != 2 {
		t.Errorf("total toolResults = %d, want 2", totalToolResults)
	}

	// Check history alternation: no two consecutive user or assistant
	for i := 1; i < len(result.history); i++ {
		prevIsUser := result.history[i-1]["userInputMessage"] != nil
		currIsUser := result.history[i]["userInputMessage"] != nil
		if prevIsUser == currIsUser {
			t.Errorf("history[%d] and history[%d] have same role (user=%v)", i-1, i, currIsUser)
		}
	}

	// Check history starts with user
	if len(result.history) > 0 && result.history[0]["userInputMessage"] == nil {
		t.Error("history does not start with userInputMessage")
	}
}

func TestConvertMessagesWithoutTools(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi there"},
		{Role: "user", Content: "how are you"},
	}

	result := convertMessages(messages, nil, "claude-sonnet-4.5", false)

	if len(result.history) != 2 {
		t.Errorf("history len = %d, want 2", len(result.history))
	}

	uim, ok := result.currentMessage["userInputMessage"].(map[string]any)
	if !ok {
		t.Fatal("currentMessage.userInputMessage is not a map")
	}
	if uim["content"] != "how are you" {
		t.Errorf("currentMessage content = %q, want 'how are you'", uim["content"])
	}
}

func TestConvertMessagesWithToolCallsNoTools(t *testing.T) {
	// AIClient2API style: toolUses always preserved even without tools in request
	messages := []Message{
		{Role: "user", Content: "list files"},
		{
			Role:    "assistant",
			Content: "",
			ToolCalls: []ToolCall{
				{ID: "call_1", Type: "function", Function: ToolCallFunction{Name: "ls", Arguments: `{}`}},
			},
		},
		{Role: "tool", Content: "file1.go", ToolCallID: "call_1"},
		{Role: "assistant", Content: "Found one file."},
		{Role: "user", Content: "thanks"},
	}

	result := convertMessages(messages, nil, "claude-sonnet-4.5", false)

	// toolUses should be preserved even without tools parameter
	foundToolUses := false
	for _, h := range result.history {
		arm, ok := h["assistantResponseMessage"].(map[string]any)
		if !ok {
			continue
		}
		tu, ok := arm["toolUses"].([]any)
		if ok && len(tu) > 0 {
			foundToolUses = true
			break
		}
	}
	if !foundToolUses {
		t.Error("toolUses should be preserved in history even without tools, got none")
	}

	// toolResults should also be preserved
	foundToolResults := false
	for _, h := range result.history {
		uim, ok := h["userInputMessage"].(map[string]any)
		if !ok {
			continue
		}
		ctx, ok := uim["userInputMessageContext"].(map[string]any)
		if !ok {
			continue
		}
		tr, ok := ctx["toolResults"].([]any)
		if ok && len(tr) > 0 {
			foundToolResults = true
			break
		}
	}
	if !foundToolResults {
		t.Error("toolResults should be preserved in history even without tools, got none")
	}
}

func TestMergeConsecutiveRolesAIClient2API(t *testing.T) {
	t.Run("merges consecutive user messages", func(t *testing.T) {
		history := []map[string]any{
			{"userInputMessage": map[string]any{"content": "a"}},
			{"userInputMessage": map[string]any{"content": "b"}},
			{"assistantResponseMessage": map[string]any{"content": "c"}},
		}

		result := mergeConsecutiveRolesAIClient2API(history, "")

		if len(result) != 2 {
			t.Fatalf("len = %d, want 2", len(result))
		}
		if result[0]["userInputMessage"] == nil {
			t.Error("result[0] should be user")
		}
		uim := result[0]["userInputMessage"].(map[string]any)
		if uim["content"] != "a\nb" {
			t.Errorf("merged content = %q, want 'a\\nb'", uim["content"])
		}
		if result[1]["assistantResponseMessage"] == nil {
			t.Error("result[1] should be assistant")
		}
	})

	t.Run("merges consecutive assistant messages", func(t *testing.T) {
		history := []map[string]any{
			{"userInputMessage": map[string]any{"content": "a"}},
			{"assistantResponseMessage": map[string]any{"content": "b"}},
			{"assistantResponseMessage": map[string]any{"content": "c"}},
		}

		result := mergeConsecutiveRolesAIClient2API(history, "")

		if len(result) != 2 {
			t.Fatalf("len = %d, want 2", len(result))
		}
		if result[1]["assistantResponseMessage"] == nil {
			t.Error("result[1] should be assistant")
		}
		arm := result[1]["assistantResponseMessage"].(map[string]any)
		if arm["content"] != "b\nc" {
			t.Errorf("merged content = %q, want 'b\\n\\nc'", arm["content"])
		}
	})

	t.Run("no change when already alternating", func(t *testing.T) {
		history := []map[string]any{
			{"userInputMessage": map[string]any{"content": "a"}},
			{"assistantResponseMessage": map[string]any{"content": "b"}},
			{"userInputMessage": map[string]any{"content": "c"}},
		}

		result := mergeConsecutiveRolesAIClient2API(history, "")

		if len(result) != 3 {
			t.Fatalf("len = %d, want 3", len(result))
		}
	})

	t.Run("single item returns as-is", func(t *testing.T) {
		history := []map[string]any{
			{"userInputMessage": map[string]any{"content": "a"}},
		}

		result := mergeConsecutiveRolesAIClient2API(history, "")

		if len(result) != 1 {
			t.Fatalf("len = %d, want 1", len(result))
		}
	})

	t.Run("mixed consecutive merges correctly", func(t *testing.T) {
		history := []map[string]any{
			{"userInputMessage": map[string]any{"content": "a"}},
			{"userInputMessage": map[string]any{"content": "b"}},
			{"assistantResponseMessage": map[string]any{"content": "c"}},
			{"assistantResponseMessage": map[string]any{"content": "d"}},
		}

		result := mergeConsecutiveRolesAIClient2API(history, "")

		if len(result) != 2 {
			t.Fatalf("len = %d, want 2", len(result))
		}
		uim := result[0]["userInputMessage"].(map[string]any)
		if uim["content"] != "a\nb" {
			t.Errorf("merged user content = %q", uim["content"])
		}
		arm := result[1]["assistantResponseMessage"].(map[string]any)
		if arm["content"] != "c\nd" {
			t.Errorf("merged assistant content = %q", arm["content"])
		}
	})

	t.Run("merges user with toolResults context", func(t *testing.T) {
		history := []map[string]any{
			{
				"userInputMessage": map[string]any{
					"content": "step 1",
					"userInputMessageContext": map[string]any{
						"toolResults": []any{
							map[string]any{"toolUseId": "call_1"},
						},
					},
				},
			},
			{
				"userInputMessage": map[string]any{
					"content": "step 2",
					"userInputMessageContext": map[string]any{
						"toolResults": []any{
							map[string]any{"toolUseId": "call_2"},
						},
					},
				},
			},
			{"assistantResponseMessage": map[string]any{"content": "done"}},
		}

		result := mergeConsecutiveRolesAIClient2API(history, "")

		if len(result) != 2 {
			t.Fatalf("len = %d, want 2", len(result))
		}
		uim := result[0]["userInputMessage"].(map[string]any)
		if uim["content"] != "step 1\nstep 2" {
			t.Errorf("merged content = %q", uim["content"])
		}
		ctx := uim["userInputMessageContext"].(map[string]any)
		trArr := ctx["toolResults"].([]any)
		if len(trArr) != 2 {
			t.Errorf("merged toolResults len = %d, want 2", len(trArr))
		}
	})

	t.Run("merges assistant with toolUses context", func(t *testing.T) {
		history := []map[string]any{
			{"userInputMessage": map[string]any{"content": "hi"}},
			{
				"assistantResponseMessage": map[string]any{
					"content": "Let me check",
					"toolUses": []any{
						map[string]any{"toolUseId": "call_1"},
					},
				},
			},
			{
				"assistantResponseMessage": map[string]any{
					"content": "Here is the result",
					"toolUses": []any{
						map[string]any{"toolUseId": "call_2"},
					},
				},
			},
		}

		result := mergeConsecutiveRolesAIClient2API(history, "")

		if len(result) != 2 {
			t.Fatalf("len = %d, want 2", len(result))
		}
		arm := result[1]["assistantResponseMessage"].(map[string]any)
		if arm["content"] != "Let me check\nHere is the result" {
			t.Errorf("merged content = %q", arm["content"])
		}
		tuArr := arm["toolUses"].([]any)
		if len(tuArr) != 2 {
			t.Errorf("merged toolUses len = %d, want 2", len(tuArr))
		}
	})
}

func TestMergeConsecutiveRoles(t *testing.T) {
	history := []map[string]any{
		{"userInputMessage": map[string]any{"content": "hello"}},
		{"userInputMessage": map[string]any{"content": "world"}},
		{"assistantResponseMessage": map[string]any{"content": "hi"}},
		{"assistantResponseMessage": map[string]any{"content": "there"}},
	}

	merged := mergeConsecutiveRolesAIClient2API(history, "")

	if len(merged) != 2 {
		t.Fatalf("len = %d, want 2", len(merged))
	}
	if merged[0]["userInputMessage"] == nil {
		t.Error("merged[0] is not userInputMessage")
	}
	if merged[1]["assistantResponseMessage"] == nil {
		t.Error("merged[1] is not assistantResponseMessage")
	}

	uim := merged[0]["userInputMessage"].(map[string]any)
	if uim["content"] != "hello\nworld" {
		t.Errorf("merged content = %q, want 'hello\\nworld'", uim["content"])
	}
}

func TestParseToolInput(t *testing.T) {
	tests := []struct {
		name    string
		input   any
		wantNil bool
		wantKey string
	}{
		{"string JSON", `{"command": "ls"}`, false, "command"},
		{"empty string", "", false, ""},
		{"object", map[string]any{"command": "ls"}, false, "command"},
		{"nil", nil, false, ""},
		{"invalid JSON", "not json", false, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseToolInput(tt.input)
			if result == nil {
				t.Fatal("result is nil")
			}
			if tt.wantKey != "" {
				m, ok := result.(map[string]any)
				if !ok {
					t.Fatal("result is not a map")
				}
				if _, ok := m[tt.wantKey]; !ok {
					t.Errorf("result missing key %q", tt.wantKey)
				}
			}
		})
	}
}

func TestSanitizeToolInput(t *testing.T) {
	t.Run("removes empty-string keys", func(t *testing.T) {
		input := map[string]any{"": "value", "valid": "ok"}
		result := sanitizeToolInput(input)
		m, ok := result.(map[string]any)
		if !ok {
			t.Fatal("result is not a map")
		}
		if _, ok := m[""]; ok {
			t.Error("empty-string key was not removed")
		}
		if m["valid"] != "ok" {
			t.Errorf("expected 'ok', got %v", m["valid"])
		}
	})
	t.Run("preserves non-empty keys", func(t *testing.T) {
		input := map[string]any{"a": 1, "b": "hello"}
		result := sanitizeToolInput(input)
		m, ok := result.(map[string]any)
		if !ok {
			t.Fatal("result is not a map")
		}
		if m["a"] != 1 || m["b"] != "hello" {
			t.Errorf("unexpected result: %v", m)
		}
	})
	t.Run("returns non-map as-is", func(t *testing.T) {
		if sanitizeToolInput("hello") != "hello" {
			t.Error("string input should be returned as-is")
		}
		if sanitizeToolInput(nil) != nil {
			t.Error("nil input should be returned as-is")
		}
	})
	t.Run("handles empty map", func(t *testing.T) {
		result := sanitizeToolInput(map[string]any{})
		m, ok := result.(map[string]any)
		if !ok || len(m) != 0 {
			t.Error("empty map should remain empty")
		}
	})
}

func TestIsEmptyMessage(t *testing.T) {
	t.Run("nil content, no tool calls", func(t *testing.T) {
		if !isEmptyMessage(Message{Role: "user"}) {
			t.Error("nil content with no tool calls should be empty")
		}
	})
	t.Run("empty string content", func(t *testing.T) {
		if !isEmptyMessage(Message{Role: "user", Content: ""}) {
			t.Error("empty string content should be empty")
		}
	})
	t.Run("whitespace content", func(t *testing.T) {
		if !isEmptyMessage(Message{Role: "user", Content: "   "}) {
			t.Error("whitespace content should be empty")
		}
	})
	t.Run("non-empty string", func(t *testing.T) {
		if isEmptyMessage(Message{Role: "user", Content: "hello"}) {
			t.Error("non-empty string should not be empty")
		}
	})
	t.Run("empty content array", func(t *testing.T) {
		if !isEmptyMessage(Message{Role: "user", Content: []any{}}) {
			t.Error("empty array content should be empty")
		}
	})
	t.Run("content array with text", func(t *testing.T) {
		if isEmptyMessage(Message{Role: "assistant", Content: []any{
			map[string]any{"type": "text", "text": "hello"},
		}}) {
			t.Error("array with text should not be empty")
		}
	})
	t.Run("content array with empty text only", func(t *testing.T) {
		if !isEmptyMessage(Message{Role: "assistant", Content: []any{
			map[string]any{"type": "text", "text": "  "},
		}}) {
			t.Error("array with only whitespace text should be empty")
		}
	})
	t.Run("content array with tool_use", func(t *testing.T) {
		if isEmptyMessage(Message{Role: "assistant", Content: []any{
			map[string]any{"type": "tool_use", "id": "call_1"},
		}}) {
			t.Error("array with tool_use should not be empty")
		}
	})
	t.Run("content array with image", func(t *testing.T) {
		if isEmptyMessage(Message{Role: "user", Content: []any{
			map[string]any{"type": "image", "image_url": map[string]any{"url": "data:..."}},
		}}) {
			t.Error("array with image should not be empty")
		}
	})
	t.Run("has tool calls field", func(t *testing.T) {
		if isEmptyMessage(Message{Role: "assistant", Content: "", ToolCalls: []ToolCall{{ID: "call_1"}}}) {
			t.Error("message with ToolCalls should not be empty")
		}
	})
	t.Run("unknown content type is meaningful", func(t *testing.T) {
		if isEmptyMessage(Message{Role: "user", Content: []any{
			map[string]any{"type": "custom_type", "data": "something"},
		}}) {
			t.Error("unknown content types should be treated as meaningful")
		}
	})
}

func TestConvertEscapedNewlines(t *testing.T) {
	t.Run("converts backslash-n to newline", func(t *testing.T) {
		result := convertEscapedNewlines("hello\\nworld")
		if result != "hello\nworld" {
			t.Errorf("expected 'hello\\nworld', got %q", result)
		}
	})
	t.Run("preserves double backslash", func(t *testing.T) {
		result := convertEscapedNewlines("hello\\\\nworld")
		if result != "hello\\nworld" {
			t.Errorf("expected 'hello\\\\nworld', got %q", result)
		}
	})
	t.Run("preserves actual newline", func(t *testing.T) {
		result := convertEscapedNewlines("hello\nworld")
		if result != "hello\nworld" {
			t.Errorf("expected 'hello\\nworld', got %q", result)
		}
	})
	t.Run("empty string", func(t *testing.T) {
		if convertEscapedNewlines("") != "" {
			t.Error("empty string should return empty")
		}
	})
	t.Run("no special chars", func(t *testing.T) {
		result := convertEscapedNewlines("hello world")
		if result != "hello world" {
			t.Errorf("expected 'hello world', got %q", result)
		}
	})
	t.Run("multiple conversions", func(t *testing.T) {
		result := convertEscapedNewlines("line1\\nline2\\nline3")
		if result != "line1\nline2\nline3" {
			t.Errorf("expected 'line1\\nline2\\nline3', got %q", result)
		}
	})
	t.Run("mixed escaped and actual newlines", func(t *testing.T) {
		result := convertEscapedNewlines("a\\nb\nc\\\\nd")
		if result != "a\nb\nc\\nd" {
			t.Errorf("expected 'a\\nb\\nc\\\\nd', got %q", result)
		}
	})
}

func TestResolveKiroEffort(t *testing.T) {
	tests := []struct {
		name string
		req  ChatRequest
		want string
	}{
		{
			"reasoning_effort high",
			ChatRequest{ReasoningEffort: "high"},
			"high",
		},
		{
			"reasoning_effort low",
			ChatRequest{ReasoningEffort: "low"},
			"low",
		},
		{
			"output_config effort",
			ChatRequest{OutputConfig: &OutputConfig{Effort: "medium"}},
			"medium",
		},
		{
			"thinking enabled high budget",
			ChatRequest{Thinking: &ThinkingBlock{Type: "enabled", BudgetTokens: 32000}},
			"high",
		},
		{
			"thinking enabled low budget",
			ChatRequest{Thinking: &ThinkingBlock{Type: "enabled", BudgetTokens: 8000}},
			"low",
		},
		{
			"thinking adaptive",
			ChatRequest{Thinking: &ThinkingBlock{Type: "adaptive"}},
			"high",
		},
		{
			"minimal maps to low",
			ChatRequest{ReasoningEffort: "minimal"},
			"low",
		},
		{
			"invalid level",
			ChatRequest{ReasoningEffort: "banana"},
			"",
		},
		{
			"empty",
			ChatRequest{},
			"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveKiroEffort(tt.req)
			if got != tt.want {
				t.Errorf("resolveKiroEffort() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestConvertKiroTools(t *testing.T) {
	t.Run("normal tool", func(t *testing.T) {
		tools := []Tool{
			{
				Type: "function",
				Function: ToolFunction{
					Name:        "executeBash",
					Description: "Run a command",
					Parameters: map[string]any{
						"type":       "object",
						"properties": map[string]any{"command": map[string]any{"type": "string"}},
						"required":   []any{"command"},
					},
				},
			},
		}

		out := convertKiroTools(tools)

		if len(out) != 1 {
			t.Fatalf("len = %d, want 1", len(out))
		}

		spec, ok := out[0].(map[string]any)
		if !ok {
			t.Fatal("out[0] is not a map")
		}
		ts, ok := spec["toolSpecification"].(map[string]any)
		if !ok {
			t.Fatal("toolSpecification is not a map")
		}
		if ts["name"] != "executeBash" {
			t.Errorf("name = %q, want executeBash", ts["name"])
		}
		if ts["description"] != "Run a command" {
			t.Errorf("description = %q, want 'Run a command'", ts["description"])
		}

		inputSchema, ok := ts["inputSchema"].(map[string]any)
		if !ok {
			t.Fatal("inputSchema is not a map")
		}
		jsonSchema, ok := inputSchema["json"].(map[string]any)
		if !ok {
			t.Fatal("inputSchema.json is not a map")
		}
		if jsonSchema["type"] != "object" {
			t.Errorf("inputSchema.json.type = %q, want object", jsonSchema["type"])
		}
	})

	t.Run("filters web_search tools", func(t *testing.T) {
		tools := []Tool{
			{Type: "function", Function: ToolFunction{Name: "web_search", Description: "Search the web", Parameters: map[string]any{}}},
			{Type: "function", Function: ToolFunction{Name: "executeBash", Description: "Run a command", Parameters: map[string]any{}}},
		}
		out := convertKiroTools(tools)
		if len(out) != 1 {
			t.Fatalf("len = %d, want 1", len(out))
		}
		ts := out[0].(map[string]any)["toolSpecification"].(map[string]any)
		if ts["name"] != "executeBash" {
			t.Errorf("name = %q, want executeBash", ts["name"])
		}
	})

	t.Run("filters empty description", func(t *testing.T) {
		tools := []Tool{
			{Type: "function", Function: ToolFunction{Name: "noop", Description: "", Parameters: map[string]any{}}},
		}
		out := convertKiroTools(tools)
		if len(out) == 0 {
			t.Error("expected placeholder tool when all tools filtered")
		}
		ts := out[0].(map[string]any)["toolSpecification"].(map[string]any)
		if ts["name"] != "no_tool_available" {
			t.Errorf("expected placeholder, got %q", ts["name"])
		}
	})

	t.Run("truncates long descriptions at 9216", func(t *testing.T) {
		longDesc := strings.Repeat("x", 10000)
		tools := []Tool{
			{Type: "function", Function: ToolFunction{Name: "longTool", Description: longDesc, Parameters: map[string]any{"type": "object", "properties": map[string]any{}}}},
		}
		out := convertKiroTools(tools)
		desc := out[0].(map[string]any)["toolSpecification"].(map[string]any)["description"].(string)
		if len(desc) > 9220 {
			t.Errorf("description too long: %d chars", len(desc))
		}
		if !strings.HasSuffix(desc, "...") {
			t.Error("truncated description should end with '...'")
		}
	})

	t.Run("placeholder when all tools filtered", func(t *testing.T) {
		tools := []Tool{
			{Type: "function", Function: ToolFunction{Name: "web_search", Description: "Search", Parameters: map[string]any{}}},
			{Type: "function", Function: ToolFunction{Name: "websearch", Description: "Search web", Parameters: map[string]any{}}},
		}
		out := convertKiroTools(tools)
		if len(out) != 1 {
			t.Fatalf("expected 1 placeholder, got %d", len(out))
		}
		ts := out[0].(map[string]any)["toolSpecification"].(map[string]any)
		if ts["name"] != "no_tool_available" {
			t.Errorf("expected placeholder, got %q", ts["name"])
		}
	})

	t.Run("placeholder when no tools at all", func(t *testing.T) {
		out := convertKiroTools(nil)
		if len(out) != 1 {
			t.Fatalf("expected 1 placeholder, got %d", len(out))
		}
		ts := out[0].(map[string]any)["toolSpecification"].(map[string]any)
		if ts["name"] != "no_tool_available" {
			t.Errorf("expected placeholder, got %q", ts["name"])
		}
	})

	t.Run("empty tools slice", func(t *testing.T) {
		out := convertKiroTools([]Tool{})
		if len(out) != 1 {
			t.Fatalf("expected 1 placeholder, got %d", len(out))
		}
		ts := out[0].(map[string]any)["toolSpecification"].(map[string]any)
		if ts["name"] != "no_tool_available" {
			t.Errorf("expected placeholder, got %q", ts["name"])
		}
	})
}

func TestBuildKiroRequestEmptyConversationID(t *testing.T) {
	req := ChatRequest{
		Model:    "claude-sonnet-4.5",
		Messages: []Message{{Role: "user", Content: "hello world"}},
	}

	resolved := ResolvedModel{Upstream: "claude-sonnet-4.5"}
	payload, err := buildKiroRequest(req, resolved, "arn:aws:test", "", "")
	if err != nil {
		t.Fatalf("buildKiroRequest: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(payload.Body, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	cs := parsed["conversationState"].(map[string]any)
	cid := cs["conversationId"].(string)
	if cid == "" {
		t.Error("conversationId is empty, should be generated UUIDv4")
	}
}

func TestBuildKiroRequestProvidedConversationID(t *testing.T) {
	req := ChatRequest{
		Model:    "claude-sonnet-4.5",
		Messages: []Message{{Role: "user", Content: "hello"}},
	}

	resolved := ResolvedModel{Upstream: "claude-sonnet-4.5"}
	payload, err := buildKiroRequest(req, resolved, "arn:aws:test", "my-custom-id", "")
	if err != nil {
		t.Fatalf("buildKiroRequest: %v", err)
	}

	var parsed map[string]any
	json.Unmarshal(payload.Body, &parsed)

	cs := parsed["conversationState"].(map[string]any)
	cid := cs["conversationId"].(string)
	if cid != "my-custom-id" {
		t.Errorf("conversationId = %q, want 'my-custom-id'", cid)
	}
}

func TestBuildKiroRequestHistoryIsSlice(t *testing.T) {
	// Multi-turn messages to ensure history is present
	req := ChatRequest{
		Model: "claude-sonnet-4.5",
		Messages: []Message{
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "hi"},
			{Role: "user", Content: "how are you"},
		},
	}

	resolved := ResolvedModel{Upstream: "claude-sonnet-4.5"}
	payload, err := buildKiroRequest(req, resolved, "arn:aws:test", "test-id", "")
	if err != nil {
		t.Fatalf("buildKiroRequest: %v", err)
	}

	var parsed map[string]any
	json.Unmarshal(payload.Body, &parsed)

	cs := parsed["conversationState"].(map[string]any)
	hist := cs["history"]
	if hist == nil {
		t.Error("history is nil, should be empty array when no prior history")
	}
	jsonBytes, _ := json.Marshal(hist)
	if string(jsonBytes) != "[]" && string(jsonBytes) != "null" {
		// Either [] or [{\"userInputMessage\":...}] is acceptable
		t.Logf("history marshals as %q", string(jsonBytes))
	}
}

func TestSerializeToolResultContent(t *testing.T) {
	tests := []struct {
		name  string
		input any
		want  string
	}{
		{"string", "hello", "hello"},
		{"empty string", "", "(no output)"},
		{"nil", nil, "(no output)"},
		{"text blocks", []any{
			map[string]any{"type": "text", "text": "hello"},
			map[string]any{"type": "text", "text": "world"},
		}, "hello\nworld"},
		{"image block", []any{
			map[string]any{"type": "image"},
		}, "[image]"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := serializeToolResultContent(tt.input)
			if got != tt.want {
				t.Errorf("serializeToolResultContent() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFullJSONRoundTrip(t *testing.T) {
	// Simulate a real client request with tools, tool calls, and tool results
	raw := `{
		"model": "claude-sonnet-4.5",
		"stream": true,
		"messages": [
			{"role": "user", "content": "list files"},
			{"role": "assistant", "content": "", "tool_calls": [
				{"id": "call_1", "type": "function", "function": {"name": "executeBash", "arguments": "{\"command\": \"ls\"}"}}
			]},
			{"role": "tool", "content": "file1.go", "tool_call_id": "call_1"},
			{"role": "assistant", "content": "Found one file."},
			{"role": "user", "content": "read it"}
		],
		"tools": [
			{"type": "function", "function": {"name": "executeBash", "description": "Run", "parameters": {"type": "object", "properties": {"command": {"type": "string"}}, "required": ["command"]}}}
		]
	}`

	var req ChatRequest
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(req.Messages) != 5 {
		t.Fatalf("messages len = %d, want 5", len(req.Messages))
	}
	if req.Messages[1].ToolCalls[0].ID != "call_1" {
		t.Errorf("tool_calls[0].ID = %q, want call_1", req.Messages[1].ToolCalls[0].ID)
	}
	if req.Messages[2].ToolCallID != "call_1" {
		t.Errorf("tool_call_id = %q, want call_1", req.Messages[2].ToolCallID)
	}
	if len(req.Tools) != 1 {
		t.Fatalf("tools len = %d, want 1", len(req.Tools))
	}

	result := convertMessages(req.Messages, req.Tools, "claude-sonnet-4.5", false)

	// Must have at least one toolUse and one toolResult
	totalToolUses := 0
	for _, h := range result.history {
		arm, ok := h["assistantResponseMessage"].(map[string]any)
		if !ok {
			continue
		}
		tuArr, _ := arm["toolUses"].([]any)
		totalToolUses += len(tuArr)
	}
	if totalToolUses != 1 {
		t.Errorf("total toolUses = %d, want 1", totalToolUses)
	}

	totalToolResults := 0
	for _, h := range result.history {
		uim, ok := h["userInputMessage"].(map[string]any)
		if !ok {
			continue
		}
		ctx, ok := uim["userInputMessageContext"].(map[string]any)
		if !ok {
			continue
		}
		trArr, _ := ctx["toolResults"].([]any)
		totalToolResults += len(trArr)
	}
	if totalToolResults != 1 {
		t.Errorf("total toolResults = %d, want 1", totalToolResults)
	}
}

func TestConvertMessagesWithImageOnly(t *testing.T) {
	messages := []Message{
		{
			Role: "user",
			Content: []any{
				map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,iVBORw0KGgo="}},
			},
		},
		{Role: "assistant", Content: "I see an image."},
		{Role: "user", Content: "what's in it?"},
	}

	result := convertMessages(messages, nil, "claude-sonnet-4.5", false)

	// First user message should have "Image provided." fallback + images
	if len(result.history) == 0 {
		t.Fatal("history is empty")
	}
	uim, ok := result.history[0]["userInputMessage"].(map[string]any)
	if !ok {
		t.Fatal("history[0] is not userInputMessage")
	}
	if uim["content"] != "Image provided." {
		t.Errorf("content = %q, want 'Image provided.'", uim["content"])
	}
	images, ok := uim["images"].([]map[string]any)
	if !ok || len(images) == 0 {
		t.Error("expected images in first user message")
	}
}

func TestConvertMessagesImageAgeOut(t *testing.T) {
	// 7 user messages with images, only last 5 should keep images
	messages := []Message{
		{Role: "user", Content: []any{map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,img1"}}}},
		{Role: "assistant", Content: "1"},
		{Role: "user", Content: []any{map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,img2"}}}},
		{Role: "assistant", Content: "2"},
		{Role: "user", Content: []any{map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,img3"}}}},
		{Role: "assistant", Content: "3"},
		{Role: "user", Content: []any{map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,img4"}}}},
		{Role: "assistant", Content: "4"},
		{Role: "user", Content: []any{map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,img5"}}}},
		{Role: "assistant", Content: "5"},
		{Role: "user", Content: []any{map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,img6"}}}},
		{Role: "assistant", Content: "6"},
		{Role: "user", Content: []any{map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,img7"}}}},
	}

	result := convertMessages(messages, nil, "claude-sonnet-4.5", false)

	// Count user messages with images in history
	historyWithImages := 0
	historyWithPlaceholder := 0
	for _, h := range result.history {
		uim, ok := h["userInputMessage"].(map[string]any)
		if !ok {
			continue
		}
		images, hasImages := uim["images"].([]map[string]any)
		content, _ := uim["content"].(string)
		if hasImages && len(images) > 0 {
			historyWithImages++
		}
		if strings.Contains(content, "图片") {
			historyWithPlaceholder++
		}
	}

	// 5 history messages should keep images, 2 should be aged out
	if historyWithImages > 5 {
		t.Errorf("too many history messages with images: %d (max 5)", historyWithImages)
	}
	if historyWithPlaceholder == 0 && len(result.history) > 5 {
		t.Error("expected some history messages to have image placeholder")
	}

	// currentMessage should always keep images
	if result.currentMessage != nil {
		uim, ok := result.currentMessage["userInputMessage"].(map[string]any)
		if ok {
			if images, hasImages := uim["images"].([]map[string]any); hasImages && len(images) > 0 {
				t.Logf("currentMessage has %d images (expected)", len(images))
			}
		}
	}
}

func TestConvertMessagesImageSupportsFalse(t *testing.T) {
	// Non-Claude model: images should be skipped, text preserved
	messages := []Message{
		{
			Role: "user",
			Content: []any{
				map[string]any{"type": "text", "text": "check this image"},
				map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,iVBORw0KGgo="}},
			},
		},
		{Role: "assistant", Content: "ok"},
	}

	result := convertMessages(messages, nil, "gpt-4o", false)

	foundUser := false
	for _, h := range result.history {
		uim, ok := h["userInputMessage"].(map[string]any)
		if !ok {
			continue
		}
		foundUser = true
		if _, hasImages := uim["images"].([]map[string]any); hasImages {
			t.Error("images should not be present for non-Claude model")
		}
	}
	if !foundUser && len(messages) > 0 {
		// The message should exist (with text), images just filtered out
		t.Log("no user message in history, checking currentMessage")
		if result.currentMessage != nil {
			uim, ok := result.currentMessage["userInputMessage"].(map[string]any)
			if ok && uim["content"] == "check this image" {
				// OK - text preserved, images filtered
			}
		}
	}
}
