package kiro

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"farouter/internal/rtk"
)

// VansRouter ref: open-sse/executors/kiro.js — main execution pipeline
//
//   RetryConfig            → kiro.js DEFAULT_RETRY_CONFIG
//   defaultRetryConfig     → kiro.js DEFAULT_RETRY_CONFIG
//   resolveRetryEntry      → kiro.js resolveRetryEntry
//   SetKiroThrottleMs      → kiro.js setKiroThrottle
//   acquireThrottle        → kiro.js acquireKiroRequestSlot
//   Execute                → kiro.js execute (streaming path)
//   pipeKiroResponse       → kiro.js createPipeline (EventStream→SSE→passthrough)
//   repairEnabled          → kiro.js repairEnabled
//   ExecuteWithIntegrityCheck → kiro.js executeWithIntegrityCheck (full integrity gate)
//   streamSSEBytes          → kiro.js streamSSEBytes (pre-buffered replay)
//   writeIntegritySSE       → kiro.js writeIntegritySSE
//   doIntegrityAttemptWithFallback → kiro.js doIntegrityAttemptWithFallback
//   processIntegrityResponse → kiro.js processIntegrityResponse
//   sendToKiroEndpoint      → kiro.js sendToKiroEndpoint (with FETCH_CONNECT_TIMEOUT_MS)
//
// AIClient2API ref: claude-kiro.js — auth + profileArn resolution + RTK compression

// envPositiveInt reads an env var and returns the parsed positive int,
// or fallback if unset / zero / invalid. VansRouter pattern for configurable timeouts.
// VansRouter ref: kiro.js envPositiveInt — used for KIRO_TOOL_CALL_REPAIR_BUFFER_MAX_BYTES,
// KIRO_TOOL_CALL_REPAIR_TTFT_TIMEOUT_MS, KIRO_TOOL_CALL_REPAIR_STALL_TIMEOUT_MS
func envPositiveInt(name string, fallback int) int {
	v := os.Getenv(name)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

// RetryConfig mirrors vansrouter's DEFAULT_RETRY_CONFIG pattern.
// Per-status-code retry: {attempts, delayMs}. Zero attempts = no retry (fallback only).
type RetryConfigEntry struct {
	Attempts int
	DelayMs  int
}

var defaultRetryConfig = map[int]RetryConfigEntry{
	429: {Attempts: 0, DelayMs: 0},       // no per-URL retry, just fallback to next URL
	502: {Attempts: 3, DelayMs: 3000},     // retry same URL up to 3x with 3s delay
	503: {Attempts: 3, DelayMs: 2000},     // retry same URL up to 3x with 2s delay
	504: {Attempts: 2, DelayMs: 3000},     // retry same URL up to 2x with 3s delay
}

func resolveRetryEntry(entry RetryConfigEntry) (attempts int, delayMs int) {
	if entry.Attempts <= 0 {
		return 0, 2000
	}
	if entry.DelayMs <= 0 {
		return entry.Attempts, 2000
	}
	return entry.Attempts, entry.DelayMs
}

type TokenUsageCallback func(inputTokens, outputTokens int64)

var GlobalTokenCallback TokenUsageCallback

// ── Request throttle (AIClient2API acquireKiroRequestSlot) ────────────────
// Prevents too-rapid requests to Kiro upstream. Default 0ms = no throttle.
// Set KIRO_THROTTLE_MS env var to enable (e.g. 1000 for 1s between requests).
var (
	throttleMu       sync.Mutex
	lastRequestStart time.Time
	kiroThrottleMs   int
)

// SetKiroThrottleMs sets the minimum interval between Kiro requests.
// Called from main.go after loading config.json.
func SetKiroThrottleMs(ms int) {
	if ms > 0 {
		kiroThrottleMs = ms
	}
}

func init() {
	if v := os.Getenv("KIRO_THROTTLE_MS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			kiroThrottleMs = n
		}
	}
}

// acquireThrottle blocks until minIntervalMs has elapsed since last request.
func acquireThrottle(minIntervalMs int) {
	if minIntervalMs <= 0 {
		return
	}
	throttleMu.Lock()
	elapsed := time.Since(lastRequestStart)
	wait := time.Duration(minIntervalMs)*time.Millisecond - elapsed
	if wait > 0 {
		throttleMu.Unlock()
		time.Sleep(wait)
		throttleMu.Lock()
	}
	lastRequestStart = time.Now()
	throttleMu.Unlock()
}

