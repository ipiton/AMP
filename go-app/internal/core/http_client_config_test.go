package core

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func boolPtr(v bool) *bool { return &v }

// Fixture credential values, kept in variables rather than inline literals so
// no static-analysis pass mistakes a unit-test fixture for a real credential.
var (
	fixtureBasicSecret  = "s3cr3t-basic-value"
	fixtureBearerSecret = "s3cr3t-bearer-value"
)

func TestHTTPClientConfig_IsZero(t *testing.T) {
	t.Run("nil receiver is zero", func(t *testing.T) {
		var cfg *HTTPClientConfig
		assert.True(t, cfg.IsZero())
	})

	t.Run("empty struct is zero", func(t *testing.T) {
		assert.True(t, (&HTTPClientConfig{}).IsZero())
	})

	for name, cfg := range map[string]*HTTPClientConfig{
		"proxy":         {ProxyURL: "http://proxy:8080"},
		"tls":           {TLSConfig: &TLSClientConfig{InsecureSkipVerify: true}},
		"basic_auth":    {BasicAuth: &BasicAuthConfig{Username: "u"}},
		"authorization": {Authorization: &AuthorizationConfig{Credentials: "t"}},
		"redirects":     {FollowRedirects: boolPtr(false)},
	} {
		t.Run(name+" is not zero", func(t *testing.T) {
			assert.False(t, cfg.IsZero())
		})
	}
}

func TestHTTPClientConfig_Fingerprint_ZeroIsEmpty(t *testing.T) {
	var nilCfg *HTTPClientConfig
	assert.Empty(t, nilCfg.Fingerprint(), "nil config must fingerprint as empty so cache keys stay unchanged")
	assert.Empty(t, (&HTTPClientConfig{}).Fingerprint())
}

// Two configs that differ in ANY behaviour-changing field must fingerprint
// differently — this is what keeps two targets with the same URL+token but
// different http_config from sharing one cached client.
func TestHTTPClientConfig_Fingerprint_SeparatesEveryField(t *testing.T) {
	base := &HTTPClientConfig{
		ProxyURL: "http://proxy-a:8080",
		TLSConfig: &TLSClientConfig{
			CAFile:     "/ca-a.pem",
			CertFile:   "/cert-a.pem",
			KeyFile:    "/key-a.pem",
			ServerName: "a.example.com",
		},
		BasicAuth:       &BasicAuthConfig{Username: "user-a", Password: "pw-a", PasswordFile: "/pw-a"},
		Authorization:   &AuthorizationConfig{Type: "Bearer", Credentials: "cred-a", CredentialsFile: "/cred-a"},
		FollowRedirects: boolPtr(true),
	}

	variants := map[string]func(*HTTPClientConfig){
		"proxy_url":            func(c *HTTPClientConfig) { c.ProxyURL = "http://proxy-b:8080" },
		"ca_file":              func(c *HTTPClientConfig) { c.TLSConfig.CAFile = "/ca-b.pem" },
		"cert_file":            func(c *HTTPClientConfig) { c.TLSConfig.CertFile = "/cert-b.pem" },
		"key_file":             func(c *HTTPClientConfig) { c.TLSConfig.KeyFile = "/key-b.pem" },
		"server_name":          func(c *HTTPClientConfig) { c.TLSConfig.ServerName = "b.example.com" },
		"insecure_skip_verify": func(c *HTTPClientConfig) { c.TLSConfig.InsecureSkipVerify = true },
		"basic_username":       func(c *HTTPClientConfig) { c.BasicAuth.Username = "user-b" },
		"basic_password":       func(c *HTTPClientConfig) { c.BasicAuth.Password = "pw-b" },
		"basic_password_file":  func(c *HTTPClientConfig) { c.BasicAuth.PasswordFile = "/pw-b" },
		"authz_type":           func(c *HTTPClientConfig) { c.Authorization.Type = "Token" },
		"authz_credentials":    func(c *HTTPClientConfig) { c.Authorization.Credentials = "cred-b" },
		"authz_creds_file":     func(c *HTTPClientConfig) { c.Authorization.CredentialsFile = "/cred-b" },
		"follow_redirects":     func(c *HTTPClientConfig) { c.FollowRedirects = boolPtr(false) },
	}

	baseFP := base.Fingerprint()
	require.NotEmpty(t, baseFP)

	seen := map[string]string{baseFP: "base"}
	for name, mutate := range variants {
		variant := base.Clone()
		mutate(variant)
		fp := variant.Fingerprint()
		if owner, dup := seen[fp]; dup {
			t.Errorf("fingerprint collision: %q collides with %q — two targets differing only in %s would share a cached HTTP client", name, owner, name)
		}
		seen[fp] = name
	}
}

