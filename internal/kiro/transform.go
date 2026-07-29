package kiro

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// VansRouter ref: open-sse/executors/kiro.js — request building + message conversion
// VansRouter ref: open-sse/translator/request/openai-to-kiro.js — message translator
//
//   buildKiroRequest           → kiro.js buildKiroRequest (via BaseExecutor)
//   KiroRequestResult          → kiro.js KiroRequestResult
//   convertMessages            → openai-to-kiro.js convertMessages
//   convertKiroTools           → openai-to-kiro.js tool conversion (toolSpecification)
//   resolveKiroEffort          → kiroConstants.js resolveKiroThinkingBudget
//   extractUserContent         → openai-to-kiro.js extractUserContent
//   extractAssistantContent    → openai-to-kiro.js extractAssistantContent
//   sanitizeToolInput          → openai-to-kiro.js safeJSONParse
//   serializeToolResultContent → openai-to-kiro.js toolResultToText
//   buildKiroRequest system prompt → kiro.js BUILTIN_PREFIX (REMOVED: AIClient2API only)
//
// Removed AIClient2API-specific code (aligned with VansRouter):
//   - kiroBuiltInPrefix (Chinese identity prompt)  → VansRouter doesn't use
//   - assistant "{" removal (hack for AIClient2API format)
//   - empty message pre-filter + isEmptyMessage  → VansRouter doesn't filter
//   - image age-out (keepImageThreshold)  → VansRouter doesn't limit images
//   - dedupToolResults  → VansRouter doesn't dedup
//   - mergeConsecutiveRolesAIClient2API  → VansRouter doesn't merge roles
//   - single-turn vs multi-turn system injection → VansRouter injects via direct prepend
//   - system msg <instructions> wrapping  → ADDED (VansRouter pattern)

type KiroRequestResult struct {
	Body    []byte
	NameMap map[string]string
}

