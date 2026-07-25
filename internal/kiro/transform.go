package kiro

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	maxContentLength  = 400000
	toolDescThreshold = 10000
	namespaceKiro     = "34f7193f-561d-4050-bc84-9547d953d6bf"
)

func buildKiroRequest(req ChatRequest, resolved ResolvedModel, profileArn string, conversationID string) ([]byte, error) {
	upstreamModel := resolved.Upstream
	timestamp := time.Now().UTC().Format(time.RFC3339)

	result := convertMessages(req.Messages, req.Tools, upstreamModel, resolved.Thinking)
	history := result.history
	currentMsg := result.currentMessage
	toolDocs := result.toolDocs
	toolsAttached := result.toolsAttached

	if len(req.Tools) > 0 {
		reconcileOrphanedToolResults(history, currentMsg)
	}

	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = req.MaxCompletion
	}
	if maxTokens == 0 {
		maxTokens = 32000
	}

	rawEffort := resolveKiroEffort(req)
	if resolved.Thinking && rawEffort == "" {
		rawEffort = "high"
	}
	kiroEffort := applyThinkingAllowlist(rawEffort, upstreamModel)

	var systemPromptParts []string
	if rawEffort != "" {
		effortLength := ThinkingLengthForEffort(rawEffort)
		systemPromptParts = append(systemPromptParts, BuildThinkingSystemPrefix(effortLength))
	}
	if resolved.Agentic {
		systemPromptParts = append(systemPromptParts, AgenticSystemPrompt)
	}
	systemPrompt := strings.Join(systemPromptParts, "\n\n")

	currentTimeContext := "[Context: Current time is " + timestamp + "]"
	var contentPrefixParts []string
	if systemPrompt != "" {
		contentPrefixParts = append(contentPrefixParts, systemPrompt)
	}
	contentPrefixParts = append(contentPrefixParts, currentTimeContext)
	contentPrefix := strings.Join(contentPrefixParts, "\n\n")

	history, currentMsg = applySessionReplay(
		conversationID, upstreamModel, systemPrompt,
		contentPrefix, currentTimeContext,
		history, currentMsg,
	)

	if history == nil {
		history = []map[string]any{}
	}

	finalContent := ""
	if currentMsg != nil {
		if uim, ok := currentMsg["userInputMessage"].(map[string]any); ok {
			finalContent, _ = uim["content"].(string)
		}
	}

	if toolDocs != "" {
		finalContent = "# Tool Documentation\n\n" + toolDocs + "\n\n---\n\n" + finalContent
	}

	if conversationID == "" {
		seed := finalContent
		if seed == "" {
			seed = timestamp
		}
		if len(seed) > 4000 {
			seed = seed[:4000]
		}
		ns := uuid.MustParse(namespaceKiro)
		conversationID = uuid.NewSHA1(ns, []byte(seed)).String()
	}

	payload := map[string]any{
		"conversationState": map[string]any{
			"chatTriggerType": "MANUAL",
			"conversationId":  conversationID,
			"currentMessage": map[string]any{
				"userInputMessage": map[string]any{
					"content": finalContent,
					"modelId": upstreamModel,
					"origin":  "AI_EDITOR",
				},
			},
			"history": history,
		},
	}

	if systemPrompt != "" {
		payload["systemPrompt"] = systemPrompt
	}

	if profileArn != "" {
		payload["profileArn"] = profileArn
	}

	if kiroEffort != "" {
		fields := map[string]any{
			"output_config": map[string]any{"effort": kiroEffort},
			"thinking":      map[string]any{"type": "adaptive", "display": "summarized"},
		}
		if maxTokens > 0 {
			fields["max_tokens"] = max(maxTokens, 1024)
		}
		payload["additionalModelRequestFields"] = fields

		if _, has := payload["inferenceConfig"]; !has {
			delete(payload, "temperature")
			delete(payload, "topP")
		}
	} else {
		additionalFields := BuildAdditionalModelRequestFields(req.ReasoningEffort, upstreamModel)
		if additionalFields != nil {
			payload["additionalModelRequestFields"] = additionalFields
		}
	}

	if maxTokens > 0 || req.Temperature != nil || req.TopP != nil {
		ic := map[string]any{}
		if maxTokens > 0 {
			ic["maxTokens"] = maxTokens
		}
		if req.Temperature != nil && kiroEffort == "" {
			ic["temperature"] = *req.Temperature
		}
		if req.TopP != nil && kiroEffort == "" {
			ic["topP"] = *req.TopP
		}
		if len(ic) > 0 {
			payload["inferenceConfig"] = ic
		}
	}

	if currentMsg != nil {
		if uim, ok := currentMsg["userInputMessage"].(map[string]any); ok {
			if content, ok := uim["content"].(string); ok && len(content) > maxContentLength {
				uim["content"] = content[:maxContentLength] + "\n... [truncated]"
			}
			if !toolsAttached && len(req.Tools) > 0 {
				if ctx, _ := uim["userInputMessageContext"].(map[string]any); ctx == nil || ctx["tools"] == nil {
					synthesized := synthesizeMinimalTools(req.Tools, history)
					if len(synthesized) > 0 {
						if ctx == nil {
							ctx = map[string]any{}
						}
						ctx["tools"] = SanitizeKiroTools(synthesized).Tools
						uim["userInputMessageContext"] = ctx
					}
				}
			}
			payload["conversationState"].(map[string]any)["currentMessage"] = map[string]any{
				"userInputMessage": uim,
			}
		}
	}

	return json.Marshal(payload)
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

