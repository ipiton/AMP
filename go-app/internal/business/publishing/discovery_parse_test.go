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

func TestParseSecret_ReceiverLabel_SingleName(t *testing.T) {
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
			Labels: map[string]string{
				AmpReceiverLabel: "slack-critical",
			},
		},
		Data: map[string][]byte{
			"config": configJSON,
		},
	}

	parsed, err := parseSecret(secret)
	require.NoError(t, err)
	assert.Equal(t, []string{"slack-critical"}, parsed.Receivers)
}

func TestParseSecret_ReceiverAnnotation_MultipleNamesWithSpaces(t *testing.T) {
	// Annotation is the primary, multi-name source: K8s label values can't
	// contain commas, so this is the only way to scope a target to several
	// receivers.
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
			Annotations: map[string]string{
				AmpReceiverLabel: "slack-critical, pagerduty-oncall ,  team-b ",
			},
		},
		Data: map[string][]byte{
			"config": configJSON,
		},
	}

	parsed, err := parseSecret(secret)
	require.NoError(t, err)
	assert.Equal(t, []string{"slack-critical", "pagerduty-oncall", "team-b"}, parsed.Receivers)
}

func TestParseSecret_ReceiverAnnotationWinsOverLabel(t *testing.T) {
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
			Annotations: map[string]string{
				AmpReceiverLabel: "slack-critical,pagerduty-oncall",
			},
			Labels: map[string]string{
				AmpReceiverLabel: "team-b",
			},
		},
		Data: map[string][]byte{
			"config": configJSON,
		},
	}

	parsed, err := parseSecret(secret)
	require.NoError(t, err)
	assert.Equal(t, []string{"slack-critical", "pagerduty-oncall"}, parsed.Receivers)
}

func TestParseSecret_ReceiverLabel_NeitherAnnotationNorLabelMeansNoScoping(t *testing.T) {
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
			// No amp.receiver annotation or label at all
		},
		Data: map[string][]byte{
			"config": configJSON,
		},
	}

	parsed, err := parseSecret(secret)
	require.NoError(t, err)
	assert.Empty(t, parsed.Receivers)
}

func TestParseSecret_ReceiverLabel_EmptyValueYieldsNoReceivers(t *testing.T) {
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
			Labels: map[string]string{
				AmpReceiverLabel: "  , ,",
			},
		},
		Data: map[string][]byte{
			"config": configJSON,
		},
	}

	parsed, err := parseSecret(secret)
	require.NoError(t, err)
	assert.Empty(t, parsed.Receivers)
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
		name    string
		target  *core.PublishingTarget
		checkFn func(*testing.T, *core.PublishingTarget)
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

// ============================================================================
// FU-HTTP-CONFIG, review M8 + M10: Secret-sourced http_config
// ============================================================================
//
// The Secret path json.Unmarshals `http_config` in for free, which is the whole
// reason K8s targets support it with no extra syntax. But "for free" also meant
// "unvalidated and un-normalised": an invalid block passed discovery, appeared
// in the target list, and then failed on EVERY publish job instead of being
// skipped once with a WARN like every other invalid Secret (M8); and a
// behaviour-free block bought a dedicated *http.Client for nothing (M10).

func secretFromTargetJSON(t *testing.T, name, raw string) corev1.Secret {
	t.Helper()
	return corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Data:       map[string][]byte{"config": []byte(raw)},
	}
}

func TestParseSecret_HTTPConfigIsParsedAndNormalized(t *testing.T) {
	// insecure_skip_verify:false and follow_redirects:true are both the DEFAULT.
	// Writing them explicitly is natural and must cost nothing.
	target, err := parseSecret(secretFromTargetJSON(t, "s", `{
	  "name": "webhook-1",
	  "type": "webhook",
	  "url": "https://hooks.internal/a",
	  "format": "alertmanager",
	  "http_config": {
	    "tls_config": {"insecure_skip_verify": false},
	    "basic_auth": {},
	    "follow_redirects": true
	  }
	}`))
	require.NoError(t, err)

	assert.Nil(t, target.HTTPConfig,
		"a behaviour-free http_config must normalise to nil, not buy a dedicated *http.Client")
	assert.Empty(t, target.HTTPConfig.Fingerprint(), "and must leave the cache key unchanged")
}

