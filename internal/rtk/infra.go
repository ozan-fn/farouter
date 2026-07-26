package rtk

import (
	"regexp"
	"strings"
)

// ── npm ─────────────────────────────────────────────────────────────────────

type NpmParser struct{}

func (p *NpmParser) Name() string { return "npm" }

func (p *NpmParser) Match(output string) bool {
	return strings.Contains(output, "npm ") || strings.Contains(output, "added ") ||
		strings.Contains(output, "removed ") || strings.Contains(output, "up to date")
}

func (p *NpmParser) Parse(output string) string {
	lines := strings.Split(output, "\n")
	var result []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, ">") || strings.Contains(line, "│") {
			continue
		}
		if strings.Contains(line, "added ") || strings.Contains(line, "removed ") ||
			strings.Contains(line, "up to date") || strings.Contains(line, "vulnerabilities") ||
			strings.Contains(line, "packages") {
			result = append(result, line)
		}
	}
	if len(result) == 0 {
		return "ok"
	}
	return strings.Join(result, "\n")
}

// ── docker ps ───────────────────────────────────────────────────────────────

type DockerPsParser struct{}

func (p *DockerPsParser) Name() string { return "docker-ps" }

func (p *DockerPsParser) Match(output string) bool {
	return strings.Contains(output, "CONTAINER ID") && strings.Contains(output, "IMAGE")
}

func (p *DockerPsParser) Parse(output string) string {
	lines := strings.Split(output, "\n")
	var result []string
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if i == 0 || strings.Contains(line, "CONTAINER ID") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 7 {
			continue
		}

		name := fields[len(fields)-1]
		id := fields[0][:12]
		image := fields[1]

		status := ""
		for j := 3; j < len(fields)-2; j++ {
			if fields[j] == "Up" || fields[j] == "Exited" || fields[j] == "Created" ||
				fields[j] == "Paused" || fields[j] == "Restarting" || fields[j] == "Removing" {
				status = fields[j]
				for k := j + 1; k < len(fields)-1; k++ {
					if strings.Contains(fields[k], ":") || strings.HasPrefix(fields[k], "0.0.0.0:") {
						break
					}
					status += " " + fields[k]
				}
				break
			}
		}
		compact := id + " " + image
		if status != "" {
			compact += " (" + status + ")"
		}
		compact += " " + name
		result = append(result, compact)
	}
	if len(result) == 0 {
		return "(no containers)"
	}
	return strings.Join(result, "\n")
}

// ── docker images ───────────────────────────────────────────────────────────

type DockerImagesParser struct{}

func (p *DockerImagesParser) Name() string { return "docker-images" }

func (p *DockerImagesParser) Match(output string) bool {
	return strings.Contains(output, "REPOSITORY") && strings.Contains(output, "TAG") &&
		strings.Contains(output, "IMAGE ID")
}

func (p *DockerImagesParser) Parse(output string) string {
	lines := strings.Split(output, "\n")
	var result []string
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if i == 0 || strings.Contains(line, "REPOSITORY") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		repo := fields[0]
		tag := fields[1]
		id := fields[2][:12]
		size := fields[len(fields)-1]
		compact := repo + ":" + tag + " " + id + " " + size
		result = append(result, compact)
	}
	if len(result) == 0 {
		return "(no images)"
	}
	return strings.Join(result, "\n")
}

// ── git stash ───────────────────────────────────────────────────────────────

type GitStashParser struct{}

func (p *GitStashParser) Name() string { return "git-stash" }

func (p *GitStashParser) Match(output string) bool {
	return strings.Contains(output, "stash@{") || strings.Contains(output, "Saved working directory")
}

func (p *GitStashParser) Parse(output string) string {
	lines := strings.Split(output, "\n")
	var result []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.Contains(trimmed, "stash@{") {
			// stash@{0}: On main: fixed bug
			re := regexp.MustCompile(`stash@\{(\d+)\}:.*?: (.+)`)
			if matches := re.FindStringSubmatch(trimmed); len(matches) > 2 {
				result = append(result, "stash["+matches[1]+"]: "+matches[2])
			} else {
				result = append(result, trimmed)
			}
		} else if strings.Contains(trimmed, "Saved") {
			result = append(result, "ok (saved)")
		} else if trimmed == "No local changes to save" {
			result = append(result, trimmed)
		} else if strings.Contains(trimmed, "Dropped") {
			result = append(result, trimmed)
		}
	}
	if len(result) == 0 {
		return "ok"
	}
	return strings.Join(result, "\n")
}

// ── git branch ──────────────────────────────────────────────────────────────

type GitBranchParser struct{}

func (p *GitBranchParser) Name() string { return "git-branch" }

func (p *GitBranchParser) Match(output string) bool {
	lines := strings.Split(output, "\n")
	if len(lines) < 2 {
		return false
	}
	count := 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && (strings.HasPrefix(trimmed, "* ") || strings.HasPrefix(trimmed, "  ") ||
			strings.HasPrefix(trimmed, "+ ")) {
			count++
		}
	}
	return count >= len(lines)/2
}

