package rtk

import (
	"regexp"
	"strings"
)

// ── read ────────────────────────────────────────────────────────────────────

type ReadParser struct{}

func (p *ReadParser) Name() string { return "read" }

func (p *ReadParser) Match(output string) bool {
	return false // file content handled by generic truncation
}

func (p *ReadParser) Parse(output string) string { return output }

// ── smart ───────────────────────────────────────────────────────────────────

type SmartParser struct{}

func (p *SmartParser) Name() string { return "smart" }

func (p *SmartParser) Match(output string) bool {
	lines := strings.Split(output, "\n")
	if len(lines) < 3 || len(lines) > 30 {
		return false
	}
	// Smart summary: detect code-like content
	count := 0
	for _, line := range lines {
		if strings.Contains(line, "func ") || strings.Contains(line, "class ") ||
			strings.Contains(line, "import ") || strings.Contains(line, "def ") ||
			strings.Contains(line, "impl ") || strings.Contains(line, "fn ") {
			count++
		}
	}
	return count >= 2
}

func (p *SmartParser) Parse(output string) string {
	lines := strings.Split(output, "\n")
	var result []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "func ") || strings.HasPrefix(trimmed, "class ") ||
			strings.HasPrefix(trimmed, "def ") || strings.HasPrefix(trimmed, "impl ") ||
			strings.HasPrefix(trimmed, "fn ") || strings.HasPrefix(trimmed, "type ") ||
			strings.HasPrefix(trimmed, "type ") || strings.HasPrefix(trimmed, "struct ") ||
			strings.HasPrefix(trimmed, "interface ") || strings.HasPrefix(trimmed, "trait ") ||
			strings.HasPrefix(trimmed, "export ") || strings.HasPrefix(trimmed, "function ") {
			if len(result) < 10 {
				result = append(result, trimmed)
			}
		}
	}
	if len(result) == 0 {
		return "(empty)"
	}
	return strings.Join(result, "\n")
}

// ── diff ────────────────────────────────────────────────────────────────────

type DiffParser struct{}

func (p *DiffParser) Name() string { return "diff" }

func (p *DiffParser) Match(output string) bool {
	return strings.HasPrefix(output, "--- ") || strings.HasPrefix(output, "diff ") ||
		strings.Contains(output, "\n--- ") || regexp.MustCompile(`(?m)^@@ .+ @@$`).MatchString(output)
}

func (p *DiffParser) Parse(output string) string {
	lines := strings.Split(output, "\n")
	var result []string
	for _, line := range lines {
		if strings.HasPrefix(line, "--- ") || strings.HasPrefix(line, "+++ ") ||
			strings.HasPrefix(line, "diff ") || strings.HasPrefix(line, "index ") {
			continue
		}
		if strings.HasPrefix(line, "@@") || strings.HasPrefix(line, "+") || strings.HasPrefix(line, "-") {
			if len(result) < 60 {
				result = append(result, line)
			}
		}
	}
	if len(result) == 0 {
		return "(no changes)"
	}
	return strings.Join(result, "\n")
}

// ── rg ──────────────────────────────────────────────────────────────────────

type RgParser struct{}

func (p *RgParser) Name() string { return "rg" }

func (p *RgParser) Match(output string) bool {
	lines := strings.Split(output, "\n")
	if len(lines) < 2 || len(lines) > 500 {
		return false
	}
	// rg output: filename:linenum:content
	count := 0
	rgRe := regexp.MustCompile(`^[^:]+:\d+:`)
	for _, line := range lines[:min(20, len(lines))] {
		if rgRe.MatchString(line) {
			count++
		}
	}
	return count >= 3 && count >= len(lines)/3
}

