package kiro

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/go-resty/resty/v2"
)

// captureTransport records the request body and answers with a canned token
// (or a canned error status).
type captureTransport struct {
	body   []byte
	hits   int
	status int
}

func (c *captureTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c.hits++
	b, _ := io.ReadAll(r.Body)
	c.body = b

	status := c.status
	if status == 0 {
		status = http.StatusOK
	}
	body := `{"accessToken":"aoaTest","refreshToken":"aorTest","expiresIn":3600}`
	if status != http.StatusOK {
		body = `{"message":"Bad credentials"}`
	}
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    r,
	}, nil
}

func stubHTTP(t *testing.T, cap *captureTransport) {
	t.Helper()
	old := getHttpClient()
	SetHTTPClient(resty.NewWithClient(&http.Client{Transport: cap}))
	t.Cleanup(func() { SetHTTPClient(old) })
}

// A pasted refresh token usually carries a trailing newline or space. Kiro
// answers that with 401 "Bad credentials", so the whitespace must be stripped
// before the token reaches upstream.
func TestRefreshToken_TrimsPastedWhitespace(t *testing.T) {
	for _, tt := range []struct {
		name string
		in   string
	}{
		{"trailing newline", "aorAAAA:signature\n"},
		{"trailing CRLF", "aorAAAA:signature\r\n"},
		{"surrounding spaces", "  aorAAAA:signature  "},
		{"tabs", "\taorAAAA:signature\t"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cap := &captureTransport{}
			stubHTTP(t, cap)

			res, err := RefreshToken(context.Background(), tt.in, ProviderSpecificData{AuthMethod: "imported"})
			if err != nil {
				t.Fatalf("RefreshToken(%q): %v", tt.in, err)
			}
			if res.AccessToken != "aoaTest" {
				t.Errorf("access token = %q, want %q", res.AccessToken, "aoaTest")
			}

			var sent map[string]string
			if err := json.Unmarshal(cap.body, &sent); err != nil {
				t.Fatalf("decode sent body %q: %v", cap.body, err)
			}
			if got := sent["refreshToken"]; got != "aorAAAA:signature" {
				t.Errorf("upstream refreshToken = %q, want %q", got, "aorAAAA:signature")
			}
		})
	}
}

// Only 401 means the token is dead. 403/429 come from upstream pushing back on
// a burst and must not be treated as a dead token.
func TestRefreshToken_ClassifiesFailures(t *testing.T) {
	for _, tt := range []struct {
		status  int
		want    error
		notWant error
	}{
		{http.StatusUnauthorized, ErrTokenRejected, ErrRefreshThrottled},
		{http.StatusForbidden, ErrRefreshThrottled, ErrTokenRejected},
		{http.StatusTooManyRequests, ErrRefreshThrottled, ErrTokenRejected},
	} {
		t.Run(http.StatusText(tt.status), func(t *testing.T) {
			stubHTTP(t, &captureTransport{status: tt.status})

			_, err := RefreshToken(context.Background(), "aorAAAA:signature", ProviderSpecificData{})
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want errors.Is %v", err, tt.want)
			}
			if errors.Is(err, tt.notWant) {
				t.Errorf("err = %v, must not match %v", err, tt.notWant)
			}
		})
	}
}

func TestRefreshToken_EmptyAfterTrim(t *testing.T) {
	cap := &captureTransport{}
	stubHTTP(t, cap)

	if _, err := RefreshToken(context.Background(), " \n\t ", ProviderSpecificData{}); err == nil {
		t.Fatal("expected error for blank refresh token")
	}
	if cap.hits != 0 {
		t.Errorf("upstream calls = %d, want 0", cap.hits)
	}
}
