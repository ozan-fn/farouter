package kiro

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// ── Credential refresh orchestration (OmniRoute KiroExecutor.refreshCredentials) ──

// RefreshCredentials orchestrates a Kiro credential refresh. It handles
// the three auth methods: AWS SSO OIDC, social login, and External IdP.
// For External IdP and IdC accounts it also discovers the profile ARN
// when not already set. Returns nil when no refresh is possible.
func RefreshCredentials(ctx context.Context, creds Credentials) (*Credentials, error) {
	if creds.PSD.AuthMethod == "api_key" {
		return nil, nil
	}
	if creds.RefreshToken == "" {
		return nil, nil
	}

	result, err := doTokenRefresh(ctx, creds)
	if err != nil {
		return nil, fmt.Errorf("token refresh: %w", err)
	}
	if result == nil {
		return nil, nil
	}

	newCreds := &Credentials{
		AccessToken:  result.AccessToken,
		RefreshToken: result.RefreshToken,
		ProfileArn:   creds.ProfileArn,
		PSD:          creds.PSD,
	}

	if result.ProfileArn != "" {
		newCreds.ProfileArn = result.ProfileArn
	}

	if newCreds.PSD.ProfileArn == "" && newCreds.ProfileArn != "" {
		newCreds.PSD.ProfileArn = newCreds.ProfileArn
	}

	return newCreds, nil
}

// doTokenRefresh dispatches to the correct refresh flow based on auth method.
func doTokenRefresh(ctx context.Context, creds Credentials) (*TokenResult, error) {
	if isExternalIdpAuthMethod(creds.PSD.AuthMethod) {
		return refreshExternalIdpToken(ctx, creds.RefreshToken, creds.PSD)
	}

	if creds.PSD.ClientID != "" && creds.PSD.ClientSecret != "" {
		return refreshAWSSSOToken(ctx, creds.RefreshToken, creds.PSD)
	}

	return refreshSocialToken(ctx, creds.RefreshToken)
}

func refreshAWSSSOToken(ctx context.Context, refreshToken string, psd ProviderSpecificData) (*TokenResult, error) {
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

	result, err := doPostTokenRefresh(ctx, endpoint, payload, "application/json")
	if err != nil {
		return nil, err
	}

	if result.ProfileArn == "" && psd.ProfileArn != "" {
		result.ProfileArn = psd.ProfileArn
	}
	return result, nil
}

func refreshSocialToken(ctx context.Context, refreshToken string) (*TokenResult, error) {
	payload, _ := json.Marshal(map[string]string{
		"refreshToken": refreshToken,
	})

	result, err := doPostTokenRefresh(ctx, socialAuthService+"/refreshToken", payload, "application/json")
	if err != nil {
		return nil, err
	}
	return result, nil
}

func refreshExternalIdpToken(ctx context.Context, refreshToken string, psd ProviderSpecificData) (*TokenResult, error) {
	tokenEndpoint := psd.TokenEndpoint
	if tokenEndpoint == "" {
		return nil, fmt.Errorf("external_idp: tokenEndpoint is required")
	}

	if err := validateExternalIdpTokenEndpoint(tokenEndpoint); err != nil {
		return nil, err
	}

	clientID := psd.ClientID
	if clientID == "" {
		return nil, fmt.Errorf("external_idp: clientId is required")
	}

	scopes := psd.Scopes
	if scopes == "" {
		scopes = "offline_access"
	}

	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("client_id", clientID)
	form.Set("refresh_token", refreshToken)
	form.Set("scope", scopes)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := getHttpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("external_idp refresh: HTTP %d: %s", resp.StatusCode, string(body))
	}

	var tokenResp struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, err
	}

	result := &TokenResult{
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		ExpiresIn:    tokenResp.ExpiresIn,
	}

	if psd.ProfileArn != "" {
		result.ProfileArn = psd.ProfileArn
	}

	return result, nil
}

func doPostTokenRefresh(ctx context.Context, endpoint string, payload []byte, contentType string) (*TokenResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)

	resp, err := getHttpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token refresh %s: HTTP %d: %s", endpoint, resp.StatusCode, string(body))
	}

	var result TokenResult
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ── External IdP helpers (OmniRoute kiroExternalIdp.ts) ────────────────────

// validateExternalIdpTokenEndpoint validates an External IdP token endpoint URL.
// Requires HTTPS and a host on the allowed IdP suffix list.
func validateExternalIdpTokenEndpoint(rawEndpoint string) error {
	endpoint := strings.TrimSpace(rawEndpoint)
	if endpoint == "" {
		return fmt.Errorf("tokenEndpoint is required for external_idp")
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("tokenEndpoint must be a valid URL: %w", err)
	}
	if parsed.Scheme != "https" {
		return fmt.Errorf("tokenEndpoint must use https")
	}
	host := strings.ToLower(parsed.Hostname())
	allowed := false
	for _, suffix := range allowedIdpHostSuffixes {
		if strings.HasPrefix(suffix, ".") {
			if strings.HasSuffix(host, suffix) {
				allowed = true
				break
			}
		} else {
			if host == suffix {
				allowed = true
				break
			}
		}
	}
	if !allowed {
		return fmt.Errorf("tokenEndpoint host is not an allowed identity provider: %s", host)
	}
	return nil
}

var allowedIdpHostSuffixes = []string{
	"login.microsoftonline.com",
	"login.microsoftonline.us",
	"login.partner.microsoftonline.cn",
	"login.microsoft.com",
	"login.windows.net",
	"sts.windows.net",
	".okta.com",
	".oktapreview.com",
	".okta-emea.com",
	".auth0.com",
	".onelogin.com",
	".pingidentity.com",
	".pingone.com",
	"accounts.google.com",
	"oauth2.googleapis.com",
	".amazoncognito.com",
}

// decodeJwtPayload performs a best-effort base64url JWT payload decode
// without signature verification.
func decodeJwtPayload(jwt string) map[string]any {
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		return nil
	}
	payload := parts[1]
	payload = strings.ReplaceAll(payload, "-", "+")
	payload = strings.ReplaceAll(payload, "_", "/")
	switch len(payload) % 4 {
	case 2:
		payload += "=="
	case 3:
		payload += "="
	}
	decoded, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return nil
	}
	var claims map[string]any
	if err := json.Unmarshal(decoded, &claims); err != nil {
		return nil
	}
	return claims
}

// emailFromExternalIdpToken extracts the login identity from an External IdP
// access token JWT. Checks preferred_username, upn, email claims.
func emailFromExternalIdpToken(accessToken string) string {
	claims := decodeJwtPayload(accessToken)
	if claims == nil {
		return ""
	}
	for _, key := range []string{"email", "preferred_username", "upn"} {
		if v, ok := claims[key].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

// ── Request builder with context ───────────────────────────────────────────

// BuildAndExecuteRequest is the top-level orchestration for a Kiro request.
// It handles credential refresh, profile ARN discovery, payload building,
// and upstream call with failover. Returns the upstream response on success.
func BuildAndExecuteRequest(ctx context.Context, creds Credentials, body []byte) (*http.Response, error) {
	if creds.ProfileArn == "" && creds.PSD.ProfileArn != "" {
		creds.ProfileArn = creds.PSD.ProfileArn
	}

	if creds.ProfileArn == "" && creds.PSD.AuthMethod != "api_key" {
		arn, err := DiscoverProfileArn(ctx, creds.AccessToken, creds.PSD.Region)
		if err == nil && arn != "" {
			creds.ProfileArn = arn
			creds.PSD.ProfileArn = arn
		}
	}

	return sendToKiroCtx(ctx, creds, body)
}
