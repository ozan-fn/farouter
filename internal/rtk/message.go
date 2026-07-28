package rtk

import (
	"encoding/json"
	"fmt"
	"log"
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

// ProcessKiroBody applies RTK filtering on the Kiro payload.
// Mirrors VansRouter compressKiroFormat in open-sse/rtk/index.js.
// Fail-open: any panic returns original body untouched.
func ProcessKiroBody(body []byte) []byte {
	result, _ := compressKiroFormat(body)
	if result == nil {
		return body
	}
	return result
}

// CompressKiroBody is like ProcessKiroBody but also returns compression stats.
func CompressKiroBody(body []byte) ([]byte, *Stats) {
	result, stats := compressKiroFormat(body)
	if result == nil {
		return body, stats
	}
	return result, stats
}

func compressKiroFormat(body []byte) (out []byte, stats *Stats) {
	stats = &Stats{}

	defer func() {
		if r := recover(); r != nil {
			log.Printf("[rtk] compressKiroFormat panic — passing through raw output: %v", r)
			out = nil
			stats = nil
		}
	}()

	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, nil
	}

	state, _ := raw["conversationState"].(map[string]any)
	if state == nil {
		return nil, nil
	}

	var allMessages []map[string]any
	if history, ok := state["history"].([]any); ok {
		for _, h := range history {
			if m, ok := h.(map[string]any); ok {
				allMessages = append(allMessages, m)
			}
		}
	}
	if current, ok := state["currentMessage"].(map[string]any); ok {
		allMessages = append(allMessages, current)
	}

	changed := false
	for _, msg := range allMessages {
		uim, _ := msg["userInputMessage"].(map[string]any)
		if uim == nil {
			continue
		}
		ctx, _ := uim["userInputMessageContext"].(map[string]any)
		if ctx == nil {
			continue
		}
		toolResults, _ := ctx["toolResults"].([]any)
		if len(toolResults) == 0 {
			continue
		}
		for _, tr := range toolResults {
			trMap, _ := tr.(map[string]any)
			if trMap == nil {
				continue
			}
			status, _ := trMap["status"].(string)
			if status == "error" {
				continue
			}
			content, _ := trMap["content"].([]any)
			if len(content) == 0 {
				continue
			}
			for _, part := range content {
				partMap, _ := part.(map[string]any)
				if partMap == nil {
					continue
				}
			text, _ := partMap["text"].(string)
			compressed := compressText(text, stats, "kiro-tool-result")
				if compressed != text {
					partMap["text"] = compressed
					changed = true
				}
			}
		}
	}

	if !changed {
		return nil, stats
	}

	b, err := json.Marshal(raw)
	if err != nil {
		return nil, stats
	}
	return b, stats
}

func compressText(text string, stats *Stats, shape string) string {
	bytesIn := len(text)
	stats.BytesBefore += bytesIn

	if bytesIn < MinCompressSize || bytesIn > RawCap {
		stats.BytesAfter += bytesIn
		return text
	}

	p := autoDetectFilter(text)
	if p == nil {
		stats.BytesAfter += bytesIn
		return text
	}

	out := safeApply(p, text)

	if out == "" || len(out) >= bytesIn {
		stats.BytesAfter += bytesIn
		return text
	}

	stats.BytesAfter += len(out)
	stats.Hits = append(stats.Hits, Hit{Shape: shape, Filter: p.Name(), Saved: bytesIn - len(out)})
	return out
}

func FormatRtkLog(stats *Stats) string {
	if stats == nil || len(stats.Hits) == 0 {
		return ""
	}
	saved := stats.BytesBefore - stats.BytesAfter
	seen := make(map[string]bool)
	var filters []string
	for _, h := range stats.Hits {
		if !seen[h.Filter] {
			seen[h.Filter] = true
			filters = append(filters, h.Filter)
		}
	}
	pct := "0.0"
	if stats.BytesBefore > 0 {
		p := float64(saved) * 100 / float64(stats.BytesBefore)
		pct = fmt.Sprintf("%.1f", p)
	}
	fStr := ""
	for i, f := range filters {
		if i > 0 {
			fStr += ","
		}
		fStr += f
	}
	return "[RTK] saved " + itoa(saved) + "B / " + itoa(stats.BytesBefore) +
		"B (" + pct + "%) via [" + fStr + "] hits=" + itoa(len(stats.Hits))
}
