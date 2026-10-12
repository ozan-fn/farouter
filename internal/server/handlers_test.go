package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHandleVerify(t *testing.T) {
	t.Run("no token", func(t *testing.T) {
		rec := httptest.NewRecorder()
		handleVerify(rec, httptest.NewRequest(http.MethodGet, "/api/verify", nil))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", rec.Code)
		}
		var body map[string]any
		json.Unmarshal(rec.Body.Bytes(), &body)
		if body["ok"] != false {
			t.Errorf("ok = %v, want false", body["ok"])
		}
	})

	t.Run("valid session", func(t *testing.T) {
		const tok = "verify-valid-token"
		sessionsMu.Lock()
		sessions[tok] = sessionEntry{expires: time.Now().Add(time.Hour)}
		sessionsMu.Unlock()
		t.Cleanup(func() {
			sessionsMu.Lock()
			delete(sessions, tok)
			sessionsMu.Unlock()
		})

		req := httptest.NewRequest(http.MethodGet, "/api/verify", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		rec := httptest.NewRecorder()
		handleVerify(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("status = %d, want 200", rec.Code)
		}
		var body map[string]any
		json.Unmarshal(rec.Body.Bytes(), &body)
		if body["ok"] != true {
			t.Errorf("ok = %v, want true", body["ok"])
		}
	})
}

// GET toggles read globals only (no saveConfig), so they are side-effect free.
func TestToggleHandlersGet(t *testing.T) {
	cases := []struct {
		name string
		h    http.HandlerFunc
		key  string
	}{
		{"rtk", handleRTK, "rtkEnabled"},
		{"caveman", handleCaveman, "cavemanLevel"},
		{"ponytail", handlePonytail, "ponytailLevel"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c.h(rec, httptest.NewRequest(http.MethodGet, "/", nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			var body map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if _, ok := body[c.key]; !ok {
				t.Errorf("missing key %q in %v", c.key, body)
			}
		})
	}
}
