package llm

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ipiton/AMP/internal/core"
)

func TestHTTPLLMClient_ClassifyAlert(t *testing.T) {
	tests := []struct {
		name           string
		alert          *core.Alert
		serverResponse ClassificationResponse
		serverStatus   int
		wantErr        bool
		wantSeverity   core.AlertSeverity
	}{
		{
			name: "successful classification - critical",
			alert: &core.Alert{
				Fingerprint: "abc123",
				AlertName:   "HighCPUUsage",
				Status:      core.StatusFiring,
				Labels:      map[string]string{"severity": "critical"},
				Annotations: map[string]string{"description": "CPU high"},
				StartsAt:    time.Now(),
			},
			serverResponse: ClassificationResponse{
				Classification: LLMClassificationResponse{
					Severity:    4,
					Category:    "infrastructure",
					Summary:     "High CPU usage detected",
					Confidence:  0.9,
					Reasoning:   "CPU usage is above threshold",
					Suggestions: []string{"Check processes", "Scale resources"},
				},
				RequestID:      "test-123",
				ProcessingTime: "100ms",
			},
			serverStatus: http.StatusOK,
			wantErr:      false,
			wantSeverity: core.SeverityCritical,
		},
		{
			name: "successful classification - warning",
			alert: &core.Alert{
				Fingerprint: "def456",
				AlertName:   "MemoryUsage",
				Status:      core.StatusFiring,
				Labels:      map[string]string{},
				Annotations: map[string]string{},
				StartsAt:    time.Now(),
			},
			serverResponse: ClassificationResponse{
				Classification: LLMClassificationResponse{
					Severity:    3,
					Category:    "infrastructure",
					Summary:     "Memory usage warning",
					Confidence:  0.75,
					Reasoning:   "Memory usage increasing",
					Suggestions: []string{"Monitor"},
				},
				RequestID:      "test-456",
				ProcessingTime: "0.05",
			},
			serverStatus: http.StatusOK,
			wantErr:      false,
			wantSeverity: core.SeverityWarning,
		},
		{
			name:         "nil alert",
			alert:        nil,
			serverStatus: http.StatusOK,
			wantErr:      true,
		},
		{
			name: "server error",
			alert: &core.Alert{
				Fingerprint: "error123",
				AlertName:   "TestAlert",
				Status:      core.StatusFiring,
				Labels:      map[string]string{},
				Annotations: map[string]string{},
				StartsAt:    time.Now(),
			},
			serverStatus: http.StatusInternalServerError,
			wantErr:      true,
		},
		{
			name: "invalid severity in response",
			alert: &core.Alert{
				Fingerprint: "invalid123",
				AlertName:   "TestAlert",
				Status:      core.StatusFiring,
				Labels:      map[string]string{},
				Annotations: map[string]string{},
				StartsAt:    time.Now(),
			},
			serverResponse: ClassificationResponse{
				Classification: LLMClassificationResponse{
					Severity:   999, // Invalid severity
					Category:   "test",
					Confidence: 0.5,
					Reasoning:  "test",
				},
			},
			serverStatus: http.StatusOK,
			wantErr:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create test server
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/classify" {
					w.WriteHeader(tt.serverStatus)
					if tt.serverStatus == http.StatusOK {
						json.NewEncoder(w).Encode(tt.serverResponse)
					}
				}
			}))
			defer server.Close()

			// Create client
			config := DefaultConfig()
			config.BaseURL = server.URL
			config.MaxRetries = 1 // Reduce retries for faster tests
			config.RetryDelay = 10 * time.Millisecond

			logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
				Level: slog.LevelError, // Reduce log noise in tests
			}))

			client := NewHTTPLLMClient(config, logger)

			// Test classification
			ctx := context.Background()
			result, err := client.ClassifyAlert(ctx, tt.alert)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Nil(t, result)
			} else {
				require.NoError(t, err)
				require.NotNil(t, result)
				assert.Equal(t, tt.wantSeverity, result.Severity)
				assert.GreaterOrEqual(t, result.Confidence, 0.0)
				assert.LessOrEqual(t, result.Confidence, 1.0)
			}
		})
	}
}

