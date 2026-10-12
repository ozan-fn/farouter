package kiro

import (
	"encoding/json"
	"strings"
	"testing"
)

func buildRolesBody(t *testing.T, req ChatRequest) string {
	t.Helper()
	res, err := buildKiroRequest(req, ResolveModel(req.Model), DefaultProfileArn("social"), "conv-1", "conn-1")
	if err != nil {
		t.Fatalf("buildKiroRequest: %v", err)
	}
	if len(res.Body) == 0 {
		t.Fatal("empty kiro body")
	}
	return string(res.Body)
}

func readTool() []Tool {
	return []Tool{{
		Type: "function",
		Function: ToolFunction{
			Name:        "read",
			Description: "Read the contents of a file",
			Parameters:  map[string]any{"type": "object", "properties": map[string]any{"filePath": map[string]any{"type": "string"}}},
		},
	}}
}

// All four roles in one conversation: system, user, assistant(tool_calls), tool(result), user.
func TestRoles_AllFourInOneRequest(t *testing.T) {
	body := buildRolesBody(t, ChatRequest{
		Model: "kr/claude-sonnet-4.5",
		Messages: []Message{
			{Role: RoleSystem, Content: "be brief"},
			{Role: RoleUser, Content: "read the file"},
			{Role: RoleAssistant, Content: "", ToolCalls: []ToolCall{{
				ID: "call_1", Type: "function",
				Function: ToolCallFunction{Name: "read", Arguments: `{"filePath":"a.go"}`},
			}}},
			{Role: RoleTool, Content: "package main", ToolCallID: "call_1"},
			{Role: RoleUser, Content: "which function is first?"},
		},
		Tools: readTool(),
	})

	// Decode before asserting: json.Marshal HTML-escapes "<" to "\u003c".
	var parsed struct {
		ConversationState struct {
			CurrentMessage struct {
				UserInputMessage struct {
					Content                 string `json:"content"`
					UserInputMessageContext struct {
						ToolResults []struct {
							ToolUseID string `json:"toolUseId"`
							Status    string `json:"status"`
						} `json:"toolResults"`
					} `json:"userInputMessageContext"`
				} `json:"userInputMessage"`
			} `json:"currentMessage"`
			History []struct {
				UserInputMessage *struct {
					Content string `json:"content"`
				} `json:"userInputMessage"`
				AssistantResponseMessage *struct {
					Content  string `json:"content"`
					ToolUses []struct {
						ToolUseID string `json:"toolUseId"`
						Name      string `json:"name"`
					} `json:"toolUses"`
				} `json:"assistantResponseMessage"`
			} `json:"history"`
		} `json:"conversationState"`
	}
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		t.Fatalf("body is not valid JSON: %v", err)
	}

	cs := parsed.ConversationState
	if len(cs.History) != 2 {
		t.Fatalf("history entries = %d, want 2 (user turn + assistant tool turn)\n%s", len(cs.History), body)
	}

	// system + user folded into one user turn, system wrapped as <instructions>.
	firstUser := cs.History[0].UserInputMessage
	if firstUser == nil {
		t.Fatalf("history[0] is not a userInputMessage\n%s", body)
	}
	for _, want := range []string{"<instructions>", "be brief", "read the file"} {
		if !strings.Contains(firstUser.Content, want) {
			t.Errorf("history[0] content missing %q; got %q", want, firstUser.Content)
		}
	}

	// assistant role with tool_calls -> assistantResponseMessage.toolUses.
	asst := cs.History[1].AssistantResponseMessage
	if asst == nil || len(asst.ToolUses) == 0 {
		t.Fatalf("history[1] has no assistant toolUses\n%s", body)
	}
	if asst.ToolUses[0].ToolUseID != "call_1" {
		t.Errorf("assistant toolUseId = %q, want call_1", asst.ToolUses[0].ToolUseID)
	}
	if asst.ToolUses[0].Name != "read" {
		t.Errorf("assistant tool name = %q, want read", asst.ToolUses[0].Name)
	}

	// tool role -> toolResults on the current user turn.
	cur := cs.CurrentMessage.UserInputMessage
	if !strings.Contains(cur.Content, "which function is first?") {
		t.Errorf("current message content = %q, want the trailing user text", cur.Content)
	}
	results := cur.UserInputMessageContext.ToolResults
	if len(results) == 0 {
		t.Fatalf("current message has no toolResults\n%s", body)
	}
	if results[0].ToolUseID != "call_1" {
		t.Errorf("toolResult toolUseId = %q, want call_1", results[0].ToolUseID)
	}
	if results[0].Status != "success" {
		t.Errorf("toolResult status = %q, want success", results[0].Status)
	}

	// OpenAI role keys must not leak into the Kiro body.
	for _, leak := range []string{`"role":`, `"tool_call_id"`} {
		if strings.Contains(body, leak) {
			t.Errorf("OpenAI-shaped key %s leaked into kiro body", leak)
		}
	}
}

