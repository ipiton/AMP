package services

import (
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/ipiton/AMP/internal/core"
	"github.com/stretchr/testify/assert"
)

func TestNewSimpleFilterEngine(t *testing.T) {
	tests := []struct {
		name   string
		logger *slog.Logger
	}{
		{
			name:   "with logger",
			logger: slog.New(slog.NewTextHandler(os.Stdout, nil)),
		},
		{
			name:   "with nil logger",
			logger: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Use NewSimpleFilterEngineWithMetrics with nil metrics for tests
			// to avoid Prometheus registration conflicts
			engine := NewSimpleFilterEngineWithMetrics(tt.logger, nil)
			assert.NotNil(t, engine)
			assert.NotNil(t, engine.logger)
		})
	}
}

func TestSimpleFilterEngine_ShouldBlock_NoiseAlerts(t *testing.T) {
	engine := NewSimpleFilterEngineWithMetrics(nil, nil)

	tests := []struct {
		name           string
		alert          *core.Alert
		classification *core.ClassificationResult
		expectBlock    bool
		expectReason   string
	}{
		{
			name: "block noise alert with classification",
			alert: &core.Alert{
				Fingerprint: "test-fp-1",
				AlertName:   "NormalAlert",
				Status:      core.StatusFiring,
				Labels:      map[string]string{"severity": "info"},
				StartsAt:    time.Now(),
			},
			classification: &core.ClassificationResult{
				Severity:   core.SeverityNoise,
				Confidence: 0.95,
				Reasoning:  "Test noise",
			},
			expectBlock:  true,
			expectReason: "noise",
		},
		{
			name: "allow non-noise alert",
			alert: &core.Alert{
				Fingerprint: "test-fp-2",
				AlertName:   "NormalAlert",
				Status:      core.StatusFiring,
				Labels:      map[string]string{"severity": "warning"},
				StartsAt:    time.Now(),
			},
			classification: &core.ClassificationResult{
				Severity:   core.SeverityWarning,
				Confidence: 0.85,
				Reasoning:  "Valid warning",
			},
			expectBlock:  false,
			expectReason: "",
		},
		{
			name: "allow critical alert",
			alert: &core.Alert{
				Fingerprint: "test-fp-3",
				AlertName:   "CriticalAlert",
				Status:      core.StatusFiring,
				Labels:      map[string]string{"severity": "critical"},
				StartsAt:    time.Now(),
			},
			classification: &core.ClassificationResult{
				Severity:   core.SeverityCritical,
				Confidence: 0.99,
				Reasoning:  "Critical issue",
			},
			expectBlock:  false,
			expectReason: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			blocked, reason := engine.ShouldBlock(tt.alert, tt.classification)
			assert.Equal(t, tt.expectBlock, blocked)
			if tt.expectBlock {
				assert.Equal(t, tt.expectReason, reason)
			}
		})
	}
}

