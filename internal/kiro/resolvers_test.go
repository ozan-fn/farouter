package kiro

import (
	"reflect"
	"testing"
)

func TestRegionFromProfileArn(t *testing.T) {
	cases := map[string]string{
		"arn:aws:codewhisperer:eu-central-1:123:profile/X": "eu-central-1",
		"arn:aws:codewhisperer:us-east-1:123:profile/X":    "us-east-1",
		"":                              "",
		"not-an-arn":                    "",
		"arn:aws:s3:us-west-2:1:bucket": "",
	}
	for in, want := range cases {
		if got := RegionFromProfileArn(in); got != want {
			t.Errorf("RegionFromProfileArn(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestResolveRuntimeRegion(t *testing.T) {
	cases := []struct {
		stored, arn, want string
	}{
		{"", "", "us-east-1"},
		{"eu-central-1", "", "eu-central-1"},
		{"ap-south-1", "", "us-east-1"}, // not in KIRO_PROFILE_REGIONS
		{"", "arn:aws:codewhisperer:eu-central-1:1:profile/X", "eu-central-1"},
		{"us-east-1", "arn:aws:codewhisperer:eu-central-1:1:profile/X", "eu-central-1"}, // ARN wins
	}
	for _, c := range cases {
		if got := ResolveRuntimeRegion(c.stored, c.arn); got != c.want {
			t.Errorf("ResolveRuntimeRegion(%q, %q) = %q, want %q", c.stored, c.arn, got, c.want)
		}
	}
}

func TestRegionalizeURL(t *testing.T) {
	cases := []struct {
		url, region, want string
	}{
		{"https://codewhisperer.us-east-1.amazonaws.com/generateAssistantResponse", "eu-central-1",
			"https://codewhisperer.eu-central-1.amazonaws.com/generateAssistantResponse"},
		{"https://q.us-east-1.amazonaws.com/generateAssistantResponse", "eu-central-1",
			"https://q.eu-central-1.amazonaws.com/generateAssistantResponse"},
		{"https://codewhisperer.us-east-1.amazonaws.com/x", "", "https://codewhisperer.us-east-1.amazonaws.com/x"},
		{"https://codewhisperer.us-east-1.amazonaws.com/x", "us-east-1", "https://codewhisperer.us-east-1.amazonaws.com/x"},
		{"https://runtime.us-east-1.kiro.dev/generateAssistantResponse", "eu-central-1",
			"https://runtime.us-east-1.kiro.dev/generateAssistantResponse"},
	}
	for _, c := range cases {
		if got := regionalizeURL(c.url, c.region); got != c.want {
			t.Errorf("regionalizeURL(%q, %q) = %q, want %q", c.url, c.region, got, c.want)
		}
	}
}

func TestBuildProfileDiscoveryRegions(t *testing.T) {
	cases := []struct {
		stored string
		want   []string
	}{
		{"", []string{"us-east-1", "eu-central-1"}},
		{"me-south-1", []string{"eu-central-1", "us-east-1"}},
		{"eu-west-1", []string{"eu-central-1", "us-east-1"}}, // eu-west-1 is not a valid KIRO region
	}
	for _, c := range cases {
		if got := buildProfileDiscoveryRegions(c.stored); !reflect.DeepEqual(got, c.want) {
			t.Errorf("buildProfileDiscoveryRegions(%q) = %v, want %v", c.stored, got, c.want)
		}
	}
}

func TestThinkingLengthForEffort(t *testing.T) {
	cases := map[string]int{"max": 32000, "xhigh": 32000, "high": 32000, "medium": 16000, "low": 8000, "": 8000}
	for in, want := range cases {
		if got := ThinkingLengthForEffort(in); got != want {
			t.Errorf("ThinkingLengthForEffort(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestIsKnownModel(t *testing.T) {
	if !IsKnownModel("claude-sonnet-4-5") {
		t.Error("claude-sonnet-4-5 should be known")
	}
	if IsKnownModel("not-a-model") {
		t.Error("not-a-model should be unknown")
	}
}

func TestKiroRuntimeHost(t *testing.T) {
	if got := KiroRuntimeHost("us-east-1"); got != "https://codewhisperer.us-east-1.amazonaws.com" {
		t.Errorf("KiroRuntimeHost(us-east-1) = %q", got)
	}
	if got := KiroRuntimeHost("eu-central-1"); got != "https://q.eu-central-1.amazonaws.com" {
		t.Errorf("KiroRuntimeHost(eu-central-1) = %q", got)
	}
}

func TestGetOrderedBaseURLs(t *testing.T) {
	idc := GetOrderedBaseURLs(Credentials{PSD: ProviderSpecificData{AuthMethod: "idc", Region: "eu-central-1"}}, "eu-central-1")
	if len(idc) != 3 {
		t.Fatalf("idc urls = %d, want 3", len(idc))
	}
	if idc[0] != "https://codewhisperer.eu-central-1.amazonaws.com/generateAssistantResponse" {
		t.Errorf("idc urls[0] = %q, want regionalized codewhisperer", idc[0])
	}

	social := GetOrderedBaseURLs(Credentials{PSD: ProviderSpecificData{AuthMethod: "builderId"}}, "us-east-1")
	if len(social) != 3 {
		t.Fatalf("social urls = %d, want 3", len(social))
	}
	if social[0] != "https://runtime.us-east-1.kiro.dev/generateAssistantResponse" {
		t.Errorf("social urls[0] = %q, want runtime host first", social[0])
	}
}

func TestEnvPositiveInt(t *testing.T) {
	const name = "KIRO_TEST_ENV_POSITIVE_INT"
	t.Setenv(name, "")
	if got := envPositiveInt(name, 7); got != 7 {
		t.Errorf("unset = %d, want fallback 7", got)
	}
	t.Setenv(name, "12")
	if got := envPositiveInt(name, 7); got != 12 {
		t.Errorf("= 12, got %d", got)
	}
	t.Setenv(name, "-3")
	if got := envPositiveInt(name, 7); got != 7 {
		t.Errorf("negative should fall back, got %d", got)
	}
	t.Setenv(name, "abc")
	if got := envPositiveInt(name, 7); got != 7 {
		t.Errorf("non-numeric should fall back, got %d", got)
	}
}

func TestResolveRetryEntry(t *testing.T) {
	cases := []struct {
		in           RetryConfigEntry
		wantAttempts int
		wantDelayMs  int
	}{
		{RetryConfigEntry{Attempts: 0, DelayMs: 0}, 0, 2000},
		{RetryConfigEntry{Attempts: 3, DelayMs: 0}, 3, 2000},
		{RetryConfigEntry{Attempts: 3, DelayMs: 3000}, 3, 3000},
	}
	for _, c := range cases {
		a, d := resolveRetryEntry(c.in)
		if a != c.wantAttempts || d != c.wantDelayMs {
			t.Errorf("resolveRetryEntry(%+v) = (%d, %d), want (%d, %d)", c.in, a, d, c.wantAttempts, c.wantDelayMs)
		}
	}
}
