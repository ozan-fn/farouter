package rtk

import "strings"

// ── SmartTruncateParser — port of VansRouter filters/smartTruncate.js ──
type SmartTruncateParser struct{}

func (p *SmartTruncateParser) Name() string { return "smart-truncate" }

func (p *SmartTruncateParser) Parse(input string) string {
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
	result = append(result, "... +"+itoa(cut)+" lines truncated")
	result = append(result, tail...)
	return strings.Join(result, "\n")
}
