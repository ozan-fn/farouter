package rtk

import (
	"fmt"
	"strings"
)

// SuggestionLevel represents ponytail mode intensity (1-4)
type SuggestionLevel int

const (
	SuggestionCritical SuggestionLevel = iota + 1
	SuggestionStandard
	SuggestionAggressive
	SuggestionMaximum
)

// Suggestion represents a context-cutting or improvement recommendation
type Suggestion struct {
	Level    SuggestionLevel
	Category string // "context-cut", "compression", "error-handling", "testing", "optimization"
	Text     string
	Priority int // 1-5, higher = more important
	Impact   string // "high", "medium", "low"
}

// SuggestionEngine generates recommendations based on compression context
type SuggestionEngine struct {
	level      SuggestionLevel
	context    *CompressionContext
	suggestions []Suggestion
}

// CompressionContext holds state for suggestion generation
type CompressionContext struct {
	FilterType       string  // "git", "grep", "build", etc
	InputSize        int     // bytes
	OutputSize       int     // bytes
	CompressionRate  float64 // percentage
	TokensUsed       int
	TokensBudget     int
	RemainingTokens  int
	ContextUtilization float64 // 0-1
	HasErrors        bool
	ErrorCount       int
	ToolName         string
}

// NewSuggestionEngine creates suggestion engine with given intensity
func NewSuggestionEngine(level SuggestionLevel) *SuggestionEngine {
	return &SuggestionEngine{
		level:       level,
		suggestions: []Suggestion{},
	}
}

// GenerateSuggestions analyzes compression context and generates recommendations
func (se *SuggestionEngine) GenerateSuggestions(ctx *CompressionContext) []Suggestion {
	se.context = ctx
	se.suggestions = []Suggestion{}

	if se.level == 0 {
		return se.suggestions
	}

	// Context cutting recommendations (most aggressive)
	if ctx.ContextUtilization > 0.8 {
		se.addContextCutSuggestions()
	}

	// Compression improvement suggestions
	se.addCompressionSuggestions()

	// Error handling suggestions
	if ctx.HasErrors {
		se.addErrorHandlingSuggestions()
	}

	// Token budget suggestions
	if ctx.RemainingTokens < ctx.TokensBudget/4 {
		se.addTokenOptimizationSuggestions()
	}

	// Testing suggestions (at higher levels)
	if se.level >= SuggestionAggressive {
		se.addTestingSuggestions()
	}

	// Performance suggestions (maximum level)
	if se.level == SuggestionMaximum {
		se.addPerformanceSuggestions()
	}

	return se.suggestions
}

// addContextCutSuggestions recommends what to drop from context
func (se *SuggestionEngine) addContextCutSuggestions() {
	cuts := []Suggestion{}

	// Critical: drop verbose logs
	cuts = append(cuts, Suggestion{
		Level:    SuggestionCritical,
		Category: "context-cut",
		Text:     "Consider truncating verbose build logs (keep errors only)",
		Priority: 5,
		Impact:   "high",
	})

	// Standard: drop old git commits
	if se.context.FilterType == "git" {
		cuts = append(cuts, Suggestion{
			Level:    SuggestionStandard,
			Category: "context-cut",
			Text:     "Keep only last 10 commits, archive older history",
			Priority: 4,
			Impact:   "high",
		})
	}

	// Aggressive: suggest external storage
	if se.level >= SuggestionAggressive {
		cuts = append(cuts, Suggestion{
			Level:    SuggestionAggressive,
			Category: "context-cut",
			Text:     "Move test results to external storage, reference by link only",
			Priority: 3,
			Impact:   "medium",
		})
	}

	// Maximum: suggest chunking
	if se.level == SuggestionMaximum {
		cuts = append(cuts, Suggestion{
			Level:    SuggestionMaximum,
			Category: "context-cut",
			Text:     "Split into multiple requests, use continuation tokens",
			Priority: 2,
			Impact:   "medium",
		})
	}

	se.suggestions = append(se.suggestions, cuts...)
}

// addCompressionSuggestions recommends compression improvements
func (se *SuggestionEngine) addCompressionSuggestions() {
	if se.context.CompressionRate < 30 {
		se.suggestions = append(se.suggestions, Suggestion{
			Level:    SuggestionCritical,
			Category: "compression",
			Text:     fmt.Sprintf("Current compression only %.0f%%, apply stricter filtering", se.context.CompressionRate),
			Priority: 5,
			Impact:   "high",
		})
	}

	if se.level >= SuggestionStandard && se.context.CompressionRate < 50 {
		se.suggestions = append(se.suggestions, Suggestion{
			Level:    SuggestionStandard,
			Category: "compression",
			Text:     "Enable caveman-ultra mode for this tool type",
			Priority: 4,
			Impact:   "high",
		})
	}

	if se.level >= SuggestionAggressive {
		se.suggestions = append(se.suggestions, Suggestion{
			Level:    SuggestionAggressive,
			Category: "compression",
			Text:     fmt.Sprintf("Combine multiple filters for %s output", se.context.FilterType),
			Priority: 3,
			Impact:   "medium",
		})
	}
}

