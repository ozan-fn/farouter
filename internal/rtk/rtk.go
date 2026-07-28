package rtk

import (
	"log"
	"regexp"
	"strings"
)

// ── constants matching VansRouter open-sse/rtk/constants.js ─────────────
const (
	MinCompressSize      = 500
	RawCap               = 10 * 1024 * 1024
	DETECT_WINDOW        = 1024
	GIT_LOG_MAX_LINES    = 200
	GIT_DIFF_HUNK_MAX    = 100
	GIT_DIFF_CONTEXT_KEEP = 3
	DEDUP_LINE_MAX       = 2000
	GREP_PER_FILE_MAX    = 10
	FIND_PER_DIR_MAX     = 10
	FIND_TOTAL_DIR_MAX   = 20
	STATUS_MAX_FILES     = 10
	STATUS_MAX_UNTRACKED = 10
	LS_EXT_SUMMARY_TOP   = 5
	TREE_MAX_LINES       = 200
	SEARCH_PER_DIR_MAX   = 10
	SEARCH_TOTAL_DIR_MAX = 20
	SMART_HEAD           = 120
	SMART_TAIL           = 60
	SMART_MIN_LINES      = 250
	READ_NUM_MIN_RATIO   = 0.7
)

// ── Parser interface ───────────────────────────────────────────────────
type Parser interface {
	Name() string
	Parse(output string) string
}

// ── safeApply — try-catch wrapper matching VansRouter applyFilter.js ───
func safeApply(p Parser, text string) (result string) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[rtk] warning: filter '%s' panicked — passing through raw output: %v", p.Name(), r)
			result = text
		}
	}()
	out := p.Parse(text)
	if out == "" {
		return text
	}
	return out
}

// ── autodetect regexes matching VansRouter open-sse/rtk/autodetect.js ──
var (
	reGitDiff         = regexp.MustCompile(`(?m)^diff --git `)
	reGitDiffHunk     = regexp.MustCompile(`(?m)^@@ `)
	reGitStatus       = regexp.MustCompile(`(?m)^On branch |^nothing to commit|^Changes (not |to be )|^Untracked files:`)
	reGitLog          = regexp.MustCompile(`(?m)^[*|/\\ ]*commit [0-9a-f]{7,40}$`)
	rePorcelain       = regexp.MustCompile(`^[ MADRCU?!][ MADRCU?!] \S`)
	reBuildOutput     = regexp.MustCompile(`(?im)^(npm (warn|error|ERR!)|yarn (warn|error)|\s*Compiling\s+\S+|\s*Downloading\s+\S+|added \d+ package|\[ERROR\]|BUILD (SUCCESS|FAILED)|\s*Finished\s+|Successfully (installed|built)|ERROR:)`)
	reTreeGlyph       = regexp.MustCompile(`[├└]──|│  `)
	reLsRow           = regexp.MustCompile(`(?m)^[-dlbcps][rwx-]{9}`)
	reLsTotal         = regexp.MustCompile(`(?m)^total \d+$`)
	reSearchListHdr   = regexp.MustCompile(`^Result of search in '[^']*' \(total (\d+) files?\):`)
	reReadNumLine     = regexp.MustCompile(`^\s*\d+\|`)
)

func autoDetectFilter(text string) Parser {
	head := text
	if len(text) > DETECT_WINDOW {
		head = text[:DETECT_WINDOW]
	}

	if reGitLog.MatchString(head) {
		return &GitLogParser{}
	}
	if reGitDiff.MatchString(head) || reGitDiffHunk.MatchString(head) {
		return &GitDiffParser{}
	}
	if reGitStatus.MatchString(head) {
		return &GitStatusParser{}
	}
	if reBuildOutput.MatchString(head) {
		return &BuildOutputParser{}
	}
	if isMostlyPorcelain(head) {
		return &GitStatusParser{}
	}

	lines := strings.Split(head, "\n")
	var nonEmpty []string
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			nonEmpty = append(nonEmpty, l)
		}
	}

	first5 := nonEmpty
	if len(first5) > 5 {
		first5 = first5[:5]
	}
	anyGrep := false
	for _, l := range first5 {
		if isGrepLine(l) {
			anyGrep = true
			break
		}
	}
	if anyGrep {
		return &GrepParser{}
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
			return &FindParser{}
		}
	}

	if reTreeGlyph.MatchString(head) {
		return &TreeParser{}
	}

	if reLsTotal.MatchString(head) || countMatches(head, reLsRow) >= 3 {
		return &LsParser{}
	}

	if reSearchListHdr.MatchString(head) {
		return &SearchListParser{}
	}

	if len(lines) >= SMART_MIN_LINES && isLineNumbered(lines) {
		return &ReadNumberedParser{}
	}

	if len(nonEmpty) >= 5 {
		return &DedupLogParser{}
	}

	if len(strings.Split(text, "\n")) >= SMART_MIN_LINES {
		return &SmartTruncateParser{}
	}

	return nil
}

func isGrepLine(line string) bool {
	first := strings.Index(line, ":")
	if first == -1 {
		return false
	}
	second := strings.Index(line[first+1:], ":")
	if second == -1 {
		return false
	}
	lineno := line[first+1 : first+1+second]
	for _, c := range lineno {
		if c < '0' || c > '9' {
			return false
		}
	}
	return len(lineno) > 0
}

func isPathLike(line string) bool {
	t := strings.TrimSpace(line)
	if t == "" {
		return false
	}
	if len(t) >= 2 && ((t[0] >= 'A' && t[0] <= 'Z') || (t[0] >= 'a' && t[0] <= 'z')) && t[1] == ':' && (len(t) == 2 || t[2] == '/' || t[2] == '\\') {
		return true
	}
	if strings.Contains(t, ":") {
		return false
	}
	return strings.HasPrefix(t, ".") || strings.HasPrefix(t, "/") || strings.Contains(t, "/")
}

func isMostlyPorcelain(head string) bool {
	lines := strings.Split(head, "\n")
	var filtered []string
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			filtered = append(filtered, l)
		}
	}
	if len(filtered) < 3 {
		return false
	}
	hits := 0
	for _, l := range filtered {
		if rePorcelain.MatchString(l) {
			hits++
		}
	}
	return float64(hits)/float64(len(filtered)) >= 0.6
}

func isLineNumbered(lines []string) bool {
	hits := 0
	nonEmpty := 0
	sample := lines
	if len(sample) > 100 {
		sample = sample[:100]
	}
	for _, l := range sample {
		if l == "" {
			continue
		}
		nonEmpty++
		if reReadNumLine.MatchString(l) {
			hits++
		}
	}
	if nonEmpty < 5 {
		return false
	}
	return float64(hits)/float64(nonEmpty) >= READ_NUM_MIN_RATIO
}

func countMatches(text string, re *regexp.Regexp) int {
	matches := re.FindAllString(text, -1)
	return len(matches)
}

// ── helpers ────────────────────────────────────────────────────────────
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


