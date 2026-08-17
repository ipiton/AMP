package publishing

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/pkg/httperror"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// telegram_publisher_test.go - tests for EnhancedTelegramPublisher

// mockTelegramClient is a mock implementation of TelegramClient
type mockTelegramClient struct {
	mock.Mock
}

func (m *mockTelegramClient) SendMessage(ctx context.Context, message *TelegramMessage) (*TelegramResponse, error) {
	args := m.Called(ctx, message)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*TelegramResponse), args.Error(1)
}

func (m *mockTelegramClient) Health(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

// Helper functions

func setupTelegramPublisher(t *testing.T) (*EnhancedTelegramPublisher, *mockTelegramClient) {
	client := new(mockTelegramClient)
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))

	// Real formatter (not mocked) - the publisher's primary path renders
	// via the shared AlertFormatter (core.FormatTelegram), same as Slack's
	// postMessage path uses core.FormatSlack.
	formatter := NewAlertFormatter("")

	publisher := NewEnhancedTelegramPublisher(client, "-1001234567890", 0, false, nil, formatter, logger).(*EnhancedTelegramPublisher)

	return publisher, client
}

func createTelegramTestAlert(fingerprint, alertName string, status core.AlertStatus) *core.EnrichedAlert {
	return &core.EnrichedAlert{
		Alert: &core.Alert{
			Fingerprint: fingerprint,
			AlertName:   alertName,
			Status:      status,
			StartsAt:    time.Now(),
			Labels:      map[string]string{"severity": "critical", "namespace": "prod"},
			Annotations: map[string]string{"summary": "Test alert summary"},
		},
	}
}

func createTelegramTestTarget() *core.PublishingTarget {
	return &core.PublishingTarget{
		Name: "test-telegram",
		Type: "telegram",
	}
}

// TestTelegramPublish_Firing tests publishing a firing alert
func TestTelegramPublish_Firing(t *testing.T) {
	publisher, client := setupTelegramPublisher(t)
	ctx := context.Background()

	alert := createTelegramTestAlert("fp123", "HighCPUUsage", core.StatusFiring)
	target := createTelegramTestTarget()

	client.On("SendMessage", ctx, mock.MatchedBy(func(msg *TelegramMessage) bool {
		return msg.ChatID == "-1001234567890" &&
			strings.Contains(msg.Text, "FIRING") &&
			strings.Contains(msg.Text, "HighCPUUsage") &&
			strings.Contains(msg.Text, "Test alert summary") &&
			msg.ParseMode == TelegramParseModeHTML
	})).Return(&TelegramResponse{OK: true, Result: &TelegramResult{MessageID: 1}}, nil)

	err := publisher.Publish(ctx, alert, target)

	require.NoError(t, err)
	client.AssertExpectations(t)
}

// TestTelegramPublish_Resolved tests publishing a resolved alert
func TestTelegramPublish_Resolved(t *testing.T) {
	publisher, client := setupTelegramPublisher(t)
	ctx := context.Background()

	alert := createTelegramTestAlert("fp123", "HighCPUUsage", core.StatusResolved)
	target := createTelegramTestTarget()

	client.On("SendMessage", ctx, mock.MatchedBy(func(msg *TelegramMessage) bool {
		return strings.Contains(msg.Text, "RESOLVED") &&
			!strings.Contains(msg.Text, "FIRING")
	})).Return(&TelegramResponse{OK: true, Result: &TelegramResult{MessageID: 2}}, nil)

	err := publisher.Publish(ctx, alert, target)

	require.NoError(t, err)
	client.AssertExpectations(t)
}

// TestTelegramPublish_ClientError tests Telegram API client error handling
func TestTelegramPublish_ClientError(t *testing.T) {
	publisher, client := setupTelegramPublisher(t)
	ctx := context.Background()

	alert := createTelegramTestAlert("fp123", "test-alert", core.StatusFiring)
	target := createTelegramTestTarget()

	client.On("SendMessage", ctx, mock.AnythingOfType("*publishing.TelegramMessage")).
		Return(nil, errors.New("telegram API error"))

	err := publisher.Publish(ctx, alert, target)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to send message")
	client.AssertExpectations(t)
}