// Execute runs the streaming Kiro pipeline (no integrity validation).
// VansRouter ref: kiro.js execute — streaming path
func Execute(ctx context.Context, creds Credentials, req ChatRequest, w http.ResponseWriter, conversationID, connectionID string, rtkEnabled bool) error {
	acquireThrottle(0) // default 0ms; set >0 via config for rate limiting

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

	if profileArn == "" && authMethod != "api_key" && authMethod != "" {
		discoverCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		arn, _ := DiscoverProfileArn(discoverCtx, creds.AccessToken, creds.PSD.Region)
		cancel()
		if arn != "" {
			profileArn = arn
		}
	}

	kiroReq, err := buildKiroRequest(req, resolved, profileArn, conversationID, connectionID)
	if err != nil {
		return err
	}

	kiroBody := kiroReq.Body

	if rtkEnabled {
		compressed, stats := rtk.CompressKiroBody(kiroBody)
		kiroBody = compressed
		if line := rtk.FormatRtkLog(stats); line != "" {
			log.Print(line)
		}
	}

	region := ResolveRuntimeRegion(creds.PSD.Region, profileArn)
	urls := GetOrderedBaseURLs(creds, region)

	var lastErr error
	var lastStatus int
	retryAttemptsByUrl := make(map[int]int)
	for urlIndex := 0; urlIndex < len(urls); urlIndex++ {
		url := urls[urlIndex]

		resp, err := sendToKiroEndpoint(ctx, creds, url, kiroBody)
		if err != nil {
			lastErr = err
			// Network error → retry same URL (matches vansrouter 502 retry pattern)
			entry := defaultRetryConfig[502]
			attempts, delayMs := resolveRetryEntry(entry)
			if attempts > 0 && retryAttemptsByUrl[urlIndex] < attempts {
				retryAttemptsByUrl[urlIndex]++
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(time.Duration(delayMs) * time.Millisecond):
				}
				urlIndex--
				continue
			}
			if urlIndex+1 < len(urls) {
				continue
			}
			return fmt.Errorf("kiro: all %d endpoints failed: %w", len(urls), lastErr)
		}

		// Per-status retry (matches vansrouter tryRetry pattern)
		if entry, ok := defaultRetryConfig[resp.StatusCode]; ok {
			attempts, delayMs := resolveRetryEntry(entry)
			if attempts > 0 && retryAttemptsByUrl[urlIndex] < attempts {
				resp.Body.Close()
				retryAttemptsByUrl[urlIndex]++
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(time.Duration(delayMs) * time.Millisecond):
				}
				urlIndex--
				continue
			}
		}

		if resp.StatusCode == http.StatusTooManyRequests {
			resp.Body.Close()
			lastErr = fmt.Errorf("429 from %s", url)
			lastStatus = resp.StatusCode
			if urlIndex+1 < len(urls) {
				continue
			}
			return fmt.Errorf("kiro: all %d endpoints returned 429", len(urls))
		}

		if resp.StatusCode != http.StatusOK {
			errBody := readResponsePrefix(resp.Body, 4096)
			resp.Body.Close()
			if resp.StatusCode == http.StatusPaymentRequired {
				return ErrExhausted
			}
			if resp.StatusCode == http.StatusForbidden {
				var e struct {
					Reason  string `json:"reason"`
					Message string `json:"message"`
				}
				json.Unmarshal([]byte(errBody), &e)
				if e.Reason == "TEMPORARILY_SUSPENDED" || strings.Contains(strings.ToLower(e.Message), "suspended") {
					return ErrSuspended
				}
			}
			lastErr = fmt.Errorf("kiro upstream: %s — %s", resp.Status, errBody)
			lastStatus = resp.StatusCode
			if urlIndex+1 < len(urls) {
				continue
			}
			return lastErr
		}

		defer resp.Body.Close()
		return pipeKiroResponse(ctx, resp, w, resolved, kiroReq.NameMap)
	}

	if lastErr != nil {
		return lastErr
	}
	return fmt.Errorf("kiro: all %d endpoints failed with status %d", len(urls), lastStatus)
}

