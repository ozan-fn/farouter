package kiro

// VansRouter equivalents:
//   FetchQuota → open-sse/services/quota/getUsageLimits (farouter-specific infrastructure)
//   parseQuota → open-sse/services/quota response parsing
//   NOTE: VansRouter doesn't have a direct quota.go equivalent.
//   This file implements CodeWhisperer GetUsageLimits API calls for dashboard display.
//   Kept as farouter-specific infrastructure.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

const kiroCQAPIBase = "https://codewhisperer.us-east-1.amazonaws.com"
const kiroQAPIBase = "https://q.us-east-1.amazonaws.com"

// FetchQuota — farouter-specific: queries Kiro usage limits for dashboard.
// VansRouter: open-sse/services/quota/getUsageLimits (similar API call)
func FetchQuota(accessToken, profileArn, authMethod string) (*QuotaResult, error) {
	headers := buildQuotaHeaders(accessToken, authMethod)

	params := url.Values{}
	params.Set("isEmailRequired", "true")
	params.Set("origin", "AI_EDITOR")
	params.Set("resourceType", "AGENTIC_REQUEST")
	if profileArn != "" {
		params.Set("profileArn", profileArn)
	}

	urlStr := kiroCQAPIBase + "/getUsageLimits?" + params.Encode()

	if body, err := doQuotaGet(urlStr, headers); err == nil {
		return parseQuota(body)
	}

	postBody := map[string]string{"origin": "AI_EDITOR", "resourceType": "AGENTIC_REQUEST"}
	if profileArn != "" {
		postBody["profileArn"] = profileArn
	}
	postJSON, _ := json.Marshal(postBody)
	postHeaders := buildQuotaPostHeaders(accessToken, authMethod)
	if body, err := doQuotaPost(kiroCQAPIBase+"/getUsageLimits", postJSON, postHeaders); err == nil {
		return parseQuota(body)
	}

	qParams := url.Values{}
	qParams.Set("origin", "AI_EDITOR")
	qParams.Set("resourceType", "AGENTIC_REQUEST")
	if profileArn != "" {
		qParams.Set("profileArn", profileArn)
	}
	url3 := kiroQAPIBase + "/getUsageLimits?" + qParams.Encode()
	if body, err := doQuotaGet(url3, headers); err == nil {
		return parseQuota(body)
	}

	return nil, ErrQuotaAllFailed
}

// VansRouter: headers in quota service
func buildQuotaHeaders(accessToken, authMethod string) map[string]string {
	h := map[string]string{
		"Authorization":   "Bearer " + accessToken,
		"Accept":          "application/json",
		"User-Agent":      "aws-sdk-js/1.0.0 KiroIDE",
		"x-amz-user-agent": "aws-sdk-js/1.0.0 KiroIDE",
	}
	if authMethod == "api_key" {
		h["tokentype"] = "API_KEY"
	} else if authMethod == "external_idp" {
		h["TokenType"] = "EXTERNAL_IDP"
	}
	return h
}

func buildQuotaPostHeaders(accessToken, authMethod string) map[string]string {
	h := buildQuotaHeaders(accessToken, authMethod)
	h["Content-Type"] = "application/x-amz-json-1.0"
	h["x-amz-target"] = "AmazonCodeWhispererService.GetUsageLimits"
	return h
}

func doQuotaGet(rawURL string, headers map[string]string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := getHttpClient().GetClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

func doQuotaPost(rawURL string, body []byte, headers map[string]string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodPost, rawURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := getHttpClient().GetClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// parseQuota — VansRouter: quota service response parsing
func parseQuota(body []byte) (*QuotaResult, error) {
	var data struct {
		UsageBreakdownList []struct {
			ResourceType string          `json:"resourceType"`
			CurrentUsage json.RawMessage `json:"currentUsage"`
			UsageLimit   json.RawMessage `json:"usageLimit"`
		} `json:"usageBreakdownList"`
		SubscriptionInfo struct {
			SubscriptionTitle string `json:"subscriptionTitle"`
		} `json:"subscriptionInfo"`
		NextDateReset json.RawMessage `json:"nextDateReset"`
		ResetDate     json.RawMessage `json:"resetDate"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, err
	}

	result := &QuotaResult{Plan: data.SubscriptionInfo.SubscriptionTitle}

	resetRaw := data.NextDateReset
	if len(resetRaw) == 0 {
		resetRaw = data.ResetDate
	}
	if len(resetRaw) > 0 {
		var s string
		if json.Unmarshal(resetRaw, &s) == nil {
			result.ResetAt = s
		} else {
			var f float64
			if json.Unmarshal(resetRaw, &f) == nil {
				result.ResetAt = fmt.Sprintf("%v", int64(f))
			}
		}
	}

	for _, b := range data.UsageBreakdownList {
		if b.ResourceType != "CREDIT" && b.ResourceType != "AGENTIC_REQUEST" {
			continue
		}
		result.Used = rawInt(b.CurrentUsage)
		result.Limit = rawInt(b.UsageLimit)
		result.Remaining = result.Limit - result.Used
		if result.Remaining < 0 {
			result.Remaining = 0
		}
		result.Exhausted = result.Limit > 0 && result.Used >= result.Limit
		if b.ResourceType == "CREDIT" {
			break
		}
	}

	return result, nil
}

// rawInt — Go-specific JSON raw message → int
func rawInt(raw json.RawMessage) int {
	if len(raw) == 0 {
		return 0
	}
	var f float64
	if json.Unmarshal(raw, &f) == nil {
		return int(f)
	}
	var obj struct {
		Value float64 `json:"value"`
	}
	if json.Unmarshal(raw, &obj) == nil {
		return int(obj.Value)
	}
	return 0
}