// Realistic client shape: assistant message with explicit null content.
func TestRoles_AssistantNullContent(t *testing.T) {
	body := buildRolesBody(t, ChatRequest{
		Model: "kr/claude-sonnet-4.5",
		Messages: []Message{
			{Role: RoleUser, Content: "go"},
			{Role: RoleAssistant, Content: nil, ToolCalls: []ToolCall{{
				ID: "call_9", Type: "function",
				Function: ToolCallFunction{Name: "read", Arguments: `{"filePath":"b.go"}`},
			}}},
			{Role: RoleTool, Content: "ok", ToolCallID: "call_9"},
			{Role: RoleUser, Content: "done?"},
		},
		Tools: readTool(),
	})
	for _, want := range []string{"toolUses", "call_9", "toolResults"} {
		if !strings.Contains(body, want) {
			t.Errorf("null-content assistant: body missing %q", want)
		}
	}
}

// Conversation that ends on a tool result (no trailing user message).
func TestRoles_EndsWithToolResult(t *testing.T) {
	body := buildRolesBody(t, ChatRequest{
		Model: "kr/claude-sonnet-4.5",
		Messages: []Message{
			{Role: RoleUser, Content: "go"},
			{Role: RoleAssistant, Content: "", ToolCalls: []ToolCall{{
				ID: "call_2", Type: "function",
				Function: ToolCallFunction{Name: "read", Arguments: `{"filePath":"c.go"}`},
			}}},
			{Role: RoleTool, Content: "file contents", ToolCallID: "call_2"},
		},
		Tools: readTool(),
	})
	if !strings.Contains(body, "toolResults") || !strings.Contains(body, "call_2") {
		t.Errorf("trailing tool result dropped from body:\n%s", body)
	}
}

// Mirrors 9router tests/unit/openai-to-kiro.test.js "tool interaction without
// client-provided tools": with no `tools` array, structured tool content must be
// flattened to text so Kiro's "tools required" 400 never fires.
func TestRoles_FlattenNoTools(t *testing.T) {
	body := buildRolesBody(t, ChatRequest{
		Model: "kr/claude-sonnet-4.5",
		Messages: []Message{
			{Role: RoleUser, Content: "Read the file"},
			{Role: RoleAssistant, Content: nil, ToolCalls: []ToolCall{{
				ID: "call_1", Type: "function",
				Function: ToolCallFunction{Name: "read_file", Arguments: `{"path":"a.txt"}`},
			}}},
			{Role: RoleTool, Content: "file contents here", ToolCallID: "call_1"},
			{Role: RoleUser, Content: "Summarize it"},
		},
		// note: no Tools
	})

	for _, leak := range []string{"toolUses", "toolResults", "userInputMessageContext"} {
		if strings.Contains(body, leak) {
			t.Errorf("no-tools body must not contain structured %q\n%s", leak, body)
		}
	}
	for _, want := range []string{"[Tool call: read_file(", "[Tool result: file contents here]"} {
		if !strings.Contains(body, want) {
			t.Errorf("no-tools body missing %q\n%s", want, body)
		}
	}
}

