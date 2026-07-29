package kiro

// VansRouter equivalents:
//   ResolveModel                  → open-sse/config/kiroConstants.js resolveKiroModel()
//   BuildThinkingSystemPrefix     → open-sse/config/kiroConstants.js buildThinkingSystemPrefix()
//   BuildAdditionalModelRequestFields → open-sse/config/kiroConstants.js buildKiroAdditionalModelRequestFields()
//   DefaultProfileArn             → open-sse/config/kiroConstants.js resolveDefaultProfileArn()
//   ResolveThinkingBudget         → open-sse/config/kiroConstants.js resolveKiroThinkingBudget()
//   IsGPTModel                    → open-sse/config/kiroConstants.js inline
//   supportsAdditionalFields      → open-sse/config/kiroConstants.js supportsKiroAdditionalModelRequestFields()

import (
	"fmt"
	"strings"
)

// ResolveModel — VansRouter: kiroConstants.js resolveKiroModel()
func ResolveModel(model string) ResolvedModel {
	model = strings.TrimPrefix(model, "kr/")

	agentic := false
	thinking := false

	if strings.HasSuffix(model, agenticSuffix) {
		agentic = true
		model = model[:len(model)-len(agenticSuffix)]
	}
	if strings.HasSuffix(model, thinkingSuffix) {
		thinking = true
		model = model[:len(model)-len(thinkingSuffix)]
	}

	if model == "" {
		model = AutoModel
	}
	if model == "auto" {
		return ResolvedModel{Upstream: "auto", Agentic: agentic, Thinking: thinking}
	}

	return ResolvedModel{Upstream: model, Agentic: agentic, Thinking: thinking}
}

// DefaultProfileArn — VansRouter: kiroConstants.js resolveDefaultProfileArn()
func DefaultProfileArn(authMethod string) string {
	if authMethod == "google" || authMethod == "github" {
		return DefaultProfileArnSocial
	}
	return DefaultProfileArnBuilderID
}

// BuildThinkingSystemPrefix — VansRouter: kiroConstants.js buildThinkingSystemPrefix()
func BuildThinkingSystemPrefix(budget int) string {
	if budget <= 0 {
		budget = ThinkingBudgetDefault
	}
	if budget < ThinkingBudgetMin {
		budget = ThinkingBudgetMin
	}
	if budget > ThinkingBudgetMax {
		budget = ThinkingBudgetMax
	}
	return fmt.Sprintf("<thinking_mode>enabled</thinking_mode><max_thinking_length>%d</max_thinking_length>", budget)
}

// IsGPTModel — VansRouter: kiroConstants.js inline effort path detection
func IsGPTModel(model string) bool {
	return strings.Contains(strings.ToLower(model), "gpt-5.6")
}

// ResolveThinkingBudget — VansRouter: open-sse/translator/concerns/thinking.js effortToBudget()
// Web-standard effort levels: https://www.anthropic.com/docs
// Maps effort levels to token budgets per Anthropic/Gemini standards
func ResolveThinkingBudget(effort string, model string) int {
	if effort != "" {
		switch strings.ToLower(effort) {
		case "none", "off", "disabled":
			return -1  // Thinking disabled
		case "minimal":
			return 512
		case "low":
			return 1024  // Changed from 4000 to match VansRouter web-standard
		case "medium":
			return 8192  // Changed from 8000 to match VansRouter
		case "high":
			return 24576  // Changed from ThinkingBudgetDefault to match VansRouter
		case "xhigh":
			return 32768  // New: web-standard value
		case "max":
			return 128000  // New: web-standard maximum
		}
	}
	m := strings.ToLower(model)
	if strings.Contains(m, "thinking") || strings.Contains(m, "-reason") {
		return ThinkingBudgetDefault  // 16000
	}
	return -1
}