// buildKiroRequest converts OpenAI ChatRequest → Kiro API request body.
// VansRouter ref: kiro.js buildKiroRequest — full request builder
func buildKiroRequest(req ChatRequest, resolved ResolvedModel, profileArn, conversationID, connectionID string) (*KiroRequestResult, error) {
	upstreamModel := resolved.Upstream
	conversationID = ensureConversationID(conversationID, req.Messages)

	result := convertMessages(req.Messages, req.Tools, upstreamModel, resolved.Thinking)
	history := result.history
	currentMsg := result.currentMessage

	// AIClient2API style: throw jika tidak ada user messages
	if currentMsg == nil {
		return nil, fmt.Errorf("no user messages found")
	}

	// ── System prompt (VansRouter pattern) ──
	// 1. Thinking prefix jika ada
	// 2. Client system prompt (dari messages role=system)
	// 3. Agentic prompt jika model -agentic

	var sysParts []string

	if resolved.Thinking {
		budget := ThinkingBudgetDefault
		if req.Thinking != nil && req.Thinking.BudgetTokens > 0 {
			budget = req.Thinking.BudgetTokens
		}
		sysParts = append(sysParts, BuildThinkingSystemPrefix(budget))
	} else {
		effort := resolveKiroEffort(req)
		if effort != "" {
			budget := ThinkingLengthForEffort(effort)
			sysParts = append(sysParts, BuildThinkingSystemPrefix(budget))
		}
	}

	// NOTE: systemContent NOT added to sysParts karena sudah di-wrap dalam
	// <instructions> tags di user message oleh convertMessages (VansRouter pattern).
	// Tidak perlu duplikasi.

	if resolved.Agentic {
		sysParts = append(sysParts, AgenticSystemPrompt)
	}

	systemPrompt := strings.Join(sysParts, "\n\n")

	// ── System injection (VansRouter pattern) ──
	// systemPrompt di-prepend ke currentMessage.content
	// VansRouter: inject ke user message terakhir, tanpa single-turn/multi-turn split
	if currentMsg != nil {
		if uim, ok := currentMsg["userInputMessage"].(map[string]any); ok {
			existing, _ := uim["content"].(string)
			uim["content"] = systemPrompt + "\n\n" + existing
			uim["modelId"] = upstreamModel
			uim["origin"] = "AI_EDITOR"
		}
	}

	var nameMap map[string]string

	// ── Tambah tools ke currentMessage jika belum ada ──
	if currentMsg != nil && len(req.Tools) > 0 {
		if uim, ok := currentMsg["userInputMessage"].(map[string]any); ok {
			ctx, _ := uim["userInputMessageContext"].(map[string]any)
			if ctx == nil {
				ctx = map[string]any{}
				uim["userInputMessageContext"] = ctx
			}
			// Jangan timpa tools yang sudah ada
			if _, has := ctx["tools"]; !has {
				converted := convertKiroTools(req.Tools)
				if len(converted) > 0 {
					sanitized := SanitizeKiroTools(converted)
					ctx["tools"] = sanitized.Tools
					if len(sanitized.NameMap) > 0 {
						nameMap = sanitized.NameMap
					}
				}
			}
		}
	}

	// ── AIClient2API-style: handle last message type ──
	// Case 1: currentMessage is assistant → move to history, create user "Continue"
	// Case 2: currentMessage is user → only fix history ending if needed
	if currentMsg != nil {
		if arm, ok := currentMsg["assistantResponseMessage"].(map[string]any); ok {
			history = append(history, currentMsg)
			uim := map[string]any{
				"content": "Continue",
				"modelId": upstreamModel,
				"origin":  "AI_EDITOR",
			}
			// If the assistant had toolUses, create synthetic toolResults
			if tuArr, has := arm["toolUses"].([]any); has && len(tuArr) > 0 {
				var syntheticResults []any
				for _, tu := range tuArr {
					if tuMap, ok := tu.(map[string]any); ok {
						if id, ok := tuMap["toolUseId"].(string); ok && id != "" {
							syntheticResults = append(syntheticResults, map[string]any{
								"toolUseId": id,
								"status":    "success",
								"content":   []any{map[string]any{"text": "(tool execution was interrupted)"}},
							})
						}
					}
				}
				if len(syntheticResults) > 0 {
					uim["userInputMessageContext"] = map[string]any{
						"toolResults": syntheticResults,
					}
				}
			}
			currentMsg = map[string]any{
				"userInputMessage": uim,
			}
		} else {
			// Ensure history ends with assistantResponseMessage
			// (required by Kiro API: history must end with assistant before currentMessage)
			if len(history) > 0 {
				last := history[len(history)-1]
				if _, ok := last["assistantResponseMessage"]; !ok {
					needsContinue := true
					toolResultsFromCurrent := false
					var currentToolResults []any
					if uim, ok := currentMsg["userInputMessage"].(map[string]any); ok {
						if ctx, ok := uim["userInputMessageContext"].(map[string]any); ok {
							if trArr, ok := ctx["toolResults"].([]any); ok && len(trArr) > 0 {
								toolResultsFromCurrent = true
								currentToolResults = trArr
							}
						}
					}

					if toolResultsFromCurrent {
						// CurrentMessage has toolResults but history doesn't end with assistant(toolUses).
						// Bedrock requires every tool_use block to have a matching tool_result in the
						// next message. Create a synthetic assistant with toolUses matching the toolResults.
						var syntheticToolUses []any
						for _, tr := range currentToolResults {
							if trMap, ok := tr.(map[string]any); ok {
								if id, ok := trMap["toolUseId"].(string); ok && id != "" {
									syntheticToolUses = append(syntheticToolUses, map[string]any{
										"toolUseId": id,
										"name":      "continue",
										"input":     map[string]any{},
									})
								}
							}
						}
						arm := map[string]any{"content": "Continue"}
						if len(syntheticToolUses) > 0 {
							arm["toolUses"] = syntheticToolUses
						}
						history = append(history, map[string]any{
							"assistantResponseMessage": arm,
						})
					} else if needsContinue {
						history = append(history, map[string]any{
							"assistantResponseMessage": map[string]any{
								"content": "Continue",
							},
						})
					}
				}
			}
		}
	}

	payload := map[string]any{
		"conversationState": map[string]any{
			"chatTriggerType": "MANUAL",
			"conversationId":  conversationID,
			"agentTaskType":   "vibe",
			"currentMessage": map[string]any{
				"userInputMessage": currentMsg["userInputMessage"],
			},
		},
	}

	if len(history) > 0 {
		payload["conversationState"].(map[string]any)["history"] = history
	}

	if profileArn != "" {
		payload["profileArn"] = profileArn
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return &KiroRequestResult{Body: body, NameMap: nameMap}, nil
}

func ensureConversationID(cid string, messages []Message) string {
	if cid != "" {
		return cid
	}
	return uuid.New().String()
}

// convertKiroTools converts OpenAI tools to Kiro format (AIClient2API style).
// - Filters out web_search/websearch
// - Filters out empty descriptions
// - Truncates descriptions >9216 chars
// - Always adds placeholder if no real tools remain (including when len(tools)==0)
func convertKiroTools(tools []Tool) []any {
	var out []any
	hasRealTool := false

	for _, t := range tools {
		name := strings.ToLower(t.Function.Name)
		// Filter web_search / websearch
		if name == "web_search" || name == "websearch" {
			continue
		}
		// Filter empty description
		if t.Function.Description == "" || strings.TrimSpace(t.Function.Description) == "" {
			continue
		}

		hasRealTool = true
		desc := t.Function.Description
		if len(desc) > 9216 {
			desc = desc[:9216] + "..."
		}

		schema := t.Function.Parameters
		if schema == nil {
			schema = map[string]any{} // AIClient2API style: empty object {} (claude-kiro.js L1305)
		}

		out = append(out, map[string]any{
			"toolSpecification": map[string]any{
				"name":        t.Function.Name,
				"description": desc,
				"inputSchema": map[string]any{"json": schema},
			},
		})
	}

	// Placeholder tool jika tidak ada real tools (termasuk len(tools)==0)
	if !hasRealTool {
		out = append(out, map[string]any{
			"toolSpecification": map[string]any{
				"name":        "no_tool_available",
				"description": "Internal no-op placeholder. Never call this tool (or any tool) in this turn. Do not announce or promise actions such as reading files or running commands. Instead, write your full and complete answer directly as natural-language text in this single reply, based on the information already available to you.",
				"inputSchema": map[string]any{"json": map[string]any{
					"type":       "object",
					"properties": map[string]any{},
				}},
			},
		})
	}

	return out
}

func resolveKiroEffort(req ChatRequest) string {
	effort := strings.ToLower(req.ReasoningEffort)

	if effort == "" && req.OutputConfig != nil {
		effort = strings.ToLower(req.OutputConfig.Effort)
	}

	if effort == "" && req.Thinking != nil {
		switch req.Thinking.Type {
		case "enabled":
			budget := req.Thinking.BudgetTokens
			if budget >= 32000 {
				effort = "high"
			} else if budget >= 16000 {
				effort = "medium"
			} else if budget > 0 {
				effort = "low"
			}
		case "adaptive":
			effort = "high"
		}
	}

	if effort == "minimal" {
		effort = "low"
	}
	kiroLevels := map[string]bool{"low": true, "medium": true, "high": true, "xhigh": true, "max": true}
	if kiroLevels[effort] {
		return effort
	}
	return ""
}

type convertResult struct {
	history        []map[string]any
	currentMessage map[string]any
	systemContent  string
}

// convertMessages converts OpenAI messages to Kiro format (VansRouter pattern).
// VansRouter ref: open-sse/translator/request/openai-to-kiro.js convertMessages
//
// Rules:
//   - system role → user message, wrapped in <instructions> tags
//   - tool role → user message with toolResults
//   - Empty user content → "continue" (VansRouter fallback)
//   - Empty assistant content + no toolUses → "..." (VansRouter fallback)
//   - System content extracted and passed up for buildKiroRequest injection
func convertMessages(messages []Message, tools []Tool, upstreamModel string, modelThinking bool) convertResult {
	// "auto" → false (upstream decides image support)
	supportsImages := strings.Contains(strings.ToLower(upstreamModel), "claude")

	var history []map[string]any
	var currentMessage map[string]any

	var pendingUserContent []string
	var pendingAssistantContent []string
	var pendingToolResults []any
	var pendingImages []map[string]any
	var systemContent string
	currentRole := ""

	flush := func() {
		if currentRole == RoleUser {
			text := strings.Join(pendingUserContent, "\n\n")
			hasToolResults := len(pendingToolResults) > 0
			hasImages := len(pendingImages) > 0
			content := text

			// Fallback (AIClient2API style)
			if content == "" {
				if hasToolResults {
					content = "Tool results provided."
				} else if hasImages {
					content = "Image provided."
				} else {
					// Skip empty user message tanpa context
					pendingUserContent = nil
					pendingToolResults = nil
					pendingImages = nil
					return
				}
			}

			uim := map[string]any{
				"content": content,
				"modelId": upstreamModel,
				"origin":  "AI_EDITOR",
			}
			if hasImages {
				uim["images"] = pendingImages
			}
			ctx := map[string]any{}
			if hasToolResults {
				ctx["toolResults"] = pendingToolResults
			}
			if len(ctx) > 0 {
				uim["userInputMessageContext"] = ctx
			}

			msg := map[string]any{"userInputMessage": uim}
			history = append(history, msg)
			currentMessage = msg
			pendingUserContent = nil
			pendingToolResults = nil
			pendingImages = nil

		} else if currentRole == RoleAssistant {
			content := strings.Join(pendingAssistantContent, "\n\n")
			if strings.TrimSpace(content) == "" {
				// Skip empty assistant (AIClient2API tidak pakai "(empty)")
				pendingAssistantContent = nil
				return
			}
			history = append(history, map[string]any{
				"assistantResponseMessage": map[string]any{"content": content},
			})
			pendingAssistantContent = nil
		}
	}

	for _, msg := range messages {
		role := msg.Role

		// VansRouter pattern: system → user, wrapped in <instructions> tags
		if role == RoleSystem {
			content, _, _ := extractUserContent(msg.Content, false)
			if content != "" {
				systemContent = content
				content = "<instructions>\n" + content + "\n</instructions>"
				pendingUserContent = append(pendingUserContent, content)
			}
			role = RoleUser
			continue // skip normal flush below; already appended
		}

		// AIClient2API style: tool → user
		if role == RoleTool {
			role = RoleUser
		}

		if role != currentRole && currentRole != "" {
			flush()
		}
		currentRole = role

		if role == RoleUser {
			if msg.Role == RoleTool {
				toolContent := serializeToolResultContent(msg.Content)
				pendingToolResults = append(pendingToolResults, map[string]any{
					"toolUseId": msg.ToolCallID,
					"status":    "success",
					"content":   []any{map[string]any{"text": toolContent}},
				})
			} else {
				content, images, toolResults := extractUserContent(msg.Content, supportsImages)
				pendingImages = append(pendingImages, images...)
				pendingToolResults = append(pendingToolResults, toolResults...)
				if content != "" {
					pendingUserContent = append(pendingUserContent, content)
				}
			}
		} else if role == RoleAssistant {
			textContent, toolUses := extractAssistantContentAIClient2API(msg)
			if textContent != "" {
				pendingAssistantContent = append(pendingAssistantContent, textContent)
			}
			if len(toolUses) > 0 {
				// Flush pending user first
				if currentRole == RoleUser {
					flush()
				}
				// Build content from pendingAssistantContent + any textContent already there
				assistantContent := strings.Join(pendingAssistantContent, "\n\n")
				if assistantContent == "" {
					assistantContent = textContent
				}
				// Create assistant message with toolUses
				arm := map[string]any{"content": assistantContent}
				if len(toolUses) > 0 {
					arm["toolUses"] = toolUses
				}
				history = append(history, map[string]any{
					"assistantResponseMessage": arm,
				})
				currentRole = ""
				pendingAssistantContent = nil
			}
		}
	}
	if currentRole != "" {
		flush()
	}

	// Last message jadi currentMessage, sisanya history
	if len(history) > 0 && history[len(history)-1]["userInputMessage"] != nil {
		currentMessage = history[len(history)-1]
		history = history[:len(history)-1]
	} else if currentMessage == nil {
		currentMessage = map[string]any{
			"userInputMessage": map[string]any{
				"content": "",
				"modelId": upstreamModel,
				"origin":  "AI_EDITOR",
			},
		}
	}

	// ── Fix stale currentMessage when history ends with assistant(toolUses) ──
	// If the last history entry is assistantResponseMessage with toolUses, and
	// currentMessage doesn't have matching toolResults, it means the last OpenAI
	// message was assistant (tool_calls) with no tool/user follow-up.
	// In that case, currentMessage is stale (still points to an older user message).
	// Replace it with a fresh "Continue" to avoid sending stale text to Bedrock
	// which would cause TOOL_USE_RESULT_MISMATCH.
	//
	// Also handle the case where currentMessage HAS toolResults but with WRONG
	// toolUseIds (from a different conversation turn). The fix must verify that
	// ALL toolResult IDs match the expected toolUse IDs from the last assistant
	// response, not just check for presence of toolResults.
	if len(history) > 0 {
		if lastARM, ok := history[len(history)-1]["assistantResponseMessage"].(map[string]any); ok {
			if tuArr, has := lastARM["toolUses"].([]any); has && len(tuArr) > 0 {
				// Collect expected toolUseIds from the last history assistant
				expectedIDs := make(map[string]bool, len(tuArr))
				for _, tu := range tuArr {
					if tuMap, ok := tu.(map[string]any); ok {
						if id, ok := tuMap["toolUseId"].(string); ok && id != "" {
							expectedIDs[id] = true
						}
					}
				}

				// Check if currentMessage has matching toolResults with CORRECT IDs.
				// BUG FIX: Also verify ALL expectedIDs have a corresponding toolResult —
				// otherwise a tool_use without tool_result causes TOOL_USE_RESULT_MISMATCH.
				hasMatchingResults := false
				if uim, ok := currentMessage["userInputMessage"].(map[string]any); ok {
					if ctx, ok := uim["userInputMessageContext"].(map[string]any); ok {
						if trArr, ok := ctx["toolResults"].([]any); ok && len(trArr) > 0 {
							// Track which expectedIDs were actually found
							foundIDs := make(map[string]bool, len(tuArr))
							allMatch := true
							for _, tr := range trArr {
								if trMap, ok := tr.(map[string]any); ok {
									if id, ok := trMap["toolUseId"].(string); ok && id != "" {
										if !expectedIDs[id] {
											allMatch = false
											break
										}
										foundIDs[id] = true
									}
								}
							}
							// ALL expected IDs must have a matching toolResult
							allExpectedFound := allMatch
							if allMatch {
								for id := range expectedIDs {
									if !foundIDs[id] {
										allExpectedFound = false
										break
									}
								}
							}
							hasMatchingResults = allExpectedFound
						}
					}
				}
				if !hasMatchingResults {
					// Create synthetic toolResults for each missing toolUseId to satisfy
					// Bedrock's requirement that every tool_use must have a tool_result
					var syntheticResults []any
					for _, tu := range tuArr {
						if tuMap, ok := tu.(map[string]any); ok {
							if id, ok := tuMap["toolUseId"].(string); ok && id != "" {
								syntheticResults = append(syntheticResults, map[string]any{
									"toolUseId": id,
									"status":    "success",
									"content":   []any{map[string]any{"text": "(tool execution was interrupted)"}},
								})
							}
						}
					}
					uim := map[string]any{
						"content": "Continue",
						"modelId": upstreamModel,
						"origin":  "AI_EDITOR",
					}
					if len(syntheticResults) > 0 {
						uim["userInputMessageContext"] = map[string]any{
							"toolResults": syntheticResults,
						}
					}
					currentMessage = map[string]any{
						"userInputMessage": uim,
					}
				}
			}
		}
	}

	// ── Continue fallback untuk empty currentMessage (VansRouter: "continue") ──
	if currentMessage != nil {
		if uim, ok := currentMessage["userInputMessage"].(map[string]any); ok {
			if content, _ := uim["content"].(string); content == "" {
				ctx, _ := uim["userInputMessageContext"].(map[string]any)
				hasToolResults := false
				if ctx != nil {
					if tr, ok := ctx["toolResults"].([]any); ok && len(tr) > 0 {
						hasToolResults = true
					}
				}
				if hasToolResults {
					uim["content"] = "Tool results provided."
				} else {
					uim["content"] = "continue"
				}
			}
		}
	}

	return convertResult{
		history:       history,
		currentMessage: currentMessage,
		systemContent: systemContent,
	}
}



func serializeToolResultContent(content any) string {
	switch v := content.(type) {
	case string:
		if v == "" {
			return "(no output)"
		}
		return v
	case []any:
		var parts []string
		for _, c := range v {
			m, ok := c.(map[string]any)
			if !ok {
				continue
			}
			t, _ := m["type"].(string)
			if t == "text" {
				if txt, ok := m["text"].(string); ok && txt != "" {
					parts = append(parts, txt)
				}
			} else if t == "image" || t == "image_url" {
				parts = append(parts, "[image]")
			} else {
				b, err := json.Marshal(m)
				if err == nil && string(b) != "{}" {
					parts = append(parts, string(b))
				}
			}
		}
		result := strings.Join(parts, "\n")
		if result == "" {
			return "(no output)"
		}
		return result
	case nil:
		return "(no output)"
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return "(no output)"
		}
		return string(b)
	}
}

