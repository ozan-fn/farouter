package rtk

import (
	"encoding/json"
	"log"
	"strings"
)

// Size guards match VansRouter open-sse/rtk/constants.js
const (
	MinCompressSize = 500           // bytes; skip tiny blobs
	RawCap          = 10 * 1024 * 1024 // 10 MiB; skip oversized blobs
)

// ProcessToolMessages scans messages array and applies RTK filtering to tool outputs
func ProcessToolMessages(messages []map[string]any) []map[string]any {
	for i := range messages {
		msg := messages[i]

		// Only process tool role messages — user/assistant messages are NOT tool output
		role, _ := msg["role"].(string)
		if role != "tool" {
			continue
		}

		content := extractContent(msg["content"])
		if content == "" {
			continue
		}

		filtered := ProcessOutput(content)
		if filtered != content {
			msg["content"] = filtered
		}
	}

	return messages
}

// ProcessKiroBody applies RTK filtering directly on the Kiro payload structure.
// Navigates conversationState.history[].userInputMessage.userInputMessageContext.toolResults[].content[].text
// matching VansRouter compressKiroFormat in open-sse/rtk/index.js
func ProcessKiroBody(body []byte) []byte {
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return body
	}

	state, _ := raw["conversationState"].(map[string]any)
	if state == nil {
		return body
	}

	// Collect all user messages that may have toolResults
	var allMessages []map[string]any
	if history, ok := state["history"].([]any); ok {
		for _, h := range history {
			if m, ok := h.(map[string]any); ok {
				allMessages = append(allMessages, m)
			}
		}
	}
	if current, ok := state["currentMessage"].(map[string]any); ok {
		allMessages = append(allMessages, current)
	}

	changed := false
	for _, msg := range allMessages {
		uim, _ := msg["userInputMessage"].(map[string]any)
		if uim == nil {
			continue
		}
		ctx, _ := uim["userInputMessageContext"].(map[string]any)
		if ctx == nil {
			continue
		}
		toolResults, _ := ctx["toolResults"].([]any)
		if len(toolResults) == 0 {
			continue
		}
		for _, tr := range toolResults {
			trMap, _ := tr.(map[string]any)
			if trMap == nil {
				continue
			}
			status, _ := trMap["status"].(string)
			if status == "error" {
				continue // preserve error traces
			}
			content, _ := trMap["content"].([]any)
			if len(content) == 0 {
				continue
			}
			for _, part := range content {
				partMap, _ := part.(map[string]any)
				if partMap == nil {
					continue
				}
				text, _ := partMap["text"].(string)
				if text == "" {
					continue
				}
				if len(text) < MinCompressSize || len(text) > RawCap {
					continue
				}
				filtered := ProcessOutput(text)
				if filtered != "" && filtered != text {
					log.Printf("[rtk] kiro toolResult: %dB→%dB", len(text), len(filtered))
					partMap["text"] = filtered
					changed = true
				}
			}
		}
	}

	if !changed {
		return body
	}

	b, err := json.Marshal(raw)
	if err != nil {
		return body
	}
	return b
}

func extractContent(content any) string {
	switch v := content.(type) {
	case string:
		return v
	case []any:
		var parts []string
		for _, part := range v {
			if m, ok := part.(map[string]any); ok {
				if text, ok := m["text"].(string); ok {
					parts = append(parts, text)
				}
			}
		}
		return strings.Join(parts, "\n")
	default:
		b, _ := json.Marshal(content)
		return string(b)
	}
}
