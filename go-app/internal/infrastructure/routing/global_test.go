package routing

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGlobalConfig_Defaults(t *testing.T) {
	config := &GlobalConfig{}

	config.Defaults()

	require.NotNil(t, config.ResolveTimeout)
	assert.Equal(t, Duration(5*time.Minute), *config.ResolveTimeout)
}

func TestGlobalConfig_Clone(t *testing.T) {
	original := &GlobalConfig{
		SMTPFrom:      "alerts@example.com",
		SMTPSmartHost: "smtp.example.com:587",
		HTTPConfig: &HTTPConfig{
			ProxyURL: "http://proxy:8080",
		},
	}

	cloned := original.Clone()

	require.NotNil(t, cloned)
	assert.Equal(t, original.SMTPFrom, cloned.SMTPFrom)
	assert.NotSame(t, original.HTTPConfig, cloned.HTTPConfig)

	// Modify clone shouldn't affect original
	cloned.SMTPFrom = "modified@example.com"
	assert.Equal(t, "alerts@example.com", original.SMTPFrom)
}

func TestHTTPConfig_Defaults(t *testing.T) {
	config := &HTTPConfig{}

	config.Defaults()

	require.NotNil(t, config.FollowRedirects)
	assert.True(t, *config.FollowRedirects)
	assert.Equal(t, 10*time.Second, config.ConnectTimeout)
	assert.Equal(t, 30*time.Second, config.RequestTimeout)
}

func TestHTTPConfig_Clone(t *testing.T) {
	followRedirects := false
	original := &HTTPConfig{
		ProxyURL:        "http://proxy:8080",
		FollowRedirects: &followRedirects,
		ConnectTimeout:  15 * time.Second,
		TLSConfig: &TLSConfig{
			CAFile: "/etc/ssl/ca.crt",
		},
	}

	cloned := original.Clone()

	require.NotNil(t, cloned)
	assert.Equal(t, original.ProxyURL, cloned.ProxyURL)
	assert.Equal(t, original.ConnectTimeout, cloned.ConnectTimeout)
	assert.NotSame(t, original.TLSConfig, cloned.TLSConfig)
}

func TestTLSConfig_Clone(t *testing.T) {
	original := &TLSConfig{
		CAFile:             "/etc/ssl/ca.crt",
		CertFile:           "/etc/ssl/client.crt",
		KeyFile:            "/etc/ssl/client.key",
		ServerName:         "example.com",
		InsecureSkipVerify: false,
	}

	cloned := original.Clone()

	require.NotNil(t, cloned)
	assert.Equal(t, original.CAFile, cloned.CAFile)
	assert.Equal(t, original.ServerName, cloned.ServerName)

	// Modify clone shouldn't affect original
	cloned.CAFile = "/modified/ca.crt"
	assert.Equal(t, "/etc/ssl/ca.crt", original.CAFile)
}

func TestDuration_UnmarshalYAML(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected time.Duration
		wantErr  bool
	}{
		{"5 minutes", "5m", 5 * time.Minute, false},
		{"30 seconds", "30s", 30 * time.Second, false},
		{"1 hour", "1h", 1 * time.Hour, false},
		{"invalid", "invalid", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var duration Duration

			// Simulate YAML unmarshaling
			unmarshal := func(v interface{}) error {
				*(v.(*string)) = tt.input
				return nil
			}

			err := duration.UnmarshalYAML(unmarshal)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, Duration(tt.expected), duration)
			}
		})
	}
}
