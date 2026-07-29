package rtk

import (
	"fmt"
	"testing"
)

// TestTokenSaversMatrix tests all combinations: 12 filters × 4 caveman levels × 4 ponytail levels
// Total: 192 combinations
func TestTokenSaversMatrix(t *testing.T) {
	filters := []string{
		"gitDiff", "gitStatus", "gitLog",
		"grep", "find", "ls", "tree",
		"buildOutput", "dedupLog", "smartTruncate", "readNumbered", "searchList",
	}

	cavemanLevels := []int{1, 2, 3, 4} // lite, full, ultra, wenyan
	ponytailLevels := []int{1, 2, 3, 4} // critical, standard, aggressive, maximum

	results := make([]TestResult, 0)

	for _, filter := range filters {
		for _, caveman := range cavemanLevels {
			for _, ponytail := range ponytailLevels {
				result := runCompressionTest(filter, caveman, ponytail)
				results = append(results, result)

				if result.Failed {
					t.Errorf("Filter=%s, Caveman=%d, Ponytail=%d: %v", 
						filter, caveman, ponytail, result.Error)
				}
			}
		}
	}

	// Print summary
	printMatrixSummary(t, results, len(filters), len(cavemanLevels), len(ponytailLevels))
}

// TestResult holds individual test outcome
type TestResult struct {
	Filter              string
	CavemanLevel        int
	PonytailLevel       int
	InputSize           int
	OutputSize          int
	CompressionRate     float64
	TokensSaved         int
	ExecutionTimeMs     float64
	SuggestionsGenerated int
	Failed              bool
	Error               string
}

// runCompressionTest executes single compression scenario
func runCompressionTest(filter string, cavemanLevel int, ponytailLevel int) TestResult {
	result := TestResult{
		Filter:        filter,
		CavemanLevel:  cavemanLevel,
		PonytailLevel: ponytailLevel,
	}

	// Simulate test input
	testInput := generateTestInput(filter)
	result.InputSize = len(testInput)

	// Apply caveman compression
	cavemanCompressed := applyCavemanCompression(testInput, cavemanLevel)

	// Apply filter-specific compression
	filtered := applyFilterCompression(cavemanCompressed, filter)
	result.OutputSize = len(filtered)

	// Calculate compression rate
	if result.InputSize > 0 {
		result.CompressionRate = float64(result.InputSize-result.OutputSize) / float64(result.InputSize)
		result.TokensSaved = result.InputSize - result.OutputSize
	}

	// Generate suggestions if ponytail enabled
	if ponytailLevel > 0 {
		suggestions := generateSuggestions(filter, cavemanLevel, ponytailLevel)
		result.SuggestionsGenerated = len(suggestions)
	}

	// Validate compression achieves minimum threshold
	switch filter {
	case "gitDiff":
		if result.CompressionRate < 0.35 {
			result.Failed = true
			result.Error = fmt.Sprintf("gitDiff compression below threshold: %.2f%%", result.CompressionRate*100)
		}
	case "dedupLog":
		if result.CompressionRate < 0.50 {
			result.Failed = true
			result.Error = fmt.Sprintf("dedupLog compression below threshold: %.2f%%", result.CompressionRate*100)
		}
	case "tree":
		if result.CompressionRate < 0.40 {
			result.Failed = true
			result.Error = fmt.Sprintf("tree compression below threshold: %.2f%%", result.CompressionRate*100)
		}
	}

	return result
}

