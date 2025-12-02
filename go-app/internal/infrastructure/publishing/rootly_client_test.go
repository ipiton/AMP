package publishing

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"log/slog"
)

func TestNewRootlyIncidentsClient(t *testing.T) {
	config := ClientConfig{
		BaseURL: "https://api.rootly.com/v1",
		APIKey:  "test-api-key",
		Timeout: 10 * time.Second,
	}

	client := NewRootlyIncidentsClient(config, slog.Default())
	assert.NotNil(t, client)
}

func TestNewRootlyIncidentsClient_Defaults(t *testing.T) {
	config := ClientConfig{
		BaseURL: "https://api.rootly.com/v1",
		APIKey:  "test-api-key",
		// Omit timeout, rate limit, etc to test defaults
	}

	client := NewRootlyIncidentsClient(config, slog.Default())
	assert.NotNil(t, client)

	// Test defaults are applied (internal check)
	defaultClient := client.(*defaultRootlyIncidentsClient)
	assert.Equal(t, 10*time.Second, defaultClient.httpClient.Timeout)
	assert.Equal(t, 3, defaultClient.retryConfig.MaxRetries)
	assert.Equal(t, 100*time.Millisecond, defaultClient.retryConfig.BaseDelay)
	assert.Equal(t, 5*time.Second, defaultClient.retryConfig.MaxDelay)
}

func TestCreateIncident_Success(t *testing.T) {
	// Create mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/incidents", r.URL.Path)
		assert.Equal(t, "Bearer test-api-key", r.Header.Get("Authorization"))
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		// Return success response
		w.WriteHeader(http.StatusCreated)
		response := IncidentResponse{}
		response.Data.ID = "incident-123"
		response.Data.Type = "incidents"
		response.Data.Attributes.Status = "started"
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	// Create client
	client := NewRootlyIncidentsClient(ClientConfig{
		BaseURL: server.URL,
		APIKey:  "test-api-key",
	}, slog.Default())

	// Create incident
	req := &CreateIncidentRequest{
		Title:       "Test Incident",
		Description: "Test description",
		Severity:    "critical",
		StartedAt:   time.Now(),
	}

	resp, err := client.CreateIncident(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, "incident-123", resp.GetID())
	assert.Equal(t, "started", resp.GetStatus())
}

func TestCreateIncident_ValidationError(t *testing.T) {
	client := NewRootlyIncidentsClient(ClientConfig{
		BaseURL: "https://api.rootly.com/v1",
		APIKey:  "test-api-key",
	}, slog.Default())

	// Invalid request (missing title)
	req := &CreateIncidentRequest{
		Description: "Test description",
		Severity:    "critical",
		StartedAt:   time.Now(),
	}

	_, err := client.CreateIncident(context.Background(), req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "title is required")
}

func TestCreateIncident_RateLimitError(t *testing.T) {
	// Create mock server that returns 429
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"errors": []map[string]interface{}{
				{
					"status": "429",
					"title":  "Rate limit exceeded",
					"detail": "Try again in 30 seconds",
				},
			},
		})
	}))
	defer server.Close()

	client := NewRootlyIncidentsClient(ClientConfig{
		BaseURL: server.URL,
		APIKey:  "test-api-key",
		RetryConfig: RetryConfig{
			MaxRetries: 0, // No retries for faster test
		},
	}, slog.Default())

	req := &CreateIncidentRequest{
		Title:       "Test Incident",
		Description: "Test description",
		Severity:    "critical",
		StartedAt:   time.Now(),
	}

	_, err := client.CreateIncident(context.Background(), req)
	assert.Error(t, err)

	assert.True(t, IsRootlyAPIError(err))
	rootlyErr := err.(*RootlyAPIError)
	assert.Equal(t, http.StatusTooManyRequests, rootlyErr.StatusCode)
	assert.True(t, rootlyErr.IsRateLimit())
	assert.True(t, rootlyErr.IsRetryable())
}

func TestUpdateIncident_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPatch, r.Method)
		assert.Contains(t, r.URL.Path, "/incidents/incident-123")

		w.WriteHeader(http.StatusOK)
		response := IncidentResponse{}
		response.Data.ID = "incident-123"
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	client := NewRootlyIncidentsClient(ClientConfig{
		BaseURL: server.URL,
		APIKey:  "test-api-key",
	}, slog.Default())

	req := &UpdateIncidentRequest{
		Description: "Updated description",
	}

	resp, err := client.UpdateIncident(context.Background(), "incident-123", req)
	require.NoError(t, err)
	assert.Equal(t, "incident-123", resp.GetID())
}

func TestResolveIncident_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Contains(t, r.URL.Path, "/incidents/incident-123/resolve")

		w.WriteHeader(http.StatusOK)
		response := IncidentResponse{}
		response.Data.ID = "incident-123"
		response.Data.Attributes.Status = "resolved"
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	client := NewRootlyIncidentsClient(ClientConfig{
		BaseURL: server.URL,
		APIKey:  "test-api-key",
	}, slog.Default())

	req := &ResolveIncidentRequest{
		Summary: "Issue resolved",
	}

	resp, err := client.ResolveIncident(context.Background(), "incident-123", req)
	require.NoError(t, err)
	assert.Equal(t, "incident-123", resp.GetID())
	assert.True(t, resp.IsResolved())
}

func TestRetryLogic_ExponentialBackoff(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			// Return 500 for first 2 attempts
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		// Success on 3rd attempt
		w.WriteHeader(http.StatusCreated)
		response := IncidentResponse{}
		response.Data.ID = "incident-123"
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	client := NewRootlyIncidentsClient(ClientConfig{
		BaseURL: server.URL,
		APIKey:  "test-api-key",
		RetryConfig: RetryConfig{
			MaxRetries: 3,
			BaseDelay:  10 * time.Millisecond,
			MaxDelay:   100 * time.Millisecond,
		},
	}, slog.Default())

	req := &CreateIncidentRequest{
		Title:       "Test Incident",
		Description: "Test description",
		Severity:    "critical",
		StartedAt:   time.Now(),
	}

	start := time.Now()
	resp, err := client.CreateIncident(context.Background(), req)
	duration := time.Since(start)

	require.NoError(t, err)
	assert.Equal(t, "incident-123", resp.GetID())
	assert.Equal(t, 3, attempts)
	assert.Greater(t, duration, 25*time.Millisecond)
}

func BenchmarkCreateIncident(b *testing.B) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		response := IncidentResponse{}
		response.Data.ID = "incident-123"
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	client := NewRootlyIncidentsClient(ClientConfig{
		BaseURL: server.URL,
		APIKey:  "test-api-key",
	}, slog.Default())

	req := &CreateIncidentRequest{
		Title:       "Benchmark Incident",
		Description: "Benchmark test",
		Severity:    "critical",
		StartedAt:   time.Now(),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = client.CreateIncident(context.Background(), req)
	}
}
