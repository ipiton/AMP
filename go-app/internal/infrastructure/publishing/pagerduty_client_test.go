package publishing

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewPagerDutyEventsClient tests client initialization
func TestNewPagerDutyEventsClient(t *testing.T) {
	t.Run("default config", func(t *testing.T) {
		config := PagerDutyClientConfig{}
		logger := slog.Default()

		client := NewPagerDutyEventsClient(config, logger)

		assert.NotNil(t, client)
	})

	t.Run("custom config", func(t *testing.T) {
		config := PagerDutyClientConfig{
			BaseURL:    "https://custom.pagerduty.com",
			Timeout:    5 * time.Second,
			MaxRetries: 5,
			RateLimit:  60.0,
		}
		logger := slog.Default()

		client := NewPagerDutyEventsClient(config, logger)

		assert.NotNil(t, client)
	})
}

// TestTriggerEvent tests triggering PagerDuty events
func TestTriggerEvent(t *testing.T) {
	t.Run("success - 202 accepted", func(t *testing.T) {
		// Mock server
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "POST", r.Method)
			assert.Equal(t, "/v2/events", r.URL.Path)
			assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

			// Parse request
			var req TriggerEventRequest
			err := json.NewDecoder(r.Body).Decode(&req)
			require.NoError(t, err)

			assert.Equal(t, "test-routing-key", req.RoutingKey)
			assert.Equal(t, "trigger", req.EventAction)

			// Return success
			w.WriteHeader(http.StatusAccepted)
			resp := EventResponse{
				Status:   "success",
				Message:  "Event processed",
				DedupKey: "test-dedup-key",
			}
			json.NewEncoder(w).Encode(resp)
		}))
		defer server.Close()

		// Create client
		config := PagerDutyClientConfig{
			BaseURL:    server.URL,
			Timeout:    5 * time.Second,
			MaxRetries: 3,
		}
		client := NewPagerDutyEventsClient(config, slog.Default())

		// Trigger event
		req := &TriggerEventRequest{
			RoutingKey:  "test-routing-key",
			EventAction: "trigger",
			DedupKey:    "test-fingerprint",
			Payload: TriggerEventPayload{
				Summary:  "Test alert",
				Source:   "test-source",
				Severity: "critical",
			},
		}

		ctx := context.Background()
		resp, err := client.TriggerEvent(ctx, req)

		assert.NoError(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, "success", resp.Status)
		assert.Equal(t, "test-dedup-key", resp.DedupKey)
	})

	t.Run("error - missing routing key", func(t *testing.T) {
		config := PagerDutyClientConfig{}
		client := NewPagerDutyEventsClient(config, slog.Default())

		req := &TriggerEventRequest{
			RoutingKey: "", // Missing
			Payload: TriggerEventPayload{
				Summary:  "Test",
				Source:   "test",
				Severity: "critical",
			},
		}

		ctx := context.Background()
		_, err := client.TriggerEvent(ctx, req)

		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrMissingRoutingKey)
	})

	t.Run("error - 400 bad request (no retry)", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"status":  "error",
				"message": "Invalid request",
				"errors":  []string{"Summary is required"},
			})
		}))
		defer server.Close()

		config := PagerDutyClientConfig{
			BaseURL:    server.URL,
			MaxRetries: 3,
		}
		client := NewPagerDutyEventsClient(config, slog.Default())

		req := &TriggerEventRequest{
			RoutingKey: "test-key",
			Payload:    TriggerEventPayload{}, // Invalid
		}

		ctx := context.Background()
		_, err := client.TriggerEvent(ctx, req)

		assert.Error(t, err)
		var apiErr *PagerDutyAPIError
		assert.ErrorAs(t, err, &apiErr)
		assert.Equal(t, 400, apiErr.StatusCode)
		assert.Contains(t, apiErr.Message, "Invalid request")
	})

	t.Run("error - 401 unauthorized (no retry)", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"status":  "error",
				"message": "Invalid routing key",
			})
		}))
		defer server.Close()

		config := PagerDutyClientConfig{
			BaseURL:    server.URL,
			MaxRetries: 3,
		}
		client := NewPagerDutyEventsClient(config, slog.Default())

		req := &TriggerEventRequest{
			RoutingKey: "invalid-key",
			Payload: TriggerEventPayload{
				Summary:  "Test",
				Source:   "test",
				Severity: "critical",
			},
		}

		ctx := context.Background()
		_, err := client.TriggerEvent(ctx, req)

		assert.Error(t, err)
		var apiErr *PagerDutyAPIError
		assert.ErrorAs(t, err, &apiErr)
		assert.Equal(t, 401, apiErr.StatusCode)
	})

	t.Run("error - 429 rate limit (retry)", func(t *testing.T) {
		attempts := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			attempts++
			if attempts <= 2 {
				w.WriteHeader(http.StatusTooManyRequests)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"status":  "error",
					"message": "Rate limit exceeded",
				})
			} else {
				w.WriteHeader(http.StatusAccepted)
				json.NewEncoder(w).Encode(EventResponse{
					Status:   "success",
					DedupKey: "test-dedup",
				})
			}
		}))
		defer server.Close()

		config := PagerDutyClientConfig{
			BaseURL:    server.URL,
			MaxRetries: 3,
		}
		client := NewPagerDutyEventsClient(config, slog.Default())

		req := &TriggerEventRequest{
			RoutingKey: "test-key",
			Payload: TriggerEventPayload{
				Summary:  "Test",
				Source:   "test",
				Severity: "critical",
			},
		}

		ctx := context.Background()
		resp, err := client.TriggerEvent(ctx, req)

		assert.NoError(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, 3, attempts) // 2 failures + 1 success
	})

	t.Run("error - 500 server error (retry)", func(t *testing.T) {
		attempts := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			attempts++
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"status":  "error",
				"message": "Internal server error",
			})
		}))
		defer server.Close()

		config := PagerDutyClientConfig{
			BaseURL:    server.URL,
			MaxRetries: 3,
		}
		client := NewPagerDutyEventsClient(config, slog.Default())

		req := &TriggerEventRequest{
			RoutingKey: "test-key",
			Payload: TriggerEventPayload{
				Summary:  "Test",
				Source:   "test",
				Severity: "critical",
			},
		}

		ctx := context.Background()
		_, err := client.TriggerEvent(ctx, req)

		assert.Error(t, err)
		assert.Equal(t, 4, attempts) // Max retries: 3 + 1 initial = 4 attempts
	})

	t.Run("context cancellation", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(100 * time.Millisecond)
			w.WriteHeader(http.StatusAccepted)
			json.NewEncoder(w).Encode(EventResponse{Status: "success"})
		}))
		defer server.Close()

		config := PagerDutyClientConfig{
			BaseURL: server.URL,
		}
		client := NewPagerDutyEventsClient(config, slog.Default())

		req := &TriggerEventRequest{
			RoutingKey: "test-key",
			Payload: TriggerEventPayload{
				Summary:  "Test",
				Source:   "test",
				Severity: "critical",
			},
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		defer cancel()

		_, err := client.TriggerEvent(ctx, req)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "context")
	})
}