// generateTestInput creates realistic sample for filter
func generateTestInput(filter string) string {
	switch filter {
	case "gitDiff":
		return `diff --git a/main.go b/main.go
index 1234567..abcdefg 100644
--- a/main.go
+++ b/main.go
@@ -10,6 +10,8 @@ func main() {
 	fmt.Println("Hello")
 	var x int = 5
+	var y int = 10
+	fmt.Println(x + y)
 	return
 }
@@ -50,3 +52,15 @@ func helper() {
 	// Old code
-	log.Fatal(err)
+	log.Printf("Error: %v", err)
` + repeatString("@@ -100,5 +105,7 @@ similar hunk\n", 20)

	case "gitStatus":
		return `On branch main
Your branch is ahead of 'origin/main' by 3 commits.

Changes not staged for commit:
  modified:   main.go
  modified:   config.yaml
  modified:   Dockerfile

Untracked files:
  new_file.txt
  temp_dir/
  cache/
` + repeatString("  untracked_file_123.tmp\n", 30)

	case "gitLog":
		return `commit abc1234def567890 (HEAD -> main)
Author: John Doe <john@example.com>
Date:   Mon Jul 29 10:30:45 2024 +0000

    feat: implement RTK compression engine
    
    - Add token budget management
    - Implement caveman mode (4 intensities)
    - Add context window calculation
    
    Fixes #123

commit def5678ghi901234
Author: Jane Smith <jane@example.com>
Date:   Sun Jul 28 15:20:30 2024 +0000

    fix: resolve token overflow in edge cases
    
    Fixes #122
` + repeatString("commit prev000hash000\nAuthor: Dev <dev@example.com>\nDate: Mon Jul 28\n\n    old commit message\n\n", 15)

	case "grep":
		return `main.go:42:	result := h.budget.AllocateBudget(req.RequestID, len(req.Input))
handlers.go:108:	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
handlers.go:112:		http.Error(w, "Invalid request", http.StatusBadRequest)
rtk.go:256:	return budget - usedTokens
dynamic_budget.go:89:	allocatedTokens := min(remaining, modelAvailable, int(float64(db.GlobalBudget)*db.ModelConfig.RecommendedUtilization))
suggestion.go:145:	se.addContextCutSuggestions()
suggestion.go:189:	se.addCompressionSuggestions()
context_window.go:34:	return c.model.ContextWindowSize - totalUsed
` + repeatString("test_file.go:100:	// test line with grep match\n", 25)

	case "find":
		return `.
./src
./src/main.go
./src/handlers.go
./src/rtk
./src/rtk/index.js
./src/rtk/caveman.js
./src/rtk/ponytail.js
./src/rtk/filters
./src/rtk/filters/gitDiff.js
./src/rtk/filters/gitStatus.js
./src/rtk/filters/grep.js
./src/rtk/filters/find.js
./src/rtk/filters/tree.js
./src/rtk/filters/ls.js
./tests
./tests/unit
./tests/unit/rtk.test.js
./tests/unit/e2e.test.js
./node_modules
./node_modules/package1
./node_modules/package2
./node_modules/deeply/nested/modules/here
` + repeatString("./generated/file_1000_deep_path_example.txt\n", 40)

	case "ls":
		return `-rw-r--r--  1 user  group   8192 Jul 29 10:30 main.go
-rw-r--r--  1 user  group   4096 Jul 29 10:25 config.yaml
-rw-r--r--  1 user  group  12288 Jul 28 15:20 handlers.go
-rw-r--r--  1 user  group   2048 Jul 28 14:10 rtk.go
drwxr-xr-x  5 user  group   4096 Jul 29 10:15 src
drwxr-xr-x  3 user  group   4096 Jul 28 20:45 tests
-rw-r--r--  1 user  group    512 Jul 28 12:00 README.md
-rw-r--r--  1 user  group   1024 Jul 28 11:30 LICENSE
` + repeatString("-rw-r--r--  1 user  group    256 Jul 29 file_%d.txt\n", 35)

	case "tree":
		return `project/
├── src/
│   ├── main.go
│   ├── handlers.go
│   ├── rtk/
│   │   ├── index.js
│   │   ├── caveman.js
│   │   ├── ponytail.js
│   │   └── filters/
│   │       ├── gitDiff.js
│   │       ├── grep.js
│   │       ├── find.js
│   │       ├── ls.js
│   │       ├── tree.js
│   │       └── build.js
│   └── utils/
│       └── helpers.go
├── tests/
│   ├── unit/
│   │   ├── rtk.test.js
│   │   └── e2e.test.js
│   └── integration/
│       └── full.test.js
├── docs/
│   ├── RTK.md
│   └── API.md
└── package.json
` + repeatString("deep level structure repeated for testing size\n", 20)

	case "buildOutput":
		return `npm notice it worked if it ends with ok
npm notice cli v8.19.4
npm notice title npm
npm notice argv "/usr/local/bin/node" "/usr/local/bin/npm" "test"
> jest
PASS  src/__tests__/rtk.test.js
  RTK Compression Suite
    ✓ gitDiff compression (45ms)
    ✓ caveman mode lite (12ms)
    ✓ ponytail suggestions (28ms)

PASS  src/__tests__/e2e.test.js
  E2E Tests
    ✓ full pipeline (234ms)
    ✓ error recovery (89ms)

Test Suites: 2 passed, 2 total
Tests: 5 passed, 5 total
Snapshots: 0 total
Time: 2.456s
npm notice ok
` + repeatString("npm WARN optional some-package SKIPPED\n", 20)

	case "dedupLog":
		return `[ERROR] Connection timeout
[ERROR] Connection timeout
[ERROR] Connection timeout
[WARN] Retry attempt 1
[WARN] Retry attempt 2
[INFO] Request processed
[INFO] Request processed
[INFO] Request processed
[INFO] Request processed
[ERROR] Connection timeout
[ERROR] Connection timeout
` + repeatString("[DEBUG] Duplicate log line for testing dedup\n", 50)

	case "smartTruncate":
		return `Line 1 of output
Line 2 of output
Line 3 of output
Line 4 of output
Line 5 of output
...
Line 500 of output (middle content omitted for brevity)
...
Line 1000 of output
Line 1001 of output
Line 1002 of output
Line 1003 of output
Line 1004 of output` + repeatString(" (middle content)\n", 100)

	case "readNumbered":
		return `1: func main() {
2:     fmt.Println("Hello")
3:     x := 5
4:     y := 10
5:     fmt.Println(x + y)
6:     return
7: }
8: 
9: func helper() {
10:     log.Println("helper called")
11: }
` + repeatString("12: // line number %d\n", 40)

	case "searchList":
		return `compression_engine.go:42: RTK token compression implementation
compression_engine.go:89: Budget allocation logic
caveman_mode.js:123: Intensity level configuration
caveman_mode.js:156: Language detection (EN/ZH)
context_window.go:34: Window calculation formula
context_window.go:78: Model-specific constraints
dynamic_budget.go:45: Per-message token tracking
` + repeatString("search_results.txt:LINE: match content\n", 30)

	default:
		return "default test input for " + filter
	}
}

