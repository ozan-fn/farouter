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

	result := convertMessages(messages, tools, "claude-sonnet-4-5", false)

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

	result := convertMessages(messages, nil, "claude-sonnet-4-5", false)

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

// TestConvertMessagesToolResultIDMismatch verifies the fix for
// TOOL_USE_RESULT_MISMATCH when currentMessage HAS toolResults but with WRONG
// toolUseIds. This happens when the last assistant has toolUses=["call_A"] but
// the current toolResults reference a DIFFERENT ID ("call_B" from an earlier
// round). Bedrock rejects this as TOOL_USE_RESULT_MISMATCH.
func TestConvertMessagesToolResultIDMismatch(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "list files"},
		{
			Role:    "assistant",
			Content: "",
			ToolCalls: []ToolCall{
				{ID: "call_A", Type: "function", Function: ToolCallFunction{Name: "ls", Arguments: `{}`}},
			},
		},
		{Role: "tool", Content: "file1.go", ToolCallID: "call_A"},
		{
			Role:    "assistant",
			Content: "",
			ToolCalls: []ToolCall{
				{ID: "call_B", Type: "function", Function: ToolCallFunction{Name: "readFile", Arguments: `{}`}},
			},
		},
		{Role: "tool", Content: "package main", ToolCallID: "call_B"},
		{Role: "assistant", Content: "Done."},
		{Role: "user", Content: "thanks"},
		{
			Role:    "assistant",
			Content: "",
			ToolCalls: []ToolCall{
				{ID: "call_C", Type: "function", Function: ToolCallFunction{Name: "editFile", Arguments: `{}`}},
			},
		},
	}

	result := convertMessages(messages, nil, "claude-sonnet-4-5", false)

	// History should end with assistantResponseMessage with toolUses=[call_C]
	if len(result.history) == 0 {
		t.Fatal("history should not be empty")
	}
	last := result.history[len(result.history)-1]
	arm, ok := last["assistantResponseMessage"].(map[string]any)
	if !ok {
		t.Fatal("history should end with assistantResponseMessage")
	}
	tuArr, has := arm["toolUses"].([]any)
	if !has || len(tuArr) == 0 {
		t.Fatal("assistantResponseMessage should have toolUses")
	}
	// Verify the toolUse is call_C
	if tu, ok := tuArr[0].(map[string]any); ok {
		if tu["toolUseId"] != "call_C" {
			t.Errorf("last toolUseId = %q, want 'call_C'", tu["toolUseId"])
		}
	}

	// currentMessage must be "Continue" because the stale currentMessage
	// (which references old "thanks" text) doesn't have matching toolResults
	// for call_C → replaced with "Continue"
	if result.currentMessage == nil {
		t.Fatal("currentMessage is nil")
	}
	uim, ok := result.currentMessage["userInputMessage"].(map[string]any)
	if !ok {
		t.Fatal("currentMessage.userInputMessage is not a map")
	}
	if uim["content"] != "Continue" {
		t.Errorf("currentMessage content = %q, want 'Continue'", uim["content"])
	}
}

// TestConvertMessagesToolResultIDMismatchWithResults verifies that when
// currentMessage has toolResults with WRONG IDs (from a different turn),
// the stale fix replaces currentMessage with "Continue" instead of
// sending mismatched IDs to Bedrock.
func TestConvertMessagesToolResultIDMismatchWithResults(t *testing.T) {
	// This scenario simulates when the last OpenAI message is
	// assistant(tool_calls=[call_B]) but the currentMessage somehow
	// still has toolResults from a PREVIOUS round (call_A).
	// The fix must detect that call_A doesn't match expected call_B
	// and replace currentMessage with "Continue".
	messages := []Message{
		{Role: "user", Content: "list files"},
		{
			Role:    "assistant",
			Content: "",
			ToolCalls: []ToolCall{
				{ID: "call_A", Type: "function", Function: ToolCallFunction{Name: "ls", Arguments: `{}`}},
			},
		},
		{Role: "tool", Content: "file1.go", ToolCallID: "call_A"},
		{
			Role:    "assistant",
			Content: "",
			ToolCalls: []ToolCall{
				{ID: "call_B", Type: "function", Function: ToolCallFunction{Name: "readFile", Arguments: `{}`}},
			},
		},
	}

	result := convertMessages(messages, nil, "claude-sonnet-4-5", false)

	// History should end with assistantResponseMessage with toolUses=[call_B]
	last := result.history[len(result.history)-1]
	arm, ok := last["assistantResponseMessage"].(map[string]any)
	if !ok {
		t.Fatal("history should end with assistantResponseMessage")
	}
	tuArr, has := arm["toolUses"].([]any)
	if !has || len(tuArr) == 0 {
		t.Fatal("assistantResponseMessage should have toolUses")
	}
	if tu, ok := tuArr[0].(map[string]any); ok {
		if tu["toolUseId"] != "call_B" {
			t.Errorf("last toolUseId = %q, want 'call_B'", tu["toolUseId"])
		}
	}

	// currentMessage should be "Continue" with synthetic toolResults for call_B
	// (to satisfy Bedrock's requirement that every tool_use must have a tool_result)
	if result.currentMessage == nil {
		t.Fatal("currentMessage is nil")
	}
	uim, ok := result.currentMessage["userInputMessage"].(map[string]any)
	if !ok {
		t.Fatal("currentMessage.userInputMessage is not a map")
	}
	if uim["content"] != "Continue" {
		t.Errorf("currentMessage content = %q, want 'Continue'", uim["content"])
	}
	// Verify synthetic toolResults are present with correct ID for call_B
	ctx, ok := uim["userInputMessageContext"].(map[string]any)
	if !ok {
		t.Fatal("currentMessage should have userInputMessageContext with synthetic toolResults")
	}
	trArr, ok := ctx["toolResults"].([]any)
	if !ok || len(trArr) != 1 {
		t.Fatalf("currentMessage should have 1 synthetic toolResult, got %v", trArr)
	}
	if tr, ok := trArr[0].(map[string]any); ok {
		if tr["toolUseId"] != "call_B" {
			t.Errorf("synthetic toolResult toolUseId = %q, want 'call_B'", tr["toolUseId"])
		}
	}
}