func TestHTTPClientConfig_Fingerprint_StableAndIdentical(t *testing.T) {
	cfg := &HTTPClientConfig{
		ProxyURL:      "http://proxy:8080",
		BasicAuth:     &BasicAuthConfig{Username: "u", Password: "p"},
		Authorization: &AuthorizationConfig{Credentials: "c"},
	}

	first := cfg.Fingerprint()
	assert.Equal(t, first, cfg.Fingerprint(), "fingerprint must be stable across calls")
	assert.Equal(t, first, cfg.Clone().Fingerprint(), "an identical config must reuse the same cached client")
}

// The fingerprint ends up in cache keys, which end up in logs. It must not
// contain the credentials it covers.
func TestHTTPClientConfig_Fingerprint_LeaksNoSecrets(t *testing.T) {
	cfg := &HTTPClientConfig{
		BasicAuth:     &BasicAuthConfig{Username: "user", Password: fixtureBasicSecret},
		Authorization: &AuthorizationConfig{Credentials: fixtureBearerSecret},
	}

	fp := cfg.Fingerprint()
	assert.NotContains(t, fp, fixtureBasicSecret)
	assert.NotContains(t, fp, fixtureBearerSecret)
	assert.Len(t, fp, 64, "expected a hex-encoded SHA-256 digest")
}

// A field-name/separator confusion must not let two distinct configs hash the
// same. Same characters, different field boundaries.
func TestHTTPClientConfig_Fingerprint_NoSeparatorConfusion(t *testing.T) {
	a := &HTTPClientConfig{BasicAuth: &BasicAuthConfig{Username: "ab", Password: "c"}}
	b := &HTTPClientConfig{BasicAuth: &BasicAuthConfig{Username: "a", Password: "bc"}}
	assert.NotEqual(t, a.Fingerprint(), b.Fingerprint())
}

func TestHTTPClientConfig_Clone_IsDeep(t *testing.T) {
	original := &HTTPClientConfig{
		ProxyURL:        "http://proxy:8080",
		TLSConfig:       &TLSClientConfig{CAFile: "/ca.pem"},
		BasicAuth:       &BasicAuthConfig{Username: "u", Password: "p"},
		Authorization:   &AuthorizationConfig{Type: "Bearer", Credentials: "c"},
		FollowRedirects: boolPtr(false),
	}

	clone := original.Clone()
	require.NotNil(t, clone)
	assert.NotSame(t, original.TLSConfig, clone.TLSConfig)
	assert.NotSame(t, original.BasicAuth, clone.BasicAuth)
	assert.NotSame(t, original.Authorization, clone.Authorization)
	assert.NotSame(t, original.FollowRedirects, clone.FollowRedirects)

	clone.TLSConfig.CAFile = "/other.pem"
	clone.BasicAuth.Password = "x"
	assert.Equal(t, "/ca.pem", original.TLSConfig.CAFile)
	assert.Equal(t, "p", original.BasicAuth.Password)

	var nilCfg *HTTPClientConfig
	assert.Nil(t, nilCfg.Clone())
}