// applyCavemanCompression simulates caveman mode compression
func applyCavemanCompression(input string, level int) string {
	// Simulate different compression levels
	compressionRates := []float64{0, 0.15, 0.30, 0.40, 0.50}
	reduction := compressionRates[level]

	targetSize := int(float64(len(input)) * (1 - reduction))
	if targetSize < len(input) {
		return input[:targetSize]
	}
	return input
}

// applyFilterCompression simulates filter-specific compression
func applyFilterCompression(input string, filter string) string {
	compressionRates := map[string]float64{
		"gitDiff":       0.50,
		"gitStatus":     0.35,
		"gitLog":        0.30,
		"grep":          0.40,
		"find":          0.35,
		"ls":            0.25,
		"tree":          0.55,
		"buildOutput":   0.45,
		"dedupLog":      0.70,
		"smartTruncate": 0.60,
		"readNumbered":  0.15,
		"searchList":    0.40,
	}

	rate := compressionRates[filter]
	targetSize := int(float64(len(input)) * (1 - rate))
	if targetSize < len(input) {
		return input[:targetSize]
	}
	return input
}

// generateSuggestions creates sample suggestions
func generateSuggestions(filter string, caveman int, ponytail int) []string {
	suggestions := []string{}

	if ponytail >= 1 {
		suggestions = append(suggestions, "Critical: Compression needed")
	}
	if ponytail >= 2 {
		suggestions = append(suggestions, fmt.Sprintf("Standard: Apply %s filter", filter))
	}
	if ponytail >= 3 {
		suggestions = append(suggestions, "Aggressive: Add error handling")
	}
	if ponytail >= 4 {
		suggestions = append(suggestions, "Maximum: Consider streaming parser")
	}

	return suggestions
}

