package kiro

// VansRouter equivalents:
//   SanitizeKiroTools  → open-sse/executors/kiro.js inline (tool name truncation via hash)
//   ResolveKiroModelAlias → open-sse/config/kiroConstants.js resolveKiroModel()
//   normalizeModelVersion → No VansRouter equivalent (Go-specific version normalization)

import (
	"crypto/sha256"
	"fmt"
	"strings"
)

const maxToolNameLength = 64

type SanitizeResult struct {
	Tools   []any
	NameMap map[string]string
}

// SanitizeKiroTools truncates tool names >64 chars with SHA256 suffix.
// VansRouter: inline in kiro.js buildHeaders / tool name handling
func SanitizeKiroTools(tools []any) SanitizeResult {
	nameMap := map[string]string{}
	if len(tools) == 0 {
		return SanitizeResult{Tools: tools, NameMap: nameMap}
	}

	out := make([]any, 0, len(tools))
	for _, t := range tools {
		tool, ok := t.(map[string]any)
		if !ok {
			out = append(out, t)
			continue
		}
		spec, _ := tool["toolSpecification"].(map[string]any)
		if spec == nil {
			out = append(out, t)
			continue
		}

		name, _ := spec["name"].(string)
		if len(name) > maxToolNameLength {
			hash := sha256.Sum256([]byte(name))
			truncated := name[:51] + "_" + fmt.Sprintf("%x", hash[:6])
			nameMap[truncated] = name
			spec["name"] = truncated
		}

		out = append(out, map[string]any{"toolSpecification": spec})
	}
	return SanitizeResult{Tools: out, NameMap: nameMap}
}

// ResolveKiroModelAlias — VansRouter: kiroConstants.js resolveKiroModel()
// Strips kr/ prefix, -agentic suffix, -thinking suffix. Returns upstream model + thinking flag.
func ResolveKiroModelAlias(model string) (upstream string, thinking bool) {
	upstream = strings.TrimPrefix(model, "Kafuu/")
	upstream = strings.TrimPrefix(upstream, "kr/")
	if upstream == "auto-kiro" {
		upstream = "auto"
	}
	upstream = strings.TrimSuffix(upstream, "-agentic")
	if strings.HasSuffix(upstream, "-thinking") {
		upstream = strings.TrimSuffix(upstream, "-thinking")
		thinking = true
	}
	upstream = normalizeModelVersion(upstream)
	return
}

// normalizeModelVersion converts claude-sonnet-4-5 → claude-sonnet-4.5
// No VansRouter equivalent — Go-specific version normalization for model catalog matching.
func normalizeModelVersion(model string) string {
	prefixes := []string{"claude-opus-", "claude-sonnet-", "claude-haiku-", "claude-3-"}
	for _, pfx := range prefixes {
		if !strings.HasPrefix(model, pfx) {
			continue
		}
		rest := model[len(pfx):]
		dashIdx := strings.LastIndex(rest, "-")
		if dashIdx < 0 {
			return model
		}
		minor := rest[dashIdx+1:]
		if len(minor) < 1 || len(minor) > 2 {
			return model
		}
		allDigits := true
		for _, c := range minor {
			if c < '0' || c > '9' {
				allDigits = false
				break
			}
		}
		if !allDigits {
			return model
		}
		return model[:len(model)-len(minor)-1] + "." + minor
	}
	return model
}
