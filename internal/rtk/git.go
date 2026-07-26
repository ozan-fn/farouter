package rtk

import (
	"regexp"
	"strings"
)

// ── GitDiffParser — port of VansRouter filters/gitDiff.js ──────────────
type GitDiffParser struct{}

func (p *GitDiffParser) Name() string { return "git-diff" }

func (p *GitDiffParser) Parse(diff string) string {
	lines := strings.Split(diff, "\n")
	if len(lines) == 0 {
		return diff
	}

	var result []string
	currentFile := ""
	added := 0
	removed := 0
	inHunk := false
	hunkShown := 0
	hunkSkipped := 0
	wasTruncated := false
	maxLines := 500
	maxHunk := GIT_DIFF_HUNK_MAX

outer:
	for _, line := range lines {
		if strings.HasPrefix(line, "diff --git") {
			if hunkSkipped > 0 {
				result = append(result, "  ... ("+itoa(hunkSkipped)+" lines truncated)")
				wasTruncated = true
				hunkSkipped = 0
			}
			if currentFile != "" && (added > 0 || removed > 0) {
				result = append(result, "  +"+itoa(added)+" -"+itoa(removed))
			}
			parts := strings.SplitN(line, " b/", 2)
			currentFile = "unknown"
			if len(parts) > 1 {
				currentFile = parts[1]
			}
			result = append(result, "")
			result = append(result, currentFile)
			added = 0
			removed = 0
			inHunk = false
			hunkShown = 0
		} else if strings.HasPrefix(line, "@@") {
			if hunkSkipped > 0 {
				result = append(result, "  ... ("+itoa(hunkSkipped)+" lines truncated)")
				wasTruncated = true
				hunkSkipped = 0
			}
			inHunk = true
			hunkShown = 0
			result = append(result, "  "+line)
		} else if inHunk {
			if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
				added++
				if hunkShown < maxHunk {
					result = append(result, "  "+line)
					hunkShown++
				} else {
					hunkSkipped++
				}
			} else if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
				removed++
				if hunkShown < maxHunk {
					result = append(result, "  "+line)
					hunkShown++
				} else {
					hunkSkipped++
				}
			} else if hunkShown < maxHunk && !strings.HasPrefix(line, "\\") {
				if hunkShown > 0 {
					result = append(result, "  "+line)
					hunkShown++
				}
			}
		}
		if len(result) >= maxLines {
			result = append(result, "")
			result = append(result, "... (more changes truncated)")
			wasTruncated = true
			break outer
		}
	}

	if hunkSkipped > 0 {
		result = append(result, "  ... ("+itoa(hunkSkipped)+" lines truncated)")
		wasTruncated = true
	}
	if currentFile != "" && (added > 0 || removed > 0) {
		result = append(result, "  +"+itoa(added)+" -"+itoa(removed))
	}
	if wasTruncated {
		result = append(result, "[full diff: rtk git diff --no-compact]")
	}
	return strings.Join(result, "\n")
}

// ── GitStatusParser — port of VansRouter filters/gitStatus.js ──────────
type GitStatusParser struct{}

func (p *GitStatusParser) Name() string { return "git-status" }

func (p *GitStatusParser) Parse(output string) string {
	lines := strings.Split(output, "\n")
	if len(lines) == 0 || (len(lines) == 1 && strings.TrimSpace(lines[0]) == "") {
		return "Clean working tree"
	}

	branch := ""
	var stagedFiles, modifiedFiles, untrackedFiles []string
	staged := 0
	modified := 0
	untracked := 0
	conflicts := 0

	for _, raw := range lines {
		if strings.TrimSpace(raw) == "" {
			continue
		}

		// Long-form branch: "On branch main"
		if m := reBranchOn.FindStringSubmatch(raw); len(m) > 1 {
			branch = m[1]
			continue
		}
		// Porcelain branch: "## main...origin/main"
		if strings.HasPrefix(raw, "##") {
			branch = strings.TrimSpace(raw[2:])
			continue
		}
		// Porcelain status: "XY file"
		if len(raw) >= 3 && rePorcelainStatus.MatchString(raw[:2]) && raw[2] == ' ' {
			x := raw[0]
			y := raw[1]
			file := strings.TrimSpace(raw[3:])
			if raw[:2] == "??" {
				untracked++
				untrackedFiles = append(untrackedFiles, file)
				continue
			}
			if strings.Contains("MADRC", string(x)) {
				staged++
				stagedFiles = append(stagedFiles, file)
			} else if x == 'U' {
				conflicts++
			}
			if y == 'M' || y == 'D' {
				modified++
				modifiedFiles = append(modifiedFiles, file)
			}
			continue
		}
		// Long-form: "modified:   path"
		if m := reLongStatus.FindStringSubmatch(raw); len(m) > 2 {
			kind := m[1]
			path := strings.TrimSpace(m[2])
			switch kind {
			case "both modified":
				conflicts++
			case "modified", "deleted":
				modified++
				modifiedFiles = append(modifiedFiles, path)
			case "new file", "renamed":
				staged++
				stagedFiles = append(stagedFiles, path)
			}
			continue
		}
	}

	var out strings.Builder
	if branch != "" {
		out.WriteString("* ")
		out.WriteString(branch)
		out.WriteString("\n")
	}
	if staged > 0 {
		out.WriteString("+ Staged: ")
		out.WriteString(itoa(staged))
		out.WriteString(" files\n")
		show := stagedFiles
		if len(show) > STATUS_MAX_FILES {
			show = show[:STATUS_MAX_FILES]
		}
		for _, f := range show {
			out.WriteString("   ")
			out.WriteString(f)
			out.WriteString("\n")
		}
		if len(stagedFiles) > STATUS_MAX_FILES {
			out.WriteString("   ... +")
			out.WriteString(itoa(len(stagedFiles) - STATUS_MAX_FILES))
			out.WriteString(" more\n")
		}
	}
	if modified > 0 {
		out.WriteString("~ Modified: ")
		out.WriteString(itoa(modified))
		out.WriteString(" files\n")
		show := modifiedFiles
		if len(show) > STATUS_MAX_FILES {
			show = show[:STATUS_MAX_FILES]
		}
		for _, f := range show {
			out.WriteString("   ")
			out.WriteString(f)
			out.WriteString("\n")
		}
		if len(modifiedFiles) > STATUS_MAX_FILES {
			out.WriteString("   ... +")
			out.WriteString(itoa(len(modifiedFiles) - STATUS_MAX_FILES))
			out.WriteString(" more\n")
		}
	}
	if untracked > 0 {
		out.WriteString("? Untracked: ")
		out.WriteString(itoa(untracked))
		out.WriteString(" files\n")
		show := untrackedFiles
		if len(show) > STATUS_MAX_UNTRACKED {
			show = show[:STATUS_MAX_UNTRACKED]
		}
		for _, f := range show {
			out.WriteString("   ")
			out.WriteString(f)
			out.WriteString("\n")
		}
		if len(untrackedFiles) > STATUS_MAX_UNTRACKED {
			out.WriteString("   ... +")
			out.WriteString(itoa(len(untrackedFiles) - STATUS_MAX_UNTRACKED))
			out.WriteString(" more\n")
		}
	}
	if conflicts > 0 {
		out.WriteString("conflicts: ")
		out.WriteString(itoa(conflicts))
		out.WriteString(" files\n")
	}
	if staged == 0 && modified == 0 && untracked == 0 && conflicts == 0 {
		out.WriteString("clean — nothing to commit\n")
	}
	return strings.TrimRight(out.String(), "\n")
}

