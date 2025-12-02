package proxy

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/ipiton/AMP/cmd/server/handlers/proxy"
	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/infrastructure/publishing"
)

// Mock implementations
type MockAlertProcessor struct {
	mock.Mock
}

func (m *MockAlertProcessor) ProcessAlert(ctx context.Context, alert *core.Alert) error {
	args := m.Called(ctx, alert)
	return args.Error(0)
}

func (m *MockAlertProcessor) Health(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

type MockClassificationService struct {
	mock.Mock
}

func (m *MockClassificationService) ClassifyAlert(ctx context.Context, alert *core.Alert) (*core.ClassificationResult, error) {
	args := m.Called(ctx, alert)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*core.ClassificationResult), args.Error(1)
}

func (m *MockClassificationService) Health(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

type MockFilterEngine struct {
	mock.Mock
}

func (m *MockFilterEngine) ShouldBlock(alert *core.Alert, classification *core.ClassificationResult) (bool, string) {
	args := m.Called(alert, classification)
	return args.Bool(0), args.String(1)
}

type MockTargetDiscoveryManager struct {
	mock.Mock
}

func (m *MockTargetDiscoveryManager) DiscoverTargets(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

func (m *MockTargetDiscoveryManager) GetTarget(name string) (*core.PublishingTarget, error) {
	args := m.Called(name)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*core.PublishingTarget), args.Error(1)
}

func (m *MockTargetDiscoveryManager) ListTargets() []*core.PublishingTarget {
	args := m.Called()
	if args.Get(0) == nil {
		return []*core.PublishingTarget{}
	}
	return args.Get(0).([]*core.PublishingTarget)
}

func (m *MockTargetDiscoveryManager) GetTargetsByType(targetType string) []*core.PublishingTarget {
	args := m.Called(targetType)
	if args.Get(0) == nil {
		return []*core.PublishingTarget{}
	}
	return args.Get(0).([]*core.PublishingTarget)
}

func (m *MockTargetDiscoveryManager) GetTargetCount() int {
	args := m.Called()
	return args.Int(0)
}

type MockParallelPublisher struct {
	mock.Mock
}

func (m *MockParallelPublisher) PublishToMultiple(ctx context.Context, alert *core.EnrichedAlert, targets []*core.PublishingTarget) (*publishing.ParallelPublishResult, error) {
	args := m.Called(ctx, alert, targets)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*publishing.ParallelPublishResult), args.Error(1)
}

func (m *MockParallelPublisher) PublishToAll(ctx context.Context, alert *core.EnrichedAlert) (*publishing.ParallelPublishResult, error) {
	args := m.Called(ctx, alert)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*publishing.ParallelPublishResult), args.Error(1)
}

func (m *MockParallelPublisher) PublishToHealthy(ctx context.Context, alert *core.EnrichedAlert) (*publishing.ParallelPublishResult, error) {
	args := m.Called(ctx, alert)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*publishing.ParallelPublishResult), args.Error(1)
}

// TestNewProxyWebhookService tests service creation
func TestNewProxyWebhookService(t *testing.T) {
	cfg := ServiceConfig{
		AlertProcessor:    new(MockAlertProcessor),
		ClassificationSvc: new(MockClassificationService),
		FilterEngine:      new(MockFilterEngine),
		TargetManager:     new(MockTargetDiscoveryManager),
		ParallelPublisher: new(MockParallelPublisher),
		Config:            proxy.DefaultProxyWebhookConfig(),
		Logger:            slog.Default(),
	}

	svc, err := NewProxyWebhookService(cfg)

	require.NoError(t, err)
	require.NotNil(t, svc)
	assert.NotNil(t, svc.alertProcessor)
	assert.NotNil(t, svc.classificationSvc)
	assert.NotNil(t, svc.filterEngine)
	assert.NotNil(t, svc.targetManager)
	assert.NotNil(t, svc.parallelPublisher)
}

