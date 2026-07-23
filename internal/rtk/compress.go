package rtk

import (
	"strconv"
	"strings"
)

type Stats struct {
	BytesBefore int
	BytesAfter  int
	Hits        []Hit
}

type Hit struct {
	Shape  string
	Filter string
	Saved  int
}

func (s *Stats) Saved() int { return s.BytesBefore - s.BytesAfter }

func CompressMessages(body map[string]any, enabled bool) *Stats {
	if !enabled || body == nil {
		return nil
	}
	if state, ok := body["conversationState"]; ok {
		if stateMap, ok := state.(map[string]any); ok {
			return compressKiroFormat(stateMap)
		}
	}
	msgs := messagesSlice(body)
	if msgs == nil {
		return nil
	}
	stats := &Stats{}
	for _, item := range msgs {
		msg, ok := item.(map[string]any)
		if !ok {
			continue
		}
		role, _ := msg["role"].(string)

		if msg["type"] == "function_call_output" {
			if s, ok := msg["output"].(string); ok {
				msg["output"] = compressText(s, stats, "openai-responses-string")
			} else if arr, ok := msg["output"].([]any); ok {
				for _, part := range arr {
					if p, ok := part.(map[string]any); ok {
						if p["type"] == "input_text" {
							if t, ok := p["text"].(string); ok {
								p["text"] = compressText(t, stats, "openai-responses-array")
							}
						}
					}
				}
			}
			continue
		}

		if role == "tool" {
			if s, ok := msg["content"].(string); ok {
				msg["content"] = compressText(s, stats, "openai-tool")
				continue
			}
			if arr, ok := msg["content"].([]any); ok {
				for _, part := range arr {
					if p, ok := part.(map[string]any); ok && p["type"] == "text" {
						if t, ok := p["text"].(string); ok {
							p["text"] = compressText(t, stats, "openai-tool-array")
						}
					}
				}
				continue
			}
		}

		arr, ok := msg["content"].([]any)
		if !ok {
			continue
		}
		for _, block := range arr {
			b, ok := block.(map[string]any)
			if !ok || b["type"] != "tool_result" {
				continue
			}
			if b["is_error"] == true {
				continue
			}
			if s, ok := b["content"].(string); ok {
				b["content"] = compressText(s, stats, "claude-string")
			} else if parts, ok := b["content"].([]any); ok {
				for _, part := range parts {
					if p, ok := part.(map[string]any); ok && p["type"] == "text" {
						if t, ok := p["text"].(string); ok {
							p["text"] = compressText(t, stats, "claude-array")
						}
					}
				}
			}
		}
	}
	return stats
}

func compressKiroFormat(state map[string]any) *Stats {
	stats := &Stats{}
	var allMessages []any
	if history, ok := state["history"].([]any); ok {
		allMessages = append(allMessages, history...)
	}
	if cur, ok := state["currentMessage"]; ok {
		allMessages = append(allMessages, cur)
	}
	for _, item := range allMessages {
		msg, ok := item.(map[string]any)
		if !ok {
			continue
		}
		uim, ok := msg["userInputMessage"].(map[string]any)
		if !ok {
			continue
		}
		ctx, ok := uim["userInputMessageContext"].(map[string]any)
		if !ok {
			continue
		}
		toolResults, ok := ctx["toolResults"].([]any)
		if !ok {
			continue
		}
		for _, tr := range toolResults {
			t, ok := tr.(map[string]any)
			if !ok {
				continue
			}
			if t["status"] == "error" {
				continue
			}
			parts, ok := t["content"].([]any)
			if !ok {
				continue
			}
			for _, part := range parts {
				p, ok := part.(map[string]any)
				if !ok {
					continue
				}
				if text, ok := p["text"].(string); ok {
					p["text"] = compressText(text, stats, "kiro-tool-result")
				}
			}
		}
	}
	return stats
}

func compressText(text string, stats *Stats, shape string) string {
	bytesIn := len(text)
	stats.BytesBefore += bytesIn
	if bytesIn < minCompressSize || bytesIn > rawCap {
		stats.BytesAfter += bytesIn
		return text
	}
	fn := autoDetect(text)
	if fn == nil {
		stats.BytesAfter += bytesIn
		return text
	}
	out := fn(text)
	if out == "" || len(out) >= bytesIn {
		stats.BytesAfter += bytesIn
		return text
	}
	stats.BytesAfter += len(out)
	stats.Hits = append(stats.Hits, Hit{Shape: shape, Filter: filterName(fn), Saved: bytesIn - len(out)})
	return out
}

func filterName(fn func(string) string) string {
	return "unknown"
}

func FormatRtkLog(stats *Stats) string {
	if stats == nil || len(stats.Hits) == 0 {
		return ""
	}
	saved := stats.Saved()
	pct := 0.0
	if stats.BytesBefore > 0 {
		pct = float64(saved) / float64(stats.BytesBefore) * 100
	}
	seen := map[string]bool{}
	var filters []string
	for _, h := range stats.Hits {
		if !seen[h.Filter] {
			seen[h.Filter] = true
			filters = append(filters, h.Filter)
		}
	}
	return "[RTK] saved " + itoa(saved) + "B / " + itoa(stats.BytesBefore) + "B (" +
		strconv.FormatFloat(pct, 'f', 1, 64) + "%) via [" + strings.Join(filters, ",") +
		"] hits=" + itoa(len(stats.Hits))
}

func messagesSlice(body map[string]any) []any {
	if msgs, ok := body["messages"].([]any); ok {
		return msgs
	}
	if input, ok := body["input"].([]any); ok {
		return input
	}
	return nil
}
