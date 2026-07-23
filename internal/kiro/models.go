package kiro

import (
	"fmt"
	"strings"
)

const (
	agenticSuffix  = "-agentic"
	thinkingSuffix = "-thinking"

	ThinkingBudgetDefault = 16000

	DefaultProfileArnBuilderID = "arn:aws:codewhisperer:us-east-1:638616132270:profile/AAAACCCCXXXX"
	DefaultProfileArnSocial    = "arn:aws:codewhisperer:us-east-1:699475941385:profile/EHGA3GRVQMUK"

	AutoModel = "claude-sonnet-4.5"

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

REMEMBER: When in doubt, write LESS per operation. Multiple small operations > one large operation.`
)

type ResolvedModel struct {
	Upstream string
	Agentic  bool
	Thinking bool
}

func ResolveModel(model string) ResolvedModel {
	// Strip known prefixes
	model = strings.TrimPrefix(model, "Kafuu/")
	model = strings.TrimPrefix(model, "kr/")

	if model == "auto" || model == "" {
		return ResolvedModel{Upstream: AutoModel}
	}

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
		budget = ThinkingBudgetDefault
	}
	if budget > 32000 {
		budget = 32000
	}
	return fmt.Sprintf("<thinking_mode>enabled</thinking_mode>\n<max_thinking_length>%d</max_thinking_length>", budget)
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
			"thinking":      map[string]any{"type": "adaptive", "display": "summarized"},
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
	}
	return false
}
