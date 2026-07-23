package kiro

import (
	"encoding/json"
	"strings"
	"sync"
	"time"
)

// sessionStore menyimpan sessionStart (msg0) per conversationId
// persis seperti kiroSessionReplay.js di 9router
var (
	sessionMu    sync.Mutex
	sessionStore = map[string]sessionEntry{}
)

type sessionEntry struct {
	sessionStart        map[string]any
	modelID             string
	systemPrompt        string
	continuationID      string
	lastUsed            time.Time
}

const sessionTTL = 30 * time.Minute

func init() {
	go func() {
		for range time.Tick(5 * time.Minute) {
			sessionMu.Lock()
			now := time.Now()
			for k, v := range sessionStore {
				if now.Sub(v.lastUsed) > sessionTTL {
					delete(sessionStore, k)
				}
			}
			sessionMu.Unlock()
		}
	}()
}

// GetOrCreateContinuationID returns stable agentContinuationId per session
func GetOrCreateContinuationID(conversationID string, newID func() string) string {
	if conversationID == "" {
		return newID()
	}
	sessionMu.Lock()
	defer sessionMu.Unlock()
	entry := sessionStore[conversationID]
	if entry.continuationID == "" {
		entry.continuationID = newID()
		entry.lastUsed = time.Now()
		sessionStore[conversationID] = entry
	}
	return entry.continuationID
}
// - First turn: freeze msg0 with contentPrefix, current turn gets currentContentPrefix only
// - Subsequent turns: replay frozen msg0, inject currentContentPrefix into current
func applySessionReplay(conversationID, modelID, systemPrompt, contentPrefix, currentTimeContext string, history []map[string]any, currentMessage map[string]any) ([]map[string]any, map[string]any) {
	if conversationID == "" {
		// Ephemeral — just prefix current
		prefixUserMessage(currentMessage, contentPrefix, modelID)
		return history, currentMessage
	}

	sessionMu.Lock()
	existing, found := sessionStore[conversationID]
	sessionMu.Unlock()

	// Deep clone to avoid mutation
	history = cloneMaps(history)
	currentMessage = cloneMap(currentMessage)

	if found && existing.modelID == modelID && existing.systemPrompt == systemPrompt {
		// Replay: replace first user message in history with frozen msg0
		sessionMu.Lock()
		existing.lastUsed = time.Now()
		sessionStore[conversationID] = existing
		sessionMu.Unlock()

		firstUserIdx := findFirstUserIndex(history)
		sessionStart := cloneMap(existing.sessionStart)
		ensureModelID(sessionStart, modelID)
		if firstUserIdx >= 0 {
			history[firstUserIdx] = sessionStart
		} else {
			history = append([]map[string]any{sessionStart}, history...)
		}
		// Current message gets only currentTimeContext
		prefixUserMessage(currentMessage, currentTimeContext, modelID)
	} else {
		// First turn: prefix first user message in history with contentPrefix
		firstUserIdx := findFirstUserIndex(history)
		if firstUserIdx >= 0 {
			prefixUserMessage(history[firstUserIdx], contentPrefix, modelID)
			sessionStart := cloneMap(history[firstUserIdx])
			sessionMu.Lock()
			sessionStore[conversationID] = sessionEntry{
				sessionStart: sessionStart,
				modelID:      modelID,
				systemPrompt: systemPrompt,
				lastUsed:     time.Now(),
			}
			sessionMu.Unlock()
			prefixUserMessage(currentMessage, currentTimeContext, modelID)
		} else {
			// No history — prefix currentMessage with full contentPrefix
			prefixUserMessage(currentMessage, contentPrefix, modelID)
			sessionStart := cloneMap(currentMessage)
			sessionMu.Lock()
			sessionStore[conversationID] = sessionEntry{
				sessionStart: sessionStart,
				modelID:      modelID,
				systemPrompt: systemPrompt,
				lastUsed:     time.Now(),
			}
			sessionMu.Unlock()
		}
	}

	ensureHistoryModelIDs(history, modelID)
	return history, currentMessage
}