// TestNewProxyWebhookService_MissingDependencies tests service creation with missing dependencies
func TestNewProxyWebhookService_MissingDependencies(t *testing.T) {
	tests := []struct {
		name        string
		config      ServiceConfig
		expectedErr string
	}{
		{
			name: "missing alert processor",
			config: ServiceConfig{
				ClassificationSvc: new(MockClassificationService),
				FilterEngine:      new(MockFilterEngine),
				TargetManager:     new(MockTargetDiscoveryManager),
				ParallelPublisher: new(MockParallelPublisher),
				Config:            proxy.DefaultProxyWebhookConfig(),
			},
			expectedErr: "alert processor is required",
		},
		{
			name: "missing classification service",
			config: ServiceConfig{
				AlertProcessor:    new(MockAlertProcessor),
				FilterEngine:      new(MockFilterEngine),
				TargetManager:     new(MockTargetDiscoveryManager),
				ParallelPublisher: new(MockParallelPublisher),
				Config:            proxy.DefaultProxyWebhookConfig(),
			},
			expectedErr: "classification service is required",
		},
		{
			name: "missing filter engine",
			config: ServiceConfig{
				AlertProcessor:    new(MockAlertProcessor),
				ClassificationSvc: new(MockClassificationService),
				TargetManager:     new(MockTargetDiscoveryManager),
				ParallelPublisher: new(MockParallelPublisher),
				Config:            proxy.DefaultProxyWebhookConfig(),
			},
			expectedErr: "filter engine is required",
		},
		{
			name: "missing target manager",
			config: ServiceConfig{
				AlertProcessor:    new(MockAlertProcessor),
				ClassificationSvc: new(MockClassificationService),
				FilterEngine:      new(MockFilterEngine),
				ParallelPublisher: new(MockParallelPublisher),
				Config:            proxy.DefaultProxyWebhookConfig(),
			},
			expectedErr: "target manager is required",
		},
		{
			name: "missing parallel publisher",
			config: ServiceConfig{
				AlertProcessor:    new(MockAlertProcessor),
				ClassificationSvc: new(MockClassificationService),
				FilterEngine:      new(MockFilterEngine),
				TargetManager:     new(MockTargetDiscoveryManager),
				Config:            proxy.DefaultProxyWebhookConfig(),
			},
			expectedErr: "parallel publisher is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, err := NewProxyWebhookService(tt.config)

			assert.Error(t, err)
			assert.Nil(t, svc)
			assert.Contains(t, err.Error(), tt.expectedErr)
		})
	}
}

// TestProxyWebhookService_ProcessWebhook_Success tests successful webhook processing
func TestProxyWebhookService_ProcessWebhook_Success(t *testing.T) {
	// Setup mocks
	mockAlertProc := new(MockAlertProcessor)
	mockClassSvc := new(MockClassificationService)
	mockFilter := new(MockFilterEngine)
	mockTargets := new(MockTargetDiscoveryManager)
	mockPublisher := new(MockParallelPublisher)

	cfg := ServiceConfig{
		AlertProcessor:    mockAlertProc,
		ClassificationSvc: mockClassSvc,
		FilterEngine:      mockFilter,
		TargetManager:     mockTargets,
		ParallelPublisher: mockPublisher,
		Config:            proxy.DefaultProxyWebhookConfig(),
		Logger:            slog.Default(),
	}

	svc, err := NewProxyWebhookService(cfg)
	require.NoError(t, err)

	// Create test request
	req := &proxy.ProxyWebhookRequest{
		Receiver: "webhook-receiver",
		Status:   "firing",
		Alerts: []proxy.AlertPayload{
			{
				Status: "firing",
				Labels: map[string]string{
					"alertname": "TestAlert",
					"severity":  "warning",
				},
				StartsAt: time.Now(),
			},
		},
	}

	// Setup mock expectations
	mockAlertProc.On("ProcessAlert", mock.Anything, mock.Anything).Return(nil)

	mockClassSvc.On("ClassifyAlert", mock.Anything, mock.Anything).Return(&core.ClassificationResult{
		Severity:   core.SeverityWarning,
		Category:   "test",
		Confidence: 0.9,
	}, nil)

	mockFilter.On("ShouldBlock", mock.Anything, mock.Anything).Return(false, "")

	mockTargets.On("ListTargets").Return([]*core.PublishingTarget{
		{
			Name:    "test-target",
			Type:    "slack",
			Enabled: true,
			URL:     "https://slack.com/webhook",
		},
	})

	mockPublisher.On("PublishToMultiple", mock.Anything, mock.Anything, mock.Anything).Return(&publishing.ParallelPublishResult{
		SuccessCount: 1,
		FailureCount: 0,
		TotalTargets: 1,
		Results: []publishing.TargetPublishResult{
			{
				TargetName: "test-target",
				TargetType: "slack",
				Success:    true,
				StatusCode: 200,
				Duration:   100 * time.Millisecond,
			},
		},
	}, nil)

	// Execute
	ctx := context.Background()
	resp, err := svc.ProcessWebhook(ctx, req)

	// Assert
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "success", resp.Status)
	assert.Equal(t, 1, resp.AlertsSummary.TotalReceived)
	assert.Equal(t, 1, resp.AlertsSummary.TotalProcessed)
	assert.Equal(t, 1, resp.AlertsSummary.TotalPublished)

	mockAlertProc.AssertExpectations(t)
	mockClassSvc.AssertExpectations(t)
	mockFilter.AssertExpectations(t)
	mockTargets.AssertExpectations(t)
	mockPublisher.AssertExpectations(t)
}