func (p *GitBranchParser) Parse(output string) string {
	lines := strings.Split(output, "\n")
	var result []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "* ") || strings.HasPrefix(trimmed, "  ") ||
			strings.HasPrefix(trimmed, "+ ") {
			result = append(result, trimmed)
		}
	}
	if len(result) == 0 {
		return "(no branches)"
	}
	return strings.Join(result, "\n")
}

// ── git fetch ───────────────────────────────────────────────────────────────

type GitFetchParser struct{}

func (p *GitFetchParser) Name() string { return "git-fetch" }

func (p *GitFetchParser) Match(output string) bool {
	return strings.Contains(output, "Fetching ") || strings.HasPrefix(output, "From ") ||
		strings.Contains(output, "->")
}

func (p *GitFetchParser) Parse(output string) string {
	lines := strings.Split(output, "\n")
	var result []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.Contains(trimmed, "->") {
			re := regexp.MustCompile(`\[\w+.*\]`)
			if re.MatchString(trimmed) {
				result = append(result, trimmed)
			}
		}
	}
	if len(result) == 0 {
		return "ok"
	}
	if len(result) > 5 {
		result = result[:5]
		result = append(result, "[... more]")
	}
	return strings.Join(result, "\n")
}

// ── git show ────────────────────────────────────────────────────────────────

type GitShowParser struct{}

func (p *GitShowParser) Name() string { return "git-show" }

func (p *GitShowParser) Match(output string) bool {
	return strings.HasPrefix(output, "commit ") && strings.Contains(output, "Author:") &&
		strings.Contains(output, "Date:")
}

func (p *GitShowParser) Parse(output string) string {
	lines := strings.Split(output, "\n")
	var result []string
	var inDiff bool
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "commit ") || strings.HasPrefix(trimmed, "Author:") ||
			strings.HasPrefix(trimmed, "Date:") || strings.HasPrefix(trimmed, "    ") &&
			!strings.HasPrefix(trimmed, "    diff") && !inDiff {
			result = append(result, trimmed)
			continue
		}
		if strings.HasPrefix(trimmed, "diff --git") || strings.HasPrefix(trimmed, "@@") {
			inDiff = true
			if len(result) < 30 {
				result = append(result, trimmed)
			}
		} else if inDiff && (strings.HasPrefix(trimmed, "+") || strings.HasPrefix(trimmed, "-")) &&
			!strings.HasPrefix(trimmed, "+++") && !strings.HasPrefix(trimmed, "---") {
			if len(result) < 50 {
				result = append(result, trimmed)
			}
		}
	}
	if len(result) == 0 {
		return "(empty)"
	}
	return strings.Join(result, "\n")
}

// ── git worktree ────────────────────────────────────────────────────────────

type GitWorktreeParser struct{}

func (p *GitWorktreeParser) Name() string { return "git-worktree" }

func (p *GitWorktreeParser) Match(output string) bool {
	return strings.Contains(output, "/") && strings.Contains(output, "[") &&
		strings.Contains(output, "]") && strings.Contains(output, "git worktree")
}

func (p *GitWorktreeParser) Parse(output string) string {
	lines := strings.Split(output, "\n")
	var result []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "git worktree") {
			continue
		}
		re := regexp.MustCompile(`(.+?)\s+\[(.+?)\]`)
		if matches := re.FindStringSubmatch(trimmed); len(matches) > 2 {
			result = append(result, matches[1]+" ["+matches[2]+"]")
		} else {
			result = append(result, trimmed)
		}
	}
	if len(result) == 0 {
		return "(no worktrees)"
	}
	return strings.Join(result, "\n")
}

// ── pnpm ────────────────────────────────────────────────────────────────────

type PnpmParser struct{}

func (p *PnpmParser) Name() string { return "pnpm" }

func (p *PnpmParser) Match(output string) bool {
	return strings.Contains(output, "pnpm") || strings.Contains(output, "Lockfile is up to date") ||
		strings.Contains(output, "Packages:") || strings.Contains(output, "progress:")
}

func (p *PnpmParser) Parse(output string) string {
	lines := strings.Split(output, "\n")
	var result []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "│") {
			continue
		}
		if strings.Contains(trimmed, "Packages:") || strings.Contains(trimmed, "Lockfile") ||
			strings.Contains(trimmed, "dependencies") || strings.Contains(trimmed, "up to date") ||
			strings.Contains(trimmed, "added") || strings.Contains(trimmed, "removed") {
			result = append(result, trimmed)
		}
	}
	if len(result) == 0 {
		return "ok"
	}
	return strings.Join(result, "\n")
}

// ── pip ─────────────────────────────────────────────────────────────────────

type PipParser struct{}

func (p *PipParser) Name() string { return "pip" }

func (p *PipParser) Match(output string) bool {
	return strings.Contains(output, "Collecting ") || strings.Contains(output, "Installing collected") ||
		strings.Contains(output, "Successfully installed")
}

