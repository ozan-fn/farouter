package analytics

import (
	"sync"
	"time"
)

type MetricSnapshot struct {
	Timestamp       int64 `json:"timestamp"`
	ActiveCount     int   `json:"activeCount"`
	ExhaustedCount  int   `json:"exhaustedCount"`
	SuspendedCount  int   `json:"suspendedCount"`
	TotalRemaining  int   `json:"totalRemaining"`
	PoolSize        int   `json:"poolSize"`
	RequestCount    int   `json:"requestCount"`
	FailedCount     int   `json:"failedCount"`
	TokensUsed      int64 `json:"tokensUsed"`
	TokensGenerated int64 `json:"tokensGenerated"`
}

type EventLog struct {
	Timestamp int64  `json:"timestamp"`
	Message   string `json:"message"`
	Type      string `json:"type"`
	AccountID string `json:"accountId,omitempty"`
}

type Analytics struct {
	mu              sync.RWMutex
	metrics         []MetricSnapshot
	logs            []EventLog
	requestCount    int
	failedCount     int
	tokensUsed      int64
	tokensGenerated int64
	maxMetrics      int
	maxLogs         int
}

func New() *Analytics {
	return &Analytics{
		metrics:    make([]MetricSnapshot, 0, 100),
		logs:       make([]EventLog, 0, 200),
		maxMetrics: 100,
		maxLogs:    200,
	}
}

func (a *Analytics) RecordMetric(active, exhausted, suspended, totalRemaining, poolSize int) {
	a.mu.Lock()
	defer a.mu.Unlock()

	snapshot := MetricSnapshot{
		Timestamp:       time.Now().UnixMilli(),
		ActiveCount:     active,
		ExhaustedCount:  exhausted,
		SuspendedCount:  suspended,
		TotalRemaining:  totalRemaining,
		PoolSize:        poolSize,
		RequestCount:    a.requestCount,
		FailedCount:     a.failedCount,
		TokensUsed:      a.tokensUsed,
		TokensGenerated: a.tokensGenerated,
	}

	a.metrics = append(a.metrics, snapshot)
	if len(a.metrics) > a.maxMetrics {
		a.metrics = a.metrics[len(a.metrics)-a.maxMetrics:]
	}
}

func (a *Analytics) AddLog(message, logType, accountID string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	log := EventLog{
		Timestamp: time.Now().UnixMilli(),
		Message:   message,
		Type:      logType,
		AccountID: accountID,
	}

	a.logs = append(a.logs, log)
	if len(a.logs) > a.maxLogs {
		a.logs = a.logs[len(a.logs)-a.maxLogs:]
	}
}

func (a *Analytics) IncrementRequests() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.requestCount++
}

func (a *Analytics) IncrementFailed() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.failedCount++
}

func (a *Analytics) GetMetrics() []MetricSnapshot {
	a.mu.RLock()
	defer a.mu.RUnlock()

	result := make([]MetricSnapshot, len(a.metrics))
	copy(result, a.metrics)
	return result
}

func (a *Analytics) GetLogs() []EventLog {
	a.mu.RLock()
	defer a.mu.RUnlock()

	result := make([]EventLog, len(a.logs))
	copy(result, a.logs)
	return result
}

func (a *Analytics) AddTokens(used, generated int64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.tokensUsed += used
	a.tokensGenerated += generated
}

func (a *Analytics) GetStats() map[string]any {
	a.mu.RLock()
	defer a.mu.RUnlock()

	return map[string]any{
		"requestCount":    a.requestCount,
		"failedCount":     a.failedCount,
		"tokensUsed":      a.tokensUsed,
		"tokensGenerated": a.tokensGenerated,
	}
}

func (a *Analytics) ClearLogs() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.logs = make([]EventLog, 0, a.maxLogs)
}

func (a *Analytics) LoadStats(tokensUsed, tokensGenerated int64, requestCount, failedCount int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.tokensUsed = tokensUsed
	a.tokensGenerated = tokensGenerated
	a.requestCount = requestCount
	a.failedCount = failedCount
}