// TestProxyWebhookService_ProcessWebhook_Filtered tests alert filtering
func TestProxyWebhookService_ProcessWebhook_Filtered(t *testing.T) {
	mockAlertProc := new(MockAlertProcessor)
	mockClassSvc := new(MockClassificationService)
	mockFilter := new(MockFilterEngine)
	mockTargets := new(MockTargetDiscoveryManager)
	mockPublisher := new(MockParallelPublisher)

	cfg := ServiceConfig{
		AlertProcessor:    mockAlertProc,
		ClassificationSvc: mockClassSvc,
		FilterEngine:      mockFilter,
		TargetManager:     mockTargets,
		ParallelPublisher: mockPublisher,
		Config:            proxy.DefaultProxyWebhookConfig(),
		Logger:            slog.Default(),
	}

	svc, err := NewProxyWebhookService(cfg)
	require.NoError(t, err)

	req := &proxy.ProxyWebhookRequest{
		Receiver: "webhook-receiver",
		Status:   "firing",
		Alerts: []proxy.AlertPayload{
			{
				Status: "firing",
				Labels: map[string]string{
					"alertname": "NoiseAlert",
				},
				StartsAt: time.Now(),
			},
		},
	}

	mockAlertProc.On("ProcessAlert", mock.Anything, mock.Anything).Return(nil)

	mockClassSvc.On("ClassifyAlert", mock.Anything, mock.Anything).Return(&core.ClassificationResult{
		Severity:   core.SeverityInfo,
		Category:   "noise",
		Confidence: 0.3,
	}, nil)

	// Filter blocks the alert
	mockFilter.On("ShouldBlock", mock.Anything, mock.Anything).Return(true, "noise")

	ctx := context.Background()
	resp, err := svc.ProcessWebhook(ctx, req)

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, 1, resp.AlertsSummary.TotalReceived)
	assert.Equal(t, 1, resp.AlertsSummary.TotalFiltered)
	assert.Equal(t, 0, resp.AlertsSummary.TotalPublished)

	mockAlertProc.AssertExpectations(t)
	mockClassSvc.AssertExpectations(t)
	mockFilter.AssertExpectations(t)
}

