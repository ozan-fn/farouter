package rtk

import (
	"regexp"
	"strings"
)

// ── ls ──────────────────────────────────────────────────────────────────────

type LsParser struct{}

func (p *LsParser) Name() string { return "ls" }

func (p *LsParser) Match(output string) bool {
	return strings.Contains(output, "total ") ||
		regexp.MustCompile(`(?m)^[drwx-]{10}`).MatchString(output)
}

func (p *LsParser) Parse(output string) string {
	lines := strings.Split(output, "\n")
	var result []string

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "total ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 9 {
			continue
		}
		perms := fields[0]
		name := strings.Join(fields[8:], " ")
		if name == "." || name == ".." {
			continue
		}
		mode := permsToOctal(perms)
		compact := mode + "  " + name
		if perms[0] != 'd' {
			compact += "  " + humanSize(fields[4])
		} else {
			compact += "/"
		}
		result = append(result, compact)
	}
	if len(result) == 0 {
		return "(empty)"
	}
	return strings.Join(result, "\n")
}

func permsToOctal(perms string) string {
	if len(perms) != 10 {
		return "644"
	}
	var octal [3]byte
	for i := 0; i < 3; i++ {
		val := 0
		if perms[1+i*3] == 'r' {
			val += 4
		}
		if perms[2+i*3] == 'w' {
			val += 2
		}
		if perms[3+i*3] == 'x' || perms[3+i*3] == 's' || perms[3+i*3] == 't' {
			val += 1
		}
		octal[i] = byte('0' + val)
	}
	return string(octal[:])
}

func humanSize(size string) string {
	if len(size) > 0 && (size[len(size)-1] == 'K' || size[len(size)-1] == 'M' ||
		size[len(size)-1] == 'G' || size[len(size)-1] == 'B') {
		return size
	}
	n := 0
	for i := 0; i < len(size); i++ {
		if size[i] >= '0' && size[i] <= '9' {
			n = n*10 + int(size[i]-'0')
		}
	}
	if n >= 1024*1024*1024 {
		return itoa(n/(1024*1024*1024)) + "G"
	} else if n >= 1024*1024 {
		return itoa(n/(1024*1024)) + "M"
	} else if n >= 1024 {
		return itoa(n/1024) + "K"
	}
	return size
}

// ── find ────────────────────────────────────────────────────────────────────

type FindParser struct{}

func (p *FindParser) Name() string { return "find" }

func (p *FindParser) Match(output string) bool {
	lines := strings.Split(output, "\n")
	if len(lines) < 3 {
		return false
	}
	count := 0
	for _, line := range lines[:min(10, len(lines))] {
		if strings.HasPrefix(line, "./") || strings.HasPrefix(line, "/") {
			count++
		}
	}
	return count >= len(lines)/2
}

func (p *FindParser) Parse(output string) string {
	lines := strings.Split(output, "\n")
	dirs := make(map[string][]string)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		idx := strings.LastIndex(line, "/")
		if idx > 0 {
			dir := line[:idx]
			file := line[idx+1:]
			dirs[dir] = append(dirs[dir], file)
		} else {
			dirs["."] = append(dirs["."], line)
		}
	}
	var result []string
	for dir, files := range dirs {
		if len(files) > 5 {
			result = append(result, dir+"/ ("+itoa(len(files))+" files)")
		} else {
			for _, f := range files {
				result = append(result, dir+"/"+f)
			}
		}
	}
	if len(result) > 20 {
		result = result[:20]
		result = append(result, "[... "+itoa(len(dirs)-20)+" more]")
	}
	if len(result) == 0 {
		return "(empty)"
	}
	return strings.Join(result, "\n")
}

// ── grep ────────────────────────────────────────────────────────────────────

type GrepParser struct{}

func (p *GrepParser) Name() string { return "grep" }

func (p *GrepParser) Match(output string) bool {
	return strings.Contains(output, ":") && countLines(output) > 3
}

