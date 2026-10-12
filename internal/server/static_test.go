package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestParseResetAt(t *testing.T) {
	cases := []struct {
		in   string
		want time.Time
	}{
		{in: "", want: time.Time{}},
		{in: "not-a-time", want: time.Time{}},
		{in: "1700000000", want: time.Unix(1700000000, 0)},
		{in: "2026-01-02T15:04:05Z", want: time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC)},
	}
	for _, c := range cases {
		if got := parseResetAt(c.in); !got.Equal(c.want) {
			t.Errorf("parseResetAt(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestMimeTypeByExt(t *testing.T) {
	cases := map[string]string{
		"index.html": "text/html",
		"app.css":    "text/css",
		"main.js":    "application/javascript",
		"logo.svg":   "image/svg+xml",
		"data.json":  "application/json",
		"pic.png":    "image/png",
		"fav.ico":    "image/x-icon",
		"LICENSE":    "",
	}
	for in, want := range cases {
		if got := mimeTypeByExt(in); got != want {
			t.Errorf("mimeTypeByExt(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAcceptsBrotli(t *testing.T) {
	cases := []struct {
		header string
		want   bool
	}{
		{"", false},
		{"gzip", false},
		{"br", true},
		{"gzip, br", true},
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		if c.header != "" {
			req.Header.Set("Accept-Encoding", c.header)
		}
		if got := acceptsBrotli(req); got != c.want {
			t.Errorf("acceptsBrotli(%q) = %v, want %v", c.header, got, c.want)
		}
	}
}

func TestMustMarshal(t *testing.T) {
	if got := string(mustMarshal(map[string]string{"a": "b"})); got != `{"a":"b"}` {
		t.Errorf("mustMarshal = %q, want %q", got, `{"a":"b"}`)
	}
}

func TestWriteJSONError(t *testing.T) {
	rec := httptest.NewRecorder()
	writeJSONError(rec, "missing model", "invalid_request_error", http.StatusBadRequest)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var body struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if body.Error.Message != "missing model" || body.Error.Type != "invalid_request_error" {
		t.Errorf("error = %+v, want message=missing model type=invalid_request_error", body.Error)
	}
}

func TestHandleModels(t *testing.T) {
	rec := httptest.NewRecorder()
	handleModels(rec, httptest.NewRequest(http.MethodGet, "/v1/models", nil))

	var body struct {
		Object string `json:"object"`
		Data   []struct {
			ID      string `json:"id"`
			Object  string `json:"object"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if body.Object != "list" {
		t.Errorf("object = %q, want list", body.Object)
	}
	if len(body.Data) != 6 {
		t.Fatalf("models = %d, want 6", len(body.Data))
	}
	if body.Data[0].ID != "kr/auto" || body.Data[0].OwnedBy != "kiro" || body.Data[0].Object != "model" {
		t.Errorf("first model = %+v, want {kr/auto model kiro}", body.Data[0])
	}
}

func TestAuthMiddleware(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
	h := authMiddleware(next)

	addSession := func(tok string, expires time.Time) {
		sessionsMu.Lock()
		sessions[tok] = sessionEntry{expires: expires}
		sessionsMu.Unlock()
		t.Cleanup(func() {
			sessionsMu.Lock()
			delete(sessions, tok)
			sessionsMu.Unlock()
		})
	}

	t.Run("no token", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/status", nil))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
		}
	})

	t.Run("valid session cookie", func(t *testing.T) {
		addSession("valid-cookie-token", time.Now().Add(time.Hour))
		req := httptest.NewRequest(http.MethodGet, "/status", nil)
		req.AddCookie(&http.Cookie{Name: "session", Value: "valid-cookie-token"})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
			t.Errorf("got status=%d body=%q, want 200 ok", rec.Code, rec.Body.String())
		}
	})

	t.Run("expired bearer token", func(t *testing.T) {
		addSession("expired-token", time.Now().Add(-time.Hour))
		req := httptest.NewRequest(http.MethodGet, "/status", nil)
		req.Header.Set("Authorization", "Bearer expired-token")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
		}
	})
}
