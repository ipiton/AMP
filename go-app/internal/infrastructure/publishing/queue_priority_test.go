package publishing

import (
	"testing"

	"github.com/ipiton/AMP/internal/core"
)

// TestDeterminePriority_CriticalFiring tests HIGH priority for critical firing alerts
func TestDeterminePriority_CriticalFiring(t *testing.T) {
	enrichedAlert := &core.EnrichedAlert{
		Alert: &core.Alert{
			Labels: map[string]string{
				"severity": "critical",
			},
			Status: core.StatusFiring,
		},
	}

	priority := determinePriority(enrichedAlert)

	if priority != PriorityHigh {
		t.Errorf("Expected PriorityHigh for critical firing alert, got %v", priority)
	}
}

// TestDeterminePriority_CriticalResolved tests LOW priority for critical resolved alerts
func TestDeterminePriority_CriticalResolved(t *testing.T) {
	enrichedAlert := &core.EnrichedAlert{
		Alert: &core.Alert{
			Labels: map[string]string{
				"severity": "critical",
			},
			Status: core.StatusResolved,
		},
	}

	priority := determinePriority(enrichedAlert)

	if priority != PriorityLow {
		t.Errorf("Expected PriorityLow for resolved alert, got %v", priority)
	}
}

// TestDeterminePriority_LLMCritical tests HIGH priority for LLM classification = critical
func TestDeterminePriority_LLMCritical(t *testing.T) {
	enrichedAlert := &core.EnrichedAlert{
		Alert: &core.Alert{
			Labels: map[string]string{
				"severity": "warning",
			},
			Status: core.StatusFiring,
		},
		Classification: &core.ClassificationResult{
			Severity: core.SeverityCritical,
		},
	}

	priority := determinePriority(enrichedAlert)

	if priority != PriorityHigh {
		t.Errorf("Expected PriorityHigh for LLM critical classification, got %v", priority)
	}
}

// TestDeterminePriority_ResolvedAlert tests LOW priority for resolved alerts
func TestDeterminePriority_ResolvedAlert(t *testing.T) {
	enrichedAlert := &core.EnrichedAlert{
		Alert: &core.Alert{
			Labels: map[string]string{
				"severity": "warning",
			},
			Status: core.StatusResolved,
		},
	}

	priority := determinePriority(enrichedAlert)

	if priority != PriorityLow {
		t.Errorf("Expected PriorityLow for resolved alert, got %v", priority)
	}
}

// TestDeterminePriority_InfoSeverity tests LOW priority for info severity
func TestDeterminePriority_InfoSeverity(t *testing.T) {
	enrichedAlert := &core.EnrichedAlert{
		Alert: &core.Alert{
			Labels: map[string]string{
				"severity": "info",
			},
			Status: core.StatusFiring,
		},
	}

	priority := determinePriority(enrichedAlert)

	if priority != PriorityLow {
		t.Errorf("Expected PriorityLow for info severity, got %v", priority)
	}
}

// TestDeterminePriority_WarningFiring tests MEDIUM priority for warning firing alerts
func TestDeterminePriority_WarningFiring(t *testing.T) {
	enrichedAlert := &core.EnrichedAlert{
		Alert: &core.Alert{
			Labels: map[string]string{
				"severity": "warning",
			},
			Status: core.StatusFiring,
		},
	}

	priority := determinePriority(enrichedAlert)

	if priority != PriorityMedium {
		t.Errorf("Expected PriorityMedium for warning firing alert, got %v", priority)
	}
}

// TestDeterminePriority_NilEnrichedAlert tests default MEDIUM for nil input
func TestDeterminePriority_NilEnrichedAlert(t *testing.T) {
	priority := determinePriority(nil)

	if priority != PriorityMedium {
		t.Errorf("Expected PriorityMedium for nil enrichedAlert, got %v", priority)
	}
}

// TestDeterminePriority_NilAlert tests default MEDIUM for nil alert
func TestDeterminePriority_NilAlert(t *testing.T) {
	enrichedAlert := &core.EnrichedAlert{
		Alert: nil,
	}

	priority := determinePriority(enrichedAlert)

	if priority != PriorityMedium {
		t.Errorf("Expected PriorityMedium for nil alert, got %v", priority)
	}
}

// TestDeterminePriority_NilClassification tests priority without LLM classification
func TestDeterminePriority_NilClassification(t *testing.T) {
	enrichedAlert := &core.EnrichedAlert{
		Alert: &core.Alert{
			Labels: map[string]string{
				"severity": "warning",
			},
			Status: core.StatusFiring,
		},
		Classification: nil,
	}

	priority := determinePriority(enrichedAlert)

	if priority != PriorityMedium {
		t.Errorf("Expected PriorityMedium for warning without classification, got %v", priority)
	}
}

// TestDeterminePriority_UnknownSeverity tests MEDIUM priority for unknown severity
func TestDeterminePriority_UnknownSeverity(t *testing.T) {
	enrichedAlert := &core.EnrichedAlert{
		Alert: &core.Alert{
			Labels: map[string]string{
				"severity": "unknown",
			},
			Status: core.StatusFiring,
		},
	}

	priority := determinePriority(enrichedAlert)

	if priority != PriorityMedium {
		t.Errorf("Expected PriorityMedium for unknown severity, got %v", priority)
	}
}

// TestPriorityString tests Priority.String() method
func TestPriorityString(t *testing.T) {
	tests := []struct {
		priority Priority
		expected string
	}{
		{PriorityHigh, "high"},
		{PriorityMedium, "medium"},
		{PriorityLow, "low"},
		{Priority(99), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			result := tt.priority.String()
			if result != tt.expected {
				t.Errorf("Priority.String() = %v, want %v", result, tt.expected)
			}
		})
	}
}

// TestJobStateString tests JobState.String() method
func TestJobStateString(t *testing.T) {
	tests := []struct {
		state    JobState
		expected string
	}{
		{JobStateQueued, "queued"},
		{JobStateProcessing, "processing"},
		{JobStateRetrying, "retrying"},
		{JobStateSucceeded, "succeeded"},
		{JobStateFailed, "failed"},
		{JobStateDLQ, "dlq"},
		{JobState(99), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			result := tt.state.String()
			if result != tt.expected {
				t.Errorf("JobState.String() = %v, want %v", result, tt.expected)
			}
		})
	}
}

// TestQueueErrorTypeString tests QueueErrorType.String() method
func TestQueueErrorTypeString(t *testing.T) {
	tests := []struct {
		errorType QueueErrorType
		expected  string
	}{
		{QueueErrorTypeTransient, "transient"},
		{QueueErrorTypePermanent, "permanent"},
		{QueueErrorTypeUnknown, "unknown"},
		{QueueErrorType(99), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			result := tt.errorType.String()
			if result != tt.expected {
				t.Errorf("QueueErrorType.String() = %v, want %v", result, tt.expected)
			}
		})
	}
}