func (p *GrepParser) Parse(output string) string {
	lines := strings.Split(output, "\n")
	files := make(map[string]int)
	var ungrouped []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if idx := strings.Index(line, ":"); idx > 0 {
			file := line[:idx]
			files[file]++
		} else {
			ungrouped = append(ungrouped, line)
		}
	}
	var result []string
	for file, count := range files {
		result = append(result, file+" ("+itoa(count)+" matches)")
	}
	if len(ungrouped) > 0 {
		for i := 0; i < min(5, len(ungrouped)); i++ {
			result = append(result, ungrouped[i])
		}
		if len(ungrouped) > 5 {
			result = append(result, "[... "+itoa(len(ungrouped)-5)+" more lines]")
		}
	}
	if len(result) == 0 {
		return "(no matches)"
	}
	return strings.Join(result, "\n")
}

// ── tree ────────────────────────────────────────────────────────────────────

type TreeParser struct{}

func (p *TreeParser) Name() string { return "tree" }

func (p *TreeParser) Match(output string) bool {
	lines := strings.Split(output, "\n")
	if len(lines) < 2 {
		return false
	}
	re := regexp.MustCompile(`[├└]──`)
	count := 0
	for _, line := range lines[1:] {
		if re.MatchString(line) {
			count++
		}
	}
	return count >= 2
}

func (p *TreeParser) Parse(output string) string {
	lines := strings.Split(output, "\n")
	var result []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if len(result) >= 30 {
			result = append(result, "[... truncated]")
			break
		}
		result = append(result, trimmed)
	}
	if len(result) == 0 {
		return "(empty)"
	}
	return strings.Join(result, "\n")
}

// ── cat / read ──────────────────────────────────────────────────────────────

type CatParser struct{}

func (p *CatParser) Name() string { return "cat" }

func (p *CatParser) Match(output string) bool {
	// Cat has no distinctive pattern — must be explicitly guarded by role="tool"
	// Only match if content is clearly code/file content (not conversation)
	return false // CatParser is disabled — use generic truncation via GenericParser
}

func (p *CatParser) Parse(output string) string {
	return output
}

// ── wc ──────────────────────────────────────────────────────────────────────

type WcParser struct{}

func (p *WcParser) Name() string { return "wc" }

func (p *WcParser) Match(output string) bool {
	// Must start with numbers and contain actual wc output pattern
	// Look for lines with Leading whitespace + numbers + filename pattern
	re := regexp.MustCompile(`(?m)^\s*\d+\s+\d+\s+\d+\s+.+`)
	matches := re.FindAllString(output, -1)
	return len(matches) >= 1 && len(matches) >= countLines(output)/2
}

func (p *WcParser) Parse(output string) string {
	lines := strings.Split(output, "\n")
	var result []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 3 {
			result = append(result, fields[0]+" "+fields[len(fields)-1])
		} else if len(fields) >= 1 {
			result = append(result, line)
		}
	}
	if len(result) == 0 {
		return "(empty)"
	}
	return strings.Join(result, "\n")
}

// ── env ─────────────────────────────────────────────────────────────────────

type EnvParser struct{}

func (p *EnvParser) Name() string { return "env" }

func (p *EnvParser) Match(output string) bool {
	lines := strings.Split(output, "\n")
	if len(lines) < 5 || len(lines) > 200 {
		return false
	}
	count := 0
	for _, line := range lines[:min(20, len(lines))] {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.Contains(trimmed, "=") && !strings.HasPrefix(trimmed, "#") {
			count++
		}
	}
	// Require at least 80% of non-empty lines to have KEY=VALUE format
	nonEmpty := 0
	for _, line := range lines[:min(20, len(lines))] {
		if strings.TrimSpace(line) != "" {
			nonEmpty++
		}
	}
	return nonEmpty > 0 && count*100/nonEmpty >= 80
}

func (p *EnvParser) Parse(output string) string {
	lines := strings.Split(output, "\n")
	var result []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if idx := strings.Index(line, "="); idx > 0 {
			key := line[:idx]
			if len(key) < 30 {
				result = append(result, line)
			}
		}
	}
	if len(result) > 30 {
		result = result[:30]
		result = append(result, "[... more]")
	}
	if len(result) == 0 {
		return "(empty)"
	}
	return strings.Join(result, "\n")
}