// containsThinkingModeTag checks if any message in the body contains
// <thinking_mode> tag. VansRouter ref: kiroConstants.js containsThinkingModeTag()
func containsThinkingModeTag(req ChatRequest) bool {
	for _, msg := range req.Messages {
		if msg.Role != RoleSystem && msg.Role != RoleUser {
			continue
		}
		if s, ok := msg.Content.(string); ok {
			if strings.Contains(s, "<thinking_mode>enabled</thinking_mode>") ||
				strings.Contains(s, "<thinking_mode>interleaved</thinking_mode>") {
				return true
			}
		} else if arr, ok := msg.Content.([]any); ok {
			for _, part := range arr {
				if m, ok := part.(map[string]any); ok {
					if txt, ok := m["text"].(string); ok {
						if strings.Contains(txt, "<thinking_mode>enabled</thinking_mode>") ||
							strings.Contains(txt, "<thinking_mode>interleaved</thinking_mode>") {
							return true
						}
					}
				}
			}
		}
	}
	return false
}

// ResolveKiroThinkingBudgetFromBody parses thinking budget from request body,
// headers, and model. VansRouter ref: kiroConstants.js resolveKiroThinkingBudget()
func ResolveKiroThinkingBudgetFromBody(req ChatRequest, model string) *int {
	// Check reasoning_effort
	effort := strings.ToLower(req.ReasoningEffort)
	if effort == "" && req.OutputConfig != nil {
		effort = strings.ToLower(req.OutputConfig.Effort)
	}
	if effort != "" {
		switch effort {
		case "none", "off", "disabled":
			return nil // null = no thinking
		case "low":
			v := 4000
			return &v
		case "medium":
			v := 8000
			return &v
		case "high", "xhigh", "max":
			v := ThinkingBudgetDefault
			return &v
		}
	}

	// Check thinking block
	if req.Thinking != nil {
		switch req.Thinking.Type {
		case "enabled":
			if req.Thinking.BudgetTokens > 0 {
				v := req.Thinking.BudgetTokens
				return &v
			}
			v := ThinkingBudgetDefault
			return &v
		case "adaptive":
			v := ThinkingBudgetDefault
			return &v
		}
	}

	// Check <thinking_mode> tags in messages
	if containsThinkingModeTag(req) {
		v := ThinkingBudgetDefault
		return &v
	}

	// Check model name hints
	m := strings.ToLower(model)
	if strings.Contains(m, "thinking") || strings.Contains(m, "-reason") {
		v := ThinkingBudgetDefault
		return &v
	}

	return nil // no thinking
}

// BuildAdditionalModelRequestFields — VansRouter: kiroConstants.js buildKiroAdditionalModelRequestFields()
func BuildAdditionalModelRequestFields(effort string, model string) map[string]any {
	e := normalizeEffort(effort)
	if e == "" {
		return nil
	}
	if IsGPTModel(model) {
		// VansRouter: reasoning effort field for GPT
		return map[string]any{
			"reasoning": map[string]any{"effort": e},
		}
	}
	if supportsAdditionalFields(model) {
		// VansRouter: output_config + thinking fields for Claude 4.5+
		return map[string]any{
			"thinking":     map[string]any{"type": "adaptive", "display": "summarized"},
			"output_config": map[string]any{"effort": e},
		}
	}
	return nil
}

// normalizeEffort — VansRouter: kiroConstants.js extractKiroEffortLevel()
func normalizeEffort(raw string) string {
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

// supportsAdditionalFields — VansRouter: kiroConstants.js supportsKiroAdditionalModelRequestFields()
func supportsAdditionalFields(model string) bool {
	m := strings.ToLower(model)
	if strings.Contains(m, "gpt-5.6") {
		return true
	}
	if !strings.Contains(m, "claude") {
		return false
	}
	parts := strings.Split(m, "-")
	for _, p := range parts {
		if len(p) >= 3 && p[1] == '.' {
			major := int(p[0] - '0')
			if major > 4 {
				return true
			}
			if major == 4 {
				minor := 0
				fmt.Sscanf(p[2:], "%d", &minor)
				return minor > 5
			}
		}
		if idx := strings.Index(p, "-"); idx > 0 {
			major := 0
			minor := 0
			fmt.Sscanf(p[:idx], "%d", &major)
			fmt.Sscanf(p[idx+1:], "%d", &minor)
			if major > 4 {
				return true
			}
			if major == 4 && minor > 5 {
				return true
			}
		}
	}
	return false
}