// TestTelegramPublish_UnknownStatus tests unknown alert status handling
func TestTelegramPublish_UnknownStatus(t *testing.T) {
	publisher, client := setupTelegramPublisher(t)
	ctx := context.Background()

	alert := createTelegramTestAlert("fp123", "test-alert", core.AlertStatus("unknown"))
	target := createTelegramTestTarget()

	err := publisher.Publish(ctx, alert, target)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown alert status")
	client.AssertNotCalled(t, "SendMessage")
}

// TestTelegramName tests Name() method
func TestTelegramName(t *testing.T) {
	publisher, _ := setupTelegramPublisher(t)

	assert.Equal(t, "Telegram", publisher.Name())
}

// TestTelegramBuildMessage_Fields tests buildMessage carries publisher configuration
func TestTelegramBuildMessage_Fields(t *testing.T) {
	client := new(mockTelegramClient)
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	publisher := NewEnhancedTelegramPublisher(client, "@my_channel", 55, true, nil, NewAlertFormatter(""), logger).(*EnhancedTelegramPublisher)

	alert := createTelegramTestAlert("fp1", "alert1", core.StatusFiring)
	message, err := publisher.buildMessage(context.Background(), alert)

	require.NoError(t, err)
	require.NotNil(t, message)
	assert.Equal(t, "@my_channel", message.ChatID)
	assert.Equal(t, 55, message.MessageThreadID)
	assert.True(t, message.DisableNotification)
	assert.Equal(t, TelegramParseModeHTML, message.ParseMode)
}

// TestTelegramBuildMessage_FormatterError tests formatter error propagation
func TestTelegramBuildMessage_FormatterError(t *testing.T) {
	client := new(mockTelegramClient)
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	publisher := NewEnhancedTelegramPublisher(client, "-1001234567890", 0, false, nil, &erroringTelegramFormatter{}, logger).(*EnhancedTelegramPublisher)

	alert := createTelegramTestAlert("fp1", "alert1", core.StatusFiring)
	message, err := publisher.buildMessage(context.Background(), alert)

	require.Error(t, err)
	assert.Nil(t, message)
	assert.Contains(t, err.Error(), "failed to format alert")
}

// TestTelegramPublish_FormatterError tests that a formatter error on the
// primary send path is surfaced without ever calling the Telegram client.
func TestTelegramPublish_FormatterError(t *testing.T) {
	client := new(mockTelegramClient)
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	publisher := NewEnhancedTelegramPublisher(client, "-1001234567890", 0, false, nil, &erroringTelegramFormatter{}, logger).(*EnhancedTelegramPublisher)

	alert := createTelegramTestAlert("fp1", "alert1", core.StatusFiring)
	target := createTelegramTestTarget()

	err := publisher.Publish(context.Background(), alert, target)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to format alert")
	client.AssertNotCalled(t, "SendMessage")
}

// erroringTelegramFormatter is an AlertFormatter stub that always errors,
// used to test the format-error path on EnhancedTelegramPublisher.
type erroringTelegramFormatter struct{}

func (f *erroringTelegramFormatter) FormatAlert(_ context.Context, _ *core.EnrichedAlert, _ core.PublishingFormat) (map[string]any, error) {
	return nil, errors.New("formatter boom")
}

// TestTelegramBuildText_Truncation tests message truncation at 4096 characters
func TestTelegramBuildText_Truncation(t *testing.T) {
	publisher, _ := setupTelegramPublisher(t)

	longSummary := strings.Repeat("A", 10000)
	alert := &core.EnrichedAlert{
		Alert: &core.Alert{
			Fingerprint: "fp1",
			AlertName:   "LongAlert",
			Status:      core.StatusFiring,
			StartsAt:    time.Now(),
			Annotations: map[string]string{"summary": longSummary},
		},
	}

	message, err := publisher.buildMessage(context.Background(), alert)

	require.NoError(t, err)
	require.NotNil(t, message)
	assert.LessOrEqual(t, len([]rune(message.Text)), MaxTelegramMessageLength)
	assert.Contains(t, message.Text, "[truncated]")
}

