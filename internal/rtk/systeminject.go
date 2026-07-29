package rtk

import "strings"

const injectSep = "\n\n"

// InjectOpenAISystemPrompt appends prompt into the system message of an OpenAI-format body.
// Handles: OpenAI Responses instructions string, messages[]/input[] with role=system|developer.
// If no system message exists, prepends one. Mirrors VansRouter systemInject.js injectMessagesSystem.
func InjectOpenAISystemPrompt(body map[string]any, prompt string) {
	if body == nil || prompt == "" {
		return
	}

	// OpenAI Responses API: top-level instructions string field
	if inst, ok := body["instructions"].(string); ok {
		if inst != "" {
			body["instructions"] = inst + injectSep + prompt
		} else {
			body["instructions"] = prompt
		}
		return
	}

	// Resolve messages[] or input[]
	var arr []any
	if msgs, ok := body["messages"].([]any); ok {
		arr = msgs
	} else if inp, ok := body["input"].([]any); ok {
		arr = inp
	}
	if arr == nil {
		return
	}

	// Find existing system/developer message
	for _, item := range arr {
		msg, ok := item.(map[string]any)
		if !ok {
			continue
		}
		role, _ := msg["role"].(string)
		if role != "system" && role != "developer" {
			continue
		}
		appendToOpenAIMessage(msg, prompt)
		return
	}

	// No system message — prepend one. Mutate the slice header in body.
	newMsg := map[string]any{"role": "system", "content": prompt}
	if _, hasMsgs := body["messages"]; hasMsgs {
		body["messages"] = append([]any{newMsg}, arr...)
	} else {
		body["input"] = append([]any{newMsg}, arr...)
	}
}

func appendToOpenAIMessage(msg map[string]any, prompt string) {
	switch c := msg["content"].(type) {
	case string:
		if c != "" {
			msg["content"] = c + injectSep + prompt
		} else {
			msg["content"] = prompt
		}
	case []any:
		// Responses-style parts [{type:"input_text"|"text", text}]
		msg["content"] = append(c, map[string]any{"type": "input_text", "text": prompt})
	default:
		msg["content"] = prompt
	}
}

// InjectSystemPrompt dispatches to the right injector based on body shape.
// Kiro bodies have conversationState; everything else is treated as OpenAI.
func InjectSystemPrompt(body map[string]any, prompt string) {
	if body == nil || prompt == "" {
		return
	}
	if _, isKiro := body["conversationState"]; isKiro {
		injectKiroSystemPromptMap(body, prompt)
		return
	}
	InjectOpenAISystemPrompt(body, prompt)
}

// injectKiroSystemPromptMap is the map[string]any equivalent of injectKiroSystemPrompt in caveman.go.
func injectKiroSystemPromptMap(body map[string]any, prompt string) {
	existing, _ := body["systemPrompt"].(string)
	if existing != "" {
		body["systemPrompt"] = existing + injectSep + prompt
	} else {
		body["systemPrompt"] = prompt
	}
}

// idempotentContains checks whether prompt is already present in a content value
// (string or []any parts), so injectors can skip double-injection.
func idempotentContains(content any, prompt string) bool {
	switch c := content.(type) {
	case string:
		return strings.Contains(c, prompt)
	case []any:
		for _, p := range c {
			if part, ok := p.(map[string]any); ok {
				if t, ok := part["text"].(string); ok && t == prompt {
					return true
				}
			}
		}
	}
	return false
}
