package rtk

import (
	"regexp"
	"sort"
	"strings"
)

// ── LsParser — port of VansRouter filters/ls.js ────────────────────────
type LsParser struct{}

func (p *LsParser) Name() string { return "ls" }

var lsNoiseDirs = map[string]bool{
	"node_modules": true, ".git": true, "target": true, "__pycache__": true,
	".next": true, "dist": true, "build": true, ".cache": true, ".turbo": true,
	".vercel": true, ".pytest_cache": true, ".mypy_cache": true, ".tox": true,
	".venv": true, "venv": true, "env": true, "coverage": true, ".nyc_output": true,
	".DS_Store": true, "Thumbs.db": true, ".idea": true, ".vscode": true, ".vs": true,
	"*.egg-info": true, ".eggs": true,
}

var reLsDate = regexp.MustCompile(`\s+(Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec)\s+\d{1,2}\s+(\d{4}|\d{2}:\d{2})\s+`)

func parseLsLine(line string) (fileType byte, size int, name string, ok bool) {
	m := reLsDate.FindStringSubmatchIndex(line)
	if m == nil {
		return
	}
	name = line[m[1]:]
	beforeDate := line[:m[0]]
	beforeParts := strings.Fields(beforeDate)
	if len(beforeParts) < 4 {
		return
	}
	fileType = beforeParts[0][0]
	// size = rightmost parseable number before date
	for i := len(beforeParts) - 1; i >= 0; i-- {
		n := 0
		isNum := true
		for _, c := range beforeParts[i] {
			if c < '0' || c > '9' {
				isNum = false
				break
			}
			n = n*10 + int(c-'0')
		}
		if isNum {
			size = n
			break
		}
	}
	return fileType, size, name, true
}

