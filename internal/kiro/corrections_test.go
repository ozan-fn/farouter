package kiro

import (
	"testing"
)

// TestThinkingBudgetConstants verifies constants match VansRouter
// VansRouter ref: open-sse/config/kiroConstants.js:41
func TestThinkingBudgetConstants(t *testing.T) {
	tests := []struct {
		name     string
		got      int
		expected int
		desc     string
	}{
		{
			name:     "DefaultBudget",
			got:      ThinkingBudgetDefault,
			expected: 16000,
			desc:     "VansRouter default is 16,000 tokens",
		},
		{
			name:     "MinBudget",
			got:      ThinkingBudgetMin,
			expected: 1,
			desc:     "VansRouter minimum is 1 token",
		},
		{
			name:     "MaxBudget",
			got:      ThinkingBudgetMax,
			expected: 32000,
			desc:     "VansRouter maximum is 32,000 tokens",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.expected {
				t.Errorf("%s: got %d, expected %d (%s)", tt.name, tt.got, tt.expected, tt.desc)
			}
		})
	}
}

// TestBuildThinkingSystemPrefix verifies correct prefix generation with clamping
// VansRouter ref: open-sse/config/kiroConstants.js:315-317
func TestBuildThinkingSystemPrefix(t *testing.T) {
	tests := []struct {
		name     string
		budget   int
		expected string
		desc     string
	}{
		{
			name:     "ZeroBudgetUsesDefault",
			budget:   0,
			expected: "<thinking_mode>enabled</thinking_mode><max_thinking_length>16000</max_thinking_length>",
			desc:     "Zero budget clamped to default 16,000",
		},
		{
			name:     "NegativeBudgetUsesDefault",
			budget:   -5,
			expected: "<thinking_mode>enabled</thinking_mode><max_thinking_length>16000</max_thinking_length>",
			desc:     "Negative budget clamped to default 16,000",
		},
		{
			name:     "ValidBudget",
			budget:   8192,
			expected: "<thinking_mode>enabled</thinking_mode><max_thinking_length>8192</max_thinking_length>",
			desc:     "Valid budget passed through",
		},
		{
			name:     "BudgetClampedToMin",
			budget:   -100,
			expected: "<thinking_mode>enabled</thinking_mode><max_thinking_length>16000</max_thinking_length>",
			desc:     "Budget below min clamped to default",
		},
		{
			name:     "BudgetClampedToMax",
			budget:   50000,
			expected: "<thinking_mode>enabled</thinking_mode><max_thinking_length>32000</max_thinking_length>",
			desc:     "Budget above max clamped to 32,000",
		},
		{
			name:     "BudgetAtMaxBoundary",
			budget:   32000,
			expected: "<thinking_mode>enabled</thinking_mode><max_thinking_length>32000</max_thinking_length>",
			desc:     "Budget at max boundary accepted",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildThinkingSystemPrefix(tt.budget)
			if got != tt.expected {
				t.Errorf("%s: got %q, expected %q\n  %s", tt.name, got, tt.expected, tt.desc)
			}
		})
	}
}

