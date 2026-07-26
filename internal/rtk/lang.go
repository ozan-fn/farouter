package rtk

import (
	"regexp"
	"strings"
)

// ── dotnet ──────────────────────────────────────────────────────────────────

type DotnetParser struct{}

func (p *DotnetParser) Name() string { return "dotnet" }

func (p *DotnetParser) Match(output string) bool {
	return strings.Contains(output, "dotnet ") || strings.Contains(output, "Build succeeded") ||
		strings.Contains(output, "Build FAILED")
}

func (p *DotnetParser) Parse(output string) string {
	lines := strings.Split(output, "\n")
	var result []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.Contains(trimmed, "error") || strings.Contains(trimmed, "warning") ||
			strings.Contains(trimmed, "Build succeeded") || strings.Contains(trimmed, "Build FAILED") ||
			strings.Contains(trimmed, "→") {
			result = append(result, trimmed)
		}
	}
	if len(result) == 0 {
		return "ok"
	}
	return strings.Join(result, "\n")
}

// ── mvn ─────────────────────────────────────────────────────────────────────

type MvnParser struct{}

func (p *MvnParser) Name() string { return "mvn" }

func (p *MvnParser) Match(output string) bool {
	return strings.Contains(output, "BUILD SUCCESS") || strings.Contains(output, "BUILD FAILURE") ||
		regexMatch(output, `\[INFO\]|\[ERROR\]|\[WARNING\]`)
}

func (p *MvnParser) Parse(output string) string {
	lines := strings.Split(output, "\n")
	var result []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "[ERROR]") || strings.HasPrefix(trimmed, "[WARNING]") ||
			strings.HasPrefix(trimmed, "BUILD ") || strings.HasPrefix(trimmed, "[INFO] BUILD") {
			result = append(result, trimmed)
		}
	}
	if len(result) == 0 {
		return "BUILD SUCCESS"
	}
	return strings.Join(result, "\n")
}

// ── gradlew ─────────────────────────────────────────────────────────────────

type GradlewParser struct{}

func (p *GradlewParser) Name() string { return "gradlew" }

func (p *GradlewParser) Match(output string) bool {
	return strings.Contains(output, "BUILD SUCCESSFUL") || strings.Contains(output, "BUILD FAILED") ||
		strings.Contains(output, "FAILURE: Build failed")
}

func (p *GradlewParser) Parse(output string) string {
	lines := strings.Split(output, "\n")
	var result []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "BUILD ") || strings.HasPrefix(trimmed, "FAILURE:") ||
			strings.Contains(trimmed, "error:") || strings.Contains(trimmed, "warning:") {
			result = append(result, trimmed)
		}
	}
	if len(result) == 0 {
		return "BUILD SUCCESSFUL"
	}
	return strings.Join(result, "\n")
}

// ── ruff ────────────────────────────────────────────────────────────────────

type RuffParser struct{}

func (p *RuffParser) Name() string { return "ruff" }

func (p *RuffParser) Match(output string) bool {
	return strings.Contains(output, "ruff") || strings.Contains(output, "Found ") &&
		strings.Contains(output, "error(s)")
}

func (p *RuffParser) Parse(output string) string {
	lines := strings.Split(output, "\n")
	var result []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.Contains(trimmed, ".py:") || strings.Contains(trimmed, "Found ") ||
			strings.Contains(trimmed, "error") || strings.Contains(trimmed, "warning") {
			result = append(result, trimmed)
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

// ── mypy ────────────────────────────────────────────────────────────────────

type MypyParser struct{}

func (p *MypyParser) Name() string { return "mypy" }

func (p *MypyParser) Match(output string) bool {
	return strings.Contains(output, "mypy") || strings.Contains(output, "Found ") &&
		strings.Contains(output, "error")
}

func (p *MypyParser) Parse(output string) string {
	lines := strings.Split(output, "\n")
	var result []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.Contains(trimmed, ".py:") || strings.Contains(trimmed, "Found ") {
			result = append(result, trimmed)
		}
	}
	if len(result) == 0 {
		return "ok"
	}
	return strings.Join(result, "\n")
}

// ── rubocop ─────────────────────────────────────────────────────────────────

type RubocopParser struct{}

func (p *RubocopParser) Name() string { return "rubocop" }

func (p *RubocopParser) Match(output string) bool {
	return strings.Contains(output, "Offenses:") || strings.Contains(output, "rubocop")
}

func (p *RubocopParser) Parse(output string) string {
	lines := strings.Split(output, "\n")
	var result []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.Contains(trimmed, "offense") || strings.Contains(trimmed, "files inspected") ||
			strings.Contains(trimmed, ".rb:") {
			result = append(result, trimmed)
		}
	}
	if len(result) == 0 {
		return "ok"
	}
	return strings.Join(result, "\n")
}

