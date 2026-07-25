package kiro

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

const socialAuthService = "https://prod.us-east-1.auth.desktop.kiro.dev"

func RefreshToken(refreshToken string, psd ProviderSpecificData) (*TokenResult, error) {
	if isExternalIdpAuthMethod(psd.AuthMethod) && psd.TokenEndpoint != "" {
		ctx := context.Background()
		return refreshExternalIdpToken(ctx, refreshToken, psd)
	}
	if psd.ClientID != "" && psd.ClientSecret != "" {
		return refreshAWSSSO(refreshToken, psd)
	}
	return refreshSocial(refreshToken)
}

func refreshAWSSSO(refreshToken string, psd ProviderSpecificData) (*TokenResult, error) {
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
	return doRefreshPost(endpoint, payload)
}

func refreshSocial(refreshToken string) (*TokenResult, error) {
	payload, _ := json.Marshal(map[string]string{
		"refreshToken": refreshToken,
	})
	return doRefreshPost(socialAuthService+"/refreshToken", payload)
}

func doRefreshPost(url string, payload []byte) (*TokenResult, error) {
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
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
