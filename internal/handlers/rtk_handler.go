package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"farouter/internal/rtk"
)

// RTKHandler manages RTK API endpoints
type RTKHandler struct {
	budget              *rtk.DynamicBudget
	calculator          *rtk.ContextWindowCalculator
	strategy            *rtk.ModelAwareCompressionStrategy
	enhancedHeadroom    *rtk.EnhancedHeadroom
	suggestionEngine    *rtk.SuggestionEngine
	settings            *RTKSettings
	statsCollector      *RTKStatsCollector
	mu                  sync.RWMutex
}

// RTKSettings holds user configuration
type RTKSettings struct {
	Enabled             bool   `json:"enabled"`
	CavemanMode         int    `json:"cavemanMode"`
	PonytailMode        int    `json:"ponytailMode"`
	CompressionStrategy string `json:"compressionStrategy"`
	TokenBudget         int    `json:"tokenBudget"`
	DynamicBudgeting    bool   `json:"dynamicBudgeting"`
	ModelName           string `json:"modelName"`
}

// RTKStats holds runtime statistics
type RTKStats struct {
	TokensUsed           int                    `json:"tokensUsed"`
	TokensBudget         int                    `json:"tokensBudget"`
	CompressionRate      float64                `json:"compressionRate"`
	ContextUtilization   float64                `json:"contextUtilization"`
	LastCompression      string                 `json:"lastCompression"`
	ActiveRequests       int                    `json:"activeRequests"`
	Suggestions          []rtk.Suggestion       `json:"suggestions"`
	CurrentStrategy      rtk.CompressionStrategy `json:"currentStrategy"`
}

// RTKStatsCollector collects metrics
type RTKStatsCollector struct {
	totalTokensCompressed int
	totalTokensUsed       int
	compressionEvents     int
	activeRequests        int
	avgCompressionRate    float64
}

// NewRTKHandler creates new RTK API handler
func NewRTKHandler(model rtk.ModelConfig) *RTKHandler {
	budget := rtk.NewDynamicBudget(model.MaxInputTokens, model)
	calculator := rtk.NewContextWindowCalculator(model, 500)
	strategy := rtk.NewModelAwareCompressionStrategy(model, calculator)
	headroom := rtk.NewEnhancedHeadroom(model, model.MaxInputTokens)

	settings := &RTKSettings{
		Enabled:             true,
		CavemanMode:         1,
		PonytailMode:        1,
		CompressionStrategy: "balanced",
		TokenBudget:         model.MaxInputTokens,
		DynamicBudgeting:    true,
		ModelName:           model.Name,
	}

	return &RTKHandler{
		budget:           budget,
		calculator:       calculator,
		strategy:         strategy,
		enhancedHeadroom: headroom,
		suggestionEngine: rtk.NewSuggestionEngine(rtk.SuggestionStandard),
		settings:         settings,
		statsCollector: &RTKStatsCollector{
			totalTokensCompressed: 0,
			totalTokensUsed:       0,
			compressionEvents:     0,
			activeRequests:        0,
			avgCompressionRate:    0.0,
		},
	}
}

// RegisterRoutes registers all RTK API routes
func (h *RTKHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/rtk/settings", h.getSettings)
	mux.HandleFunc("POST /api/rtk/settings", h.updateSettings)
	mux.HandleFunc("GET /api/rtk/stats", h.getStats)
	mux.HandleFunc("POST /api/rtk/stats/reset", h.resetStats)
	mux.HandleFunc("POST /api/rtk/compress", h.compressOutput)
	mux.HandleFunc("GET /api/rtk/models", h.listModels)
	mux.HandleFunc("POST /api/rtk/models/select", h.selectModel)
}

// getSettings returns current RTK settings
func (h *RTKHandler) getSettings(w http.ResponseWriter, r *http.Request) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(h.settings)
}

// updateSettings updates RTK settings
func (h *RTKHandler) updateSettings(w http.ResponseWriter, r *http.Request) {
	var newSettings RTKSettings
	if err := json.NewDecoder(r.Body).Decode(&newSettings); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	h.mu.Lock()
	h.settings = &newSettings
	h.enhancedHeadroom.SetCompressionCallback(func(cs rtk.CompressionStrategy) {
		// Log compression strategy change
		fmt.Printf("Compression strategy adapted: %s\n", cs.Name)
	})
	h.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(h.settings)
}

// getStats returns current RTK statistics
func (h *RTKHandler) getStats(w http.ResponseWriter, r *http.Request) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	compCtx := &rtk.CompressionContext{
		TokensUsed:       h.statsCollector.totalTokensUsed,
		TokensBudget:     h.settings.TokenBudget,
		RemainingTokens:  h.settings.TokenBudget - h.statsCollector.totalTokensUsed,
		CompressionRate:  h.statsCollector.avgCompressionRate,
		ContextUtilization: float64(h.statsCollector.totalTokensUsed) / float64(h.settings.TokenBudget),
	}

	suggestions := h.suggestionEngine.GenerateSuggestions(compCtx)

	strategy, _, _ := h.enhancedHeadroom.CheckAndAdapt(0)

	stats := RTKStats{
		TokensUsed:         h.statsCollector.totalTokensUsed,
		TokensBudget:       h.settings.TokenBudget,
		CompressionRate:    h.statsCollector.avgCompressionRate * 100,
		ContextUtilization: compCtx.ContextUtilization,
		LastCompression:    "N/A",
		ActiveRequests:     h.statsCollector.activeRequests,
		Suggestions:        suggestions,
		CurrentStrategy:    strategy,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

// resetStats clears collected statistics
func (h *RTKHandler) resetStats(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	h.statsCollector = &RTKStatsCollector{}
	h.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "Stats reset successfully"})
}