func TestHTTPLLMClient_Health(t *testing.T) {
	tests := []struct {
		name         string
		serverStatus int
		wantErr      bool
	}{
		{
			name:         "healthy",
			serverStatus: http.StatusOK,
			wantErr:      false,
		},
		{
			name:         "unhealthy",
			serverStatus: http.StatusServiceUnavailable,
			wantErr:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/health" {
					w.WriteHeader(tt.serverStatus)
				}
			}))
			defer server.Close()

			config := DefaultConfig()
			config.BaseURL = server.URL

			client := NewHTTPLLMClient(config, nil)

			ctx := context.Background()
			err := client.Health(ctx)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestHTTPLLMClient_RetryLogic(t *testing.T) {
	attempts := 0
	maxAttempts := 3

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/classify" {
			attempts++
			if attempts < maxAttempts {
				w.WriteHeader(http.StatusServiceUnavailable)
			} else {
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(ClassificationResponse{
					Classification: LLMClassificationResponse{
						Severity:    2,
						Category:    "test",
						Summary:     "Test",
						Confidence:  0.5,
						Reasoning:   "Test",
						Suggestions: []string{},
					},
					RequestID:      "retry-test",
					ProcessingTime: "50ms",
				})
			}
		}
	}))
	defer server.Close()

	config := DefaultConfig()
	config.BaseURL = server.URL
	config.MaxRetries = 3
	config.RetryDelay = 10 * time.Millisecond
	config.RetryBackoff = 1.5
	config.CircuitBreaker.Enabled = false // Disable circuit breaker for retry test

	client := NewHTTPLLMClient(config, nil)

	alert := &core.Alert{
		Fingerprint: "retry123",
		AlertName:   "RetryTest",
		Status:      core.StatusFiring,
		Labels:      map[string]string{},
		Annotations: map[string]string{},
		StartsAt:    time.Now(),
	}

	ctx := context.Background()
	result, err := client.ClassifyAlert(ctx, alert)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, maxAttempts, attempts)
}

func TestMockLLMClient(t *testing.T) {
	mock := NewMockLLMClient()

	alert := &core.Alert{
		Fingerprint: "mock123",
		AlertName:   "MockAlert",
		Status:      core.StatusFiring,
		Labels:      map[string]string{},
		Annotations: map[string]string{},
		StartsAt:    time.Now(),
	}

	ctx := context.Background()

	t.Run("ClassifyAlert", func(t *testing.T) {
		result, err := mock.ClassifyAlert(ctx, alert)

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, core.SeverityWarning, result.Severity)
		assert.Equal(t, 0.85, result.Confidence)
	})

	t.Run("Health", func(t *testing.T) {
		err := mock.Health(ctx)
		assert.NoError(t, err)
	})

	t.Run("Custom ClassifyAlertFunc", func(t *testing.T) {
		mock.ClassifyAlertFunc = func(ctx context.Context, alert *core.Alert) (*core.ClassificationResult, error) {
			return &core.ClassificationResult{
				Severity:        core.SeverityCritical,
				Confidence:      1.0,
				Reasoning:       "Custom test",
				Recommendations: []string{"Custom action"},
			}, nil
		}

		result, err := mock.ClassifyAlert(ctx, alert)

		require.NoError(t, err)
		assert.Equal(t, core.SeverityCritical, result.Severity)
		assert.Equal(t, 1.0, result.Confidence)
	})
}

func TestHTTPLLMClient_ContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/classify" {
			time.Sleep(1 * time.Second) // Simulate slow response
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()

	config := DefaultConfig()
	config.BaseURL = server.URL
	config.MaxRetries = 0
	config.Timeout = 5 * time.Second

	client := NewHTTPLLMClient(config, nil)

	alert := &core.Alert{
		Fingerprint: "cancel123",
		AlertName:   "CancelTest",
		Status:      core.StatusFiring,
		Labels:      map[string]string{},
		Annotations: map[string]string{},
		StartsAt:    time.Now(),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	result, err := client.ClassifyAlert(ctx, alert)

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "context")
}

func TestHTTPLLMClient_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/classify" {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(ClassificationResponse{
				Error: "API key invalid",
			})
		}
	}))
	defer server.Close()

	config := DefaultConfig()
	config.BaseURL = server.URL
	config.MaxRetries = 0

	client := NewHTTPLLMClient(config, nil)

	alert := &core.Alert{
		Fingerprint: "error123",
		AlertName:   "ErrorTest",
		Status:      core.StatusFiring,
		Labels:      map[string]string{},
		Annotations: map[string]string{},
		StartsAt:    time.Now(),
	}

	ctx := context.Background()
	result, err := client.ClassifyAlert(ctx, alert)

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "API key invalid")
}
