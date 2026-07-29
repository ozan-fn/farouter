package kiro

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"strings"
	"sync/atomic"
	"time"
)

const SSEDone = "data: [DONE]\n\n"

var SSEHeadersCORS = map[string]string{
	"Content-Type":           "text/event-stream",
	"Cache-Control":         "no-cache",
	"Connection":            "keep-alive",
	"Access-Control-Allow-Origin": "*",
}

type ssePassthroughState struct {
	usage                map[string]any
	totalContentLength   int
	accumulatedContent   string
	accumulatedThinking  string
	ttftAt               time.Time
	sseLineCount         int64
	streamDoneSent       atomic.Bool
	responseID           string
	created              int64
	model                string
}

func ParseSSELine(line string) (map[string]any, bool) {
	if line == "" {
		return nil, false
	}
	if len(line) < 5 || line[0] != 'd' {
		return nil, false
	}
	data := strings.TrimSpace(line[5:])
	if data == "[DONE]" {
		return nil, true
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(data), &parsed); err != nil {
		return nil, false
	}
	return parsed, false
}

func hasValuableContent(chunk map[string]any) bool {
	choices, _ := chunk["choices"].([]any)
	if len(choices) == 0 {
		return true
	}
	choice, _ := choices[0].(map[string]any)
	delta, _ := choice["delta"].(map[string]any)
	if delta == nil {
		return true
	}
	content, _ := delta["content"].(string)
	reasoning, _ := delta["reasoning_content"].(string)
	toolCalls, _ := delta["tool_calls"].([]any)
	finishReason, _ := choice["finish_reason"].(string)
	role, _ := delta["role"].(string)

	return (content != "") || (reasoning != "") || (len(toolCalls) > 0) || (finishReason != "") || (role != "")
}

func fixInvalidID(parsed map[string]any) (fixed bool, upstreamID string) {
	id, _ := parsed["id"].(string)
	if id != "" && id != "chat" && id != "completion" && len(id) >= 8 {
		return false, id
	}
	newID := fmt.Sprintf("chatcmpl-%d", time.Now().UnixMilli())
	parsed["id"] = newID
	return true, newID
}

func formatSSE(data map[string]any) string {
	if data == nil {
		return "data: null\n\n"
	}
	cleaned := cleanUsagePayload(data)
	b, _ := json.Marshal(cleaned)
	return fmt.Sprintf("data: %s\n\n", b)
}

func cleanUsagePayload(data map[string]any) map[string]any {
	if data == nil {
		return nil
	}
	if u, ok := data["usage"]; ok && u == nil {
		cleaned := make(map[string]any, len(data)-1)
		for k, v := range data {
			if k != "usage" {
				cleaned[k] = v
			}
		}
		return cleaned
	}
	return data
}