func applyThinkingAllowlist(effort, model string) string {
	if effort == "" {
		return ""
	}
	if SupportsKiroAdaptiveThinking(model) {
		return effort
	}
	return ""
}

func synthesizeMinimalTools(tools []Tool, history []map[string]any) []any {
	seen := map[string]bool{}
	var synthesized []any
	pushName := func(name string) {
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		synthesized = append(synthesized, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        name,
				"description": "Tool: " + name,
				"parameters":  map[string]any{"type": "object", "properties": map[string]any{}, "required": []any{}},
			},
		})
	}
	for _, h := range history {
		arm, ok := h["assistantResponseMessage"].(map[string]any)
		if !ok {
			continue
		}
		tuArr, _ := arm["toolUses"].([]any)
		for _, tu := range tuArr {
			if m, ok := tu.(map[string]any); ok {
				pushName(fmt.Sprintf("%v", m["name"]))
			}
		}
	}
	for _, t := range tools {
		pushName(t.Function.Name)
	}
	return synthesized
}

type convertResult struct {
	history        []map[string]any
	currentMessage map[string]any
	toolDocs       string
	toolsAttached  bool
}

func convertMessages(messages []Message, tools []Tool, upstreamModel string, modelThinking bool) convertResult {
	clientHasTools := len(tools) > 0

	if !clientHasTools {
		messages = flattenToolInteractions(messages)
	}

	supportsImages := strings.Contains(strings.ToLower(upstreamModel), "claude")

	var history []map[string]any
	var currentMessage map[string]any

	var pendingUserContent []string
	var pendingAssistantContent []string
	var pendingToolResults []any
	var pendingImages []map[string]any
	var toolDocsParts []string
	currentRole := ""
	toolsInjected := false

	flush := func() {
		if currentRole == "user" {
			text := strings.Join(pendingUserContent, "\n\n")
			hasContext := len(pendingToolResults) > 0 || len(pendingImages) > 0
			content := text
			if content == "" && !hasContext {
				content = "(empty)"
			}

			uim := map[string]any{
				"content": content,
				"modelId": upstreamModel,
				"origin":  "AI_EDITOR",
			}
			if len(pendingImages) > 0 {
				uim["images"] = pendingImages
			}
			ctx := map[string]any{}
			if len(pendingToolResults) > 0 {
				ctx["toolResults"] = pendingToolResults
			}
			if clientHasTools && !toolsInjected {
				sanitized := SanitizeKiroTools(convertTools(tools, upstreamModel, &toolDocsParts))
				ctx["tools"] = sanitized.Tools
				toolsInjected = true
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
		} else if currentRole == "assistant" {
			content := strings.Join(pendingAssistantContent, "\n\n")
			if strings.TrimSpace(content) == "" {
				content = "(empty)"
			}
			history = append(history, map[string]any{
				"assistantResponseMessage": map[string]any{"content": content},
			})
			pendingAssistantContent = nil
		}
	}

	for _, msg := range messages {
		role := msg.Role
		wasSystem := role == "system"
		if role == "system" || role == "tool" {
			role = "user"
		}

		if role != currentRole && currentRole != "" {
			flush()
		}
		currentRole = role

		if role == "user" {
			if msg.Role == "tool" {
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
					if wasSystem {
						content = wrapSystemReminder(content)
					}
					pendingUserContent = append(pendingUserContent, content)
				}
			}
		} else if role == "assistant" {
			textContent, toolUses := extractAssistantContent(msg)
			if textContent != "" {
				pendingAssistantContent = append(pendingAssistantContent, textContent)
			}
			if len(toolUses) > 0 {
				flush()
				if len(history) > 0 {
					last := history[len(history)-1]
					if arm, ok := last["assistantResponseMessage"].(map[string]any); ok {
						arm["toolUses"] = toolUses
					}
				}
				currentRole = ""
			}
		}
	}
	if currentRole != "" {
		flush()
	}

	if len(history) > 0 && history[len(history)-1]["userInputMessage"] != nil {
		currentMessage = history[len(history)-1]
		history = history[:len(history)-1]
	} else {
		currentMessage = map[string]any{
			"userInputMessage": map[string]any{
				"content": "...",
				"modelId": upstreamModel,
				"origin":  "AI_EDITOR",
			},
		}
	}

	promoteToolsToCurrent(history, currentMessage)

	cleanHistoryForKiro(history, upstreamModel)

	merged := mergeConsecutiveRoles(history)

	if len(merged) > 0 && merged[0]["assistantResponseMessage"] != nil {
		syntheticUser := map[string]any{
			"userInputMessage": map[string]any{
				"content": "(empty)",
				"modelId": upstreamModel,
				"origin":  "AI_EDITOR",
			},
		}
		merged = append([]map[string]any{syntheticUser}, merged...)
	}

	fixOrphanedToolResults(merged)
	fixOrphanedToolResultsSingle(currentMessage, merged)

	alternating := ensureAlternatingRoles(merged)

	if currentMessage == nil {
		currentMessage = map[string]any{
			"userInputMessage": map[string]any{
				"content": "",
				"modelId": upstreamModel,
				"origin":  "AI_EDITOR",
			},
		}
	}

	return convertResult{
		history:        alternating,
		currentMessage: currentMessage,
		toolDocs:       strings.Join(toolDocsParts, "\n\n---\n\n"),
		toolsAttached:  toolsInjected,
	}
}

