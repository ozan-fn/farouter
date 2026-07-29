package kiro

// VansRouter equivalents:
//   RefreshToken    → open-sse/executors/kiro.js refreshCredentials()
//   refreshAWSSSO   → open-sse/services/tokenRefresh.js refreshKiroToken() AWS SSO path
//   refreshSocial   → open-sse/services/tokenRefresh.js refreshKiroToken() social path
//   socialAuthService → open-sse/config/appConstants.js OAUTH_ENDPOINTS

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const (
	socialAuthService     = "https://prod.us-east-1.auth.desktop.kiro.dev"
	TOKEN_EXPIRY_BUFFER_MS = 5 * 60 * 1000 // 5 min buffer before expiry
)

// RefreshToken — VansRouter: kiro.js refreshCredentials() → tokenRefresh.js
func RefreshToken(ctx context.Context, refreshToken string, psd ProviderSpecificData) (*TokenResult, error) {
	if isExternalIdpAuthMethod(psd.AuthMethod) && psd.TokenEndpoint != "" {
		return refreshExternalIdpToken(ctx, refreshToken, psd)
	}
	if psd.ClientID != "" && psd.ClientSecret != "" {
		return refreshAWSSSO(ctx, refreshToken, psd)
	}
	return refreshSocial(ctx, refreshToken)
}

// VansRouter: AWS SSO OIDC token refresh
func refreshAWSSSO(ctx context.Context, refreshToken string, psd ProviderSpecificData) (*TokenResult, error) {
	region := psd.Region
	if region == "" {
		region = "us-east-1"
	}
	endpoint := fmt.Sprintf("https://oidc.%s.amazonaws.com/token", region)
	payload, _ := json.Marshal(map[string]string{
		"clientId":     psd.ClientID,
		"clientSecret": psd.ClientSecret,
		"refreshToken": refreshToken,
		"grantType":    "refresh_token",
	})
	return doRefreshPost(ctx, endpoint, payload)
}

// VansRouter: social auth token refresh
func refreshSocial(ctx context.Context, refreshToken string) (*TokenResult, error) {
	payload, _ := json.Marshal(map[string]string{
		"refreshToken": refreshToken,
	})
	return doRefreshPost(ctx, socialAuthService+"/refreshToken", payload)
}

// isUnrecoverableRefreshError checks if a refresh error is terminal.
// VansRouter ref: tokenRefresh.js isUnrecoverableRefreshError()
func isUnrecoverableRefreshError(result *TokenResult) bool {
	if result == nil {
		return false
	}
	switch result.Error {
	case "unrecoverable_refresh_error", "refresh_token_reused", "invalid_request", "invalid_grant":
		return true
	}
	return false
}

// refreshWithRetry retries a refresh function with exponential backoff.
// VansRouter ref: tokenRefresh.js refreshWithRetry()
func refreshWithRetry(refreshFn func() (*TokenResult, error), maxRetries int) (*TokenResult, error) {
	if maxRetries <= 0 {
		maxRetries = 3
	}
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * time.Second)
		}
		result, err := refreshFn()
		if err == nil && result != nil {
			return result, nil
		}
	}
	return nil, fmt.Errorf("token refresh failed after %d attempts", maxRetries)
}

// doRefreshPost — Go-specific HTTP POST helper
func doRefreshPost(ctx context.Context, url string, payload []byte) (*TokenResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := getHttpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("upstream returned %s", resp.Status)
	}
	var result TokenResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return &result, nil
}
