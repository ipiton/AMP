package services

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/infrastructure/cache"
)

// Tests for ClassificationService

func TestNewClassificationService_Success(t *testing.T) {
	mockLLM := &mockLLMClient{}
	mockCache := newMockCache()

	config := ClassificationServiceConfig{
		LLMClient: mockLLM,
		Cache:     mockCache,
		Config:    DefaultClassificationConfig(),
	}

	svc, err := NewClassificationService(config)
	require.NoError(t, err)
	assert.NotNil(t, svc)
}

func TestNewClassificationService_ValidationErrors(t *testing.T) {
	t.Run("nil LLM client when LLM enabled", func(t *testing.T) {
		mockCache := newMockCache()

		config := ClassificationServiceConfig{
			LLMClient: nil,
			Cache:     mockCache,
			Config:    DefaultClassificationConfig(),
		}

		_, err := NewClassificationService(config)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "LLM client is required")
	})

	t.Run("nil cache", func(t *testing.T) {
		mockLLM := &mockLLMClient{}

		config := ClassificationServiceConfig{
			LLMClient: mockLLM,
			Cache:     nil,
			Config:    DefaultClassificationConfig(),
		}

		_, err := NewClassificationService(config)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "cache is required")
	})
}

func TestClassificationService_ClassifyAlert_Success(t *testing.T) {
	mockLLM := &mockLLMClient{
		classifyFunc: func(ctx context.Context, alert *core.Alert) (*core.ClassificationResult, error) {
			return &core.ClassificationResult{
				Severity:   core.SeverityCritical,
				Confidence: 0.95,
				Reasoning:  "Critical infrastructure alert",
			}, nil
		},
	}
	mockCache := newMockCache()

	config := ClassificationServiceConfig{
		LLMClient: mockLLM,
		Cache:     mockCache,
		Config:    DefaultClassificationConfig(),
	}

	svc, err := NewClassificationService(config)
	require.NoError(t, err)

	alert := createTestAlert()
	alert.Fingerprint = "test-fp-classify-success"
	result, err := svc.ClassifyAlert(context.Background(), alert)

	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, core.SeverityCritical, result.Severity)
	assert.Equal(t, 0.95, result.Confidence)
}

func TestClassificationService_ClassifyAlert_CacheHit(t *testing.T) {
	callCount := 0
	mockLLM := &mockLLMClient{
		classifyFunc: func(ctx context.Context, alert *core.Alert) (*core.ClassificationResult, error) {
			callCount++
			return &core.ClassificationResult{
				Severity:   core.SeverityWarning,
				Confidence: 0.8,
			}, nil
		},
	}
	mockCache := newMockCache()

	config := ClassificationServiceConfig{
		LLMClient: mockLLM,
		Cache:     mockCache,
		Config:    DefaultClassificationConfig(),
	}

	svc, err := NewClassificationService(config)
	require.NoError(t, err)

	alert := createTestAlert()

	// First call - should call LLM
	result1, err := svc.ClassifyAlert(context.Background(), alert)
	require.NoError(t, err)
	assert.NotNil(t, result1)
	assert.Equal(t, 1, callCount)

	// Second call - should hit cache
	result2, err := svc.ClassifyAlert(context.Background(), alert)
	require.NoError(t, err)
	assert.NotNil(t, result2)
	assert.Equal(t, 1, callCount) // LLM not called again

	assert.Equal(t, result1.Severity, result2.Severity)
}

func TestClassificationService_ClassifyAlert_FallbackOnLLMFailure(t *testing.T) {
	mockLLM := &mockLLMClient{
		classifyFunc: func(ctx context.Context, alert *core.Alert) (*core.ClassificationResult, error) {
			return nil, errors.New("LLM unavailable")
		},
	}
	mockCache := newMockCache()

	config := ClassificationServiceConfig{
		LLMClient: mockLLM,
		Cache:     mockCache,
		Config:    DefaultClassificationConfig(),
	}

	svc, err := NewClassificationService(config)
	require.NoError(t, err)

	alert := createTestAlert()
	alert.AlertName = "NodeDown" // Match fallback rule

	result, err := svc.ClassifyAlert(context.Background(), alert)

	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, core.SeverityCritical, result.Severity)

	stats := svc.GetStats()
	assert.Greater(t, stats.FallbackRate, 0.0)
}