func wrapSystemReminder(text string) string {
	return "<system-reminder>\n" + text + "\n</system-reminder>"
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

func promoteToolsToCurrent(history []map[string]any, currentMessage map[string]any) {
	if currentMessage != nil && currentMessage["userInputMessage"] != nil {
		if uim, ok := currentMessage["userInputMessage"].(map[string]any); ok {
			if ctx, _ := uim["userInputMessageContext"].(map[string]any); ctx != nil && ctx["tools"] != nil {
				return
			}
		}
	}
	for _, h := range history {
		if uim, ok := h["userInputMessage"].(map[string]any); ok {
			if ctx, ok := uim["userInputMessageContext"].(map[string]any); ok && ctx["tools"] != nil {
				if cuim, ok := currentMessage["userInputMessage"].(map[string]any); ok {
					if cctx, _ := cuim["userInputMessageContext"].(map[string]any); cctx == nil {
						cctx = map[string]any{}
						cuim["userInputMessageContext"] = cctx
					}
					cctx, _ := cuim["userInputMessageContext"].(map[string]any)
					if _, has := cctx["tools"]; !has {
						cctx["tools"] = ctx["tools"]
					}
				}
				return
			}
		}
	}
}

func cleanHistoryForKiro(history []map[string]any, upstreamModel string) {
	for _, item := range history {
		uim, ok := item["userInputMessage"].(map[string]any)
		if !ok {
			continue
		}
		if ctx, ok := uim["userInputMessageContext"].(map[string]any); ok {
			delete(ctx, "tools")
			if len(ctx) == 0 {
				delete(uim, "userInputMessageContext")
			}
		}
		if _, ok := uim["modelId"]; !ok {
			uim["modelId"] = upstreamModel
		}
		if _, ok := uim["origin"]; !ok {
			uim["origin"] = "AI_EDITOR"
		}
	}
}

func mergeConsecutiveRoles(history []map[string]any) []map[string]any {
	var merged []map[string]any
	for _, item := range history {
		if len(merged) == 0 {
			merged = append(merged, item)
			continue
		}
		prev := merged[len(merged)-1]
		if item["userInputMessage"] != nil && prev["userInputMessage"] != nil {
			prevUIM := prev["userInputMessage"].(map[string]any)
			curUIM := item["userInputMessage"].(map[string]any)
			prevContent, _ := prevUIM["content"].(string)
			curContent, _ := curUIM["content"].(string)
			prevUIM["content"] = prevContent + "\n\n" + curContent
			curCtx, _ := curUIM["userInputMessageContext"].(map[string]any)
			if curCtx != nil {
				prevCtx, _ := prevUIM["userInputMessageContext"].(map[string]any)
				if prevCtx == nil {
					prevUIM["userInputMessageContext"] = curCtx
				} else {
					if tr, ok := curCtx["toolResults"].([]any); ok {
						prevTr, _ := prevCtx["toolResults"].([]any)
						prevCtx["toolResults"] = append(prevTr, tr...)
					}
				}
			}
		} else if item["assistantResponseMessage"] != nil && prev["assistantResponseMessage"] != nil {
			prevARM := prev["assistantResponseMessage"].(map[string]any)
			curARM := item["assistantResponseMessage"].(map[string]any)
			prevContent, _ := prevARM["content"].(string)
			curContent, _ := curARM["content"].(string)
			prevARM["content"] = prevContent + "\n\n" + curContent
			if curTU, ok := curARM["toolUses"].([]any); ok && len(curTU) > 0 {
				prevTU, _ := prevARM["toolUses"].([]any)
				prevARM["toolUses"] = append(prevTU, curTU...)
			}
		} else {
			merged = append(merged, item)
		}
	}
	return merged
}

func ensureAlternatingRoles(history []map[string]any) []map[string]any {
	var out []map[string]any
	for _, item := range history {
		if len(out) > 0 {
			last := out[len(out)-1]
			if item["userInputMessage"] != nil && last["userInputMessage"] != nil {
				out = append(out, map[string]any{
					"assistantResponseMessage": map[string]any{"content": "(empty)"},
				})
			}
		}
		out = append(out, item)
	}
	return out
}

func fixOrphanedToolResults(history []map[string]any) {
	for i := 0; i < len(history); i++ {
		item := history[i]
		uim, ok := item["userInputMessage"].(map[string]any)
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
		prev := history[i-1]
		arm, _ := prev["assistantResponseMessage"].(map[string]any)
		tuArr, _ := arm["toolUses"].([]any)
		if len(tuArr) > 0 {
			continue
		}
		var texts []string
		for _, tr := range trArr {
			m, _ := tr.(map[string]any)
			id, _ := m["toolUseId"].(string)
			content, _ := m["content"].([]any)
			var txt string
			for _, c := range content {
				cm, _ := c.(map[string]any)
				if t, ok := cm["text"].(string); ok {
					txt += t
				}
			}
			if id != "" {
				texts = append(texts, fmt.Sprintf("[Tool Result (%s)]\n%s", id, txt))
			} else {
				texts = append(texts, fmt.Sprintf("[Tool Result]\n%s", txt))
			}
		}
		existing, _ := uim["content"].(string)
		joined := strings.Join(texts, "\n\n")
		if existing != "" {
			uim["content"] = existing + "\n\n" + joined
		} else {
			uim["content"] = joined
		}
		delete(ctx, "toolResults")
		if len(ctx) == 0 {
			delete(uim, "userInputMessageContext")
		}
	}
}

func fixOrphanedToolResultsSingle(currentMessage map[string]any, history []map[string]any) {
	if currentMessage == nil {
		return
	}
	uim, ok := currentMessage["userInputMessage"].(map[string]any)
	if !ok {
		return
	}
	ctx, ok := uim["userInputMessageContext"].(map[string]any)
	if !ok {
		return
	}
	trArr, _ := ctx["toolResults"].([]any)
	if len(trArr) == 0 {
		return
	}
	var lastHistory map[string]any
	if len(history) > 0 {
		lastHistory = history[len(history)-1]
	}
	arm, _ := lastHistory["assistantResponseMessage"].(map[string]any)
	tuArr, _ := arm["toolUses"].([]any)
	if len(tuArr) > 0 {
		return
	}
	var texts []string
	for _, tr := range trArr {
		m, _ := tr.(map[string]any)
		id, _ := m["toolUseId"].(string)
		content, _ := m["content"].([]any)
		var txt string
		for _, c := range content {
			cm, _ := c.(map[string]any)
			if t, ok := cm["text"].(string); ok {
				txt += t
			}
		}
		if id != "" {
			texts = append(texts, fmt.Sprintf("[Tool Result (%s)]\n%s", id, txt))
		} else {
			texts = append(texts, fmt.Sprintf("[Tool Result]\n%s", txt))
		}
	}
	existing, _ := uim["content"].(string)
	joined := strings.Join(texts, "\n\n")
	if existing != "" {
		uim["content"] = existing + "\n\n" + joined
	} else {
		uim["content"] = joined
	}
	delete(ctx, "toolResults")
	if len(ctx) == 0 {
		delete(uim, "userInputMessageContext")
	}
}

func flattenToolInteractions(messages []Message) []Message {
	var out []Message
	for _, msg := range messages {
		if msg.Role == "tool" {
			content := msgContentString(msg.Content)
			out = append(out, Message{Role: "user", Content: "[Tool result: " + content + "]"})
			continue
		}
		if msg.Role == "assistant" {
			var parts []string
			if s, ok := msg.Content.(string); ok {
				if s != "" {
					parts = append(parts, s)
				}
			} else if arr, ok := msg.Content.([]any); ok {
				for _, c := range arr {
					if m, ok := c.(map[string]any); ok {
						t, _ := m["type"].(string)
						if t == "text" {
							parts = append(parts, fmt.Sprintf("%v", m["text"]))
						} else if t == "tool_use" {
							name := fmt.Sprintf("%v", m["name"])
							input, _ := json.Marshal(m["input"])
							parts = append(parts, "[Tool call: "+name+"("+string(input)+")]")
						}
					}
				}
			}
			for _, tc := range msg.ToolCalls {
				parts = append(parts, "[Tool call: "+tc.Function.Name+"("+tc.Function.Arguments+")]")
			}
			out = append(out, Message{Role: "assistant", Content: strings.Join(parts, "\n")})
			continue
		}
		if msg.Role == "user" {
			if arr, ok := msg.Content.([]any); ok {
				var newContent []any
				for _, c := range arr {
					if m, ok := c.(map[string]any); ok && m["type"] == "tool_result" {
						text := serializeToolResultContent(m["content"])
						newContent = append(newContent, map[string]any{"type": "text", "text": "[Tool result: " + text + "]"})
					} else {
						newContent = append(newContent, c)
					}
				}
				out = append(out, Message{Role: "user", Content: newContent})
				continue
			}
		}
		out = append(out, msg)
	}
	return out
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
				isError, _ := m["is_error"].(bool)
				status := "success"
				if isError {
					status = "error"
				}
				toolResults = append(toolResults, map[string]any{
					"toolUseId": toolUseID,
					"status":    status,
					"content":   []any{map[string]any{"text": text}},
				})
			}
		}
		return strings.Join(texts, "\n"), images, toolResults
	}
	return "", nil, nil
}

