package kiro

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// VansRouter ref: open-sse/executors/kiro.js — integrity validation pipeline
//   normalizeStopReason     → kiro.js normalizeStopReason
//   mergeStopReason         → kiro.js mergeStopReason
//   stopDisposition         → kiro.js stopDisposition
//   refusalMatch            → kiro.js refusalMatch
//   isEllipsisOnly          → kiro.js isEllipsisOnly
//   isShortFutureAction     → kiro.js isShortFutureAction
//   inspectSSEChunk         → kiro.js inspectSSEChunk
//   validateIntegrity       → kiro.js validateIntegrity
//   appendRepairInstruction → kiro.js addRepairInstruction
//   RunIntegrityCheck       → kiro.js runIntegrityCheck
//
// AIClient2API ref: claude-kiro.js — short future action detection patterns

var wsHyphenRe = regexp.MustCompile(`[\s-]+`)

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
	return normalizeStopReasonString(raw)
}

func normalizeStopReasonString(value string) string {
	if value == "" {
		return ""
	}
	// Insert underscore between lowercase→uppercase transitions only (like VansRouter)
	result := ""
	for i, r := range value {
		if i > 0 {
			prev := rune(value[i-1])
			if prev >= 'a' && prev <= 'z' && r >= 'A' && r <= 'Z' {
				result += "_"
			}
		}
		result += string(r)
	}
	result = strings.ToLower(result)
	// Collapse consecutive whitespace/hyphens into single underscore (like VansRouter)
	result = wsHyphenRe.ReplaceAllString(result, "_")
	switch result {
	case "endturn", "end_turn", "stop", "stop_sequence":
		return "end_turn"
	case "tooluse", "tool_use", "tool_calls":
		return "tool_use"
	case "maxtokens", "max_tokens", "max_output_tokens", "length":
		return "max_tokens"
	}
	return result
}

// VansRouter ref: kiro.js mergeStopReason — severity-based merging
func mergeStopReason(current, incoming string) string {
	if incoming == "" {
		return current
	}
	if current == "" {
		return incoming
	}
	severity := func(r string) int {
		switch r {
		case "end_turn", "tool_use":
			return 1
		case "max_tokens":
			return 2
		case "malformed_model_output", "invalid_model_output":
			return 3
		case "cancelled", "pause_turn", "model_context_window_exceeded":
			return 5
		case "refusal":
			return 6
		}
		return 4
	}
	if severity(incoming) > severity(current) {
		return incoming
	}
	return current
}

// VansRouter ref: kiro.js refusalMatch
func refusalMatch(stopReason string) bool {
	if stopReason == "refusal" {
		return true
	}
	lower := strings.ToLower(stopReason)
	return strings.Contains(lower, "content") && strings.Contains(lower, "filter") ||
		strings.Contains(lower, "guardrail") ||
		strings.Contains(lower, "safety") ||
		strings.Contains(lower, "policy") ||
		strings.Contains(lower, "blocked")
}

// VansRouter ref: kiro.js stopDisposition
func stopDisposition(stopReason string, hasToolCalls bool) StopDisposition {
	switch stopReason {
	case "malformed_model_output", "invalid_model_output":
		return StopRetryableProtocolFail
	case "cancelled", "pause_turn", "model_context_window_exceeded":
		return StopTerminalIncomplete
	case "refusal":
		return StopTerminalRefusal
	case "max_tokens":
		if hasToolCalls {
			return StopTerminalIncomplete
		}
		return StopLength
	}
	if refusalMatch(stopReason) {
		return StopTerminalRefusal
	}
	if stopReason != "" && stopReason != "end_turn" && stopReason != "tool_use" {
		return StopUnknownFailure
	}
	if hasToolCalls || stopReason == "tool_use" {
		return StopToolUse
	}
	if stopReason == "" || stopReason == "end_turn" {
		return StopComplete
	}
	return StopUnknownFailure
}

// VansRouter ref: kiro.js isEllipsisOnly
func isEllipsisOnly(value string) bool {
	v := strings.TrimSpace(value)
	return v == "..." || v == "…"
}

type shortFutureActionState struct {
	content       string
	hasToolCalls  bool
	error         string
}

