package kiro

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"strings"
	"time"
)

// VansRouter ref: open-sse/executors/kiro.js — createSSEStream (SSE transform pipeline)
//   transformKiroToSSE        → kiro.js makeKiroWithTransform / createSSEStream
//   kiroThinkingState          → kiro.js processThinkingTag state
//   kiroSSEState               → kiro.js SSEState / diagnostics accumulator
//   kiroToolBuffer             → kiro.js toolBuffer
//   kiroToolArgBuffer          → kiro.js toolArgBuffer + fragment type tracking
//   TransformOptions           → kiro.js TransformOptions / callback for terminal diagnostics
//   handleKiroToolUseEvent     → kiro.js handleToolUseEvent
//   handleKiroReasoningEvent   → kiro.js handleReasoningEvent
//   handleKiroMetricsEvent     → kiro.js handleMetricsEvent
//   handleKiroMeteringEvent    → kiro.js handleMeteringEvent
//   handleKiroContextUsageEvent→ kiro.js handleContextUsageEvent
//   flushKiroBufferedToolArgs  → kiro.js flushBufferedToolArgs
//   buildKiroUsage             → kiro.js buildUsage
//   splitInlineThinking        → kiro.js processThinkingTag
//   convertEscapedNewlines     → kiro.js escaped-newline conversion (AIClient2API)
//
// AIClient2API ref: claude-kiro.js — SSE delta emission + content dedup + tool buffer

type kiroThinkingState struct {
	thinkingMode bool
	pendingTag   string
}

const partialTagMaxLen = 11

// KIRO_PLACEHOLDER_TOOL_NAME is the placeholder tool name for empty tools (AIClient2API style)
const KIRO_PLACEHOLDER_TOOL_NAME = "no_tool_available"

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
	usage             *UsageSummary
	responseID        string
	created           int64
	model             string
	thinkingState     kiroThinkingState
	thinkingEnabled   bool
	totalContentLength   int
	contextUsagePct     float64
	hasContextUsage     bool
	lastContentEvent    string   // for content dedup (AIClient2API style)
	hasYieldedContent   bool     // has any content/reasoning/toolUse been emitted
	toolNameMap       map[string]string // reverse map: truncated → original

	eventCounts           map[string]int
	transportState        string
	terminalProvenance    string
	bufferedToolBytes     int
	validatedFrameCount   int         // frames validated (VansRouter keepalive tracking)
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
	inputKind    string  // "string" or "object" (VansRouter fragment type tracking)
}

type TransformOptions struct {
	OnTerminalState func(*IntegrityDiagnostics)
	MaxToolBytes    int
	ToolNameMap     map[string]string // reverse map: truncated → original name
}