// Test-looking alerts are delivered like any other: there is no rule on alert
// content since PROD-HARDCODED-FILTER (upstream drops none of these).
func TestSimpleFilterEngine_ShouldBlock_TestAlerts(t *testing.T) {
	engine := NewSimpleFilterEngineWithMetrics(nil, nil)

	tests := []struct {
		name         string
		alert        *core.Alert
		expectBlock  bool
		expectReason string
	}{
		{
			name: "allow alert with 'test' in name (lowercase)",
			alert: &core.Alert{
				Fingerprint: "test-fp-1",
				AlertName:   "test-alert",
				Status:      core.StatusFiring,
				Labels:      map[string]string{},
			},
			expectBlock:  false,
			expectReason: "",
		},
		{
			name: "allow alert with 'Test' in name (capitalized)",
			alert: &core.Alert{
				Fingerprint: "test-fp-2",
				AlertName:   "TestAlert",
				Status:      core.StatusFiring,
				Labels:      map[string]string{},
			},
			expectBlock:  false,
			expectReason: "",
		},
		{
			name: "allow alert with 'TEST' in name (uppercase)",
			alert: &core.Alert{
				Fingerprint: "test-fp-3",
				AlertName:   "TEST_ALERT",
				Status:      core.StatusFiring,
				Labels:      map[string]string{},
			},
			expectBlock:  false,
			expectReason: "",
		},
		{
			name: "allow alert with test in alertname label",
			alert: &core.Alert{
				Fingerprint: "test-fp-4",
				AlertName:   "NormalAlert",
				Status:      core.StatusFiring,
				Labels: map[string]string{
					"alertname": "test_alert",
				},
			},
			expectBlock:  false,
			expectReason: "",
		},
		{
			name: "allow alert with test environment",
			alert: &core.Alert{
				Fingerprint: "test-fp-5",
				AlertName:   "NormalAlert",
				Status:      core.StatusFiring,
				Labels: map[string]string{
					"environment": "test",
				},
			},
			expectBlock:  false,
			expectReason: "",
		},
		{
			name: "allow alert with testing environment",
			alert: &core.Alert{
				Fingerprint: "test-fp-6",
				AlertName:   "NormalAlert",
				Status:      core.StatusFiring,
				Labels: map[string]string{
					"environment": "testing",
				},
			},
			expectBlock:  false,
			expectReason: "",
		},
		{
			name: "allow normal production alert",
			alert: &core.Alert{
				Fingerprint: "test-fp-7",
				AlertName:   "ProductionAlert",
				Status:      core.StatusFiring,
				Labels: map[string]string{
					"environment": "production",
				},
			},
			expectBlock:  false,
			expectReason: "",
		},
		{
			name: "allow alert with 'latest' (not test)",
			alert: &core.Alert{
				Fingerprint: "test-fp-8",
				AlertName:   "LatestAlert",
				Status:      core.StatusFiring,
				Labels:      map[string]string{},
			},
			expectBlock:  false,
			expectReason: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			blocked, reason := engine.ShouldBlock(tt.alert, nil)
			assert.Equal(t, tt.expectBlock, blocked, "Block status mismatch for: %s", tt.name)
			if tt.expectBlock {
				assert.Equal(t, tt.expectReason, reason, "Reason mismatch for: %s", tt.name)
			}
		})
	}
}

func TestSimpleFilterEngine_ShouldBlock_LowConfidence(t *testing.T) {
	engine := NewSimpleFilterEngineWithMetrics(nil, nil)

	tests := []struct {
		name           string
		alert          *core.Alert
		classification *core.ClassificationResult
		expectBlock    bool
		expectReason   string
	}{
		{
			name: "block alert with confidence < 0.3",
			alert: &core.Alert{
				Fingerprint: "test-fp-1",
				AlertName:   "UncertainAlert",
				Status:      core.StatusFiring,
				Labels:      map[string]string{},
				StartsAt:    time.Now(),
			},
			classification: &core.ClassificationResult{
				Severity:   core.SeverityWarning,
				Confidence: 0.25,
				Reasoning:  "Low confidence",
			},
			expectBlock:  true,
			expectReason: "low_confidence",
		},
		{
			name: "block alert with confidence = 0.29",
			alert: &core.Alert{
				Fingerprint: "test-fp-2",
				AlertName:   "UncertainAlert",
				Status:      core.StatusFiring,
				Labels:      map[string]string{},
			},
			classification: &core.ClassificationResult{
				Severity:   core.SeverityWarning,
				Confidence: 0.29,
				Reasoning:  "Low confidence",
			},
			expectBlock:  true,
			expectReason: "low_confidence",
		},
		{
			name: "allow alert with confidence = 0.3 (boundary)",
			alert: &core.Alert{
				Fingerprint: "test-fp-3",
				AlertName:   "BoundaryAlert",
				Status:      core.StatusFiring,
				Labels:      map[string]string{},
			},
			classification: &core.ClassificationResult{
				Severity:   core.SeverityWarning,
				Confidence: 0.3,
				Reasoning:  "Acceptable confidence",
			},
			expectBlock:  false,
			expectReason: "",
		},
		{
			name: "allow alert with high confidence",
			alert: &core.Alert{
				Fingerprint: "test-fp-4",
				AlertName:   "ConfidentAlert",
				Status:      core.StatusFiring,
				Labels:      map[string]string{},
			},
			classification: &core.ClassificationResult{
				Severity:   core.SeverityWarning,
				Confidence: 0.95,
				Reasoning:  "High confidence",
			},
			expectBlock:  false,
			expectReason: "",
		},
		{
			name: "allow alert without classification",
			alert: &core.Alert{
				Fingerprint: "test-fp-5",
				AlertName:   "NoClassificationAlert",
				Status:      core.StatusFiring,
				Labels:      map[string]string{},
			},
			classification: nil,
			expectBlock:    false,
			expectReason:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			blocked, reason := engine.ShouldBlock(tt.alert, tt.classification)
			assert.Equal(t, tt.expectBlock, blocked)
			if tt.expectBlock {
				assert.Equal(t, tt.expectReason, reason)
			}
		})
	}
}