func (p *LsParser) Parse(input string) string {
	var dirs []string
	type fileEntry struct{ name, size string }
	var files []fileEntry
	byExt := make(map[string]int)

	for _, line := range strings.Split(input, "\n") {
		if strings.HasPrefix(line, "total ") || line == "" {
			continue
		}
		ft, size, name, ok := parseLsLine(line)
		if !ok {
			continue
		}
		if name == "." || name == ".." {
			continue
		}
		if lsNoiseDirs[name] {
			continue
		}
		if ft == 'd' {
			dirs = append(dirs, name)
		} else if ft == '-' || ft == 'l' {
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

	var out strings.Builder
	for _, d := range dirs {
		out.WriteString(d)
		out.WriteString("/\n")
	}
	for _, f := range files {
		out.WriteString(f.name)
		out.WriteString("  ")
		out.WriteString(f.size)
		out.WriteString("\n")
	}
	out.WriteString("\nSummary: ")
	out.WriteString(itoa(len(files)))
	out.WriteString(" files, ")
	out.WriteString(itoa(len(dirs)))
	out.WriteString(" dirs")
	if len(byExt) > 0 {
		type extCount struct{ ext string; count int }
		var sorted []extCount
		for e, c := range byExt {
			sorted = append(sorted, extCount{e, c})
		}
		sort.Slice(sorted, func(i, j int) bool {
			return sorted[i].count > sorted[j].count
		})
		out.WriteString(" (")
		top := sorted
		if len(top) > LS_EXT_SUMMARY_TOP {
			top = top[:LS_EXT_SUMMARY_TOP]
		}
		for i, e := range top {
			if i > 0 {
				out.WriteString(", ")
			}
			out.WriteString(itoa(e.count))
			out.WriteString(" ")
			out.WriteString(e.ext)
		}
		if len(sorted) > LS_EXT_SUMMARY_TOP {
			out.WriteString(", +")
			out.WriteString(itoa(len(sorted) - LS_EXT_SUMMARY_TOP))
			out.WriteString(" more")
		}
		out.WriteString(")")
	}
	return out.String()
}

func humanSize(bytes int) string {
	if bytes >= 1048576 {
		return itoa(bytes/1048576) + "." + itoa((bytes%1048576)/104857) + "M"
	}
	if bytes >= 1024 {
		return itoa(bytes/1024) + "." + itoa((bytes%1024)/102) + "K"
	}
	return itoa(bytes) + "B"
}

// ── GrepParser — port of VansRouter filters/grep.js ────────────────────
type GrepParser struct{}

func (p *GrepParser) Name() string { return "grep" }

func (p *GrepParser) Parse(input string) string {
	type match struct{ lineNum, content string }
	byFile := make(map[string][]match)
	total := 0

	for _, line := range strings.Split(input, "\n") {
		first := strings.Index(line, ":")
		if first == -1 {
			continue
		}
		second := strings.Index(line[first+1:], ":")
		if second == -1 {
			continue
		}
		second += first + 1
		file := line[:first]
		lineNumStr := line[first+1 : second]
		content := line[second+1:]
		allDigits := true
		for _, c := range lineNumStr {
			if c < '0' || c > '9' {
				allDigits = false
				break
			}
		}
		if !allDigits || len(lineNumStr) == 0 {
			continue
		}
		total++
		byFile[file] = append(byFile[file], match{lineNumStr, content})
	}
	if total == 0 {
		return input
	}

	var files []string
	for f := range byFile {
		files = append(files, f)
	}
	sort.Strings(files)

	var out strings.Builder
	out.WriteString(itoa(total))
	out.WriteString(" matches in ")
	out.WriteString(itoa(len(files)))
	out.WriteString("F:\n\n")

	for _, file := range files {
		matches := byFile[file]
		out.WriteString("[file] ")
		out.WriteString(file)
		out.WriteString(" (")
		out.WriteString(itoa(len(matches)))
		out.WriteString("):\n")
		show := matches
		if len(show) > GREP_PER_FILE_MAX {
			show = show[:GREP_PER_FILE_MAX]
		}
		for _, m := range show {
			out.WriteString("  ")
			pad := 4 - len(m.lineNum)
			for i := 0; i < pad; i++ {
				out.WriteString(" ")
			}
			out.WriteString(m.lineNum)
			out.WriteString(": ")
			out.WriteString(strings.TrimSpace(m.content))
			out.WriteString("\n")
		}
		if len(matches) > GREP_PER_FILE_MAX {
			out.WriteString("  +")
			out.WriteString(itoa(len(matches) - GREP_PER_FILE_MAX))
			out.WriteString("\n")
		}
		out.WriteString("\n")
	}
	return strings.TrimRight(out.String(), "\n")
}

// ── FindParser — port of VansRouter filters/find.js ────────────────────
type FindParser struct{}

func (p *FindParser) Name() string { return "find" }

func (p *FindParser) Parse(input string) string {
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
		lastSep := strings.LastIndex(path, "/")
		if lastSep == -1 {
			lastSep = strings.LastIndex(path, "\\")
		}
		var dir, basename string
		if lastSep == -1 {
			dir = "."
			basename = path
		} else {
			dir = path[:lastSep]
			if dir == "" {
				dir = "/"
			}
			basename = path[lastSep+1:]
		}
		byDir[dir] = append(byDir[dir], basename)
	}

	var dirs []string
	for d := range byDir {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)

	var out strings.Builder
	out.WriteString(itoa(len(filtered)))
	out.WriteString(" files in ")
	out.WriteString(itoa(len(dirs)))
	out.WriteString(" dirs:\n\n")

	showDirs := dirs
	if len(showDirs) > FIND_TOTAL_DIR_MAX {
		showDirs = showDirs[:FIND_TOTAL_DIR_MAX]
	}
	for _, dir := range showDirs {
		files := byDir[dir]
		dirLabel := strings.ReplaceAll(dir, "\\", "/")
		out.WriteString(dirLabel)
		out.WriteString("/  (")
		out.WriteString(itoa(len(files)))
		out.WriteString(")\n")
		showFiles := files
		if len(showFiles) > FIND_PER_DIR_MAX {
			showFiles = showFiles[:FIND_PER_DIR_MAX]
		}
		for _, f := range showFiles {
			out.WriteString("  ")
			out.WriteString(f)
			out.WriteString("\n")
		}
		if len(files) > FIND_PER_DIR_MAX {
			out.WriteString("  +")
			out.WriteString(itoa(len(files) - FIND_PER_DIR_MAX))
			out.WriteString("\n")
		}
	}
	if len(dirs) > FIND_TOTAL_DIR_MAX {
		out.WriteString("\n+")
		out.WriteString(itoa(len(dirs) - FIND_TOTAL_DIR_MAX))
		out.WriteString(" more dirs\n")
	}
	return strings.TrimRight(out.String(), "\n")
}

// ── TreeParser — port of VansRouter filters/tree.js ────────────────────
type TreeParser struct{}

func (p *TreeParser) Name() string { return "tree" }

func (p *TreeParser) Parse(input string) string {
	lines := strings.Split(input, "\n")
	if len(lines) == 0 {
		return input
	}
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
	if len(filtered) > TREE_MAX_LINES {
		cut := len(filtered) - TREE_MAX_LINES
		return strings.Join(filtered[:TREE_MAX_LINES], "\n") +
			"\n... +" + itoa(cut) + " more lines"
	}
	return strings.Join(filtered, "\n")
}

// ── SearchListParser — port of VansRouter filters/searchList.js ────────
type SearchListParser struct{}

func (p *SearchListParser) Name() string { return "search-list" }

func (p *SearchListParser) Parse(input string) string {
	lines := strings.Split(input, "\n")
	if len(lines) == 0 {
		return input
	}
	header := lines[0]
	var paths []string
	for _, raw := range lines[1:] {
		t := strings.TrimSpace(raw)
		if strings.HasPrefix(t, "- ") {
			paths = append(paths, t[2:])
		}
	}
	if len(paths) == 0 {
		return input
	}

	byDir := make(map[string][]string)
	for _, p := range paths {
		slash := strings.LastIndex(p, "/")
		dir := "."
		name := p
		if slash != -1 {
			dir = p[:slash]
			if dir == "" {
				dir = "/"
			}
			name = p[slash+1:]
		}
		byDir[dir] = append(byDir[dir], name)
	}

	var dirs []string
	for d := range byDir {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)

	var out strings.Builder
	out.WriteString(header)
	out.WriteString("\n")
	out.WriteString(itoa(len(paths)))
	out.WriteString(" files in ")
	out.WriteString(itoa(len(dirs)))
	out.WriteString(" dirs:\n\n")

	for i, dir := range dirs {
		if i >= SEARCH_TOTAL_DIR_MAX {
			break
		}
		names := byDir[dir]
		out.WriteString(dir)
		out.WriteString("/ (")
		out.WriteString(itoa(len(names)))
		out.WriteString("):\n")
		for j, n := range names {
			if j >= SEARCH_PER_DIR_MAX {
				break
			}
			out.WriteString("  ")
			out.WriteString(n)
			out.WriteString("\n")
		}
		if len(names) > SEARCH_PER_DIR_MAX {
			out.WriteString("  +")
			out.WriteString(itoa(len(names) - SEARCH_PER_DIR_MAX))
			out.WriteString("\n")
		}
		out.WriteString("\n")
	}
	if len(dirs) > SEARCH_TOTAL_DIR_MAX {
		out.WriteString("+")
		out.WriteString(itoa(len(dirs) - SEARCH_TOTAL_DIR_MAX))
		out.WriteString(" more dirs\n")
	}
	return strings.TrimRight(out.String(), "\n")
}

// ── ReadNumberedParser — port of VansRouter filters/readNumbered.js ────
type ReadNumberedParser struct{}

func (p *ReadNumberedParser) Name() string { return "read-numbered" }

func (p *ReadNumberedParser) Parse(input string) string {
	lines := strings.Split(input, "\n")
	if len(lines) < SMART_MIN_LINES {
		return input
	}
	head := lines
	if len(head) > SMART_HEAD {
		head = head[:SMART_HEAD]
	}
	tail := lines
	if len(tail) > SMART_TAIL {
		tail = tail[len(tail)-SMART_TAIL:]
	}
	cut := len(lines) - len(head) - len(tail)
	result := make([]string, 0, len(head)+1+len(tail))
	result = append(result, head...)
	result = append(result, "... +"+itoa(cut)+" lines truncated (file continues)")
	result = append(result, tail...)
	return strings.Join(result, "\n")
}