// Mirrors 9router "should salvage orphaned tool_result content as text instead
// of discarding it".
func TestRoles_OrphanToolResultSalvaged(t *testing.T) {
	body := buildRolesBody(t, ChatRequest{
		Model: "kr/claude-sonnet-4.5",
		Messages: []Message{
			{Role: RoleUser, Content: "Start"},
			{Role: RoleUser, Content: []any{map[string]any{
				"type": "tool_result", "tool_use_id": "orphan_call", "content": "important orphaned output",
			}}},
			{Role: RoleUser, Content: "Now continue"},
		},
		Tools: readTool(),
	})

	if strings.Contains(body, "orphan_call") {
		t.Errorf("dangling tool_use_id reached upstream\n%s", body)
	}
	if !strings.Contains(body, "[Tool result: important orphaned output]") {
		t.Errorf("orphan content not salvaged as text\n%s", body)
	}
}

// 9router ref: a tool result flagged as an error is reported with status "error".
func TestRoles_ToolErrorStatus(t *testing.T) {
	body := buildRolesBody(t, ChatRequest{
		Model: "kr/claude-sonnet-4.5",
		Messages: []Message{
			{Role: RoleUser, Content: "go"},
			{Role: RoleAssistant, Content: "", ToolCalls: []ToolCall{{
				ID: "call_3", Type: "function",
				Function: ToolCallFunction{Name: "read", Arguments: `{}`},
			}}},
			{Role: RoleTool, Content: "boom", ToolCallID: "call_3", Status: "error"},
			{Role: RoleUser, Content: "what happened?"},
		},
		Tools: readTool(),
	})
	if !strings.Contains(body, `"status":"error"`) {
		t.Errorf("tool error status not propagated\n%s", body)
	}
}

// An empty assistant turn must NOT become a bare "..." placeholder: the response
// pipeline treats an ellipsis-only turn as a hallucination (IntegrityEllipsis)
// and the model can echo the placeholder back to the client. History still has
// to stay user/assistant alternating, which is what the placeholder was for.
func TestRoles_EmptyAssistantNoEllipsisPlaceholder(t *testing.T) {
	body := buildRolesBody(t, ChatRequest{
		Model: "kr/claude-sonnet-4.5",
		Messages: []Message{
			{Role: RoleUser, Content: "first"},
			{Role: RoleAssistant, Content: ""},
			{Role: RoleUser, Content: "second"},
		},
	})

	var parsed struct {
		ConversationState struct {
			History []map[string]any `json:"history"`
		} `json:"conversationState"`
	}
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		t.Fatalf("body is not valid JSON: %v", err)
	}

	if strings.Contains(body, `"..."`) {
		t.Errorf("body contains a bare \"...\" placeholder\n%s", body)
	}

	var roles []string
	for _, h := range parsed.ConversationState.History {
		switch {
		case h["userInputMessage"] != nil:
			roles = append(roles, "user")
		case h["assistantResponseMessage"] != nil:
			roles = append(roles, "assistant")
		}
	}
	for i := 1; i < len(roles); i++ {
		if roles[i] == roles[i-1] {
			t.Fatalf("non-alternating history roles: %v", roles)
		}
	}
}

// A tool result whose tool_call_id is empty must not reach upstream as an empty
// toolUseId (Kiro rejects that with TOOL_USE_RESULT_MISMATCH).
func TestRoles_ToolResultEmptyID(t *testing.T) {
	body := buildRolesBody(t, ChatRequest{
		Model: "kr/claude-sonnet-4.5",
		Messages: []Message{
			{Role: RoleUser, Content: "go"},
			{Role: RoleTool, Content: "orphan result", ToolCallID: ""},
			{Role: RoleUser, Content: "next"},
		},
		Tools: readTool(),
	})
	var parsed map[string]any
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		t.Fatalf("body is not valid JSON: %v", err)
	}
	if strings.Contains(body, `"toolUseId":""`) || strings.Contains(body, `"toolUseId": ""`) {
		t.Errorf("empty toolUseId sent upstream:\n%s", body)
	}
}