func TestClassificationService_ClassifyAlert_ValidationErrors(t *testing.T) {
	mockLLM := &mockLLMClient{}
	mockCache := newMockCache()

	config := ClassificationServiceConfig{
		LLMClient: mockLLM,
		Cache:     mockCache,
		Config:    DefaultClassificationConfig(),
	}

	svc, err := NewClassificationService(config)
	require.NoError(t, err)

	t.Run("nil alert", func(t *testing.T) {
		_, err := svc.ClassifyAlert(context.Background(), nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "alert cannot be nil")
	})

	t.Run("empty fingerprint", func(t *testing.T) {
		alert := createTestAlert()
		alert.Fingerprint = "" // Set empty fingerprint
		_, err := svc.ClassifyAlert(context.Background(), alert)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "fingerprint is required")
	})
}

func TestClassificationService_GetCachedClassification(t *testing.T) {
	mockLLM := &mockLLMClient{
		classifyFunc: func(ctx context.Context, alert *core.Alert) (*core.ClassificationResult, error) {
			return &core.ClassificationResult{
				Severity:   core.SeverityWarning,
				Confidence: 0.85,
				Reasoning:  "Test classification",
			}, nil
		},
	}
	mockCache := newMockCache()

	config := ClassificationServiceConfig{
		LLMClient: mockLLM,
		Cache:     mockCache,
		Config:    DefaultClassificationConfig(),
	}

	svc, err := NewClassificationService(config)
	require.NoError(t, err)

	// Populate cache using the same fingerprint
	alert := createTestAlert()
	fingerprint := alert.Fingerprint // Use the actual fingerprint from test alert
	_, err = svc.ClassifyAlert(context.Background(), alert)
	require.NoError(t, err)

	// Get from cache using the same fingerprint
	cached, err := svc.GetCachedClassification(context.Background(), fingerprint)
	require.NoError(t, err)
	assert.NotNil(t, cached)
	assert.Equal(t, core.SeverityWarning, cached.Severity)
	assert.Equal(t, 0.85, cached.Confidence)
}

func TestClassificationService_GetCachedClassification_NotFound(t *testing.T) {
	mockLLM := &mockLLMClient{}
	mockCache := newMockCache()

	config := ClassificationServiceConfig{
		LLMClient: mockLLM,
		Cache:     mockCache,
		Config:    DefaultClassificationConfig(),
	}

	svc, err := NewClassificationService(config)
	require.NoError(t, err)

	_, err = svc.GetCachedClassification(context.Background(), "non-existent")
	assert.Error(t, err)
	assert.ErrorIs(t, err, cache.ErrNotFound)
}

func TestClassificationService_ClassifyBatch(t *testing.T) {
	mockLLM := &mockLLMClient{}
	mockCache := newMockCache()

	config := ClassificationServiceConfig{
		LLMClient: mockLLM,
		Cache:     mockCache,
		Config:    DefaultClassificationConfig(),
	}

	svc, err := NewClassificationService(config)
	require.NoError(t, err)

	alerts := []*core.Alert{
		createTestAlert(),
		createTestAlert(),
		createTestAlert(),
	}

	results, err := svc.ClassifyBatch(context.Background(), alerts)
	require.NoError(t, err)
	assert.Len(t, results, 3)

	for _, result := range results {
		assert.NotNil(t, result)
	}
}

func TestClassificationService_ClassifyBatch_ExceedsMax(t *testing.T) {
	mockLLM := &mockLLMClient{}
	mockCache := newMockCache()

	cfg := DefaultClassificationConfig()
	cfg.MaxBatchSize = 2

	config := ClassificationServiceConfig{
		LLMClient: mockLLM,
		Cache:     mockCache,
		Config:    cfg,
	}

	svc, err := NewClassificationService(config)
	require.NoError(t, err)

	alerts := []*core.Alert{
		createTestAlert(),
		createTestAlert(),
		createTestAlert(),
	}

	_, err = svc.ClassifyBatch(context.Background(), alerts)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds maximum")
}