// VansRouter ref: kiro.js makeKiroWithTransform / createSSEStream — full SSE transform pipeline
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
	if opts != nil && opts.ToolNameMap != nil {
		state.toolNameMap = opts.ToolNameMap
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

	emitFinish := func(finishReason string, usage *UsageSummary) {
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
			// Content dedup: skip consecutive identical content (AIClient2API style, claude-kiro.js L2465-2470)
			if s == state.lastContentEvent {
				return
			}
			state.lastContentEvent = s

			// Escaped newline conversion: \\n → \n (AIClient2API style, claude-kiro.js L2703)
			decoded := convertEscapedNewlines(s)
			state.hasText = true
			state.hasYieldedContent = true
			state.totalContentLength += len(decoded)
			emitDelta(map[string]any{"content": decoded})
		}
	}

	onReasoning := func(s string) {
		if s != "" {
			state.hasReasoning = true
			state.hasYieldedContent = true
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
		state.validatedFrameCount++

		// SSE keepalive: emit once on first validated frame if no SSE chunks yet
		// (VansRouter kiro.js pattern: ": kiro-upstream\n\n")
		if state.validatedFrameCount == 1 && state.chunkIndex == 0 {
			fmt.Fprintf(w, ": kiro-upstream\n\n")
		}

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

	// Empty response detection: no content, no thinking, no tool calls (AIClient2API style, claude-kiro.js L3116-3118)
	if !state.hasYieldedContent {
		return fmt.Errorf("kiro empty response: no content/reasoning/toolUse events received from upstream")
	}

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
		if state.usage == nil {
			state.usage = &UsageSummary{}
		}
		state.usage.PromptTokens = prompt
		state.usage.CompletionTokens = completion
		state.usage.TotalTokens = prompt + completion
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
	return s.usage.KiroCredits > 0
}

func hasUsageTokens(usage *UsageSummary) bool {
	if usage == nil {
		return false
	}
	return usage.PromptTokens > 0 || usage.CompletionTokens > 0
}

// VansRouter ref: kiro.js buildDiagnostics
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

// VansRouter ref: kiro.js handleReasoningEvent
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

// VansRouter ref: kiro.js handleToolUseEvent + fragment type validation
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
		// Reverse-map tool name jika ada NameMap (AIClient2API style)
		if state.toolNameMap != nil {
			if original, ok := state.toolNameMap[name]; ok {
				name = original
			}
		}

		// Filter out placeholder tool (AIClient2API style, claude-kiro.js L1731-1736, L2895-2901)
		if name == KIRO_PLACEHOLDER_TOOL_NAME {
			continue
		}

		// VansRouter: validate toolUseId — if present, must be non-empty string
		toolIDRaw, hasToolID := value["toolUseId"]
		var toolID string
		if hasToolID {
			s, ok := toolIDRaw.(string)
			if !ok || strings.TrimSpace(s) == "" {
				return fmt.Errorf("kiro toolUseEvent has an invalid toolUseId: %v", toolIDRaw)
			}
			toolID = s
		} else {
			toolID = fmt.Sprintf("call_%d_%d", state.created, len(state.tools)+1)
		}

		if _, exists := state.tools[toolID]; !exists {
			state.tools[toolID] = &kiroToolBuffer{id: toolID, name: name}
			state.bufferedToolBytes += len(toolID) + len(name) + 32
		} else if state.tools[toolID].name != name {
			return fmt.Errorf("kiro tool name changed between fragments: was %q, got %q", state.tools[toolID].name, name)
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
			// Fragment type validation (VansRouter pattern): string chunks must stay string
			if buf.inputKind != "" && buf.inputKind != "string" {
				return fmt.Errorf("kiro tool input changed fragment type: was %s, got string", buf.inputKind)
			}
			buf.inputKind = "string"
			buf.stringParts = append(buf.stringParts, iv)
			buf.isObjectForm = false
			state.bufferedToolBytes += len(iv)
		case map[string]any:
			// Fragment type validation (VansRouter pattern): object chunks must stay object
			if buf.inputKind != "" && buf.inputKind != "object" {
				return fmt.Errorf("kiro tool input changed fragment type: was %s, got object", buf.inputKind)
			}
			buf.inputKind = "object"
			b, _ := json.Marshal(iv)
			state.bufferedToolBytes -= len(buf.canonical)
			buf.canonical = string(b)
			buf.isObjectForm = true
			state.bufferedToolBytes += len(buf.canonical)
		default:
			return fmt.Errorf("kiro tool input must be a string or JSON object, got %T", input)
		}

		if state.bufferedToolBytes > maxToolBytes {
			return fmt.Errorf("kiro buffered tool input exceeded the integrity memory bound (%d bytes)", maxToolBytes)
		}
	}
	return nil
}

// VansRouter ref: kiro.js handleMetricsEvent
func handleKiroMetricsEvent(payload map[string]any, state *kiroSSEState) {
	metrics := payload
	if m, ok := payload["metricsEvent"].(map[string]any); ok {
		metrics = m
	}
	prompt := toInt(metrics["inputTokens"])
	completion := toInt(metrics["outputTokens"])
	if prompt > 0 || completion > 0 {
		if state.usage == nil {
			state.usage = &UsageSummary{}
		}
		state.usage.PromptTokens = prompt
		state.usage.CompletionTokens = completion
		state.usage.TotalTokens = prompt + completion

		cacheRead := toInt(metrics["cacheReadInputTokens"])
		if cacheRead == 0 {
			cacheRead = toInt(metrics["cache_read_input_tokens"])
		}
		if cacheRead > 0 {
			state.usage.CacheReadInputTokens = cacheRead
		}
		cacheCreate := toInt(metrics["cacheCreationInputTokens"])
		if cacheCreate == 0 {
			cacheCreate = toInt(metrics["cache_creation_input_tokens"])
		}
		if cacheCreate > 0 {
			state.usage.CacheCreationInputTokens = cacheCreate
		}
	}
}

