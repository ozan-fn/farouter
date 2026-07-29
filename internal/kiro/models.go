package kiro

import (
	"fmt"
	"strings"
)

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
	// Pass "auto" through to upstream as-is (Kiro upstream resolves it)
	if model == "auto" {
		return ResolvedModel{Upstream: "auto", Agentic: agentic, Thinking: thinking}
	}

	return ResolvedModel{Upstream: model, Agentic: agentic, Thinking: thinking}
}

func DefaultProfileArn(authMethod string) string {
	if authMethod == "google" || authMethod == "github" {
		return DefaultProfileArnSocial
	}
	return DefaultProfileArnBuilderID
}

func BuildThinkingSystemPrefix(budget int) string {
	if budget <= 0 {
		budget = 20000
	}
	if budget < 1024 {
		budget = 1024
	}
	if budget > 24576 {
		budget = 24576
	}
	return fmt.Sprintf("<thinking_mode>enabled</thinking_mode><max_thinking_length>%d</max_thinking_length>", budget)
}

func IsGPTModel(model string) bool {
	return strings.Contains(strings.ToLower(model), "gpt-5.6")
}

func ResolveThinkingBudget(effort string, model string) int {
	if effort != "" {
		switch strings.ToLower(effort) {
		case "none", "off", "disabled":
			return -1
		case "low":
			return 4000
		case "medium":
			return 8000
		case "high", "xhigh", "max":
			return ThinkingBudgetDefault
		}
	}
	m := strings.ToLower(model)
	if strings.Contains(m, "thinking") || strings.Contains(m, "-reason") {
		return ThinkingBudgetDefault
	}
	return -1
}

func BuildAdditionalModelRequestFields(effort string, model string) map[string]any {
	e := normalizeEffort(effort)
	if e == "" {
		return nil
	}
	if IsGPTModel(model) {
		return map[string]any{
			"reasoning": map[string]any{"effort": e},
		}
	}
	if supportsAdditionalFields(model) {
		return map[string]any{
			"thinking": map[string]any{"type": "adaptive", "display": "summarized"},
			"output_config": map[string]any{"effort": e},
		}
	}
	return nil
}

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

func supportsAdditionalFields(model string) bool {
	m := strings.ToLower(model)
	if strings.Contains(m, "gpt-5.6") {
		return true
	}
	if !strings.Contains(m, "claude") {
		return false
	}
	// Match both old dot format (claude-sonnet-4.5) and new dash format (claude-sonnet-4-5)
	// Extract the version segment (e.g. "4-5" or "4.5") and parse major.minor
	parts := strings.Split(m, "-")
	for _, p := range parts {
		// Dot format: "4.5" → major=4, minor=5
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
		// Dash format: "4-5" → major=4, minor=5
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
