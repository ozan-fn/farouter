package rtk

import (
	"log"
	"strings"
)

// Parser defines interface for tool output parsers
type Parser interface {
	Name() string
	Match(output string) bool
	Parse(output string) string
}

var parsers = []Parser{
	&GitStatusParser{},
	&GitLogParser{},
	&GitDiffParser{},
	&GitPushParser{},
	&GitPullParser{},
	&GitAddParser{},
	&GitCommitParser{},
	&GitStashParser{},
	&GitBranchParser{},
	&GitFetchParser{},
	&GitShowParser{},
	&GitWorktreeParser{},
	&LsParser{},
	&TreeParser{},
	&FindParser{},
	&GoTestParser{},
	&GoBuildParser{},
	&GoVetParser{},
	&CargoTestParser{},
	&CargoBuildParser{},
	&CargoClippyParser{},
	&JestParser{},
	&VitestParser{},
	&PytestParser{},
	&PlaywrightParser{},
	&NpmParser{},
	&PnpmParser{},
	&BunParser{},
	&CurlParser{},
	&KubectlParser{},
	&OcParser{},
	&DockerPsParser{},
	&DockerImagesParser{},
	&DockerComposeParser{},
	&DockerLogsParser{},
	&PodmanParser{},
	&AwsParser{},
	&GcloudParser{},
	&DotnetParser{},
	&MvnParser{},
	&GradlewParser{},
	&RuffParser{},
	&MypyParser{},
	&RubocopParser{},
	&TscParser{},
	&EslintParser{},
	&PrettierParser{},
	&GolangciLintParser{},
	&GhParser{},
	&GlabParser{},
	&PrismaParser{},
	&PsqlParser{},
	&MysqlParser{},
	&GenericParser{maxLines: 30},
}

// ProcessOutput applies RTK-style filtering to tool output
func ProcessOutput(output string) string {
	output = strings.TrimSpace(output)
	if output == "" {
		return "(empty)"
	}

	origLines := countLines(output)

	for _, p := range parsers {
		if p.Match(output) {
			result := p.Parse(output)
			newLines := countLines(result)
			log.Printf("[rtk] %s: %d→%d lines (-%d%%)", p.Name(), origLines, newLines,
				percent(origLines-newLines, origLines))
			return result
		}
	}

	return truncateOutput(output, 30)
}

func truncateOutput(output string, maxLines int) string {
	lines := strings.Split(output, "\n")
	if len(lines) <= maxLines {
		return output
	}
	truncated := strings.Join(lines[:maxLines], "\n")
	omitted := len(lines) - maxLines
	return truncated + "\n\n[... " + itoa(omitted) + " lines omitted]"
}

func percent(diff, total int) int {
	if total <= 0 {
		return 0
	}
	return diff * 100 / total
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf) - 1
	for n > 0 {
		buf[i] = byte('0' + n%10)
		n /= 10
		i--
	}
	return string(buf[i+1:])
}

func countLines(s string) int {
	n := 1
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			n++
		}
	}
	return n
}