func extractUserContent(content any, supportsImages bool) (string, []map[string]any, []any) {
	switch v := content.(type) {
	case string:
		return v, nil, nil
	case []any:
		var texts []string
		var images []map[string]any
		var toolResults []any
		for _, c := range v {
			m, ok := c.(map[string]any)
			if !ok {
				continue
			}
			t, _ := m["type"].(string)
			switch t {
			case "text":
				texts = append(texts, fmt.Sprintf("%v", m["text"]))
			case "image_url":
				if !supportsImages {
					continue
				}
				if iu, ok := m["image_url"].(map[string]any); ok {
					url, _ := iu["url"].(string)
					if strings.HasPrefix(url, "data:") {
						if parsed := parseDataURI(url); parsed != nil {
							images = append(images, parsed)
						}
					} else if strings.HasPrefix(url, "http") {
						texts = append(texts, "[Image: "+url+"]")
					}
				}
			case "image":
				if !supportsImages {
					continue
				}
				if src, ok := m["source"].(map[string]any); ok {
					if src["type"] == "base64" {
						mediaType, _ := src["media_type"].(string)
						data, _ := src["data"].(string)
						format := strings.TrimPrefix(mediaType, "image/")
						images = append(images, map[string]any{
							"format": format,
							"source": map[string]any{"bytes": data},
						})
					}
				}
			case "tool_result":
				toolUseID, _ := m["tool_use_id"].(string)
				text := serializeToolResultContent(m["content"])
				// AIClient2API style: always success, ignore is_error
				toolResults = append(toolResults, map[string]any{
					"toolUseId": toolUseID,
					"status":    "success",
					"content":   []any{map[string]any{"text": text}},
				})
			}
		}
		return strings.Join(texts, "\n"), images, toolResults
	}
	return "", nil, nil
}

