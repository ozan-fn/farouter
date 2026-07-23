package rtk

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var (
	reGitLog    = regexp.MustCompile(`(?im)^commit [0-9a-f]{7,40}$`)
	reGitDiff   = regexp.MustCompile(`(?m)^diff --git `)
	reGitHunk   = regexp.MustCompile(`(?m)^@@ `)
	reGitStatus = regexp.MustCompile(`(?m)^On branch |^nothing to commit|^Changes (not |to be )|^Untracked files:`)
	rePorcelain = regexp.MustCompile(`(?m)^[ MADRCU?!][ MADRCU?!] \S`)
	reBuild     = regexp.MustCompile(`(?im)^(npm (warn|error|ERR!)|yarn (warn|error)|\s*Compiling\s+\S+|\s*Downloading\s+\S+|added \d+ package|\[ERROR\]|BUILD (SUCCESS|FAILED)|\s*Finished\s+|Successfully (installed|built)|ERROR:)`)
	reTreeGlyph = regexp.MustCompile(`[├└]──|│  `)
	reLsRow     = regexp.MustCompile(`(?m)^[-dlbcps][rwx-]{9}`)
	reGrepLine  = regexp.MustCompile(`^([^:]+):(\d+):(.+)$`)
)

func autoDetect(text string) func(string) string {
	head := text
	if len(head) > detectWindow {
		head = head[:detectWindow]
	}
	if reGitLog.MatchString(head) {
		return filterGitLog
	}
	if reGitDiff.MatchString(head) || reGitHunk.MatchString(head) {
		return filterGitDiff
	}
	if reGitStatus.MatchString(head) {
		return filterGitStatus
	}
	if reBuild.MatchString(head) {
		return filterBuildOutput
	}
	if rePorcelain.MatchString(head) {
		return filterGitStatus
	}
	lines := strings.Split(head, "\n")
	nonEmpty := make([]string, 0, len(lines))
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			nonEmpty = append(nonEmpty, l)
		}
	}
	first5 := nonEmpty
	if len(first5) > 5 {
		first5 = first5[:5]
	}
	for _, l := range first5 {
		if reGrepLine.MatchString(l) {
			return filterGrep
		}
	}
	if len(nonEmpty) >= 3 {
		allPath := true
		for _, l := range nonEmpty {
			if !isPathLike(l) {
				allPath = false
				break
			}
		}
		if allPath {
			return filterFind
		}
	}
	if reTreeGlyph.MatchString(head) {
		return filterTree
	}
	if reLsRow.MatchString(head) {
		return filterLs
	}
	totalLines := strings.Count(text, "\n") + 1
	if totalLines >= smartTruncMinLines {
		return filterSmartTruncate
	}
	return filterDedupLog
}

func isPathLike(t string) bool {
	t = strings.TrimSpace(t)
	if len(t) >= 2 && t[1] == ':' {
		return true
	}
	return strings.HasPrefix(t, ".") || strings.HasPrefix(t, "/") || strings.Contains(t, "/")
}

