package kiro

import (
	"bytes"
	"strings"
	"testing"
)

// sse builds minimal OpenAI SSE bytes from contents.
func sse(contents ...string) []byte {
	var b bytes.Buffer
	for _, c := range contents {
		// single chunk with content
		b.WriteString(`data: {"choices":[{"delta":{"content":` + jsonStr(c) + `},"index":0}]}` + "\n\n")
	}
	b.WriteString(`data: {"choices":[{"delta":{},"finish_reason":"end_turn","index":0}]}` + "\n\n")
	b.WriteString("data: [DONE]\n\n")
	return b.Bytes()
}

func jsonStr(s string) string {
	// minimal JSON string escaping
	r := strings.ReplaceAll(s, `\`, `\\`)
	r = strings.ReplaceAll(r, `"`, `\"`)
	r = strings.ReplaceAll(r, "\n", `\n`)
	return `"` + r + `"`
}

func TestHallucination_EllipsisOnly(t *testing.T) {
	for _, c := range []string{"...", "…", " ... "} {
		if !isEllipsisOnly(c) {
			t.Errorf("isEllipsisOnly(%q)=false want true", c)
		}
		buf := sse(c)
		res := validateIntegrity(bytes.NewReader(buf), 1<<20, &IntegrityDiagnostics{StopDisposition: StopComplete})
		if res.Kind != IntegrityEllipsis {
			t.Errorf("ellipsis %q -> kind=%v want IntegrityEllipsis", c, res.Kind)
		}
	}
	// non-ellipsis should not be flagged
	buf := sse("real answer with data")
	res := validateIntegrity(bytes.NewReader(buf), 1<<20, &IntegrityDiagnostics{StopDisposition: StopComplete})
	if res.Kind == IntegrityEllipsis {
		t.Error("valid content flagged as ellipsis")
	}
}

func TestHallucination_ShortFutureAction(t *testing.T) {
	cases := []struct {
		content string
		want    bool
	}{
		{"I'll verify the deployment and confirm", true},
		{"Let me check the logs", true},
		{"Next, I will validate the fix", true},
		{"real answer: status is ok, checksum matches 0x123", false}, // has result clause
		{"Done — verified and fixed, all tests passed", false},       // completedFinal
		{"Please provide your approval to continue", false},            // userWait
	}
	for _, tc := range cases {
		got := isShortFutureAction(tc.content)
		if got != tc.want {
			t.Errorf("isShortFutureAction(%q)=%v want %v", tc.content, got, tc.want)
		}
	}
	// via validateIntegrity (no tool_calls + short future -> IntegrityShortFinal)
	buf := sse("I'll verify the deployment")
	res := validateIntegrity(bytes.NewReader(buf), 1<<20, &IntegrityDiagnostics{StopDisposition: StopComplete})
	if res.Kind != IntegrityShortFinal {
		t.Errorf("short future via integrity: kind=%v want ShortFinal", res.Kind)
	}
}

func TestHallucination_FutureWithToolCalls_NotFlagged(t *testing.T) {
	// If model already emitted tool_calls, short final is OK (it's delegating)
	var b bytes.Buffer
	b.WriteString(`data: {"choices":[{"delta":{"tool_calls":[{"id":"call_1","function":{"name":"executeBash"}}]},"index":0}]}` + "\n\n")
	b.WriteString(`data: {"choices":[{"delta":{"content":"I'll verify"},"index":0}]}` + "\n\n")
	b.WriteString(`data: {"choices":[{"delta":{},"finish_reason":"tool_use","index":0}]}` + "\n\n")
	b.WriteString("data: [DONE]\n\n")
	res := validateIntegrity(bytes.NewReader(b.Bytes()), 1<<20, &IntegrityDiagnostics{StopDisposition: StopToolUse})
	if res.Kind == IntegrityShortFinal || res.Kind == IntegrityEllipsis {
		t.Errorf("tool_use should not be flagged as hallucination, got %v", res.Kind)
	}
}

func TestHallucination_ValidAnswer_Passes(t *testing.T) {
	buf := sse("Found 3 TODOs in main.go:10, handlers.go:42. All verified against grep output.")
	res := validateIntegrity(bytes.NewReader(buf), 1<<20, &IntegrityDiagnostics{StopDisposition: StopComplete})
	if res.Kind != IntegrityComplete {
		t.Errorf("valid answer flagged as %v", res.Kind)
	}
}

func TestHallucination_RetryableProtocolFail(t *testing.T) {
	diag := &IntegrityDiagnostics{StopDisposition: StopRetryableProtocolFail, StopReason: "malformed_model_output"}
	buf := sse("anything")
	res := validateIntegrity(bytes.NewReader(buf), 1<<20, diag)
	if res.Kind != IntegrityInvalidTool {
		t.Errorf("retryable protocol fail -> %v want InvalidTool", res.Kind)
	}
}

func TestHallucination_RepairInstruction_Appended(t *testing.T) {
	// Simulate ExecuteWithIntegrityCheck repair round: hallucinated ellipsis should get repair instruction
	origBody := []byte(`{"messages":[{"role":"user","content":"do thing"}]}`)
	repaired := appendRepairInstruction(origBody, IntegrityEllipsis)
	if !bytes.Contains(repaired, []byte("repair")) && !bytes.Contains(repaired, []byte("REPAIR")) && len(repaired) <= len(origBody) {
		t.Errorf("repair not appended for ellipsis: %s", string(repaired))
	}
	// generic check: repaired body is larger and still valid JSON-ish
	if len(repaired) <= len(origBody) {
		t.Errorf("repaired body not larger: %d vs %d", len(repaired), len(origBody))
	}
}