// TestConvertMessagesToolResultIDMatch verifies that when currentMessage
// has toolResults with IDs that MATCH the last history assistant's toolUses,
// the currentMessage is preserved (not replaced with "Continue").
func TestConvertMessagesToolResultIDMatch(t *testing.T) {
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
		{Role: "assistant", Content: "Done."},
		{Role: "user", Content: "now read it"},
		{
			Role:    "assistant",
			Content: "",
			ToolCalls: []ToolCall{
				{ID: "call_2", Type: "function", Function: ToolCallFunction{Name: "readFile", Arguments: `{}`}},
			},
		},
		{Role: "tool", Content: "package main", ToolCallID: "call_2"},
	}

	result := convertMessages(messages, nil, "claude-sonnet-4-5", false)

	// History should end with assistantResponseMessage with toolUses=[call_2]
	last := result.history[len(result.history)-1]
	arm, ok := last["assistantResponseMessage"].(map[string]any)
	if !ok {
		t.Fatal("history should end with assistantResponseMessage")
	}
	tuArr, has := arm["toolUses"].([]any)
	if !has || len(tuArr) == 0 {
		t.Fatal("assistantResponseMessage should have toolUses")
	}
	if tu, ok := tuArr[0].(map[string]any); ok {
		if tu["toolUseId"] != "call_2" {
			t.Errorf("last toolUseId = %q, want 'call_2'", tu["toolUseId"])
		}
	}

	// currentMessage should have toolResults=[call_2] — preserved
	if result.currentMessage == nil {
		t.Fatal("currentMessage is nil")
	}
	uim, ok := result.currentMessage["userInputMessage"].(map[string]any)
	if !ok {
		t.Fatal("currentMessage.userInputMessage is not a map")
	}
	// Content should be "Tool results provided." not "Continue"
	if uim["content"] == "Continue" {
		t.Error("currentMessage should not be replaced with 'Continue' when toolResult IDs match")
	}
	if uim["content"] != "Tool results provided." {
		t.Errorf("currentMessage content = %q, want 'Tool results provided.'", uim["content"])
	}
	// Verify toolResults are present with correct ID
	ctx, ok := uim["userInputMessageContext"].(map[string]any)
	if !ok {
		t.Fatal("currentMessage should have userInputMessageContext")
	}
	trArr, ok := ctx["toolResults"].([]any)
	if !ok || len(trArr) != 1 {
		t.Fatalf("currentMessage should have 1 toolResult, got %v", trArr)
	}
	if tr, ok := trArr[0].(map[string]any); ok {
		if tr["toolUseId"] != "call_2" {
			t.Errorf("toolResult toolUseId = %q, want 'call_2'", tr["toolUseId"])
		}
	}
}

// TestConvertMessagesStaleCurrentMessageWithSyntheticToolResults verifies the fix for
// TOOL_USE_RESULT_MISMATCH: when the last history entry is assistant with toolUses
// and currentMessage has no matching toolResults, the fix must create synthetic
// toolResults for each toolUseId to satisfy Bedrock's requirement.
func TestConvertMessagesStaleCurrentMessageWithSyntheticToolResults(t *testing.T) {
	// Scenario: last OpenAI message is assistant with tool_calls, no tool results follow
	messages := []Message{
		{Role: "user", Content: "list files"},
		{
			Role:    "assistant",
			Content: "",
			ToolCalls: []ToolCall{
				{ID: "call_A", Type: "function", Function: ToolCallFunction{Name: "ls", Arguments: `{}`}},
			},
		},
		{Role: "tool", Content: "file1.go", ToolCallID: "call_A"},
		{
			Role:    "assistant",
			Content: "",
			ToolCalls: []ToolCall{
				{ID: "call_B", Type: "function", Function: ToolCallFunction{Name: "readFile", Arguments: `{}`}},
			},
		},
	}

	result := convertMessages(messages, nil, "claude-sonnet-4-5", false)

	// History should end with assistantResponseMessage with toolUses=[call_B]
	if len(result.history) == 0 {
		t.Fatal("history should not be empty")
	}
	last := result.history[len(result.history)-1]
	arm, ok := last["assistantResponseMessage"].(map[string]any)
	if !ok {
		t.Fatal("history should end with assistantResponseMessage")
	}
	tuArr, has := arm["toolUses"].([]any)
	if !has || len(tuArr) == 0 {
		t.Fatal("assistantResponseMessage should have toolUses")
	}
	if tu, ok := tuArr[0].(map[string]any); ok {
		if tu["toolUseId"] != "call_B" {
			t.Errorf("last toolUseId = %q, want 'call_B'", tu["toolUseId"])
		}
	}

	// currentMessage must have synthetic toolResults for call_B
	if result.currentMessage == nil {
		t.Fatal("currentMessage is nil")
	}
	uim, ok := result.currentMessage["userInputMessage"].(map[string]any)
	if !ok {
		t.Fatal("currentMessage.userInputMessage is not a map")
	}
	if uim["content"] != "Continue" {
		t.Errorf("currentMessage content = %q, want 'Continue'", uim["content"])
	}
	// Verify synthetic toolResults are present with correct ID
	ctx, ok := uim["userInputMessageContext"].(map[string]any)
	if !ok {
		t.Fatal("currentMessage should have userInputMessageContext with synthetic toolResults")
	}
	trArr, ok := ctx["toolResults"].([]any)
	if !ok || len(trArr) != 1 {
		t.Fatalf("currentMessage should have 1 synthetic toolResult, got %v", trArr)
	}
	if tr, ok := trArr[0].(map[string]any); ok {
		if tr["toolUseId"] != "call_B" {
			t.Errorf("synthetic toolResult toolUseId = %q, want 'call_B'", tr["toolUseId"])
		}
		if tr["status"] != "success" {
			t.Errorf("synthetic toolResult status = %q, want 'success'", tr["status"])
		}
	}
}

