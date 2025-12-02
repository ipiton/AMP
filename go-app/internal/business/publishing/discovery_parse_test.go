package publishing

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/ipiton/AMP/internal/core"
)

// Test Suite: Secret Parsing

func TestParseSecret_ValidSecret(t *testing.T) {
	target := core.PublishingTarget{
		Name:    "test-target",
		Type:    "rootly",
		URL:     "https://api.rootly.io/v1/incidents",
		Format:  "rootly",
		Enabled: true,
		Headers: map[string]string{
			"Authorization": "Bearer token123",
		},
	}

	configJSON, _ := json.Marshal(target)
	configBase64 := base64.StdEncoding.EncodeToString(configJSON)

	secret := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-secret",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"config": []byte(configBase64),
		},
	}

	parsed, err := parseSecret(secret)
	require.NoError(t, err)
	assert.Equal(t, "test-target", parsed.Name)
	assert.Equal(t, "rootly", parsed.Type)
	assert.Equal(t, "https://api.rootly.io/v1/incidents", parsed.URL)
	assert.True(t, parsed.Enabled)
	assert.Equal(t, "Bearer token123", parsed.Headers["Authorization"])
}

func TestParseSecret_MissingConfigField(t *testing.T) {
	secret := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-secret",
			Namespace: "default",
		},
		Data: map[string][]byte{
			// No 'config' field
			"other": []byte("data"),
		},
	}

	parsed, err := parseSecret(secret)
	assert.Error(t, err)
	assert.Nil(t, parsed)

	var formatErr *ErrInvalidSecretFormat
	require.ErrorAs(t, err, &formatErr)
	assert.Equal(t, "test-secret", formatErr.SecretName)
	assert.Contains(t, formatErr.Reason, "missing 'config' field")
}

func TestParseSecret_EmptyConfig(t *testing.T) {
	secret := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-secret",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"config": []byte(""),
		},
	}

	parsed, err := parseSecret(secret)
	assert.Error(t, err)
	assert.Nil(t, parsed)

	var formatErr *ErrInvalidSecretFormat
	require.ErrorAs(t, err, &formatErr)
	assert.Contains(t, formatErr.Reason, "empty")
}

func TestParseSecret_InvalidBase64(t *testing.T) {
	secret := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-secret",
			Namespace: "default",
		},
		Data: map[string][]byte{
			// This looks like base64 but decodes to "Hello World" which is not valid JSON
			// Go's base64 decoder is lenient with padding, so it will decode successfully
			// but JSON unmarshal will fail
			"config": []byte("SGVsbG8gV29ybGQ"), // "Hello World" (not JSON)
		},
	}

	parsed, err := parseSecret(secret)
	// Should fail at JSON unmarshal stage (not base64)
	assert.Error(t, err)
	assert.Nil(t, parsed)

	var formatErr *ErrInvalidSecretFormat
	require.ErrorAs(t, err, &formatErr)
	// Since Go's base64 is lenient, it decodes successfully but JSON fails
	assert.Contains(t, formatErr.Reason, "JSON")
}

func TestParseSecret_InvalidJSON(t *testing.T) {
	invalidJSON := "{invalid json, no closing brace"
	configBase64 := base64.StdEncoding.EncodeToString([]byte(invalidJSON))

	secret := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-secret",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"config": []byte(configBase64),
		},
	}

	parsed, err := parseSecret(secret)
	assert.Error(t, err)
	assert.Nil(t, parsed)

	var formatErr *ErrInvalidSecretFormat
	require.ErrorAs(t, err, &formatErr)
	assert.Contains(t, formatErr.Reason, "JSON")
}

func TestParseSecret_RawJSON(t *testing.T) {
	// Test parsing raw JSON (not base64-encoded)
	// This happens when K8s client-go auto-decodes
	target := core.PublishingTarget{
		Name:   "test-target",
		Type:   "webhook",
		URL:    "https://example.com",
		Format: "webhook",
	}

	configJSON, _ := json.Marshal(target)

	secret := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-secret",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"config": configJSON, // Raw JSON (not base64)
		},
	}

	parsed, err := parseSecret(secret)
	require.NoError(t, err)
	assert.Equal(t, "test-target", parsed.Name)
	assert.Equal(t, "webhook", parsed.Type)
}

func TestIsBase64Encoded_ValidBase64(t *testing.T) {
	data := []byte("SGVsbG8gV29ybGQ=") // "Hello World" in base64
	assert.True(t, isBase64Encoded(data))
}

func TestIsBase64Encoded_RawJSON(t *testing.T) {
	data := []byte(`{"name":"test"}`)
	assert.False(t, isBase64Encoded(data))
}

func TestIsBase64Encoded_Empty(t *testing.T) {
	data := []byte("")
	assert.True(t, isBase64Encoded(data)) // Empty string is valid base64
}

func TestIsBase64Char(t *testing.T) {
	tests := []struct {
		name     string
		char     byte
		expected bool
	}{
		{"uppercase A", 'A', true},
		{"uppercase Z", 'Z', true},
		{"lowercase a", 'a', true},
		{"lowercase z", 'z', true},
		{"digit 0", '0', true},
		{"digit 9", '9', true},
		{"plus", '+', true},
		{"slash", '/', true},
		{"equals", '=', true},
		{"space", ' ', false},
		{"curly brace", '{', false},
		{"colon", ':', false},
		{"quote", '"', false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isBase64Char(tt.char)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestApplyDefaults(t *testing.T) {
	tests := []struct {
		name     string
		target   *core.PublishingTarget
		checkFn  func(*testing.T, *core.PublishingTarget)
	}{
		{
			name: "default enabled=true when all zero values",
			target: &core.PublishingTarget{
				Name:   "test",
				Type:   "webhook",
				URL:    "https://example.com",
				Format: "webhook",
				// enabled, headers, filter_config all zero values
			},
			checkFn: func(t *testing.T, target *core.PublishingTarget) {
				assert.True(t, target.Enabled)
				assert.NotNil(t, target.Headers)
				assert.NotNil(t, target.FilterConfig)
			},
		},
		{
			name: "respect explicit enabled=false",
			target: &core.PublishingTarget{
				Name:    "test",
				Type:    "webhook",
				URL:     "https://example.com",
				Format:  "webhook",
				Enabled: false,
				Headers: map[string]string{
					"X-Custom": "value",
				},
			},
			checkFn: func(t *testing.T, target *core.PublishingTarget) {
				assert.False(t, target.Enabled)
				assert.NotNil(t, target.Headers)
			},
		},
		{
			name: "initialize nil headers",
			target: &core.PublishingTarget{
				Name:    "test",
				Type:    "webhook",
				URL:     "https://example.com",
				Format:  "webhook",
				Enabled: true,
				Headers: nil,
			},
			checkFn: func(t *testing.T, target *core.PublishingTarget) {
				assert.NotNil(t, target.Headers)
				assert.Len(t, target.Headers, 0)
			},
		},
		{
			name: "initialize nil filter_config",
			target: &core.PublishingTarget{
				Name:         "test",
				Type:         "webhook",
				URL:          "https://example.com",
				Format:       "webhook",
				Enabled:      true,
				FilterConfig: nil,
			},
			checkFn: func(t *testing.T, target *core.PublishingTarget) {
				assert.NotNil(t, target.FilterConfig)
				assert.Len(t, target.FilterConfig, 0)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			applyDefaults(tt.target)
			tt.checkFn(t, tt.target)
		})
	}
}
