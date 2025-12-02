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
	"github.com/ipiton/AMP/internal/core"
)

func TestNewRootlyPublisher(t *testing.T) {
	formatter := NewAlertFormatter()
	publisher := NewRootlyPublisher(formatter, slog.Default())

	assert.NotNil(t, publisher)
	assert.Equal(t, "Rootly", publisher.Name())
}

func TestNewPagerDutyPublisher(t *testing.T) {
	formatter := NewAlertFormatter()
	publisher := NewPagerDutyPublisher(formatter, slog.Default())

	assert.NotNil(t, publisher)
	assert.Equal(t, "PagerDuty", publisher.Name())
}

func TestNewSlackPublisher(t *testing.T) {
	formatter := NewAlertFormatter()
	publisher := NewSlackPublisher(formatter, slog.Default())

	assert.NotNil(t, publisher)
	assert.Equal(t, "Slack", publisher.Name())
}

func TestNewWebhookPublisher(t *testing.T) {
	formatter := NewAlertFormatter()
	publisher := NewWebhookPublisher(formatter, slog.Default())

	assert.NotNil(t, publisher)
	assert.Equal(t, "Webhook", publisher.Name())
}

func TestPublish_Success(t *testing.T) {
	// Create test HTTP server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "success"})
	}))
	defer server.Close()

	// Create publisher
	formatter := NewAlertFormatter()
	publisher := NewWebhookPublisher(formatter, slog.Default())

	// Create test alert
	now := time.Now()
	enrichedAlert := &core.EnrichedAlert{
		Alert: &core.Alert{
			Fingerprint: "test-123",
			AlertName:   "TestAlert",
			Status:      core.StatusFiring,
			Labels: map[string]string{
				"severity": "warning",
			},
			Annotations: map[string]string{
				"summary": "Test alert",
			},
			StartsAt: now,
		},
	}

	// Create target
	target := &core.PublishingTarget{
		Name:   "test-webhook",
		Type:   "webhook",
		URL:    server.URL,
		Format: core.FormatWebhook,
	}

	// Publish
	err := publisher.Publish(context.Background(), enrichedAlert, target)

	assert.NoError(t, err)
}

func TestPublish_HTTPError(t *testing.T) {
	// Create test HTTP server that returns error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Internal server error"))
	}))
	defer server.Close()

	// Create publisher
	formatter := NewAlertFormatter()
	publisher := NewWebhookPublisher(formatter, slog.Default())

	// Create test alert
	now := time.Now()
	enrichedAlert := &core.EnrichedAlert{
		Alert: &core.Alert{
			Fingerprint: "test-123",
			AlertName:   "TestAlert",
			Status:      core.StatusFiring,
			StartsAt:    now,
		},
	}

	// Create target
	target := &core.PublishingTarget{
		Name:   "test-webhook",
		Type:   "webhook",
		URL:    server.URL,
		Format: core.FormatWebhook,
	}

	// Publish (should fail)
	err := publisher.Publish(context.Background(), enrichedAlert, target)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "500")
}

func TestPublish_WithCustomHeaders(t *testing.T) {
	var receivedHeaders http.Header

	// Create test HTTP server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeaders = r.Header
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// Create publisher
	formatter := NewAlertFormatter()
	publisher := NewWebhookPublisher(formatter, slog.Default())

	// Create test alert
	now := time.Now()
	enrichedAlert := &core.EnrichedAlert{
		Alert: &core.Alert{
			Fingerprint: "test-123",
			AlertName:   "TestAlert",
			Status:      core.StatusFiring,
			StartsAt:    now,
		},
	}

	// Create target with custom headers
	target := &core.PublishingTarget{
		Name:   "test-webhook",
		Type:   "webhook",
		URL:    server.URL,
		Format: core.FormatWebhook,
		Headers: map[string]string{
			"X-Custom-Header": "custom-value",
			"Authorization":   "Bearer test-token",
		},
	}

	// Publish
	err := publisher.Publish(context.Background(), enrichedAlert, target)

	require.NoError(t, err)
	assert.Equal(t, "custom-value", receivedHeaders.Get("X-Custom-Header"))
	assert.Equal(t, "Bearer test-token", receivedHeaders.Get("Authorization"))
}

func TestPublisherFactory_CreatePublisher(t *testing.T) {
	formatter := NewAlertFormatter()
	factory := NewPublisherFactory(formatter, slog.Default())

	tests := []struct {
		targetType   string
		expectedName string
	}{
		{"rootly", "Rootly"},
		{"pagerduty", "PagerDuty"},
		{"slack", "Slack"},
		{"webhook", "Webhook"},
		{"alertmanager", "Webhook"}, // Alertmanager uses webhook publisher
		{"unknown", "Webhook"},      // Unknown defaults to webhook
	}

	for _, tt := range tests {
		t.Run(tt.targetType, func(t *testing.T) {
			publisher, err := factory.CreatePublisher(tt.targetType)

			require.NoError(t, err)
			assert.Equal(t, tt.expectedName, publisher.Name())
		})
	}
}