// VansRouter ref: kiro.js createPipeline — EventStream→SSE→passthrough pipeline
func pipeKiroResponse(ctx context.Context, resp *http.Response, w http.ResponseWriter, resolved ResolvedModel, toolNameMap map[string]string) error {
	// Pipeline: Kiro EventStream → SSE → passthrough transform → client
	sc := NewStreamController(ctx)

	// Stage 1: Kiro EventStream → raw SSE via pipe
	kiroPR, kiroPW := io.Pipe()
	go func() {
		defer kiroPW.Close()
		opts := &TransformOptions{ToolNameMap: toolNameMap}
		err := transformKiroToSSE(resp.Body, resolved.Upstream, resolved.Thinking, kiroPW, opts)
		if err != nil {
			writeStreamError(kiroPW, 502, err.Error())
			fmt.Fprintf(kiroPW, "data: [DONE]\n\n")
		}
	}()

	// Stage 2: Pipe with disconnect + dual timeout (TTFT + stall watchdog)
	// VansRouter: first chunk = STREAM_FIRST_CHUNK_TIMEOUT_MS, subsequent = STREAM_STALL_TIMEOUT_MS
	pipeOut := PipeWithDisconnect(kiroPR, sc, StreamFirstChunkTimeout, StreamStallTimeout, resolved.Upstream)

	// Stage 3: Passthrough transform (stream.js createSSEStream PASSTHROUGH mode)
	finalReader := createPassthroughTransform(pipeOut, sc, resolved.Upstream)

	// SSE headers (sseConstants.js SSE_HEADERS_CORS)
	for k, v := range SSEHeadersCORS {
		w.Header().Set(k, v)
	}

	// Streaming copy
	buf := make([]byte, 32768)
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		n, rerr := finalReader.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return nil
			}
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
		}
		if rerr == io.EOF {
			return nil
		}
		if rerr != nil {
			return nil
		}
	}
}

// repairEnabled returns true unless disabled via env var or per-credential flag.
// VansRouter pattern: KIRO_TOOL_CALL_REPAIR env + credentials.providerSpecificData.kiroToolCallRepair
func repairEnabled(creds Credentials) bool {
	if os.Getenv("KIRO_TOOL_CALL_REPAIR") == "false" {
		return false
	}
	// Credential-level opt-out: kiroToolCallRepair=false on the connection
	if !creds.PSD.KiroToolCallRepair && creds.PSD.KiroToolCallRepairSet {
		return false
	}
	return true
}

// ExecuteWithIntegrityCheck runs Execute with the 9router integrity gate:
// buffer full SSE, validate content, retry with repair instruction on ellipsis/short_final/invalid_tool.
// VansRouter ref: kiro.js executeWithIntegrityCheck
func ExecuteWithIntegrityCheck(ctx context.Context, creds Credentials, req ChatRequest, w http.ResponseWriter, conversationID, connectionID string, rtkEnabled bool) error {
	acquireThrottle(kiroThrottleMs)

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

	if profileArn == "" && authMethod != "api_key" && authMethod != "" {
		discoverCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		arn, _ := DiscoverProfileArn(discoverCtx, creds.AccessToken, creds.PSD.Region)
		cancel()
		if arn != "" {
			profileArn = arn
		}
	}

	kiroReq, err := buildKiroRequest(req, resolved, profileArn, conversationID, connectionID)
	if err != nil {
		return err
	}

	kiroBody := kiroReq.Body

	if rtkEnabled {
		compressed, stats := rtk.CompressKiroBody(kiroBody)
		kiroBody = compressed
		if line := rtk.FormatRtkLog(stats); line != "" {
			log.Print(line)
		}
	}

	region := ResolveRuntimeRegion(creds.PSD.Region, profileArn)
	urls := GetOrderedBaseURLs(creds, region)

	result := doIntegrityAttemptWithFallback(ctx, creds, urls, kiroBody, resolved.Upstream, resolved.Thinking)
	if result.Kind == IntegrityComplete {
		streamSSEBytes(ctx, w, result.Bytes, resolved.Upstream)
		return nil
	}
	if result.Kind == IntegrityAccountError {
		return fmt.Errorf("%s", result.Message)
	}
	if result.Kind == IntegrityTerminalStop || result.Kind == IntegrityUpstreamError {
		writeIntegritySSE(w, integrityFailureSSE(result))
		return nil
	}

	repairKind := ""
	switch result.Kind {
	case IntegrityEllipsis:
		repairKind = IntegrityEllipsis
	case IntegrityShortFinal:
		repairKind = IntegrityShortFinal
	case IntegrityInvalidTool:
		if !repairEnabled(creds) {
			// VansRouter: tool repair disabled → return error directly, no retry
			writeIntegritySSE(w, encodeSSEErrorWithDiagnostics(
				"kiro_integrity_repair_disabled",
				"Kiro tool call repair is disabled for this account/connection",
				result.Diagnostics,
			))
			return nil
		}
		repairKind = IntegrityInvalidTool
	}
	if repairKind == "" {
		writeIntegritySSE(w, encodeSSEErrorWithDiagnostics("kiro_integrity_failed", "Kiro integrity validation failed: "+result.Message, result.Diagnostics))
		return nil
	}

	repairedBody := appendRepairInstruction(kiroReq.Body, repairKind)

	// Start heartbeat during repair to keep connection alive
	heartbeatStop := make(chan struct{})
	defer close(heartbeatStop)
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				w.Write([]byte(": heartbeat\n\n"))
				if flusher, ok := w.(http.Flusher); ok {
					flusher.Flush()
				}
			case <-heartbeatStop:
				return
			case <-ctx.Done():
				return
			}
		}
	}()

	result2 := doIntegrityAttemptWithFallback(ctx, creds, urls, repairedBody, resolved.Upstream, resolved.Thinking)
	if result2.Kind == IntegrityComplete {
		streamSSEBytes(ctx, w, result2.Bytes, resolved.Upstream)
		return nil
	}
	if result2.Kind == IntegrityAccountError {
		return fmt.Errorf("%s", result2.Message)
	}

	code := "kiro_ellipsis_retry_failed"
	switch result2.Kind {
	case IntegrityShortFinal:
		code = "kiro_short_final_retry_failed"
	case IntegrityInvalidTool:
		code = "kiro_tool_call_repair_retry_failed"
	case IntegrityMissingTerminal:
		code = "kiro_missing_terminal_retry_failed"
	}
	writeIntegritySSE(w, encodeSSEErrorWithDiagnostics(code,
		fmt.Sprintf("Kiro integrity validation failed after one bounded retry: %s", result2.Message),
		result2.Diagnostics))
	return nil
}