func TestClassificationService_InvalidateCache(t *testing.T) {
	mockLLM := &mockLLMClient{}
	mockCache := newMockCache()

	config := ClassificationServiceConfig{
		LLMClient: mockLLM,
		Cache:     mockCache,
		Config:    DefaultClassificationConfig(),
	}

	svc, err := NewClassificationService(config)
	require.NoError(t, err)

	alert := createTestAlert()
	_, err = svc.ClassifyAlert(context.Background(), alert)
	require.NoError(t, err)

	// Invalidate
	err = svc.InvalidateCache(context.Background(), "test-invalidate")
	require.NoError(t, err)

	// Verify not cached
	_, err = svc.GetCachedClassification(context.Background(), "test-invalidate")
	assert.Error(t, err)
}

func TestClassificationService_WarmCache(t *testing.T) {
	mockLLM := &mockLLMClient{}
	mockCache := newMockCache()

	config := ClassificationServiceConfig{
		LLMClient: mockLLM,
		Cache:     mockCache,
		Config:    DefaultClassificationConfig(),
	}

	svc, err := NewClassificationService(config)
	require.NoError(t, err)

	alerts := []*core.Alert{
		createTestAlert(),
		createTestAlert(),
	}

	err = svc.WarmCache(context.Background(), alerts)
	require.NoError(t, err)

	// Verify cached
	for _, alert := range alerts {
		cached, err := svc.GetCachedClassification(context.Background(), alert.Fingerprint)
		require.NoError(t, err)
		assert.NotNil(t, cached)
	}
}

func TestClassificationService_GetStats(t *testing.T) {
	mockLLM := &mockLLMClient{}
	mockCache := newMockCache()

	config := ClassificationServiceConfig{
		LLMClient: mockLLM,
		Cache:     mockCache,
		Config:    DefaultClassificationConfig(),
	}

	svc, err := NewClassificationService(config)
	require.NoError(t, err)

	stats := svc.GetStats()
	assert.Equal(t, int64(0), stats.TotalRequests)

	// Make requests
	alert1 := createTestAlert()
	_, _ = svc.ClassifyAlert(context.Background(), alert1)

	alert2 := createTestAlert()
	_, _ = svc.ClassifyAlert(context.Background(), alert2)

	// Cache hit
	_, _ = svc.ClassifyAlert(context.Background(), alert1)

	stats = svc.GetStats()
	assert.Equal(t, int64(3), stats.TotalRequests)
	assert.Greater(t, stats.CacheHitRate, 0.0)
}

func TestClassificationService_Health(t *testing.T) {
	t.Run("healthy with fallback", func(t *testing.T) {
		mockLLM := &mockLLMClient{
			healthFunc: func(ctx context.Context) error {
				return errors.New("LLM down")
			},
		}
		mockCache := newMockCache()

		config := ClassificationServiceConfig{
			LLMClient: mockLLM,
			Cache:     mockCache,
			Config:    DefaultClassificationConfig(),
		}

		svc, err := NewClassificationService(config)
		require.NoError(t, err)

		err = svc.Health(context.Background())
		assert.NoError(t, err) // Should pass with fallback
	})

	t.Run("unhealthy without fallback", func(t *testing.T) {
		mockLLM := &mockLLMClient{
			healthFunc: func(ctx context.Context) error {
				return errors.New("LLM down")
			},
		}
		mockCache := newMockCache()

		cfg := DefaultClassificationConfig()
		cfg.EnableFallback = false

		config := ClassificationServiceConfig{
			LLMClient: mockLLM,
			Cache:     mockCache,
			Config:    cfg,
		}

		svc, err := NewClassificationService(config)
		require.NoError(t, err)

		err = svc.Health(context.Background())
		assert.Error(t, err)
	})
}

func TestClassificationConfig_Validate(t *testing.T) {
	t.Run("valid config", func(t *testing.T) {
		cfg := DefaultClassificationConfig()
		err := cfg.Validate()
		assert.NoError(t, err)
	})

	t.Run("invalid cache TTL", func(t *testing.T) {
		cfg := DefaultClassificationConfig()
		cfg.CacheTTL = -1 * time.Hour
		err := cfg.Validate()
		assert.Error(t, err)
	})

	t.Run("memory TTL exceeds Redis TTL", func(t *testing.T) {
		cfg := DefaultClassificationConfig()
		cfg.MemoryCacheTTL = 2 * time.Hour
		cfg.CacheTTL = 1 * time.Hour
		err := cfg.Validate()
		assert.Error(t, err)
	})
}