// TestProxyWebhookService_ProcessWebhook_ClassificationFallback tests classification fallback
func TestProxyWebhookService_ProcessWebhook_ClassificationFallback(t *testing.T) {
	mockAlertProc := new(MockAlertProcessor)
	mockClassSvc := new(MockClassificationService)
	mockFilter := new(MockFilterEngine)
	mockTargets := new(MockTargetDiscoveryManager)
	mockPublisher := new(MockParallelPublisher)

	cfg := ServiceConfig{
		AlertProcessor:    mockAlertProc,
		ClassificationSvc: mockClassSvc,
		FilterEngine:      mockFilter,
		TargetManager:     mockTargets,
		ParallelPublisher: mockPublisher,
		Config:            proxy.DefaultProxyWebhookConfig(),
		Logger:            slog.Default(),
	}

	svc, err := NewProxyWebhookService(cfg)
	require.NoError(t, err)

	req := &proxy.ProxyWebhookRequest{
		Receiver: "webhook-receiver",
		Status:   "firing",
		Alerts: []proxy.AlertPayload{
			{
				Status: "firing",
				Labels: map[string]string{
					"alertname": "TestAlert",
					"severity":  "critical", // Will be used for fallback
				},
				StartsAt: time.Now(),
			},
		},
	}

	mockAlertProc.On("ProcessAlert", mock.Anything, mock.Anything).Return(nil)

	// Classification fails
	mockClassSvc.On("ClassifyAlert", mock.Anything, mock.Anything).Return(nil, errors.New("LLM unavailable"))

	mockFilter.On("ShouldBlock", mock.Anything, mock.Anything).Return(false, "")

	mockTargets.On("ListTargets").Return([]*core.PublishingTarget{})

	ctx := context.Background()
	resp, err := svc.ProcessWebhook(ctx, req)

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, 1, resp.AlertsSummary.TotalReceived)
	assert.Equal(t, 1, resp.AlertsSummary.TotalClassified) // Fallback classification counted

	// Verify fallback classification was used (severity from labels)
	assert.Len(t, resp.AlertResults, 1)
	if len(resp.AlertResults) > 0 && resp.AlertResults[0].Classification != nil {
		assert.Equal(t, "critical", resp.AlertResults[0].Classification.Severity)
		assert.Equal(t, "default", resp.AlertResults[0].Classification.Source)
	}

	mockAlertProc.AssertExpectations(t)
	mockClassSvc.AssertExpectations(t)
}

// TestProxyWebhookService_ProcessWebhook_PartialSuccess tests partial publishing success
func TestProxyWebhookService_ProcessWebhook_PartialSuccess(t *testing.T) {
	mockAlertProc := new(MockAlertProcessor)
	mockClassSvc := new(MockClassificationService)
	mockFilter := new(MockFilterEngine)
	mockTargets := new(MockTargetDiscoveryManager)
	mockPublisher := new(MockParallelPublisher)

	cfg := ServiceConfig{
		AlertProcessor:    mockAlertProc,
		ClassificationSvc: mockClassSvc,
		FilterEngine:      mockFilter,
		TargetManager:     mockTargets,
		ParallelPublisher: mockPublisher,
		Config:            proxy.DefaultProxyWebhookConfig(),
		Logger:            slog.Default(),
	}

	svc, err := NewProxyWebhookService(cfg)
	require.NoError(t, err)

	req := &proxy.ProxyWebhookRequest{
		Receiver: "webhook-receiver",
		Status:   "firing",
		Alerts: []proxy.AlertPayload{
			{
				Status: "firing",
				Labels: map[string]string{
					"alertname": "TestAlert",
				},
				StartsAt: time.Now(),
			},
		},
	}

	mockAlertProc.On("ProcessAlert", mock.Anything, mock.Anything).Return(nil)
	mockClassSvc.On("ClassifyAlert", mock.Anything, mock.Anything).Return(&core.ClassificationResult{
		Severity:   core.SeverityWarning,
		Confidence: 0.8,
	}, nil)
	mockFilter.On("ShouldBlock", mock.Anything, mock.Anything).Return(false, "")

	mockTargets.On("ListTargets").Return([]*core.PublishingTarget{
		{Name: "target1", Type: "slack", Enabled: true},
		{Name: "target2", Type: "pagerduty", Enabled: true},
	})

	// Partial success: 1 success, 1 failure
	mockPublisher.On("PublishToMultiple", mock.Anything, mock.Anything, mock.Anything).Return(&publishing.ParallelPublishResult{
		SuccessCount: 1,
		FailureCount: 1,
		TotalTargets: 2,
		Results: []publishing.TargetPublishResult{
			{TargetName: "target1", Success: true, StatusCode: 200},
			{TargetName: "target2", Success: false, ErrorMessage: "timeout"},
		},
	}, nil)

	ctx := context.Background()
	resp, err := svc.ProcessWebhook(ctx, req)

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "partial", resp.Status)
	assert.Equal(t, 1, resp.PublishingSummary.SuccessfulTargets)
	assert.Equal(t, 1, resp.PublishingSummary.FailedTargets)

	mockPublisher.AssertExpectations(t)
}

