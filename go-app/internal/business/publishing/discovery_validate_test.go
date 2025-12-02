package publishing

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/ipiton/AMP/internal/core"
)

// Test Suite: Target Validation

func TestValidateTarget_ValidTarget(t *testing.T) {
	target := &core.PublishingTarget{
		Name:    "rootly-prod",
		Type:    "rootly",
		URL:     "https://api.rootly.io/v1/incidents",
		Format:  "rootly",
		Enabled: true,
		Headers: map[string]string{
			"Authorization": "Bearer token",
		},
	}

	errors := validateTarget(target)
	assert.Empty(t, errors)
}

func TestValidateTarget_MissingName(t *testing.T) {
	target := &core.PublishingTarget{
		Name:   "", // missing
		Type:   "rootly",
		URL:    "https://example.com",
		Format: "rootly",
	}

	errors := validateTarget(target)
	assert.NotEmpty(t, errors)
	assert.Equal(t, "name", errors[0].Field)
	assert.Contains(t, errors[0].Message, "required")
}

func TestValidateTarget_InvalidName(t *testing.T) {
	tests := []struct {
		name   string
		target string
		reason string
	}{
		{
			name:   "uppercase letters",
			target: "Rootly-Prod",
			reason: "must be lowercase",
		},
		{
			name:   "underscore",
			target: "rootly_prod",
			reason: "must be lowercase",
		},
		{
			name:   "special characters",
			target: "rootly@prod",
			reason: "must be lowercase",
		},
		{
			name:   "too long (>63 chars)",
			target: "a-very-long-target-name-that-exceeds-sixty-three-characters-limit-for-dns",
			reason: "must be lowercase",
		},
		{
			name:   "starts with hyphen",
			target: "-rootly",
			reason: "must be lowercase",
		},
		{
			name:   "ends with hyphen",
			target: "rootly-",
			reason: "must be lowercase",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := &core.PublishingTarget{
				Name:   tt.target,
				Type:   "rootly",
				URL:    "https://example.com",
				Format: "rootly",
			}

			errors := validateTarget(target)
			assert.NotEmpty(t, errors)
			found := false
			for _, err := range errors {
				if err.Field == "name" {
					found = true
					break
				}
			}
			assert.True(t, found, "Expected name validation error")
		})
	}
}

func TestValidateTarget_MissingType(t *testing.T) {
	target := &core.PublishingTarget{
		Name:   "test-target",
		Type:   "", // missing
		URL:    "https://example.com",
		Format: "webhook",
	}

	errors := validateTarget(target)
	assert.NotEmpty(t, errors)
	assert.Equal(t, "type", errors[0].Field)
}

func TestValidateTarget_InvalidType(t *testing.T) {
	target := &core.PublishingTarget{
		Name:   "test-target",
		Type:   "invalid-type",
		URL:    "https://example.com",
		Format: "webhook",
	}

	errors := validateTarget(target)
	assert.NotEmpty(t, errors)
	found := false
	for _, err := range errors {
		if err.Field == "type" {
			found = true
			assert.Contains(t, err.Message, "rootly, pagerduty, slack, webhook")
			break
		}
	}
	assert.True(t, found)
}

func TestValidateTarget_MissingURL(t *testing.T) {
	target := &core.PublishingTarget{
		Name:   "test-target",
		Type:   "webhook",
		URL:    "", // missing
		Format: "webhook",
	}

	errors := validateTarget(target)
	assert.NotEmpty(t, errors)
	assert.Equal(t, "url", errors[0].Field)
}

func TestValidateTarget_InvalidURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{"not a URL", "not-a-url"},
		{"missing scheme", "example.com"},
		{"ftp scheme", "ftp://example.com"},
		{"empty host", "https://"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := &core.PublishingTarget{
				Name:   "test-target",
				Type:   "webhook",
				URL:    tt.url,
				Format: "webhook",
			}

			errors := validateTarget(target)
			assert.NotEmpty(t, errors)
			found := false
			for _, err := range errors {
				if err.Field == "url" {
					found = true
					break
				}
			}
			assert.True(t, found, "Expected URL validation error")
		})
	}
}

func TestValidateTarget_MissingFormat(t *testing.T) {
	target := &core.PublishingTarget{
		Name:   "test-target",
		Type:   "webhook",
		URL:    "https://example.com",
		Format: "", // missing
	}

	errors := validateTarget(target)
	assert.NotEmpty(t, errors)
	assert.Equal(t, "format", errors[0].Field)
}

func TestValidateTarget_InvalidFormat(t *testing.T) {
	target := &core.PublishingTarget{
		Name:   "test-target",
		Type:   "webhook",
		URL:    "https://example.com",
		Format: "invalid-format",
	}

	errors := validateTarget(target)
	assert.NotEmpty(t, errors)
	found := false
	for _, err := range errors {
		if err.Field == "format" {
			found = true
			break
		}
	}
	assert.True(t, found)
}

