package services

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/ipiton/AMP/internal/core"
)

// Mock implementations

type mockLLMClient struct {
	classifyFunc func(ctx context.Context, alert *core.Alert) (*core.ClassificationResult, error)
	healthFunc   func(ctx context.Context) error
}

func (m *mockLLMClient) ClassifyAlert(ctx context.Context, alert *core.Alert) (*core.ClassificationResult, error) {
	if m.classifyFunc != nil {
		return m.classifyFunc(ctx, alert)
	}
	return &core.ClassificationResult{
		Severity:   core.SeverityCritical,
		Confidence: 0.95,
		Reasoning:  "High resource usage detected",
	}, nil
}

func (m *mockLLMClient) Health(ctx context.Context) error {
	if m.healthFunc != nil {
		return m.healthFunc(ctx)
	}
	return nil
}

type mockFilterEngine struct {
	shouldBlockFunc func(alert *core.Alert, classification *core.ClassificationResult) (bool, string)
}

func (m *mockFilterEngine) ShouldBlock(alert *core.Alert, classification *core.ClassificationResult) (bool, string) {
	if m.shouldBlockFunc != nil {
		return m.shouldBlockFunc(alert, classification)
	}
	return false, "" // Default: don't block
}

type mockPublisher struct {
	publishToAllFunc              func(ctx context.Context, alert *core.Alert) error
	publishWithClassificationFunc func(ctx context.Context, alert *core.Alert, classification *core.ClassificationResult) error
}

func (m *mockPublisher) PublishToAll(ctx context.Context, alert *core.Alert) error {
	if m.publishToAllFunc != nil {
		return m.publishToAllFunc(ctx, alert)
	}
	return nil
}

func (m *mockPublisher) PublishWithClassification(ctx context.Context, alert *core.Alert, classification *core.ClassificationResult) error {
	if m.publishWithClassificationFunc != nil {
		return m.publishWithClassificationFunc(ctx, alert, classification)
	}
	return nil
}

func createTestAlert() *core.Alert {
	now := time.Now()
	return &core.Alert{
		Fingerprint:  "test-fingerprint-123",
		AlertName:    "HighCPUUsage",
		Status:       core.StatusFiring,
		Labels:       map[string]string{"severity": "warning", "namespace": "production"},
		Annotations:  map[string]string{"summary": "CPU usage is high"},
		StartsAt:     now,
		EndsAt:       nil,
		GeneratorURL: nil,
		Timestamp:    &now,
	}
}

func TestNewAlertProcessor(t *testing.T) {
	tests := []struct {
		name        string
		config      AlertProcessorConfig
		expectError bool
	}{
		{
			name: "valid_config",
			config: AlertProcessorConfig{
				EnrichmentManager: &mockEnrichmentManager{},
				FilterEngine:      &mockFilterEngine{},
				Publisher:         &mockPublisher{},
			},
			expectError: false,
		},
		{
			name: "missing_enrichment_manager",
			config: AlertProcessorConfig{
				FilterEngine: &mockFilterEngine{},
				Publisher:    &mockPublisher{},
			},
			expectError: true,
		},
		{
			name: "missing_filter_engine",
			config: AlertProcessorConfig{
				EnrichmentManager: &mockEnrichmentManager{},
				Publisher:         &mockPublisher{},
			},
			expectError: true,
		},
		{
			name: "missing_publisher",
			config: AlertProcessorConfig{
				EnrichmentManager: &mockEnrichmentManager{},
				FilterEngine:      &mockFilterEngine{},
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			processor, err := NewAlertProcessor(tt.config)
			if tt.expectError {
				assert.Error(t, err)
				assert.Nil(t, processor)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, processor)
			}
		})
	}
}

func TestAlertProcessor_ProcessAlert_TransparentWithRecommendations(t *testing.T) {
	publishToAllCalled := false

	enrichmentManager := &mockEnrichmentManager{
		mode: EnrichmentModeTransparentWithRecommendations,
	}

	publisher := &mockPublisher{
		publishToAllFunc: func(ctx context.Context, alert *core.Alert) error {
			publishToAllCalled = true
			return nil
		},
	}

	processor, err := NewAlertProcessor(AlertProcessorConfig{
		EnrichmentManager: enrichmentManager,
		FilterEngine:      &mockFilterEngine{},
		Publisher:         publisher,
		Logger:            slog.Default(),
	})
	assert.NoError(t, err)

	alert := createTestAlert()
	err = processor.ProcessAlert(context.Background(), alert)

	assert.NoError(t, err)
	assert.True(t, publishToAllCalled, "PublishToAll should be called in transparent_with_recommendations mode")
}

