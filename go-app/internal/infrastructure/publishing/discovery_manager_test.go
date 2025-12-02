package publishing

import (
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/ipiton/AMP/internal/core"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func createTestSecret(name, namespace string, data map[string]string) *corev1.Secret {
	secretData := make(map[string][]byte)
	for k, v := range data {
		secretData[k] = []byte(v)
	}

	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				"publishing-target": "true",
			},
		},
		Data: secretData,
	}
}

func TestDefaultTargetDiscoveryConfig(t *testing.T) {
	config := DefaultTargetDiscoveryConfig()

	assert.Equal(t, "default", config.Namespace)
	assert.Equal(t, "publishing-target=true", config.LabelSelector)
	assert.NotNil(t, config.Logger)
}

func TestParseTargetType(t *testing.T) {
	tests := []struct {
		input    string
		expected TargetType
	}{
		{"rootly", TargetTypeRootly},
		{"pagerduty", TargetTypePagerDuty},
		{"pager_duty", TargetTypePagerDuty},
		{"slack", TargetTypeSlack},
		{"webhook", TargetTypeWebhook},
		{"alertmanager", TargetTypeAlertmanager},
		{"unknown", TargetTypeWebhook}, // Default
	}

	for _, tt := range tests {
		result := ParseTargetType(tt.input)
		assert.Equal(t, tt.expected, result, "Input: %s", tt.input)
	}
}

func TestParseSecretToTarget_Success(t *testing.T) {
	secret := createTestSecret("test-rootly", "default", map[string]string{
		"name":    "rootly-prod",
		"type":    "rootly",
		"url":     "https://api.rootly.com/v1/incidents",
		"enabled": "true",
		"api_key": "test-key-123",
	})

	// Create manager just for testing parseSecretToTarget
	config := DefaultTargetDiscoveryConfig()
	manager := &DefaultTargetDiscoveryManager{
		config: config,
		logger: slog.Default(),
	}

	target, err := manager.parseSecretToTarget(secret)

	require.NoError(t, err)
	assert.Equal(t, "rootly-prod", target.Name)
	assert.Equal(t, "rootly", target.Type)
	assert.Equal(t, "https://api.rootly.com/v1/incidents", target.URL)
	assert.True(t, target.Enabled)
	assert.Contains(t, target.Headers, "Authorization")
	assert.Contains(t, target.Headers["Authorization"], "test-key-123")
}

func TestParseSecretToTarget_MissingType(t *testing.T) {
	secret := createTestSecret("test-invalid", "default", map[string]string{
		"name": "invalid-target",
		"url":  "https://example.com",
		// Missing type
	})

	config := DefaultTargetDiscoveryConfig()
	manager := &DefaultTargetDiscoveryManager{
		config: config,
		logger: slog.Default(),
	}

	target, err := manager.parseSecretToTarget(secret)

	assert.Error(t, err)
	assert.Nil(t, target)
	assert.Contains(t, err.Error(), "type")
}

func TestParseSecretToTarget_MissingURL(t *testing.T) {
	secret := createTestSecret("test-invalid", "default", map[string]string{
		"name": "invalid-target",
		"type": "webhook",
		// Missing url
	})

	config := DefaultTargetDiscoveryConfig()
	manager := &DefaultTargetDiscoveryManager{
		config: config,
		logger: slog.Default(),
	}

	target, err := manager.parseSecretToTarget(secret)

	assert.Error(t, err)
	assert.Nil(t, target)
	assert.Contains(t, err.Error(), "url")
}

func TestParseSecretToTarget_DefaultFormat(t *testing.T) {
	secret := createTestSecret("test-slack", "default", map[string]string{
		"name": "slack-prod",
		"type": "slack",
		"url":  "https://hooks.slack.com/services/xxx",
		// No format specified
	})

	config := DefaultTargetDiscoveryConfig()
	manager := &DefaultTargetDiscoveryManager{
		config: config,
		logger: slog.Default(),
	}

	target, err := manager.parseSecretToTarget(secret)

	require.NoError(t, err)
	assert.Equal(t, core.PublishingFormat("slack"), target.Format) // Default to type
}

func TestParseSecretToTarget_CustomHeaders(t *testing.T) {
	secret := createTestSecret("test-webhook", "default", map[string]string{
		"name":    "custom-webhook",
		"type":    "webhook",
		"url":     "https://example.com/webhook",
		"headers": "X-Custom=value1, X-Another=value2",
	})

	config := DefaultTargetDiscoveryConfig()
	manager := &DefaultTargetDiscoveryManager{
		config: config,
		logger: slog.Default(),
	}

	target, err := manager.parseSecretToTarget(secret)

	require.NoError(t, err)
	assert.Equal(t, "value1", target.Headers["X-Custom"])
	assert.Equal(t, "value2", target.Headers["X-Another"])
}

func TestGetTarget_NotFound(t *testing.T) {
	config := DefaultTargetDiscoveryConfig()
	manager := &DefaultTargetDiscoveryManager{
		config:  config,
		logger:  slog.Default(),
		targets: make(map[string]*core.PublishingTarget),
	}

	target, err := manager.GetTarget("nonexistent")

	assert.Error(t, err)
	assert.Nil(t, target)
	assert.Contains(t, err.Error(), "not found")
}

func TestListTargets_Empty(t *testing.T) {
	config := DefaultTargetDiscoveryConfig()
	manager := &DefaultTargetDiscoveryManager{
		config:  config,
		logger:  slog.Default(),
		targets: make(map[string]*core.PublishingTarget),
	}

	targets := manager.ListTargets()

	assert.Empty(t, targets)
}

func TestGetTargetCount(t *testing.T) {
	config := DefaultTargetDiscoveryConfig()
	manager := &DefaultTargetDiscoveryManager{
		config: config,
		logger: slog.Default(),
		targets: map[string]*core.PublishingTarget{
			"target1": {Name: "target1"},
			"target2": {Name: "target2"},
		},
	}

	count := manager.GetTargetCount()

	assert.Equal(t, 2, count)
}

// Note: Full integration tests with K8s client would be in separate file
// For now, focusing on unit tests of parsing logic