func TestParseSecret_MeaningfulHTTPConfigSurvivesNormalization(t *testing.T) {
	target, err := parseSecret(secretFromTargetJSON(t, "s", `{
	  "name": "webhook-1",
	  "type": "webhook",
	  "url": "https://hooks.internal/a",
	  "format": "alertmanager",
	  "http_config": {
	    "proxy_url": "http://proxy.corp:8080",
	    "tls_config": {"ca_file": "/etc/ssl/ca.pem"},
	    "follow_redirects": false
	  }
	}`))
	require.NoError(t, err)

	require.NotNil(t, target.HTTPConfig)
	assert.Equal(t, "http://proxy.corp:8080", target.HTTPConfig.ProxyURL)
	require.NotNil(t, target.HTTPConfig.TLSConfig)
	assert.Equal(t, "/etc/ssl/ca.pem", target.HTTPConfig.TLSConfig.CAFile)
	require.NotNil(t, target.HTTPConfig.FollowRedirects)
	assert.False(t, *target.HTTPConfig.FollowRedirects)
}

// M8: an invalid http_config must be caught at DISCOVERY, so the target is
// skipped once and loudly rather than failing every job forever.
func TestValidateTarget_RejectsInvalidHTTPConfig(t *testing.T) {
	cases := map[string]*core.HTTPClientConfig{
		"both auth methods": {
			BasicAuth:     &core.BasicAuthConfig{Username: "u", Password: "p"},
			Authorization: &core.AuthorizationConfig{Credentials: "t"},
		},
		"cert without key":                 {TLSConfig: &core.TLSClientConfig{CertFile: "/c.pem"}},
		"unroutable proxy":                 {ProxyURL: "proxy.corp:8080"},
		"authorization without credential": {Authorization: &core.AuthorizationConfig{Type: "Bearer"}},
	}

	for name, httpConfig := range cases {
		t.Run(name, func(t *testing.T) {
			target := &core.PublishingTarget{
				Name:       "webhook-1",
				Type:       "webhook",
				URL:        "https://hooks.internal/a",
				Format:     core.FormatAlertmanager,
				Enabled:    true,
				HTTPConfig: httpConfig,
			}

			errs := validateTarget(target)
			require.NotEmpty(t, errs, "an invalid http_config must fail validation at discovery")

			var found bool
			for _, e := range errs {
				if e.Field == "http_config" {
					found = true
					assert.NotContains(t, e.Value, "password",
						"the validation error must never echo the config's credentials")
				}
			}
			assert.True(t, found, "the error must be attributed to the http_config field")
		})
	}
}

// A valid http_config (and no http_config at all) must pass validation
// untouched — the new check must not reject working targets.
func TestValidateTarget_AcceptsValidAndAbsentHTTPConfig(t *testing.T) {
	for name, httpConfig := range map[string]*core.HTTPClientConfig{
		"absent": nil,
		"valid": {
			ProxyURL:  "http://proxy.corp:8080",
			TLSConfig: &core.TLSClientConfig{CAFile: "/ca.pem", CertFile: "/c.pem", KeyFile: "/k.pem"},
			BasicAuth: &core.BasicAuthConfig{Username: "amp", PasswordFile: "/pw"},
		},
		// File readability is the client builder's job: a projected volume may
		// legitimately not exist yet when the Secret is first observed.
		"unreadable files": {TLSConfig: &core.TLSClientConfig{CAFile: "/definitely/not/here.pem"}},
	} {
		t.Run(name, func(t *testing.T) {
			target := &core.PublishingTarget{
				Name:       "webhook-1",
				Type:       "webhook",
				URL:        "https://hooks.internal/a",
				Format:     core.FormatAlertmanager,
				Enabled:    true,
				HTTPConfig: httpConfig,
			}
			assert.Empty(t, validateTarget(target))
		})
	}
}
