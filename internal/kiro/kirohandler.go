package kiro

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"strings"
	"time"
)

type kiroThinkingState struct {
	thinkingMode bool
	pendingTag   string
}

const partialTagMaxLen = 11

var kiroEventTypes = map[string]bool{
	"assistantResponseEvent":  true,
	"reasoningContentEvent":   true,
	"codeEvent":               true,
	"toolUseEvent":            true,
	"messageStopEvent":        true,
	"metadataEvent":           true,
	"MetadataEvent":           true,
	"contextUsageEvent":       true,
	"meteringEvent":           true,
	"metricsEvent":            true,
}

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
	thinkingEnabled   bool
	totalContentLength int
	contextUsagePct   float64
	hasContextUsage   bool

	eventCounts          map[string]int
	transportState       string
	terminalProvenance   string
	bufferedToolBytes    int
}

type kiroToolBuffer struct {
	id   string
	name string
}

type kiroToolArgBuffer struct {
	toolIndex    int
	canonical    string
	stringParts  []string
	isObjectForm bool
}

type TransformOptions struct {
	OnTerminalState func(*IntegrityDiagnostics)
	MaxToolBytes    int
}

func transformKiroToSSE(r io.Reader, model string, thinkingEnabled bool, w io.Writer, opts *TransformOptions) error {
	maxToolBytes := KIRO_TOOL_CALL_REPAIR_BUFFER_MAX_BYTES / 2
	if opts != nil && opts.MaxToolBytes > 0 {
		maxToolBytes = opts.MaxToolBytes
	}

	state := &kiroSSEState{
		tools:           make(map[string]*kiroToolBuffer),
		toolArgsBuffer:  make(map[string]*kiroToolArgBuffer),
		responseID:      fmt.Sprintf("chatcmpl-%d", time.Now().UnixMilli()),
		created:         time.Now().Unix(),
		model:           model,
		thinkingEnabled: thinkingEnabled,
		eventCounts:     make(map[string]int),
		transportState:  "consuming_response",
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

	callTerminal := func(provenance string) {
		if opts == nil || opts.OnTerminalState == nil {
			return
		}
		d := buildDiagnostics(state)
		d.TerminalProvenance = provenance
		opts.OnTerminalState(d)
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
			state.transportState = "upstream_error"
			callTerminal("upstream_eventstream_error")
			return fmt.Errorf("kiro upstream eventstream error: %s", msg)
		}

		if !state.startEmitted {
			emitDelta(map[string]any{})
		}

		eventType := event.Headers[":event-type"]
		if kiroEventTypes[eventType] {
			state.eventCounts[eventType]++
		} else {
			state.eventCounts["other"]++
		}

		switch eventType {
		case "assistantResponseEvent":
			content, _ := event.Payload["content"].(string)
			if state.thinkingEnabled {
				splitInlineThinking(&state.thinkingState, content, onContent, onReasoning)
			} else {
				onContent(content)
			}

		case "reasoningContentEvent":
			if state.thinkingEnabled {
				handleKiroReasoningEvent(event.Payload, emitDelta, state)
			}

		case "codeEvent":
			content, _ := event.Payload["content"].(string)
			if content != "" {
				state.hasText = true
				state.totalContentLength += len(content)
				emitDelta(map[string]any{"content": content})
			}

		case "toolUseEvent":
			if err := handleKiroToolUseEvent(event.Payload, state, maxToolBytes); err != nil {
				return err
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
			merged := mergeStopReason(state.stopReason, reason)
			if merged != state.stopReason {
				state.terminalProvenance = "message_stop_event"
			}
			state.stopReason = merged

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
				merged := mergeStopReason(state.stopReason, reason)
				if merged != state.stopReason {
					state.terminalProvenance = "metadata_stop_reason"
				}
				state.stopReason = merged
			}

		case "metricsEvent":
			handleKiroMetricsEvent(event.Payload, state)

		case "meteringEvent":
			handleKiroMeteringEvent(event.Payload, state)

		case "contextUsageEvent":
			handleKiroContextUsageEvent(event.Payload, state)
		}
		state.transportState = "valid_complete_frame"
		return nil
	})

	flushPendingThinking(&state.thinkingState, onContent, onReasoning)

	if err != nil {
		log.Printf("[kiro] stream error: %v | events=%v", err, state.eventCounts)
		writeStreamError(w, 502, err.Error())
		fmt.Fprintf(w, "data: [DONE]\n\n")
		return err
	}

	state.transportState = "clean_eof"

	flushKiroBufferedToolArgs(state, emitDelta)

	disposition := stopDisposition(state.stopReason, len(state.tools) > 0)
	finishReason := "stop"
	if len(state.tools) > 0 {
		finishReason = "tool_calls"
	} else if state.stopReason == "max_tokens" {
		finishReason = "length"
	}

	if disposition == StopRetryableProtocolFail ||
		disposition == StopTerminalIncomplete ||
		disposition == StopTerminalRefusal ||
		disposition == StopUnknownFailure {
		provenance := state.terminalProvenance
		if provenance == "" {
			provenance = "metadata_stop_reason"
		}
		callTerminal(provenance)
		writeStreamError(w, 502, fmt.Sprintf("Kiro ended with non-success stop reason: %s", state.stopReason))
		fmt.Fprintf(w, "data: [DONE]\n\n")
		return fmt.Errorf("kiro non-success stop disposition: %s (reason=%s)", disposition, state.stopReason)
	}

	if state.hasMetering() && state.hasContextUsage && !hasUsageTokens(state.usage) {
		completion := state.totalContentLength
		if completion > 0 {
			completion = completion / 4
			if completion < 1 {
				completion = 1
			}
		}
		prompt := 0
		if state.hasContextUsage {
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
		state.usage = map[string]any{
			"prompt_tokens":     prompt,
			"completion_tokens": completion,
			"total_tokens":      prompt + completion,
		}
	}

	usage := buildKiroUsage(state)
	emitFinish(finishReason, usage)
	fmt.Fprintf(w, "data: [DONE]\n\n")

	provenance := state.terminalProvenance
	if provenance == "" {
		provenance = "clean_eventstream_eof"
	}
	callTerminal(provenance)
	return nil
}