// CompressRequest holds compression request
type CompressRequest struct {
	FilterType string `json:"filterType"`
	Input      string `json:"input"`
	RequestID  string `json:"requestId"`
}

// CompressResponse holds compression result
type CompressResponse struct {
	Output             string  `json:"output"`
	InputSize          int     `json:"inputSize"`
	OutputSize         int     `json:"outputSize"`
	CompressionRate    float64 `json:"compressionRate"`
	TokensSaved        int     `json:"tokensSaved"`
	Strategy           string  `json:"strategy"`
	Suggestions        []string `json:"suggestions"`
}

// compressOutput applies RTK compression to output
func (h *RTKHandler) compressOutput(w http.ResponseWriter, r *http.Request) {
	if !h.settings.Enabled {
		http.Error(w, "RTK compression is disabled", http.StatusBadRequest)
		return
	}

	var req CompressRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	// Allocate budget
	_, err := h.budget.AllocateBudget(req.RequestID, len(req.Input))
	if err != nil {
		http.Error(w, fmt.Sprintf("Budget allocation failed: %v", err), http.StatusBadRequest)
		return
	}

	// Determine compression strategy
	strategy, _, _ := h.enhancedHeadroom.CheckAndAdapt(len(req.Input))

	// Simulate compression (in real impl, apply actual filters)
	compressionRate := h.strategy.PredictCompressionRate(req.FilterType, strategy)
	outputSize := int(float64(len(req.Input)) * (1 - compressionRate))
	tokensSaved := len(req.Input) - outputSize

	// Update stats
	h.statsCollector.totalTokensCompressed += tokensSaved
	h.statsCollector.totalTokensUsed += len(req.Input)
	h.statsCollector.compressionEvents++
	if h.statsCollector.compressionEvents > 0 {
		h.statsCollector.avgCompressionRate = float64(h.statsCollector.totalTokensCompressed) / float64(h.statsCollector.totalTokensUsed)
	}

	// Generate suggestions if ponytail enabled
	var suggestionStrs []string
	if h.settings.PonytailMode > 0 {
		compCtx := &rtk.CompressionContext{
			FilterType:        req.FilterType,
			InputSize:         len(req.Input),
			OutputSize:        outputSize,
			CompressionRate:   compressionRate,
			ContextUtilization: float64(h.statsCollector.totalTokensUsed) / float64(h.settings.TokenBudget),
		}
		suggestions := h.suggestionEngine.GenerateSuggestions(compCtx)
		for _, s := range suggestions {
			suggestionStrs = append(suggestionStrs, s.Format())
		}
	}

	response := CompressResponse{
		Output:          req.Input[:outputSize], // Simulated compressed output
		InputSize:       len(req.Input),
		OutputSize:      outputSize,
		CompressionRate: compressionRate * 100,
		TokensSaved:     tokensSaved,
		Strategy:        strategy.Name,
		Suggestions:     suggestionStrs,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)

	// Complete request
	h.budget.CompleteRequest(req.RequestID)
}

// listModels returns available model configurations
func (h *RTKHandler) listModels(w http.ResponseWriter, r *http.Request) {
	models := []map[string]interface{}{}
	for name, config := range rtk.PresetModelConfigs {
		models = append(models, map[string]interface{}{
			"name":                name,
			"contextWindowSize":   config.ContextWindowSize,
			"costPerMToken":       config.CostPerMToken,
			"recommendedUtilization": config.RecommendedUtilization,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(models)
}

// selectModel switches to a different model
func (h *RTKHandler) selectModel(w http.ResponseWriter, r *http.Request) {
	var req map[string]string
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	modelName, ok := req["model"]
	if !ok {
		http.Error(w, "Model name required", http.StatusBadRequest)
		return
	}

	config, exists := rtk.PresetModelConfigs[modelName]
	if !exists {
		http.Error(w, "Model not found", http.StatusBadRequest)
		return
	}

	h.mu.Lock()
	h.settings.ModelName = modelName
	h.budget = rtk.NewDynamicBudget(config.MaxInputTokens, config)
	h.calculator = rtk.NewContextWindowCalculator(config, 500)
	h.strategy = rtk.NewModelAwareCompressionStrategy(config, h.calculator)
	h.enhancedHeadroom = rtk.NewEnhancedHeadroom(config, config.MaxInputTokens)
	h.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": fmt.Sprintf("Model switched to %s", modelName)})
}

// HealthCheck returns RTK handler health status
func (h *RTKHandler) HealthCheck(w http.ResponseWriter, r *http.Request) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	health := map[string]interface{}{
		"status":   "healthy",
		"rtk":      h.settings.Enabled,
		"model":    h.settings.ModelName,
		"active":   h.statsCollector.activeRequests,
		"budget":   h.settings.TokenBudget,
		"used":     h.statsCollector.totalTokensUsed,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(health)
}
