package publishing

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/infrastructure/k8s"
	"github.com/ipiton/AMP/pkg/metrics"
)

// Test helpers

// createTestK8sClient creates fake K8s client for testing.
func createTestK8sClient(secrets ...corev1.Secret) (k8s.K8sClient, error) {
	fakeClientset := fake.NewSimpleClientset()

	// Add secrets to fake clientset
	for _, secret := range secrets {
		_, err := fakeClientset.CoreV1().Secrets(secret.Namespace).Create(
			context.Background(),
			&secret,
			metav1.CreateOptions{},
		)
		if err != nil {
			return nil, err
		}
	}

	// Wrap fake clientset in K8sClient interface
	// Note: We need a test adapter since K8sClient expects specific interface
	// For this test, we'll use a mock that satisfies the interface
	return &mockK8sClient{
		clientset: fakeClientset,
		namespace: "default",
	}, nil
}

// mockK8sClient implements k8s.K8sClient interface for testing.
type mockK8sClient struct {
	clientset *fake.Clientset
	namespace string
}

func (m *mockK8sClient) ListSecrets(ctx context.Context, namespace string, labelSelector string) ([]corev1.Secret, error) {
	secretList, err := m.clientset.CoreV1().Secrets(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return nil, err
	}
	return secretList.Items, nil
}

func (m *mockK8sClient) GetSecret(ctx context.Context, namespace, name string) (*corev1.Secret, error) {
	return m.clientset.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
}

func (m *mockK8sClient) Health(ctx context.Context) error {
	return nil // Always healthy in tests
}

func (m *mockK8sClient) Close() error {
	return nil
}

// createValidTestSecret creates valid K8s secret with publishing target config.
func createValidTestSecret(name, namespace, targetType string) corev1.Secret {
	target := core.PublishingTarget{
		Name:    name,
		Type:    targetType,
		URL:     "https://example.com/webhook",
		Format:  core.PublishingFormat(targetType),
		Enabled: true,
		Headers: map[string]string{
			"Authorization": "Bearer token",
		},
	}

	configJSON, _ := json.Marshal(target)
	configBase64 := base64.StdEncoding.EncodeToString(configJSON)

	return corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				"publishing-target": "true",
			},
		},
		Data: map[string][]byte{
			"config": []byte(configBase64),
		},
	}
}

// createInvalidTestSecret creates K8s secret with invalid config.
func createInvalidTestSecret(name, namespace, reason string) corev1.Secret {
	var data map[string][]byte

	switch reason {
	case "missing_config":
		data = map[string][]byte{} // No 'config' field
	case "invalid_base64":
		data = map[string][]byte{
			"config": []byte("not-valid-base64!!!"),
		}
	case "invalid_json":
		data = map[string][]byte{
			"config": []byte(base64.StdEncoding.EncodeToString([]byte("{invalid json"))),
		}
	default:
		data = map[string][]byte{
			"config": []byte(""),
		}
	}

	return corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				"publishing-target": "true",
			},
		},
		Data: data,
	}
}

// Test Suite: Discovery

func TestNewTargetDiscoveryManager_Success(t *testing.T) {
	k8sClient, err := createTestK8sClient()
	require.NoError(t, err)

	manager, err := NewTargetDiscoveryManager(
		k8sClient,
		"default",
		"publishing-target=true",
		slog.New(slog.NewTextHandler(os.Stdout, nil)),
		nil, // no metrics
	)

	assert.NoError(t, err)
	assert.NotNil(t, manager)
}

func TestNewTargetDiscoveryManager_NilK8sClient(t *testing.T) {
	manager, err := NewTargetDiscoveryManager(
		nil, // nil K8s client
		"default",
		"publishing-target=true",
		nil,
		nil,
	)

	assert.Error(t, err)
	assert.Nil(t, manager)
	assert.Contains(t, err.Error(), "k8sClient is required")
}

func TestNewTargetDiscoveryManager_EmptyNamespace(t *testing.T) {
	k8sClient, _ := createTestK8sClient()

	manager, err := NewTargetDiscoveryManager(
		k8sClient,
		"", // empty namespace
		"publishing-target=true",
		nil,
		nil,
	)

	assert.Error(t, err)
	assert.Nil(t, manager)
	assert.Contains(t, err.Error(), "namespace is required")
}

