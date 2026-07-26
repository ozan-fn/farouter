package rtk

import (
	"strings"
)

// ── Go Test ─────────────────────────────────────────────────────────────────

type GoTestParser struct{}

func (p *GoTestParser) Name() string { return "go-test" }

func (p *GoTestParser) Match(output string) bool {
	return strings.Contains(output, "PASS") || strings.Contains(output, "FAIL") ||
		strings.Contains(output, "ok  \t") || strings.Contains(output, "?   \t")
}

func (p *GoTestParser) Parse(output string) string {
	lines := strings.Split(output, "\n")
	var result []string

	inFailure := false
	var failureLines []string

	for _, line := range lines {
		line = strings.TrimSpace(line)

		if strings.HasPrefix(line, "ok  \t") || strings.HasPrefix(line, "PASS") {
			result = append(result, line)
		} else if strings.HasPrefix(line, "FAIL\t") || strings.HasPrefix(line, "?   \t") {
			result = append(result, line)
			inFailure = true
		} else if strings.HasPrefix(line, "--- FAIL:") || strings.HasPrefix(line, "--- PASS:") {
			if strings.Contains(line, "FAIL") {
				failureLines = append(failureLines, line)
				inFailure = true
			}
		} else if inFailure && line != "" {
			if len(failureLines) < 10 {
				failureLines = append(failureLines, "  "+line)
			}
		} else if line == "" {
			inFailure = false
		}
	}

	if len(failureLines) > 0 {
		result = append(result, failureLines...)
	}

	if len(result) == 0 {
		return "ok (no output)"
	}

	return strings.Join(result, "\n")
}

// ── Cargo Test ──────────────────────────────────────────────────────────────

type CargoTestParser struct{}

func (p *CargoTestParser) Name() string { return "cargo-test" }

func (p *CargoTestParser) Match(output string) bool {
	return strings.Contains(output, "running ") && strings.Contains(output, "test result:")
}

func (p *CargoTestParser) Parse(output string) string {
	lines := strings.Split(output, "\n")
	var result []string

	inFailure := false

	for _, line := range lines {
		line = strings.TrimSpace(line)

		if strings.HasPrefix(line, "Compiling ") || strings.HasPrefix(line, "Finished ") {
			continue
		}

		if strings.HasPrefix(line, "running ") || strings.HasPrefix(line, "test result:") {
			result = append(result, line)
		} else if strings.HasPrefix(line, "test ") && strings.Contains(line, "FAILED") {
			result = append(result, line)
			inFailure = true
		} else if inFailure && (strings.HasPrefix(line, "thread ") || strings.HasPrefix(line, "note:")) {
			if len(result) < 20 {
				result = append(result, line)
			}
		}
	}

	if len(result) == 0 {
		return "ok"
	}

	return strings.Join(result, "\n")
}

// ── Generic Test ────────────────────────────────────────────────────────────

type TestParser struct{}

func (p *TestParser) Name() string { return "test" }

func (p *TestParser) Match(output string) bool {
	return strings.Contains(output, "PASS") || strings.Contains(output, "FAIL") ||
		strings.Contains(output, "test result:")
}

func (p *TestParser) Parse(output string) string {
	lines := strings.Split(output, "\n")
	var result []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.Contains(trimmed, "FAIL") || strings.Contains(trimmed, "PASS") ||
			strings.Contains(trimmed, "test result:") || strings.Contains(trimmed, "tests failed") ||
			strings.Contains(trimmed, "tests passed") {
			result = append(result, trimmed)
		}
	}
	if len(result) == 0 {
		return "ok"
	}
	return strings.Join(result, "\n")
}

// ── Jest ────────────────────────────────────────────────────────────────────

type JestParser struct{}

func (p *JestParser) Name() string { return "jest" }

func (p *JestParser) Match(output string) bool {
	return strings.Contains(output, "Tests:") && strings.Contains(output, "Snapshots:") ||
		(strings.Contains(output, "PASS") && strings.Contains(output, "FAIL") &&
			strings.Contains(output, "●"))
}

func (p *JestParser) Parse(output string) string {
	lines := strings.Split(output, "\n")
	var result []string
	inFailure := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.Contains(trimmed, "Tests:") || strings.Contains(trimmed, "Snapshots:") ||
			strings.Contains(trimmed, "Time:") {
			result = append(result, trimmed)
		}
		if strings.HasPrefix(trimmed, "●") {
			result = append(result, trimmed)
			inFailure = true
		} else if inFailure && strings.HasPrefix(trimmed, "  ") {
			result = append(result, trimmed)
		} else if inFailure && !strings.HasPrefix(trimmed, "●") && !strings.HasPrefix(trimmed, "  ") {
			inFailure = false
		}
	}
	if len(result) == 0 {
		return "ok"
	}
	if len(result) > 30 {
		result = result[:30]
		result = append(result, "[... more failures]")
	}
	return strings.Join(result, "\n")
}

