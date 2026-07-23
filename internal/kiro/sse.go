package kiro

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type sseState struct {
	chunkIndex   int
	hasText      bool
	hasReasoning bool
	hasToolCalls bool
	inThinking   bool
	stopReason   string
	explicitStop bool
	tools        map[string]*toolBuffer
	usage        map[string]any
}

type toolBuffer struct {
	id         string
	name       string
	inputParts []string
}

func transformEventStreamToSSE(resp *http.Response, model string, w http.ResponseWriter) error {
	responseID := fmt.Sprintf("chatcmpl-%d", time.Now().UnixMilli())
	created := time.Now().Unix()
	flusher, _ := w.(http.Flusher)

	state := &sseState{
		tools: make(map[string]*toolBuffer),
	}

	emitSSE := func(delta map[string]any, finishReason any, usage map[string]any) {
		chunk := map[string]any{
			"id":      responseID,
			"object":  "chat.completion.chunk",
			"created": created,
			"model":   model,
			"choices": []map[string]any{{
				"index":         0,
				"delta":         delta,
				"finish_reason": finishReason,
			}},
		}
		if usage != nil {
			chunk["usage"] = usage
		}
		b, _ := json.Marshal(chunk)
		fmt.Fprintf(w, "data: %s\n\n", b)
		if flusher != nil {
			flusher.Flush()
		}
	}

	emitDelta := func(delta map[string]any) {
		if state.chunkIndex == 0 {
			delta["role"] = "assistant"
		}
		state.chunkIndex++
		emitSSE(delta, nil, nil)
	}

	err := ParseEventStream(resp.Body, func(event Event) error {
		msgType := event.Headers[":message-type"]
		if msgType == "error" || msgType == "exception" {
			msg := ""
			if event.Payload != nil {
				if m, ok := event.Payload["message"].(string); ok {
					msg = m
				}
			}
			return fmt.Errorf("kiro upstream eventstream error: %s", msg)
		}

		eventType := event.Headers[":event-type"]

		switch eventType {
		case "assistantResponseEvent":
			content, _ := event.Payload["content"].(string)
			if state.inThinking {
				end := strings.Index(content, "</thinking>")
				if end < 0 {
					content = ""
				} else {
					state.inThinking = false
					content = strings.TrimPrefix(content[end+11:], "\n")
				}
			} else {
				start := strings.Index(content, "<thinking>")
				if start >= 0 {
					end := strings.Index(content, "</thinking>")
					if end < 0 {
						state.inThinking = true
						content = content[:start]
					} else {
						content = content[:start] + strings.TrimPrefix(content[end+11:], "\n")
					}
				}
			}
			if content != "" || !state.hasReasoning {
				if content != "" {
					state.hasText = true
				}
				emitDelta(map[string]any{"content": content})
			}

		case "reasoningContentEvent":
			var content string
			if event.Payload != nil {
				if v, ok := event.Payload["reasoningContentEvent"]; ok {
					switch vv := v.(type) {
					case string:
						content = vv
					case map[string]any:
						content, _ = vv["text"].(string)
						if content == "" {
							content, _ = vv["content"].(string)
						}
					}
				} else if s, ok := event.Payload["text"].(string); ok {
					content = s
				}
			}
			if content != "" {
				state.hasReasoning = true
				emitDelta(map[string]any{"reasoning_content": content})
			}

		case "codeEvent":
			content, _ := event.Payload["content"].(string)
			if content != "" {
				state.hasText = true
				emitDelta(map[string]any{"content": content})
			}

		case "toolUseEvent":
			var values []map[string]any
			if arr, ok := event.Payload["toolUseEvent"].([]any); ok {
				for _, v := range arr {
					if m, ok := v.(map[string]any); ok {
						values = append(values, m)
					}
				}
			} else if m, ok := event.Payload["toolUseEvent"].(map[string]any); ok {
				values = append(values, m)
			} else if arr, ok := event.Payload[""].([]any); ok {
				for _, v := range arr {
					if m, ok := v.(map[string]any); ok {
						values = append(values, m)
					}
				}
			} else {
				// payload IS the tool event
				if name, ok := event.Payload["name"].(string); ok && name != "" {
					values = append(values, event.Payload)
				}
			}
			for _, value := range values {
				name, _ := value["name"].(string)
				toolID, _ := value["toolUseId"].(string)
				if toolID == "" {
					toolID = fmt.Sprintf("call_%d_%d", created, len(state.tools)+1)
				}
				tool, exists := state.tools[toolID]
				if !exists {
					tool = &toolBuffer{id: toolID, name: name}
					state.tools[toolID] = tool
				}
				if input, ok := value["input"]; ok {
					switch iv := input.(type) {
					case string:
						tool.inputParts = append(tool.inputParts, iv)
					case map[string]any:
						b, _ := json.Marshal(iv)
						tool.inputParts = append(tool.inputParts, string(b))
					}
				}
			}

		case "messageStopEvent":
			state.explicitStop = true
			reason := normalizeStopReason(event.Payload)
			if reason == "" {
				if len(state.tools) > 0 {
					reason = "tool_use"
				} else {
					reason = "end_turn"
				}
			}
			state.stopReason = mergeStopReason(state.stopReason, reason)

		case "metadataEvent", "MetadataEvent":
			meta := event.Payload
			if m, ok := event.Payload["metadataEvent"].(map[string]any); ok {
				meta = m
			} else if m, ok := event.Payload["metadata"].(map[string]any); ok {
				meta = m
			}
			reason := normalizeStopReason(meta)
			if reason != "" {
				state.explicitStop = true
				state.stopReason = mergeStopReason(state.stopReason, reason)
			}

		case "metricsEvent":
			metrics := event.Payload
			if m, ok := event.Payload["metricsEvent"].(map[string]any); ok {
				metrics = m
			}
			prompt := toInt(metrics["inputTokens"])
			completion := toInt(metrics["outputTokens"])
			if prompt > 0 || completion > 0 {
				state.usage = map[string]any{
					"prompt_tokens":     prompt,
					"completion_tokens": completion,
					"total_tokens":      prompt + completion,
				}
				if v := toInt(metrics["cacheReadInputTokens"]); v > 0 {
					state.usage["cache_read_input_tokens"] = v
				}
				if v := toInt(metrics["cacheCreationInputTokens"]); v > 0 {
					state.usage["cache_creation_input_tokens"] = v
				}
			}
		}
		return nil
	})

	if err != nil {
		errChunk, _ := json.Marshal(map[string]any{
			"error": map[string]any{
				"message": err.Error(),
				"type":    "upstream_error",
			},
		})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", errChunk)
		if flusher != nil {
			flusher.Flush()
		}
		return nil
	}

	// Emit tool calls
	toolIndex := 0
	for _, tool := range state.tools {
		state.hasToolCalls = true
		arguments := strings.Join(tool.inputParts, "")
		emitDelta(map[string]any{
			"tool_calls": []map[string]any{{
				"index": toolIndex,
				"id":    tool.id,
				"type":  "function",
				"function": map[string]any{
					"name":      tool.name,
					"arguments": "",
				},
			}},
		})
		emitDelta(map[string]any{
			"tool_calls": []map[string]any{{
				"index":    toolIndex,
				"function": map[string]any{"arguments": arguments},
			}},
		})
		toolIndex++
	}

	finishReason := "stop"
	if state.hasToolCalls {
		finishReason = "tool_calls"
	} else if state.stopReason == "max_tokens" {
		finishReason = "length"
	}

	emitSSE(map[string]any{}, finishReason, state.usage)
	fmt.Fprintf(w, "data: [DONE]\n\n")
	if flusher != nil {
		flusher.Flush()
	}
	return nil
}

func normalizeStopReason(payload map[string]any) string {
	if payload == nil {
		return ""
	}
	raw := ""
	if v, ok := payload["stopReason"].(string); ok {
		raw = v
	} else if v, ok := payload["stop_reason"].(string); ok {
		raw = v
	}
	raw = strings.ToLower(strings.ReplaceAll(raw, "-", "_"))
	switch raw {
	case "endturn", "end_turn", "stop", "stop_sequence":
		return "end_turn"
	case "tooluse", "tool_use", "tool_calls":
		return "tool_use"
	case "maxtokens", "max_tokens", "max_output_tokens", "length":
		return "max_tokens"
	}
	return raw
}

func mergeStopReason(current, incoming string) string {
	if incoming == "" {
		return current
	}
	if current == "" {
		return incoming
	}
	severity := func(r string) int {
		switch r {
		case "tool_use", "end_turn":
			return 1
		case "max_tokens":
			return 2
		}
		return 3
	}
	if severity(incoming) > severity(current) {
		return incoming
	}
	return current
}

func toInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	}
	return 0
}