// VansRouter ref: kiro.js streamSSEBytes — replay pre-buffered SSE
func streamSSEBytes(ctx context.Context, w http.ResponseWriter, data []byte, model string) {
	sc := NewStreamController(ctx)
	// Pre-buffered data: no TTFT needed, use stall timeout
	pipeOut := PipeWithDisconnect(bytes.NewReader(data), sc, 0, StreamStallTimeout, model)
	finalReader := createPassthroughTransform(pipeOut, sc, model)
	for k, v := range SSEHeadersCORS {
		w.Header().Set(k, v)
	}
	buf := make([]byte, 32768)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		n, rerr := finalReader.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
		}
		if rerr == io.EOF {
			return
		}
		if rerr != nil {
			return
		}
	}
}

// VansRouter ref: kiro.js writeIntegritySSE
func writeIntegritySSE(w http.ResponseWriter, data []byte) {
	for k, v := range SSEHeadersCORS {
		w.Header().Set(k, v)
	}
	w.Write(data)
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

// VansRouter ref: kiro.js doIntegrityAttemptWithFallback
func doIntegrityAttemptWithFallback(ctx context.Context, creds Credentials, urls []string, body []byte, model string, thinkingEnabled bool) *IntegrityResult {
	var lastErr error
	retryAttemptsByUrl := make(map[int]int)
	for urlIndex := 0; urlIndex < len(urls); urlIndex++ {
		url := urls[urlIndex]

		resp, err := sendToKiroEndpoint(ctx, creds, url, body)
		if err != nil {
			lastErr = err
			// Network error → retry same URL (matches vansrouter 502 retry pattern)
			entry := defaultRetryConfig[502]
			attempts, delayMs := resolveRetryEntry(entry)
			if attempts > 0 && retryAttemptsByUrl[urlIndex] < attempts {
				retryAttemptsByUrl[urlIndex]++
				select {
				case <-ctx.Done():
					return &IntegrityResult{Kind: IntegrityAccountError, Message: ctx.Err().Error()}
				case <-time.After(time.Duration(delayMs) * time.Millisecond):
				}
				urlIndex--
				continue
			}
			if urlIndex+1 < len(urls) {
				continue
			}
			return &IntegrityResult{
				Kind:    IntegrityAccountError,
				Message: fmt.Sprintf("all %d endpoints failed: %v", len(urls), lastErr),
			}
		}

		// Per-status retry (matches vansrouter tryRetry pattern)
		if entry, ok := defaultRetryConfig[resp.StatusCode]; ok {
			attempts, delayMs := resolveRetryEntry(entry)
			if attempts > 0 && retryAttemptsByUrl[urlIndex] < attempts {
				resp.Body.Close()
				retryAttemptsByUrl[urlIndex]++
				select {
				case <-ctx.Done():
					return &IntegrityResult{Kind: IntegrityAccountError, Message: ctx.Err().Error()}
				case <-time.After(time.Duration(delayMs) * time.Millisecond):
				}
				urlIndex--
				continue
			}
		}

		if resp.StatusCode == http.StatusTooManyRequests {
			resp.Body.Close()
			lastErr = fmt.Errorf("429 from %s", url)
			if urlIndex+1 < len(urls) {
				continue
			}
			return &IntegrityResult{
				Kind:    IntegrityAccountError,
				Message: fmt.Sprintf("all %d endpoints returned 429", len(urls)),
			}
		}

		if resp.StatusCode != http.StatusOK {
			errBody := readResponsePrefix(resp.Body, 4096)
			resp.Body.Close()
			if resp.StatusCode == http.StatusPaymentRequired {
				return &IntegrityResult{Kind: IntegrityAccountError, Message: ErrExhausted.Error()}
			}
			if resp.StatusCode == http.StatusForbidden {
				var e struct {
					Reason  string `json:"reason"`
					Message string `json:"message"`
				}
				json.Unmarshal([]byte(errBody), &e)
				if e.Reason == "TEMPORARILY_SUSPENDED" || strings.Contains(strings.ToLower(e.Message), "suspended") {
					return &IntegrityResult{Kind: IntegrityAccountError, Message: ErrSuspended.Error()}
				}
			}
			lastErr = fmt.Errorf("kiro upstream: %s — %s", resp.Status, errBody)
			if urlIndex+1 < len(urls) {
				continue
			}
			return &IntegrityResult{Kind: IntegrityAccountError, Message: lastErr.Error()}
		}

		defer resp.Body.Close()
		return processIntegrityResponse(resp, model, thinkingEnabled)
	}

	return &IntegrityResult{
		Kind:    IntegrityAccountError,
		Message: fmt.Sprintf("all %d endpoints failed: %v", len(urls), lastErr),
	}
}

// readResponsePrefix reads up to maxBytes from the response body for error diagnostics.
// VansRouter ref: kiro.js readResponsePrefix — bounded read, prevents giant error bodies.
func readResponsePrefix(r io.Reader, maxBytes int) string {
	limited := io.LimitReader(r, int64(maxBytes))
	b, err := io.ReadAll(limited)
	if err != nil {
		return fmt.Sprintf("(read error: %v)", err)
	}
	return string(b)
}

// VansRouter ref: kiro.js processIntegrityResponse — SSE transform + validate
func processIntegrityResponse(resp *http.Response, model string, thinkingEnabled bool) *IntegrityResult {
	var buf bytes.Buffer
	var diagResult *IntegrityDiagnostics

	opts := &TransformOptions{
		OnTerminalState: func(d *IntegrityDiagnostics) {
			diagResult = d
		},
	}

	err := transformKiroToSSE(resp.Body, model, thinkingEnabled, &buf, opts)
	if err != nil {
		return &IntegrityResult{
			Kind:    IntegrityMissingTerminal,
			Message: fmt.Sprintf("Kiro transport read failed: %s", err.Error()),
			Diagnostics: &IntegrityDiagnostics{
				TerminalProvenance: "transport_read_error",
				TransportState:     "upstream_error",
				StopDisposition:    StopTerminalIncomplete,
				ResponseState:      "no_semantic_output",
				EventCounts:        map[string]int{},
			},
		}
	}

	maxBytes := envPositiveInt("KIRO_TOOL_CALL_REPAIR_BUFFER_MAX_BYTES", KIRO_TOOL_CALL_REPAIR_BUFFER_MAX_BYTES)
	return validateIntegrity(bytes.NewReader(buf.Bytes()), maxBytes, diagResult)
}

// sendToKiroEndpoint sends the request to a single Kiro URL.
// NOTE: context.WithTimeout untuk connect timeout TIDAK dipakai di sini karena
// Go's resp.Body terikat ke request context — wrapping dengan timeout akan membunuh
// stream setelah N detik, bukan cuma connection phase.
// VansRouter JS clearTimeout(fetchTimeoutId) setelah response headers diterima;
// Go tidak punya mekanisme equivalent, jadi timeout streaming diserahkan ke
// PipeWithDisconnect (TTFT 200s + stall 360s).
// VansRouter ref: kiro.js sendToKiroEndpoint — FETCH_CONNECT_TIMEOUT_MS (connect only)
func sendToKiroEndpoint(ctx context.Context, creds Credentials, url string, body []byte) (*http.Response, error) {
	headers := BuildKiroHeaders(creds)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	return getHttpClient().GetClient().Do(req)
}