// TestProxyWebhookService_ProcessWebhook_NoTargets tests webhook processing with no targets
func TestProxyWebhookService_ProcessWebhook_NoTargets(t *testing.T) {
	mockAlertProc := new(MockAlertProcessor)
	mockClassSvc := new(MockClassificationService)
	mockFilter := new(MockFilterEngine)
	mockTargets := new(MockTargetDiscoveryManager)
	mockPublisher := new(MockParallelPublisher)

	cfg := ServiceConfig{
		AlertProcessor:    mockAlertProc,
		ClassificationSvc: mockClassSvc,
		FilterEngine:      mockFilter,
		TargetManager:     mockTargets,
		ParallelPublisher: mockPublisher,
		Config:            proxy.DefaultProxyWebhookConfig(),
		Logger:            slog.Default(),
	}

	svc, err := NewProxyWebhookService(cfg)
	require.NoError(t, err)

	req := &proxy.ProxyWebhookRequest{
		Receiver: "webhook-receiver",
		Status:   "firing",
		Alerts: []proxy.AlertPayload{
			{
				Status: "firing",
				Labels: map[string]string{"alertname": "TestAlert"},
				StartsAt: time.Now(),
			},
		},
	}

	mockAlertProc.On("ProcessAlert", mock.Anything, mock.Anything).Return(nil)
	mockClassSvc.On("ClassifyAlert", mock.Anything, mock.Anything).Return(&core.ClassificationResult{
		Severity:   core.SeverityWarning,
		Confidence: 0.8,
	}, nil)
	mockFilter.On("ShouldBlock", mock.Anything, mock.Anything).Return(false, "")

	// No targets available
	mockTargets.On("ListTargets").Return([]*core.PublishingTarget{})

	ctx := context.Background()
	resp, err := svc.ProcessWebhook(ctx, req)

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "success", resp.Status)
	assert.Equal(t, 0, resp.PublishingSummary.TotalTargets)

	mockTargets.AssertExpectations(t)
}

// TestProxyWebhookService_ProcessWebhook_MultipleAlerts tests batch processing
func TestProxyWebhookService_ProcessWebhook_MultipleAlerts(t *testing.T) {
	mockAlertProc := new(MockAlertProcessor)
	mockClassSvc := new(MockClassificationService)
	mockFilter := new(MockFilterEngine)
	mockTargets := new(MockTargetDiscoveryManager)
	mockPublisher := new(MockParallelPublisher)

	cfg := ServiceConfig{
		AlertProcessor:    mockAlertProc,
		ClassificationSvc: mockClassSvc,
		FilterEngine:      mockFilter,
		TargetManager:     mockTargets,
		ParallelPublisher: mockPublisher,
		Config:            proxy.DefaultProxyWebhookConfig(),
		Logger:            slog.Default(),
	}

	svc, err := NewProxyWebhookService(cfg)
	require.NoError(t, err)

	req := &proxy.ProxyWebhookRequest{
		Receiver: "webhook-receiver",
		Status:   "firing",
		Alerts: []proxy.AlertPayload{
			{
				Status: "firing",
				Labels: map[string]string{"alertname": "Alert1"},
				StartsAt: time.Now(),
			},
			{
				Status: "firing",
				Labels: map[string]string{"alertname": "Alert2"},
				StartsAt: time.Now(),
			},
			{
				Status: "firing",
				Labels: map[string]string{"alertname": "Alert3"},
				StartsAt: time.Now(),
			},
		},
	}

	mockAlertProc.On("ProcessAlert", mock.Anything, mock.Anything).Return(nil).Times(3)
	mockClassSvc.On("ClassifyAlert", mock.Anything, mock.Anything).Return(&core.ClassificationResult{
		Severity:   core.SeverityWarning,
		Confidence: 0.8,
	}, nil).Times(3)
	mockFilter.On("ShouldBlock", mock.Anything, mock.Anything).Return(false, "").Times(3)
	mockTargets.On("ListTargets").Return([]*core.PublishingTarget{}).Times(3)

	ctx := context.Background()
	resp, err := svc.ProcessWebhook(ctx, req)

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, 3, resp.AlertsSummary.TotalReceived)
	assert.Equal(t, 3, resp.AlertsSummary.TotalProcessed)

	mockAlertProc.AssertExpectations(t)
	mockClassSvc.AssertExpectations(t)
	mockFilter.AssertExpectations(t)
}