var (
	reBranchOn        = regexp.MustCompile(`^On branch (\S+)`)
	rePorcelainStatus = regexp.MustCompile(`^[ MADRCU?!][ MADRCU?!]$`)
	reLongStatus      = regexp.MustCompile(`^\s*(modified|new file|deleted|renamed|both modified):\s+(.+)$`)
)

// ── GitLogParser — port of VansRouter filters/gitLog.js ────────────────
type GitLogParser struct{}

func (p *GitLogParser) Name() string { return "git-log" }

func (p *GitLogParser) Parse(text string) string {
	if text == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	var out []string
	skipped := 0
	inCommit := false
	subjectSeen := false
	maxLines := GIT_LOG_MAX_LINES

	pushLine := func(l string) {
		if len(out) < maxLines {
			out = append(out, l)
		} else {
			skipped++
		}
	}

	for _, raw := range lines {
		line := strings.TrimRight(raw, " \t\r")
		trimmed := strings.TrimSpace(line)

		// "commit <sha>" header (possibly with graph decoration)
		if reCommitHeader.MatchString(trimmed) {
			inCommit = true
			subjectSeen = false
			pushLine(line)
			continue
		}
		if inCommit {
			if reAuthorDate.MatchString(trimmed) {
				pushLine(trimmed)
				continue
			}
			if trimmed == "" {
				continue
			}
			// Indented subject (4 spaces, possibly with graph prefix)
			if !subjectSeen && reSubject.MatchString(line) {
				pushLine("  Subject: " + trimmed)
				subjectSeen = true
				continue
			}
			// Stat summary: "N file(s) changed..."
			if reFileChanged.MatchString(trimmed) {
				pushLine("  " + trimmed)
				continue
			}
			// Embedded diff header
			if strings.HasPrefix(trimmed, "diff --git ") {
				pushLine("  ... diff body omitted")
				continue
			}
			continue
		}
		// Not in commit block — oneline/graph mode
		if m := reGraphMatch.FindStringSubmatch(trimmed); len(m) > 1 {
			pushLine(m[1])
			continue
		}
		if rePlainOneline.MatchString(trimmed) {
			pushLine(trimmed)
			continue
		}
		if rePureGraph.MatchString(trimmed) {
			continue
		}
		pushLine(trimmed)
	}

	if skipped > 0 {
		out = append(out, "... ("+itoa(skipped)+" more lines)")
	}
	result := strings.Join(out, "\n")
	if result == "" && text != "" {
		return text
	}
	if len(result) > len(text) {
		return text
	}
	return result
}

var (
	reCommitHeader  = regexp.MustCompile(`(?i)^[*|/\\ ]*commit [0-9a-f]{7,40}$`)
	reAuthorDate    = regexp.MustCompile(`(?i)^[*|/\\ ]*(Author|Date):`)
	reSubject       = regexp.MustCompile(`^[*|/\\ ]*    \S`)
	reFileChanged   = regexp.MustCompile(`^\d+ file\w* changed`)
	reGraphMatch    = regexp.MustCompile(`^[*|/\\ ]+([0-9a-f]{7,40}\s+.+)`)
	rePlainOneline  = regexp.MustCompile(`^[0-9a-f]{7,40}\s+`)
	rePureGraph     = regexp.MustCompile(`^[*|/\\ ]+$`)
)
