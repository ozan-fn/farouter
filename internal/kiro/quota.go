package kiro

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type QuotaResult struct {
	Used      int
	Limit     int
	Remaining int
	ResetAt   string
	Plan      string
	Exhausted bool
}

func FetchQuota(accessToken, profileArn, authMethod string) (*QuotaResult, error) {
	headers := map[string]string{
		"Authorization":    "Bearer " + accessToken,
		"Accept":           "application/json",
		"User-Agent":       "aws-sdk-js/1.0.0 KiroIDE",
		"x-amz-user-agent": "aws-sdk-js/1.0.0 KiroIDE",
	}
	if authMethod == "api_key" {
		headers["tokentype"] = "API_KEY"
	} else if authMethod == "external_idp" {
		headers["TokenType"] = "EXTERNAL_IDP"
	}

	// Attempt 1: GET codewhisperer
	params := "isEmailRequired=true&origin=AI_EDITOR&resourceType=AGENTIC_REQUEST"
	if profileArn != "" {
		params += "&profileArn=" + profileArn
	}
	url1 := "https://codewhisperer.us-east-1.amazonaws.com/getUsageLimits?" + params
	if body, err := doGet(url1, headers); err == nil {
		return parseQuota(body)
	}

	// Attempt 2: POST codewhisperer
	postBody := map[string]string{"origin": "AI_EDITOR", "resourceType": "AGENTIC_REQUEST"}
	if profileArn != "" {
		postBody["profileArn"] = profileArn
	}
	postJSON, _ := json.Marshal(postBody)
	postHeaders := map[string]string{
		"Authorization": "Bearer " + accessToken,
		"Content-Type":  "application/x-amz-json-1.0",
		"x-amz-target":  "AmazonCodeWhispererService.GetUsageLimits",
		"Accept":        "application/json",
	}
	if authMethod == "api_key" {
		postHeaders["tokentype"] = "API_KEY"
	}
	if body, err := doPost2("https://codewhisperer.us-east-1.amazonaws.com/getUsageLimits", postJSON, postHeaders); err == nil {
		return parseQuota(body)
	}

	// Attempt 3: GET q endpoint
	qParams := "origin=AI_EDITOR&resourceType=AGENTIC_REQUEST"
	if profileArn != "" {
		qParams += "&profileArn=" + profileArn
	}
	url3 := "https://q.us-east-1.amazonaws.com/getUsageLimits?" + qParams
	if body, err := doGet(url3, headers); err == nil {
		return parseQuota(body)
	}

	return nil, fmt.Errorf("all quota endpoints failed")
}

func doGet(url string, headers map[string]string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

func doPost2(url string, body []byte, headers map[string]string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

func parseQuota(body []byte) (*QuotaResult, error) {
	var data struct {
		UsageBreakdownList []struct {
			ResourceType                string          `json:"resourceType"`
			CurrentUsage                json.RawMessage `json:"currentUsage"`
			UsageLimit                  json.RawMessage `json:"usageLimit"`
			NextDateReset               json.RawMessage `json:"nextDateReset"`
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
