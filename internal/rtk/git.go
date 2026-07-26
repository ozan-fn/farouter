package rtk

import (
	"regexp"
	"strings"
)

// ── Git Status ──────────────────────────────────────────────────────────────

type GitStatusParser struct{}

func (p *GitStatusParser) Name() string { return "git-status" }

func (p *GitStatusParser) Match(output string) bool {
	return strings.Contains(output, "On branch") ||
		strings.Contains(output, "nothing to commit") ||
		strings.Contains(output, "Changes not staged") ||
		strings.Contains(output, "Changes to be committed") ||
		strings.Contains(output, "Untracked files")
}

func (p *GitStatusParser) Parse(output string) string {
	lines := strings.Split(output, "\n")
	
	var branch string
	var status []string
	
	for _, line := range lines {
		line = strings.TrimSpace(line)
		
		// Extract branch
		if strings.HasPrefix(line, "On branch ") {
			branch = strings.TrimPrefix(line, "On branch ")
		} else if strings.Contains(line, "Your branch is up to date with") {
			re := regexp.MustCompile(`'([^']+)'`)
			if matches := re.FindStringSubmatch(line); len(matches) > 1 {
				branch += "..." + matches[1]
			}
		} else if strings.Contains(line, "Your branch is ahead of") {
			re := regexp.MustCompile(`'([^']+)' by (\d+)`)
			if matches := re.FindStringSubmatch(line); len(matches) > 2 {
				branch += "..." + matches[1] + " +" + matches[2]
			}
		} else if strings.Contains(line, "Your branch is behind") {
			re := regexp.MustCompile(`'([^']+)' by (\d+)`)
			if matches := re.FindStringSubmatch(line); len(matches) > 2 {
				branch += "..." + matches[1] + " -" + matches[2]
			}
		}
		
		// Status lines
		if strings.HasPrefix(line, "modified:") || strings.HasPrefix(line, "deleted:") ||
			strings.HasPrefix(line, "new file:") || strings.HasPrefix(line, "renamed:") {
			status = append(status, line)
		}
	}
	
	if branch == "" {
		branch = "main"
	}
	
	result := "* " + branch
	
	if strings.Contains(output, "nothing to commit") {
		result += "\nclean — nothing to commit"
	} else if len(status) > 0 {
		result += "\n" + strings.Join(status, "\n")
	}
	
	return result
}

// ── Git Log ─────────────────────────────────────────────────────────────────

type GitLogParser struct{}

func (p *GitLogParser) Name() string { return "git-log" }

func (p *GitLogParser) Match(output string) bool {
	return strings.Contains(output, "commit ") && strings.Contains(output, "Author:")
}

func (p *GitLogParser) Parse(output string) string {
	lines := strings.Split(output, "\n")
	var result []string
	
	var hash, author, subject string
	var dateStr string
	bodyLines := 0
	
	for _, line := range lines {
		line = strings.TrimSpace(line)
		
		if strings.HasPrefix(line, "commit ") {
			if hash != "" {
				// Flush previous commit
				compact := hash[:7] + " " + subject
				if dateStr != "" {
					compact += " (" + dateStr + ")"
				}
				if author != "" {
					compact += " <" + author + ">"
				}
				if bodyLines > 0 {
					compact += "\n  [+" + itoa(bodyLines) + " lines omitted]"
				}
				result = append(result, compact)
			}
			hash = strings.TrimPrefix(line, "commit ")
			author = ""
			subject = ""
			dateStr = ""
			bodyLines = 0
		} else if strings.HasPrefix(line, "Author:") {
			authorFull := strings.TrimPrefix(line, "Author:")
			authorFull = strings.TrimSpace(authorFull)
			// Extract name only
			if idx := strings.Index(authorFull, "<"); idx > 0 {
				author = strings.TrimSpace(authorFull[:idx])
			} else {
				author = authorFull
			}
		} else if strings.HasPrefix(line, "Date:") {
			dateRaw := strings.TrimPrefix(line, "Date:")
			dateRaw = strings.TrimSpace(dateRaw)
			// Convert to relative time (simplified)
			dateStr = "recently"
		} else if line != "" && subject == "" && !strings.HasPrefix(line, "Merge:") {
			subject = line
		} else if line != "" && subject != "" {
			bodyLines++
		}
	}
	
	// Flush last commit
	if hash != "" {
		compact := hash[:7] + " " + subject
		if dateStr != "" {
			compact += " (" + dateStr + ")"
		}
		if author != "" {
			compact += " <" + author + ">"
		}
		if bodyLines > 0 {
			compact += "\n  [+" + itoa(bodyLines) + " lines omitted]"
		}
		result = append(result, compact)
	}
	
	return strings.Join(result, "\n")
}

