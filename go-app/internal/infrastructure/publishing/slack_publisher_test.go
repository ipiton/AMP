package publishing

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/ipiton/AMP/internal/core"
)

// slack_publisher_test.go - Comprehensive tests for EnhancedSlackPublisher
// Coverage target: 90%+, 20+ tests

var (
	// Shared metrics instance to avoid duplicate registration
	sharedSlackMetrics *SlackMetrics
)

func init() {
	// Create metrics once for all tests
	sharedSlackMetrics = NewSlackMetrics()
}

// mockSlackWebhookClient is a mock implementation of SlackWebhookClient (Slack-specific)
type mockSlackWebhookClient struct {
	mock.Mock
}

func (m *mockSlackWebhookClient) PostMessage(ctx context.Context, message *SlackMessage) (*SlackResponse, error) {
	args := m.Called(ctx, message)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*SlackResponse), args.Error(1)
}

func (m *mockSlackWebhookClient) ReplyInThread(ctx context.Context, threadTS string, message *SlackMessage) (*SlackResponse, error) {
	args := m.Called(ctx, threadTS, message)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*SlackResponse), args.Error(1)
}

func (m *mockSlackWebhookClient) Health(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

// mockSlackMessageIDCache is a mock implementation of MessageIDCache (Slack-specific)
type mockSlackMessageIDCache struct {
	mock.Mock
}

func (m *mockSlackMessageIDCache) Store(fingerprint string, entry *MessageEntry) {
	m.Called(fingerprint, entry)
}

func (m *mockSlackMessageIDCache) Get(fingerprint string) (*MessageEntry, bool) {
	args := m.Called(fingerprint)
	if args.Get(0) == nil {
		return nil, args.Bool(1)
	}
	return args.Get(0).(*MessageEntry), args.Bool(1)
}

func (m *mockSlackMessageIDCache) Delete(fingerprint string) {
	m.Called(fingerprint)
}

func (m *mockSlackMessageIDCache) Cleanup(ttl time.Duration) int {
	args := m.Called(ttl)
	return args.Int(0)
}

func (m *mockSlackMessageIDCache) Size() int {
	args := m.Called()
	return args.Int(0)
}

// mockSlackAlertFormatter is a mock implementation of AlertFormatter (Slack-specific)
type mockSlackAlertFormatter struct {
	mock.Mock
}

func (m *mockSlackAlertFormatter) FormatAlert(ctx context.Context, enrichedAlert *core.EnrichedAlert, format core.PublishingFormat) (map[string]any, error) {
	args := m.Called(ctx, enrichedAlert, format)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(map[string]any), args.Error(1)
}

// Helper functions

func setupSlackPublisher(t *testing.T) (*EnhancedSlackPublisher, *mockSlackWebhookClient, *mockSlackMessageIDCache, *mockSlackAlertFormatter) {
	client := new(mockSlackWebhookClient)
	cache := new(mockSlackMessageIDCache)
	formatter := new(mockSlackAlertFormatter)
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))

	// Use shared metrics to avoid duplicate registration
	publisher := NewEnhancedSlackPublisher(client, cache, sharedSlackMetrics, formatter, logger).(*EnhancedSlackPublisher)

	return publisher, client, cache, formatter
}

func createSlackTestAlert(fingerprint, alertName string, status core.AlertStatus) *core.EnrichedAlert {
	return &core.EnrichedAlert{
		Alert: &core.Alert{
			Fingerprint: fingerprint,
			AlertName:   alertName,
			Status:      status,
			StartsAt:    time.Now(),
			Labels:      map[string]string{"severity": "critical"},
			Annotations: map[string]string{"summary": "Test alert"},
		},
		Classification: &core.ClassificationResult{
			Severity:   core.SeverityCritical,
			Confidence: 0.95,
			Reasoning:  "Test reasoning",
		},
	}
}

func createSlackTestTarget() *core.PublishingTarget {
	return &core.PublishingTarget{
		Name: "test-slack",
		Type: "slack",
		URL:  "https://hooks.slack.com/services/test",
	}
}

