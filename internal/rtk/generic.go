package rtk

import (
	"strings"
)

// GenericParser is a fallback parser for unknown tool output
type GenericParser struct {
	maxLines int
}

func (p *GenericParser) Name() string { return "generic" }

func (p *GenericParser) Match(output string) bool {
	// Always matches as fallback
	return true
}

func (p *GenericParser) Parse(output string) string {
	lines := strings.Split(output, "\n")
	
	// Remove blank lines
	var filtered []string
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			filtered = append(filtered, line)
		}
	}
	
	if len(filtered) == 0 {
		return "(empty)"
	}
	
	if len(filtered) <= p.maxLines {
		return strings.Join(filtered, "\n")
	}
	
	// Truncate
	truncated := strings.Join(filtered[:p.maxLines], "\n")
	omitted := len(filtered) - p.maxLines
	return truncated + "\n\n[... " + itoa(omitted) + " lines omitted]"
}
