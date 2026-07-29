package kiro

import (
	"fmt"
)

const (
	agenticSuffix       = "-agentic"
	thinkingSuffix      = "-thinking"
	ThinkingBudgetDefault = 16000
	DefaultContextLength  = 200000

	DefaultProfileArnBuilderID = "arn:aws:codewhisperer:us-east-1:638616132270:profile/AAAACCCCXXXX"
	DefaultProfileArnSocial    = "arn:aws:codewhisperer:us-east-1:699475941385:profile/EHGA3GRVQMUK"

	AutoModel = "claude-sonnet-4.5"

	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
	RoleSystem    = "system"

	AgenticSystemPrompt = `# CRITICAL: CHUNKED WRITE PROTOCOL (MANDATORY)

You MUST follow these rules for ALL file operations. Violation causes server timeouts and task failure.

## ABSOLUTE LIMITS
- **MAXIMUM 350 LINES** per single write/edit operation - NO EXCEPTIONS
- **RECOMMENDED 300 LINES** or less for optimal performance
- **NEVER** write entire files in one operation if >300 lines

## MANDATORY CHUNKED WRITE STRATEGY

### For NEW FILES (>300 lines total):
1. FIRST: Write initial chunk (first 250-300 lines) using write_to_file/fsWrite
2. THEN: Append remaining content in 250-300 line chunks using file append operations
3. REPEAT: Continue appending until complete

### For EDITING EXISTING FILES:
1. Use surgical edits (apply_diff/targeted edits) - change ONLY what's needed
2. NEVER rewrite entire files - use incremental modifications
3. Split large refactors into multiple small, focused edits

### For LARGE CODE GENERATION:
1. Generate in logical sections (imports, types, functions separately)
2. Write each section as a separate operation
3. Use append operations for subsequent sections

## EXAMPLES OF CORRECT BEHAVIOR

CORRECT: Writing a 600-line file
- Operation 1: Write lines 1-300 (initial file creation)
- Operation 2: Append lines 301-600

CORRECT: Editing multiple functions
- Operation 1: Edit function A
- Operation 2: Edit function B
- Operation 3: Edit function C

WRONG: Writing 500 lines in single operation -> TIMEOUT
WRONG: Rewriting entire file to change 5 lines -> TIMEOUT
WRONG: Generating massive code blocks without chunking -> TIMEOUT

## WHY THIS MATTERS
- Server has 2-3 minute timeout for operations
- Large writes exceed timeout and FAIL completely
- Chunked writes are FASTER and more RELIABLE
- Failed writes waste time and require retry

REMEMBER: When in doubt, write LESS per operation. Multiple small operations > one large operation.`
)

var (
	baseURLs = []string{
		"https://runtime.us-east-1.kiro.dev/generateAssistantResponse",
		"https://codewhisperer.us-east-1.amazonaws.com/generateAssistantResponse",
		"https://q.us-east-1.amazonaws.com/generateAssistantResponse",
	}

	ErrExhausted      = fmt.Errorf("kiro: account exhausted (402)")
	ErrSuspended      = fmt.Errorf("kiro: account suspended (403)")
	ErrQuotaAllFailed = fmt.Errorf("kiro: all quota endpoints failed")
	ErrTokenAllFailed = fmt.Errorf("kiro: all token refresh endpoints failed")
)

// Provider-specific data persisted per connection.
type ProviderSpecificData struct {
	AuthMethod       string
	ClientID         string
	ClientSecret     string
	Region           string
	ProfileArn       string
	TokenEndpoint    string
	IssuerURL        string
	Scopes           string
	ClientSecretExp  string
}

// Credentials holds auth state for a single Kiro account.
type Credentials struct {
	AccessToken  string
	RefreshToken string
	ProfileArn   string
	PSD          ProviderSpecificData
}

// ChatRequest is an incoming OpenAI-format chat completion request.
type ChatRequest struct {
	Model           string              `json:"model"`
	Messages        []Message           `json:"messages"`
	Stream          bool                `json:"stream"`
	Temperature     *float64            `json:"temperature,omitempty"`
	TopP            *float64            `json:"top_p,omitempty"`
	ReasoningEffort string              `json:"reasoning_effort,omitempty"`
	Tools           []Tool              `json:"tools,omitempty"`
	MaxTokens       int                 `json:"max_tokens,omitempty"`
	MaxCompletion   int                 `json:"max_completion_tokens,omitempty"`
	OutputConfig    *OutputConfig       `json:"output_config,omitempty"`
	Thinking        *ThinkingBlock      `json:"thinking,omitempty"`
	CavemanLevel    string              `json:"caveman_level,omitempty"`
	PonytailLevel   string              `json:"ponytail_level,omitempty"`
}

type OutputConfig struct {
	Effort string `json:"effort,omitempty"`
}

type ThinkingBlock struct {
	Type        string `json:"type,omitempty"`
	BudgetTokens int   `json:"budget_tokens,omitempty"`
}

