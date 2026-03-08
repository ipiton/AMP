package publishing

import (
	"context"
	"fmt"

	infrapublishing "github.com/ipiton/AMP/internal/infrastructure/publishing"
)

// ModeMetricsCollector collects current publishing mode state.
//
// This collector bridges infrastructure ModeManager into the publishing metrics
// aggregator used by dashboard and stats handlers.
type ModeMetricsCollector struct {
	manager infrapublishing.ModeManager
}

// NewModeMetricsCollector creates a collector for publishing mode metrics.
func NewModeMetricsCollector(manager infrapublishing.ModeManager) *ModeMetricsCollector {
	return &ModeMetricsCollector{manager: manager}
}

// Collect returns current publishing mode metrics.
func (c *ModeMetricsCollector) Collect(ctx context.Context) (map[string]float64, error) {
	if c.manager == nil {
		return nil, fmt.Errorf("mode manager not initialized")
	}

	_ = ctx

	modeMetrics := c.manager.GetModeMetrics()
	currentMode := c.manager.GetCurrentMode()
	currentValue := 0.0
	if currentMode == infrapublishing.ModeMetricsOnly {
		currentValue = 1.0
	}

	metrics := make(map[string]float64, 4)
	metrics["publishing_mode_current"] = currentValue
	metrics[fmt.Sprintf("publishing_mode{mode=%q}", currentMode.String())] = 1.0
	metrics["publishing_mode_transition_count"] = float64(modeMetrics.TransitionCount)
	metrics["publishing_mode_duration_seconds"] = modeMetrics.CurrentModeDuration.Seconds()

	return metrics, nil
}

// Name returns collector name.
func (c *ModeMetricsCollector) Name() string {
	return "mode"
}

// IsAvailable returns true when mode manager is initialized.
func (c *ModeMetricsCollector) IsAvailable() bool {
	return c.manager != nil
}
