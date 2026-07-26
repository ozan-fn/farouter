package rtk

import (
	"encoding/json"
	"strings"
)

// ProcessToolMessages scans messages array and applies RTK filtering to tool outputs
func ProcessToolMessages(messages []map[string]any) []map[string]any {
	for i := range messages {
		msg := messages[i]

		// Only process tool role messages — user/assistant messages are NOT tool output
		role, _ := msg["role"].(string)
		if role != "tool" {
			continue
		}

		content := extractContent(msg["content"])
		if content == "" {
			continue
		}

		filtered := ProcessOutput(content)
		if filtered != content {
			msg["content"] = filtered
		}
	}

	return messages
}

func extractContent(content any) string {
	switch v := content.(type) {
	case string:
		return v
	case []any:
		var parts []string
		for _, part := range v {
			if m, ok := part.(map[string]any); ok {
				if text, ok := m["text"].(string); ok {
					parts = append(parts, text)
				}
			}
		}
		return strings.Join(parts, "\n")
	default:
		b, _ := json.Marshal(content)
		return string(b)
	}
}
