package kiro

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"farouter/internal/rtk"
)

const (
	maxRetries        = 2
	retryDelayMs      = 2000
	intraRetryDelayMs = 2000
)

func Execute(ctx context.Context, creds Credentials, req ChatRequest, w http.ResponseWriter, conversationID, connectionID string, rtkEnabled bool) error {
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

	kiroBody, err := buildKiroRequest(req, resolved, profileArn, conversationID, connectionID)
	if err != nil {
		return err
	}

	if rtkEnabled {
		kiroBody = rtk.ProcessKiroBody(kiroBody)
	}

	filteredBody := transformRequestPayload(kiroBody)

	region := ResolveRuntimeRegion(creds.PSD.Region, profileArn)
	urls := GetOrderedBaseURLs(creds, region)

	var lastErr error
	var lastStatus int
	for urlIndex, url := range urls {
		resp, err := sendToKiroEndpoint(ctx, creds, url, filteredBody)
		if err != nil {
			lastErr = err
			if urlIndex+1 < len(urls) {
				continue
			}
			return fmt.Errorf("kiro: all %d endpoints failed: %w", len(urls), lastErr)
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
			errBody, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusPaymentRequired {
				return ErrExhausted
			}
			if resp.StatusCode == http.StatusForbidden {
				var e struct {
					Reason  string `json:"reason"`
					Message string `json:"message"`
				}
				json.Unmarshal(errBody, &e)
				if e.Reason == "TEMPORARILY_SUSPENDED" || strings.Contains(strings.ToLower(e.Message), "suspended") {
					return ErrSuspended
				}
			}
			lastErr = fmt.Errorf("kiro upstream: %s — %s", resp.Status, string(errBody))
			lastStatus = resp.StatusCode
			if urlIndex+1 < len(urls) {
				continue
			}
			return lastErr
		}

		defer resp.Body.Close()
		return pipeKiroResponse(ctx, resp, w, resolved)
	}

	if lastErr != nil {
		return lastErr
	}
	return fmt.Errorf("kiro: all %d endpoints failed with status %d", len(urls), lastStatus)
}

func pipeKiroResponse(ctx context.Context, resp *http.Response, w http.ResponseWriter, resolved ResolvedModel) error {
	// Pipeline: Kiro EventStream → SSE → passthrough transform → client
	sc := NewStreamController(ctx)

	// Stage 1: Kiro EventStream → raw SSE via pipe
	kiroPR, kiroPW := io.Pipe()
	go func() {
		defer kiroPW.Close()
		err := transformKiroToSSE(resp.Body, resolved.Upstream, resolved.Thinking, kiroPW, nil)
		if err != nil {
			writeStreamError(kiroPW, 502, err.Error())
			fmt.Fprintf(kiroPW, "data: [DONE]\n\n")
		}
	}()

	// Stage 2: Pipe with disconnect + stall watchdog (streamHandler.js pipeWithDisconnect)
	pipeOut := PipeWithDisconnect(kiroPR, sc, StreamStallTimeoutMs, resolved.Upstream)

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

// ExecuteWithIntegrityCheck runs Execute with the 9router integrity gate:
// buffer full SSE, validate content, retry with repair instruction on ellipsis/short_final/invalid_tool.
func ExecuteWithIntegrityCheck(ctx context.Context, creds Credentials, req ChatRequest, w http.ResponseWriter, conversationID, connectionID string, rtkEnabled bool) error {
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

	kiroBody, err := buildKiroRequest(req, resolved, profileArn, conversationID, connectionID)
	if err != nil {
		return err
	}

	if rtkEnabled {
		kiroBody = rtk.ProcessKiroBody(kiroBody)
	}

	filteredBody := transformRequestPayload(kiroBody)
	region := ResolveRuntimeRegion(creds.PSD.Region, profileArn)
	urls := GetOrderedBaseURLs(creds, region)

	result := doIntegrityAttemptWithFallback(ctx, creds, urls, filteredBody, resolved.Upstream, resolved.Thinking)
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
		repairKind = IntegrityInvalidTool
	}
	if repairKind == "" {
		writeIntegritySSE(w, encodeSSEErrorWithDiagnostics("kiro_integrity_failed", "Kiro integrity validation failed: "+result.Message, result.Diagnostics))
		return nil
	}
	
	repairedBody := appendRepairInstruction(filteredBody, repairKind)
	
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

func streamSSEBytes(ctx context.Context, w http.ResponseWriter, data []byte, model string) {
	sc := NewStreamController(ctx)
	pipeOut := PipeWithDisconnect(bytes.NewReader(data), sc, StreamStallTimeoutMs, model)
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

func writeIntegritySSE(w http.ResponseWriter, data []byte) {
	for k, v := range SSEHeadersCORS {
		w.Header().Set(k, v)
	}
	w.Write(data)
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

func doIntegrityAttemptWithFallback(ctx context.Context, creds Credentials, urls []string, body []byte, model string, thinkingEnabled bool) *IntegrityResult {
	var lastErr error
	for urlIndex, url := range urls {
		resp, err := sendToKiroEndpoint(ctx, creds, url, body)
		if err != nil {
			lastErr = err
			if urlIndex+1 < len(urls) {
				continue
			}
			return &IntegrityResult{
				Kind:    IntegrityAccountError,
				Message: fmt.Sprintf("all %d endpoints failed: %v", len(urls), lastErr),
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
			errBody, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusPaymentRequired {
				return &IntegrityResult{Kind: IntegrityAccountError, Message: ErrExhausted.Error()}
			}
			if resp.StatusCode == http.StatusForbidden {
				var e struct {
					Reason  string `json:"reason"`
					Message string `json:"message"`
				}
				json.Unmarshal(errBody, &e)
				if e.Reason == "TEMPORARILY_SUSPENDED" || strings.Contains(strings.ToLower(e.Message), "suspended") {
					return &IntegrityResult{Kind: IntegrityAccountError, Message: ErrSuspended.Error()}
				}
			}
			lastErr = fmt.Errorf("kiro upstream: %s — %s", resp.Status, string(errBody))
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

	return validateIntegrity(bytes.NewReader(buf.Bytes()), KIRO_TOOL_CALL_REPAIR_BUFFER_MAX_BYTES, diagResult)
}

func transformRequestPayload(body []byte) []byte {
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return body
	}

	allowed := map[string]bool{
		"conversationState":            true,
		"profileArn":                   true,
		"inferenceConfig":              true,
		"additionalModelRequestFields": true,
		"systemPrompt":                 true,
		"agentMode":                    true,
	}

	filtered := make(map[string]any, len(allowed))
	for k, v := range raw {
		if allowed[k] {
			filtered[k] = v
		}
	}

	b, err := json.Marshal(filtered)
	if err != nil {
		return body
	}
	return b
}

func sendToKiroWithRetry(ctx context.Context, creds Credentials, url string, body []byte) (*http.Response, error) {
	headers := BuildKiroHeaders(creds)

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			delay := time.Duration(intraRetryDelayMs*attempt) * time.Millisecond
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			lastErr = err
			continue
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}

		resp, err := getHttpClient().Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			resp.Body.Close()
			lastErr = fmt.Errorf("429 from %s", url)
			continue
		}
		return resp, nil
	}
	return nil, fmt.Errorf("kiro endpoint %s failed after %d retries: %w", url, maxRetries, lastErr)
}

func sendToKiroEndpoint(ctx context.Context, creds Credentials, url string, body []byte) (*http.Response, error) {
	headers := BuildKiroHeaders(creds)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	return getHttpClient().Do(req)
}
