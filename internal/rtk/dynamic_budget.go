package rtk

import (
	"fmt"
	"time"
)

// DynamicBudget manages per-message token budget with adaptation
type DynamicBudget struct {
	GlobalBudget     int
	UsedTokens       int
	RequestStartTime time.Time
	Requests         []*RequestBudget
	ModelConfig      ModelConfig
	ContextWindow    ContextWindow
}

// RequestBudget tracks tokens for individual request
type RequestBudget struct {
	RequestID       string
	AllocationTime  time.Time
	AllocatedTokens int
	UsedTokens      int
	PeakUsage       int
	Status          string // "active", "completed", "failed"
}

// ModelConfig holds model-specific compression settings
type ModelConfig struct {
	Name                  string
	ContextWindowSize     int // total tokens
	CostPerMToken         float64 // $ per 1M tokens
	RecommendedUtilization float64 // 0-1, e.g. 0.8 = use 80% max
	CompressionStrategy   string // "aggressive", "balanced", "conservative"
	MaxInputTokens        int
	MaxOutputTokens       int
}

// ContextWindow calculates context usage and availability
type ContextWindow struct {
	TotalSize          int
	InputTokens        int
	OutputTokens       int
	SystemPromptTokens int
	AvailableTokens    int
	UtilizationRate    float64
	EstimatedRemaining int
}

// NewDynamicBudget creates dynamic budget manager
func NewDynamicBudget(globalBudget int, modelConfig ModelConfig) *DynamicBudget {
	return &DynamicBudget{
		GlobalBudget:     globalBudget,
		UsedTokens:       0,
		RequestStartTime: time.Now(),
		Requests:         []*RequestBudget{},
		ModelConfig:      modelConfig,
		ContextWindow: ContextWindow{
			TotalSize: modelConfig.ContextWindowSize,
		},
	}
}

// AllocateBudget allocates tokens for new request based on context
func (db *DynamicBudget) AllocateBudget(requestID string, estimatedInputTokens int) (*RequestBudget, error) {
	// Calculate remaining global budget
	remaining := db.GlobalBudget - db.UsedTokens
	if remaining <= 0 {
		return nil, fmt.Errorf("global budget exhausted: %d tokens used of %d", db.UsedTokens, db.GlobalBudget)
	}

	// Calculate available per model constraints
	modelAvailable := db.ModelConfig.MaxInputTokens - db.ContextWindow.InputTokens
	if modelAvailable <= 0 {
		return nil, fmt.Errorf("model context window full: %d tokens input", db.ContextWindow.InputTokens)
	}

	// Allocate conservative amount based on estimated input
	allocatedTokens := min(remaining, modelAvailable, int(float64(db.GlobalBudget)*db.ModelConfig.RecommendedUtilization))

	// Ensure minimum allocation
	if allocatedTokens < 100 {
		return nil, fmt.Errorf("insufficient budget for new request: only %d tokens available", allocatedTokens)
	}

	rb := &RequestBudget{
		RequestID:       requestID,
		AllocationTime:  time.Now(),
		AllocatedTokens: allocatedTokens,
		UsedTokens:      0,
		Status:          "active",
	}

	db.Requests = append(db.Requests, rb)
	return rb, nil
}

// UpdateTokenUsage updates token consumption
func (db *DynamicBudget) UpdateTokenUsage(requestID string, tokensUsed int) error {
	for _, rb := range db.Requests {
		if rb.RequestID == requestID {
			if tokensUsed > rb.AllocatedTokens {
				return fmt.Errorf("request %s exceeded allocated budget: %d > %d tokens", requestID, tokensUsed, rb.AllocatedTokens)
			}
			rb.UsedTokens = tokensUsed
			if tokensUsed > rb.PeakUsage {
				rb.PeakUsage = tokensUsed
			}
			db.UsedTokens += tokensUsed
			db.updateContextWindow()
			return nil
		}
	}
	return fmt.Errorf("request %s not found", requestID)
}

// AdaptCompressionStrategy recommends strategy based on budget status
func (db *DynamicBudget) AdaptCompressionStrategy() string {
	utilizationRate := float64(db.UsedTokens) / float64(db.GlobalBudget)

	switch {
	case utilizationRate > 0.9:
		return "maximum" // Extreme compression needed
	case utilizationRate > 0.75:
		return "aggressive" // Strong compression
	case utilizationRate > 0.5:
		return "balanced" // Moderate compression
	default:
		return "conservative" // Minimal compression
	}
}

// GetRemainingBudget returns remaining tokens
func (db *DynamicBudget) GetRemainingBudget() int {
	return db.GlobalBudget - db.UsedTokens
}

// updateContextWindow recalculates context availability
func (db *DynamicBudget) updateContextWindow() {
	cw := &db.ContextWindow

	// Calculate total context usage
	cw.AvailableTokens = cw.TotalSize - cw.InputTokens - cw.OutputTokens - cw.SystemPromptTokens
	cw.UtilizationRate = float64(cw.TotalSize-cw.AvailableTokens) / float64(cw.TotalSize)

	// Estimate remaining with buffer
	bufferPercentage := 0.1 // Keep 10% buffer
	cw.EstimatedRemaining = int(float64(cw.AvailableTokens) * (1 - bufferPercentage))
}