func TestDiscoverTargets_Success_ValidSecrets(t *testing.T) {
	// Create 2 valid secrets
	secret1 := createValidTestSecret("rootly-prod", "default", "rootly")
	secret2 := createValidTestSecret("slack-ops", "default", "slack")

	k8sClient, err := createTestK8sClient(secret1, secret2)
	require.NoError(t, err)

	manager, err := NewTargetDiscoveryManager(k8sClient, "default", "publishing-target=true", nil, nil)
	require.NoError(t, err)

	// Discover targets
	err = manager.DiscoverTargets(context.Background())
	assert.NoError(t, err)

	// Check stats
	stats := manager.GetStats()
	assert.Equal(t, 2, stats.TotalTargets)
	assert.Equal(t, 2, stats.ValidTargets)
	assert.Equal(t, 0, stats.InvalidTargets)
	assert.NotZero(t, stats.LastDiscovery)

	// Check targets in cache
	targets := manager.ListTargets()
	assert.Len(t, targets, 2)

	// Get specific target
	rootlyTarget, err := manager.GetTarget("rootly-prod")
	assert.NoError(t, err)
	assert.Equal(t, "rootly", rootlyTarget.Type)
	assert.Equal(t, "https://example.com/webhook", rootlyTarget.URL)
}

func TestDiscoverTargets_Success_EmptyCache(t *testing.T) {
	// No secrets in cluster
	k8sClient, err := createTestK8sClient()
	require.NoError(t, err)

	manager, err := NewTargetDiscoveryManager(k8sClient, "default", "publishing-target=true", nil, nil)
	require.NoError(t, err)

	// Discover targets (should succeed with empty cache)
	err = manager.DiscoverTargets(context.Background())
	assert.NoError(t, err)

	// Check stats
	stats := manager.GetStats()
	assert.Equal(t, 0, stats.TotalTargets)
	assert.Equal(t, 0, stats.ValidTargets)

	// Check empty cache
	targets := manager.ListTargets()
	assert.Empty(t, targets)
}

func TestDiscoverTargets_PartialSuccess_MixedValidInvalid(t *testing.T) {
	// Create 1 valid + 2 invalid secrets
	validSecret := createValidTestSecret("rootly-prod", "default", "rootly")
	invalidSecret1 := createInvalidTestSecret("bad-secret-1", "default", "missing_config")
	invalidSecret2 := createInvalidTestSecret("bad-secret-2", "default", "invalid_json")

	k8sClient, err := createTestK8sClient(validSecret, invalidSecret1, invalidSecret2)
	require.NoError(t, err)

	manager, err := NewTargetDiscoveryManager(k8sClient, "default", "publishing-target=true", nil, nil)
	require.NoError(t, err)

	// Discover targets (should succeed with partial success)
	err = manager.DiscoverTargets(context.Background())
	assert.NoError(t, err)

	// Check stats (1 valid, 2 invalid)
	stats := manager.GetStats()
	assert.Equal(t, 3, stats.TotalTargets)
	assert.Equal(t, 1, stats.ValidTargets)
	assert.Equal(t, 2, stats.InvalidTargets)

	// Check cache has only valid target
	targets := manager.ListTargets()
	assert.Len(t, targets, 1)
	assert.Equal(t, "rootly-prod", targets[0].Name)
}

func TestGetTarget_Found(t *testing.T) {
	secret := createValidTestSecret("test-target", "default", "webhook")
	k8sClient, _ := createTestK8sClient(secret)

	manager, _ := NewTargetDiscoveryManager(k8sClient, "default", "publishing-target=true", nil, nil)
	_ = manager.DiscoverTargets(context.Background())

	// Get existing target
	target, err := manager.GetTarget("test-target")
	assert.NoError(t, err)
	assert.NotNil(t, target)
	assert.Equal(t, "test-target", target.Name)
}

func TestGetTarget_NotFound(t *testing.T) {
	k8sClient, _ := createTestK8sClient()
	manager, _ := NewTargetDiscoveryManager(k8sClient, "default", "publishing-target=true", nil, nil)
	_ = manager.DiscoverTargets(context.Background())

	// Get non-existent target
	target, err := manager.GetTarget("nonexistent")
	assert.Error(t, err)
	assert.Nil(t, target)

	// Check error type
	var notFoundErr *ErrTargetNotFound
	assert.ErrorAs(t, err, &notFoundErr)
	assert.Equal(t, "nonexistent", notFoundErr.TargetName)
}

