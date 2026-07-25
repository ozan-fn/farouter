package kiro

import "strings"

// ── Kiro service headers (OmniRoute getKiroServiceHeaders) ─────────────────

const (
	KIRO_STREAMING_TARGET = "AmazonCodeWhispererStreamingService.GenerateAssistantResponse"
	KIRO_SDK_USER_AGENT   = "AWS-SDK-JS/3.0.0 kiro-ide/1.0.0"
	KIRO_AMZ_USER_AGENT   = "aws-sdk-js/3.0.0 kiro-ide/1.0.0"

	KIRO_EXTERNAL_IDP_AUTH_METHOD      = "external_idp"
	KIRO_EXTERNAL_IDP_TOKEN_TYPE_HEADER = "TokenType"
	KIRO_EXTERNAL_IDP_TOKEN_TYPE_VALUE  = "EXTERNAL_IDP"
)

// GetKiroServiceHeaders returns the standard headers for the Kiro streaming API.
func GetKiroServiceHeaders() map[string]string {
	return map[string]string{
		"Content-Type":  "application/json",
		"Accept":        "application/vnd.amazon.eventstream",
		"X-Amz-Target":  KIRO_STREAMING_TARGET,
		"User-Agent":    KIRO_SDK_USER_AGENT,
		"X-Amz-User-Agent": KIRO_AMZ_USER_AGENT,
	}
}

// ── Model catalog (OmniRoute kiroProvider.models) ──────────────────────────

var KnownModels = []KiroModel{
	{ID: "claude-sonnet-5", Name: "Claude Sonnet 5", ContextLength: 1000000, MaxOutputTokens: 128000},
	{ID: "claude-sonnet-4.5", Name: "Claude Sonnet 4.5", ContextLength: 200000, MaxOutputTokens: 64000},
	{ID: "claude-haiku-4.5", Name: "Claude Haiku 4.5", ContextLength: 200000, MaxOutputTokens: 64000},
	{ID: "deepseek-3.2", Name: "DeepSeek V3.2"},
	{ID: "minimax-m2.5", Name: "MiniMax M2.5"},
	{ID: "minimax-m2.1", Name: "MiniMax M2.1"},
	{ID: "glm-5", Name: "GLM-5"},
	{ID: "qwen3-coder-next", Name: "Qwen3 Coder Next"},
	{ID: "gpt-5.6-sol", Name: "GPT-5.6 Sol", ContextLength: 272000, MaxOutputTokens: 128000},
	{ID: "gpt-5.6-terra", Name: "GPT-5.6 Terra", ContextLength: 272000, MaxOutputTokens: 128000},
	{ID: "gpt-5.6-luna", Name: "GPT-5.6 Luna", ContextLength: 272000, MaxOutputTokens: 128000},
}

// IsKnownModel returns true if the model ID is in the Kiro upstream catalog.
func IsKnownModel(id string) bool {
	for _, m := range KnownModels {
		if m.ID == id {
			return true
		}
	}
	return false
}

// ── Adaptive thinking allowlist (OmniRoute adaptiveThinking.ts) ────────────

// KiroAdaptiveThinkingModels is the set of models that accept
// additionalModelRequestFields for adaptive thinking on Kiro.
// Only claude-sonnet-5 is confirmed; claude-sonnet-4.5 and claude-haiku-4.5
// reject it with upstream 400.
var KiroAdaptiveThinkingModels = map[string]bool{
	"claude-sonnet-5": true,
}

// SupportsKiroAdaptiveThinking reports whether the normalized model ID
// accepts the additionalModelRequestFields adaptive thinking envelope.
func SupportsKiroAdaptiveThinking(model string) bool {
	return KiroAdaptiveThinkingModels[model]
}

// ── Thinking effort levels (OmniRoute openai-to-kiro.ts) ───────────────────

var kiroEffortLevels = map[string]bool{
	"low": true, "medium": true, "high": true, "xhigh": true, "max": true,
}

// NormalizeEffort maps an OpenAI/Anthropic reasoning effort string to a Kiro
// effort level, or "" when no reasoning was requested.
func NormalizeEffort(raw string) string {
	switch strings.ToLower(raw) {
	case "none", "off", "disabled", "":
		return ""
	case "max":
		return "xhigh"
	case "low", "medium", "high", "xhigh":
		return strings.ToLower(raw)
	}
	return ""
}

// ThinkingLengthForEffort returns a soft <max_thinking_length> budget per effort level.
// Clamped to 32000 per Kiro upstream maximum.
func ThinkingLengthForEffort(effort string) int {
	switch effort {
	case "max", "xhigh", "high":
		return 32000
	case "medium":
		return 16000
	default:
		return 8000
	}
}

// ── Region resolution (OmniRoute kiroRegion.ts) ────────────────────────────

var KIRO_PROFILE_REGIONS = []string{"us-east-1", "eu-central-1"}

// KiroRuntimeHost returns the CodeWhisperer/Amazon Q runtime host for a region.
func KiroRuntimeHost(region string) string {
	if region == "us-east-1" {
		return "https://codewhisperer.us-east-1.amazonaws.com"
	}
	return "https://q." + region + ".amazonaws.com"
}

// RegionFromProfileArn extracts the region from a Kiro profile ARN.
func RegionFromProfileArn(profileArn string) string {
	if profileArn == "" {
		return ""
	}
	lower := strings.ToLower(profileArn)
	prefix := "arn:aws:codewhisperer:"
	if !strings.HasPrefix(lower, prefix) {
		return ""
	}
	rest := lower[len(prefix):]
	idx := strings.Index(rest, ":")
	if idx < 0 {
		return ""
	}
	return rest[:idx]
}

// isExternalIdpAuthMethod reports whether the auth method is External IdP (enterprise SSO).
func isExternalIdpAuthMethod(authMethod string) bool {
	return strings.ToLower(strings.TrimSpace(authMethod)) == KIRO_EXTERNAL_IDP_AUTH_METHOD
}

// ResolveRuntimeRegion resolves the runtime region for Kiro API calls.
// Priority: profileArn region > stored region (if valid profile region) > us-east-1.
func ResolveRuntimeRegion(storedRegion, profileArn string) string {
	if fromArn := RegionFromProfileArn(profileArn); fromArn != "" {
		return fromArn
	}
	stored := strings.ToLower(strings.TrimSpace(storedRegion))
	if stored != "" {
		for _, r := range KIRO_PROFILE_REGIONS {
			if stored == r {
				return stored
			}
		}
	}
	return "us-east-1"
}
