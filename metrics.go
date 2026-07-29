package main

import (
	"encoding/json"
	"net/http"
	"time"
)

func startMetricsCollector() {
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for range ticker.C {
			active := 0
			exhausted := 0
			suspended := 0
			totalRemaining := 0

			for _, a := range accounts {
				a.mu.Lock()
				if a.suspended {
					suspended++
				} else if a.exhausted {
					exhausted++
				} else {
					active++
				}
				totalRemaining += a.remaining
				a.mu.Unlock()
			}

			rotationMu.Lock()
			poolSize := 0
			for i := 0; i < 3; i++ {
				if activeBatch[i] != nil {
					poolSize++
				}
			}
			rotationMu.Unlock()

			analyticsData.RecordMetric(active, exhausted, suspended, totalRemaining, poolSize)
		}
	}()
}

func handleMetrics(w http.ResponseWriter, r *http.Request) {
	metrics := analyticsData.GetMetrics()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(metrics)
}

func handleLogs(w http.ResponseWriter, r *http.Request) {
	logs := analyticsData.GetLogs()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(logs)
}

func handleClearLogs(w http.ResponseWriter, r *http.Request) {
	analyticsData.ClearLogs()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "cleared"})
}