// TestResolveThinkingBudgetEffortLevels verifies effort→budget mapping matches VansRouter web-standard
// VansRouter ref: open-sse/translator/concerns/thinking.js:9-24
func TestResolveThinkingBudgetEffortLevels(t *testing.T) {
	tests := []struct {
		name     string
		effort   string
		model    string
		expected int
		desc     string
	}{
		{
			name:     "EffortNone",
			effort:   "none",
			model:    "",
			expected: -1,
			desc:     "Effort 'none' disables thinking",
		},
		{
			name:     "EffortOff",
			effort:   "off",
			model:    "",
			expected: -1,
			desc:     "Effort 'off' disables thinking",
		},
		{
			name:     "EffortDisabled",
			effort:   "disabled",
			model:    "",
			expected: -1,
			desc:     "Effort 'disabled' disables thinking",
		},
		{
			name:     "EffortMinimal",
			effort:   "minimal",
			model:    "",
			expected: 512,
			desc:     "VansRouter web-standard: minimal = 512 tokens",
		},
		{
			name:     "EffortLow",
			effort:   "low",
			model:    "",
			expected: 1024,
			desc:     "VansRouter web-standard: low = 1,024 tokens",
		},
		{
			name:     "EffortMedium",
			effort:   "medium",
			model:    "",
			expected: 8192,
			desc:     "VansRouter web-standard: medium = 8,192 tokens",
		},
		{
			name:     "EffortHigh",
			effort:   "high",
			model:    "",
			expected: 24576,
			desc:     "VansRouter web-standard: high = 24,576 tokens",
		},
		{
			name:     "EffortXHigh",
			effort:   "xhigh",
			model:    "",
			expected: 32768,
			desc:     "VansRouter web-standard: xhigh = 32,768 tokens",
		},
		{
			name:     "EffortMax",
			effort:   "max",
			model:    "",
			expected: 128000,
			desc:     "VansRouter web-standard: max = 128,000 tokens",
		},
		{
			name:     "ModelThinkingSuffix",
			effort:   "",
			model:    "claude-sonnet-4.5-thinking",
			expected: 16000,
			desc:     "Model with '-thinking' suffix uses default budget",
		},
		{
			name:     "ModelReasonSuffix",
			effort:   "",
			model:    "gpt-5.6-reason",
			expected: 16000,
			desc:     "Model with '-reason' suffix uses default budget",
		},
		{
			name:     "NoEffortNoModel",
			effort:   "",
			model:    "claude-sonnet-4.5",
			expected: -1,
			desc:     "No effort and no thinking model = disabled",
		},
		{
			name:     "CaseInsensitiveLow",
			effort:   "LOW",
			model:    "",
			expected: 1024,
			desc:     "Case-insensitive effort matching",
		},
		{
			name:     "CaseInsensitiveHigh",
			effort:   "HIGH",
			model:    "",
			expected: 24576,
			desc:     "Case-insensitive effort matching",
		},
		{
			name:     "EffortTrumpsModel",
			effort:   "low",
			model:    "claude-sonnet-4.5-thinking",
			expected: 1024,
			desc:     "Explicit effort trumps model-based detection",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveThinkingBudget(tt.effort, tt.model)
			if got != tt.expected {
				t.Errorf("%s: got %d, expected %d\n  %s", tt.name, got, tt.expected, tt.desc)
			}
		})
	}
}

// TestNormalizeStopReasonString verifies stop reason normalization matches VansRouter
// VansRouter ref: src/mitm/handlers/kiro.js:135-141
func TestNormalizeStopReasonString(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
		desc     string
	}{
		{
			name:     "EmptyString",
			input:    "",
			expected: "",
			desc:     "Empty input returns empty",
		},
		{
			name:     "EndTurn",
			input:    "endTurn",
			expected: "end_turn",
			desc:     "endTurn → end_turn (camelCase normalization)",
		},
		{
			name:     "EndTurnAlready",
			input:    "end_turn",
			expected: "end_turn",
			desc:     "end_turn already normalized",
		},
		{
			name:     "ToolUse",
			input:    "toolUse",
			expected: "tool_use",
			desc:     "toolUse → tool_use (camelCase)",
		},
		{
			name:     "ToolCalls",
			input:    "tool_calls",
			expected: "tool_use",
			desc:     "tool_calls → tool_use (alias)",
		},
		{
			name:     "MaxTokens",
			input:    "maxTokens",
			expected: "max_tokens",
			desc:     "maxTokens → max_tokens (camelCase)",
		},
		{
			name:     "MaxOutputTokens",
			input:    "max_output_tokens",
			expected: "max_tokens",
			desc:     "max_output_tokens → max_tokens (alias)",
		},
		{
			name:     "Length",
			input:    "length",
			expected: "max_tokens",
			desc:     "length → max_tokens (alias)",
		},
		{
			name:     "ModelContextWindowExceeded",
			input:    "model_context_window_exceeded",
			expected: "model_context_window_exceeded",
			desc:     "Multi-word reason preserved",
		},
		{
			name:     "MalformedModelOutput",
			input:    "malformed_model_output",
			expected: "malformed_model_output",
			desc:     "malformed_model_output preserved",
		},
		{
			name:     "ContentFilter",
			input:    "content_filter",
			expected: "content_filter",
			desc:     "content_filter preserved",
		},
		{
			name:     "WithWhitespace",
			input:    "end turn",
			expected: "end_turn",
			desc:     "Whitespace collapsed to underscore",
		},
		{
			name:     "WithHyphen",
			input:    "end-turn",
			expected: "end_turn",
			desc:     "Hyphens collapsed to underscore",
		},
		{
			name:     "MixedCase",
			input:    "EndTurn",
			expected: "end_turn",
			desc:     "Mixed case normalized",
		},
		{
			name:     "ConsecutiveHyphens",
			input:    "max---tokens",
			expected: "max_tokens",
			desc:     "Consecutive hyphens collapsed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeStopReasonString(tt.input)
			if got != tt.expected {
				t.Errorf("%s: got %q, expected %q\n  %s", tt.name, got, tt.expected, tt.desc)
			}
		})
	}
}