// TestAcknowledgeEvent tests acknowledging events
func TestAcknowledgeEvent(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "POST", r.Method)

			var req AcknowledgeEventRequest
			json.NewDecoder(r.Body).Decode(&req)

			assert.Equal(t, "acknowledge", req.EventAction)
			assert.Equal(t, "test-dedup-key", req.DedupKey)

			w.WriteHeader(http.StatusAccepted)
			json.NewEncoder(w).Encode(EventResponse{
				Status:   "success",
				DedupKey: req.DedupKey,
			})
		}))
		defer server.Close()

		config := PagerDutyClientConfig{BaseURL: server.URL}
		client := NewPagerDutyEventsClient(config, slog.Default())

		req := &AcknowledgeEventRequest{
			RoutingKey: "test-key",
			DedupKey:   "test-dedup-key",
		}

		ctx := context.Background()
		resp, err := client.AcknowledgeEvent(ctx, req)

		assert.NoError(t, err)
		assert.Equal(t, "success", resp.Status)
	})

	t.Run("error - missing dedup key", func(t *testing.T) {
		config := PagerDutyClientConfig{}
		client := NewPagerDutyEventsClient(config, slog.Default())

		req := &AcknowledgeEventRequest{
			RoutingKey: "test-key",
			DedupKey:   "", // Missing
		}

		ctx := context.Background()
		_, err := client.AcknowledgeEvent(ctx, req)

		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidDedupKey)
	})
}

