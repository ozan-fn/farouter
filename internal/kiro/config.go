package kiro

// VansRouter equivalents:
//   GetKiroServiceHeaders  → open-sse/config/appConstants.js + kiro.js buildHeaders()
//   KnownModels            → open-sse/config/providerModels.js + capabilities.js
//   GetOrderedBaseURLs     → open-sse/executors/kiro.js getOrderedBaseUrls()
//   ResolveRuntimeRegion   → open-sse/config/kiroRegion.ts
//   NormalizeEffort        → open-sse/config/kiroConstants.js extractKiroEffortLevel()
//   ThinkingLengthForEffort → open-sse/config/kiroConstants.js effortToBudget()
//   RegionFromProfileArn   → open-sse/config/kiroRegion.ts

import "strings"

// GetKiroServiceHeaders — VansRouter: appConstants.js + kiro.js buildHeaders()
const (
	KIRO_STREAMING_TARGET               = "AmazonCodeWhispererStreamingService.GenerateAssistantResponse"
	KIRO_VERSION                        = "0.11.107"
	KIRO_SDK_VERSION                    = "1.0.34"
	KIRO_EXTERNAL_IDP_AUTH_METHOD       = "external_idp"
	KIRO_EXTERNAL_IDP_TOKEN_TYPE_HEADER = "TokenType"
	KIRO_EXTERNAL_IDP_TOKEN_TYPE_VALUE  = "EXTERNAL_IDP"
)

// buildUserAgent builds the full User-Agent string for a given machineId.
// Format matches Kiro-Go kiro_headers.go buildKiroHeaderValues().
func buildUserAgent(machineId string) string {
	ua := "aws-sdk-js/" + KIRO_SDK_VERSION + " ua/2.1 os/linux lang/js md/nodejs#22.22.0 api/codewhispererstreaming#" + KIRO_SDK_VERSION + " m/E KiroIDE-" + KIRO_VERSION
	if machineId != "" {
		ua += "-" + machineId
	}
	return ua
}

// buildAmzUserAgent builds the x-amz-user-agent string for a given machineId.
func buildAmzUserAgent(machineId string) string {
	ua := "aws-sdk-js/" + KIRO_SDK_VERSION + " KiroIDE-" + KIRO_VERSION
	if machineId != "" {
		ua += "-" + machineId
	}
	return ua
}

// GetKiroServiceHeaders — VansRouter: kiro.js buildHeaders() headers base
// User-Agent is not included here; it is set per-request in BuildKiroHeaders.
func GetKiroServiceHeaders() map[string]string {
	return map[string]string{
		"Content-Type":                "application/json",
		"Accept":                      "application/vnd.amazon.eventstream",
		"X-Amz-Target":                KIRO_STREAMING_TARGET,
		"x-amzn-codewhisperer-optout": "true",
		"x-amzn-kiro-agent-mode":      "vibe",
	}
}