// TestStopDisposition verifies stop reason → disposition mapping matches VansRouter
// VansRouter ref: src/mitm/handlers/kiro.js:406-412
func TestStopDisposition(t *testing.T) {
	tests := []struct {
		name         string
		stopReason   string
		hasToolCalls bool
		expected     StopDisposition
		desc         string
	}{
		{
			name:         "MalformedModelOutput",
			stopReason:   "malformed_model_output",
			hasToolCalls: false,
			expected:     StopRetryableProtocolFail,
			desc:         "Malformed output is retryable",
		},
		{
			name:         "InvalidModelOutput",
			stopReason:   "invalid_model_output",
			hasToolCalls: false,
			expected:     StopRetryableProtocolFail,
			desc:         "Invalid output is retryable",
		},
		{
			name:         "Cancelled",
			stopReason:   "cancelled",
			hasToolCalls: false,
			expected:     StopTerminalIncomplete,
			desc:         "Cancelled is terminal incomplete",
		},
		{
			name:         "PauseTurn",
			stopReason:   "pause_turn",
			hasToolCalls: false,
			expected:     StopTerminalIncomplete,
			desc:         "Pause turn is terminal incomplete",
		},
		{
			name:         "ModelContextWindowExceeded",
			stopReason:   "model_context_window_exceeded",
			hasToolCalls: false,
			expected:     StopTerminalIncomplete,
			desc:         "Context window exceeded is terminal incomplete",
		},
		{
			name:         "RefusalDirect",
			stopReason:   "refusal",
			hasToolCalls: false,
			expected:     StopTerminalRefusal,
			desc:         "Direct refusal is terminal refusal",
		},
		{
			name:         "ContentFilter",
			stopReason:   "content_filter",
			hasToolCalls: false,
			expected:     StopTerminalRefusal,
			desc:         "Content filter is terminal refusal",
		},
		{
			name:         "MaxTokensWithoutTools",
			stopReason:   "max_tokens",
			hasToolCalls: false,
			expected:     StopLength,
			desc:         "Max tokens without tools = length",
		},
		{
			name:         "MaxTokensWithTools",
			stopReason:   "max_tokens",
			hasToolCalls: true,
			expected:     StopTerminalIncomplete,
			desc:         "Max tokens with tools = terminal incomplete",
		},
		{
			name:         "ToolUseWithFlag",
			stopReason:   "tool_use",
			hasToolCalls: true,
			expected:     StopToolUse,
			desc:         "Tool use with flag = tool use",
		},
		{
			name:         "ToolUseWithoutFlag",
			stopReason:   "tool_use",
			hasToolCalls: false,
			expected:     StopToolUse,
			desc:         "Tool use reason = tool use (regardless of flag)",
		},
		{
			name:         "EndTurn",
			stopReason:   "end_turn",
			hasToolCalls: false,
			expected:     StopComplete,
			desc:         "End turn = complete",
		},
		{
			name:         "Empty",
			stopReason:   "",
			hasToolCalls: false,
			expected:     StopComplete,
			desc:         "Empty reason = complete",
		},
		{
			name:         "UnknownReason",
			stopReason:   "unknown_reason",
			hasToolCalls: false,
			expected:     StopUnknownFailure,
			desc:         "Unknown reason = unknown failure",
		},
		{
			name:         "ToolCallsWithUnknown",
			stopReason:   "unknown_reason",
			hasToolCalls: true,
			expected:     StopUnknownFailure,
			desc:         "Unknown reason with tool calls = unknown failure",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stopDisposition(tt.stopReason, tt.hasToolCalls)
			if got != tt.expected {
				t.Errorf("%s: got %v, expected %v\n  %s", tt.name, got, tt.expected, tt.desc)
			}
		})
	}
}