func findFirstUserIndex(history []map[string]any) int {
	for i, h := range history {
		if _, ok := h["userInputMessage"]; ok {
			return i
		}
	}
	return -1
}

func prefixUserMessage(msg map[string]any, prefix, modelID string) {
	uim, ok := msg["userInputMessage"].(map[string]any)
	if !ok {
		return
	}
	ensureModelID(msg, modelID)
	if prefix == "" {
		return
	}
	existing, _ := uim["content"].(string)
	if existing != "" {
		uim["content"] = prefix + "\n\n" + existing
	} else {
		uim["content"] = prefix
	}
}

func ensureModelID(msg map[string]any, modelID string) {
	uim, ok := msg["userInputMessage"].(map[string]any)
	if !ok {
		return
	}
	if _, ok := uim["modelId"]; !ok {
		uim["modelId"] = modelID
	}
}

func ensureHistoryModelIDs(history []map[string]any, modelID string) {
	for _, h := range history {
		ensureModelID(h, modelID)
	}
}

func cloneMap(m map[string]any) map[string]any {
	b, _ := json.Marshal(m)
	var out map[string]any
	json.Unmarshal(b, &out)
	return out
}

func cloneMaps(ms []map[string]any) []map[string]any {
	b, _ := json.Marshal(ms)
	var out []map[string]any
	json.Unmarshal(b, &out)
	return out
}

// reconcileOrphanedToolResults mirrors reconcileOrphanedToolResults from 9router
func reconcileOrphanedToolResults(history []map[string]any, currentMessage map[string]any) {
	// Phase 1: collect valid toolUseIds from assistant messages
	validIDs := map[string]bool{}
	for _, h := range history {
		arm, ok := h["assistantResponseMessage"].(map[string]any)
		if !ok {
			continue
		}
		toolUses, _ := arm["toolUses"].([]any)
		for _, tu := range toolUses {
			if m, ok := tu.(map[string]any); ok {
				if id, ok := m["toolUseId"].(string); ok && id != "" {
					validIDs[id] = true
				}
			}
		}
	}

	// Phase 2: salvage orphaned toolResults as text
	carriers := append(history, currentMessage)
	for _, item := range carriers {
		uim, ok := item["userInputMessage"].(map[string]any)
		if !ok {
			continue
		}
		ctx, ok := uim["userInputMessageContext"].(map[string]any)
		if !ok {
			continue
		}
		toolResults, _ := ctx["toolResults"].([]any)
		if len(toolResults) == 0 {
			continue
		}

		var kept []any
		var salvaged []string
		for _, tr := range toolResults {
			m, ok := tr.(map[string]any)
			if !ok {
				continue
			}
			id, _ := m["toolUseId"].(string)
			if validIDs[id] {
				kept = append(kept, tr)
			} else {
				salvaged = append(salvaged, toolResultToTextFromMap(m["content"]))
			}
		}

		if len(salvaged) == 0 {
			continue
		}

		extra := strings.Join(salvaged, "\n")
		existing, _ := uim["content"].(string)
		if existing != "" {
			uim["content"] = existing + "\n\n" + extra
		} else {
			uim["content"] = extra
		}

		ctx["toolResults"] = kept
		if len(kept) == 0 {
			if tools, ok := ctx["tools"]; !ok || tools == nil {
				delete(uim, "userInputMessageContext")
			} else {
				ctx["toolResults"] = nil
			}
		}
	}
}

func toolResultToTextFromMap(content any) string {
	switch v := content.(type) {
	case string:
		return "[Tool result: " + v + "]"
	case []any:
		var parts []string
		for _, c := range v {
			if m, ok := c.(map[string]any); ok {
				if t, ok := m["text"].(string); ok {
					parts = append(parts, t)
				}
			} else if s, ok := c.(string); ok {
				parts = append(parts, s)
			}
		}
		return "[Tool result: " + strings.Join(parts, "\n") + "]"
	}
	return "[Tool result:]"
}
