package rtk

import "strings"

// Termination prompt — anti-loop contract injected into system message.
// Mirrors VansRouter open-sse/rtk/terminationPrompt.js TERMINATION_PROMPT.
const TerminationPrompt = `When you have gathered sufficient information to answer the request, STOP calling tools and provide your final answer. Do not call a tool with the same arguments more than once. If a previous attempt returned the same result, change strategy or summarize with available data. Plan briefly (1-3 steps max), then ACT immediately. Do NOT restate your plan — if you have decided what to do, do it now. If you catch yourself repeating the same intention, STOP and give your answer with current knowledge.`

// Tool protocol prompt — injected for providers that misuse tool names.
// Mirrors VansRouter TOOL_PROTOCOL_PROMPT.
const ToolProtocolPrompt = `Tool protocol: call tools only through the structured tool_call mechanism. Use tool names exactly as listed; do not add prefixes, namespaces, dots, or concatenate words. Never invent tool names.`

// InjectTerminationPrompt injects TerminationPrompt into body's system message.
// Supports OpenAI messages[], OpenAI Responses instructions/input[], and Kiro systemPrompt.
// Idempotent: skips if already present. Returns true when injected.
func InjectTerminationPrompt(body map[string]any) bool {
	return injectIdempotent(body, TerminationPrompt)
}

// InjectToolProtocolPrompt injects ToolProtocolPrompt (optionally with tool names) into
// body's system message. toolNames is deduplicated and capped at 80. Idempotent.
// Returns true when injected.
func InjectToolProtocolPrompt(body map[string]any, toolNames []string) bool {
	prompt := buildToolProtocolPrompt(toolNames)
	return injectIdempotent(body, prompt)
}

func buildToolProtocolPrompt(toolNames []string) string {
	if len(toolNames) == 0 {
		return ToolProtocolPrompt
	}
	seen := make(map[string]bool, len(toolNames))
	var unique []string
	for _, n := range toolNames {
		n = strings.TrimSpace(n)
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		unique = append(unique, n)
		if len(unique) == 80 {
			break
		}
	}
	if len(unique) == 0 {
		return ToolProtocolPrompt
	}
	return ToolProtocolPrompt + " Valid tool names: " + strings.Join(unique, ", ") + "."
}

// injectIdempotent injects prompt into body using the right path for its format.
// Skips if prompt is already present. Returns true when the prompt was injected.
func injectIdempotent(body map[string]any, prompt string) bool {
	if body == nil || prompt == "" {
		return false
	}

	// Kiro: systemPrompt top-level string
	if _, isKiro := body["conversationState"]; isKiro {
		existing, _ := body["systemPrompt"].(string)
		if strings.Contains(existing, prompt) {
			return false
		}
		if existing != "" {
			body["systemPrompt"] = existing + injectSep + prompt
		} else {
			body["systemPrompt"] = prompt
		}
		return true
	}

	// OpenAI Responses: top-level instructions string
	if inst, ok := body["instructions"].(string); ok {
		if strings.Contains(inst, prompt) {
			return false
		}
		if inst != "" {
			body["instructions"] = inst + injectSep + prompt
		} else {
			body["instructions"] = prompt
		}
		return true
	}

	// OpenAI messages[] or input[]
	var arr []any
	arrKey := ""
	if msgs, ok := body["messages"].([]any); ok {
		arr = msgs
		arrKey = "messages"
	} else if inp, ok := body["input"].([]any); ok {
		arr = inp
		arrKey = "input"
	}
	if arr == nil {
		return false
	}

	for _, item := range arr {
		msg, ok := item.(map[string]any)
		if !ok {
			continue
		}
		role, _ := msg["role"].(string)
		if role != "system" && role != "developer" {
			continue
		}
		if idempotentContains(msg["content"], prompt) {
			return false
		}
		appendToOpenAIMessage(msg, prompt)
		return true
	}

	// No system message — prepend one
	newMsg := map[string]any{"role": "system", "content": prompt}
	body[arrKey] = append([]any{newMsg}, arr...)
	return true
}