func (p *RgParser) Parse(output string) string {
	lines := strings.Split(output, "\n")
	files := make(map[string]int)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		rgRe := regexp.MustCompile(`^([^:]+:\d+)`)
		if m := rgRe.FindString(line); m != "" {
			file := m
			files[file]++
		}
	}
	var result []string
	for file, count := range files {
		if len(result) >= 30 {
			break
		}
		result = append(result, file+" ("+itoa(count)+" match)")
	}
	if len(result) == 0 {
		return "(no matches)"
	}
	omitted := len(files) - len(result)
	if omitted > 0 {
		result = append(result, "[... "+itoa(omitted)+" more]")
	}
	return strings.Join(result, "\n")
}

// ── npx ─────────────────────────────────────────────────────────────────────

type NpxParser struct{}

func (p *NpxParser) Name() string { return "npx" }

func (p *NpxParser) Match(output string) bool {
	return strings.Contains(output, "npx: ") || strings.Contains(output, "Need to install") ||
		strings.Contains(output, "up to date") || regexp.MustCompile(`(?m)^npx `).MatchString(output)
}

func (p *NpxParser) Parse(output string) string {
	lines := strings.Split(output, "\n")
	var result []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.Contains(trimmed, "│") || strings.Contains(trimmed, "progress") {
			continue
		}
		if strings.Contains(trimmed, "added") || strings.Contains(trimmed, "removed") ||
			strings.Contains(trimmed, "up to date") || strings.Contains(trimmed, "error") ||
			strings.Contains(trimmed, "Error") || strings.Contains(trimmed, "warn") {
			result = append(result, trimmed)
		}
	}
	if len(result) == 0 {
		return "ok"
	}
	return strings.Join(result, "\n")
}

// ── wget ────────────────────────────────────────────────────────────────────

type WgetParser struct{}

func (p *WgetParser) Name() string { return "wget" }

func (p *WgetParser) Match(output string) bool {
	return strings.Contains(output, "Resolving ") || strings.Contains(output, "Connecting to ") ||
		strings.Contains(output, "HTTP request sent") || strings.Contains(output, "Saving to:")
}

func (p *WgetParser) Parse(output string) string {
	lines := strings.Split(output, "\n")
	var result []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.Contains(trimmed, "Saving to:") || strings.Contains(trimmed, "saved") ||
			strings.Contains(trimmed, "ERROR") || strings.Contains(trimmed, "failed") ||
			strings.Contains(trimmed, "100%") && strings.Contains(trimmed, "=") {
			result = append(result, trimmed)
		}
	}
	if len(result) == 0 {
		return "ok"
	}
	return strings.Join(result, "\n")
}

// ── oc ──────────────────────────────────────────────────────────────────────

type OcParser struct{}

func (p *OcParser) Name() string { return "oc" }

func (p *OcParser) Match(output string) bool {
	return strings.Contains(output, "oc ") || strings.Contains(output, "oc:") ||
		(strings.Contains(output, "NAME") && strings.Contains(output, "READY") &&
			strings.Contains(output, "STATUS"))
}

func (p *OcParser) Parse(output string) string {
	lines := strings.Split(output, "\n")
	var result []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.Contains(trimmed, "NAME") && strings.Contains(trimmed, "READY") ||
			strings.Contains(trimmed, "No resources") {
			result = append(result, trimmed)
			continue
		}
		fields := strings.Fields(trimmed)
		if len(fields) >= 3 {
			name := fields[0]
			status := ""
			for _, f := range fields {
				if f == "Running" || f == "Pending" || f == "CrashLoopBackOff" ||
					f == "Error" || f == "Completed" || f == "Terminating" ||
					f == "ContainerCreating" || f == "Init:Error" || f == "ImagePullBackOff" {
					status = f
					break
				}
			}
			if status != "" {
				result = append(result, name+" "+status)
			}
		}
	}
	if len(result) == 0 {
		return "(no resources)"
	}
	if len(result) > 30 {
		result = result[:30]
		result = append(result, "[... more]")
	}
	return strings.Join(result, "\n")
}

// ── psql ────────────────────────────────────────────────────────────────────

type PsqlParser struct{}

func (p *PsqlParser) Name() string { return "psql" }