// ── Git Diff ────────────────────────────────────────────────────────────────

type GitDiffParser struct{}

func (p *GitDiffParser) Name() string { return "git-diff" }

func (p *GitDiffParser) Match(output string) bool {
	return strings.Contains(output, "diff --git") || strings.Contains(output, "@@")
}

func (p *GitDiffParser) Parse(output string) string {
	lines := strings.Split(output, "\n")
	var result []string
	
	for _, line := range lines {
		// Skip index, mode, ---/+++ headers
		if strings.HasPrefix(line, "index ") || strings.HasPrefix(line, "new file mode") ||
			strings.HasPrefix(line, "deleted file mode") || strings.HasPrefix(line, "---") ||
			strings.HasPrefix(line, "+++") {
			continue
		}
		
		// Keep diff markers, hunks, and changed lines
		if strings.HasPrefix(line, "diff --git") || strings.HasPrefix(line, "@@") ||
			strings.HasPrefix(line, "+") || strings.HasPrefix(line, "-") {
			result = append(result, line)
		}
	}
	
	if len(result) == 0 {
		return "(no changes)"
	}
	
	return strings.Join(result, "\n")
}

// ── Git Push ────────────────────────────────────────────────────────────────

type GitPushParser struct{}

func (p *GitPushParser) Name() string { return "git-push" }

func (p *GitPushParser) Match(output string) bool {
	return (strings.Contains(output, "To ") && strings.Contains(output, "->")) ||
		strings.Contains(output, "Everything up-to-date")
}

func (p *GitPushParser) Parse(output string) string {
	if strings.Contains(output, "Everything up-to-date") {
		return "ok (up-to-date)"
	}
	
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		if strings.Contains(line, "->") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				return "ok " + parts[len(parts)-1]
			}
		}
	}
	
	return "ok"
}

// ── Git Pull ────────────────────────────────────────────────────────────────

type GitPullParser struct{}

func (p *GitPullParser) Name() string { return "git-pull" }

func (p *GitPullParser) Match(output string) bool {
	return strings.Contains(output, "Already up to date") ||
		strings.Contains(output, "Fast-forward") ||
		strings.Contains(output, "Updating ")
}

func (p *GitPullParser) Parse(output string) string {
	if strings.Contains(output, "Already up to date") {
		return "ok (up-to-date)"
	}
	
	lines := strings.Split(output, "\n")
	var stats []string
	
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "file changed") || strings.Contains(line, "files changed") {
			stats = append(stats, line)
		}
	}
	
	if len(stats) > 0 {
		return "ok " + strings.Join(stats, ", ")
	}
	
	return "ok"
}

// ── Git Add ─────────────────────────────────────────────────────────────────

type GitAddParser struct{}

func (p *GitAddParser) Name() string { return "git-add" }

func (p *GitAddParser) Match(output string) bool {
	// git add has zero output when successful, or "nothing added..."
	return output == "" || output == "(no output)" || output == "nothing added, but untracked files present"
}

func (p *GitAddParser) Parse(output string) string {
	return "ok"
}

// ── Git Commit ──────────────────────────────────────────────────────────────

type GitCommitParser struct{}

func (p *GitCommitParser) Name() string { return "git-commit" }

func (p *GitCommitParser) Match(output string) bool {
	// Must be actual git commit output: [branch hash] or "nothing to commit"
	return strings.HasPrefix(output, "[") &&
		(strings.Contains(output, "]: ") || strings.Contains(output, "(root-commit)"))
}

func (p *GitCommitParser) Parse(output string) string {
	re := regexp.MustCompile(`\[.*?([a-f0-9]{7,40})\]`)
	if matches := re.FindStringSubmatch(output); len(matches) > 1 {
		return "ok " + matches[1][:7]
	}
	return "ok"
}