func TestAlertProcessor_ProcessAlert_Transparent(t *testing.T) {
	t.Run("not_blocked", func(t *testing.T) {
		publishToAllCalled := false

		enrichmentManager := &mockEnrichmentManager{
			mode: EnrichmentModeTransparent,
		}

		filterEngine := &mockFilterEngine{
			shouldBlockFunc: func(alert *core.Alert, classification *core.ClassificationResult) (bool, string) {
				return false, ""
			},
		}

		publisher := &mockPublisher{
			publishToAllFunc: func(ctx context.Context, alert *core.Alert) error {
				publishToAllCalled = true
				return nil
			},
		}

		processor, err := NewAlertProcessor(AlertProcessorConfig{
			EnrichmentManager: enrichmentManager,
			FilterEngine:      filterEngine,
			Publisher:         publisher,
		})
		assert.NoError(t, err)

		alert := createTestAlert()
		err = processor.ProcessAlert(context.Background(), alert)

		assert.NoError(t, err)
		assert.True(t, publishToAllCalled)
	})

	t.Run("blocked_by_filter", func(t *testing.T) {
		publishToAllCalled := false

		enrichmentManager := &mockEnrichmentManager{
			mode: EnrichmentModeTransparent,
		}

		filterEngine := &mockFilterEngine{
			shouldBlockFunc: func(alert *core.Alert, classification *core.ClassificationResult) (bool, string) {
				return true, "test alert"
			},
		}

		publisher := &mockPublisher{
			publishToAllFunc: func(ctx context.Context, alert *core.Alert) error {
				publishToAllCalled = true
				return nil
			},
		}

		processor, err := NewAlertProcessor(AlertProcessorConfig{
			EnrichmentManager: enrichmentManager,
			FilterEngine:      filterEngine,
			Publisher:         publisher,
		})
		assert.NoError(t, err)

		alert := createTestAlert()
		err = processor.ProcessAlert(context.Background(), alert)

		assert.NoError(t, err) // Filtering is not an error
		assert.False(t, publishToAllCalled, "Should not publish when blocked")
	})
}

func TestAlertProcessor_ProcessAlert_Enriched(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		classifyCalled := false
		publishWithClassificationCalled := false

		enrichmentManager := &mockEnrichmentManager{
			mode: EnrichmentModeEnriched,
		}

		llmClient := &mockLLMClient{
			classifyFunc: func(ctx context.Context, alert *core.Alert) (*core.ClassificationResult, error) {
				classifyCalled = true
				return &core.ClassificationResult{
					Severity:   core.SeverityCritical,
					Confidence: 0.95,
					Reasoning:  "Critical infrastructure issue",
				}, nil
			},
		}

		filterEngine := &mockFilterEngine{
			shouldBlockFunc: func(alert *core.Alert, classification *core.ClassificationResult) (bool, string) {
				return false, ""
			},
		}

		publisher := &mockPublisher{
			publishWithClassificationFunc: func(ctx context.Context, alert *core.Alert, classification *core.ClassificationResult) error {
				publishWithClassificationCalled = true
				assert.Equal(t, core.SeverityCritical, classification.Severity)
				return nil
			},
		}

		processor, err := NewAlertProcessor(AlertProcessorConfig{
			EnrichmentManager: enrichmentManager,
			LLMClient:         llmClient,
			FilterEngine:      filterEngine,
			Publisher:         publisher,
		})
		assert.NoError(t, err)

		alert := createTestAlert()
		err = processor.ProcessAlert(context.Background(), alert)

		assert.NoError(t, err)
		assert.True(t, classifyCalled)
		assert.True(t, publishWithClassificationCalled)
	})

	t.Run("llm_failure_fallback_to_transparent", func(t *testing.T) {
		enrichmentManager := &mockEnrichmentManager{
			mode: EnrichmentModeEnriched,
		}

		llmClient := &mockLLMClient{
			classifyFunc: func(ctx context.Context, alert *core.Alert) (*core.ClassificationResult, error) {
				return nil, errors.New("LLM service unavailable")
			},
		}

		publishToAllCalled := false
		publisher := &mockPublisher{
			publishToAllFunc: func(ctx context.Context, alert *core.Alert) error {
				publishToAllCalled = true
				return nil
			},
		}

		processor, err := NewAlertProcessor(AlertProcessorConfig{
			EnrichmentManager: enrichmentManager,
			LLMClient:         llmClient,
			FilterEngine:      &mockFilterEngine{},
			Publisher:         publisher,
		})
		assert.NoError(t, err)

		alert := createTestAlert()
		err = processor.ProcessAlert(context.Background(), alert)

		assert.NoError(t, err)
		assert.True(t, publishToAllCalled, "Should fall back to transparent mode")
	})

	t.Run("no_llm_client_fallback_to_transparent", func(t *testing.T) {
		enrichmentManager := &mockEnrichmentManager{
			mode: EnrichmentModeEnriched,
		}

		publishToAllCalled := false
		publisher := &mockPublisher{
			publishToAllFunc: func(ctx context.Context, alert *core.Alert) error {
				publishToAllCalled = true
				return nil
			},
		}

		processor, err := NewAlertProcessor(AlertProcessorConfig{
			EnrichmentManager: enrichmentManager,
			LLMClient:         nil, // No LLM client configured
			FilterEngine:      &mockFilterEngine{},
			Publisher:         publisher,
		})
		assert.NoError(t, err)

		alert := createTestAlert()
		err = processor.ProcessAlert(context.Background(), alert)

		assert.NoError(t, err)
		assert.True(t, publishToAllCalled)
	})

	t.Run("blocked_after_classification", func(t *testing.T) {
		enrichmentManager := &mockEnrichmentManager{
			mode: EnrichmentModeEnriched,
		}

		llmClient := &mockLLMClient{
			classifyFunc: func(ctx context.Context, alert *core.Alert) (*core.ClassificationResult, error) {
				return &core.ClassificationResult{
					Severity:   core.SeverityNoise,
					Confidence: 0.99,
				}, nil
			},
		}

		filterEngine := &mockFilterEngine{
			shouldBlockFunc: func(alert *core.Alert, classification *core.ClassificationResult) (bool, string) {
				// Block low-severity alerts
				return classification.Severity == core.SeverityNoise, "noise alert"
			},
		}

		publishCalled := false
		publisher := &mockPublisher{
			publishWithClassificationFunc: func(ctx context.Context, alert *core.Alert, classification *core.ClassificationResult) error {
				publishCalled = true
				return nil
			},
		}

		processor, err := NewAlertProcessor(AlertProcessorConfig{
			EnrichmentManager: enrichmentManager,
			LLMClient:         llmClient,
			FilterEngine:      filterEngine,
			Publisher:         publisher,
		})
		assert.NoError(t, err)

		alert := createTestAlert()
		err = processor.ProcessAlert(context.Background(), alert)

		assert.NoError(t, err)
		assert.False(t, publishCalled, "Should not publish when blocked")
	})
}

