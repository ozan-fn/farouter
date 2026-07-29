package kiro

// VansRouter equivalents:
//   writeStreamError → open-sse/executors/kiro.js encodeSSEError()
//   buildErrorBody   → open-sse/config/runtimeConfig.js HTTP_STATUS / errorConfig.js
//   errorTypes       → open-sse/config/runtimeConfig.js HTTP_STATUS mapping
//
// formatProviderError — Go-specific error formatting for provider errors
// No direct VansRouter equivalent (provider error format is router-specific)

import (
	"encoding/json"
	"fmt"
	"io"
)

// formatProviderError — Go-specific error formatting for provider errors
// No direct VansRouter equivalent (provider error format is router-specific)
func formatProviderError(err error, provider string, endpoint string, status int) string {
	msg := fmt.Sprintf("%s://%s: %d", provider, endpoint, status)
	if err != nil {
		msg += " — " + err.Error()
	}
	return msg
}

type errorInfo struct {
	Type string
	Code string
}

// HTTP status → error type mapping. VansRouter: runtimeConfig.js HTTP_STATUS
var errorTypes = map[int]errorInfo{
	400: {"invalid_request_error", "bad_request"},
	401: {"authentication_error", "invalid_api_key"},
	402: {"billing_error", "payment_required"},
	403: {"permission_error", "insufficient_quota"},
	404: {"invalid_request_error", "model_not_found"},
	406: {"invalid_request_error", "model_not_supported"},
	429: {"rate_limit_error", "rate_limit_exceeded"},
	500: {"server_error", "internal_server_error"},
	502: {"server_error", "bad_gateway"},
	503: {"server_error", "service_unavailable"},
	504: {"server_error", "gateway_timeout"},
}

// VansRouter: errorConfig.js DEFAULT_ERROR_MESSAGES
var defaultErrorMessages = map[int]string{
	400: "Bad request",
	401: "Invalid API key provided",
	402: "Payment required",
	403: "You exceeded your current quota",
	404: "Model not found",
	406: "Model not supported",
	429: "Rate limit exceeded",
	500: "Internal server error",
	502: "Bad gateway - upstream provider error",
	503: "Service temporarily unavailable",
	504: "Gateway timeout",
}

// buildErrorBody — VansRouter: encodeSSEError() pattern
func buildErrorBody(status int, message string) map[string]any {
	info := errorTypes[status]
	if info.Type == "" {
		if status >= 500 {
			info = errorInfo{Type: "server_error", Code: "internal_server_error"}
		} else {
			info = errorInfo{Type: "invalid_request_error", Code: ""}
		}
	}
	msg := message
	if msg == "" {
		msg = defaultErrorMessages[status]
	}
	if msg == "" {
		msg = "An error occurred"
	}
	return map[string]any{
		"error": map[string]any{
			"message": msg,
			"type":    info.Type,
			"code":    info.Code,
		},
	}
}

// writeStreamError — VansRouter: encodeSSEError()
func writeStreamError(w io.Writer, status int, message string) {
	body := buildErrorBody(status, message)
	b, _ := json.Marshal(body)
	fmt.Fprintf(w, "data: %s\n\n", b)
}