func (p *PsqlParser) Match(output string) bool {
	return strings.Contains(output, " psql:") || strings.Contains(output, " psql ") ||
		(strings.Contains(output, "(") && strings.Contains(output, "rows)") &&
			strings.Contains(output, "---"))
}

func (p *PsqlParser) Parse(output string) string {
	lines := strings.Split(output, "\n")
	var result []string
	seenHeader := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.Contains(trimmed, "---") && strings.Contains(trimmed, "|") {
			seenHeader = true
			continue
		}
		if strings.Contains(trimmed, "(") && strings.Contains(trimmed, "rows)") {
			result = append(result, trimmed)
			continue
		}
		if seenHeader {
			result = append(result, trimmed)
		}
	}
	if len(result) == 0 {
		return "(empty result)"
	}
	if len(result) > 20 {
		result = result[:20]
		result = append(result, "[... more rows]")
	}
	return strings.Join(result, "\n")
}

// ── prisma ──────────────────────────────────────────────────────────────────

type PrismaParser struct{}

func (p *PrismaParser) Name() string { return "prisma" }

func (p *PrismaParser) Match(output string) bool {
	return strings.Contains(output, "prisma:") || strings.Contains(output, "Prisma ") ||
		strings.Contains(output, "Generated Prisma Client") || strings.Contains(output, "prisma generate") ||
		strings.Contains(output, "npx prisma") || strings.Contains(output, "datasource")
}

func (p *PrismaParser) Parse(output string) string {
	lines := strings.Split(output, "\n")
	var result []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.Contains(trimmed, "│") || strings.Contains(trimmed, "progress") {
			continue
		}
		if strings.Contains(trimmed, "Generated") || strings.Contains(trimmed, "✔") ||
			strings.Contains(trimmed, "✓") || strings.Contains(trimmed, "error") ||
			strings.Contains(trimmed, "Error") || strings.Contains(trimmed, "warn") ||
			strings.Contains(trimmed, "warning") || strings.Contains(trimmed, "Your Prisma") {
			result = append(result, trimmed)
		}
	}
	if len(result) == 0 {
		return "ok"
	}
	return strings.Join(result, "\n")
}

// ── gt ──────────────────────────────────────────────────────────────────────

type GtParser struct{}

func (p *GtParser) Name() string { return "gt" }

func (p *GtParser) Match(output string) bool {
	return strings.HasPrefix(output, "gt: ") || strings.Contains(output, "Creating branch") ||
		strings.Contains(output, "Stack:") || strings.Contains(output, "Branch:") ||
		strings.Contains(output, "Pull request created")
}

func (p *GtParser) Parse(output string) string {
	lines := strings.Split(output, "\n")
	var result []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.Contains(trimmed, "✓") || strings.Contains(trimmed, "✔") ||
			strings.Contains(trimmed, "created") || strings.Contains(trimmed, "updated") ||
			strings.Contains(trimmed, "Stack:") || strings.Contains(trimmed, "Branch:") ||
			strings.Contains(trimmed, "error") || strings.Contains(trimmed, "Error") {
			result = append(result, trimmed)
		}
	}
	if len(result) == 0 {
		return "ok"
	}
	return strings.Join(result, "\n")
}

// ── err ─────────────────────────────────────────────────────────────────────

type ErrParser struct{}

func (p *ErrParser) Name() string { return "err" }

func (p *ErrParser) Match(output string) bool {
	// err output: only errors/warnings remain
	count := 0
	lines := strings.Split(output, "\n")
	for _, line := range lines[:min(10, len(lines))] {
		if strings.Contains(line, "error") || strings.Contains(line, "Error") ||
			strings.Contains(line, "ERROR") || strings.Contains(line, "FAIL") ||
			strings.Contains(line, "fail") || strings.Contains(line, "warn") ||
			strings.Contains(line, "WARN") || strings.Contains(line, "Warning") {
			count++
		}
	}
	return count >= 2 && count >= len(lines)/2
}