// TestTelegramBuildText_NoTruncationForShortMessages verifies short messages are untouched
func TestTelegramBuildText_NoTruncationForShortMessages(t *testing.T) {
	publisher, _ := setupTelegramPublisher(t)

	alert := createTelegramTestAlert("fp1", "ShortAlert", core.StatusFiring)
	message, err := publisher.buildMessage(context.Background(), alert)

	require.NoError(t, err)
	require.NotNil(t, message)
	assert.NotContains(t, message.Text, "[truncated]")
	assert.Contains(t, message.Text, "ShortAlert")
}

// TestTelegramBuildText_HTMLEscaping is an integration check that the
// publisher's primary send path (buildMessage -> shared AlertFormatter via
// core.FormatTelegram) still escapes alert-controlled values end-to-end.
// The escaping logic itself is unit-tested at the formatter level in
// formatter_test.go (TestFormatTelegram_HTMLEscaping).
func TestTelegramBuildText_HTMLEscaping(t *testing.T) {
	publisher, _ := setupTelegramPublisher(t)

	alert := &core.EnrichedAlert{
		Alert: &core.Alert{
			Fingerprint: "fp1",
			AlertName:   "<script>alert(1)</script>",
			Status:      core.StatusFiring,
			StartsAt:    time.Now(),
			Annotations: map[string]string{"summary": "5 < 10 & 10 > 5"},
		},
	}

	message, err := publisher.buildMessage(context.Background(), alert)

	require.NoError(t, err)
	require.NotNil(t, message)
	assert.NotContains(t, message.Text, "<script>")
	assert.Contains(t, message.Text, "&lt;script&gt;")
	assert.Contains(t, message.Text, "&lt; 10 &amp; 10 &gt; 5")
}

// TestClassifyTelegramError tests error classification for metrics
func TestClassifyTelegramError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected string
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: "unknown",
		},
		{
			name:     "rate limit error",
			err:      httperror.NewHTTPError(429, "rate_limited", ProviderTelegram),
			expected: "rate_limit",
		},
		{
			name:     "server error",
			err:      httperror.NewHTTPError(503, "service_unavailable", ProviderTelegram),
			expected: "server_error",
		},
		{
			name:     "auth error",
			err:      httperror.NewHTTPError(401, "unauthorized", ProviderTelegram),
			expected: "auth_error",
		},
		{
			name:     "bad request error",
			err:      httperror.NewHTTPError(400, "invalid_payload", ProviderTelegram),
			expected: "bad_request",
		},
		{
			name:     "network error",
			err:      errors.New("network timeout"),
			expected: "network_error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := classifyTelegramError(tt.err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestTruncateTelegramMessage tests the standalone truncation helper
func TestTruncateTelegramMessage(t *testing.T) {
	t.Run("short message unchanged", func(t *testing.T) {
		text := "hello world"
		assert.Equal(t, text, TruncateTelegramMessage(text))
	})

	t.Run("exact limit unchanged", func(t *testing.T) {
		text := strings.Repeat("x", MaxTelegramMessageLength)
		assert.Equal(t, text, TruncateTelegramMessage(text))
	})

	t.Run("over limit truncated", func(t *testing.T) {
		text := strings.Repeat("x", MaxTelegramMessageLength+100)
		result := TruncateTelegramMessage(text)
		assert.LessOrEqual(t, len([]rune(result)), MaxTelegramMessageLength)
		assert.Contains(t, result, "[truncated]")
	})

	t.Run("multi-byte runes not split", func(t *testing.T) {
		text := strings.Repeat("é", MaxTelegramMessageLength+50)
		result := TruncateTelegramMessage(text)
		// Must remain valid UTF-8 and within the rune limit
		assert.LessOrEqual(t, len([]rune(result)), MaxTelegramMessageLength)
		assert.True(t, len(result) > 0)
	})
}
