package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	infrapublishing "github.com/ipiton/AMP/internal/infrastructure/publishing"
)

// === Task rec fix round 2 (review findings R4/R9): the delivery-confirmation
// knob's bounds ===
//
// The knob is not a plain timeout: the notify chain holds each group's publish
// lock and its cross-replica claim for the whole wait, and grouping derives the
// timer-callback deadline and the orphan-adoption grace from it. Both bounds are
// therefore load-bearing rather than cosmetic.

func publishingTestConfig(deliveryConfirmationTimeout time.Duration) *Config {
	cfg := &Config{}
	cfg.Publishing.Enabled = true
	cfg.Publishing.Queue.MaxConcurrent = 5
	cfg.Publishing.Queue.WorkerCount = 10
	cfg.Publishing.Queue.HighPriorityQueueSize = 500
	cfg.Publishing.Queue.MediumPriorityQueueSize = 1000
	cfg.Publishing.Queue.LowPriorityQueueSize = 500
	cfg.Publishing.Queue.MaxRetries = 3
	cfg.Publishing.Queue.RetryInterval = 2 * time.Second
	cfg.Publishing.Queue.StopTimeout = 10 * time.Second
	cfg.Publishing.Queue.JobTrackingCapacity = 10000
	cfg.Publishing.Queue.DeliveryConfirmationTimeout = deliveryConfirmationTimeout
	return cfg
}

func TestValidatePublishing_DeliveryConfirmationTimeoutMustBePositive(t *testing.T) {
	err := publishingTestConfig(0).validatePublishing()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "delivery_confirmation_timeout must be positive")
}

func TestValidatePublishing_DeliveryConfirmationTimeoutHasACeiling(t *testing.T) {
	err := publishingTestConfig(infrapublishing.MaxDeliveryConfirmationTimeout + time.Second).validatePublishing()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds the supported maximum")

	// The ceiling itself is allowed.
	assert.NoError(t, publishingTestConfig(infrapublishing.MaxDeliveryConfirmationTimeout).validatePublishing())
}

func TestValidatePublishing_DefaultDeliveryConfirmationTimeoutIsAccepted(t *testing.T) {
	assert.NoError(t, publishingTestConfig(infrapublishing.DefaultDeliveryConfirmationTimeout).validatePublishing())
}

// TestSetDefaults_DeliveryConfirmationTimeoutMatchesThePublishingDefault keeps
// the viper default and the Go default from drifting: the grouping-side budgets
// are derived from whichever one actually reaches the coordinator.
func TestSetDefaults_DeliveryConfirmationTimeoutMatchesThePublishingDefault(t *testing.T) {
	cfg, err := LoadConfigFromEnv()
	require.NoError(t, err)
	assert.Equal(t, infrapublishing.DefaultDeliveryConfirmationTimeout, cfg.Publishing.Queue.DeliveryConfirmationTimeout)
}