func (p *ErrParser) Parse(output string) string {
	lines := strings.Split(output, "\n")
	var result []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.Contains(trimmed, "error") || strings.Contains(trimmed, "Error") ||
			strings.Contains(trimmed, "ERROR") || strings.Contains(trimmed, "FAIL") ||
			strings.Contains(trimmed, "fail") || strings.Contains(trimmed, "warn") ||
			strings.Contains(trimmed, "WARN") || strings.Contains(trimmed, "Warning") {
			if len(result) < 20 {
				result = append(result, trimmed)
			}
		}
	}
	if len(result) == 0 {
		return "ok (no errors)"
	}
	return strings.Join(result, "\n")
}

// ── summary ─────────────────────────────────────────────────────────────────

type SummaryParser struct{}

func (p *SummaryParser) Name() string { return "summary" }

func (p *SummaryParser) Match(output string) bool {
	// Heuristic summary: tool output that has been summarized to just a few lines
	lines := strings.Split(output, "\n")
	if len(lines) > 30 || len(lines) < 2 {
		return false
	}
	// Check for summary markers
	markers := 0
	for _, line := range lines {
		if strings.Contains(line, "summary:") || strings.Contains(line, "Summary:") ||
			strings.Contains(line, "output:") || strings.Contains(line, "Output:") ||
			strings.Contains(line, "result:") || strings.Contains(line, "Result:") {
			markers++
		}
	}
	countLines(output)
	return markers >= 1
}

func (p *SummaryParser) Parse(output string) string {
	return output // already summarized
}

// ── format ──────────────────────────────────────────────────────────────────

type FormatParser struct{}

func (p *FormatParser) Name() string { return "format" }

func (p *FormatParser) Match(output string) bool {
	return strings.Contains(output, "prettier") && strings.Contains(output, "black") ||
		strings.Contains(output, "ruff format") || strings.Contains(output, "format") &&
		(strings.Contains(output, "checked") || strings.Contains(output, "reformatted"))
}

func (p *FormatParser) Parse(output string) string {
	lines := strings.Split(output, "\n")
	var result []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.Contains(trimmed, "reformatted") || strings.Contains(trimmed, "checked") ||
			strings.Contains(trimmed, "error") || strings.Contains(trimmed, "unformatted") {
			result = append(result, trimmed)
		}
	}
	if len(result) == 0 {
		return "ok (all formatted)"
	}
	return strings.Join(result, "\n")
}

// ── json ────────────────────────────────────────────────────────────────────

type JsonParser struct{}

func (p *JsonParser) Name() string { return "json" }

func (p *JsonParser) Match(output string) bool {
	trimmed := strings.TrimSpace(output)
	return strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")
}

func (p *JsonParser) Parse(output string) string {
	lines := strings.Split(output, "\n")
	if len(lines) <= 20 {
		return output
	}
	truncated := strings.Join(lines[:20], "\n")
	omitted := len(lines) - 20
	return truncated + "\n[... " + itoa(omitted) + " lines]"
}

// ── deps ────────────────────────────────────────────────────────────────────

type DepsParser struct{}

func (p *DepsParser) Name() string { return "deps" }

func (p *DepsParser) Match(output string) bool {
	return strings.Contains(output, "dependencies") || strings.Contains(output, "devDependencies") ||
		strings.Contains(output, "package.json") || strings.Contains(output, "go.mod") ||
		strings.Contains(output, "Cargo.toml") || strings.Contains(output, "requirements.txt")
}

func (p *DepsParser) Parse(output string) string {
	lines := strings.Split(output, "\n")
	var result []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.Contains(trimmed, "\"") || strings.Contains(trimmed, "version") ||
			strings.Contains(trimmed, "name") || strings.Contains(trimmed, "license") {
			if len(result) < 15 {
				result = append(result, trimmed)
			}
		}
	}
	if len(result) == 0 {
		return "(empty)"
	}
	return strings.Join(result, "\n")
}
