package kiro

import (
	"encoding/json"
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

func TestConvertMessagesFlattensToolInteractionsWithoutTools(t *testing.T) {
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

	// Without tools, tool interactions should be flattened
	for _, h := range result.history {
		arm, ok := h["assistantResponseMessage"].(map[string]any)
		if !ok {
			continue
		}
		if tu, ok := arm["toolUses"].([]any); ok && len(tu) > 0 {
			t.Error("toolUses found in history when tools not provided")
		}
	}
}

func TestEnsureAlternatingRoles(t *testing.T) {
	t.Run("merges consecutive user messages", func(t *testing.T) {
		history := []map[string]any{
			{"userInputMessage": map[string]any{"content": "a"}},
			{"userInputMessage": map[string]any{"content": "b"}},
			{"assistantResponseMessage": map[string]any{"content": "c"}},
		}

		result := ensureAlternatingRoles(history)

		if len(result) != 2 {
			t.Fatalf("len = %d, want 2", len(result))
		}
		if result[0]["userInputMessage"] == nil {
			t.Error("result[0] should be user")
		}
		uim := result[0]["userInputMessage"].(map[string]any)
		if uim["content"] != "a\n\nb" {
			t.Errorf("merged content = %q, want 'a\\n\\nb'", uim["content"])
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

		result := ensureAlternatingRoles(history)

		if len(result) != 2 {
			t.Fatalf("len = %d, want 2", len(result))
		}
		if result[1]["assistantResponseMessage"] == nil {
			t.Error("result[1] should be assistant")
		}
		arm := result[1]["assistantResponseMessage"].(map[string]any)
		if arm["content"] != "b\n\nc" {
			t.Errorf("merged content = %q, want 'b\\n\\nc'", arm["content"])
		}
	})

	t.Run("no change when already alternating", func(t *testing.T) {
		history := []map[string]any{
			{"userInputMessage": map[string]any{"content": "a"}},
			{"assistantResponseMessage": map[string]any{"content": "b"}},
			{"userInputMessage": map[string]any{"content": "c"}},
		}

		result := ensureAlternatingRoles(history)

		if len(result) != 3 {
			t.Fatalf("len = %d, want 3", len(result))
		}
	})

	t.Run("single item returns as-is", func(t *testing.T) {
		history := []map[string]any{
			{"userInputMessage": map[string]any{"content": "a"}},
		}

		result := ensureAlternatingRoles(history)

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

		result := ensureAlternatingRoles(history)

		if len(result) != 2 {
			t.Fatalf("len = %d, want 2", len(result))
		}
		uim := result[0]["userInputMessage"].(map[string]any)
		if uim["content"] != "a\n\nb" {
			t.Errorf("merged user content = %q", uim["content"])
		}
		arm := result[1]["assistantResponseMessage"].(map[string]any)
		if arm["content"] != "c\n\nd" {
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

		result := ensureAlternatingRoles(history)

		if len(result) != 2 {
			t.Fatalf("len = %d, want 2", len(result))
		}
		uim := result[0]["userInputMessage"].(map[string]any)
		if uim["content"] != "step 1\n\nstep 2" {
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

		result := ensureAlternatingRoles(history)

		if len(result) != 2 {
			t.Fatalf("len = %d, want 2", len(result))
		}
		arm := result[1]["assistantResponseMessage"].(map[string]any)
		if arm["content"] != "Let me check\n\nHere is the result" {
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

	merged := mergeConsecutiveRoles(history)

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
	if uim["content"] != "hello\n\nworld" {
		t.Errorf("merged content = %q, want 'hello\\n\\nworld'", uim["content"])
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

func TestStableToolUseID(t *testing.T) {
	id1 := stableToolUseID("ls", 0)
	id2 := stableToolUseID("ls", 0)
	id3 := stableToolUseID("ls", 1)

	if id1 == "" {
		t.Error("stableToolUseID returned empty")
	}
	if id1 != id2 {
		t.Errorf("same input produced different IDs: %q vs %q", id1, id2)
	}
	if id1 == id3 {
		t.Error("different inputs produced same ID")
	}
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

func TestConvertTools(t *testing.T) {
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

	var toolDocs []string
	out := convertTools(tools, "claude-sonnet-4.5", &toolDocs)

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
}

func TestConvertToolsLongDescription(t *testing.T) {
	longDesc := ""
	for i := 0; i < 20000; i++ {
		longDesc += "x"
	}

	tools := []Tool{
		{
			Type: "function",
			Function: ToolFunction{
				Name:        "longTool",
				Description: longDesc,
				Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
			},
		},
	}

	var toolDocs []string
	out := convertTools(tools, "claude-sonnet-4.5", &toolDocs)

	ts := out[0].(map[string]any)["toolSpecification"].(map[string]any)
	desc := ts["description"].(string)

	if desc == longDesc {
		t.Error("long description was not truncated")
	}
	if len(toolDocs) == 0 {
		t.Error("toolDocs not populated for long description")
	}
}

func TestWrapSystemReminder(t *testing.T) {
	got := wrapSystemReminder("hello")
	want := "<system-reminder>\nhello\n</system-reminder>"
	if got != want {
		t.Errorf("wrapSystemReminder() = %q, want %q", got, want)
	}
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
	if err := json.Unmarshal(payload, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	cs := parsed["conversationState"].(map[string]any)
	cid := cs["conversationId"].(string)
	if cid == "" {
		t.Error("conversationId is empty, should be generated UUIDv5")
	}

	payload2, _ := buildKiroRequest(req, resolved, "arn:aws:test", "", "")
	var parsed2 map[string]any
	json.Unmarshal(payload2, &parsed2)
	cid2 := parsed2["conversationState"].(map[string]any)["conversationId"].(string)
	if cid != cid2 {
		t.Errorf("same input produced different conversationIds: %q vs %q", cid, cid2)
	}

	req2 := ChatRequest{
		Model:    "claude-sonnet-4.5",
		Messages: []Message{{Role: "user", Content: "different message"}},
	}
	payload3, _ := buildKiroRequest(req2, resolved, "arn:aws:test", "", "")
	var parsed3 map[string]any
	json.Unmarshal(payload3, &parsed3)
	cid3 := parsed3["conversationState"].(map[string]any)["conversationId"].(string)
	if cid == cid3 {
		t.Error("different input produced same conversationId")
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
	json.Unmarshal(payload, &parsed)

	cs := parsed["conversationState"].(map[string]any)
	cid := cs["conversationId"].(string)
	if cid != "my-custom-id" {
		t.Errorf("conversationId = %q, want 'my-custom-id'", cid)
	}
}

func TestBuildKiroRequestHistoryIsSlice(t *testing.T) {
	req := ChatRequest{
		Model:    "claude-sonnet-4.5",
		Messages: []Message{{Role: "user", Content: "hello"}},
	}

	resolved := ResolvedModel{Upstream: "claude-sonnet-4.5"}
	payload, err := buildKiroRequest(req, resolved, "arn:aws:test", "test-id", "")
	if err != nil {
		t.Fatalf("buildKiroRequest: %v", err)
	}

	var parsed map[string]any
	json.Unmarshal(payload, &parsed)

	cs := parsed["conversationState"].(map[string]any)
	hist := cs["history"]
	if hist == nil {
		t.Error("history is nil, should be empty array")
	}
	jsonBytes, _ := json.Marshal(hist)
	if string(jsonBytes) != "[]" {
		t.Errorf("history marshals as %q, want []", string(jsonBytes))
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