func TestListTargets(t *testing.T) {
	secret1 := createValidTestSecret("target-1", "default", "rootly")
	secret2 := createValidTestSecret("target-2", "default", "slack")
	secret3 := createValidTestSecret("target-3", "default", "webhook")

	k8sClient, _ := createTestK8sClient(secret1, secret2, secret3)
	manager, _ := NewTargetDiscoveryManager(k8sClient, "default", "publishing-target=true", nil, nil)
	_ = manager.DiscoverTargets(context.Background())

	// List all targets
	targets := manager.ListTargets()
	assert.Len(t, targets, 3)

	// Check target names (order may vary)
	names := []string{targets[0].Name, targets[1].Name, targets[2].Name}
	assert.Contains(t, names, "target-1")
	assert.Contains(t, names, "target-2")
	assert.Contains(t, names, "target-3")
}

func TestGetTargetsByType(t *testing.T) {
	secret1 := createValidTestSecret("slack-1", "default", "slack")
	secret2 := createValidTestSecret("slack-2", "default", "slack")
	secret3 := createValidTestSecret("rootly-1", "default", "rootly")

	k8sClient, _ := createTestK8sClient(secret1, secret2, secret3)
	manager, _ := NewTargetDiscoveryManager(k8sClient, "default", "publishing-target=true", nil, nil)
	_ = manager.DiscoverTargets(context.Background())

	// Get Slack targets
	slackTargets := manager.GetTargetsByType("slack")
	assert.Len(t, slackTargets, 2)
	for _, target := range slackTargets {
		assert.Equal(t, "slack", target.Type)
	}

	// Get Rootly targets
	rootlyTargets := manager.GetTargetsByType("rootly")
	assert.Len(t, rootlyTargets, 1)
	assert.Equal(t, "rootly", rootlyTargets[0].Type)

	// Get non-existent type
	pdTargets := manager.GetTargetsByType("pagerduty")
	assert.Empty(t, pdTargets)
}

func TestGetStats(t *testing.T) {
	secret1 := createValidTestSecret("target-1", "default", "rootly")
	secret2 := createInvalidTestSecret("bad-target", "default", "invalid_json")

	k8sClient, _ := createTestK8sClient(secret1, secret2)
	manager, _ := NewTargetDiscoveryManager(k8sClient, "default", "publishing-target=true", nil, nil)

	// Discover targets
	startTime := time.Now()
	_ = manager.DiscoverTargets(context.Background())
	endTime := time.Now()

	// Get stats
	stats := manager.GetStats()
	assert.Equal(t, 2, stats.TotalTargets)
	assert.Equal(t, 1, stats.ValidTargets)
	assert.Equal(t, 1, stats.InvalidTargets)
	assert.True(t, stats.LastDiscovery.After(startTime))
	assert.True(t, stats.LastDiscovery.Before(endTime.Add(time.Second)))
}

func TestHealth_Healthy(t *testing.T) {
	k8sClient, _ := createTestK8sClient()
	manager, _ := NewTargetDiscoveryManager(k8sClient, "default", "publishing-target=true", nil, nil)

	err := manager.Health(context.Background())
	assert.NoError(t, err)
}

func TestDiscoverTargets_WithMetrics(t *testing.T) {
	secret := createValidTestSecret("test-target", "default", "rootly")
	k8sClient, _ := createTestK8sClient(secret)

	metricsRegistry := metrics.DefaultRegistry()
	manager, _ := NewTargetDiscoveryManager(k8sClient, "default", "publishing-target=true", nil, metricsRegistry)

	// Discover targets (should record metrics)
	err := manager.DiscoverTargets(context.Background())
	assert.NoError(t, err)

	// Metrics are recorded but we can't easily verify values without inspecting prometheus
	// This test ensures no panic when metrics are enabled
}

// Test Suite: Concurrent Access

func TestConcurrentGetAndDiscovery(t *testing.T) {
	secret1 := createValidTestSecret("target-1", "default", "rootly")
	secret2 := createValidTestSecret("target-2", "default", "slack")

	k8sClient, _ := createTestK8sClient(secret1, secret2)
	manager, _ := NewTargetDiscoveryManager(k8sClient, "default", "publishing-target=true", nil, nil)

	// Initial discovery
	_ = manager.DiscoverTargets(context.Background())

	// Run concurrent operations
	done := make(chan bool)

	// 10 readers
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				_, _ = manager.GetTarget("target-1")
				_ = manager.ListTargets()
				_ = manager.GetTargetsByType("rootly")
			}
			done <- true
		}()
	}

	// 1 writer
	go func() {
		for j := 0; j < 10; j++ {
			_ = manager.DiscoverTargets(context.Background())
			time.Sleep(10 * time.Millisecond)
		}
		done <- true
	}()

	// Wait for completion
	for i := 0; i < 11; i++ {
		<-done
	}

	// Final check - cache should be consistent
	targets := manager.ListTargets()
	assert.Len(t, targets, 2)
}
