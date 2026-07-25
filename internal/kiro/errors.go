package kiro

import (
	"encoding/json"
	"fmt"
	"io"
)

type errorInfo struct {
	Type string
	Code string
}

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

func errorResponse(status int, message string) (int, map[string]any) {
	return status, buildErrorBody(status, message)
}

func writeStreamError(w io.Writer, status int, message string) {
	body := buildErrorBody(status, message)
	b, _ := json.Marshal(body)
	fmt.Fprintf(w, "data: %s\n\n", b)
}

func formatProviderError(err error, provider, model string, statusCode int) string {
	code := statusCode
	if code == 0 {
		if c, ok := err.(interface{ Code() string }); ok {
			return fmt.Sprintf("[%s]: %s", c.Code(), err.Error())
		}
		code = 502
	}
	msg := err.Error()
	if msg == "" {
		msg = "Unknown error"
	}
	var causeStr string
	type causer interface{ Cause() error }
	if c, ok := err.(causer); ok {
		cause := c.Cause()
		if cause != nil {
			causeStr = fmt.Sprintf(" (cause: %s)", cause.Error())
		}
	}
	return fmt.Sprintf("[%d]: %s%s", code, msg, causeStr)
}
