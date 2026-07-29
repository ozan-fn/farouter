package kiro

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

const socialAuthService = "https://prod.us-east-1.auth.desktop.kiro.dev"

func RefreshToken(ctx context.Context, refreshToken string, psd ProviderSpecificData) (*TokenResult, error) {
	if isExternalIdpAuthMethod(psd.AuthMethod) && psd.TokenEndpoint != "" {
		return refreshExternalIdpToken(ctx, refreshToken, psd)
	}
	if psd.ClientID != "" && psd.ClientSecret != "" {
		return refreshAWSSSO(ctx, refreshToken, psd)
	}
	return refreshSocial(ctx, refreshToken)
}

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

func refreshSocial(ctx context.Context, refreshToken string) (*TokenResult, error) {
	payload, _ := json.Marshal(map[string]string{
		"refreshToken": refreshToken,
	})
	return doRefreshPost(ctx, socialAuthService+"/refreshToken", payload)
}

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