// TestConvertMessagesMultipleToolUsesWithoutResults verifies that when the last
// assistant has MULTIPLE toolUses and no matching toolResults, synthetic results
// are created for ALL toolUseIds.
func TestConvertMessagesMultipleToolUsesWithoutResults(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "do everything"},
		{
			Role:    "assistant",
			Content: "",
			ToolCalls: []ToolCall{
				{ID: "call_A", Type: "function", Function: ToolCallFunction{Name: "ls", Arguments: `{}`}},
				{ID: "call_B", Type: "function", Function: ToolCallFunction{Name: "pwd", Arguments: `{}`}},
			},
		},
	}

	result := convertMessages(messages, nil, "claude-sonnet-4-5", false)

	// History should end with assistant with toolUses=[call_A, call_B]
	last := result.history[len(result.history)-1]
	arm, ok := last["assistantResponseMessage"].(map[string]any)
	if !ok {
		t.Fatal("history should end with assistantResponseMessage")
	}
	tuArr, has := arm["toolUses"].([]any)
	if !has || len(tuArr) != 2 {
		t.Fatalf("expected 2 toolUses, got %v", tuArr)
	}

	// currentMessage must have synthetic toolResults for BOTH call_A and call_B
	if result.currentMessage == nil {
		t.Fatal("currentMessage is nil")
	}
	uim, ok := result.currentMessage["userInputMessage"].(map[string]any)
	if !ok {
		t.Fatal("currentMessage.userInputMessage is not a map")
	}
	ctx, ok := uim["userInputMessageContext"].(map[string]any)
	if !ok {
		t.Fatal("currentMessage should have userInputMessageContext")
	}
	trArr, ok := ctx["toolResults"].([]any)
	if !ok || len(trArr) != 2 {
		t.Fatalf("currentMessage should have 2 synthetic toolResults, got %d", len(trArr))
	}
	// Check both IDs are present
	ids := map[string]bool{}
	for _, tr := range trArr {
		if trMap, ok := tr.(map[string]any); ok {
			if id, ok := trMap["toolUseId"].(string); ok {
				ids[id] = true
			}
		}
	}
	if !ids["call_A"] {
		t.Error("missing synthetic toolResult for call_A")
	}
	if !ids["call_B"] {
		t.Error("missing synthetic toolResult for call_B")
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

	result := convertMessages(messages, tools, "claude-sonnet-4-5", false)

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

	result := convertMessages(messages, nil, "claude-sonnet-4-5", false)

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

// TestConvertMessagesSystemInstructions verifies the VansRouter pattern:
// system role messages are wrapped in <instructions> tags and placed as
// user messages (not system role).
func TestConvertMessagesSystemInstructions(t *testing.T) {
	t.Run("single system message", func(t *testing.T) {
		messages := []Message{
			{Role: "system", Content: "You are a helpful assistant."},
			{Role: "user", Content: "hello"},
		}

		result := convertMessages(messages, nil, "claude-sonnet-4-5", false)

		if result.currentMessage == nil {
			t.Fatal("currentMessage is nil")
		}
		uim, ok := result.currentMessage["userInputMessage"].(map[string]any)
		if !ok {
			t.Fatal("currentMessage.userInputMessage is not a map")
		}
		content, _ := uim["content"].(string)
		if !strings.Contains(content, "<instructions>") {
			t.Errorf("content missing <instructions> tag: %q", content)
		}
		if !strings.Contains(content, "You are a helpful assistant.") {
			t.Errorf("content missing system text: %q", content)
		}
		if !strings.Contains(content, "</instructions>") {
			t.Errorf("content missing </instructions> tag: %q", content)
		}
		// Must also have the user message
		if !strings.Contains(content, "hello") {
			t.Errorf("content missing user text 'hello': %q", content)
		}
		// systemContent should be set (for buildKiroRequest)
		if result.systemContent != "You are a helpful assistant." {
			t.Errorf("systemContent = %q, want 'You are a helpful assistant.'", result.systemContent)
		}
	})

	t.Run("system with multi-turn", func(t *testing.T) {
		messages := []Message{
			{Role: "system", Content: "Be concise."},
			{Role: "user", Content: "hi"},
			{Role: "assistant", Content: "Hello!"},
			{Role: "user", Content: "how are you?"},
		}

		result := convertMessages(messages, nil, "claude-sonnet-4-5", false)

		// history[0] should be the <instructions> user message
		if len(result.history) < 2 {
			t.Fatalf("expected >= 2 history entries, got %d", len(result.history))
		}
		first, ok := result.history[0]["userInputMessage"].(map[string]any)
		if !ok {
			t.Fatal("history[0] is not userInputMessage")
		}
		content, _ := first["content"].(string)
		if !strings.Contains(content, "<instructions>") || !strings.Contains(content, "Be concise.") {
			t.Errorf("history[0] missing instructions: %q", content)
		}
		// Should also contain "hi" — system + user merged
		if !strings.Contains(content, "hi") {
			t.Errorf("history[0] missing user content 'hi': %q", content)
		}

		// currentMessage should be the last user message
		uim, ok := result.currentMessage["userInputMessage"].(map[string]any)
		if !ok {
			t.Fatal("currentMessage.userInputMessage is not a map")
		}
		if uim["content"] != "how are you?" {
			t.Errorf("currentMessage content = %q, want 'how are you?'", uim["content"])
		}
	})

	t.Run("system with empty content", func(t *testing.T) {
		messages := []Message{
			{Role: "system", Content: ""},
			{Role: "user", Content: "hello"},
		}

		result := convertMessages(messages, nil, "claude-sonnet-4-5", false)

		// Empty system should be skipped entirely
		uim, ok := result.currentMessage["userInputMessage"].(map[string]any)
		if !ok {
			t.Fatal("currentMessage.userInputMessage is not a map")
		}
		content, _ := uim["content"].(string)
		if strings.Contains(content, "<instructions>") {
			t.Errorf("empty system should not produce <instructions> tag, got: %q", content)
		}
		if content != "hello" {
			t.Errorf("content = %q, want 'hello'", content)
		}
		if result.systemContent != "" {
			t.Errorf("systemContent = %q, want empty", result.systemContent)
		}
	})

	t.Run("system with array content", func(t *testing.T) {
		messages := []Message{
			{
				Role: "system",
				Content: []any{
					map[string]any{"type": "text", "text": "You are Claude."},
				},
			},
			{Role: "user", Content: "hello"},
		}

		result := convertMessages(messages, nil, "claude-sonnet-4-5", false)

		uim, ok := result.currentMessage["userInputMessage"].(map[string]any)
		if !ok {
			t.Fatal("currentMessage.userInputMessage is not a map")
		}
		content, _ := uim["content"].(string)
		if !strings.Contains(content, "You are Claude.") {
			t.Errorf("content missing system text: %q", content)
		}
		if !strings.Contains(content, "<instructions>") {
			t.Errorf("content missing <instructions>: %q", content)
		}
		if result.systemContent != "You are Claude." {
			t.Errorf("systemContent = %q, want 'You are Claude.'", result.systemContent)
		}
	})
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

	result := convertMessages(messages, nil, "claude-sonnet-4-5", false)

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

// TestHandleKiroToolUseEventFragmentType verifies fragment type validation
// (VansRouter pattern: string chunks must stay string, object must stay object).
func TestHandleKiroToolUseEventFragmentType(t *testing.T) {
	t.Run("string→object transition rejected", func(t *testing.T) {
		state := &kiroSSEState{
			tools:          make(map[string]*kiroToolBuffer),
			toolArgsBuffer: make(map[string]*kiroToolArgBuffer),
		}

		// First: string payload
		payload1 := map[string]any{
			"toolUseEvent": []any{
				map[string]any{
					"toolUseId": "call_1",
					"name":      "readFile",
					"input":     `{"file": "/tmp/test"}`,
				},
			},
		}
		err := handleKiroToolUseEvent(payload1, state, 8*1024*1024)
		if err != nil {
			t.Fatalf("first call (string) should succeed: %v", err)
		}

		// Second: object payload for same toolUseId → should fail
		payload2 := map[string]any{
			"toolUseEvent": []any{
				map[string]any{
					"toolUseId": "call_1",
					"name":      "readFile",
					"input":     map[string]any{"file": "/tmp/test"},
				},
			},
		}
		err = handleKiroToolUseEvent(payload2, state, 8*1024*1024)
		if err == nil {
			t.Fatal("expected error for string→object transition")
		}
		if !strings.Contains(err.Error(), "was string, got object") {
			t.Errorf("error message = %q, want 'was string, got object'", err.Error())
		}
	})

	t.Run("object→string transition rejected", func(t *testing.T) {
		state := &kiroSSEState{
			tools:          make(map[string]*kiroToolBuffer),
			toolArgsBuffer: make(map[string]*kiroToolArgBuffer),
		}

		// First: object payload
		payload1 := map[string]any{
			"toolUseEvent": []any{
				map[string]any{
					"toolUseId": "call_2",
					"name":      "editFile",
					"input":     map[string]any{"path": "/tmp/test", "content": "hello"},
				},
			},
		}
		err := handleKiroToolUseEvent(payload1, state, 8*1024*1024)
		if err != nil {
			t.Fatalf("first call (object) should succeed: %v", err)
		}

		// Second: string payload for same toolUseId → should fail
		payload2 := map[string]any{
			"toolUseEvent": []any{
				map[string]any{
					"toolUseId": "call_2",
					"name":      "editFile",
					"input":     `{}`,
				},
			},
		}
		err = handleKiroToolUseEvent(payload2, state, 8*1024*1024)
		if err == nil {
			t.Fatal("expected error for object→string transition")
		}
		if !strings.Contains(err.Error(), "was object, got string") {
			t.Errorf("error message = %q, want 'was object, got string'", err.Error())
		}
	})

	t.Run("same type multiple calls ok", func(t *testing.T) {
		state := &kiroSSEState{
			tools:          make(map[string]*kiroToolBuffer),
			toolArgsBuffer: make(map[string]*kiroToolArgBuffer),
		}

		// Multiple string chunks for same tool → should succeed
		for i := 0; i < 3; i++ {
			payload := map[string]any{
				"toolUseEvent": []any{
					map[string]any{
						"toolUseId": "call_3",
						"name":      "search",
						"input":     `{"q": "hello"}`,
					},
				},
			}
			err := handleKiroToolUseEvent(payload, state, 8*1024*1024)
			if err != nil {
				t.Fatalf("call %d should succeed: %v", i+1, err)
			}
		}
	})
}

// TestHandleKiroToolUseEventPlaceholderFilter verifies that placeholder tools
// (no_tool_available) are skipped when encountered in toolUseEvent.
func TestHandleKiroToolUseEventPlaceholderFilter(t *testing.T) {
	state := &kiroSSEState{
		tools:          make(map[string]*kiroToolBuffer),
		toolArgsBuffer: make(map[string]*kiroToolArgBuffer),
	}

	payload := map[string]any{
		"toolUseEvent": []any{
			map[string]any{
				"toolUseId": "call_placeholder",
				"name":      "no_tool_available",
				"input":     map[string]any{},
			},
		},
	}

	err := handleKiroToolUseEvent(payload, state, 8*1024*1024)
	if err != nil {
		t.Fatalf("placeholder should be silently skipped: %v", err)
	}
	// tools map should still be empty (placeholder was skipped)
	if len(state.tools) != 0 {
		t.Errorf("expected 0 tools after placeholder, got %d", len(state.tools))
	}
}

// TestHandleKiroToolUseEventNameReverseMap verifies that truncated tool names
// are reversed to their original names via the toolNameMap.
func TestHandleKiroToolUseEventNameReverseMap(t *testing.T) {
	state := &kiroSSEState{
		tools:        make(map[string]*kiroToolBuffer),
		toolArgsBuffer: make(map[string]*kiroToolArgBuffer),
		toolNameMap: map[string]string{
			"truncatedToolName_abc123": "originalVeryLongToolNameForTesting",
		},
	}

	payload := map[string]any{
		"toolUseEvent": []any{
			map[string]any{
				"toolUseId": "call_reverse",
				"name":      "truncatedToolName_abc123",
				"input":     map[string]any{"key": "value"},
			},
		},
	}

	err := handleKiroToolUseEvent(payload, state, 8*1024*1024)
	if err != nil {
		t.Fatalf("call should succeed: %v", err)
	}

	// The tool should be stored with the ORIGINAL name, not the truncated one
	tool, exists := state.tools["call_reverse"]
	if !exists {
		t.Fatal("tool should be stored in state.tools")
	}
	if tool.name != "originalVeryLongToolNameForTesting" {
		t.Errorf("tool name = %q, want 'originalVeryLongToolNameForTesting'", tool.name)
	}
}

// TestHandleKiroToolUseEventToolNameConsistency verifies that tool name changes
// between fragments for the same toolUseId are rejected.
func TestHandleKiroToolUseEventToolNameConsistency(t *testing.T) {
	t.Run("same name ok", func(t *testing.T) {
		state := &kiroSSEState{
			tools:          make(map[string]*kiroToolBuffer),
			toolArgsBuffer: make(map[string]*kiroToolArgBuffer),
		}
		// First fragment
		payload1 := map[string]any{
			"toolUseEvent": []any{
				map[string]any{
					"toolUseId": "call_1",
					"name":      "readFile",
					"input":     `{"file": "a"}`,
				},
			},
		}
		if err := handleKiroToolUseEvent(payload1, state, 8*1024*1024); err != nil {
			t.Fatalf("first fragment: %v", err)
		}
		// Second fragment with same name
		payload2 := map[string]any{
			"toolUseEvent": []any{
				map[string]any{
					"toolUseId": "call_1",
					"name":      "readFile",
					"input":     `{"file": "b"}`,
				},
			},
		}
		if err := handleKiroToolUseEvent(payload2, state, 8*1024*1024); err != nil {
			t.Fatalf("second fragment with same name: %v", err)
		}
	})

	t.Run("different name rejected", func(t *testing.T) {
		state := &kiroSSEState{
			tools:          make(map[string]*kiroToolBuffer),
			toolArgsBuffer: make(map[string]*kiroToolArgBuffer),
		}
		// First fragment: name="readFile"
		payload1 := map[string]any{
			"toolUseEvent": []any{
				map[string]any{
					"toolUseId": "call_2",
					"name":      "readFile",
					"input":     `{}`,
				},
			},
		}
		if err := handleKiroToolUseEvent(payload1, state, 8*1024*1024); err != nil {
			t.Fatalf("first fragment: %v", err)
		}
		// Second fragment: name="editFile" — should be rejected
		payload2 := map[string]any{
			"toolUseEvent": []any{
				map[string]any{
					"toolUseId": "call_2",
					"name":      "editFile",
					"input":     `{}`,
				},
			},
		}
		err := handleKiroToolUseEvent(payload2, state, 8*1024*1024)
		if err == nil {
			t.Fatal("expected error for name change")
		}
		if !strings.Contains(err.Error(), "tool name changed between fragments") {
			t.Errorf("error = %q, want 'tool name changed between fragments'", err.Error())
		}
	})
}

// TestHandleKiroToolUseEventToolUseIDValidation verifies that invalid toolUseId
// values (non-string, empty after trim) are rejected.
func TestHandleKiroToolUseEventToolUseIDValidation(t *testing.T) {
	t.Run("missing toolUseId auto-generated", func(t *testing.T) {
		state := &kiroSSEState{
			tools:          make(map[string]*kiroToolBuffer),
			toolArgsBuffer: make(map[string]*kiroToolArgBuffer),
		}
		payload := map[string]any{
			"toolUseEvent": []any{
				map[string]any{
					"name":  "testTool",
					"input": map[string]any{},
				},
			},
		}
		err := handleKiroToolUseEvent(payload, state, 8*1024*1024)
		if err != nil {
			t.Fatalf("missing toolUseId should auto-generate: %v", err)
		}
		// Should have 1 tool with auto-generated ID
		if len(state.tools) != 1 {
			t.Errorf("expected 1 tool, got %d", len(state.tools))
		}
	})

	t.Run("non-string toolUseId rejected", func(t *testing.T) {
		state := &kiroSSEState{
			tools:          make(map[string]*kiroToolBuffer),
			toolArgsBuffer: make(map[string]*kiroToolArgBuffer),
		}
		payload := map[string]any{
			"toolUseEvent": []any{
				map[string]any{
					"toolUseId": 12345, // number, not string
					"name":      "testTool",
					"input":     map[string]any{},
				},
			},
		}
		err := handleKiroToolUseEvent(payload, state, 8*1024*1024)
		if err == nil {
			t.Fatal("expected error for non-string toolUseId")
		}
		if !strings.Contains(err.Error(), "invalid toolUseId") {
			t.Errorf("error = %q, want 'invalid toolUseId'", err.Error())
		}
	})

	t.Run("empty string toolUseId rejected", func(t *testing.T) {
		state := &kiroSSEState{
			tools:          make(map[string]*kiroToolBuffer),
			toolArgsBuffer: make(map[string]*kiroToolArgBuffer),
		}
		payload := map[string]any{
			"toolUseEvent": []any{
				map[string]any{
					"toolUseId": "   ", // whitespace-only
					"name":      "testTool",
					"input":     map[string]any{},
				},
			},
		}
		err := handleKiroToolUseEvent(payload, state, 8*1024*1024)
		if err == nil {
			t.Fatal("expected error for whitespace-only toolUseId")
		}
	})
}

// TestFlushKiroBufferedToolArgsToolCallValidation verifies that tool_call
// wrapper tools with invalid inputs are silently skipped.
func TestFlushKiroBufferedToolArgsToolCallValidation(t *testing.T) {
	t.Run("valid tool_call emitted", func(t *testing.T) {
		state := &kiroSSEState{
			tools: map[string]*kiroToolBuffer{
				"call_1": {id: "call_1", name: "tool_call"},
			},
			toolArgsBuffer: map[string]*kiroToolArgBuffer{
				"call_1": {canonical: `{"name":"readFile","arguments":{"path":"/tmp"}}`, isObjectForm: true},
			},
		}
		emitted := false
		emitDelta := func(delta map[string]any) {
			if tc, ok := delta["tool_calls"]; ok {
				if tcArr, ok := tc.([]map[string]any); ok && len(tcArr) > 0 {
					if fn, ok := tcArr[0]["function"].(map[string]any); ok {
						if fn["name"] == "tool_call" {
							emitted = true
						}
					}
				}
			}
		}
		flushKiroBufferedToolArgs(state, emitDelta)
		if !emitted {
			t.Error("valid tool_call should be emitted")
		}
	})

	t.Run("invalid tool_call skipped", func(t *testing.T) {
		state := &kiroSSEState{
			tools: map[string]*kiroToolBuffer{
				"call_2": {id: "call_2", name: "tool_call"},
				"call_3": {id: "call_3", name: "readFile"},
			},
			toolArgsBuffer: map[string]*kiroToolArgBuffer{
				"call_2": {canonical: `{"notName":"blah"}`, isObjectForm: true},
				"call_3": {canonical: `{"path":"/tmp"}`, isObjectForm: true},
			},
		}
		emittedValid := false
		emittedInvalid := false
		emitDelta := func(delta map[string]any) {
			if tc, ok := delta["tool_calls"]; ok {
				if tcArr, ok := tc.([]map[string]any); ok && len(tcArr) > 0 {
					if fn, ok := tcArr[0]["function"].(map[string]any); ok {
						if fn["name"] == "tool_call" {
							emittedInvalid = true
						}
						if fn["name"] == "readFile" {
							emittedValid = true
						}
					}
				}
			}
		}
		flushKiroBufferedToolArgs(state, emitDelta)
		if emittedInvalid {
			t.Error("invalid tool_call should be skipped")
		}
		if !emittedValid {
			t.Error("valid tool (readFile) should still be emitted")
		}
	})
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

// TestBuildKiroRequestFullPipeline verifies buildKiroRequest end-to-end with
// system messages, tools, tool calls, and tool results.
func TestBuildKiroRequestFullPipeline(t *testing.T) {
	req := ChatRequest{
		Model: "claude-sonnet-4-5",
		Messages: []Message{
			{Role: "system", Content: "You are a coding assistant."},
			{Role: "user", Content: "list files"},
			{
				Role:    "assistant",
				Content: "",
				ToolCalls: []ToolCall{
					{ID: "call_1", Type: "function", Function: ToolCallFunction{Name: "executeBash", Arguments: `{"command": "ls"}`}},
				},
			},
			{Role: "tool", Content: "file1.go", ToolCallID: "call_1"},
			{Role: "assistant", Content: "Found one file."},
			{Role: "user", Content: "read it"},
		},
		Tools: []Tool{
			{Type: "function", Function: ToolFunction{Name: "executeBash", Description: "Execute a bash command", Parameters: map[string]any{"type": "object", "properties": map[string]any{"command": map[string]any{"type": "string"}}, "required": []any{"command"}}}},
		},
	}

	resolved := ResolvedModel{Upstream: "claude-sonnet-4-5"}
	payload, err := buildKiroRequest(req, resolved, "arn:aws:test:profile/test", "test-conv-id", "")
	if err != nil {
		t.Fatalf("buildKiroRequest: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(payload.Body, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Check profileArn
	if parsed["profileArn"] != "arn:aws:test:profile/test" {
		t.Errorf("profileArn = %q", parsed["profileArn"])
	}

	// Check conversationState
	cs, ok := parsed["conversationState"].(map[string]any)
	if !ok {
		t.Fatal("conversationState missing")
	}
	if cs["conversationId"] != "test-conv-id" {
		t.Errorf("conversationId = %q", cs["conversationId"])
	}
	if cs["chatTriggerType"] != "MANUAL" {
		t.Errorf("chatTriggerType = %q", cs["chatTriggerType"])
	}

	// Check currentMessage
	cm, ok := cs["currentMessage"].(map[string]any)
	if !ok {
		t.Fatal("currentMessage missing")
	}
	uim, ok := cm["userInputMessage"].(map[string]any)
	if !ok {
		t.Fatal("userInputMessage missing")
	}

	// Content should be: system prompt (thinking prefix) + "\n\n" + user content
	content, _ := uim["content"].(string)
	if !strings.Contains(content, "read it") {
		t.Errorf("currentMessage missing user content: %q", content)
	}
	// System message should be in <instructions> tags somewhere in history

	// Check history
	hist, ok := cs["history"].([]any)
	if !ok {
		t.Fatal("history missing or not array")
	}

	// Find system message with <instructions> in history
	foundInstructions := false
	foundToolUses := false
	foundToolResults := false
	for _, h := range hist {
		hm, ok := h.(map[string]any)
		if !ok {
			continue
		}
		uim, ok := hm["userInputMessage"].(map[string]any)
		if ok {
			content, _ := uim["content"].(string)
			if strings.Contains(content, "<instructions>") {
				foundInstructions = true
			}
			ctx, ok := uim["userInputMessageContext"].(map[string]any)
			if ok {
				if trArr, ok := ctx["toolResults"].([]any); ok && len(trArr) > 0 {
					foundToolResults = true
				}
			}
		}
		arm, ok := hm["assistantResponseMessage"].(map[string]any)
		if ok {
			if tuArr, ok := arm["toolUses"].([]any); ok && len(tuArr) > 0 {
				foundToolUses = true
			}
		}
	}

	if !foundInstructions {
		t.Error("history should contain <instructions> wrapped system message")
	}
	if !foundToolUses {
		t.Error("history should contain toolUses for call_1")
	}
	if !foundToolResults {
		t.Error("history should contain toolResults for call_1")
	}

	// Check tools in currentMessage context
	ctx, ok := uim["userInputMessageContext"].(map[string]any)
	if !ok {
		t.Fatal("currentMessage missing userInputMessageContext")
	}
	tools, ok := ctx["tools"].([]any)
	if !ok || len(tools) == 0 {
		t.Error("currentMessage should have tools in context")
	} else {
		spec := tools[0].(map[string]any)["toolSpecification"].(map[string]any)
		if spec["name"] != "executeBash" {
			t.Errorf("tool name = %q, want executeBash", spec["name"])
		}
	}

	// Check modelId and origin
	if uim["modelId"] != "claude-sonnet-4-5" {
		t.Errorf("modelId = %q", uim["modelId"])
	}
	if uim["origin"] != "AI_EDITOR" {
		t.Errorf("origin = %q", uim["origin"])
	}
}

func TestBuildKiroRequestEmptyConversationID(t *testing.T) {
	req := ChatRequest{
		Model:    "claude-sonnet-4-5",
		Messages: []Message{{Role: "user", Content: "hello world"}},
	}

	resolved := ResolvedModel{Upstream: "claude-sonnet-4-5"}
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
		Model:    "claude-sonnet-4-5",
		Messages: []Message{{Role: "user", Content: "hello"}},
	}

	resolved := ResolvedModel{Upstream: "claude-sonnet-4-5"}
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
		Model: "claude-sonnet-4-5",
		Messages: []Message{
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "hi"},
			{Role: "user", Content: "how are you"},
		},
	}

	resolved := ResolvedModel{Upstream: "claude-sonnet-4-5"}
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
		"model": "claude-sonnet-4-5",
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

	result := convertMessages(req.Messages, req.Tools, "claude-sonnet-4-5", false)

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

	result := convertMessages(messages, nil, "claude-sonnet-4-5", false)

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

// TestIsShortFutureAction verifies the regex-based short future action detection.
// VansRouter ref: kiro.js isShortFutureAction
func TestIsShortFutureAction(t *testing.T) {
	tests := []struct {
		name string
		text string
		want bool
	}{
		// English future action patterns (short, no result evidence)
		{"english verify short", "I'll verify the status", true},
		{"english check short", "Now I will check the deployment", true},
		{"english confirm short", "Let me confirm the checksum", true},
		{"english investigate short", "Next, I need to investigate the error", true},
		{"english trace short", "Then let me trace the request", true},
		{"english follow up short", "I am going to follow up on this", true},
		{"english test short", "i will test the endpoint", true},
		{"english validate short", "let me validate the response", true},
		{"english continue short", "I'll continue with the analysis", true},

		// Chinese future action patterns
		{"chinese 補 short", "我將補查一下", true},
		{"chinese 確認 short", "我將確認結果", true},
		{"chinese 驗證 short", "我將驗證看看", true},
		{"chinese 檢查 short", "現在我檢查配置", true},
		{"chinese 測試 short", "下一步我測試看看", true},
		{"chinese 追蹤 short", "我只再追這個問題", true},
		{"chinese 繼續 short", "我將繼續調查", true},

		// Already has result evidence → not future action
		{"has result evidence found", "I'll verify the status. Found the issue: timeout", false},
		{"has result evidence succeeded", "Now let me check. The test succeeded", false},
		{"chinese has result 發現", "我來檢查一下。發現錯誤在 config 中", false},
		{"chinese has result 成功", "我將確認部署是否成功", false},

		// Already completed → not future action
		{"completed done", "I'll check. Done", false},
		{"completed fixed", "Now let me verify. Fixed", false},
		{"chinese completed 完成", "我來檢查。已完成驗證", false},
		{"chinese completed 總結", "我將測試。總結：一切正常", false},

		// Waiting for user → not future action
		{"user wait please", "Please approve this change", false},
		{"user wait after you", "I'll check after you provide the file", false},
		{"chinese user wait 請你", "請你確認一下", false},
		{"chinese user wait 等待", "等待使用者提供資料", false},

		// Empty or no-op → not future action
		{"empty string", "", false},
		{"whitespace only", "   ", false},
		{"no future action plain text", "The answer is 42.", false},
		{"no future action greeting", "Hello, how can I help?", false},

		// Regular content with result → not future action
		{"has result with colons", "I'll verify: status is ok", false},
		{"has result with status", "Let me check. Status: deployed successfully", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isShortFutureAction(tt.text)
			if got != tt.want {
				t.Errorf("isShortFutureAction(%q) = %v, want %v", tt.text, got, tt.want)
			}
		})
	}
}

// TestIsShortFutureActionObservedPattern verifies the specific observed trailing future action pattern.
func TestIsShortFutureActionObservedPattern(t *testing.T) {
	text := "目前證據顯示訪問日誌中有多次504錯誤。最後補查 504 access log，確認 host/路徑與是否為集中流量。"
	if !isShortFutureAction(text) {
		t.Error("observed trailing future action pattern should match")
	}
}

// TestIsShortFutureActionEnglishFutureWithResult verifies English future actions with result clauses.
func TestIsShortFutureActionEnglishFutureWithResult(t *testing.T) {
	tests := []struct {
		name string
		text string
	}{
		{"with status is", "I'll verify the status is healthy"},
		{"with checksum matches", "Now let me check the checksum matches the expected value"},
		{"with response was", "Let me confirm the response was successful"},
		{"with deployment returned", "I need to investigate the deployment returned an error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if isShortFutureAction(tt.text) {
				t.Errorf("should NOT be future action: %q", tt.text)
			}
		})
	}
}

// TestIsShortFutureActionChineseFutureWithResult verifies Chinese future actions with result clauses.
func TestIsShortFutureActionChineseFutureWithResult(t *testing.T) {
	tests := []struct {
		name string
		text string
	}{
		{"with 版本是", "我來檢查版本是 2.0"},
		{"with 回應為", "我將確認回應為成功"},
		{"with 結果顯示", "我來檢查結果顯示正常"},
		{"with 狀態為", "我將驗證狀態為 healthy"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if isShortFutureAction(tt.text) {
				t.Errorf("should NOT be future action: %q", tt.text)
			}
		})
	}
}
