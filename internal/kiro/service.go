package kiro

// VansRouter equivalents:
//   RefreshCredentials → open-sse/executors/kiro.js refreshCredentials()
//   validateExternalIdpTokenEndpoint → open-sse/services/kiroExternalIdp.ts
//   BuildAndExecuteRequest → open-sse/executors/kiro.js execute() + base.js execute()

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// RefreshCredentials — VansRouter: kiro.js refreshCredentials()
// Orchestrates Kiro credential refresh. Delegates token dispatch to token.go RefreshToken.
// Uses token.go's RefreshToken dispatch (AWS SSO OIDC, social, External IdP).
func RefreshCredentials(ctx context.Context, creds Credentials) (*Credentials, error) {
	if creds.PSD.AuthMethod == "api_key" {
		return nil, nil
	}
	if creds.RefreshToken == "" {
		return nil, nil
	}

	result, err := RefreshToken(ctx, creds.RefreshToken, creds.PSD)
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

// VansRouter: kiro.js refreshCredentials() — External IdP token refresh
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

// validateExternalIdpTokenEndpoint — VansRouter: kiroExternalIdp.ts validateEndpoint
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

// allowedIdpHostSuffixes — VansRouter: kiroExternalIdp.ts allowed host list
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

// BuildAndExecuteRequest — VansRouter: kiro.js execute() + profile resolution
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
