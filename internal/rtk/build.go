package rtk

import (
	"regexp"
	"strings"
)

// ── BuildOutputParser — port of VansRouter filters/buildOutput.js ─────
type BuildOutputParser struct{}

func (p *BuildOutputParser) Name() string { return "build-output" }

func (p *BuildOutputParser) Parse(input string) string {
	lines := strings.Split(input, "\n")
	if len(lines) == 0 {
		return input
	}

	var errors, warnings, deprecations []string
	var summary []string
	compilingCount := 0
	downloadingCount := 0
	inCargoError := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Continuation of cargo error block
		if inCargoError {
			if trimmed == "" {
				inCargoError = false
				continue
			}
			if reCargoCont.MatchString(line) {
				errors = append(errors, line)
				continue
			}
			inCargoError = false
		}
		if trimmed == "" {
			continue
		}

		if reNpmErr.MatchString(trimmed) {
			errors = append(errors, line)
			continue
		}
		if reNpmDeprecate.MatchString(trimmed) {
			deprecations = append(deprecations, line)
			continue
		}
		if reNpmWarn.MatchString(trimmed) {
			warnings = append(warnings, line)
			continue
		}
		if reErrorLine.MatchString(trimmed) {
			errors = append(errors, line)
			inCargoError = true
			continue
		}
		if reWarningLine.MatchString(trimmed) {
			warnings = append(warnings, line)
			inCargoError = true
			continue
		}
		if reErrorPrefix.MatchString(trimmed) {
			errors = append(errors, line)
			continue
		}
		if reBuildFailed.MatchString(trimmed) || reBuildFailBracket.MatchString(trimmed) {
			errors = append(errors, line)
			continue
		}
		if reWarningBracket.MatchString(trimmed) {
			warnings = append(warnings, line)
			continue
		}
		if reCompiling.MatchString(trimmed) {
			compilingCount++
			continue
		}
		if reDownloading.MatchString(trimmed) {
			downloadingCount++
			continue
		}
		if reSummaryLine.MatchString(trimmed) ||
			reBuildSuccess.MatchString(trimmed) ||
			reVulnCount.MatchString(trimmed) ||
			reSuccessInstall.MatchString(trimmed) ||
			reNpmAudit.MatchString(trimmed) ||
			reFunding.MatchString(trimmed) {
			summary = append(summary, line)
			continue
		}
	}

	var out strings.Builder
	keepDep := deprecations
	if len(keepDep) > 3 {
		keepDep = keepDep[:3]
	}
	for _, d := range keepDep {
		out.WriteString(d)
		out.WriteString("\n")
	}
	if len(deprecations) > 3 {
		out.WriteString("... +")
		out.WriteString(itoa(len(deprecations) - 3))
		out.WriteString(" more deprecated packages\n")
	}
	if compilingCount > 0 {
		out.WriteString("Compiled ")
		out.WriteString(itoa(compilingCount))
		out.WriteString(" packages\n")
	}
	if downloadingCount > 0 {
		out.WriteString("Downloaded ")
		out.WriteString(itoa(downloadingCount))
		out.WriteString(" packages\n")
	}
	for _, e := range errors {
		out.WriteString(e)
		out.WriteString("\n")
	}
	keepWarnings := warnings
	if len(keepWarnings) > 5 {
		keepWarnings = keepWarnings[:5]
	}
	for _, w := range keepWarnings {
		out.WriteString(w)
		out.WriteString("\n")
	}
	if len(warnings) > 5 {
		out.WriteString("... +")
		out.WriteString(itoa(len(warnings) - 5))
		out.WriteString(" more warnings\n")
	}
	for _, s := range summary {
		out.WriteString(s)
		out.WriteString("\n")
	}
	result := strings.TrimRight(out.String(), "\n")
	if result == "" {
		return input
	}
	return result
}

var (
	reCargoCont     = regexp.MustCompile(`^\s*(-->|\||\d+\s*\||=)`)
	reNpmErr        = regexp.MustCompile(`(?i)^npm (ERR!|error)`)
	reNpmDeprecate  = regexp.MustCompile(`(?i)^npm warn deprecated`)
	reNpmWarn       = regexp.MustCompile(`(?i)^npm warn|^yarn warn`)
	reErrorLine     = regexp.MustCompile(`(?i)^error(\[|:)|^error -->`)
	reWarningLine   = regexp.MustCompile(`(?i)^warning(\[|:)|^warning -->`)
	reErrorPrefix   = regexp.MustCompile(`(?i)^ERROR:`)
	reBuildFailed   = regexp.MustCompile(`(?i)^BUILD FAILED`)
	reBuildFailBracket = regexp.MustCompile(`(?i)^\[ERROR\]`)
	reWarningBracket = regexp.MustCompile(`(?i)^\[WARNING\]`)
	reCompiling     = regexp.MustCompile(`(?i)^\s*Compiling\s+\S+`)
	reDownloading   = regexp.MustCompile(`(?i)^\s*(Downloading\s+\S+|Fetching\s+)`)
	reSummaryLine   = regexp.MustCompile(`(?i)^(added|removed|changed|audited|installed)\s+\d+\s+package`)
	reBuildSuccess  = regexp.MustCompile(`(?i)^\s*Finished\s+|^(BUILD SUCCESS|BUILD SUCCESSFUL)`)
	reVulnCount     = regexp.MustCompile(`^\d+\s+(vulnerabilities|packages?|warnings?|errors?)`)
	reSuccessInstall = regexp.MustCompile(`(?i)^Successfully (installed|built)`)
	reNpmAudit      = regexp.MustCompile(`(?i)^(To address .* issues|Run \x60npm (audit|fund)\x60)`)
	reFunding       = regexp.MustCompile(`packages are looking for funding`)
)
