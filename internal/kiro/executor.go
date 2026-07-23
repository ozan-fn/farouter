package kiro

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

var baseURLs = []string{
	"https://runtime.us-east-1.kiro.dev/generateAssistantResponse",
	"https://codewhisperer.us-east-1.amazonaws.com/generateAssistantResponse",
	"https://q.us-east-1.amazonaws.com/generateAssistantResponse",
}

type Credentials struct {
	AccessToken  string
	RefreshToken string
	ProfileArn   string
	PSD          ProviderSpecificData
}

type ChatRequest struct {
	Model           string   `json:"model"`
	Messages        []Message `json:"messages"`
	Stream          bool      `json:"stream"`
	Temperature     *float64  `json:"temperature,omitempty"`
	TopP            *float64  `json:"top_p,omitempty"`
	ReasoningEffort string    `json:"reasoning_effort,omitempty"`
	Tools           []Tool    `json:"tools,omitempty"`
}

type Message struct {
	Role       string     `json:"role"`
	Content    any        `json:"content"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
}

type Tool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

type ToolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type convertResult struct {
	history        []map[string]any
	currentMessage map[string]any
}

var ErrExhausted = fmt.Errorf("kiro: account exhausted (402)")
var ErrSuspended = fmt.Errorf("kiro: account suspended (403)")

func Execute(creds Credentials, req ChatRequest, w http.ResponseWriter, conversationID string) error {
	resolved := ResolveModel(req.Model)

	authMethod := creds.PSD.AuthMethod
	accountBoundAuth := authMethod == "api_key" || authMethod == "idc" || authMethod == "external_idp"
	profileArn := ""
	if accountBoundAuth {
		profileArn = creds.PSD.ProfileArn
	} else {
		if creds.ProfileArn != "" {
			profileArn = creds.ProfileArn
		} else {
			profileArn = DefaultProfileArn(authMethod)
		}
	}

	kiroBody, err := buildKiroRequest(req, resolved, profileArn, conversationID)
	if err != nil {
		return err
	}

	resp, err := sendToKiro(creds, kiroBody)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(resp.Body)
		if resp.StatusCode == http.StatusPaymentRequired {
			return ErrExhausted
		}
		if resp.StatusCode == http.StatusForbidden {
			var e struct{ Reason string `json:"reason"` }
			json.Unmarshal(errBody, &e)
			if e.Reason == "TEMPORARILY_SUSPENDED" {
				return ErrSuspended
			}
		}
		return fmt.Errorf("kiro upstream: %s — %s", resp.Status, string(errBody))
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")

	return transformEventStreamToSSE(resp, resolved.Upstream, w)
}

func buildKiroRequest(req ChatRequest, resolved ResolvedModel, profileArn string, conversationID string) ([]byte, error) {
	upstreamModel := resolved.Upstream
	timestamp := time.Now().UTC().Format(time.RFC3339)

	var systemParts []string
	thinkingBudget := ResolveThinkingBudget(req.ReasoningEffort, req.Model)
	usesNativeGptEffort := IsGPTModel(upstreamModel) && req.ReasoningEffort != ""
	if thinkingBudget > 0 && !usesNativeGptEffort {
		systemParts = append(systemParts, BuildThinkingSystemPrefix(thinkingBudget))
	}
	if resolved.Agentic {
		systemParts = append(systemParts, AgenticSystemPrompt)
	}
	systemPrompt := strings.Join(systemParts, "\n\n")
	currentTimeContext := fmt.Sprintf("[Context: Current time is %s]", timestamp)

	var contentPrefixParts []string
	if systemPrompt != "" {
		contentPrefixParts = append(contentPrefixParts, systemPrompt)
	}
	contentPrefixParts = append(contentPrefixParts, currentTimeContext)
	contentPrefix := strings.Join(contentPrefixParts, "\n\n")

	result := convertMessages(req.Messages, req.Tools, upstreamModel)
	history := result.history
	currentMsg := result.currentMessage

	if len(req.Tools) > 0 {
		reconcileOrphanedToolResults(history, currentMsg)
	}

	history, currentMsg = applySessionReplay(
		conversationID, upstreamModel, systemPrompt,
		contentPrefix, currentTimeContext,
		history, currentMsg,
	)

	if history == nil {
		history = []map[string]any{}
	}

	replayCurrent := currentMsg["userInputMessage"].(map[string]any)
	uimPayload := map[string]any{
		"content": replayCurrent["content"],
		"modelId": upstreamModel,
		"origin":  "AI_EDITOR",
	}
	if images, ok := replayCurrent["images"]; ok {
		uimPayload["images"] = images
	}
	if ctx, ok := replayCurrent["userInputMessageContext"]; ok {
		uimPayload["userInputMessageContext"] = ctx
	}

	payload := map[string]any{
		"conversationState": map[string]any{
			"chatTriggerType":     "MANUAL",
			"conversationId":      conversationID,
		"agentContinuationId": GetOrCreateContinuationID(conversationID, uuid.NewString),
			"agentTaskType":       "vibe",
			"currentMessage":      map[string]any{"userInputMessage": uimPayload},
			"history":             history,
		},
		"agentMode": "vibe",
	}

	if profileArn != "" {
		payload["profileArn"] = profileArn
	}
	if systemPrompt != "" {
		payload["systemPrompt"] = systemPrompt
	}

	additionalFields := BuildAdditionalModelRequestFields(req.ReasoningEffort, upstreamModel)
	if additionalFields != nil {
		payload["additionalModelRequestFields"] = additionalFields
	}

	inferenceConfig := map[string]any{"maxTokens": 32000}
	if req.Temperature != nil {
		inferenceConfig["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		inferenceConfig["topP"] = *req.TopP
	}
	payload["inferenceConfig"] = inferenceConfig

	return json.Marshal(payload)
}

func convertMessages(messages []Message, tools []Tool, upstreamModel string) convertResult {
	clientHasTools := len(tools) > 0

	if !clientHasTools {
		messages = flattenToolInteractions(messages)
	}

	var history []map[string]any
	var currentMessage map[string]any

	var pendingUserContent []string
	var pendingAssistantContent []string
	var pendingToolResults []map[string]any
	var pendingImages []map[string]any
	currentRole := ""
	toolsInjected := false

	flush := func() {
		if currentRole == "user" {
			content := strings.Join(pendingUserContent, "\n\n")
			if strings.TrimSpace(content) == "" {
				content = "continue"
			}
			uim := map[string]any{
				"content": content,
				"modelId": upstreamModel,
			}
			if len(pendingImages) > 0 {
				uim["images"] = pendingImages
			}
			ctx := map[string]any{}
			if len(pendingToolResults) > 0 {
				ctx["toolResults"] = pendingToolResults
			}
			if clientHasTools && !toolsInjected {
				ctx["tools"] = convertTools(tools)
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
				content = "..."
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
				toolContent := msgContentString(msg.Content)
				pendingToolResults = append(pendingToolResults, map[string]any{
					"toolUseId": msg.ToolCallID,
					"status":    "success",
					"content":   []map[string]any{{"text": toolContent}},
				})
			} else {
				content, images, toolResults := extractUserContent(msg.Content)
				pendingImages = append(pendingImages, images...)
				pendingToolResults = append(pendingToolResults, toolResults...)
				if content != "" {
					if wasSystem {
						content = "<instructions>\n" + content + "\n</instructions>"
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

	// Pop last userInputMessage as currentMessage
	for i := len(history) - 1; i >= 0; i-- {
		if _, ok := history[i]["userInputMessage"]; ok {
			currentMessage = history[i]
			history = append(history[:i], history[i+1:]...)
			break
		}
	}

	// Grab tools from first history item BEFORE cleanup
	var firstHistoryTools any
	for _, h := range history {
		if uim, ok := h["userInputMessage"].(map[string]any); ok {
			if ctx, ok := uim["userInputMessageContext"].(map[string]any); ok {
				if t, ok := ctx["tools"]; ok {
					firstHistoryTools = t
				}
			}
			break
		}
	}

	// Clean up history
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
	}

	// Merge consecutive user messages
	var merged []map[string]any
	for _, item := range history {
		if _, ok := item["userInputMessage"]; ok && len(merged) > 0 {
			if _, ok := merged[len(merged)-1]["userInputMessage"]; ok {
				prev := merged[len(merged)-1]["userInputMessage"].(map[string]any)
				cur := item["userInputMessage"].(map[string]any)
				prev["content"] = prev["content"].(string) + "\n\n" + cur["content"].(string)
				prevCtx, _ := prev["userInputMessageContext"].(map[string]any)
				curCtx, _ := cur["userInputMessageContext"].(map[string]any)
				if curCtx != nil {
					if prevCtx == nil {
						prev["userInputMessageContext"] = curCtx
					} else {
						if tr, ok := curCtx["toolResults"].([]map[string]any); ok {
							prevTr, _ := prevCtx["toolResults"].([]map[string]any)
							prevCtx["toolResults"] = append(prevTr, tr...)
						}
						if t, ok := curCtx["tools"]; ok {
							prevCtx["tools"] = t
						}
					}
				}
				continue
			}
		}
		merged = append(merged, item)
	}

	// Fallback currentMessage
	if currentMessage == nil {
		currentMessage = map[string]any{
			"userInputMessage": map[string]any{
				"content": "",
				"modelId": upstreamModel,
				"origin":  "AI_EDITOR",
			},
		}
	}

	// Inject tools into currentMessage if needed
	if firstHistoryTools != nil {
		uim := currentMessage["userInputMessage"].(map[string]any)
		ctx, _ := uim["userInputMessageContext"].(map[string]any)
		if ctx == nil {
			ctx = map[string]any{}
		}
		if _, ok := ctx["tools"]; !ok {
			ctx["tools"] = firstHistoryTools
			uim["userInputMessageContext"] = ctx
		}
	}

	return convertResult{history: merged, currentMessage: currentMessage}
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
		// User: replace tool_result blocks with text
		if msg.Role == "user" {
			if arr, ok := msg.Content.([]any); ok {
				var newContent []any
				for _, c := range arr {
					if m, ok := c.(map[string]any); ok && m["type"] == "tool_result" {
						text := toolResultContentToText(m["content"])
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

func toolResultContentToText(content any) string {
	switch v := content.(type) {
	case string:
		return v
	case []any:
		var parts []string
		for _, c := range v {
			if m, ok := c.(map[string]any); ok {
				if t, ok := m["text"].(string); ok {
					parts = append(parts, t)
				}
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

func extractUserContent(content any) (string, []map[string]any, []map[string]any) {
	switch v := content.(type) {
	case string:
		return v, nil, nil
	case []any:
		var texts []string
		var images []map[string]any
		var toolResults []map[string]any
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
				text := toolResultContentToText(m["content"])
				toolResults = append(toolResults, map[string]any{
					"toolUseId": toolUseID,
					"status":    "success",
					"content":   []map[string]any{{"text": text}},
				})
			}
		}
		return strings.Join(texts, "\n"), images, toolResults
	}
	return "", nil, nil
}

func extractAssistantContent(msg Message) (string, []map[string]any) {
	var textContent string
	var toolUses []map[string]any

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
					toolID = uuid.New().String()
				}
				toolUses = append(toolUses, map[string]any{
					"toolUseId": toolID,
					"name":      m["name"],
					"input":     m["input"],
				})
			}
		}
		textContent = strings.TrimSpace(strings.Join(texts, "\n"))
	}

	for _, tc := range msg.ToolCalls {
		toolID := tc.ID
		if toolID == "" {
			toolID = uuid.New().String()
		}
		var input map[string]any
		json.Unmarshal([]byte(tc.Function.Arguments), &input)
		toolUses = append(toolUses, map[string]any{
			"toolUseId": toolID,
			"name":      tc.Function.Name,
			"input":     input,
		})
	}

	return textContent, toolUses
}

func convertTools(tools []Tool) []any {
	var out []any
	for _, t := range tools {
		name := t.Function.Name
		desc := t.Function.Description
		if desc == "" {
			desc = "Tool: " + name
		}
		schema := t.Function.Parameters
		if schema == nil {
			schema = map[string]any{"type": "object", "properties": map[string]any{}, "required": []any{}}
		} else {
			if _, ok := schema["required"]; !ok {
				schema["required"] = []any{}
			}
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

func sendToKiro(creds Credentials, body []byte) (*http.Response, error) {
	urls := make([]string, len(baseURLs))
	copy(urls, baseURLs)

	authMethod := creds.PSD.AuthMethod
	isCodeWhispererSurface := authMethod == "api_key" || authMethod == "external_idp" || authMethod == "idc"
	if isCodeWhispererSurface {
		var amazon, others []string
		for _, u := range urls {
			if strings.Contains(u, "amazonaws.com") {
				amazon = append(amazon, u)
			} else {
				others = append(others, u)
			}
		}
		urls = append(amazon, others...)
	}

	var lastErr error
	for _, baseURL := range urls {
		req, err := http.NewRequest(http.MethodPost, baseURL, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/vnd.amazon.eventstream")
		req.Header.Set("X-Amz-Target", "AmazonCodeWhispererStreamingService.GenerateAssistantResponse")
		req.Header.Set("User-Agent", "AWS-SDK-JS/3.0.0 kiro-ide/1.0.0")
		req.Header.Set("X-Amz-User-Agent", "aws-sdk-js/3.0.0 kiro-ide/1.0.0")
		req.Header.Set("Amz-Sdk-Request", "attempt=1; max=3")
		req.Header.Set("Amz-Sdk-Invocation-Id", uuid.New().String())

		if authMethod == "api_key" {
			req.Header.Set("Authorization", "Bearer "+creds.AccessToken)
			req.Header.Set("tokentype", "API_KEY")
		} else {
			req.Header.Set("Authorization", "Bearer "+creds.AccessToken)
			if authMethod == "external_idp" {
				req.Header.Set("TokenType", "EXTERNAL_IDP")
			}
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			resp.Body.Close()
			lastErr = fmt.Errorf("429 from %s", baseURL)
			continue
		}
		return resp, nil
	}
	return nil, fmt.Errorf("all kiro endpoints failed: %w", lastErr)
}