func TestSimpleFilterEngine_ShouldBlock_CombinedRules(t *testing.T) {
	engine := NewSimpleFilterEngineWithMetrics(nil, nil)

	tests := []struct {
		name           string
		alert          *core.Alert
		classification *core.ClassificationResult
		expectBlock    bool
		expectReason   string
		description    string
	}{
		{
			name: "test-named alert with good classification allowed",
			alert: &core.Alert{
				Fingerprint: "test-fp-1",
				AlertName:   "test-alert",
				Status:      core.StatusFiring,
				Labels:      map[string]string{},
			},
			classification: &core.ClassificationResult{
				Severity:   core.SeverityCritical,
				Confidence: 0.99,
				Reasoning:  "Critical issue",
			},
			expectBlock:  false,
			expectReason: "",
			description:  "Test-named alerts are not blocked by name",
		},
		{
			name: "production alert with noise classification blocked",
			alert: &core.Alert{
				Fingerprint: "test-fp-2",
				AlertName:   "ProductionAlert",
				Status:      core.StatusFiring,
				Labels: map[string]string{
					"environment": "production",
				},
			},
			classification: &core.ClassificationResult{
				Severity:   core.SeverityNoise,
				Confidence: 0.95,
				Reasoning:  "Classified as noise",
			},
			expectBlock:  true,
			expectReason: "noise",
			description:  "Even production alerts are blocked if classified as noise",
		},
		{
			name: "production alert with good classification allowed",
			alert: &core.Alert{
				Fingerprint: "test-fp-3",
				AlertName:   "ProductionAlert",
				Status:      core.StatusFiring,
				Labels: map[string]string{
					"environment": "production",
					"severity":    "critical",
				},
			},
			classification: &core.ClassificationResult{
				Severity:   core.SeverityCritical,
				Confidence: 0.95,
				Reasoning:  "Valid critical alert",
			},
			expectBlock:  false,
			expectReason: "",
			description:  "Good production alerts are allowed",
		},
		{
			name: "production alert with low confidence blocked",
			alert: &core.Alert{
				Fingerprint: "test-fp-4",
				AlertName:   "ProductionAlert",
				Status:      core.StatusFiring,
				Labels: map[string]string{
					"environment": "production",
				},
			},
			classification: &core.ClassificationResult{
				Severity:   core.SeverityWarning,
				Confidence: 0.15,
				Reasoning:  "Uncertain classification",
			},
			expectBlock:  true,
			expectReason: "low_confidence",
			description:  "Low confidence alerts are blocked even in production",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			blocked, reason := engine.ShouldBlock(tt.alert, tt.classification)
			assert.Equal(t, tt.expectBlock, blocked, tt.description)
			if tt.expectBlock {
				assert.Equal(t, tt.expectReason, reason, tt.description)
			}
		})
	}
}