// addErrorHandlingSuggestions recommends error recovery strategies
func (se *SuggestionEngine) addErrorHandlingSuggestions() {
	if se.context.ErrorCount > 0 {
		se.suggestions = append(se.suggestions, Suggestion{
			Level:    SuggestionCritical,
			Category: "error-handling",
			Text:     fmt.Sprintf("Handle %d errors with fallback parser", se.context.ErrorCount),
			Priority: 5,
			Impact:   "high",
		})
	}

	if se.level >= SuggestionStandard {
		se.suggestions = append(se.suggestions, Suggestion{
			Level:    SuggestionStandard,
			Category: "error-handling",
			Text:     "Add retry logic with exponential backoff",
			Priority: 3,
			Impact:   "medium",
		})
	}
}

// addTokenOptimizationSuggestions recommends token budget optimization
func (se *SuggestionEngine) addTokenOptimizationSuggestions() {
	remaining := se.context.RemainingTokens
	budget := se.context.TokensBudget

	se.suggestions = append(se.suggestions, Suggestion{
		Level:    SuggestionCritical,
		Category: "compression",
		Text:     fmt.Sprintf("Only %d tokens remaining (%.0f%% budget used), aggressive compression needed", remaining, float64(budget-remaining)/float64(budget)*100),
		Priority: 5,
		Impact:   "high",
	})

	if se.level >= SuggestionAggressive {
		se.suggestions = append(se.suggestions, Suggestion{
			Level:    SuggestionAggressive,
			Category: "compression",
			Text:     "Switch to wenyan mode for extreme compression (50-60%)",
			Priority: 4,
			Impact:   "high",
		})
	}
}

// addTestingSuggestions recommends test coverage
func (se *SuggestionEngine) addTestingSuggestions() {
	se.suggestions = append(se.suggestions, Suggestion{
		Level:    SuggestionAggressive,
		Category: "testing",
		Text:     fmt.Sprintf("Add tests for %s filter with edge cases", se.context.FilterType),
		Priority: 3,
		Impact:   "medium",
	})

	se.suggestions = append(se.suggestions, Suggestion{
		Level:    SuggestionAggressive,
		Category: "testing",
		Text:     "Test all caveman intensity combinations (4 levels)",
		Priority: 2,
		Impact:   "low",
	})
}

// addPerformanceSuggestions recommends performance optimizations
func (se *SuggestionEngine) addPerformanceSuggestions() {
	se.suggestions = append(se.suggestions, Suggestion{
		Level:    SuggestionMaximum,
		Category: "optimization",
		Text:     "Implement streaming parser for large inputs",
		Priority: 3,
		Impact:   "medium",
	})

	se.suggestions = append(se.suggestions, Suggestion{
		Level:    SuggestionMaximum,
		Category: "optimization",
		Text:     "Cache frequently used filter outputs",
		Priority: 2,
		Impact:   "low",
	})

	se.suggestions = append(se.suggestions, Suggestion{
		Level:    SuggestionMaximum,
		Category: "optimization",
		Text:     "Profile compression performance with pprof",
		Priority: 1,
		Impact:   "low",
	})
}

// Format returns formatted suggestion string
func (s Suggestion) Format() string {
	icon := "→"
	if s.Priority >= 4 {
		icon = "⚠"
	}
	return fmt.Sprintf("%s [%s] %s (impact: %s)", icon, s.Category, s.Text, s.Impact)
}

// FilterBySuggestionLevel filters suggestions by minimum level
func (se *SuggestionEngine) FilterBySuggestionLevel(minLevel SuggestionLevel) []Suggestion {
	filtered := []Suggestion{}
	for _, s := range se.suggestions {
		if s.Level >= minLevel {
			filtered = append(filtered, s)
		}
	}
	return filtered
}

// SuggestionsToString formats all suggestions for display
func (se *SuggestionEngine) SuggestionsToString() string {
	if len(se.suggestions) == 0 {
		return "No suggestions."
	}

	var buf strings.Builder
	buf.WriteString(fmt.Sprintf("Suggestions (%d):\n", len(se.suggestions)))
	for i, s := range se.suggestions {
		buf.WriteString(fmt.Sprintf("%d. %s\n", i+1, s.Format()))
	}
	return buf.String()
}
