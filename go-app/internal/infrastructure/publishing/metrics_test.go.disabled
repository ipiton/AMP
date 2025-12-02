package publishing

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Note: Metrics tests are minimal because Prometheus metrics are global
// and can only be registered once. Full testing requires custom registries
// which is beyond the scope of unit tests. These tests verify basic
// functionality without panics.

func TestNewPublishingMetrics(t *testing.T) {
	// Only test once to avoid duplicate registration
	if testing.Short() {
		t.Skip("Skipping metrics test in short mode")
	}

	metrics := NewPublishingMetrics()

	assert.NotNil(t, metrics)
	assert.NotNil(t, metrics.AlertsPublished)
	assert.NotNil(t, metrics.AlertsPublishErrors)
	assert.NotNil(t, metrics.QueueSize)
	assert.NotNil(t, metrics.CircuitBreakerState)
	assert.NotNil(t, metrics.DiscoveredTargets)
}
