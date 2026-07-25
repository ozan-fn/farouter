package kiro

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

type kiroThinkingState struct {
	thinkingMode bool
	pendingTag   string
}

const partialTagMaxLen = 11

type kiroSSEState struct {
	chunkIndex        int
	startEmitted      bool
	hasText           bool
	hasReasoning      bool
	hasToolCalls      bool
	stopReason        string
	explicitStop      bool
	tools             map[string]*kiroToolBuffer
	toolArgsBuffer    map[string]*kiroToolArgBuffer
	usage             map[string]any
	responseID        string
	created           int64
	model             string
	thinkingState     kiroThinkingState
	totalContentLength int
	contextUsagePct   float64
	hasContextUsage   bool
}

type kiroToolBuffer struct {
	id   string
	name string
}

type kiroToolArgBuffer struct {
	toolIndex   int
	canonical   string
	stringParts []string
	isObjectForm bool
}

func transformKiroToSSE(r io.Reader, model string, w io.Writer) error {
	state := &kiroSSEState{
		tools:          make(map[string]*kiroToolBuffer),
		toolArgsBuffer: make(map[string]*kiroToolArgBuffer),
		responseID:     fmt.Sprintf("chatcmpl-%d", time.Now().UnixMilli()),
		created:        time.Now().Unix(),
		model:          model,
	}

	emitDelta := func(delta map[string]any) {
		if !state.startEmitted {
			delta["role"] = "assistant"
			state.startEmitted = true
		}
		state.chunkIndex++
		chunk := map[string]any{
			"id":      state.responseID,
			"object":  "chat.completion.chunk",
			"created": state.created,
			"model":   model,
			"choices": []map[string]any{{
				"index": 0,
				"delta": delta,
			}},
		}
		b, _ := json.Marshal(chunk)
		fmt.Fprintf(w, "data: %s\n\n", b)
	}

	emitFinish := func(finishReason string, usage map[string]any) {
		chunk := map[string]any{
			"id":      state.responseID,
			"object":  "chat.completion.chunk",
			"created": state.created,
			"model":   model,
			"choices": []map[string]any{{
				"index":         0,
				"delta":         map[string]any{},
				"finish_reason": finishReason,
			}},
		}
		if usage != nil {
			chunk["usage"] = usage
		}
		b, _ := json.Marshal(chunk)
		fmt.Fprintf(w, "data: %s\n\n", b)
	}

	onContent := func(s string) {
		if s != "" {
			state.hasText = true
			state.totalContentLength += len(s)
			emitDelta(map[string]any{"content": s})
		}
	}

	onReasoning := func(s string) {
		if s != "" {
			state.hasReasoning = true
			state.totalContentLength += len(s)
			emitDelta(map[string]any{"reasoning_content": s})
		}
	}

	err := ParseEventStream(r, func(event Event) error {
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

		if !state.startEmitted {
			emitDelta(map[string]any{})
		}

		eventType := event.Headers[":event-type"]

		switch eventType {
		case "assistantResponseEvent":
			content, _ := event.Payload["content"].(string)
			splitInlineThinking(&state.thinkingState, content, onContent, onReasoning)

		case "reasoningContentEvent":
			handleKiroReasoningEvent(event.Payload, emitDelta, state)

		case "codeEvent":
			content, _ := event.Payload["content"].(string)
			if content != "" {
				state.hasText = true
				state.totalContentLength += len(content)
				emitDelta(map[string]any{"content": content})
			}

		case "toolUseEvent":
			handleKiroToolUseEvent(event.Payload, state)

		case "messageStopEvent":
			state.explicitStop = true
			reason := normalizeKiroStopReason(event.Payload)
			if reason == "" {
				if len(state.tools) > 0 {
					reason = "tool_use"
				} else {
					reason = "end_turn"
				}
			}
			state.stopReason = mergeKiroStopReason(state.stopReason, reason)

		case "metadataEvent", "MetadataEvent":
			meta := event.Payload
			if m, ok := event.Payload["metadataEvent"].(map[string]any); ok {
				meta = m
			} else if m, ok := event.Payload["metadata"].(map[string]any); ok {
				meta = m
			}
			reason := normalizeKiroStopReason(meta)
			if reason != "" {
				state.explicitStop = true
				state.stopReason = mergeKiroStopReason(state.stopReason, reason)
			}

		case "metricsEvent":
			handleKiroMetricsEvent(event.Payload, state)

		case "meteringEvent":
			handleKiroMeteringEvent(event.Payload, state)

		case "contextUsageEvent":
			handleKiroContextUsageEvent(event.Payload, state)
		}
		return nil
	})

	flushPendingThinking(&state.thinkingState, onContent, onReasoning)

	if err != nil {
		writeStreamError(w, 502, err.Error())
		fmt.Fprintf(w, "data: [DONE]\n\n")
		return err
	}

	flushKiroBufferedToolArgs(state, emitDelta)

	finishReason := "stop"
	if len(state.tools) > 0 {
		finishReason = "tool_calls"
	} else if state.stopReason == "max_tokens" {
		finishReason = "length"
	}

	usage := buildKiroUsage(state)
	emitFinish(finishReason, usage)
	fmt.Fprintf(w, "data: [DONE]\n\n")
	return nil
}