// KnownModels — VansRouter: capabilities.js + providerModels.js
var KnownModels = []KiroModel{
	{ID: "claude-sonnet-5", Name: "Claude Sonnet 5", ContextLength: 1000000, MaxOutputTokens: 128000},
	{ID: "claude-opus-5", Name: "Claude Opus 5", ContextLength: 1000000, MaxOutputTokens: 128000},
	{ID: "claude-opus-4-8", Name: "Claude Opus 4.8", ContextLength: 1000000, MaxOutputTokens: 128000},
	{ID: "claude-opus-4-7", Name: "Claude Opus 4.7", ContextLength: 1000000, MaxOutputTokens: 128000},
	{ID: "claude-opus-4-6", Name: "Claude Opus 4.6", ContextLength: 1000000, MaxOutputTokens: 128000},
	{ID: "claude-opus-4-5", Name: "Claude Opus 4.5", ContextLength: 1000000, MaxOutputTokens: 128000},
	{ID: "claude-opus-4-5-20251101", Name: "Claude Opus 4.5", ContextLength: 1000000, MaxOutputTokens: 128000},
	{ID: "claude-sonnet-4-6", Name: "Claude Sonnet 4.6", ContextLength: 200000, MaxOutputTokens: 64000},
	{ID: "claude-sonnet-4-5", Name: "Claude Sonnet 4.5", ContextLength: 200000, MaxOutputTokens: 64000},
	{ID: "claude-sonnet-4-5-20250929", Name: "Claude Sonnet 4.5", ContextLength: 200000, MaxOutputTokens: 64000},
	{ID: "claude-haiku-4-5", Name: "Claude Haiku 4.5", ContextLength: 200000, MaxOutputTokens: 64000},
	{ID: "claude-haiku-4-5-20251001", Name: "Claude Haiku 4.5", ContextLength: 200000, MaxOutputTokens: 64000},
	{ID: "deepseek-3.2", Name: "DeepSeek V3.2"},
	{ID: "minimax-m2.5", Name: "MiniMax M2.5"},
	{ID: "minimax-m2.1", Name: "MiniMax M2.1"},
	{ID: "glm-5", Name: "GLM-5"},
	{ID: "qwen3-coder-next", Name: "Qwen3 Coder Next"},
	{ID: "gpt-5.6-sol", Name: "GPT-5.6 Sol", ContextLength: 1000000, MaxOutputTokens: 128000},
	{ID: "gpt-5.6-terra", Name: "GPT-5.6 Terra", ContextLength: 1000000, MaxOutputTokens: 128000},
	{ID: "gpt-5.6-luna", Name: "GPT-5.6 Luna", ContextLength: 1000000, MaxOutputTokens: 128000},
}

// IsKnownModel — VansRouter: providerModels.js lookup
func IsKnownModel(id string) bool {
	for _, m := range KnownModels {
		if m.ID == id {
			return true
		}
	}
	return false
}

// NormalizeEffort — VansRouter: kiroConstants.js extractKiroEffortLevel()
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

// ThinkingLengthForEffort — VansRouter: kiroConstants.js effortToBudget()
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

// KIRO_PROFILE_REGIONS — VansRouter: kiroRegion.ts regions
var KIRO_PROFILE_REGIONS = []string{"us-east-1", "eu-central-1"}

// KiroRuntimeHost — VansRouter: kiroRegion.ts runtimeHost()
func KiroRuntimeHost(region string) string {
	if region == "us-east-1" {
		return "https://codewhisperer.us-east-1.amazonaws.com"
	}
	return "https://q." + region + ".amazonaws.com"
}

// RegionFromProfileArn — VansRouter: kiroRegion.ts regionFromProfileArn()
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

// isExternalIdpAuthMethod — VansRouter: auth method check
func isExternalIdpAuthMethod(authMethod string) bool {
	return strings.ToLower(strings.TrimSpace(authMethod)) == KIRO_EXTERNAL_IDP_AUTH_METHOD
}

// GetOrderedBaseURLs — VansRouter: kiro.js getOrderedBaseUrls()
func GetOrderedBaseURLs(creds Credentials, region string) []string {
	authMethod := creds.PSD.AuthMethod
	isCodeWhispererSurface := authMethod == "api_key" || authMethod == "external_idp" || authMethod == "idc"

	var urls []string
	if isCodeWhispererSurface {
		urls = []string{
			regionalizeURL("https://codewhisperer.us-east-1.amazonaws.com/generateAssistantResponse", region),
			regionalizeURL("https://q.us-east-1.amazonaws.com/generateAssistantResponse", region),
			"https://runtime.us-east-1.kiro.dev/generateAssistantResponse",
		}
	} else {
		urls = []string{
			"https://runtime.us-east-1.kiro.dev/generateAssistantResponse",
			regionalizeURL("https://codewhisperer.us-east-1.amazonaws.com/generateAssistantResponse", region),
			regionalizeURL("https://q.us-east-1.amazonaws.com/generateAssistantResponse", region),
		}
	}
	return urls
}

// ResolveRuntimeRegion — VansRouter: kiroRegion.ts resolveRuntimeRegion()
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