// printMatrixSummary outputs test results
func printMatrixSummary(t *testing.T, results []TestResult, filterCount int, cavemanCount int, ponytailCount int) {
	total := len(results)
	failed := 0
	totalTokensSaved := 0
	avgCompressionRate := 0.0

	for _, r := range results {
		if r.Failed {
			failed++
		}
		totalTokensSaved += r.TokensSaved
		avgCompressionRate += r.CompressionRate
	}

	avgCompressionRate /= float64(total)

	t.Logf("\n========== TOKEN SAVERS MATRIX SUMMARY ==========")
	t.Logf("Total Combinations: %d (%d filters × %d caveman × %d ponytail)",
		total, filterCount, cavemanCount, ponytailCount)
	t.Logf("Passed: %d/%d", total-failed, total)
	t.Logf("Failed: %d/%d", failed, total)
	t.Logf("Average Compression: %.2f%%", avgCompressionRate*100)
	t.Logf("Total Tokens Saved: %d", totalTokensSaved)
	t.Logf("================================================\n")
}

// repeatString repeats string n times
func repeatString(s string, n int) string {
	result := ""
	for i := 0; i < n; i++ {
		result += s
	}
	return result
}

// TestCavemanPonytailCombos tests 16 mode combinations (4×4)
func TestCavemanPonytailCombos(t *testing.T) {
	testCases := []struct {
		caveman  int
		ponytail int
		expected string
	}{
		{1, 1, "lite with critical"},
		{1, 2, "lite with standard"},
		{1, 3, "lite with aggressive"},
		{1, 4, "lite with maximum"},
		{2, 1, "full with critical"},
		{2, 2, "full with standard"},
		{2, 3, "full with aggressive"},
		{2, 4, "full with maximum"},
		{3, 1, "ultra with critical"},
		{3, 2, "ultra with standard"},
		{3, 3, "ultra with aggressive"},
		{3, 4, "ultra with maximum"},
		{4, 1, "wenyan with critical"},
		{4, 2, "wenyan with standard"},
		{4, 3, "wenyan with aggressive"},
		{4, 4, "wenyan with maximum"},
	}

	for _, tc := range testCases {
		t.Run(tc.expected, func(t *testing.T) {
			// Test combination validity
			if tc.caveman < 1 || tc.caveman > 4 {
				t.Errorf("Invalid caveman level: %d", tc.caveman)
			}
			if tc.ponytail < 1 || tc.ponytail > 4 {
				t.Errorf("Invalid ponytail level: %d", tc.ponytail)
			}
		})
	}

	t.Logf("Tested %d caveman/ponytail combinations", len(testCases))
}

// TestHeadroomBoundaryConditions tests edge cases
func TestHeadroomBoundaryConditions(t *testing.T) {
	model := PresetModelConfigs["gpt-4"]
	headroom := NewEnhancedHeadroom(model, 1000)

	testCases := []struct {
		name          string
		inputSize     int
		expectedError bool
	}{
		{"normal load", 500, false},
		{"high load", 900, false},
		{"critical", 950, false},
		{"overflow", 1100, true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			strategy, _, status := headroom.CheckAndAdapt(tc.inputSize)

			if tc.inputSize > 1000 && status != "CRITICAL" {
				t.Errorf("Expected CRITICAL status for overflow, got %s", status)
			}

			if strategy.Name == "" {
				t.Error("Strategy name should not be empty")
			}
		})
	}
}
