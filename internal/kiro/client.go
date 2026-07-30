package kiro

// VansRouter equivalents:
//   BuildKiroHeaders   → open-sse/executors/kiro.js buildHeaders()
//   sendToKiroCtx      → open-sse/executors/kiro.js execute() + base.js execute()
//   getOrderedBaseUrls → open-sse/executors/kiro.js getOrderedBaseUrls()
//   DiscoverProfileArn → open-sse/services/discoverProfileArn (kiro external service)

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-resty/resty/v2"
	"github.com/google/uuid"
)

// httpClient uses resty v2 with connect-only timeout via DialContext + TLSHandshakeTimeout.
// VansRouter pattern: FETCH_CONNECT_TIMEOUT_MS hanya untuk connection phase,
// tidak untuk stream. resty.Client.SetTimeout TIDAK dipakai karena membunuh body read.
// Stream timeout di-handle oleh PipeWithDisconnect (TTFT 200s + stall 360s).
// Non-streaming request (quota, token) punya context.WithTimeout sendiri.
var httpClient = createRestyClient()

func createRestyClient() *resty.Client {
	client := resty.New()
	client.SetTransport(&http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   FetchConnectTimeout,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout: FetchConnectTimeout,
	})
	return client
}

func SetHTTPClient(client *resty.Client) {
	if client != nil {
		httpClient = client
	}
}

func getHttpClient() *resty.Client {
	return httpClient
}

// VansRouter: Go-specific infrastructure (HTTP exec helpers)

func doRequestRawCtx(ctx context.Context, req *http.Request) (*http.Response, error) {
	// For http.Request, use the underlying HTTP client via resty
	// This preserves body and headers from the raw request
	client := getHttpClient()
	
	// Use resty's underlying client to execute the request
	httpCli := client.GetClient()
	return httpCli.Do(req.WithContext(ctx))
}

// BuildKiroHeaders — VansRouter: kiro.js buildHeaders()
// Constructs full request headers for Kiro generateAssistantResponse call.
// Handles auth-method-specific headers (API_KEY vs External IdP vs OAuth).
func BuildKiroHeaders(creds Credentials) map[string]string {
	headers := GetKiroServiceHeaders()
	headers["Amz-Sdk-Request"] = "attempt=1; max=3"
	headers["Amz-Sdk-Invocation-Id"] = uuid.New().String()
	headers["User-Agent"] = buildUserAgent(creds.MachineId)
	headers["X-Amz-User-Agent"] = buildAmzUserAgent(creds.MachineId)

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

// BuildStreamingHeaders — VansRouter legacy pattern (kiro.js inline)
func BuildStreamingHeaders(creds Credentials, host string) map[string]string {
	headers := map[string]string{
		"User-Agent":       buildUserAgent(creds.MachineId),
		"x-amz-user-agent": buildAmzUserAgent(creds.MachineId),
	}
	if host != "" {
		headers["Host"] = host
	}
	return headers
}

// regionalizeURL — VansRouter: kiro.js getOrderedBaseUrls() inline regionalize lambda
func regionalizeURL(url, region string) string {
	if region == "" || region == "us-east-1" || !strings.Contains(url, "amazonaws.com") {
		return url
	}
	re := regexp.MustCompile(`([a-z]+)\.[a-z0-9-]+\.amazonaws\.com`)
	return re.ReplaceAllString(url, fmt.Sprintf("$1.%s.amazonaws.com", region))
}

// sendToKiroCtx — VansRouter: kiro.js execute() + base.js execute() URL fallback loop
func sendToKiroCtx(ctx context.Context, creds Credentials, body []byte) (*http.Response, error) {
	urls := make([]string, len(baseURLs))
	copy(urls, baseURLs)

	isCodeWhispererSurface := creds.PSD.AuthMethod == "api_key" ||
		creds.PSD.AuthMethod == "external_idp" ||
		creds.PSD.AuthMethod == "idc"

	if isCodeWhispererSurface {
		region := strings.TrimSpace(creds.PSD.Region)
		if region == "" {
			region = "us-east-1"
		}

		var amazon, others []string
		for _, u := range urls {
			if strings.Contains(u, "amazonaws.com") {
				regionalized := regionalizeURL(u, region)
				amazon = append(amazon, regionalized)
			} else {
				others = append(others, u)
			}
		}
		urls = append(amazon, others...)
	}

	headers := BuildKiroHeaders(creds)
	client := getHttpClient()

	var lastErr error
	for _, baseURL := range urls {
		resp, err := client.R().
			SetContext(ctx).
			SetHeaders(headers).
			SetBody(body).
			Post(baseURL)

		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode() == http.StatusTooManyRequests {
			lastErr = fmt.Errorf("429 from %s", baseURL)
			continue
		}
		return resp.RawResponse, nil
	}
	return nil, fmt.Errorf("all kiro endpoints failed: %w", lastErr)
}

// DiscoverProfileArn — VansRouter: discoverKiroProfileArnAcrossRegions (external service)
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

// buildProfileDiscoveryRegions — VansRouter: kiroRegion.ts priority logic
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

	timeoutCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	var data profileResp
	resp, err := getHttpClient().R().
		SetContext(timeoutCtx).
		SetHeader("Content-Type", "application/x-amz-json-1.0").
		SetHeader("Accept", "application/json").
		SetHeader("x-amz-target", "AmazonCodeWhispererService.ListAvailableProfiles").
		SetHeader("Authorization", "Bearer "+accessToken).
		SetBody(reqBody).
		SetResult(&data).
		Post(host + "/")

	if err != nil {
		return "", err
	}

	if resp.StatusCode() != http.StatusOK {
		return "", fmt.Errorf("list profiles: HTTP %d", resp.StatusCode())
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

// jsonMarshal/jsonDecode — Go-specific helpers
func jsonMarshal(v any) ([]byte, error) {
	return json.Marshal(v)
}

func jsonDecode(r io.Reader, v any) error {
	return json.NewDecoder(r).Decode(v)
}