// Test cases

// TestPublish_NewFiringAlert tests publishing a new firing alert
func TestPublish_NewFiringAlert(t *testing.T) {
	publisher, client, cache, formatter := setupSlackPublisher(t)
	ctx := context.Background()

	alert := createSlackTestAlert("fp123", "test-alert", core.StatusFiring)
	target := createSlackTestTarget()

	// Mock cache miss (no existing message)
	cache.On("Get", "fp123").Return(nil, false)

	// Mock formatter success
	formattedPayload := map[string]any{
		"text": "Test alert",
		"blocks": []interface{}{
			map[string]interface{}{
				"type": "header",
				"text": map[string]interface{}{
					"type": "plain_text",
					"text": "Test header",
				},
			},
		},
	}
	formatter.On("FormatAlert", ctx, alert, core.FormatSlack).Return(formattedPayload, nil)

	// Mock client success
	slackResp := &SlackResponse{
		OK: true,
		TS: "1234567890.123456",
	}
	client.On("PostMessage", ctx, mock.AnythingOfType("*publishing.SlackMessage")).Return(slackResp, nil)

	// Mock cache store
	cache.On("Store", "fp123", mock.MatchedBy(func(entry *MessageEntry) bool {
		return entry.MessageTS == "1234567890.123456" && entry.ThreadTS == "1234567890.123456"
	})).Return()

	// Execute
	err := publisher.Publish(ctx, alert, target)

	// Verify
	require.NoError(t, err)
	cache.AssertExpectations(t)
	formatter.AssertExpectations(t)
	client.AssertExpectations(t)
}

// TestPublish_ResolvedAlert_CacheHit tests publishing resolved alert (reply in thread)
func TestPublish_ResolvedAlert_CacheHit(t *testing.T) {
	publisher, client, cache, formatter := setupSlackPublisher(t)
	ctx := context.Background()

	alert := createSlackTestAlert("fp123", "test-alert", core.StatusResolved)
	target := createSlackTestTarget()

	// Mock cache hit (existing firing message)
	cacheEntry := &MessageEntry{
		MessageTS: "1234567890.123456",
		ThreadTS:  "1234567890.123456",
		CreatedAt: time.Now(),
	}
	cache.On("Get", "fp123").Return(cacheEntry, true)

	// Mock client thread reply success
	slackResp := &SlackResponse{
		OK: true,
		TS: "1234567890.123457", // New reply timestamp
	}
	client.On("ReplyInThread", ctx, "1234567890.123456", mock.AnythingOfType("*publishing.SlackMessage")).Return(slackResp, nil)

	// Execute
	err := publisher.Publish(ctx, alert, target)

	// Verify
	require.NoError(t, err)
	cache.AssertExpectations(t)
	client.AssertExpectations(t)
	formatter.AssertNotCalled(t, "FormatAlert") // No formatting needed for thread reply
}

// TestPublish_ResolvedAlert_CacheMiss tests resolved alert without firing message
func TestPublish_ResolvedAlert_CacheMiss(t *testing.T) {
	publisher, client, cache, formatter := setupSlackPublisher(t)
	ctx := context.Background()

	alert := createSlackTestAlert("fp123", "test-alert", core.StatusResolved)
	target := createSlackTestTarget()

	// Mock cache miss
	cache.On("Get", "fp123").Return(nil, false)

	// Mock formatter success
	formattedPayload := map[string]any{
		"text": "Test alert resolved",
	}
	formatter.On("FormatAlert", ctx, alert, core.FormatSlack).Return(formattedPayload, nil)

	// Mock client success
	slackResp := &SlackResponse{
		OK: true,
		TS: "1234567890.123456",
	}
	client.On("PostMessage", ctx, mock.AnythingOfType("*publishing.SlackMessage")).Return(slackResp, nil)

	// Mock cache store
	cache.On("Store", "fp123", mock.AnythingOfType("*publishing.MessageEntry")).Return()

	// Execute
	err := publisher.Publish(ctx, alert, target)

	// Verify
	require.NoError(t, err)
	cache.AssertExpectations(t)
	formatter.AssertExpectations(t)
	client.AssertExpectations(t)
}