func TestAlertProcessor_Health(t *testing.T) {
	t.Run("healthy", func(t *testing.T) {
		processor, err := NewAlertProcessor(AlertProcessorConfig{
			EnrichmentManager: &mockEnrichmentManager{},
			FilterEngine:      &mockFilterEngine{},
			Publisher:         &mockPublisher{},
			LLMClient: &mockLLMClient{
				healthFunc: func(ctx context.Context) error {
					return nil
				},
			},
		})
		assert.NoError(t, err)

		err = processor.Health(context.Background())
		assert.NoError(t, err)
	})

	t.Run("enrichment_manager_unhealthy", func(t *testing.T) {
		processor, err := NewAlertProcessor(AlertProcessorConfig{
			EnrichmentManager: &mockEnrichmentManager{
				getModeFunc: func(ctx context.Context) (EnrichmentMode, error) {
					return "", errors.New("enrichment manager down")
				},
			},
			FilterEngine: &mockFilterEngine{},
			Publisher:    &mockPublisher{},
		})
		assert.NoError(t, err)

		err = processor.Health(context.Background())
		assert.Error(t, err)
	})

	t.Run("llm_unhealthy_non_critical", func(t *testing.T) {
		processor, err := NewAlertProcessor(AlertProcessorConfig{
			EnrichmentManager: &mockEnrichmentManager{},
			FilterEngine:      &mockFilterEngine{},
			Publisher:         &mockPublisher{},
			LLMClient: &mockLLMClient{
				healthFunc: func(ctx context.Context) error {
					return errors.New("LLM down")
				},
			},
		})
		assert.NoError(t, err)

		// LLM unhealthy is not critical (we can fall back)
		err = processor.Health(context.Background())
		assert.NoError(t, err, "LLM unhealthy should not fail health check")
	})
}

// Helper mock for enrichment manager
type mockEnrichmentManager struct {
	mode        EnrichmentMode
	source      string
	getModeFunc func(ctx context.Context) (EnrichmentMode, error)
}

func (m *mockEnrichmentManager) GetMode(ctx context.Context) (EnrichmentMode, error) {
	if m.getModeFunc != nil {
		return m.getModeFunc(ctx)
	}
	if m.mode == "" {
		return EnrichmentModeEnriched, nil
	}
	return m.mode, nil
}

func (m *mockEnrichmentManager) GetModeWithSource(ctx context.Context) (EnrichmentMode, string, error) {
	mode, err := m.GetMode(ctx)
	source := m.source
	if source == "" {
		source = "default"
	}
	return mode, source, err
}

func (m *mockEnrichmentManager) SetMode(ctx context.Context, mode EnrichmentMode) error {
	m.mode = mode
	return nil
}

func (m *mockEnrichmentManager) ValidateMode(mode EnrichmentMode) error {
	if !mode.IsValid() {
		return errors.New("invalid mode")
	}
	return nil
}

func (m *mockEnrichmentManager) GetStats(ctx context.Context) (*EnrichmentStats, error) {
	return &EnrichmentStats{
		CurrentMode:    m.mode,
		Source:         m.source,
		RedisAvailable: true,
	}, nil
}

func (m *mockEnrichmentManager) RefreshCache(ctx context.Context) error {
	return nil
}
