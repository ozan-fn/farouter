package kiro

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	requestTimeout    = 300 * time.Second
	defaultHTTPTimeout = 60 * time.Second
)

var httpClient = &http.Client{
	Timeout: requestTimeout,
}

func SetHTTPClient(client *http.Client) {
	if client != nil {
		httpClient = client
	}
}

func getHttpClient() *http.Client {
	return httpClient
}

// ── Context-aware HTTP execution ───────────────────────────────────────────

func doRequestCtx(ctx context.Context, req *http.Request) (*http.Response, error) {
	resp, err := getHttpClient().Do(req.WithContext(ctx))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("kiro upstream %s: %s", resp.Status, string(body))
	}
	return resp, nil
}

func doRequestRawCtx(ctx context.Context, req *http.Request) (*http.Response, error) {
	return getHttpClient().Do(req.WithContext(ctx))
}

// ── Full Kiro header builder (OmniRoute KiroExecutor.buildHeaders) ─────────

// BuildKiroHeaders constructs the full request headers for a Kiro
// generateAssistantResponse call. It handles auth-method-specific headers
// (API_KEY vs External IdP vs OAuth), SDK identity headers, and cache control.
func BuildKiroHeaders(creds Credentials) map[string]string {
	headers := GetKiroServiceHeaders()
	headers["Amz-Sdk-Request"] = "attempt=1; max=3"
	headers["Amz-Sdk-Invocation-Id"] = uuid.New().String()
	headers["x-amzn-bedrock-cache-control"] = "enable"
	headers["anthropic-beta"] = "prompt-caching-2024-07-31"

	token := creds.AccessToken
	if creds.PSD.AuthMethod == "api_key" {
		if creds.AccessToken != "" {
			token = creds.AccessToken
		}
	}

	if token != "" {
		headers["Authorization"] = "Bearer " + token
		if creds.PSD.AuthMethod == "api_key" {
			headers["tokentype"] = "API_KEY"
		}
		if isExternalIdpAuthMethod(creds.PSD.AuthMethod) {
			headers[KIRO_EXTERNAL_IDP_TOKEN_TYPE_HEADER] = KIRO_EXTERNAL_IDP_TOKEN_TYPE_VALUE
		}
	}

	return headers
}

// BuildStreamingHeaders returns legacy-style headers for the streaming endpoint.
func BuildStreamingHeaders(creds Credentials, host string) map[string]string {
	headers := map[string]string{
		"User-Agent":     "aws-sdk-js/3.0.0 ua/2.1 os/linux lang/js md/nodejs#22.22.0 api/codewhispererstreaming#3.0.0 m/E KiroIDE-0.11.107",
		"x-amz-user-agent": "aws-sdk-js/3.0.0 KiroIDE-0.11.107",
	}
	if host != "" {
		headers["Host"] = host
	}
	return headers
}

// ── Auth header helpers ────────────────────────────────────────────────────

// setAuthHeaders applies auth-specific headers to a request based on the
// credentials' auth method (API_KEY, external_idp, or default OAuth).
func setAuthHeaders(req *http.Request, creds Credentials) {
	token := creds.AccessToken
	if creds.PSD.AuthMethod == "api_key" && creds.AccessToken != "" {
		token = creds.AccessToken
	}

	req.Header.Set("Authorization", "Bearer "+token)

	switch {
	case creds.PSD.AuthMethod == "api_key":
		req.Header.Set("tokentype", "API_KEY")
	case isExternalIdpAuthMethod(creds.PSD.AuthMethod):
		req.Header.Set(KIRO_EXTERNAL_IDP_TOKEN_TYPE_HEADER, KIRO_EXTERNAL_IDP_TOKEN_TYPE_VALUE)
	}
}

// ── Core request sending with failover (backward-compatible) ───────────────

func sendToKiro(creds Credentials, body []byte) (*http.Response, error) {
	return sendToKiroCtx(context.Background(), creds, body)
}

// regionalizeURL replaces the region in an amazonaws.com URL
// e.g., "https://codewhisperer.us-east-1.amazonaws.com" + "eu-central-1"
//    → "https://codewhisperer.eu-central-1.amazonaws.com"
func regionalizeURL(url, region string) string {
	if region == "" || region == "us-east-1" || !strings.Contains(url, "amazonaws.com") {
		return url
	}
	// Pattern: {service}.{region}.amazonaws.com → {service}.{newRegion}.amazonaws.com
	re := regexp.MustCompile(`([a-z]+)\.[a-z0-9-]+\.amazonaws\.com`)
	return re.ReplaceAllString(url, fmt.Sprintf("$1.%s.amazonaws.com", region))
}

