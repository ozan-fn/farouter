package rtk

import (
	"strings"
)

// ── go build ────────────────────────────────────────────────────────────────

type GoBuildParser struct{}

func (p *GoBuildParser) Name() string { return "go-build" }

func (p *GoBuildParser) Match(output string) bool {
	return strings.Contains(output, "# ") && (strings.Contains(output, "cannot") ||
		strings.Contains(output, "undefined") || strings.Contains(output, "syntax") ||
		strings.Contains(output, "expected"))
}

func (p *GoBuildParser) Parse(output string) string {
	lines := strings.Split(output, "\n")
	var result []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "# ") {
			result = append(result, line)
		} else if len(result) > 0 && !strings.HasPrefix(line, "#") {
			result = append(result, "  "+line)
		}
	}
	if len(result) == 0 {
		return "ok"
	}
	return strings.Join(result, "\n")
}

// ── go vet ──────────────────────────────────────────────────────────────────

type GoVetParser struct{}

func (p *GoVetParser) Name() string { return "go-vet" }

func (p *GoVetParser) Match(output string) bool {
	return strings.Contains(output, "vet:") || strings.Contains(output, "declared but not used")
}

func (p *GoVetParser) Parse(output string) string {
	lines := strings.Split(output, "\n")
	var result []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.Contains(line, ".go:") {
			result = append(result, line)
		}
	}
	if len(result) == 0 {
		return "ok"
	}
	return strings.Join(result, "\n")
}

// ── cargo build ─────────────────────────────────────────────────────────────

type CargoBuildParser struct{}

func (p *CargoBuildParser) Name() string { return "cargo-build" }

func (p *CargoBuildParser) Match(output string) bool {
	return (strings.Contains(output, "Compiling ") || strings.Contains(output, "error[")) &&
		strings.Contains(output, "Finished")
}

func (p *CargoBuildParser) Parse(output string) string {
	lines := strings.Split(output, "\n")
	var result []string
	hasError := false
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "error[") || strings.HasPrefix(line, "error:") {
			result = append(result, line)
			hasError = true
		} else if hasError && strings.HasPrefix(line, "  ") {
			result = append(result, line)
		} else if strings.HasPrefix(line, "warning[") || strings.HasPrefix(line, "warning:") {
			result = append(result, line)
		} else if strings.HasPrefix(line, "Finished") {
			if !hasError {
				result = append(result, line)
			}
		}
	}
	if len(result) == 0 {
		return "ok"
	}
	return strings.Join(result, "\n")
}

// ── cargo clippy ────────────────────────────────────────────────────────────

type CargoClippyParser struct{}

func (p *CargoClippyParser) Name() string { return "cargo-clippy" }

func (p *CargoClippyParser) Match(output string) bool {
	return strings.Contains(output, "clippy") || strings.Contains(output, "warning:")
}

func (p *CargoClippyParser) Parse(output string) string {
	lines := strings.Split(output, "\n")
	var result []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "warning:") || strings.Contains(line, "error:") ||
			strings.Contains(line, "help:") || strings.Contains(line, "for further") {
			result = append(result, line)
		}
	}
	if len(result) == 0 {
		return "ok (no warnings)"
	}
	if len(result) > 20 {
		result = result[:20]
		result = append(result, "[... more]")
	}
	return strings.Join(result, "\n")
}

// ── tsc ─────────────────────────────────────────────────────────────────────

type TscParser struct{}

func (p *TscParser) Name() string { return "tsc" }

func (p *TscParser) Match(output string) bool {
	return strings.Contains(output, ".ts(") || strings.Contains(output, ".tsx(") ||
		strings.Contains(output, "error TS") || strings.Contains(output, "Cannot find")
}

func (p *TscParser) Parse(output string) string {
	lines := strings.Split(output, "\n")
	var result []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.Contains(line, ".ts(") || strings.Contains(line, ".tsx(") {
			result = append(result, line)
		} else if strings.HasPrefix(line, "  ") && len(result) > 0 {
			result = append(result, line)
		} else if strings.Contains(line, "error") || strings.Contains(line, "Error") {
			result = append(result, line)
		}
	}
	if len(result) == 0 {
		return "ok"
	}
	return strings.Join(result, "\n")
}

// ── eslint ──────────────────────────────────────────────────────────────────

type EslintParser struct{}

func (p *EslintParser) Name() string { return "eslint" }

func (p *EslintParser) Match(output string) bool {
	return strings.Contains(output, "✖") ||
		(strings.Contains(output, "problems") && strings.Contains(output, "errors"))
}

func (p *EslintParser) Parse(output string) string {
	lines := strings.Split(output, "\n")
	var result []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.Contains(line, "✖") || strings.Contains(line, "problems") ||
			strings.Contains(line, "errors") || strings.Contains(line, "warnings") {
			result = append(result, line)
		}
	}
	if len(result) == 0 {
		return "ok"
	}
	return strings.Join(result, "\n")
}

// ── prettier ────────────────────────────────────────────────────────────────

type PrettierParser struct{}

func (p *PrettierParser) Name() string { return "prettier" }

func (p *PrettierParser) Match(output string) bool {
	return strings.Contains(output, "[warn]") || strings.Contains(output, "[error]") ||
		strings.Contains(output, "prettier")
}

func (p *PrettierParser) Parse(output string) string {
	lines := strings.Split(output, "\n")
	var result []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.Contains(trimmed, "[warn]") || strings.Contains(trimmed, "[error]") {
			result = append(result, trimmed)
		}
	}
	hasCheck := false
	for _, line := range lines {
		if strings.Contains(line, "checked") || strings.Contains(line, "All done") {
			hasCheck = true
			break
		}
	}
	if len(result) == 0 && hasCheck {
		return "ok"
	}
	if len(result) == 0 {
		return "ok"
	}
	return strings.Join(result, "\n")
}

// ── next build ──────────────────────────────────────────────────────────────

type NextBuildParser struct{}

func (p *NextBuildParser) Name() string { return "next" }

func (p *NextBuildParser) Match(output string) bool {
	return strings.Contains(output, "Creating an optimized production build") ||
		strings.Contains(output, "Route (app)")
}

func (p *NextBuildParser) Parse(output string) string {
	lines := strings.Split(output, "\n")
	var result []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.Contains(line, "✓") || strings.Contains(line, "✗") ||
			strings.Contains(line, "×") || strings.Contains(line, "error") ||
			strings.Contains(line, "Error") || strings.Contains(line, "Failed") {
			result = append(result, line)
		}
	}
	if len(result) == 0 {
		return "ok"
	}
	return strings.Join(result, "\n")
}

// ── golangci-lint ───────────────────────────────────────────────────────────

type GolangciLintParser struct{}

func (p *GolangciLintParser) Name() string { return "golangci-lint" }

func (p *GolangciLintParser) Match(output string) bool {
	return strings.Contains(output, "golangci-lint") || strings.Contains(output, ".go:") ||
		(strings.Contains(output, "issues") && strings.Contains(output, "out of"))
}

func (p *GolangciLintParser) Parse(output string) string {
	lines := strings.Split(output, "\n")
	var result []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.Contains(line, ".go:") || strings.Contains(line, "issues") ||
			strings.Contains(line, "result") {
			result = append(result, line)
		}
	}
	if len(result) == 0 {
		return "ok"
	}
	if len(result) > 20 {
		result = result[:20]
		result = append(result, "[... more]")
	}
	return strings.Join(result, "\n")
}