func handleKiroReasoningEvent(payload map[string]any, emitDelta func(map[string]any), state *kiroSSEState) {
	if payload == nil {
		return
	}
	var content string
	if v, ok := payload["reasoningContentEvent"]; ok {
		switch vv := v.(type) {
		case string:
			content = vv
		case map[string]any:
			content, _ = vv["text"].(string)
			if content == "" {
				content, _ = vv["Text"].(string)
			}
			if content == "" {
				content, _ = vv["content"].(string)
			}
		}
	} else if s, ok := payload["text"].(string); ok {
		content = s
	}
	if content != "" {
		state.hasReasoning = true
		state.totalContentLength += len(content)
		emitDelta(map[string]any{"reasoning_content": content})
	}
}

func handleKiroToolUseEvent(payload map[string]any, state *kiroSSEState) {
	var values []map[string]any
	if arr, ok := payload["toolUseEvent"].([]any); ok {
		for _, v := range arr {
			if m, ok := v.(map[string]any); ok {
				values = append(values, m)
			}
		}
	} else if m, ok := payload["toolUseEvent"].(map[string]any); ok {
		values = append(values, m)
	} else if arr, ok := payload[""].([]any); ok {
		for _, v := range arr {
			if m, ok := v.(map[string]any); ok {
				values = append(values, m)
			}
		}
	} else {
		if name, ok := payload["name"].(string); ok && name != "" {
			values = append(values, payload)
		}
	}

	for _, value := range values {
		name, _ := value["name"].(string)
		toolID, _ := value["toolUseId"].(string)
		if toolID == "" {
			toolID = fmt.Sprintf("call_%d_%d", state.created, len(state.tools)+1)
		}

		tool, exists := state.tools[toolID]
		if !exists {
			tool = &kiroToolBuffer{id: toolID, name: name}
			state.tools[toolID] = tool
		}

		input, ok := value["input"]
		if !ok {
			continue
		}

		buf, bufExists := state.toolArgsBuffer[toolID]
		if !bufExists {
			buf = &kiroToolArgBuffer{toolIndex: len(state.tools) - 1}
			state.toolArgsBuffer[toolID] = buf
		}

		switch iv := input.(type) {
		case string:
			buf.stringParts = append(buf.stringParts, iv)
			buf.isObjectForm = false
		case map[string]any:
			b, _ := json.Marshal(iv)
			buf.canonical = string(b)
			buf.isObjectForm = true
		}
	}
}

func handleKiroMetricsEvent(payload map[string]any, state *kiroSSEState) {
	metrics := payload
	if m, ok := payload["metricsEvent"].(map[string]any); ok {
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
		// Handle both camelCase and snake_case variants
		cacheRead := toInt(metrics["cacheReadInputTokens"])
		if cacheRead == 0 {
			cacheRead = toInt(metrics["cache_read_input_tokens"])
		}
		if cacheRead > 0 {
			state.usage["cache_read_input_tokens"] = cacheRead
		}
		cacheCreate := toInt(metrics["cacheCreationInputTokens"])
		if cacheCreate == 0 {
			cacheCreate = toInt(metrics["cache_creation_input_tokens"])
		}
		if cacheCreate > 0 {
			state.usage["cache_creation_input_tokens"] = cacheCreate
		}
	}
}

