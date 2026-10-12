package analytics

import "testing"

func TestCountersAndTokens(t *testing.T) {
	a := New()
	a.IncrementRequests()
	a.IncrementRequests()
	a.IncrementFailed()
	a.AddTokens(100, 40)
	a.AddTokens(5, 2)

	stats := a.GetStats()
	if stats["requestCount"] != 2 {
		t.Errorf("requestCount = %v, want 2", stats["requestCount"])
	}
	if stats["failedCount"] != 1 {
		t.Errorf("failedCount = %v, want 1", stats["failedCount"])
	}
	if stats["tokensUsed"] != int64(105) {
		t.Errorf("tokensUsed = %v, want 105", stats["tokensUsed"])
	}
	if stats["tokensGenerated"] != int64(42) {
		t.Errorf("tokensGenerated = %v, want 42", stats["tokensGenerated"])
	}
}

func TestLoadStats(t *testing.T) {
	a := New()
	a.IncrementRequests()
	a.LoadStats(10, 20, 30, 40)
	stats := a.GetStats()
	if stats["tokensUsed"] != int64(10) || stats["tokensGenerated"] != int64(20) {
		t.Errorf("tokens = %v/%v, want 10/20", stats["tokensUsed"], stats["tokensGenerated"])
	}
	if stats["requestCount"] != 30 || stats["failedCount"] != 40 {
		t.Errorf("counts = %v/%v, want 30/40", stats["requestCount"], stats["failedCount"])
	}
}

func TestRecordMetricSnapshot(t *testing.T) {
	a := New()
	a.IncrementRequests()
	a.AddTokens(7, 3)
	a.RecordMetric(2, 1, 0, 55, 3)

	m := a.GetMetrics()
	if len(m) != 1 {
		t.Fatalf("metrics = %d, want 1", len(m))
	}
	got := m[0]
	if got.ActiveCount != 2 || got.ExhaustedCount != 1 || got.SuspendedCount != 0 ||
		got.TotalRemaining != 55 || got.PoolSize != 3 {
		t.Errorf("snapshot = %+v, want active=2 exhausted=1 suspended=0 remaining=55 pool=3", got)
	}
	if got.RequestCount != 1 || got.TokensUsed != 7 || got.TokensGenerated != 3 {
		t.Errorf("snapshot counters = %+v, want requests=1 used=7 generated=3", got)
	}
	if got.Timestamp == 0 {
		t.Error("timestamp not set")
	}
}

func TestMetricsRingBufferCap(t *testing.T) {
	a := New()
	for i := 0; i < a.maxMetrics+10; i++ {
		a.RecordMetric(i, 0, 0, 0, 0)
	}
	m := a.GetMetrics()
	if len(m) != a.maxMetrics {
		t.Fatalf("metrics = %d, want %d", len(m), a.maxMetrics)
	}
	if m[0].ActiveCount != 10 {
		t.Errorf("oldest kept = %d, want 10 (first 10 evicted)", m[0].ActiveCount)
	}
}

func TestLogsRingBufferAndClear(t *testing.T) {
	a := New()
	for i := 0; i < a.maxLogs+5; i++ {
		a.AddLog("m", "info", "acc")
	}
	logs := a.GetLogs()
	if len(logs) != a.maxLogs {
		t.Fatalf("logs = %d, want %d", len(logs), a.maxLogs)
	}
	if logs[0].Type != "info" || logs[0].AccountID != "acc" {
		t.Errorf("log entry = %+v, want type=info accountId=acc", logs[0])
	}
	a.ClearLogs()
	if got := a.GetLogs(); len(got) != 0 {
		t.Errorf("logs after clear = %d, want 0", len(got))
	}
}

func TestGetMetricsAndLogsReturnCopies(t *testing.T) {
	a := New()
	a.RecordMetric(1, 0, 0, 0, 0)
	a.AddLog("first", "info", "")

	m := a.GetMetrics()
	m[0].ActiveCount = 999
	if a.GetMetrics()[0].ActiveCount != 1 {
		t.Error("GetMetrics aliases internal slice")
	}

	l := a.GetLogs()
	l[0].Message = "mutated"
	if a.GetLogs()[0].Message != "first" {
		t.Error("GetLogs aliases internal slice")
	}
}