// TestPublish_StillFiring_CacheHit tests still firing alert (reply in thread)
func TestPublish_StillFiring_CacheHit(t *testing.T) {
	publisher, client, cache, _ := setupSlackPublisher(t)
	ctx := context.Background()

	alert := createSlackTestAlert("fp123", "test-alert", core.StatusFiring)
	target := createSlackTestTarget()

	// Mock cache hit (existing firing message)
	cacheEntry := &MessageEntry{
		MessageTS: "1234567890.123456",
		ThreadTS:  "1234567890.123456",
		CreatedAt: time.Now().Add(-10 * time.Minute), // Old firing alert
	}
	cache.On("Get", "fp123").Return(cacheEntry, true)

	// Mock client thread reply success
	slackResp := &SlackResponse{
		OK: true,
		TS: "1234567890.123457",
	}
	client.On("ReplyInThread", ctx, "1234567890.123456", mock.AnythingOfType("*publishing.SlackMessage")).Return(slackResp, nil)

	// Execute
	err := publisher.Publish(ctx, alert, target)

	// Verify
	require.NoError(t, err)
	cache.AssertExpectations(t)
	client.AssertExpectations(t)
}

// TestPublish_FormatterError tests formatter error handling
func TestPublish_FormatterError(t *testing.T) {
	publisher, _, cache, formatter := setupSlackPublisher(t)
	ctx := context.Background()

	alert := createSlackTestAlert("fp123", "test-alert", core.StatusFiring)
	target := createSlackTestTarget()

	// Mock cache miss
	cache.On("Get", "fp123").Return(nil, false)

	// Mock formatter error
	formatter.On("FormatAlert", ctx, alert, core.FormatSlack).Return(nil, errors.New("formatter error"))

	// Execute
	err := publisher.Publish(ctx, alert, target)

	// Verify
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to format alert")
	cache.AssertExpectations(t)
	formatter.AssertExpectations(t)
}

// TestPublish_ClientError tests Slack API client error handling
func TestPublish_ClientError(t *testing.T) {
	publisher, client, cache, formatter := setupSlackPublisher(t)
	ctx := context.Background()

	alert := createSlackTestAlert("fp123", "test-alert", core.StatusFiring)
	target := createSlackTestTarget()

	// Mock cache miss
	cache.On("Get", "fp123").Return(nil, false)

	// Mock formatter success
	formattedPayload := map[string]any{"text": "Test"}
	formatter.On("FormatAlert", ctx, alert, core.FormatSlack).Return(formattedPayload, nil)

	// Mock client error
	client.On("PostMessage", ctx, mock.AnythingOfType("*publishing.SlackMessage")).Return(nil, errors.New("slack API error"))

	// Execute
	err := publisher.Publish(ctx, alert, target)

	// Verify
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to post message")
	cache.AssertExpectations(t)
	formatter.AssertExpectations(t)
	client.AssertExpectations(t)
}

// TestPublish_ThreadReplyError tests thread reply error handling
func TestPublish_ThreadReplyError(t *testing.T) {
	publisher, client, cache, _ := setupSlackPublisher(t)
	ctx := context.Background()

	alert := createSlackTestAlert("fp123", "test-alert", core.StatusResolved)
	target := createSlackTestTarget()

	// Mock cache hit
	cacheEntry := &MessageEntry{
		MessageTS: "1234567890.123456",
		ThreadTS:  "1234567890.123456",
		CreatedAt: time.Now(),
	}
	cache.On("Get", "fp123").Return(cacheEntry, true)

	// Mock client error
	client.On("ReplyInThread", ctx, "1234567890.123456", mock.AnythingOfType("*publishing.SlackMessage")).Return(nil, errors.New("thread reply error"))

	// Execute
	err := publisher.Publish(ctx, alert, target)

	// Verify
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to reply in thread")
	cache.AssertExpectations(t)
	client.AssertExpectations(t)
}