func extractAssistantContent(msg Message) (string, []any) {
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
					toolID = stableToolUseID(fmt.Sprintf("%v", m["name"]), len(toolUses))
				}
				input := parseToolInput(m["input"])
				toolUses = append(toolUses, map[string]any{
					"toolUseId": toolID,
					"name":      m["name"],
					"input":     input,
				})
			}
		}
		textContent = strings.TrimSpace(strings.Join(texts, "\n"))
	}

	for idx, tc := range msg.ToolCalls {
		toolID := tc.ID
		if toolID == "" {
			toolID = stableToolUseID(tc.Function.Name, idx)
		}
		input := parseToolInput(tc.Function.Arguments)
		toolUses = append(toolUses, map[string]any{
			"toolUseId": toolID,
			"name":      tc.Function.Name,
			"input":     input,
		})
	}

	return textContent, toolUses
}

func stableToolUseID(name string, index int) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s:%d", name, index)))
	ns := uuid.MustParse("a1b2c3d4-e5f6-7890-abcd-ef1234567890")
	return uuid.NewSHA1(ns, h[:]).String()
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

func convertTools(tools []Tool, upstreamModel string, toolDocsParts *[]string) []any {
	var out []any
	for _, t := range tools {
		name := t.Function.Name
		desc := t.Function.Description
		if desc == "" {
			desc = "Tool: " + name
		}
		if len(desc) > toolDescThreshold {
			if toolDocsParts != nil {
				*toolDocsParts = append(*toolDocsParts, fmt.Sprintf("## Tool: %s\n\n%s", name, desc))
			}
			desc = fmt.Sprintf("[Full documentation in system prompt under '## Tool: %s']", name)
		}
		schema := t.Function.Parameters
		if schema == nil {
			schema = map[string]any{"type": "object", "properties": map[string]any{}, "required": []any{}}
		} else {
			schema = normalizeKiroToolSchema(schema)
		}
		out = append(out, map[string]any{
			"toolSpecification": map[string]any{
				"name":        name,
				"description": desc,
				"inputSchema": map[string]any{"json": schema},
			},
		})
	}
	return out
}

func msgContentString(content any) string {
	if s, ok := content.(string); ok {
		return s
	}
	b, _ := json.Marshal(content)
	return string(b)
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

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