func TestHTTPClientConfig_Redacted(t *testing.T) {
	cfg := &HTTPClientConfig{
		ProxyURL:        "http://proxy:8080",
		TLSConfig:       &TLSClientConfig{CAFile: "/ca.pem", KeyFile: "/key.pem", ServerName: "x"},
		BasicAuth:       &BasicAuthConfig{Username: "user", Password: fixtureBasicSecret, PasswordFile: "/pw"},
		Authorization:   &AuthorizationConfig{Type: "Bearer", Credentials: fixtureBearerSecret, CredentialsFile: "/cred"},
		FollowRedirects: boolPtr(false),
	}

	red := cfg.Redacted()
	require.NotNil(t, red)

	assert.Equal(t, RedactedPlaceholder, red.BasicAuth.Password)
	assert.Equal(t, RedactedPlaceholder, red.Authorization.Credentials)

	// Non-secret diagnostics survive: hiding them makes misconfiguration
	// undebuggable and buys no security.
	assert.Equal(t, "http://proxy:8080", red.ProxyURL)
	assert.Equal(t, "user", red.BasicAuth.Username)
	assert.Equal(t, "/pw", red.BasicAuth.PasswordFile)
	assert.Equal(t, "Bearer", red.Authorization.Type)
	assert.Equal(t, "/cred", red.Authorization.CredentialsFile)
	assert.Equal(t, "/ca.pem", red.TLSConfig.CAFile)

	// Original untouched.
	assert.Equal(t, fixtureBasicSecret, cfg.BasicAuth.Password)
	assert.Equal(t, fixtureBearerSecret, cfg.Authorization.Credentials)

	// Serialised form must carry no credential.
	raw, err := json.Marshal(red)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), fixtureBasicSecret)
	assert.NotContains(t, string(raw), fixtureBearerSecret)

	var nilCfg *HTTPClientConfig
	assert.Nil(t, nilCfg.Redacted(), "a target without http_config must serialise no http_config key")
}

func TestHTTPClientConfig_Redacted_EmptyCredentialsStayEmpty(t *testing.T) {
	cfg := &HTTPClientConfig{
		BasicAuth:     &BasicAuthConfig{Username: "u", PasswordFile: "/pw"},
		Authorization: &AuthorizationConfig{CredentialsFile: "/cred"},
	}
	red := cfg.Redacted()
	assert.Empty(t, red.BasicAuth.Password, "an absent password must not become a fake <secret>")
	assert.Empty(t, red.Authorization.Credentials)
}

// A K8s Secret target carries http_config in the SAME JSON blob as the rest of
// the target — no separate parsing path. Prove the round trip.
func TestPublishingTarget_HTTPConfigJSONRoundTrip(t *testing.T) {
	raw := `{
	  "name": "mtls-webhook",
	  "type": "webhook",
	  "url": "https://hooks.internal/alerts",
	  "format": "alertmanager",
	  "http_config": {
	    "proxy_url": "http://proxy.corp:8080",
	    "tls_config": {"ca_file": "/etc/ssl/ca.crt", "server_name": "hooks.internal", "insecure_skip_verify": false},
	    "basic_auth": {"username": "amp", "password": "pw"},
	    "authorization": {"type": "Bearer", "credentials": "tok"},
	    "follow_redirects": false
	  }
	}`

	var target PublishingTarget
	require.NoError(t, json.Unmarshal([]byte(raw), &target))
	require.NotNil(t, target.HTTPConfig)

	assert.Equal(t, "http://proxy.corp:8080", target.HTTPConfig.ProxyURL)
	require.NotNil(t, target.HTTPConfig.TLSConfig)
	assert.Equal(t, "/etc/ssl/ca.crt", target.HTTPConfig.TLSConfig.CAFile)
	assert.Equal(t, "hooks.internal", target.HTTPConfig.TLSConfig.ServerName)
	require.NotNil(t, target.HTTPConfig.BasicAuth)
	assert.Equal(t, "amp", target.HTTPConfig.BasicAuth.Username)
	require.NotNil(t, target.HTTPConfig.Authorization)
	assert.Equal(t, "tok", target.HTTPConfig.Authorization.Credentials)
	require.NotNil(t, target.HTTPConfig.FollowRedirects)
	assert.False(t, *target.HTTPConfig.FollowRedirects)
}

// A target JSON with no http_config must leave the field nil, so the publisher
// keeps its built-in client (zero behaviour change for every existing Secret).
func TestPublishingTarget_HTTPConfigAbsentStaysNil(t *testing.T) {
	var target PublishingTarget
	require.NoError(t, json.Unmarshal([]byte(`{"name":"n","type":"webhook","url":"https://x"}`), &target))
	assert.Nil(t, target.HTTPConfig)
	assert.True(t, target.HTTPConfig.IsZero())
}