// CalculateContextWindow computes context usage details
func (db *DynamicBudget) CalculateContextWindow(inputTokens, outputTokens, systemPromptTokens int) ContextWindow {
	cw := ContextWindow{
		TotalSize:          db.ModelConfig.ContextWindowSize,
		InputTokens:        inputTokens,
		OutputTokens:       outputTokens,
		SystemPromptTokens: systemPromptTokens,
	}

	// Calculate available space
	used := inputTokens + outputTokens + systemPromptTokens
	cw.AvailableTokens = cw.TotalSize - used

	if cw.AvailableTokens < 0 {
		cw.AvailableTokens = 0
	}

	cw.UtilizationRate = float64(used) / float64(cw.TotalSize)
	cw.EstimatedRemaining = cw.AvailableTokens - int(float64(cw.TotalSize)*0.1) // Keep 10% buffer

	return cw
}

// ShouldCompress returns true if compression should be applied
func (db *DynamicBudget) ShouldCompress() bool {
	utilizationRate := float64(db.UsedTokens) / float64(db.GlobalBudget)
	return utilizationRate > 0.5 || db.ContextWindow.UtilizationRate > 0.7
}

// GetCompressionIntensity returns recommended caveman intensity (1-4)
func (db *DynamicBudget) GetCompressionIntensity() int {
	utilizationRate := float64(db.UsedTokens) / float64(db.GlobalBudget)

	switch {
	case utilizationRate > 0.85:
		return 4 // ultra
	case utilizationRate > 0.7:
		return 3 // full
	case utilizationRate > 0.5:
		return 2 // lite
	default:
		return 1 // none
	}
}

// EstimateCost calculates estimated cost for request
func (db *DynamicBudget) EstimateCost(estimatedTokens int) float64 {
	return float64(estimatedTokens) * db.ModelConfig.CostPerMToken / 1_000_000
}

// CostOptimize suggests compression to meet cost targets
func (db *DynamicBudget) CostOptimize(targetCost float64) (bool, string) {
	remainingBudget := db.GlobalBudget - db.UsedTokens
	maxTokensAtTargetCost := int(targetCost * 1_000_000 / db.ModelConfig.CostPerMToken)

	if remainingBudget <= maxTokensAtTargetCost {
		return true, "Within cost target"
	}

	compressionNeeded := float64(remainingBudget-maxTokensAtTargetCost) / float64(remainingBudget) * 100
	return false, fmt.Sprintf("Need %.0f%% compression to meet cost target of $%.2f", compressionNeeded, targetCost)
}

// CompleteRequest marks request as complete
func (db *DynamicBudget) CompleteRequest(requestID string) error {
	for _, rb := range db.Requests {
		if rb.RequestID == requestID {
			rb.Status = "completed"
			return nil
		}
	}
	return fmt.Errorf("request %s not found", requestID)
}

// GetBudgetSummary returns human-readable budget status
func (db *DynamicBudget) GetBudgetSummary() string {
	remaining := db.GetRemainingBudget()
	utilization := float64(db.UsedTokens) / float64(db.GlobalBudget) * 100

	return fmt.Sprintf(
		"Budget: %d/%d tokens (%.1f%% used, %d remaining)\nContext: %.1f%% utilized (input:%d, output:%d, available:%d)",
		db.UsedTokens, db.GlobalBudget, utilization, remaining,
		db.ContextWindow.UtilizationRate*100,
		db.ContextWindow.InputTokens, db.ContextWindow.OutputTokens, db.ContextWindow.AvailableTokens,
	)
}

// Helper function
func min(values ...int) int {
	if len(values) == 0 {
		return 0
	}
	result := values[0]
	for _, v := range values[1:] {
		if v < result {
			result = v
		}
	}
	return result
}

// PresetModelConfigs provides common model configurations
var PresetModelConfigs = map[string]ModelConfig{
	"gpt-4": {
		Name:                   "gpt-4",
		ContextWindowSize:      8192,
		CostPerMToken:          0.03,
		RecommendedUtilization: 0.8,
		CompressionStrategy:    "balanced",
		MaxInputTokens:         8000,
		MaxOutputTokens:        2000,
	},
	"gpt-4-32k": {
		Name:                   "gpt-4-32k",
		ContextWindowSize:      32768,
		CostPerMToken:          0.06,
		RecommendedUtilization: 0.85,
		CompressionStrategy:    "conservative",
		MaxInputTokens:         30000,
		MaxOutputTokens:        2000,
	},
	"claude-3-opus": {
		Name:                   "claude-3-opus",
		ContextWindowSize:      200000,
		CostPerMToken:          0.015,
		RecommendedUtilization: 0.9,
		CompressionStrategy:    "conservative",
		MaxInputTokens:         195000,
		MaxOutputTokens:        4000,
	},
	"deepseek-v3": {
		Name:                   "deepseek-v3",
		ContextWindowSize:      128000,
		CostPerMToken:          0.001,
		RecommendedUtilization: 0.85,
		CompressionStrategy:    "balanced",
		MaxInputTokens:         120000,
		MaxOutputTokens:        4000,
	},
}
