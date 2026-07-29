package rtk

import (
	"fmt"
	"math"
)

// ContextWindowCalculator manages model context sizing
type ContextWindowCalculator struct {
	model              ModelConfig
	systemPromptTokens int
	conversationTokens int
	bufferPercentage   float64 // Reserved buffer (e.g., 0.1 = 10%)
}

// NewContextWindowCalculator creates calculator for model
func NewContextWindowCalculator(model ModelConfig, systemPromptTokens int) *ContextWindowCalculator {
	return &ContextWindowCalculator{
		model:              model,
		systemPromptTokens: systemPromptTokens,
		conversationTokens: 0,
		bufferPercentage:   0.10, // Default 10% buffer
	}
}

// CalculateAvailable computes available space for current message
func (c *ContextWindowCalculator) CalculateAvailable(inputTokensEstimate int) ContextWindow {
	totalUsed := c.systemPromptTokens + c.conversationTokens + inputTokensEstimate
	available := c.model.ContextWindowSize - totalUsed

	// Reserve buffer
	bufferTokens := int(math.Ceil(float64(c.model.ContextWindowSize) * c.bufferPercentage))
	effectiveAvailable := available - bufferTokens

	if effectiveAvailable < 0 {
		effectiveAvailable = 0
	}

	utilizationRate := float64(totalUsed) / float64(c.model.ContextWindowSize)

	return ContextWindow{
		TotalSize:          c.model.ContextWindowSize,
		InputTokens:        inputTokensEstimate,
		OutputTokens:       0,
		SystemPromptTokens: c.systemPromptTokens,
		AvailableTokens:    effectiveAvailable,
		UtilizationRate:    utilizationRate,
		EstimatedRemaining: effectiveAvailable,
	}
}

// IsCritical returns true if context is near full
func (c *ContextWindowCalculator) IsCritical() bool {
	totalUsed := c.systemPromptTokens + c.conversationTokens
	utilizationRate := float64(totalUsed) / float64(c.model.ContextWindowSize)
	return utilizationRate > 0.85
}

// AddConversationTokens adds tokens from conversation history
func (c *ContextWindowCalculator) AddConversationTokens(tokens int) {
	c.conversationTokens += tokens
}

// ResetConversation clears conversation token count
func (c *ContextWindowCalculator) ResetConversation() {
	c.conversationTokens = 0
}

// PredictOverflow predicts if message will exceed context
func (c *ContextWindowCalculator) PredictOverflow(estimatedInputTokens int, estimatedOutputTokens int) (bool, string) {
	totalUsed := c.systemPromptTokens + c.conversationTokens + estimatedInputTokens + estimatedOutputTokens
	bufferTokens := int(math.Ceil(float64(c.model.ContextWindowSize) * c.bufferPercentage))

	if totalUsed+bufferTokens > c.model.ContextWindowSize {
		overflow := totalUsed + bufferTokens - c.model.ContextWindowSize
		return true, fmt.Sprintf("Predicted overflow: %d tokens over limit", overflow)
	}

	return false, "Safe"
}

// ModelAwareCompressionStrategy determines compression based on model + context
type ModelAwareCompressionStrategy struct {
	model              ModelConfig
	contextCalculator  *ContextWindowCalculator
	compressionHistory map[string]float64 // Filter type -> avg compression rate
}

// NewModelAwareCompressionStrategy creates strategy
func NewModelAwareCompressionStrategy(model ModelConfig, calculator *ContextWindowCalculator) *ModelAwareCompressionStrategy {
	return &ModelAwareCompressionStrategy{
		model:              model,
		contextCalculator:  calculator,
		compressionHistory: make(map[string]float64),
	}
}

// DetermineStrategy returns compression strategy based on context state
func (m *ModelAwareCompressionStrategy) DetermineStrategy() CompressionStrategy {
	utilizationRate := float64(m.contextCalculator.systemPromptTokens+m.contextCalculator.conversationTokens) / float64(m.model.ContextWindowSize)

	// Adapt based on utilization
	switch {
	case utilizationRate > 0.9:
		return CompressionStrategy{
			Name:               "emergency",
			CavemanIntensity:   4, // ultra
			ApplyPonytail:      true,
			PonytailLevel:      4,
			FilterAggression:   100,
			DropMetadata:       true,
			DropComments:       true,
			DropDuplicates:     true,
			MaxLineLength:      50,
			Description:        "Emergency compression - context critical",
		}

	case utilizationRate > 0.75:
		return CompressionStrategy{
			Name:               "aggressive",
			CavemanIntensity:   3, // full
			ApplyPonytail:      true,
			PonytailLevel:      3,
			FilterAggression:   80,
			DropMetadata:       true,
			DropComments:       false,
			DropDuplicates:     true,
			MaxLineLength:      80,
			Description:        "Aggressive compression - context high",
		}

	case utilizationRate > 0.5:
		return CompressionStrategy{
			Name:               "balanced",
			CavemanIntensity:   2, // lite
			ApplyPonytail:      true,
			PonytailLevel:      2,
			FilterAggression:   60,
			DropMetadata:       false,
			DropComments:       false,
			DropDuplicates:     true,
			MaxLineLength:      120,
			Description:        "Balanced compression - context moderate",
		}

	default:
		return CompressionStrategy{
			Name:               "conservative",
			CavemanIntensity:   1, // lite
			ApplyPonytail:      false,
			PonytailLevel:      0,
			FilterAggression:   30,
			DropMetadata:       false,
			DropComments:       false,
			DropDuplicates:     false,
			MaxLineLength:      200,
			Description:        "Conservative compression - context plenty",
		}
	}
}