// Message is a single chat message in OpenAI format.
type Message struct {
	Role       string     `json:"role"`
	Content    any        `json:"content"`
	ToolCallID string     `json:"tool_call_id"`
	ToolCalls  []ToolCall `json:"tool_calls"`
}

type Tool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

type ToolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type ToolCall struct {
	ID       string             `json:"id"`
	Type     string             `json:"type"`
	Function ToolCallFunction   `json:"function"`
}

type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ResolvedModel is the result of resolving a model alias to upstream + flags.
type ResolvedModel struct {
	Upstream string
	Agentic  bool
	Thinking bool
}

// QuotaResult holds parsed quota/usage data from the upstream Kiro API.
type QuotaResult struct {
	Used      int
	Limit     int
	Remaining int
	ResetAt   string
	Plan      string
	Exhausted bool
}

// TokenResult is the response from a Kiro token refresh call.
type TokenResult struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	ProfileArn   string `json:"profileArn"`
	ExpiresIn    int    `json:"expiresIn"`
	Error        string `json:"error"`
}

// Event is a parsed AWS EventStream frame.
type Event struct {
	Headers map[string]string
	Payload map[string]any
}

// ── Stop disposition (kiro.js stopDisposition) ──────────────────────────

type StopDisposition string

const (
	StopComplete              StopDisposition = "complete"
	StopToolUse               StopDisposition = "tool_use"
	StopLength                StopDisposition = "length"
	StopRetryableProtocolFail StopDisposition = "retryable_protocol_failure"
	StopTerminalIncomplete    StopDisposition = "terminal_incomplete"
	StopTerminalRefusal       StopDisposition = "terminal_refusal"
	StopUnknownFailure        StopDisposition = "unknown_failure"
)

// IntegrityDiagnostics tracks stream-level integrity metadata (kiro.js diagnostics).
type IntegrityDiagnostics struct {
	TerminalProvenance   string            `json:"terminal_provenance"`
	TransportState       string            `json:"transport_state"`
	StopReason           string            `json:"stop_reason"`
	StopDisposition      StopDisposition   `json:"stop_disposition"`
	ResponseState        string            `json:"response_state"`
	EventCounts          map[string]int    `json:"event_counts"`
	IncompleteFrameBytes int               `json:"incomplete_frame_bytes"`
}

// IntegrityResult from validating buffered SSE content.
type IntegrityResult struct {
	Kind        string               `json:"kind"`
	Bytes       []byte               `json:"-"`
	Message     string               `json:"message,omitempty"`
	Diagnostics *IntegrityDiagnostics `json:"diagnostics,omitempty"`
}

const (
	IntegrityComplete        = "complete"
	IntegrityEllipsis        = "ellipsis"
	IntegrityShortFinal      = "short_final"
	IntegrityInvalidTool     = "invalid_tool"
	IntegrityTerminalStop    = "terminal_stop"
	IntegrityUpstreamError   = "upstream_error"
	IntegrityMissingTerminal = "missing_terminal"
	IntegrityAccountError    = "account_error"
)

const (
	RepairTool      = "Retry the previous response because its Kiro tool_call wrapper was malformed. If you use the wrapper tool named tool_call, its input must contain a non-empty name and an arguments field."
	RepairEllipsis  = "Retry the previous response because it ended with only an ellipsis. Return the complete final answer, not only ... or ...."
	RepairShortFinal = "Retry the previous response because its final only announced a future action. Complete the check now and return the result or a concrete blocker."
)

const (
	KIRO_TOOL_CALL_REPAIR_BUFFER_MAX_BYTES = 8 * 1024 * 1024
	EventstreamMaxMessageBytes             = 24 * 1024 * 1024
	EventstreamMaxHeadersBytes             = 128 * 1024
)

// UsageSummary tracks token usage across the stream.
type UsageSummary struct {
	PromptTokens             int    `json:"prompt_tokens"`
	CompletionTokens         int    `json:"completion_tokens"`
	TotalTokens              int    `json:"total_tokens"`
	CacheReadInputTokens     int    `json:"cache_read_input_tokens,omitempty"`
	CacheCreationInputTokens int    `json:"cache_creation_input_tokens,omitempty"`
	KiroCredits              int    `json:"kiro_credits,omitempty"`
	KiroCreditUnit           string `json:"kiro_credit_unit,omitempty"`
}

// ── Kiro model catalog entry (OmniRoute RegistryModel equivalent) ──────────

// KiroModel describes a model available through Kiro upstream.
type KiroModel struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	ContextLength   int    `json:"contextLength,omitempty"`
	MaxOutputTokens int    `json:"maxOutputTokens,omitempty"`
}

// awsEventStreamFrame is kept for backward compat with parser.go internals.
type awsEventStreamFrame struct {
	totalLength   uint32
	headersLength uint32
	headers       []byte
	payload       []byte
}