// ── gh ──────────────────────────────────────────────────────────────────────

type GhParser struct{}

func (p *GhParser) Name() string { return "gh" }

func (p *GhParser) Match(output string) bool {
	return strings.Contains(output, "gh: ") || strings.Contains(output, "To ") &&
		strings.Contains(output, "https://github.com") || strings.Contains(output, "#") &&
		strings.Contains(output, "Merged")
}

func (p *GhParser) Parse(output string) string {
	lines := strings.Split(output, "\n")
	var result []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.Contains(trimmed, "merged") || strings.Contains(trimmed, "created") ||
			strings.Contains(trimmed, "closed") || strings.Contains(trimmed, "opened") ||
			strings.Contains(trimmed, "approved") || strings.Contains(trimmed, "✓") ||
			strings.HasPrefix(trimmed, "https://") || strings.HasPrefix(trimmed, "To ") {
			result = append(result, trimmed)
		}
	}
	if len(result) == 0 {
		return "ok"
	}
	return strings.Join(result, "\n")
}

// ── glab ────────────────────────────────────────────────────────────────────

type GlabParser struct{}

func (p *GlabParser) Name() string { return "glab" }

func (p *GlabParser) Match(output string) bool {
	return strings.Contains(output, "glab:") || strings.Contains(output, "glab") &&
		strings.Contains(output, "merge request")
}

func (p *GlabParser) Parse(output string) string {
	lines := strings.Split(output, "\n")
	var result []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.Contains(trimmed, "!") || strings.Contains(trimmed, "merged") ||
			strings.Contains(trimmed, "created") || strings.Contains(trimmed, "MR") {
			result = append(result, trimmed)
		}
	}
	if len(result) == 0 {
		return "ok"
	}
	return strings.Join(result, "\n")
}

// ── log ─────────────────────────────────────────────────────────────────────

type LogParser struct{}

func (p *LogParser) Name() string { return "log" }

func (p *LogParser) Match(output string) bool {
	// Detect log output: timestamps or log levels
	count := 0
	lines := strings.Split(output, "\n")
	for _, line := range lines[:min(20, len(lines))] {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, "ERROR") || strings.Contains(trimmed, "WARN") ||
			strings.Contains(trimmed, "INFO") || strings.Contains(trimmed, "TRACE") ||
			strings.Contains(trimmed, "DEBUG") || strings.Contains(trimmed, "FATAL") ||
			regexMatch(trimmed, `\d{4}-\d{2}-\d{2}`) || regexMatch(trimmed, `\d{2}:\d{2}:\d{2}`) {
			count++
		}
	}
	return count >= 3
}

func (p *LogParser) Parse(output string) string {
	lines := strings.Split(output, "\n")
	var errors []string
	var all []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.Contains(trimmed, "ERROR") || strings.Contains(trimmed, "FATAL") {
			errors = append(errors, trimmed)
		}
		if len(all) < 20 {
			all = append(all, trimmed)
		}
	}
	if len(errors) > 0 {
		if len(errors) > 20 {
			errors = errors[:20]
			errors = append(errors, "[... more errors]")
		}
		return strings.Join(errors, "\n")
	}
	if len(all) > 20 {
		all = all[:20]
		all = append(all, "[... truncated]")
	}
	return strings.Join(all, "\n")
}

func regexMatch(s, pattern string) bool {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return false
	}
	return re.MatchString(s)
}
