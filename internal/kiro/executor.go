package kiro

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const (
	maxRetries        = 2
	retryDelayMs      = 2000
	intraRetryDelayMs = 2000
)

func Execute(ctx context.Context, creds Credentials, req ChatRequest, w http.ResponseWriter, conversationID string) error {
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

	kiroBody, err := buildKiroRequest(req, resolved, profileArn, conversationID)
	if err != nil {
		return err
	}

	filteredBody := transformRequestPayload(kiroBody)

	region := ResolveRuntimeRegion(creds.PSD.Region, profileArn)
	url := KiroRuntimeHost(region) + "/generateAssistantResponse"

	resp, err := sendToKiroWithRetry(ctx, creds, url, filteredBody)
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
			var e struct {
				Reason  string `json:"reason"`
				Message string `json:"message"`
			}
			json.Unmarshal(errBody, &e)
			if e.Reason == "TEMPORARILY_SUSPENDED" || strings.Contains(strings.ToLower(e.Message), "suspended") {
				return ErrSuspended
			}
		}
		return fmt.Errorf("kiro upstream: %s — %s", resp.Status, string(errBody))
	}

	// Content-type validation (streamingHandler.js:62-80)
	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "text/event-stream") && !strings.Contains(ct, "application/json") {
		bodyText, _ := io.ReadAll(resp.Body)
		titleRe := regexp.MustCompile(`<title>([^<]+)</title>`)
		match := titleRe.FindStringSubmatch(string(bodyText))
		shortMsg := ""
		if len(match) > 1 {
			shortMsg = strings.TrimSpace(match[1])
			shortMsg = regexp.MustCompile(`<[^>]*>`).ReplaceAllString(shortMsg, "")
			shortMsg = regexp.MustCompile(`[\r\n]+`).ReplaceAllString(shortMsg, " ")
			if len(shortMsg) > 160 {
				shortMsg = shortMsg[:160]
			}
		}
		if shortMsg == "" {
			if len(bodyText) < 200 {
				shortMsg = regexp.MustCompile(`<[^>]*>`).ReplaceAllString(string(bodyText), "")
				shortMsg = strings.TrimSpace(shortMsg)
				if len(shortMsg) > 160 {
					shortMsg = shortMsg[:160]
				}
			} else {
				shortMsg = fmt.Sprintf("Upstream returned non-SSE response (%s)", ct)
			}
		}
		if shortMsg == "" {
			shortMsg = fmt.Sprintf("non-SSE response status=%d", resp.StatusCode)
		}
		status := resp.StatusCode
		if status == 0 {
			status = 502
		}
		return fmt.Errorf("[%d]: %s", status, shortMsg)
	}

	// Pipeline: Kiro EventStream → SSE → passthrough transform → client
	sc := NewStreamController(ctx)

	// Stage 1: Kiro EventStream → raw SSE via pipe
	kiroPR, kiroPW := io.Pipe()
	go func() {
		defer kiroPW.Close()
		err := transformKiroToSSE(resp.Body, resolved.Upstream, kiroPW)
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