func filterGrep(input string) string {
	type match struct{ lineNum, content string }
	byFile := make(map[string][]match)
	total := 0
	for _, line := range strings.Split(input, "\n") {
		m := reGrepLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		file, lineNum, content := m[1], m[2], m[3]
		byFile[file] = append(byFile[file], match{lineNum, content})
		total++
	}
	if total == 0 {
		return input
	}
	files := make([]string, 0, len(byFile))
	for f := range byFile {
		files = append(files, f)
	}
	sort.Strings(files)
	var sb strings.Builder
	sb.WriteString(itoa(total) + " matches in " + itoa(len(files)) + "F:\n\n")
	for _, f := range files {
		matches := byFile[f]
		sb.WriteString("[file] " + f + " (" + itoa(len(matches)) + "):\n")
		show := matches
		if len(show) > grepPerFileMax {
			show = show[:grepPerFileMax]
		}
		for _, m := range show {
			pad := strings.Repeat(" ", 4-len(m.lineNum))
			sb.WriteString("  " + pad + m.lineNum + ": " + strings.TrimSpace(m.content) + "\n")
		}
		if len(matches) > grepPerFileMax {
			sb.WriteString("  +" + itoa(len(matches)-grepPerFileMax) + "\n")
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

func filterFind(input string) string {
	lines := strings.Split(input, "\n")
	var filtered []string
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			filtered = append(filtered, l)
		}
	}
	if len(filtered) == 0 {
		return input
	}
	byDir := make(map[string][]string)
	for _, path := range filtered {
		lastSlash := strings.LastIndexAny(path, "/\\")
		var dir, base string
		if lastSlash == -1 {
			dir, base = ".", path
		} else {
			dir = path[:lastSlash]
			if dir == "" {
				dir = "/"
			}
			base = path[lastSlash+1:]
		}
		byDir[dir] = append(byDir[dir], base)
	}
	dirs := make([]string, 0, len(byDir))
	for d := range byDir {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	var sb strings.Builder
	sb.WriteString(itoa(len(filtered)) + " files in " + itoa(len(dirs)) + " dirs:\n\n")
	show := dirs
	if len(show) > findTotalDirMax {
		show = show[:findTotalDirMax]
	}
	for _, d := range show {
		files := byDir[d]
		sb.WriteString(strings.ReplaceAll(d, "\\", "/") + "/  (" + itoa(len(files)) + ")\n")
		showF := files
		if len(showF) > findPerDirMax {
			showF = showF[:findPerDirMax]
		}
		for _, f := range showF {
			sb.WriteString("  " + f + "\n")
		}
		if len(files) > findPerDirMax {
			sb.WriteString("  +" + itoa(len(files)-findPerDirMax) + "\n")
		}
	}
	if len(dirs) > findTotalDirMax {
		sb.WriteString("\n+" + itoa(len(dirs)-findTotalDirMax) + " more dirs\n")
	}
	return sb.String()
}

func filterGitDiff(diff string) string {
	var result []string
	currentFile := ""
	added, removed := 0, 0
	inHunk := false
	hunkShown, hunkSkipped := 0, 0
	wasTruncated := false

	for _, line := range strings.Split(diff, "\n") {
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
			if len(parts) > 1 {
				currentFile = parts[1]
			} else {
				currentFile = "unknown"
			}
			result = append(result, "\n"+currentFile)
			added, removed = 0, 0
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
				if hunkShown < gitDiffHunkMax {
					result = append(result, "  "+line)
					hunkShown++
				} else {
					hunkSkipped++
				}
			} else if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
				removed++
				if hunkShown < gitDiffHunkMax {
					result = append(result, "  "+line)
					hunkShown++
				} else {
					hunkSkipped++
				}
			} else if hunkShown < gitDiffHunkMax && !strings.HasPrefix(line, "\\") {
				if hunkShown > 0 {
					result = append(result, "  "+line)
					hunkShown++
				}
			}
		}
		if len(result) >= 500 {
			result = append(result, "\n... (more changes truncated)")
			wasTruncated = true
			break
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

func filterTree(input string) string {
	lines := strings.Split(input, "\n")
	var filtered []string
	for _, line := range lines {
		if strings.Contains(line, "director") && strings.Contains(line, "file") {
			continue
		}
		if strings.TrimSpace(line) == "" && len(filtered) == 0 {
			continue
		}
		filtered = append(filtered, line)
	}
	for len(filtered) > 0 && strings.TrimSpace(filtered[len(filtered)-1]) == "" {
		filtered = filtered[:len(filtered)-1]
	}
	if len(filtered) > treeMaxLines {
		cut := len(filtered) - treeMaxLines
		return strings.Join(filtered[:treeMaxLines], "\n") + "\n... +" + itoa(cut) + " more lines"
	}
	return strings.Join(filtered, "\n")
}

func filterLs(input string) string {
	type fileEntry struct{ name, size string }
	var dirs []string
	var files []fileEntry
	byExt := make(map[string]int)

	reLsDate := regexp.MustCompile(`\s+(Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec)\s+\d{1,2}\s+(\d{4}|\d{2}:\d{2})\s+`)
	noiseDir := map[string]bool{
		"node_modules": true, ".git": true, "target": true, "__pycache__": true,
		".next": true, "dist": true, "build": true, ".cache": true,
	}

	for _, line := range strings.Split(input, "\n") {
		if strings.HasPrefix(line, "total ") || line == "" {
			continue
		}
		m := reLsDate.FindStringIndex(line)
		if m == nil {
			continue
		}
		name := strings.TrimSpace(line[m[1]:])
		if name == "." || name == ".." || noiseDir[name] {
			continue
		}
		perms := strings.TrimSpace(line[:m[0]])
		if len(perms) == 0 {
			continue
		}
		fileType := rune(perms[0])
		beforeParts := strings.Fields(perms)
		size := 0
		for i := len(beforeParts) - 1; i >= 0; i-- {
			var n int
			if _, err := strings.NewReader(beforeParts[i]).Read([]byte{}); err == nil {
				fmt_n := beforeParts[i]
				allDigit := true
				for _, c := range fmt_n {
					if c < '0' || c > '9' {
						allDigit = false
						break
					}
				}
				if allDigit && len(fmt_n) > 0 {
					for _, c := range fmt_n {
						n = n*10 + int(c-'0')
					}
					size = n
					break
				}
			}
		}
		if fileType == 'd' {
			dirs = append(dirs, name)
		} else if fileType == '-' || fileType == 'l' {
			dot := strings.LastIndex(name, ".")
			ext := "no ext"
			if dot > 0 {
				ext = name[dot:]
			}
			byExt[ext]++
			files = append(files, fileEntry{name, humanSize(size)})
		}
	}

	if len(dirs) == 0 && len(files) == 0 {
		return input
	}

	var sb strings.Builder
	for _, d := range dirs {
		sb.WriteString(d + "/\n")
	}
	for _, f := range files {
		sb.WriteString(f.name + "  " + f.size + "\n")
	}
	sb.WriteString("\nSummary: " + itoa(len(files)) + " files, " + itoa(len(dirs)) + " dirs")
	if len(byExt) > 0 {
		type extCount struct{ ext string; count int }
		var exts []extCount
		for e, c := range byExt {
			exts = append(exts, extCount{e, c})
		}
		sort.Slice(exts, func(i, j int) bool { return exts[i].count > exts[j].count })
		sb.WriteString(" (")
		for i, e := range exts {
			if i >= lsExtSummaryTop {
				sb.WriteString(", +" + itoa(len(exts)-lsExtSummaryTop) + " more")
				break
			}
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(itoa(e.count) + " " + e.ext)
		}
		sb.WriteString(")")
	}
	return sb.String()
}

func humanSize(bytes int) string {
	if bytes >= 1048576 {
		return strings.TrimRight(strings.TrimRight(fmt_f(float64(bytes)/1048576), "0"), ".") + "M"
	}
	if bytes >= 1024 {
		return strings.TrimRight(strings.TrimRight(fmt_f(float64(bytes)/1024), "0"), ".") + "K"
	}
	return itoa(bytes) + "B"
}

func fmt_f(f float64) string {
	s := strconv.FormatFloat(f, 'f', 1, 64)
	return s
}

func filterGitStatus(input string) string {
	lines := strings.Split(input, "\n")
	if len(lines) == 0 || (len(lines) == 1 && strings.TrimSpace(lines[0]) == "") {
		return "Clean working tree"
	}
	var branch string
	var staged, modified, untracked []string
	conflicts := 0

	reBranch := regexp.MustCompile(`^On branch (\S+)`)
	reLong := regexp.MustCompile(`^\s*(modified|new file|deleted|renamed|both modified):\s+(.+)$`)

	for _, raw := range lines {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		if m := reBranch.FindStringSubmatch(raw); m != nil {
			branch = m[1]
			continue
		}
		if strings.HasPrefix(raw, "##") {
			branch = strings.TrimPrefix(raw, "## ")
			continue
		}
		if len(raw) >= 3 && regexp.MustCompile(`^[ MADRCU?!][ MADRCU?!] `).MatchString(raw) {
			x, y := raw[0], raw[1]
			file := raw[3:]
			if raw[:2] == "??" {
				untracked = append(untracked, file)
				continue
			}
			if strings.ContainsRune("MADRC", rune(x)) {
				staged = append(staged, file)
			} else if x == 'U' {
				conflicts++
			}
			if y == 'M' || y == 'D' {
				modified = append(modified, file)
			}
			continue
		}
		if m := reLong.FindStringSubmatch(raw); m != nil {
			kind, path := m[1], strings.TrimSpace(m[2])
			switch kind {
			case "both modified":
				conflicts++
			case "modified", "deleted":
				modified = append(modified, path)
			case "new file", "renamed":
				staged = append(staged, path)
			}
		}
	}

	var sb strings.Builder
	if branch != "" {
		sb.WriteString("* " + branch + "\n")
	}
	if len(staged) > 0 {
		sb.WriteString("+ Staged: " + itoa(len(staged)) + " files\n")
		for _, f := range staged[:min(len(staged), statusMaxFiles)] {
			sb.WriteString("   " + f + "\n")
		}
		if len(staged) > statusMaxFiles {
			sb.WriteString("   ... +" + itoa(len(staged)-statusMaxFiles) + " more\n")
		}
	}
	if len(modified) > 0 {
		sb.WriteString("~ Modified: " + itoa(len(modified)) + " files\n")
		for _, f := range modified[:min(len(modified), statusMaxFiles)] {
			sb.WriteString("   " + f + "\n")
		}
		if len(modified) > statusMaxFiles {
			sb.WriteString("   ... +" + itoa(len(modified)-statusMaxFiles) + " more\n")
		}
	}
	if len(untracked) > 0 {
		sb.WriteString("? Untracked: " + itoa(len(untracked)) + " files\n")
		for _, f := range untracked[:min(len(untracked), statusMaxUntrack)] {
			sb.WriteString("   " + f + "\n")
		}
		if len(untracked) > statusMaxUntrack {
			sb.WriteString("   ... +" + itoa(len(untracked)-statusMaxUntrack) + " more\n")
		}
	}
	if conflicts > 0 {
		sb.WriteString("conflicts: " + itoa(conflicts) + " files\n")
	}
	if len(staged) == 0 && len(modified) == 0 && len(untracked) == 0 && conflicts == 0 {
		sb.WriteString("clean — nothing to commit\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}

func filterGitLog(text string) string {
	if text == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	out := make([]string, 0, gitLogMaxLines)
	skipped := 0
	inCommit := false
	subjectSeen := false

	reCommit := regexp.MustCompile(`(?i)^commit [0-9a-f]{7,40}$`)
	reCommitGraph := regexp.MustCompile(`(?i)^[*|/\\ ]+commit [0-9a-f]{7,40}`)
	reAuthorDate := regexp.MustCompile(`(?i)^[*|/\\ ]*(Author|Date):`)
	reSubject := regexp.MustCompile(`^[*|/\\ ]*    \S`)
	reStat := regexp.MustCompile(`^\d+ file\w* changed`)
	reOneline := regexp.MustCompile(`^[0-9a-f]{7,40}\s+`)
	reGraph := regexp.MustCompile(`^[*|/\\ ]+([0-9a-f]{7,40}\s+.+)`)
	rePureGraph := regexp.MustCompile(`^[*|/\\ ]+$`)

	push := func(l string) bool {
		if len(out) < gitLogMaxLines {
			out = append(out, l)
			return true
		}
		skipped++
		return false
	}

	for _, raw := range lines {
		line := strings.TrimRight(raw, " \t")
		trimmed := strings.TrimSpace(line)

		if reCommit.MatchString(trimmed) || reCommitGraph.MatchString(trimmed) {
			inCommit = true
			subjectSeen = false
			push(line)
			continue
		}
		if inCommit {
			if reAuthorDate.MatchString(trimmed) {
				push(trimmed)
				continue
			}
			if trimmed == "" {
				continue
			}
			if !subjectSeen && reSubject.MatchString(line) {
				push("  Subject: " + trimmed)
				subjectSeen = true
				continue
			}
			if reStat.MatchString(trimmed) {
				push("  " + trimmed)
				continue
			}
			if strings.HasPrefix(trimmed, "diff --git ") {
				push("  ... diff body omitted")
				continue
			}
			continue
		}
		if m := reGraph.FindStringSubmatch(trimmed); m != nil {
			push(m[1])
			continue
		}
		if reOneline.MatchString(trimmed) {
			push(trimmed)
			continue
		}
		if rePureGraph.MatchString(trimmed) && strings.ContainsAny(trimmed, "*|/\\") {
			continue
		}
		push(trimmed)
	}
	if skipped > 0 {
		out = append(out, "... ("+itoa(skipped)+" more lines)")
	}
	result := strings.Join(out, "\n")
	if result == "" {
		return text
	}
	if len(result) > len(text) {
		return text
	}
	return result
}

func filterBuildOutput(input string) string {
	lines := strings.Split(input, "\n")
	var errors, warnings, deprecations []string
	var summary string
	compilingCount, downloadingCount := 0, 0
	inCargoError := false

	reCargoErr := regexp.MustCompile(`^\s*(-->|\||\d+\s*\||=)`)

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if inCargoError {
			if trimmed == "" {
				inCargoError = false
				continue
			}
			if reCargoErr.MatchString(line) {
				errors = append(errors, line)
				continue
			}
			inCargoError = false
		}
		if trimmed == "" {
			continue
		}
		switch {
		case regexp.MustCompile(`(?i)^npm (ERR!|error)`).MatchString(trimmed) ||
			regexp.MustCompile(`(?i)^yarn error`).MatchString(trimmed):
			errors = append(errors, line)
		case regexp.MustCompile(`(?i)^npm warn deprecated`).MatchString(trimmed):
			deprecations = append(deprecations, line)
		case regexp.MustCompile(`(?i)^npm warn`).MatchString(trimmed) ||
			regexp.MustCompile(`(?i)^yarn warn`).MatchString(trimmed):
			warnings = append(warnings, line)
		case regexp.MustCompile(`(?i)^error(\[|:)`).MatchString(trimmed):
			errors = append(errors, line)
			inCargoError = true
		case regexp.MustCompile(`(?i)^warning(\[|:)`).MatchString(trimmed):
			warnings = append(warnings, line)
			inCargoError = true
		case regexp.MustCompile(`(?i)^ERROR:`).MatchString(trimmed):
			errors = append(errors, line)
		case regexp.MustCompile(`(?i)^\[ERROR\]`).MatchString(trimmed) ||
			regexp.MustCompile(`(?i)^BUILD FAILED`).MatchString(trimmed):
			errors = append(errors, line)
		case regexp.MustCompile(`(?i)^\[WARNING\]`).MatchString(trimmed):
			warnings = append(warnings, line)
		case regexp.MustCompile(`(?i)^\s*Compiling\s+\S+`).MatchString(trimmed):
			compilingCount++
		case regexp.MustCompile(`(?i)^\s*Downloading\s+\S+`).MatchString(trimmed) ||
			regexp.MustCompile(`(?i)^Fetching\s+`).MatchString(trimmed):
			downloadingCount++
		case regexp.MustCompile(`(?i)^(added|removed|changed|audited|installed)\s+\d+\s+package`).MatchString(trimmed) ||
			regexp.MustCompile(`(?i)^\s*Finished\s+`).MatchString(trimmed) ||
			regexp.MustCompile(`(?i)^BUILD SUCCESS`).MatchString(trimmed) ||
			regexp.MustCompile(`(?i)^Successfully (installed|built)`).MatchString(trimmed):
			if summary != "" {
				summary += "\n" + line
			} else {
				summary = line
			}
		}
	}

	var sb strings.Builder
	keep := deprecations
	if len(keep) > 3 {
		keep = keep[:3]
	}
	for _, d := range keep {
		sb.WriteString(d + "\n")
	}
	if len(deprecations) > 3 {
		sb.WriteString("... +" + itoa(len(deprecations)-3) + " more deprecated packages\n")
	}
	if compilingCount > 0 {
		sb.WriteString("Compiled " + itoa(compilingCount) + " packages\n")
	}
	if downloadingCount > 0 {
		sb.WriteString("Downloaded " + itoa(downloadingCount) + " packages\n")
	}
	for _, e := range errors {
		sb.WriteString(e + "\n")
	}
	keepW := warnings
	if len(keepW) > 5 {
		keepW = keepW[:5]
	}
	for _, w := range keepW {
		sb.WriteString(w + "\n")
	}
	if len(warnings) > 5 {
		sb.WriteString("... +" + itoa(len(warnings)-5) + " more warnings\n")
	}
	if summary != "" {
		sb.WriteString(summary + "\n")
	}
	result := strings.TrimRight(sb.String(), "\n")
	if result == "" {
		return input
	}
	return result
}

func filterSmartTruncate(text string) string {
	lines := strings.Split(text, "\n")
	if len(lines) < smartTruncMinLines {
		return text
	}
	head := lines[:smartTruncHead]
	tail := lines[len(lines)-smartTruncTail:]
	cut := len(lines) - smartTruncHead - smartTruncTail
	parts := append(head, "... +"+itoa(cut)+" lines truncated")
	parts = append(parts, tail...)
	return strings.Join(parts, "\n")
}

func filterDedupLog(text string) string {
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	var prev string
	runCount := 0
	blankStreak := 0

	flush := func() {
		if prev != "" && runCount > 1 {
			out = append(out, "  ... ("+itoa(runCount-1)+" duplicate lines)")
		}
	}

	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			if blankStreak < 1 {
				out = append(out, line)
			}
			blankStreak++
			flush()
			prev = ""
			runCount = 0
			continue
		}
		blankStreak = 0
		if line == prev {
			runCount++
			continue
		}
		flush()
		out = append(out, line)
		prev = line
		runCount = 1
		if len(out) >= dedupLineMax {
			out = append(out, "... (truncated at "+itoa(dedupLineMax)+" lines)")
			return strings.Join(out, "\n")
		}
	}
	flush()
	return strings.Join(out, "\n")
}
