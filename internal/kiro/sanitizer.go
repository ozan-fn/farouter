package kiro

import (
	"crypto/sha256"
	"fmt"
	"strings"
)

const maxToolNameLength = 64

var stripKeys = map[string]bool{
	"additionalProperties": true,
	"anyOf":                true,
	"oneOf":                true,
	"allOf":                true,
	"not":                  true,
	"$schema":              true,
	"$id":                  true,
	"$ref":                 true,
	"$defs":                true,
	"definitions":          true,
	"if":                   true,
	"then":                 true,
	"else":                 true,
	"unevaluatedProperties": true,
	"unevaluatedItems":     true,
	"contentEncoding":      true,
	"contentMediaType":     true,
}

func stripUnsupportedKeys(value any) any {
	if value == nil {
		return value
	}
	switch v := value.(type) {
	case map[string]any:
		cleaned := make(map[string]any, len(v))
		for key, val := range v {
			if stripKeys[key] {
				continue
			}
			if key == "required" {
				if arr, ok := val.([]any); ok && len(arr) == 0 {
					continue
				}
			}
			cleaned[key] = stripUnsupportedKeys(val)
		}
		return cleaned
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = stripUnsupportedKeys(item)
		}
		return out
	default:
		return v
	}
}

func normalizeKiroToolSchema(schema map[string]any) map[string]any {
	if schema == nil {
		return map[string]any{"type": "object", "properties": map[string]any{}}
	}
	result := stripUnsupportedKeys(schema)
	if m, ok := result.(map[string]any); ok {
		if _, has := m["required"]; !has {
			m["required"] = []any{}
		}
		return m
	}
	return map[string]any{"type": "object", "properties": map[string]any{}, "required": []any{}}
}

type SanitizeResult struct {
	Tools   []any
	NameMap map[string]string
}

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
			truncated := name[:56] + "_" + fmt.Sprintf("%x", hash[:7])
			nameMap[truncated] = name
			spec["name"] = truncated
		}

		schema, _ := spec["inputSchema"].(map[string]any)
		if schema != nil {
			jsonSchema, _ := schema["json"].(map[string]any)
			if jsonSchema != nil {
				schema["json"] = normalizeKiroToolSchema(jsonSchema)
			}
		}

		out = append(out, map[string]any{"toolSpecification": spec})
	}
	return SanitizeResult{Tools: out, NameMap: nameMap}
}

func ResolveKiroModelAlias(model string) (upstream string, thinking bool) {
	upstream = strings.TrimPrefix(model, "Kafuu/")
	upstream = strings.TrimPrefix(upstream, "kr/")
	if upstream == "auto-kiro" {
		upstream = "auto"
	}
	if strings.HasSuffix(upstream, "-agentic") {
		upstream = upstream[:len(upstream)-len("-agentic")]
	}
	if strings.HasSuffix(upstream, "-thinking") {
		upstream = upstream[:len(upstream)-len("-thinking")]
		thinking = true
	}
	upstream = normalizeModelVersion(upstream)
	return
}

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