// TestProxyWebhookService_Health tests health checking
func TestProxyWebhookService_Health(t *testing.T) {
	mockAlertProc := new(MockAlertProcessor)
	mockClassSvc := new(MockClassificationService)
	mockFilter := new(MockFilterEngine)
	mockTargets := new(MockTargetDiscoveryManager)
	mockPublisher := new(MockParallelPublisher)

	cfg := ServiceConfig{
		AlertProcessor:    mockAlertProc,
		ClassificationSvc: mockClassSvc,
		FilterEngine:      mockFilter,
		TargetManager:     mockTargets,
		ParallelPublisher: mockPublisher,
		Config:            proxy.DefaultProxyWebhookConfig(),
		Logger:            slog.Default(),
	}

	svc, err := NewProxyWebhookService(cfg)
	require.NoError(t, err)

	tests := []struct {
		name        string
		setupMocks  func()
		expectError bool
	}{
		{
			name: "all healthy",
			setupMocks: func() {
				mockAlertProc.On("Health", mock.Anything).Return(nil)
				mockClassSvc.On("Health", mock.Anything).Return(nil)
				mockTargets.On("GetTargetCount").Return(5)
			},
			expectError: false,
		},
		{
			name: "alert processor unhealthy",
			setupMocks: func() {
				mockAlertProc.On("Health", mock.Anything).Return(errors.New("database unavailable"))
			},
			expectError: true,
		},
		{
			name: "classification service unhealthy",
			setupMocks: func() {
				mockAlertProc.On("Health", mock.Anything).Return(nil)
				mockClassSvc.On("Health", mock.Anything).Return(errors.New("LLM unavailable"))
			},
			expectError: true,
		},
		{
			name: "no targets (non-critical)",
			setupMocks: func() {
				mockAlertProc.On("Health", mock.Anything).Return(nil)
				mockClassSvc.On("Health", mock.Anything).Return(nil)
				mockTargets.On("GetTargetCount").Return(0)
			},
			expectError: false, // No targets is non-critical
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset mocks
			mockAlertProc.ExpectedCalls = nil
			mockClassSvc.ExpectedCalls = nil
			mockTargets.ExpectedCalls = nil

			tt.setupMocks()

			ctx := context.Background()
			err := svc.Health(ctx)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestProxyWebhookService_GetStats tests statistics retrieval
func TestProxyWebhookService_GetStats(t *testing.T) {
	mockAlertProc := new(MockAlertProcessor)
	mockClassSvc := new(MockClassificationService)
	mockFilter := new(MockFilterEngine)
	mockTargets := new(MockTargetDiscoveryManager)
	mockPublisher := new(MockParallelPublisher)

	cfg := ServiceConfig{
		AlertProcessor:    mockAlertProc,
		ClassificationSvc: mockClassSvc,
		FilterEngine:      mockFilter,
		TargetManager:     mockTargets,
		ParallelPublisher: mockPublisher,
		Config:            proxy.DefaultProxyWebhookConfig(),
		Logger:            slog.Default(),
	}

	svc, err := NewProxyWebhookService(cfg)
	require.NoError(t, err)

	// Initial stats
	stats := svc.GetStats()
	assert.Equal(t, int64(0), stats.TotalRequests)
	assert.Equal(t, int64(0), stats.TotalAlertsReceived)

	// Process a webhook
	req := &proxy.ProxyWebhookRequest{
		Receiver: "webhook-receiver",
		Status:   "firing",
		Alerts: []proxy.AlertPayload{
			{
				Status: "firing",
				Labels: map[string]string{"alertname": "TestAlert"},
				StartsAt: time.Now(),
			},
		},
	}

	mockAlertProc.On("ProcessAlert", mock.Anything, mock.Anything).Return(nil)
	mockClassSvc.On("ClassifyAlert", mock.Anything, mock.Anything).Return(&core.ClassificationResult{
		Severity: core.SeverityWarning,
	}, nil)
	mockFilter.On("ShouldBlock", mock.Anything, mock.Anything).Return(false, "")
	mockTargets.On("ListTargets").Return([]*core.PublishingTarget{})

	ctx := context.Background()
	_, _ = svc.ProcessWebhook(ctx, req)

	// Updated stats
	stats = svc.GetStats()
	assert.Equal(t, int64(1), stats.TotalRequests)
	assert.Equal(t, int64(1), stats.TotalAlertsReceived)
	assert.False(t, stats.LastProcessedAt.IsZero())
}

// BenchmarkProxyWebhookService_ProcessWebhook benchmarks full webhook processing
func BenchmarkProxyWebhookService_ProcessWebhook(b *testing.B) {
	mockAlertProc := new(MockAlertProcessor)
	mockClassSvc := new(MockClassificationService)
	mockFilter := new(MockFilterEngine)
	mockTargets := new(MockTargetDiscoveryManager)
	mockPublisher := new(MockParallelPublisher)

	cfg := ServiceConfig{
		AlertProcessor:    mockAlertProc,
		ClassificationSvc: mockClassSvc,
		FilterEngine:      mockFilter,
		TargetManager:     mockTargets,
		ParallelPublisher: mockPublisher,
		Config:            proxy.DefaultProxyWebhookConfig(),
		Logger:            slog.Default(),
	}

	svc, _ := NewProxyWebhookService(cfg)

	req := &proxy.ProxyWebhookRequest{
		Receiver: "webhook-receiver",
		Status:   "firing",
		Alerts: []proxy.AlertPayload{
			{
				Status: "firing",
				Labels: map[string]string{"alertname": "TestAlert"},
				StartsAt: time.Now(),
			},
		},
	}

	mockAlertProc.On("ProcessAlert", mock.Anything, mock.Anything).Return(nil)
	mockClassSvc.On("ClassifyAlert", mock.Anything, mock.Anything).Return(&core.ClassificationResult{
		Severity: core.SeverityWarning,
	}, nil)
	mockFilter.On("ShouldBlock", mock.Anything, mock.Anything).Return(false, "")
	mockTargets.On("ListTargets").Return([]*core.PublishingTarget{})

	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = svc.ProcessWebhook(ctx, req)
	}
}

// BenchmarkProxyWebhookService_ProcessWebhook_Batch benchmarks batch processing
func BenchmarkProxyWebhookService_ProcessWebhook_Batch(b *testing.B) {
	mockAlertProc := new(MockAlertProcessor)
	mockClassSvc := new(MockClassificationService)
	mockFilter := new(MockFilterEngine)
	mockTargets := new(MockTargetDiscoveryManager)
	mockPublisher := new(MockParallelPublisher)

	cfg := ServiceConfig{
		AlertProcessor:    mockAlertProc,
		ClassificationSvc: mockClassSvc,
		FilterEngine:      mockFilter,
		TargetManager:     mockTargets,
		ParallelPublisher: mockPublisher,
		Config:            proxy.DefaultProxyWebhookConfig(),
		Logger:            slog.Default(),
	}

	svc, _ := NewProxyWebhookService(cfg)

	// Create batch of 10 alerts
	alerts := make([]proxy.AlertPayload, 10)
	for i := range alerts {
		alerts[i] = proxy.AlertPayload{
			Status: "firing",
			Labels: map[string]string{"alertname": "TestAlert"},
			StartsAt: time.Now(),
		}
	}

	req := &proxy.ProxyWebhookRequest{
		Receiver: "webhook-receiver",
		Status:   "firing",
		Alerts:   alerts,
	}

	mockAlertProc.On("ProcessAlert", mock.Anything, mock.Anything).Return(nil)
	mockClassSvc.On("ClassifyAlert", mock.Anything, mock.Anything).Return(&core.ClassificationResult{
		Severity: core.SeverityWarning,
	}, nil)
	mockFilter.On("ShouldBlock", mock.Anything, mock.Anything).Return(false, "")
	mockTargets.On("ListTargets").Return([]*core.PublishingTarget{})

	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = svc.ProcessWebhook(ctx, req)
	}
}