// TestPublish_UnknownStatus tests unknown alert status handling
func TestPublish_UnknownStatus(t *testing.T) {
	publisher, _, cache, _ := setupSlackPublisher(t)
	ctx := context.Background()

	alert := createSlackTestAlert("fp123", "test-alert", core.AlertStatus("unknown"))
	target := createSlackTestTarget()

	// Mock cache miss (Publish will check cache regardless of status)
	cache.On("Get", "fp123").Return(nil, false)

	// Execute
	err := publisher.Publish(ctx, alert, target)

	// Verify
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown alert status")
	cache.AssertExpectations(t)
}

// TestName tests Name() method
func TestName(t *testing.T) {
	publisher, _, _, _ := setupSlackPublisher(t)

	name := publisher.Name()

	assert.Equal(t, "Slack", name)
}

// TestBuildMessage_WithBlocks tests buildMessage with blocks
func TestBuildMessage_WithBlocks(t *testing.T) {
	publisher, _, _, _ := setupSlackPublisher(t)

	payload := map[string]any{
		"text": "Fallback text",
		"blocks": []interface{}{
			map[string]interface{}{
				"type": "header",
				"text": map[string]interface{}{
					"type": "plain_text",
					"text": "Test Header",
				},
			},
			map[string]interface{}{
				"type": "section",
				"fields": []interface{}{
					map[string]interface{}{
						"type": "mrkdwn",
						"text": "*Field 1*",
					},
				},
			},
		},
	}

	message := publisher.buildMessage(payload)

	require.NotNil(t, message)
	assert.Equal(t, "Fallback text", message.Text)
	assert.Len(t, message.Blocks, 2)
	assert.Equal(t, "header", message.Blocks[0].Type)
	assert.Equal(t, "section", message.Blocks[1].Type)
}

// TestBuildMessage_WithAttachments tests buildMessage with attachments
func TestBuildMessage_WithAttachments(t *testing.T) {
	publisher, _, _, _ := setupSlackPublisher(t)

	payload := map[string]any{
		"text": "Test text",
		"attachments": []interface{}{
			map[string]interface{}{
				"color": "#FF0000",
				"text":  "Attachment text",
			},
		},
	}

	message := publisher.buildMessage(payload)

	require.NotNil(t, message)
	assert.Equal(t, "Test text", message.Text)
	assert.Len(t, message.Attachments, 1)
	assert.Equal(t, "#FF0000", message.Attachments[0].Color)
	assert.Equal(t, "Attachment text", message.Attachments[0].Text)
}

// TestBuildMessage_EmptyPayload tests buildMessage with empty payload
func TestBuildMessage_EmptyPayload(t *testing.T) {
	publisher, _, _, _ := setupSlackPublisher(t)

	payload := map[string]any{}

	message := publisher.buildMessage(payload)

	require.NotNil(t, message)
	assert.Empty(t, message.Text)
	assert.Empty(t, message.Blocks)
	assert.Empty(t, message.Attachments)
}

// TestClassifySlackError tests error classification for metrics
func TestClassifySlackError(t *testing.T) {
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
			err:      &SlackAPIError{StatusCode: 429, ErrorMessage: "rate_limited"},
			expected: "rate_limit",
		},
		{
			name:     "server error",
			err:      &SlackAPIError{StatusCode: 503, ErrorMessage: "service_unavailable"},
			expected: "server_error",
		},
		{
			name:     "auth error",
			err:      &SlackAPIError{StatusCode: 403, ErrorMessage: "forbidden"},
			expected: "auth_error",
		},
		{
			name:     "bad request error",
			err:      &SlackAPIError{StatusCode: 400, ErrorMessage: "invalid_payload"},
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
			result := classifySlackError(tt.err)
			assert.Equal(t, tt.expected, result)
		})
	}
}