func TestSimpleFilterEngine_ShouldBlock_AdditionalRules(t *testing.T) {
	engine := NewSimpleFilterEngineWithMetrics(nil, nil)

	tests := []struct {
		name           string
		alert          *core.Alert
		classification *core.ClassificationResult
		expectBlock    bool
		expectReason   string
		description    string
	}{
		{
			name: "allow namespace dev-sandbox",
			alert: &core.Alert{
				Fingerprint: "test-fp-1",
				AlertName:   "ProductionAlert",
				Status:      core.StatusFiring,
				Labels: map[string]string{
					"namespace": "dev-sandbox",
				},
				StartsAt: time.Now(),
			},
			classification: nil,
			expectBlock:    false,
			expectReason:   "",
			description:    "dev-sandbox namespace should be allowed",
		},
		{
			name: "allow namespace tmp",
			alert: &core.Alert{
				Fingerprint: "test-fp-2",
				AlertName:   "TempAlert",
				Status:      core.StatusFiring,
				Labels: map[string]string{
					"namespace": "tmp",
				},
				StartsAt: time.Now(),
			},
			classification: nil,
			expectBlock:    false,
			expectReason:   "",
			description:    "tmp namespace should be allowed",
		},
		{
			name: "allow kube-system namespace",
			alert: &core.Alert{
				Fingerprint: "test-fp-3",
				AlertName:   "SystemAlert",
				Status:      core.StatusFiring,
				Labels: map[string]string{
					"namespace": "kube-system",
				},
				StartsAt: time.Now(),
			},
			classification: nil,
			expectBlock:    false,
			expectReason:   "",
			description:    "kube-system namespace should be allowed",
		},
		{
			name: "allow production namespace",
			alert: &core.Alert{
				Fingerprint: "test-fp-4",
				AlertName:   "ProdAlert",
				Status:      core.StatusFiring,
				Labels: map[string]string{
					"namespace": "production",
				},
				StartsAt: time.Now(),
			},
			classification: nil,
			expectBlock:    false,
			expectReason:   "",
			description:    "production namespace should be allowed",
		},
		{
			name: "block empty alert name",
			alert: &core.Alert{
				Fingerprint: "test-fp-5",
				AlertName:   "",
				Status:      core.StatusFiring,
				Labels: map[string]string{
					"namespace": "production",
				},
				StartsAt: time.Now(),
			},
			classification: nil,
			expectBlock:    true,
			expectReason:   "empty_alert_name",
			description:    "empty alert name should be blocked",
		},
		{
			name: "block old resolved alert (25 hours)",
			alert: &core.Alert{
				Fingerprint: "test-fp-6",
				AlertName:   "OldAlert",
				Status:      core.StatusResolved,
				Labels:      map[string]string{},
				StartsAt:    time.Now().Add(-26 * time.Hour),
				EndsAt:      ptrTime(time.Now().Add(-25 * time.Hour)),
			},
			classification: nil,
			expectBlock:    true,
			expectReason:   "old_resolved",
			description:    "resolved alert older than 24h should be blocked",
		},
		{
			name: "allow recent resolved alert (23 hours)",
			alert: &core.Alert{
				Fingerprint: "test-fp-7",
				AlertName:   "RecentAlert",
				Status:      core.StatusResolved,
				Labels:      map[string]string{},
				StartsAt:    time.Now().Add(-24 * time.Hour),
				EndsAt:      ptrTime(time.Now().Add(-23 * time.Hour)),
			},
			classification: nil,
			expectBlock:    false,
			expectReason:   "",
			description:    "resolved alert younger than 24h should be allowed",
		},
		{
			name: "allow firing alert regardless of age",
			alert: &core.Alert{
				Fingerprint: "test-fp-8",
				AlertName:   "FiringAlert",
				Status:      core.StatusFiring,
				Labels:      map[string]string{},
				StartsAt:    time.Now().Add(-48 * time.Hour),
			},
			classification: nil,
			expectBlock:    false,
			expectReason:   "",
			description:    "firing alert should not be blocked by age",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			blocked, reason := engine.ShouldBlock(tt.alert, tt.classification)
			assert.Equal(t, tt.expectBlock, blocked, tt.description)
			if tt.expectBlock {
				assert.Equal(t, tt.expectReason, reason, tt.description)
			}
		})
	}
}

// Helper functions
func ptrTime(t time.Time) *time.Time {
	return &t
}

// Benchmark tests
func BenchmarkSimpleFilterEngine_ShouldBlock(b *testing.B) {
	engine := NewSimpleFilterEngineWithMetrics(nil, nil)
	alert := &core.Alert{
		Fingerprint: "bench-fp",
		AlertName:   "BenchmarkAlert",
		Status:      core.StatusFiring,
		Labels: map[string]string{
			"severity":    "warning",
			"environment": "production",
		},
		StartsAt: time.Now(),
	}
	classification := &core.ClassificationResult{
		Severity:   core.SeverityWarning,
		Confidence: 0.85,
		Reasoning:  "Test reasoning",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = engine.ShouldBlock(alert, classification)
	}
}