func handleKiroMeteringEvent(payload map[string]any, state *kiroSSEState) {
	metering := payload
	if m, ok := payload["meteringEvent"].(map[string]any); ok {
		metering = m
	}
	credits := toInt(metering["usage"])
	if credits > 0 {
		if state.usage == nil {
			state.usage = make(map[string]any)
		}
		state.usage["kiro_credits"] = credits
		unit := "credit"
		if u, ok := metering["unit"].(string); ok && u != "" {
			unit = u
		}
		state.usage["kiro_credit_unit"] = unit
	}
}

func handleKiroContextUsageEvent(payload map[string]any, state *kiroSSEState) {
	if pct, ok := payload["contextUsagePercentage"].(float64); ok {
		state.contextUsagePct = pct
		state.hasContextUsage = true
	}
}

func flushKiroBufferedToolArgs(state *kiroSSEState, emitDelta func(map[string]any)) {
	toolIndex := 0
	for toolID, tool := range state.tools {
		state.hasToolCalls = true

		buf, hasBuf := state.toolArgsBuffer[toolID]
		var arguments string
		if hasBuf {
			if buf.isObjectForm {
				arguments = buf.canonical
			} else {
				arguments = strings.Join(buf.stringParts, "")
			}
		}

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
		if arguments != "" {
			emitDelta(map[string]any{
				"tool_calls": []map[string]any{{
					"index":    toolIndex,
					"function": map[string]any{"arguments": arguments},
				}},
			})
		}
		toolIndex++
	}
}

func buildKiroUsage(state *kiroSSEState) map[string]any {
	if state.usage != nil {
		return state.usage
	}
	if !state.hasContextUsage && state.chunkIndex == 0 {
		return nil
	}
	completion := state.chunkIndex
	if completion == 0 {
		completion = state.totalContentLength / 4
	}
	prompt := 0
	if state.hasContextUsage {
		// Get model's context window; default to 200000
		contextWindow := DefaultContextLength
		for _, m := range KnownModels {
			if m.ID == state.model {
				if m.ContextLength > 0 {
					contextWindow = m.ContextLength
				}
				break
			}
		}
		prompt = int(state.contextUsagePct * float64(contextWindow) / 100)
	}
	return map[string]any{
		"prompt_tokens":     prompt,
		"completion_tokens": completion,
		"total_tokens":      prompt + completion,
	}
}

func normalizeKiroStopReason(payload map[string]any) string {
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

func mergeKiroStopReason(current, incoming string) string {
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

func splitInlineThinking(state *kiroThinkingState, raw string, onContent, onReasoning func(string)) {
	text := state.pendingTag + raw
	state.pendingTag = ""

	for len(text) > 0 {
		target := "</thinking>"
		if state.thinkingMode {
			target = "</thinking>"
		} else {
			target = "<thinking>"
		}

		idx := strings.Index(text, target)
		if idx < 0 {
			holdFrom := len(text)
			maxCheck := len(text) - partialTagMaxLen
			if maxCheck < 0 {
				maxCheck = 0
			}
			for i := maxCheck; i < len(text); i++ {
				tail := text[i:]
				if strings.HasPrefix(target, tail) && len(tail) > 0 {
					holdFrom = i
					break
				}
			}
			flushable := text[:holdFrom]
			if flushable != "" {
				if state.thinkingMode {
					onReasoning(flushable)
				} else {
					onContent(flushable)
				}
			}
			state.pendingTag = text[holdFrom:]
			return
		}

		before := text[:idx]
		if before != "" {
			if state.thinkingMode {
				onReasoning(before)
			} else {
				onContent(before)
			}
		}
		state.thinkingMode = !state.thinkingMode
		text = text[idx+len(target):]
	}
}

func flushPendingThinking(state *kiroThinkingState, onContent, onReasoning func(string)) {
	if state.pendingTag == "" {
		return
	}
	leftover := state.pendingTag
	state.pendingTag = ""
	if state.thinkingMode {
		onReasoning(leftover)
	} else {
		onContent(leftover)
	}
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