// VansRouter ref: kiro.js handleMeteringEvent
func handleKiroMeteringEvent(payload map[string]any, state *kiroSSEState) {
	metering := payload
	if m, ok := payload["meteringEvent"].(map[string]any); ok {
		metering = m
	}
	credits := toInt(metering["usage"])
	if credits > 0 {
		if state.usage == nil {
			state.usage = &UsageSummary{}
		}
		state.usage.KiroCredits = credits
		unit := "credit"
		if u, ok := metering["unit"].(string); ok && u != "" {
			unit = u
		}
		state.usage.KiroCreditUnit = unit
	}
}

// VansRouter ref: kiro.js handleContextUsageEvent
func handleKiroContextUsageEvent(payload map[string]any, state *kiroSSEState) {
	if pct, ok := payload["contextUsagePercentage"].(float64); ok {
		state.contextUsagePct = pct
		state.hasContextUsage = true
	}
}

// VansRouter ref: kiro.js flushBufferedToolArgs
func flushKiroBufferedToolArgs(state *kiroSSEState, emitDelta func(map[string]any)) {
	toolIndex := 0
	for toolID, tool := range state.tools {
		// Skip placeholder tools (AIClient2API style)
		if tool.name == KIRO_PLACEHOLDER_TOOL_NAME {
			delete(state.tools, toolID)
			continue
		}
		state.hasToolCalls = true
		state.hasYieldedContent = true

		buf, hasBuf := state.toolArgsBuffer[toolID]
		var arguments string
		if hasBuf {
			if buf.isObjectForm {
				arguments = buf.canonical
			} else {
				arguments = strings.Join(buf.stringParts, "")
			}
		}

		// VansRouter: validate tool_call wrapper (MCP tool nesting)
		// If the tool is named "tool_call", its input must be valid JSON with
		// a non-empty `name` and an `arguments` field.
		// Silently skip + remove malformed tool_call so valid tools still emit
		// and finish_reason isn't polluted by stale tool count.
		if tool.name == "tool_call" && arguments != "" {
			var inputMap map[string]any
			if err := json.Unmarshal([]byte(arguments), &inputMap); err != nil {
				delete(state.tools, toolID)
				continue
			}
			nameVal, _ := inputMap["name"].(string)
			if strings.TrimSpace(nameVal) == "" {
				delete(state.tools, toolID)
				continue
			}
			if _, hasArgs := inputMap["arguments"]; !hasArgs {
				delete(state.tools, toolID)
				continue
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

// VansRouter ref: kiro.js buildUsage
func buildKiroUsage(state *kiroSSEState) *UsageSummary {
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
	return &UsageSummary{
		PromptTokens:     prompt,
		CompletionTokens: completion,
		TotalTokens:      prompt + completion,
	}
}

// VansRouter ref: kiro.js processThinkingTag — inline thinking split
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

// convertEscapedNewlines converts \\n to \n while preserving \\\n (AIClient2API style claude-kiro.js L2703).
// JS: text.replace(/(?<!\\)\\n/g, '\n') — converts literal backslash-n to newline,
// but NOT when preceded by another backslash (escaped backslash).
func convertEscapedNewlines(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			if s[i+1] == '\\' {
				// Escaped backslash: \\ → \, skip both
				b.WriteByte('\\')
				i++
				continue
			}
			if s[i+1] == 'n' {
				// Backslash-n → actual newline
				b.WriteByte('\n')
				i++
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// VansRouter ref: kiro.js flushPendingThinking
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