// VansRouter ref: kiro.js inspectSSEChunk
func inspectSSEChunk(data []byte, state *shortFutureActionState) {
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "data: ") {
			continue
		}
		content := strings.TrimSpace(trimmed[5:])
		if content == "" || content == "[DONE]" {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(content), &event); err != nil {
			continue
		}
		if e, ok := event["error"]; ok {
			if m, ok := e.(map[string]any); ok {
				if msg, ok := m["message"].(string); ok {
					state.error = msg
				}
			}
		}
		choices, _ := event["choices"].([]any)
		if len(choices) == 0 {
			continue
		}
		choice, _ := choices[0].(map[string]any)
		delta, _ := choice["delta"].(map[string]any)
		if delta == nil {
			continue
		}
		if c, ok := delta["content"].(string); ok {
			state.content += c
		}
		if r, ok := delta["reasoning_content"].(string); ok {
			state.content += r
		}
		if tc, ok := delta["tool_calls"].([]any); ok && len(tc) > 0 {
			state.hasToolCalls = true
		}
	}
}

// ── Short future action detection patterns (VansRouter kiro.js) ────────────
// Note: Go's regexp (RE2) does not support \b word boundary, so patterns
// without \b are used. This is fine for SSE content matching.

// KIRO_SHORT_FINAL_MAX_CHARS is the max length for short future action detection.
// VansRouter ref: kiro.js KIRO_SHORT_FINAL_MAX_CHARS
const KIRO_SHORT_FINAL_MAX_CHARS = 800

var (
	// OBSERVED_TRAILING_FUTURE_ACTION — specific observed whole-response signature.
	// VansRouter: kiro.js OBSERVED_TRAILING_FUTURE_ACTION
	observedTrailingFutureActionRe = regexp.MustCompile(`(?i)^目前證據顯示[\s\S]{1,700}[。.!?；;]\s*最後補查\s+504\s+access\s+log[，,]\s*確認\s+host[／/]路徑與是否為集中流量[。.!]?$`)

	// ENGLISH_RESULT_CLAUSE — positive evidence that a result was provided.
	// VansRouter: kiro.js ENGLISH_RESULT_CLAUSE
	englishResultClauseRe = regexp.MustCompile(`(?i)(?:[:;\n]|[.!?]\s+\S|(?:status|checksum|response|deployment)\s+(?:is|are|was|were|matches?|equals?|returned))`)

	// CHINESE_RESULT_CLAUSE — positive evidence that a Chinese result was provided.
	// VansRouter: kiro.js CHINESE_RESULT_CLAUSE
	chineseResultClauseRe = regexp.MustCompile(`[。！？]\s*\S|(?:版本|狀態|回應|結果|部署|校驗碼)(?:是|為|等於|顯示)`)

	// ENGLISH_FUTURE_ACTION — match English sentence that starts with future action.
	// VansRouter: kiro.js ENGLISH_FUTURE_ACTION
	englishFutureActionRe = regexp.MustCompile(`(?i)^(?:(?:next|now|then)[\s,:-]*)?(?:i(?:'ll| will| am going to| need to)|let me)\s+(?:verify|check|confirm|validate|investigate|trace|continue|follow up|test)`)

	// CHINESE_FUTURE_ACTION — match Chinese sentence that starts with future action.
	// VansRouter: kiro.js CHINESE_FUTURE_ACTION
	chineseFutureActionRe = regexp.MustCompile(`^(?:(?:現在|接著|接下來|下一步)[，,:：\s]*(?:我(?:只)?(?:會|要|將|再)?\s*)?|我只再|我(?:會|要|將)(?:再|重新)?)(?:補|抓取|查|確認|驗證|追|繼續|檢查|測試)`)

	// SHORT_FUTURE_ACTION — combined regex for English+Chinese premature action announcements.
	// VansRouter: kiro.js SHORT_FUTURE_ACTION (/iu flags → (?i) in Go)
	shortFutureActionRe = regexp.MustCompile(`(?i)^(?:(?:(?:現在|接著|接下來|下一步)[，,:：\s]*(?:我(?:只)?(?:會|要|將|再)?\s*)?|我只再)(?:補|查|確認|驗證|追(?:查|蹤)?|繼續|檢查|測試)|我(?:會|要|將)(?:再|重新)?(?:補(?:齊|查)?|抓取|查(?:詢)?|確認|驗證|追(?:查|蹤)?|繼續|檢查|測試)|(?:(?:next|now|then)[\s,:-]*)?(?:i(?:'ll| will| am going to| need to)|let me)\s+(?:verify|check|confirm|validate|investigate|trace|continue|follow up|test))`)

	// USER_WAIT — patterns that indicate waiting for user input (not a future action).
	// VansRouter: kiro.js USER_WAIT
	userWaitRe = regexp.MustCompile(`(?i)(?:請(?:你|先)|你(?:先|需要|可以|提供|確認|批准|允許)|等待(?:你|使用者)|等你|核准|同意|授權|(?:after|when|once)\s+you|your\s+(?:approval|confirmation|permission|input)|wait(?:ing)?\s+for\s+you|please\s+(?:approve|confirm|provide|send))`)

	// COMPLETED_FINAL — patterns that indicate completion (not a future action).
	// VansRouter: kiro.js COMPLETED_FINAL
	completedFinalRe = regexp.MustCompile(`(?i)(?:已(?:經)?完成|完成(?:了|驗證|確認)|修復完成|確認無誤|驗證(?:完成|通過)|測試(?:均)?通過|結論|總結|(?:done|completed|fixed|verified|confirmed|passed|in conclusion|summary)|(?:is|are) complete)`)

	// RESULT_EVIDENCE — patterns that indicate evidence/result was provided.
	// VansRouter: kiro.js RESULT_EVIDENCE
	resultEvidenceRe = regexp.MustCompile(`(?i)(?:顯示|發現|因此|成功|失敗|正常|無錯誤|沒有錯誤|(?:found|shows?|showed|because|therefore|succeeded|failed|healthy|green|no errors?))`)
)

