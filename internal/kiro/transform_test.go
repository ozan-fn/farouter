package kiro

import (
	"strings"
	"testing"
)

func TestFixOrphanedToolResults_ValidToolResults(t *testing.T) {
	// Scenario: toolResult has matching toolUse in history
	history := []map[string]any{
		{
			"assistantResponseMessage": map[string]any{
				"content": "Let me check that",
				"toolUses": []any{
					map[string]any{
						"toolUseId": "tool_123",
						"name":      "bash",
					},
				},
			},
		},
		{
			"userInputMessage": map[string]any{
				"content": "Here is the result",
				"userInputMessageContext": map[string]any{
					"toolResults": []any{
						map[string]any{
							"toolUseId": "tool_123",
							"content": []any{
								map[string]any{"text": "success"},
							},
						},
					},
				},
			},
		},
	}

	fixOrphanedToolResults(history)

	// toolResult should be kept (not salvaged as text)
	uim := history[1]["userInputMessage"].(map[string]any)
	ctx := uim["userInputMessageContext"].(map[string]any)
	trArr := ctx["toolResults"].([]any)
	
	if len(trArr) != 1 {
		t.Fatalf("expected 1 toolResult, got %d", len(trArr))
	}

	content := uim["content"].(string)
	if strings.Contains(content, "[Tool Result") {
		t.Errorf("valid toolResult should not be salvaged as text, got: %s", content)
	}
}

func TestFixOrphanedToolResults_OrphanedToolResults(t *testing.T) {
	// Scenario: toolResult has NO matching toolUse in history
	history := []map[string]any{
		{
			"assistantResponseMessage": map[string]any{
				"content": "Let me check that",
				"toolUses": []any{
					map[string]any{
						"toolUseId": "tool_123",
						"name":      "bash",
					},
				},
			},
		},
		{
			"userInputMessage": map[string]any{
				"content": "Result 1",
				"userInputMessageContext": map[string]any{
					"toolResults": []any{
						map[string]any{
							"toolUseId": "tool_123",
							"content": []any{
								map[string]any{"text": "result 1"},
							},
						},
					},
				},
			},
		},
		{
			"assistantResponseMessage": map[string]any{
				"content": "Response without toolUses",
			},
		},
		{
			"userInputMessage": map[string]any{
				"content": "Result 2",
				"userInputMessageContext": map[string]any{
					"toolResults": []any{
						map[string]any{
							"toolUseId": "tool_456", // ← orphaned, no matching toolUse
							"content": []any{
								map[string]any{"text": "orphaned result"},
							},
						},
					},
				},
			},
		},
	}

	fixOrphanedToolResults(history)

	// First toolResult (tool_123) should be kept
	uim1 := history[1]["userInputMessage"].(map[string]any)
	ctx1 := uim1["userInputMessageContext"].(map[string]any)
	trArr1 := ctx1["toolResults"].([]any)
	
	if len(trArr1) != 1 {
		t.Fatalf("expected 1 valid toolResult in history[1], got %d", len(trArr1))
	}

	// Second toolResult (tool_456) should be salvaged as text
	uim2 := history[3]["userInputMessage"].(map[string]any)
	content2 := uim2["content"].(string)
	
	if !strings.Contains(content2, "[Tool Result (tool_456)]") {
		t.Errorf("orphaned toolResult should be salvaged as text, got: %s", content2)
	}
	
	if !strings.Contains(content2, "orphaned result") {
		t.Errorf("orphaned toolResult content missing, got: %s", content2)
	}

	// toolResults should be removed from context
	ctx2, hasCtx := uim2["userInputMessageContext"].(map[string]any)
	if hasCtx {
		if trArr2, ok := ctx2["toolResults"].([]any); ok && len(trArr2) > 0 {
			t.Errorf("orphaned toolResults should be removed from context, got %d items", len(trArr2))
		}
	}
}

func TestFixOrphanedToolResults_CheckAllHistory(t *testing.T) {
	// Scenario: toolResult matches toolUse from earlier (not just previous) message
	history := []map[string]any{
		{
			"assistantResponseMessage": map[string]any{
				"content": "Let me check",
				"toolUses": []any{
					map[string]any{
						"toolUseId": "tool_early",
						"name":      "bash",
					},
				},
			},
		},
		{
			"userInputMessage": map[string]any{
				"content": "First user message",
			},
		},
		{
			"assistantResponseMessage": map[string]any{
				"content": "Another response without toolUses",
			},
		},
		{
			"userInputMessage": map[string]any{
				"content": "Result from early tool",
				"userInputMessageContext": map[string]any{
					"toolResults": []any{
						map[string]any{
							"toolUseId": "tool_early", // ← matches history[0], NOT history[2]
							"content": []any{
								map[string]any{"text": "early tool result"},
							},
						},
					},
				},
			},
		},
	}

	fixOrphanedToolResults(history)

	// toolResult should be kept (not salvaged) because it matches history[0]
	uim := history[3]["userInputMessage"].(map[string]any)
	ctx := uim["userInputMessageContext"].(map[string]any)
	trArr := ctx["toolResults"].([]any)
	
	if len(trArr) != 1 {
		t.Fatalf("expected 1 toolResult kept, got %d", len(trArr))
	}

	content := uim["content"].(string)
	if strings.Contains(content, "[Tool Result") {
		t.Errorf("valid toolResult from earlier history should not be salvaged, got: %s", content)
	}
}