// sendToKiroCtx sends the given body to the Kiro generateAssistantResponse
// endpoint with endpoint failover. It tries amazonaws.com endpoints first
// for CodeWhisperer-surface auth methods, then falls back to kiro.dev.
func sendToKiroCtx(ctx context.Context, creds Credentials, body []byte) (*http.Response, error) {
	urls := make([]string, len(baseURLs))
	copy(urls, baseURLs)

	isCodeWhispererSurface := creds.PSD.AuthMethod == "api_key" ||
		creds.PSD.AuthMethod == "external_idp" ||
		creds.PSD.AuthMethod == "idc"

	if isCodeWhispererSurface {
		// Regionalize URLs based on token region (match VansRouter behavior)
		region := strings.TrimSpace(creds.PSD.Region)
		if region == "" {
			region = "us-east-1"
		}
		
		var amazon, others []string
		for _, u := range urls {
			if strings.Contains(u, "amazonaws.com") {
				// Regionalize amazonaws.com URLs if not us-east-1
				regionalized := regionalizeURL(u, region)
				amazon = append(amazon, regionalized)
			} else {
				others = append(others, u)
			}
		}
		urls = append(amazon, others...)
	}

	headers := BuildKiroHeaders(creds)

	var lastErr error
	for _, baseURL := range urls {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL, bytes.NewReader(body))
		if err != nil {
			lastErr = err
			continue
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}

		resp, err := doRequestRawCtx(ctx, req)
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			resp.Body.Close()
			lastErr = fmt.Errorf("429 from %s", baseURL)
			continue
		}
		return resp, nil
	}
	return nil, fmt.Errorf("all kiro endpoints failed: %w", lastErr)
}

// ── Profile ARN discovery (OmniRoute discoverKiroProfileArnAcrossRegions) ──

// DiscoverProfileArn discovers a Kiro profile ARN by probing the Q Developer
// profile regions (us-east-1 / eu-central-1) with the given access token.
// Returns the first ARN found, or "" when no profile is available.
func DiscoverProfileArn(ctx context.Context, accessToken, storedRegion string) (string, error) {
	token := strings.TrimSpace(accessToken)
	if token == "" {
		return "", fmt.Errorf("empty access token")
	}

	regions := buildProfileDiscoveryRegions(storedRegion)
	for _, region := range regions {
		arn, err := listProfileArnForRegion(ctx, token, region)
		if err == nil && arn != "" {
			return arn, nil
		}
	}
	return "", nil
}

func buildProfileDiscoveryRegions(storedRegion string) []string {
	stored := strings.ToLower(strings.TrimSpace(storedRegion))
	preferEu := strings.HasPrefix(stored, "eu-") ||
		strings.HasPrefix(stored, "af-") ||
		strings.HasPrefix(stored, "me-") ||
		strings.HasPrefix(stored, "il-")

	var regions []string
	if preferEu {
		regions = []string{"eu-central-1", "us-east-1"}
	} else {
		regions = []string{"us-east-1", "eu-central-1"}
	}

	if stored != "" && isValidAWSRegion(stored) {
		found := false
		for _, r := range regions {
			if stored == r {
				found = true
				break
			}
		}
		if !found {
			regions = append(regions, stored)
		}
	}
	return regions
}

func isValidAWSRegion(region string) bool {
	for _, r := range KIRO_PROFILE_REGIONS {
		if region == r {
			return true
		}
	}
	return false
}

func listProfileArnForRegion(ctx context.Context, accessToken, region string) (string, error) {
	host := KiroRuntimeHost(region)

	type profileResp struct {
		Profiles []struct {
			ARN string `json:"arn"`
		} `json:"profiles"`
	}

	type listReq struct {
		MaxResults int `json:"maxResults"`
	}

	reqBody, _ := jsonMarshal(listReq{MaxResults: 10})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, host+"/", bytes.NewReader(reqBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("x-amz-target", "AmazonCodeWhispererService.ListAvailableProfiles")
	req.Header.Set("Authorization", "Bearer "+accessToken)

	timeoutCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	resp, err := doRequestRawCtx(timeoutCtx, req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("list profiles: HTTP %d", resp.StatusCode)
	}

	var data profileResp
	if err := jsonDecode(resp.Body, &data); err != nil {
		return "", err
	}

	for _, p := range data.Profiles {
		if p.ARN != "" {
			if arnRegion := RegionFromProfileArn(p.ARN); arnRegion == region {
				return p.ARN, nil
			}
		}
	}
	if len(data.Profiles) > 0 && data.Profiles[0].ARN != "" {
		return data.Profiles[0].ARN, nil
	}
	return "", nil
}

// ── JSON helpers (avoid encoding/json import in every call site) ───────────

func jsonMarshal(v any) ([]byte, error) {
	return json.Marshal(v)
}

func jsonDecode(r io.Reader, v any) error {
	return json.NewDecoder(r).Decode(v)
}