// VansRouter ref: kiro.js isShortFutureAction — regex-based detection of premature action announcements
func isShortFutureAction(value string) bool {
	text := strings.TrimSpace(value)
	if text == "" {
		return false
	}

	// Step 1: Check observed trailing future action pattern
	if observedTrailingFutureActionRe.MatchString(text) {
		return true
	}

	// Step 2: If English future action has result clause → not a future action
	if englishFutureActionRe.MatchString(text) && englishResultClauseRe.MatchString(text) {
		return false
	}

	// Step 3: If Chinese future action has result clause → not a future action
	if chineseFutureActionRe.MatchString(text) && chineseResultClauseRe.MatchString(text) {
		return false
	}

	// Step 4: General short future action check with exclusion patterns
	return len(text) > 0 && len(text) <= KIRO_SHORT_FINAL_MAX_CHARS &&
		shortFutureActionRe.MatchString(text) &&
		!userWaitRe.MatchString(text) &&
		!completedFinalRe.MatchString(text) &&
		!resultEvidenceRe.MatchString(text)
}

// VansRouter ref: kiro.js validateIntegrity — full SSE buffer + content validation
func validateIntegrity(reader io.Reader, maxBytes int, eventstreamDiag *IntegrityDiagnostics) *IntegrityResult {
	var buf bytes.Buffer
	limited := io.LimitReader(reader, int64(maxBytes)+1)

	scanner := bufio.NewScanner(limited)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	inspector := &shortFutureActionState{}
	diagnostics := eventstreamDiag

	for scanner.Scan() {
		line := scanner.Text()
		buf.Write([]byte(line))
		buf.Write([]byte("\n"))
		inspectSSEChunk([]byte(line+"\n"), inspector)
	}

	totalBytes := buf.Len()
	if totalBytes > maxBytes {
		return &IntegrityResult{
			Kind:    IntegrityTerminalStop,
			Message: fmt.Sprintf("integrity buffer exceeded %d bytes", maxBytes),
			Diagnostics: &IntegrityDiagnostics{
				TerminalProvenance:   "integrity_buffer_exceeded",
				TransportState:       "buffer_exceeded",
				StopDisposition:      StopTerminalIncomplete,
				IncompleteFrameBytes: totalBytes,
			},
		}
	}

	if diagnostics != nil && diagnostics.StopDisposition == StopRetryableProtocolFail {
		return &IntegrityResult{
			Kind:        IntegrityInvalidTool,
			Message:     diagnostics.StopReason,
			Diagnostics: diagnostics,
		}
	}

	if diagnostics != nil && (diagnostics.StopDisposition == StopTerminalIncomplete ||
		diagnostics.StopDisposition == StopTerminalRefusal ||
		diagnostics.StopDisposition == StopUnknownFailure) {
		return &IntegrityResult{
			Kind:        IntegrityTerminalStop,
			Message:     diagnostics.StopReason,
			Diagnostics: diagnostics,
		}
	}

	if inspector.error != "" {
		return &IntegrityResult{
			Kind:        IntegrityMissingTerminal,
			Message:     inspector.error,
			Diagnostics: diagnostics,
		}
	}

	if !inspector.hasToolCalls {
		if isEllipsisOnly(inspector.content) {
			return &IntegrityResult{
				Kind:    IntegrityEllipsis,
				Diagnostics: &IntegrityDiagnostics{
					TerminalProvenance: "integrity_validation",
					TransportState:     "valid_complete_frame",
					StopDisposition:    StopComplete,
				},
			}
		}
		if isShortFutureAction(inspector.content) {
			return &IntegrityResult{
				Kind:    IntegrityShortFinal,
				Diagnostics: &IntegrityDiagnostics{
					TerminalProvenance: "integrity_validation",
					TransportState:     "valid_complete_frame",
					StopDisposition:    StopComplete,
				},
			}
		}
	}

	return &IntegrityResult{
		Kind:  IntegrityComplete,
		Bytes: buf.Bytes(),
		Diagnostics: &IntegrityDiagnostics{
			TerminalProvenance: "clean_eventstream_eof",
			TransportState:     "clean_eof",
			StopDisposition:    StopComplete,
		},
	}
}