func TestFixOrphanedToolResultsSingle_ValidToolResult(t *testing.T) {
	// Scenario: currentMessage toolResult has matching toolUse in history
	history := []map[string]any{
		{
			"assistantResponseMessage": map[string]any{
				"content": "Let me check",
				"toolUses": []any{
					map[string]any{
						"toolUseId": "tool_current",
						"name":      "bash",
					},
				},
			},
		},
	}

	currentMessage := map[string]any{
		"userInputMessage": map[string]any{
			"content": "Current result",
			"userInputMessageContext": map[string]any{
				"toolResults": []any{
					map[string]any{
						"toolUseId": "tool_current",
						"content": []any{
							map[string]any{"text": "current tool result"},
						},
					},
				},
			},
		},
	}

	fixOrphanedToolResultsSingle(currentMessage, history)

	// toolResult should be kept
	uim := currentMessage["userInputMessage"].(map[string]any)
	ctx := uim["userInputMessageContext"].(map[string]any)
	trArr := ctx["toolResults"].([]any)
	
	if len(trArr) != 1 {
		t.Fatalf("expected 1 toolResult kept, got %d", len(trArr))
	}

	content := uim["content"].(string)
	if strings.Contains(content, "[Tool Result") {
		t.Errorf("valid toolResult should not be salvaged, got: %s", content)
	}
}

func TestFixOrphanedToolResultsSingle_OrphanedToolResult(t *testing.T) {
	// Scenario: currentMessage toolResult has NO matching toolUse in history
	history := []map[string]any{
		{
			"assistantResponseMessage": map[string]any{
				"content": "Response without toolUses",
			},
		},
	}

	currentMessage := map[string]any{
		"userInputMessage": map[string]any{
			"content": "Orphaned result",
			"userInputMessageContext": map[string]any{
				"toolResults": []any{
					map[string]any{
						"toolUseId": "tool_orphan",
						"content": []any{
							map[string]any{"text": "orphaned data"},
						},
					},
				},
			},
		},
	}

	fixOrphanedToolResultsSingle(currentMessage, history)

	// toolResult should be salvaged as text
	uim := currentMessage["userInputMessage"].(map[string]any)
	content := uim["content"].(string)
	
	if !strings.Contains(content, "[Tool Result (tool_orphan)]") {
		t.Errorf("orphaned toolResult should be salvaged as text, got: %s", content)
	}
	
	if !strings.Contains(content, "orphaned data") {
		t.Errorf("orphaned toolResult content missing, got: %s", content)
	}

	// toolResults should be removed from context
	ctx, hasCtx := uim["userInputMessageContext"].(map[string]any)
	if hasCtx {
		if trArr, ok := ctx["toolResults"].([]any); ok && len(trArr) > 0 {
			t.Errorf("orphaned toolResults should be removed, got %d items", len(trArr))
		}
	}
}

func TestFixOrphanedToolResults_MixedValidAndOrphaned(t *testing.T) {
	// Scenario: mix of valid and orphaned toolResults in same message
	history := []map[string]any{
		{
			"assistantResponseMessage": map[string]any{
				"content": "Let me check",
				"toolUses": []any{
					map[string]any{
						"toolUseId": "tool_valid",
						"name":      "bash",
					},
				},
			},
		},
		{
			"userInputMessage": map[string]any{
				"content": "Mixed results",
				"userInputMessageContext": map[string]any{
					"toolResults": []any{
						map[string]any{
							"toolUseId": "tool_valid",
							"content": []any{
								map[string]any{"text": "valid result"},
							},
						},
						map[string]any{
							"toolUseId": "tool_invalid",
							"content": []any{
								map[string]any{"text": "orphaned result"},
							},
						},
					},
				},
			},
		},
	}

	fixOrphanedToolResults(history)

	uim := history[1]["userInputMessage"].(map[string]any)
	ctx := uim["userInputMessageContext"].(map[string]any)
	trArr := ctx["toolResults"].([]any)
	
	// Only valid toolResult should be kept
	if len(trArr) != 1 {
		t.Fatalf("expected 1 valid toolResult kept, got %d", len(trArr))
	}

	tr := trArr[0].(map[string]any)
	if tr["toolUseId"] != "tool_valid" {
		t.Errorf("expected tool_valid to be kept, got: %v", tr["toolUseId"])
	}

	// Orphaned toolResult should be salvaged as text
	content := uim["content"].(string)
	if !strings.Contains(content, "[Tool Result (tool_invalid)]") {
		t.Errorf("orphaned toolResult should be salvaged, got: %s", content)
	}
	
	if !strings.Contains(content, "orphaned result") {
		t.Errorf("orphaned content missing, got: %s", content)
	}
}