// extractAssistantContentAIClient2API extracts text + toolUses from assistant message.
// Handles both content array and OpenAI ToolCalls format.
func extractAssistantContentAIClient2API(msg Message) (string, []any) {
	var textContent string
	var toolUses []any

	if s, ok := msg.Content.(string); ok {
		textContent = strings.TrimSpace(s)
	} else if arr, ok := msg.Content.([]any); ok {
		var texts []string
		for _, c := range arr {
			m, ok := c.(map[string]any)
			if !ok {
				continue
			}
			t, _ := m["type"].(string)
			if t == "text" {
				texts = append(texts, fmt.Sprintf("%v", m["text"]))
			} else if t == "tool_use" {
				toolID, _ := m["id"].(string)
				if toolID == "" {
					toolID = fmt.Sprintf("call_%d", len(toolUses))
				}
				input := sanitizeToolInput(parseToolInput(m["input"]))
				toolUses = append(toolUses, map[string]any{
					"toolUseId": toolID,
					"name":      m["name"],
					"input":     input,
				})
			}
		}
		textContent = strings.TrimSpace(strings.Join(texts, "\n"))
	}

	// Handle OpenAI ToolCalls format (tool_calls field on message)
	for _, tc := range msg.ToolCalls {
		toolID := tc.ID
		if toolID == "" {
			toolID = fmt.Sprintf("call_%d", len(toolUses))
		}
		input := sanitizeToolInput(parseToolInput(tc.Function.Arguments))
		toolUses = append(toolUses, map[string]any{
			"toolUseId": toolID,
			"name":      tc.Function.Name,
			"input":     input,
		})
	}

	return textContent, toolUses
}

