package kiro

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

var (
	sessionMu    sync.Mutex
	sessionStore = map[string]sessionEntry{}
)

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

func sessionKey(connectionID, conversationID string) string {
	return connectionID + ":" + conversationID
}

// ── helpers ───────────────────────────────────────────────────────────

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

func ensureHistoryModelIDs(history []map[string]any, modelID string) []map[string]any {
	for _, h := range history {
		ensureModelID(h, modelID)
	}
	return history
}

func ensureUserMessageModelID(msg map[string]any, modelID string) map[string]any {
	if msg == nil {
		return msg
	}
	uim, ok := msg["userInputMessage"].(map[string]any)
	if ok && uim != nil {
		if _, has := uim["modelId"]; !has {
			uim["modelId"] = modelID
		}
	}
	return msg
}

func prefixUserMessageCopy(msg map[string]any, prefix, modelID string) map[string]any {
	out := cloneMap(msg)
	if out == nil {
		out = map[string]any{"userInputMessage": map[string]any{"content": ""}}
	}
	prefixUserMessage(out, prefix, modelID)
	return out
}

func cloneMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	b, _ := json.Marshal(m)
	var out map[string]any
	json.Unmarshal(b, &out)
	return out
}

func cloneMaps(ms []map[string]any) []map[string]any {
	if ms == nil {
		return nil
	}
	b, _ := json.Marshal(ms)
	var out []map[string]any
	json.Unmarshal(b, &out)
	return out
}

// ── Session replay (port of VansRouter kiroSessionReplay.js) ──────────

// applySessionReplay preserves Kiro cacheability by freezing the first user
// message (sessionStart) for a session, replaying that exact message as the
// first history user on later turns, and injecting volatile current-time
// context only into the current turn.
func applySessionReplay(connectionID, conversationID, modelID, systemPrompt, contentPrefix, currentContentPrefix string, history []map[string]any, currentMessage map[string]any) ([]map[string]any, map[string]any, bool) {
	key := sessionKey(connectionID, conversationID)
	sessionMu.Lock()
	existing := sessionStore[key]
	sessionMu.Unlock()

	baseHistory := cloneMaps(history)
	baseCurrent := cloneMap(currentMessage)
	if baseCurrent == nil {
		baseCurrent = map[string]any{"userInputMessage": map[string]any{"content": ""}}
	}

	if existing.sessionStart != nil && existing.modelID == modelID && existing.systemPrompt == systemPrompt {
		// Later turn: replay frozen sessionStart as first history entry
		sessionMu.Lock()
		existing.lastUsed = time.Now()
		sessionStore[key] = existing
		sessionMu.Unlock()

		sessionStart := ensureUserMessageModelID(cloneMap(existing.sessionStart), modelID)
		firstUserIndex := findFirstUserIndex(baseHistory)
		if firstUserIndex >= 0 {
			baseHistory[firstUserIndex] = sessionStart
		} else {
			baseHistory = append([]map[string]any{sessionStart}, baseHistory...)
		}
		return ensureHistoryModelIDs(baseHistory, modelID), prefixUserMessageCopy(baseCurrent, currentContentPrefix, modelID), true
	}

	// First turn: save sessionStart (with contentPrefix), currentMessage gets currentContentPrefix
	firstUserIndex := findFirstUserIndex(baseHistory)
	var sessionStart map[string]any
	if firstUserIndex >= 0 {
		sessionStart = prefixUserMessageCopy(baseHistory[firstUserIndex], contentPrefix, modelID)
		baseHistory[firstUserIndex] = cloneMap(sessionStart)
	} else {
		sessionStart = prefixUserMessageCopy(baseCurrent, contentPrefix, modelID)
	}
	nextCurrent := prefixUserMessageCopy(baseCurrent, currentContentPrefix, modelID)

	if conversationID != "" {
		sessionMu.Lock()
		sessionStore[key] = sessionEntry{
			sessionStart: cloneMap(sessionStart),
			modelID:      modelID,
			systemPrompt: systemPrompt,
			lastUsed:     time.Now(),
		}
		// Evict oldest if >5000
		if len(sessionStore) >= 5000 {
			oldest := ""
			var oldestTime time.Time
			for k, v := range sessionStore {
				if oldest == "" || v.lastUsed.Before(oldestTime) {
					oldest = k
					oldestTime = v.lastUsed
				}
			}
			if oldest != "" {
				delete(sessionStore, oldest)
			}
		}
		sessionMu.Unlock()
	}

	return ensureHistoryModelIDs(baseHistory, modelID), nextCurrent, false
}

// ── Orphaned tool results reconciliation ──────────────────────────────

func reconcileOrphanedToolResults(history []map[string]any, currentMessage map[string]any) {
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
				salvaged = append(salvaged, toolResultToTextFromMap(id, m["content"]))
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
			if _, hasTools := ctx["tools"]; !hasTools || ctx["tools"] == nil {
				delete(uim, "userInputMessageContext")
			} else {
				ctx["toolResults"] = nil
			}
		}
	}
}

func toolResultToTextFromMap(toolUseID string, content any) string {
	var txt string
	switch v := content.(type) {
	case string:
		txt = v
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
		txt = strings.Join(parts, "\n")
	}
	if toolUseID != "" {
		return fmt.Sprintf("[Tool Result (%s)]\n%s", toolUseID, txt)
	}
	return fmt.Sprintf("[Tool Result]\n%s", txt)
}