// TestRefusalMatch verifies refusal pattern detection matches VansRouter
// VansRouter ref: src/mitm/handlers/kiro.js:406-412 refusal regex
func TestRefusalMatch(t *testing.T) {
	tests := []struct {
		name     string
		reason   string
		expected bool
		desc     string
	}{
		{
			name:     "DirectRefusal",
			reason:   "refusal",
			expected: true,
			desc:     "Direct 'refusal' matches",
		},
		{
			name:     "ContentFilter",
			reason:   "content_filter",
			expected: true,
			desc:     "'content_filter' matches",
		},
		{
			name:     "Guardrail",
			reason:   "guardrail_triggered",
			expected: true,
			desc:     "'guardrail' matches",
		},
		{
			name:     "SafetyPolicy",
			reason:   "safety_policy_violation",
			expected: true,
			desc:     "'safety' and 'policy' match",
		},
		{
			name:     "Blocked",
			reason:   "request_blocked",
			expected: true,
			desc:     "'blocked' matches",
		},
		{
			name:     "NoMatch",
			reason:   "end_turn",
			expected: false,
			desc:     "'end_turn' doesn't match",
		},
		{
			name:     "EmptyString",
			reason:   "",
			expected: false,
			desc:     "Empty string doesn't match",
		},
		{
			name:     "PartialMatch",
			reason:   "content_analysis",
			expected: false,
			desc:     "Contains 'content' but not 'filter' - no match",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := refusalMatch(tt.reason)
			if got != tt.expected {
				t.Errorf("%s: got %v, expected %v\n  %s", tt.name, got, tt.expected, tt.desc)
			}
		})
	}
}

// TestIsEllipsisOnly verifies ellipsis detection
// VansRouter ref: src/mitm/handlers/kiro.js:169-171
func TestIsEllipsisOnly(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		expected bool
		desc     string
	}{
		{
			name:     "ThreeDots",
			value:    "...",
			expected: true,
			desc:     "Three dots is ellipsis only",
		},
		{
			name:     "EllipsisDot",
			value:    "…",
			expected: true,
			desc:     "Unicode ellipsis (…) is ellipsis only",
		},
		{
			name:     "WithWhitespace",
			value:    "  ...  ",
			expected: true,
			desc:     "Ellipsis with whitespace trimmed",
		},
		{
			name:     "WithText",
			value:    "... and more",
			expected: false,
			desc:     "Ellipsis with text is not ellipsis only",
		},
		{
			name:     "Empty",
			value:    "",
			expected: false,
			desc:     "Empty is not ellipsis only",
		},
		{
			name:     "OnlyWhitespace",
			value:    "   ",
			expected: false,
			desc:     "Only whitespace is not ellipsis only",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isEllipsisOnly(tt.value)
			if got != tt.expected {
				t.Errorf("%s: got %v, expected %v\n  %s", tt.name, got, tt.expected, tt.desc)
			}
		})
	}
}

// BenchmarkResolveThinkingBudget benchmarks effort→budget resolution
func BenchmarkResolveThinkingBudget(b *testing.B) {
	for i := 0; i < b.N; i++ {
		ResolveThinkingBudget("high", "")
	}
}

// BenchmarkNormalizeStopReasonString benchmarks stop reason normalization
func BenchmarkNormalizeStopReasonString(b *testing.B) {
	for i := 0; i < b.N; i++ {
		normalizeStopReasonString("modelContextWindowExceeded")
	}
}

// BenchmarkStopDisposition benchmarks disposition resolution
func BenchmarkStopDisposition(b *testing.B) {
	for i := 0; i < b.N; i++ {
		stopDisposition("max_tokens", true)
	}
}