func createPassthroughTransform(r io.Reader, sc *StreamController, model string) io.Reader {
	pr, pw := io.Pipe()
	state := &ssePassthroughState{
		responseID: fmt.Sprintf("chatcmpl-%d", time.Now().UnixMilli()),
		created:    time.Now().Unix(),
		model:      model,
	}

	go func() {
		defer pw.Close()
		scanner := bufio.NewScanner(r)
		scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

		for scanner.Scan() {
			if !sc.IsConnected() {
				return
			}

			line := scanner.Text()
			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				continue
			}

			atomic.AddInt64(&state.sseLineCount, 1)

			parsed, isDone := ParseSSELine(trimmed)
			if isDone {
				continue
			}
			if parsed == nil {
				if strings.HasPrefix(trimmed, "data:") {
					output := "data: " + strings.TrimSpace(trimmed[5:]) + "\n"
					pw.Write([]byte(output))
				} else {
					pw.Write([]byte(trimmed + "\n"))
				}
				continue
			}

			if state.ttftAt.IsZero() {
				state.ttftAt = time.Now()
			}

			fixed, upstreamID := fixInvalidID(parsed)
			if !fixed && state.responseID == "" {
				// First valid upstream ID → preserve it (AIClient2API style)
				state.responseID = upstreamID
			}
			if state.responseID != "" {
				parsed["id"] = state.responseID
			}

			if choices, ok := parsed["choices"].([]any); ok {
				if parsed["object"] == nil || parsed["object"] == "" {
					parsed["object"] = "chat.completion.chunk"
				}
				if parsed["created"] == nil {
					parsed["created"] = state.created
				}

				for _, c := range choices {
					choice, _ := c.(map[string]any)
					if choice == nil {
						continue
					}
					delete(choice, "prompt_filter_results")
					delete(choice, "content_filter_results")

					delta, _ := choice["delta"].(map[string]any)
					if delta != nil {
						if tc, ok := delta["tool_calls"].([]any); ok && len(tc) == 0 {
							delete(delta, "tool_calls")
						}
					}

					delta, _ = choice["delta"].(map[string]any)
					if delta != nil {
						if content, _ := delta["content"].(string); content != "" {
							state.totalContentLength += len(content)
							state.accumulatedContent += content
						}
						if reasoning, _ := delta["reasoning_content"].(string); reasoning != "" {
							state.totalContentLength += len(reasoning)
							state.accumulatedThinking += reasoning
						}
					}
				}
			}

			if !hasValuableContent(parsed) {
				continue
			}

			usage := extractUsageFromChunk(parsed)
			if usage != nil {
				state.usage = mergeUsage(state.usage, usage)
				if GlobalTokenCallback != nil && usage != nil {
					if prompt, ok := usage["prompt_tokens"].(int); ok {
						if completion, ok := usage["completion_tokens"].(int); ok {
							GlobalTokenCallback(int64(prompt), int64(completion))
						}
					}
				}
			}

			finishReason, _ := parsed["choices"].([]any)
			var fr string
			if len(finishReason) > 0 {
				if frc, ok := finishReason[0].(map[string]any); ok {
					fr, _ = frc["finish_reason"].(string)
				}
			}

			if fr != "" {
				if !hasValidUsage(parsed) {
					estimated := estimateUsage(state.totalContentLength)
					parsed["usage"] = estimated
					state.usage = estimated
				} else if state.usage != nil {
					parsed["usage"] = filterUsage(state.usage)
				}
			}

			output := formatSSE(parsed)
			pw.Write([]byte(output))
		}

		if err := scanner.Err(); err != nil {
			log.Printf("[kiro] ssepipe scanner error: %v | lines=%d", err, atomic.LoadInt64(&state.sseLineCount))
			if !state.streamDoneSent.Load() {
				writeStreamError(pw, 502, "upstream stream error: "+err.Error())
				pw.Write([]byte(SSEDone))
				state.streamDoneSent.Store(true)
			}
			return
		}

		if state.totalContentLength > 0 {
			if !hasValidUsageRaw(state.usage) {
				state.usage = estimateUsage(state.totalContentLength)
			}
		}

		if !state.streamDoneSent.Load() {
			output := SSEDone
			pw.Write([]byte(output))
			state.streamDoneSent.Store(true)
		}
	}()

	return pr
}

func hasValidUsage(data map[string]any) bool {
	usage, _ := data["usage"].(map[string]any)
	return hasValidUsageRaw(usage)
}

func hasValidUsageRaw(usage map[string]any) bool {
	if usage == nil {
		return false
	}
	pt, _ := usage["prompt_tokens"].(float64)
	ct, _ := usage["completion_tokens"].(float64)
	return int(pt) > 0 || int(ct) > 0
}

func extractUsageFromChunk(chunk map[string]any) map[string]any {
	raw, _ := chunk["usage"].(map[string]any)
	if raw == nil {
		return nil
	}
	u := make(map[string]any)
	if v, ok := raw["prompt_tokens"].(float64); ok {
		u["prompt_tokens"] = int(v)
	}
	if v, ok := raw["completion_tokens"].(float64); ok {
		u["completion_tokens"] = int(v)
	}
	if v, ok := raw["total_tokens"].(float64); ok {
		u["total_tokens"] = int(v)
	}
	if len(u) == 0 {
		if v, ok := raw["prompt_tokens"].(int); ok {
			u["prompt_tokens"] = v
		}
		if v, ok := raw["completion_tokens"].(int); ok {
			u["completion_tokens"] = v
		}
		if v, ok := raw["total_tokens"].(int); ok {
			u["total_tokens"] = v
		}
	}
	if len(u) == 0 {
		return nil
	}
	return u
}

func mergeUsage(a, b map[string]any) map[string]any {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	r := make(map[string]any)
	for k, v := range a {
		r[k] = v
	}
	for k, v := range b {
		if av, ok := r[k].(int); ok {
			if bv, ok := v.(int); ok {
				r[k] = av + bv
			}
		} else {
			r[k] = v
		}
	}
	return r
}

func estimateUsage(contentLength int) map[string]any {
	prompt := 0
	completion := contentLength / 4
	if completion < 1 {
		completion = 1
	}
	return map[string]any{
		"prompt_tokens":     prompt,
		"completion_tokens": completion,
		"total_tokens":      prompt + completion,
	}
}

func filterUsage(usage map[string]any) map[string]any {
	if usage == nil {
		return nil
	}
	r := make(map[string]any)
	if v, ok := usage["prompt_tokens"]; ok {
		r["prompt_tokens"] = v
	}
	if v, ok := usage["completion_tokens"]; ok {
		r["completion_tokens"] = v
	}
	if v, ok := usage["total_tokens"]; ok {
		r["total_tokens"] = v
	}
	if len(r) == 0 {
		return nil
	}
	return r
}