func (p *PipParser) Parse(output string) string {
	lines := strings.Split(output, "\n")
	var result []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "Collecting ") || strings.HasPrefix(trimmed, "Installing ") ||
			strings.HasPrefix(trimmed, "Successfully installed") ||
			strings.HasPrefix(trimmed, "Requirement already") {
			result = append(result, trimmed)
		}
	}
	if len(result) == 0 {
		return "ok"
	}
	if len(result) > 10 {
		result = result[:10]
		result = append(result, "[... more]")
	}
	return strings.Join(result, "\n")
}

// ── curl ────────────────────────────────────────────────────────────────────

type CurlParser struct{}

func (p *CurlParser) Name() string { return "curl" }

func (p *CurlParser) Match(output string) bool {
	lines := strings.Split(output, "\n")
	if len(lines) < 2 {
		return false
	}
	// Detect JSON response
	first := strings.TrimSpace(lines[0])
	return strings.HasPrefix(first, "{") || strings.HasPrefix(first, "[") ||
		strings.Contains(output, "% Total")
}

func (p *CurlParser) Parse(output string) string {
	lines := strings.Split(output, "\n")
	// Try to extract JSON body
	var bodyLines []string
	started := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, "{") || strings.Contains(trimmed, "[") && !started {
			started = true
		}
		if started {
			bodyLines = append(bodyLines, trimmed)
		}
	}
	if len(bodyLines) > 0 {
		if len(bodyLines) <= 20 {
			return strings.Join(bodyLines, "\n")
		}
		truncated := strings.Join(bodyLines[:20], "\n")
		return truncated + "\n[... " + itoa(len(bodyLines)-20) + " lines]"
	}
	// No JSON, just truncate
	return truncateOutput(output, 30)
}

// ── kubectl ─────────────────────────────────────────────────────────────────

type KubectlParser struct{}

func (p *KubectlParser) Name() string { return "kubectl" }

func (p *KubectlParser) Match(output string) bool {
	return strings.Contains(output, "kubectl") || strings.Contains(output, "No resources found") ||
		(strings.Contains(output, "NAME") && strings.Contains(output, "READY") &&
			strings.Contains(output, "STATUS"))
}

func (p *KubectlParser) Parse(output string) string {
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

// ── docker compose ──────────────────────────────────────────────────────────

type DockerComposeParser struct{}

func (p *DockerComposeParser) Name() string { return "docker-compose" }

func (p *DockerComposeParser) Match(output string) bool {
	return strings.Contains(output, "Container") && strings.Contains(output, "Status") ||
		strings.Contains(output, "Network") && strings.Contains(output, "Created") ||
		strings.Contains(output, "service") && strings.Contains(output, "running")
}

func (p *DockerComposeParser) Parse(output string) string {
	lines := strings.Split(output, "\n")
	var result []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "Container") || strings.HasPrefix(trimmed, "Name") ||
			strings.HasPrefix(trimmed, "Service") {
			result = append(result, trimmed)
		}
		if strings.Contains(trimmed, "Up ") || strings.Contains(trimmed, "running") ||
			strings.Contains(trimmed, "exited") || strings.Contains(trimmed, "Exit") {
			result = append(result, trimmed)
		}
	}
	if len(result) == 0 {
		return "ok"
	}
	return strings.Join(result, "\n")
}

// ── docker logs ─────────────────────────────────────────────────────────────

type DockerLogsParser struct{}

func (p *DockerLogsParser) Name() string { return "docker-logs" }

func (p *DockerLogsParser) Match(output string) bool {
	lines := strings.Split(output, "\n")
	return len(lines) >= 5 && strings.Contains(output, "202") || len(lines) >= 5 &&
		regexp.MustCompile(`\d{4}-\d{2}-\d{2}`).MatchString(output)
}

func (p *DockerLogsParser) Parse(output string) string {
	lines := strings.Split(output, "\n")
	if len(lines) <= 30 {
		// Show last 30 lines
		return strings.Join(lines, "\n")
	}
	// Show last 30 lines
	last := lines[len(lines)-30:]
	return "... (showing last 30 lines)\n" + strings.Join(last, "\n")
}

// ── aws ─────────────────────────────────────────────────────────────────────

type AwsParser struct{}

func (p *AwsParser) Name() string { return "aws" }

func (p *AwsParser) Match(output string) bool {
	return strings.Contains(output, "An error occurred") ||
		strings.HasPrefix(strings.TrimSpace(output), "{") ||
		strings.Contains(output, "export AWS")
}

func (p *AwsParser) Parse(output string) string {
	lines := strings.Split(output, "\n")
	var result []string
	jsonStarted := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.Contains(trimmed, "An error occurred") {
			result = append(result, trimmed)
		}
		if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
			jsonStarted = true
		}
		if jsonStarted {
			if len(result) < 20 {
				result = append(result, trimmed)
			}
		}
	}
	if len(result) == 0 {
		return "(empty)"
	}
	if len(result) > 20 {
		result = result[:20]
		omitted := len(lines) - 20
		result = append(result, "[... "+itoa(omitted)+" lines]")
	}
	return strings.Join(result, "\n")
}