// VansRouter ref: kiro.js addRepairInstruction
func appendRepairInstruction(body []byte, kind string) []byte {
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return body
	}

	instruction := RepairTool
	switch kind {
	case IntegrityEllipsis:
		instruction = RepairEllipsis
	case IntegrityShortFinal:
		instruction = RepairShortFinal
	}

	existingSP, _ := raw["systemPrompt"].(string)
	if existingSP != "" {
		raw["systemPrompt"] = existingSP + "\n\n" + instruction
	} else {
		raw["systemPrompt"] = instruction
	}
	b, _ := json.Marshal(raw)
	return b
}

// VansRouter ref: kiro.js encodeSSEError — error SSE emission
func createSSEErrorBytes(code, message string) []byte {
	errorPayload := map[string]any{
		"error": map[string]any{
			"message": message,
			"type":    "upstream_error",
			"code":    code,
		},
	}
	b, _ := json.Marshal(errorPayload)
	return []byte(fmt.Sprintf("data: %s\n\ndata: [DONE]\n\n", b))
}

// VansRouter ref: kiro.js encodeSSEErrorWithDiagnostics
func encodeSSEErrorWithDiagnostics(code, message string, diag *IntegrityDiagnostics) []byte {
	payload := map[string]any{
		"error": map[string]any{
			"message":     message,
			"type":        "upstream_error",
			"code":        code,
		},
	}
	if diag != nil {
		diagnosticsMap := map[string]any{
			"terminal_provenance":    diag.TerminalProvenance,
			"transport_state":        diag.TransportState,
			"stop_reason":            diag.StopReason,
			"stop_disposition":       string(diag.StopDisposition),
			"response_state":         diag.ResponseState,
			"incomplete_frame_bytes": diag.IncompleteFrameBytes,
		}
		if diag.EventCounts != nil {
			diagnosticsMap["event_counts"] = diag.EventCounts
		}
		payload["diagnostics"] = diagnosticsMap
	}
	b, _ := json.Marshal(payload)
	return []byte(fmt.Sprintf("data: %s\n\ndata: [DONE]\n\n", b))
}

// VansRouter ref: kiro.js integrityFailureSSE
func integrityFailureSSE(result *IntegrityResult) []byte {
	if result == nil || result.Diagnostics == nil {
		return createSSEErrorBytes("kiro_integrity_failed", "Kiro integrity validation failed")
	}
	d := result.Diagnostics
	code := "kiro_unknown_stop_reason"
	switch {
	case d.TerminalProvenance == "integrity_buffer_exceeded":
		code = "kiro_integrity_buffer_exceeded"
	case result.Kind == IntegrityUpstreamError:
		code = "kiro_upstream_eventstream_error"
	case d.StopDisposition == StopTerminalRefusal:
		code = "kiro_terminal_refusal"
	case d.StopDisposition == StopTerminalIncomplete:
		code = "kiro_terminal_incomplete"
	}
	return encodeSSEErrorWithDiagnostics(code, result.Message, d)
}

// VansRouter ref: kiro.js runIntegrityCheck
func RunIntegrityCheck(respBody io.Reader, model string, opts IntegrityOptions) *IntegrityResult {
	maxBytes := opts.MaxBytes
	if maxBytes <= 0 {
		maxBytes = KIRO_TOOL_CALL_REPAIR_BUFFER_MAX_BYTES
	}

	buf := &bytes.Buffer{}
	tee := io.TeeReader(respBody, buf)

	result := validateIntegrity(tee, maxBytes, nil)
	return result
}

type IntegrityOptions struct {
	MaxBytes      int
	RepairEnabled bool
	Signal        <-chan struct{}
}
