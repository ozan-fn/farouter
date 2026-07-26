package rtk

import "strings"

// ── DedupLogParser — port of VansRouter filters/dedupLog.js ────────────
type DedupLogParser struct{}

func (p *DedupLogParser) Name() string { return "dedup-log" }

func (p *DedupLogParser) Parse(input string) string {
	lines := strings.Split(input, "\n")
	if len(lines) == 0 {
		return input
	}

	var out []string
	prev := ""
	runCount := 0
	blankStreak := 0

	flushRun := func() {
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
			flushRun()
			prev = ""
			runCount = 0
			continue
		}
		blankStreak = 0
		if line == prev {
			runCount++
			continue
		}
		flushRun()
		out = append(out, line)
		prev = line
		runCount = 1
		if len(out) >= DEDUP_LINE_MAX {
			out = append(out, "... (truncated at "+itoa(DEDUP_LINE_MAX)+" lines)")
			return strings.Join(out, "\n")
		}
	}
	flushRun()
	return strings.Join(out, "\n")
}