// CompressionStrategy holds compression parameters
type CompressionStrategy struct {
	Name             string
	CavemanIntensity int    // 1-4
	ApplyPonytail    bool
	PonytailLevel    int    // 1-4
	FilterAggression int    // 0-100
	DropMetadata     bool
	DropComments     bool
	DropDuplicates   bool
	MaxLineLength    int
	Description      string
}

// RecordCompressionRate stores effectiveness for future decisions
func (m *ModelAwareCompressionStrategy) RecordCompressionRate(filterType string, rate float64) {
	m.compressionHistory[filterType] = rate
}

// PredictCompressionRate estimates compression based on filter type and strategy
func (m *ModelAwareCompressionStrategy) PredictCompressionRate(filterType string, strategy CompressionStrategy) float64 {
	// Base rates by filter type
	baseRates := map[string]float64{
		"git":    0.40,
		"grep":   0.35,
		"find":   0.30,
		"ls":     0.20,
		"tree":   0.50,
		"build":  0.45,
		"log":    0.55,
	}

	baseRate := baseRates[filterType]
	if baseRate == 0 {
		baseRate = 0.35 // Default
	}

	// Historical adjustment
	if historical, exists := m.compressionHistory[filterType]; exists {
		baseRate = (baseRate + historical) / 2
	}

	// Apply strategy aggression
	adjusted := baseRate * float64(strategy.FilterAggression) / 100

	// Bonus for metadata/comment dropping
	if strategy.DropMetadata {
		adjusted += 0.05
	}
	if strategy.DropComments {
		adjusted += 0.03
	}
	if strategy.DropDuplicates {
		adjusted += 0.02
	}

	// Cap at 95%
	if adjusted > 0.95 {
		adjusted = 0.95
	}

	return adjusted
}

// EstimateCompressionNeeded calculates required compression percentage
func (m *ModelAwareCompressionStrategy) EstimateCompressionNeeded(inputSize int) float64 {
	available := m.contextCalculator.CalculateAvailable(inputSize)

	if available.AvailableTokens < 0 {
		return 1.0 // Need extreme compression
	}

	// Estimate token count from size (rough: 1 token ≈ 4 chars)
	estimatedTokens := inputSize / 4

	if estimatedTokens > available.AvailableTokens {
		needed := float64(estimatedTokens-available.AvailableTokens) / float64(estimatedTokens)
		return math.Min(needed, 1.0)
	}

	return 0 // No compression needed
}

// SuggestFilter recommends best filter for model/context
func (m *ModelAwareCompressionStrategy) SuggestFilter(toolOutput string, toolType string) string {
	// Cost optimization for models with high token costs
	if m.model.CostPerMToken > 0.01 {
		// Expensive model - use most aggressive filter
		switch toolType {
		case "git":
			return "gitDiff" // 95% compression
		case "build":
			return "buildOutput" // 90% compression
		case "log":
			return "dedupLog" // 95% compression
		default:
			return "smartTruncate" // 90% compression
		}
	}

	// Cheap model - use balanced filter
	switch toolType {
	case "git":
		return "gitStatus" // 90% compression
	case "grep":
		return "grep" // 85% compression
	default:
		return "generic" // 50-70% compression
	}
}

// EnhancedHeadroom integrates all context management
type EnhancedHeadroom struct {
	budget              *DynamicBudget
	calculator          *ContextWindowCalculator
	strategy            *ModelAwareCompressionStrategy
	compressionCallback func(CompressionStrategy)
	warningThreshold    float64
	criticalThreshold   float64
}

// NewEnhancedHeadroom creates integrated headroom manager
func NewEnhancedHeadroom(model ModelConfig, globalBudget int) *EnhancedHeadroom {
	budget := NewDynamicBudget(globalBudget, model)
	calculator := NewContextWindowCalculator(model, 500) // Assume 500 token system prompt
	strategy := NewModelAwareCompressionStrategy(model, calculator)

	return &EnhancedHeadroom{
		budget:           budget,
		calculator:       calculator,
		strategy:         strategy,
		warningThreshold: 0.7,
		criticalThreshold: 0.85,
	}
}

// CheckAndAdapt checks context status and adapts compression
func (e *EnhancedHeadroom) CheckAndAdapt(inputEstimate int) (CompressionStrategy, bool, string) {
	cw := e.calculator.CalculateAvailable(inputEstimate)

	var status string
	isAdapting := false

	if cw.UtilizationRate > e.criticalThreshold {
		status = "CRITICAL"
		isAdapting = true
	} else if cw.UtilizationRate > e.warningThreshold {
		status = "WARNING"
		isAdapting = true
	} else {
		status = "OK"
	}

	strategy := e.strategy.DetermineStrategy()

	if isAdapting && e.compressionCallback != nil {
		e.compressionCallback(strategy)
	}

	return strategy, isAdapting, status
}

// SetCompressionCallback sets handler for adaptation events
func (e *EnhancedHeadroom) SetCompressionCallback(callback func(CompressionStrategy)) {
	e.compressionCallback = callback
}

// GetStatus returns human-readable status
func (e *EnhancedHeadroom) GetStatus() string {
	budget := e.budget.GetBudgetSummary()
	cw := e.calculator.CalculateAvailable(0)

	strategy := e.strategy.DetermineStrategy()

	return fmt.Sprintf(
		"%s\nStrategy: %s (%s)\nUtilization: %.1f%%",
		budget, strategy.Name, strategy.Description, cw.UtilizationRate*100,
	)
}
