package publishing

import (
	"testing"
	"time"
)

// ============================================================================
// TrendDetector Tests
// ============================================================================

// TestTrendDetector_EmptyData tests behavior with no historical data.
func TestTrendDetector_EmptyData(t *testing.T) {
	detector := NewTrendDetector()

	// Analyze with no data
	trends := detector.Analyze()

	// Should return neutral trends
	if trends.SuccessRateTrend != "stable" {
		t.Errorf("Expected success_rate_trend 'stable', got '%s'", trends.SuccessRateTrend)
	}

	if trends.LatencyTrend != "stable" {
		t.Errorf("Expected latency_trend 'stable', got '%s'", trends.LatencyTrend)
	}

	if trends.QueueGrowthTrend != "stable" {
		t.Errorf("Expected queue_growth_trend 'stable', got '%s'", trends.QueueGrowthTrend)
	}

	if trends.ErrorSpikeDetected {
		t.Error("Expected no error spike with empty data")
	}
}

// TestTrendDetector_SuccessRateTrend tests success rate trend classification.
func TestTrendDetector_SuccessRateTrend(t *testing.T) {
	detector := NewTrendDetector()
	now := time.Now()

	// Baseline: 23h of 85% success rate (1380 snapshots, leaving room for recent)
	for i := 0; i < 1380; i++ { // 23 hours of baseline
		snapshot := &MetricsSnapshot{
			Timestamp: now.Add(time.Duration(i-1440) * time.Minute),
			Metrics: map[string]float64{
				"jobs_submitted_total": 100.0,
				"jobs_completed_total": 85.0, // 85% success
			},
		}
		detector.RecordSnapshot(snapshot)
	}

	// Recent: last 1h with 95% success rate (increasing trend: 85% → 95% = +10%)
	for i := 0; i < 60; i++ {
		snapshot := &MetricsSnapshot{
			Timestamp: now.Add(time.Duration(i-60) * time.Minute),
			Metrics: map[string]float64{
				"jobs_submitted_total": 100.0,
				"jobs_completed_total": 95.0, // 95% success
			},
		}
		detector.RecordSnapshot(snapshot)
	}

	// Analyze
	trends := detector.Analyze()

	// Should detect increasing trend (+5% > 5% threshold)
	if trends.SuccessRateTrend != "increasing" {
		t.Errorf("Expected success_rate_trend 'increasing', got '%s'", trends.SuccessRateTrend)
	}

	if trends.SuccessRateChange <= 0 {
		t.Errorf("Expected positive success_rate_change, got %f", trends.SuccessRateChange)
	}
}

// TestTrendDetector_LatencyTrend tests latency trend classification.
func TestTrendDetector_LatencyTrend(t *testing.T) {
	detector := NewTrendDetector()
	now := time.Now()

	// Baseline: 24h of 100ms latency
	for i := 0; i < 1440; i++ {
		snapshot := &MetricsSnapshot{
			Timestamp: now.Add(time.Duration(i-1440) * time.Minute),
			Metrics: map[string]float64{
				"publishing_duration_seconds_sum":   10.0,  // 10 seconds total
				"publishing_duration_seconds_count": 100.0, // 100 requests
				// Average: 10/100 = 0.1s = 100ms
			},
		}
		detector.RecordSnapshot(snapshot)
	}

	// Recent: last 1h with 80ms latency (improving trend)
	for i := 0; i < 60; i++ {
		snapshot := &MetricsSnapshot{
			Timestamp: now.Add(time.Duration(i-60) * time.Minute),
			Metrics: map[string]float64{
				"publishing_duration_seconds_sum":   8.0,   // 8 seconds total
				"publishing_duration_seconds_count": 100.0, // 100 requests
				// Average: 8/100 = 0.08s = 80ms
			},
		}
		detector.RecordSnapshot(snapshot)
	}

	// Analyze
	trends := detector.Analyze()

	// Should detect improving trend (-20ms < -5% threshold)
	if trends.LatencyTrend != "improving" {
		t.Errorf("Expected latency_trend 'improving', got '%s'", trends.LatencyTrend)
	}

	if trends.LatencyChange >= 0 {
		t.Errorf("Expected negative latency_change (improvement), got %f", trends.LatencyChange)
	}
}

// TestTrendDetector_ErrorSpike tests error spike detection (>3σ).
func TestTrendDetector_ErrorSpike(t *testing.T) {
	detector := NewTrendDetector()
	now := time.Now()

	// Baseline: 23h of 1% error rate (very stable, 1380 snapshots)
	for i := 0; i < 1380; i++ {
		snapshot := &MetricsSnapshot{
			Timestamp: now.Add(time.Duration(i-1440) * time.Minute),
			Metrics: map[string]float64{
				"jobs_submitted_total": 100.0,
				"jobs_failed_total":    1.0, // 1% error rate
			},
		}
		detector.RecordSnapshot(snapshot)
	}

	// Recent: sudden spike to 10% error rate
	for i := 0; i < 60; i++ {
		snapshot := &MetricsSnapshot{
			Timestamp: now.Add(time.Duration(i-60) * time.Minute),
			Metrics: map[string]float64{
				"jobs_submitted_total": 100.0,
				"jobs_failed_total":    10.0, // 10% error rate (spike!)
			},
		}
		detector.RecordSnapshot(snapshot)
	}

	// Analyze
	trends := detector.Analyze()

	// Should detect error spike (10% >> 1% baseline, >3σ deviation)
	if !trends.ErrorSpikeDetected {
		t.Error("Expected error spike detected")
	}

	if trends.ErrorRateCurrent <= trends.ErrorRateBaseline {
		t.Errorf("Expected current error rate (%f) > baseline (%f)",
			trends.ErrorRateCurrent, trends.ErrorRateBaseline)
	}
}