func TestValidateTarget_TypeFormatMismatch(t *testing.T) {
	tests := []struct {
		name   string
		typ    string
		format core.PublishingFormat
		valid  bool
	}{
		{"rootly/rootly", "rootly", "rootly", true},
		{"rootly/slack", "rootly", "slack", false},
		{"pagerduty/pagerduty", "pagerduty", "pagerduty", true},
		{"slack/slack", "slack", "slack", true},
		{"webhook/alertmanager", "webhook", "alertmanager", true},
		{"webhook/webhook", "webhook", "webhook", true},
		{"webhook/rootly", "webhook", "rootly", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := &core.PublishingTarget{
				Name:   "test-target",
				Type:   tt.typ,
				URL:    "https://example.com",
				Format: tt.format,
			}

			errors := validateTarget(target)
			if tt.valid {
				assert.Empty(t, errors, "Expected no validation errors")
			} else {
				assert.NotEmpty(t, errors, "Expected validation error")
				found := false
				for _, err := range errors {
					if err.Field == "format" && err.Message != "field is required" {
						found = true
						assert.Contains(t, err.Message, "incompatible")
						break
					}
				}
				assert.True(t, found, "Expected type/format compatibility error")
			}
		})
	}
}

func TestValidateTarget_EmptyHeaderKey(t *testing.T) {
	target := &core.PublishingTarget{
		Name:   "test-target",
		Type:   "webhook",
		URL:    "https://example.com",
		Format: "webhook",
		Headers: map[string]string{
			"": "value",
		},
	}

	errors := validateTarget(target)
	assert.NotEmpty(t, errors)
	found := false
	for _, err := range errors {
		if err.Field == "headers" && err.Message == "header key cannot be empty" {
			found = true
			break
		}
	}
	assert.True(t, found)
}

func TestValidateTarget_EmptyHeaderValue(t *testing.T) {
	target := &core.PublishingTarget{
		Name:   "test-target",
		Type:   "webhook",
		URL:    "https://example.com",
		Format: "webhook",
		Headers: map[string]string{
			"Authorization": "",
		},
	}

	errors := validateTarget(target)
	assert.NotEmpty(t, errors)
	found := false
	for _, err := range errors {
		if err.Field == "headers" && err.Message != "" && err.Message != "header key cannot be empty" {
			found = true
			assert.Contains(t, err.Message, "cannot be empty")
			break
		}
	}
	assert.True(t, found)
}

func TestIsValidTargetName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		valid bool
	}{
		{"valid simple", "test", true},
		{"valid with hyphen", "test-target", true},
		{"valid with numbers", "target-1", true},
		{"valid complex", "rootly-prod-us-west-2", true},
		{"empty", "", false},
		{"uppercase", "Test", false},
		{"underscore", "test_target", false},
		{"special char", "test@target", false},
		{"starts with hyphen", "-test", false},
		{"ends with hyphen", "test-", false},
		{"too long", "a-very-long-name-that-exceeds-sixty-three-characters-limit-definitely", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isValidTargetName(tt.input)
			assert.Equal(t, tt.valid, result)
		})
	}
}

func TestIsValidTargetType(t *testing.T) {
	tests := []struct {
		name  string
		input string
		valid bool
	}{
		{"rootly", "rootly", true},
		{"pagerduty", "pagerduty", true},
		{"slack", "slack", true},
		{"webhook", "webhook", true},
		{"invalid", "invalid", false},
		{"uppercase", "ROOTLY", false},
		{"empty", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isValidTargetType(tt.input)
			assert.Equal(t, tt.valid, result)
		})
	}
}

func TestIsValidFormat(t *testing.T) {
	tests := []struct {
		name  string
		input string
		valid bool
	}{
		{"alertmanager", "alertmanager", true},
		{"rootly", "rootly", true},
		{"pagerduty", "pagerduty", true},
		{"slack", "slack", true},
		{"webhook", "webhook", true},
		{"invalid", "invalid", false},
		{"uppercase", "ALERTMANAGER", false},
		{"empty", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isValidFormat(tt.input)
			assert.Equal(t, tt.valid, result)
		})
	}
}

func TestIsValidURL(t *testing.T) {
	tests := []struct {
		name  string
		input string
		valid bool
	}{
		{"valid https", "https://example.com", true},
		{"valid http", "http://example.com", true},
		{"with path", "https://example.com/path", true},
		{"with port", "https://example.com:8080", true},
		{"with query", "https://example.com?foo=bar", true},
		{"localhost", "http://localhost:8080", true},
		{"IP address", "http://192.168.1.1", true},
		{"no scheme", "example.com", false},
		{"ftp scheme", "ftp://example.com", false},
		{"empty", "", false},
		{"just scheme", "https://", false},
		{"invalid", "not a url", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isValidURL(tt.input)
			assert.Equal(t, tt.valid, result)
		})
	}
}

func TestIsCompatibleTypeFormat(t *testing.T) {
	tests := []struct {
		name   string
		typ    string
		format string
		valid  bool
	}{
		{"rootly/rootly", "rootly", "rootly", true},
		{"rootly/slack", "rootly", "slack", false},
		{"pagerduty/pagerduty", "pagerduty", "pagerduty", true},
		{"slack/slack", "slack", "slack", true},
		{"webhook/alertmanager", "webhook", "alertmanager", true},
		{"webhook/webhook", "webhook", "webhook", true},
		{"webhook/rootly", "webhook", "rootly", false},
		{"unknown type", "unknown", "webhook", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isCompatibleTypeFormat(tt.typ, tt.format)
			assert.Equal(t, tt.valid, result)
		})
	}
}