// TestResolveEvent tests resolving events
func TestResolveEvent(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var req ResolveEventRequest
			json.NewDecoder(r.Body).Decode(&req)

			assert.Equal(t, "resolve", req.EventAction)

			w.WriteHeader(http.StatusAccepted)
			json.NewEncoder(w).Encode(EventResponse{
				Status:   "success",
				DedupKey: req.DedupKey,
			})
		}))
		defer server.Close()

		config := PagerDutyClientConfig{BaseURL: server.URL}
		client := NewPagerDutyEventsClient(config, slog.Default())

		req := &ResolveEventRequest{
			RoutingKey: "test-key",
			DedupKey:   "test-dedup-key",
		}

		ctx := context.Background()
		resp, err := client.ResolveEvent(ctx, req)

		assert.NoError(t, err)
		assert.Equal(t, "success", resp.Status)
	})
}

// TestSendChangeEvent tests sending change events
func TestSendChangeEvent(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "POST", r.Method)
			assert.Equal(t, "/v2/change/enqueue", r.URL.Path)

			w.WriteHeader(http.StatusAccepted)
			json.NewEncoder(w).Encode(ChangeEventResponse{
				Status:  "success",
				Message: "Change event processed",
			})
		}))
		defer server.Close()

		config := PagerDutyClientConfig{BaseURL: server.URL}
		client := NewPagerDutyEventsClient(config, slog.Default())

		req := &ChangeEventRequest{
			RoutingKey: "test-key",
			Payload: ChangeEventPayload{
				Summary: "Deployment: v1.2.3",
				Source:  "ci-cd",
			},
		}

		ctx := context.Background()
		resp, err := client.SendChangeEvent(ctx, req)

		assert.NoError(t, err)
		assert.Equal(t, "success", resp.Status)
	})
}

// TestHealth tests health check
func TestHealth(t *testing.T) {
	t.Run("success - API reachable", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest) // Even 400 means API is reachable
		}))
		defer server.Close()

		config := PagerDutyClientConfig{BaseURL: server.URL}
		client := NewPagerDutyEventsClient(config, slog.Default())

		ctx := context.Background()
		err := client.Health(ctx)

		assert.NoError(t, err)
	})

	t.Run("error - API unreachable", func(t *testing.T) {
		config := PagerDutyClientConfig{
			BaseURL: "http://invalid-url-that-does-not-exist",
			Timeout: 100 * time.Millisecond,
		}
		client := NewPagerDutyEventsClient(config, slog.Default())

		ctx := context.Background()
		err := client.Health(ctx)

		assert.Error(t, err)
	})
}

// TestCalculateBackoff tests exponential backoff calculation
func TestCalculateBackoff(t *testing.T) {
	config := PagerDutyClientConfig{}
	client := NewPagerDutyEventsClient(config, slog.Default()).(*pagerDutyEventsClientImpl)

	tests := []struct {
		attempt  int
		expected time.Duration
	}{
		{0, 100 * time.Millisecond},
		{1, 200 * time.Millisecond},
		{2, 400 * time.Millisecond},
		{3, 800 * time.Millisecond},
		{4, 1600 * time.Millisecond},
		{10, 5 * time.Second}, // Capped at max
	}

	for _, tt := range tests {
		t.Run("attempt "+string(rune(tt.attempt)), func(t *testing.T) {
			backoff := client.calculateBackoff(tt.attempt)
			assert.Equal(t, tt.expected, backoff)
		})
	}
}

// TestShouldRetry tests retry decision logic
func TestShouldRetry(t *testing.T) {
	tests := []struct {
		statusCode int
		expected   bool
	}{
		{200, false},
		{202, false},
		{400, false}, // Bad request - no retry
		{401, false}, // Unauthorized - no retry
		{403, false}, // Forbidden - no retry
		{404, false}, // Not found - no retry
		{429, true},  // Rate limit - retry
		{500, true},  // Server error - retry
		{502, true},  // Bad gateway - retry
		{503, true},  // Service unavailable - retry
		{504, true},  // Gateway timeout - retry
	}

	for _, tt := range tests {
		t.Run("status "+string(rune(tt.statusCode)), func(t *testing.T) {
			result := shouldRetry(tt.statusCode)
			assert.Equal(t, tt.expected, result)
		})
	}
}