func (s *kiroSSEState) hasMetering() bool {
	if s.usage == nil {
		return false
	}
	_, ok := s.usage["kiro_credits"]
	return ok
}

func hasUsageTokens(usage map[string]any) bool {
	if usage == nil {
		return false
	}
	pt, _ := usage["prompt_tokens"].(float64)
	ct, _ := usage["completion_tokens"].(float64)
	return int(pt) > 0 || int(ct) > 0
}

func buildDiagnostics(state *kiroSSEState) *IntegrityDiagnostics {
	responseState := "no_semantic_output"
	switch {
	case state.hasToolCalls:
		responseState = "valid_tool"
	case state.hasText || state.hasReasoning:
		responseState = "text_reasoning"
	case state.explicitStop:
		responseState = "explicit_stop"
	}
	return &IntegrityDiagnostics{
		TransportState:       state.transportState,
		StopReason:           state.stopReason,
		StopDisposition:      stopDisposition(state.stopReason, len(state.tools) > 0),
		ResponseState:        responseState,
		EventCounts:          copyEventCounts(state.eventCounts),
		IncompleteFrameBytes: 0,
	}
}

func copyEventCounts(src map[string]int) map[string]int {
	dst := make(map[string]int, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
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

func handleKiroToolUseEvent(payload map[string]any, state *kiroSSEState, maxToolBytes int) error {
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
			state.bufferedToolBytes += len(toolID) + len(name) + 32
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
			state.bufferedToolBytes += len(iv)
		case map[string]any:
			b, _ := json.Marshal(iv)
			state.bufferedToolBytes -= len(buf.canonical)
			buf.canonical = string(b)
			buf.isObjectForm = true
			state.bufferedToolBytes += len(buf.canonical)
		}

		if state.bufferedToolBytes > maxToolBytes {
			return fmt.Errorf("Kiro buffered tool input exceeded the integrity memory bound (%d bytes)", maxToolBytes)
		}
	}
	return nil
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