// TestTrendDetector_QueueGrowth tests queue growth rate calculation.
func TestTrendDetector_QueueGrowth(t *testing.T) {
	detector := NewTrendDetector()
	now := time.Now()

	// Baseline: 24h of stable queue (size 100)
	for i := 0; i < 1440; i++ {
		snapshot := &MetricsSnapshot{
			Timestamp: now.Add(time.Duration(i-1440) * time.Minute),
			Metrics: map[string]float64{
				"queue_size_total": 100.0, // Stable at 100
			},
		}
		detector.RecordSnapshot(snapshot)
	}

	// Recent: last 1h with growing queue (100 → 200)
	for i := 0; i < 60; i++ {
		queueSize := 100.0 + float64(i)*1.67 // Grows from 100 to ~200 over 1h
		snapshot := &MetricsSnapshot{
			Timestamp: now.Add(time.Duration(i-60) * time.Minute),
			Metrics: map[string]float64{
				"queue_size_total": queueSize,
			},
		}
		detector.RecordSnapshot(snapshot)
	}

	// Analyze
	trends := detector.Analyze()

	// Should detect growing queue
	if trends.QueueGrowthTrend != "growing" {
		t.Errorf("Expected queue_growth_trend 'growing', got '%s'", trends.QueueGrowthTrend)
	}

	if trends.QueueGrowthRate <= 0 {
		t.Errorf("Expected positive queue_growth_rate, got %f", trends.QueueGrowthRate)
	}
}

// TestTrendDetector_StableTrends tests stable trend classification.
func TestTrendDetector_StableTrends(t *testing.T) {
	detector := NewTrendDetector()
	now := time.Now()

	// 24h of stable metrics (no significant changes)
	for i := 0; i < 1440; i++ {
		snapshot := &MetricsSnapshot{
			Timestamp: now.Add(time.Duration(i-1440) * time.Minute),
			Metrics: map[string]float64{
				"jobs_submitted_total":                 100.0,
				"jobs_completed_total":                 95.0, // 95% success (stable)
				"publishing_duration_seconds_sum":      10.0,
				"publishing_duration_seconds_count":    100.0, // 100ms latency (stable)
				"jobs_failed_total":                    5.0,   // 5% error rate (stable)
				"queue_size_total":                     100.0, // Queue size 100 (stable)
			},
		}
		detector.RecordSnapshot(snapshot)
	}

	// Analyze
	trends := detector.Analyze()

	// All trends should be stable (changes < 5% threshold)
	if trends.SuccessRateTrend != "stable" {
		t.Errorf("Expected success_rate_trend 'stable', got '%s'", trends.SuccessRateTrend)
	}

	if trends.LatencyTrend != "stable" {
		t.Errorf("Expected latency_trend 'stable', got '%s'", trends.LatencyTrend)
	}

	if trends.QueueGrowthTrend != "stable" {
		t.Errorf("Expected queue_growth_trend 'stable', got '%s'", trends.QueueGrowthTrend)
	}

	// No error spike (5% is within normal range)
	if trends.ErrorSpikeDetected {
		t.Error("Expected no error spike with stable metrics")
	}
}

// TestTrendDetector_RecordSnapshot tests snapshot recording.
func TestTrendDetector_RecordSnapshot(t *testing.T) {
	detector := NewTrendDetector()

	// Record a few snapshots
	for i := 0; i < 10; i++ {
		snapshot := &MetricsSnapshot{
			Timestamp: time.Now().Add(time.Duration(i) * time.Minute),
			Metrics: map[string]float64{
				"index": float64(i),
			},
		}
		detector.RecordSnapshot(snapshot)
	}

	// Verify they are stored (check via Analyze, which accesses history)
	trends := detector.Analyze()

	// With recent data, trends should not be empty
	if trends.Timestamp.IsZero() {
		t.Error("Expected non-zero timestamp after recording snapshots")
	}
}

// TestMetricExtractors tests metric extraction functions.
func TestMetricExtractors(t *testing.T) {
	metrics := map[string]float64{
		"jobs_submitted_total":                 100.0,
		"jobs_completed_total":                 90.0,
		"jobs_failed_total":                    10.0,
		"publishing_duration_seconds_sum":      10.0,
		"publishing_duration_seconds_count":    100.0,
		"queue_size_total":                     50.0,
	}

	// Test extractSuccessRate
	successRate := extractSuccessRate(metrics)
	expected := 90.0
	if successRate != expected {
		t.Errorf("Expected success rate %.1f%%, got %.1f%%", expected, successRate)
	}

	// Test extractErrorRate
	errorRate := extractErrorRate(metrics)
	expected = 10.0
	if errorRate != expected {
		t.Errorf("Expected error rate %.1f%%, got %.1f%%", expected, errorRate)
	}

	// Test extractAvgLatency
	avgLatency := extractAvgLatency(metrics)
	expected = 100.0 // (10 / 100) * 1000 = 100ms
	if avgLatency != expected {
		t.Errorf("Expected avg latency %.1fms, got %.1fms", expected, avgLatency)
	}

	// Test extractQueueSize
	queueSize := extractQueueSize(metrics)
	expected = 50.0
	if queueSize != expected {
		t.Errorf("Expected queue size %.1f, got %.1f", expected, queueSize)
	}
}