// sanitizeToolInput removes empty-string keys from tool input (AIClient2API style, claude-kiro.js L1058-1071).
// Kiro API does not accept empty-string keys (e.g., {"": "value"}).
func sanitizeToolInput(input any) any {
	m, ok := input.(map[string]any)
	if !ok {
		return input
	}
	sanitized := make(map[string]any, len(m))
	for k, v := range m {
		if k == "" {
			continue
		}
		sanitized[k] = v
	}
	return sanitized
}

func parseToolInput(value any) any {
	if value == nil {
		return map[string]any{}
	}
	switch v := value.(type) {
	case map[string]any:
		return v
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return map[string]any{}
		}
		var parsed any
		if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
			return map[string]any{}
		}
		if m, ok := parsed.(map[string]any); ok {
			return m
		}
		return map[string]any{}
	default:
		return map[string]any{}
	}
}

func parseDataURI(uri string) map[string]any {
	rest := strings.TrimPrefix(uri, "data:")
	semi := strings.Index(rest, ";")
	if semi < 0 {
		return nil
	}
	mediaType := rest[:semi]
	rest = rest[semi+1:]
	if !strings.HasPrefix(rest, "base64,") {
		return nil
	}
	data := rest[7:]
	format := strings.TrimPrefix(mediaType, "image/")
	return map[string]any{
		"format": format,
		"source": map[string]any{"bytes": data},
	}
}