// ── Vitest ──────────────────────────────────────────────────────────────────

type VitestParser struct{}

func (p *VitestParser) Name() string { return "vitest" }

func (p *VitestParser) Match(output string) bool {
	return strings.Contains(output, "FAIL  ") || strings.Contains(output, "PASS  ") ||
		(strings.Contains(output, "Tests ") && strings.Contains(output, "Failed"))
}

func (p *VitestParser) Parse(output string) string {
	lines := strings.Split(output, "\n")
	var result []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "FAIL ") || strings.HasPrefix(trimmed, "PASS ") {
			result = append(result, trimmed)
		}
		if strings.Contains(trimmed, "Tests ") && (strings.Contains(trimmed, "fail") ||
			strings.Contains(trimmed, "pass")) {
			result = append(result, trimmed)
		}
		if strings.Contains(trimmed, "×") {
			result = append(result, trimmed)
		}
	}
	if len(result) == 0 {
		return "ok"
	}
	if len(result) > 30 {
		result = result[:30]
		result = append(result, "[... more]")
	}
	return strings.Join(result, "\n")
}

// ── Pytest ──────────────────────────────────────────────────────────────────

type PytestParser struct{}

func (p *PytestParser) Name() string { return "pytest" }

func (p *PytestParser) Match(output string) bool {
	return strings.Contains(output, "passed") && strings.Contains(output, "failed") &&
		strings.Contains(output, "==") && strings.Contains(output, "test session")
}

func (p *PytestParser) Parse(output string) string {
	lines := strings.Split(output, "\n")
	var result []string
	var lineCount int
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "FAILED") || strings.HasPrefix(trimmed, "ERROR") {
			result = append(result, trimmed)
			lineCount++
		} else if strings.Contains(trimmed, "passed") && strings.Contains(trimmed, "failed") {
			result = append(result, trimmed)
		} else if strings.Contains(trimmed, "short test summary") {
			result = append(result, trimmed)
		} else if strings.Contains(trimmed, "==") && strings.Contains(trimmed, "in ") &&
			strings.Contains(trimmed, "s") {
			result = append(result, trimmed)
		}
	}
	if lineCount == 0 && len(result) == 0 {
		return "ok"
	}
	return strings.Join(result, "\n")
}

// ── Playwright ──────────────────────────────────────────────────────────────

type PlaywrightParser struct{}

func (p *PlaywrightParser) Name() string { return "playwright" }

func (p *PlaywrightParser) Match(output string) bool {
	return strings.Contains(output, "Running ") && strings.Contains(output, "tests") &&
		(strings.Contains(output, "✓") || strings.Contains(output, "✗") ||
			strings.Contains(output, "×"))
}

func (p *PlaywrightParser) Parse(output string) string {
	lines := strings.Split(output, "\n")
	var result []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.Contains(trimmed, "✗") || strings.Contains(trimmed, "×") ||
			strings.Contains(trimmed, "failed") {
			result = append(result, trimmed)
		}
	}
	if strings.Contains(output, "passed") && len(result) == 0 {
		// All passed, just show summary
		for _, line := range lines {
			if strings.Contains(line, "passed") && strings.Contains(line, "total") {
				return line
			}
		}
		return "ok"
	}
	if len(result) == 0 {
		return "ok"
	}
	return strings.Join(result, "\n")
}

// ── RSpec ───────────────────────────────────────────────────────────────────

type RSpecParser struct{}

func (p *RSpecParser) Name() string { return "rspec" }

func (p *RSpecParser) Match(output string) bool {
	return strings.Contains(output, "examples") && strings.Contains(output, "failures")
}

func (p *RSpecParser) Parse(output string) string {
	lines := strings.Split(output, "\n")
	var result []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "examples") && strings.Contains(line, "failures") {
			result = append(result, line)
		} else if strings.HasPrefix(line, "  # ") || strings.HasPrefix(line, "  FAILED") {
			result = append(result, line)
		}
	}
	if len(result) == 0 {
		return "ok"
	}
	return strings.Join(result, "\n")
}

// ── Rake ────────────────────────────────────────────────────────────────────

type RakeParser struct{}

func (p *RakeParser) Name() string { return "rake" }

func (p *RakeParser) Match(output string) bool {
	return strings.Contains(output, "rake ") && strings.Contains(output, "FAIL") ||
		(strings.Contains(output, "tests") && strings.Contains(output, "assertions"))
}

func (p *RakeParser) Parse(output string) string {
	lines := strings.Split(output, "\n")
	var result []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "FAIL") || strings.Contains(line, "Error") {
			result = append(result, line)
		} else if strings.Contains(line, "tests") && strings.Contains(line, "assertions") {
			result = append(result, line)
		}
	}
	if len(result) == 0 {
		return "ok"
	}
	return strings.Join(result, "\n")
}
