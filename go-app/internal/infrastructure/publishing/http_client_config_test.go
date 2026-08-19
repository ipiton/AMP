package publishing

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ipiton/AMP/internal/core"
)

// ============================================================================
// FU-HTTP-CONFIG: applyHTTPClientConfig
// ============================================================================

func redirectPolicy(v bool) *bool { return &v }

// httpConfigFixtureSecret is a per-run throwaway value, kept in a variable so
// no static-analysis pass mistakes a unit-test fixture for a credential.
var httpConfigFixtureSecret = "fixture-pass-value"

// plainBase mirrors the shape a publisher hands in: an *http.Client whose
// Transport is an *http.Transport carrying tunings that must survive.
func plainBase() *http.Client {
	return &http.Client{
		Timeout: 7 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
			MaxIdleConns:        42,
			MaxIdleConnsPerHost: 7,
			ForceAttemptHTTP2:   true,
		},
	}
}

func TestApplyHTTPClientConfig_ZeroConfigReturnsBaseUntouched(t *testing.T) {
	base := plainBase()

	for name, cfg := range map[string]*core.HTTPClientConfig{
		"nil":   nil,
		"empty": {},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := applyHTTPClientConfig(base, cfg)
			require.NoError(t, err)
			assert.Same(t, base, got, "a target without http_config must keep the publisher's own client")
		})
	}
}

// Layering http_config on top must not lose the publisher's own tuning — that
// is the whole reason applyHTTPClientConfig copies rather than rebuilds.
func TestApplyHTTPClientConfig_PreservesBaseTuning(t *testing.T) {
	base := plainBase()

	got, err := applyHTTPClientConfig(base, &core.HTTPClientConfig{ProxyURL: "http://proxy.example:8080"})
	require.NoError(t, err)
	require.NotSame(t, base, got)

	assert.Equal(t, 7*time.Second, got.Timeout, "publisher timeout must survive")

	transport, ok := got.Transport.(*http.Transport)
	require.True(t, ok)
	assert.Equal(t, 42, transport.MaxIdleConns)
	assert.Equal(t, 7, transport.MaxIdleConnsPerHost)
	assert.True(t, transport.ForceAttemptHTTP2)
	require.NotNil(t, transport.TLSClientConfig)
	assert.Equal(t, uint16(tls.VersionTLS12), transport.TLSClientConfig.MinVersion, "TLS 1.2 floor must survive")

	// The base transport must be untouched — other targets share it.
	baseTransport, ok := base.Transport.(*http.Transport)
	require.True(t, ok)
	assert.Nil(t, baseTransport.Proxy, "the publisher's own transport must not be mutated")
}

// A plain-HTTP proxy sees an absolute-URI request line for every request; that
// is the observable proof the proxy is actually in the path.
func TestApplyHTTPClientConfig_ProxyURLIsUsed(t *testing.T) {
	var proxied atomic.Int64
	var lastRequestURI atomic.Value

	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxied.Add(1)
		lastRequestURI.Store(r.RequestURI)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("via-proxy"))
	}))
	defer proxy.Close()

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	defer origin.Close()

	client, err := applyHTTPClientConfig(plainBase(), &core.HTTPClientConfig{ProxyURL: proxy.URL})
	require.NoError(t, err)

	body, status := httpConfigGet(t, client, origin.URL+"/alerts")

	assert.Equal(t, http.StatusOK, status)
	assert.Equal(t, "via-proxy", body, "the proxy answered, so the origin was never reached directly")
	assert.Equal(t, int64(1), proxied.Load())
	assert.Equal(t, origin.URL+"/alerts", lastRequestURI.Load(),
		"a proxied plain-HTTP request carries the absolute URI")
}

// Without a proxy the same request reaches the origin — proves the assertion
// above is about the proxy and not about the test setup.
func TestApplyHTTPClientConfig_NoProxyReachesOriginDirectly(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	defer origin.Close()

	client, err := applyHTTPClientConfig(plainBase(), &core.HTTPClientConfig{
		BasicAuth: &core.BasicAuthConfig{Username: "u"},
	})
	require.NoError(t, err)

	_, status := httpConfigGet(t, client, origin.URL)
	assert.Equal(t, http.StatusTeapot, status)
}

func TestApplyHTTPClientConfig_InvalidProxyURL(t *testing.T) {
	for name, proxyURL := range map[string]string{
		"unparseable": "http://[::1",
		"no scheme":   "proxy.example:8080",
		"scheme only": "http://",
		"not a url":   "not a url at all",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := applyHTTPClientConfig(plainBase(), &core.HTTPClientConfig{ProxyURL: proxyURL})
			require.Error(t, err, "a proxy_url that cannot route must fail the target build, not be ignored")
			assert.Contains(t, err.Error(), "proxy_url")
		})
	}
}

// insecure_skip_verify against a self-signed httptest TLS server: the request
// must fail without it and succeed with it.
func TestApplyHTTPClientConfig_TLSInsecureSkipVerify(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	t.Run("rejected without insecure_skip_verify", func(t *testing.T) {
		client, err := applyHTTPClientConfig(plainBase(), &core.HTTPClientConfig{
			TLSConfig: &core.TLSClientConfig{ServerName: "not-the-cert-name.example.com"},
		})
		require.NoError(t, err)

		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, nil)
		require.NoError(t, err)
		resp, err := client.Do(req) //nolint:bodyclose // expected to fail before a body exists
		if err == nil {
			_ = resp.Body.Close()
		}
		require.Error(t, err, "a self-signed certificate must not verify by default")
	})

	t.Run("accepted with insecure_skip_verify", func(t *testing.T) {
		client, err := applyHTTPClientConfig(plainBase(), &core.HTTPClientConfig{
			TLSConfig: &core.TLSClientConfig{InsecureSkipVerify: true},
		})
		require.NoError(t, err)

		_, status := httpConfigGet(t, client, server.URL)
		assert.Equal(t, http.StatusNoContent, status)
	})
}

// A ca_file holding the server's own certificate must verify it WITHOUT
// insecure_skip_verify — this is the real private-CA path.
func TestApplyHTTPClientConfig_TLSCAFileVerifies(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	require.NotEmpty(t, server.Certificate().Raw)
	caPath := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: server.Certificate().Raw,
	}), 0o600))

	serverURL, err := url.Parse(server.URL)
	require.NoError(t, err)

	client, err := applyHTTPClientConfig(plainBase(), &core.HTTPClientConfig{
		TLSConfig: &core.TLSClientConfig{CAFile: caPath, ServerName: serverURL.Hostname()},
	})
	require.NoError(t, err)

	_, status := httpConfigGet(t, client, server.URL)
	assert.Equal(t, http.StatusNoContent, status)
}

// An unreadable or unusable ca_file must fail the target build LOUDLY rather
// than silently fall back to the system trust store.
func TestApplyHTTPClientConfig_BadCAFileFailsTargetBuild(t *testing.T) {
	dir := t.TempDir()

	notPEM := filepath.Join(dir, "garbage.pem")
	require.NoError(t, os.WriteFile(notPEM, []byte("this is not a certificate\n"), 0o600))

	for name, path := range map[string]string{
		"missing file":  filepath.Join(dir, "does-not-exist.pem"),
		"is a dir":      dir,
		"no PEM inside": notPEM,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := applyHTTPClientConfig(plainBase(), &core.HTTPClientConfig{
				TLSConfig: &core.TLSClientConfig{CAFile: path},
			})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "ca_file")
			assert.Contains(t, err.Error(), path, "the error must name the path so it is debuggable")
		})
	}
}

func TestApplyHTTPClientConfig_HalfClientCertificateIsRejected(t *testing.T) {
	_, err := applyHTTPClientConfig(plainBase(), &core.HTTPClientConfig{
		TLSConfig: &core.TLSClientConfig{CertFile: "/tmp/cert.pem"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "key_file")

	_, err = applyHTTPClientConfig(plainBase(), &core.HTTPClientConfig{
		TLSConfig: &core.TLSClientConfig{KeyFile: "/tmp/key.pem"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cert_file")
}

func TestApplyHTTPClientConfig_UnreadableClientCertificateFailsTargetBuild(t *testing.T) {
	dir := t.TempDir()
	_, err := applyHTTPClientConfig(plainBase(), &core.HTTPClientConfig{
		TLSConfig: &core.TLSClientConfig{
			CertFile: filepath.Join(dir, "missing-cert.pem"),
			KeyFile:  filepath.Join(dir, "missing-key.pem"),
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "client certificate")
}

// A real client certificate pair must load and be presented to the server.
func TestApplyHTTPClientConfig_ClientCertificateIsOffered(t *testing.T) {
	certPath, keyPath := writeSelfSignedPair(t)

	var offered atomic.Int64
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS != nil && len(r.TLS.PeerCertificates) > 0 {
			offered.Add(1)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	server.TLS = &tls.Config{
		MinVersion: tls.VersionTLS12,
		ClientAuth: tls.RequestClientCert,
	}
	server.StartTLS()
	defer server.Close()

	client, err := applyHTTPClientConfig(plainBase(), &core.HTTPClientConfig{
		TLSConfig: &core.TLSClientConfig{
			CertFile:           certPath,
			KeyFile:            keyPath,
			InsecureSkipVerify: true,
		},
	})
	require.NoError(t, err)

	_, status := httpConfigGet(t, client, server.URL)
	assert.Equal(t, http.StatusNoContent, status)
	assert.Equal(t, int64(1), offered.Load(), "the configured client certificate must be presented")
}

func TestApplyHTTPClientConfig_BasicAuthHeader(t *testing.T) {
	var gotUser, gotPass atomic.Value
	var gotOK atomic.Bool

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		gotUser.Store(user)
		gotPass.Store(pass)
		gotOK.Store(ok)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client, err := applyHTTPClientConfig(plainBase(), &core.HTTPClientConfig{
		BasicAuth: &core.BasicAuthConfig{Username: "amp", Password: httpConfigFixtureSecret},
	})
	require.NoError(t, err)

	httpConfigGet(t, client, server.URL)

	assert.True(t, gotOK.Load())
	assert.Equal(t, "amp", gotUser.Load())
	assert.Equal(t, httpConfigFixtureSecret, gotPass.Load())
}

// password_file wins over an inline password, and its trailing newline is
// trimmed — a Secret mounted as a file almost always has one.
func TestApplyHTTPClientConfig_BasicAuthPasswordFile(t *testing.T) {
	pwPath := filepath.Join(t.TempDir(), "pw")
	require.NoError(t, os.WriteFile(pwPath, []byte("from-file-value\n"), 0o600))

	var gotPass atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, pass, _ := r.BasicAuth()
		gotPass.Store(pass)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client, err := applyHTTPClientConfig(plainBase(), &core.HTTPClientConfig{
		BasicAuth: &core.BasicAuthConfig{Username: "amp", Password: "inline", PasswordFile: pwPath},
	})
	require.NoError(t, err)

	httpConfigGet(t, client, server.URL)
	assert.Equal(t, "from-file-value", gotPass.Load(),
		"password_file must win over the inline value and be newline-trimmed")
}

func TestApplyHTTPClientConfig_UnreadablePasswordFileFailsTargetBuild(t *testing.T) {
	_, err := applyHTTPClientConfig(plainBase(), &core.HTTPClientConfig{
		BasicAuth: &core.BasicAuthConfig{Username: "amp", PasswordFile: filepath.Join(t.TempDir(), "nope")},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "basic_auth.password_file")
}

func TestApplyHTTPClientConfig_BearerAuthorizationHeader(t *testing.T) {
	var gotHeader atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader.Store(r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	t.Run("explicit type", func(t *testing.T) {
		client, err := applyHTTPClientConfig(plainBase(), &core.HTTPClientConfig{
			Authorization: &core.AuthorizationConfig{Type: "Token", Credentials: "tok-value"},
		})
		require.NoError(t, err)
		httpConfigGet(t, client, server.URL)
		assert.Equal(t, "Token tok-value", gotHeader.Load())
	})

	t.Run("type defaults to Bearer", func(t *testing.T) {
		client, err := applyHTTPClientConfig(plainBase(), &core.HTTPClientConfig{
			Authorization: &core.AuthorizationConfig{Credentials: "tok-value"},
		})
		require.NoError(t, err)
		httpConfigGet(t, client, server.URL)
		assert.Equal(t, "Bearer tok-value", gotHeader.Load())
	})

	t.Run("credentials_file", func(t *testing.T) {
		credPath := filepath.Join(t.TempDir(), "cred")
		require.NoError(t, os.WriteFile(credPath, []byte("file-tok\n"), 0o600))

		client, err := applyHTTPClientConfig(plainBase(), &core.HTTPClientConfig{
			Authorization: &core.AuthorizationConfig{CredentialsFile: credPath},
		})
		require.NoError(t, err)
		httpConfigGet(t, client, server.URL)
		assert.Equal(t, "Bearer file-tok", gotHeader.Load())
	})
}

func TestApplyHTTPClientConfig_AuthorizationWithoutCredentialsFails(t *testing.T) {
	_, err := applyHTTPClientConfig(plainBase(), &core.HTTPClientConfig{
		Authorization: &core.AuthorizationConfig{Type: "Bearer"},
	})
	require.Error(t, err, "a bare scheme with no credential must not be sent")
}

// Upstream rejects the combination; guessing an order would make the header
// depend on implementation detail.
func TestApplyHTTPClientConfig_BasicAndAuthorizationTogetherIsRejected(t *testing.T) {
	_, err := applyHTTPClientConfig(plainBase(), &core.HTTPClientConfig{
		BasicAuth:     &core.BasicAuthConfig{Username: "u", Password: "p"},
		Authorization: &core.AuthorizationConfig{Credentials: "t"},
	})
	require.ErrorIs(t, err, ErrHTTPConfigAmbiguousAuth)
}

// The auth RoundTripper must not mutate the caller's request — publishers retry
// the same *http.Request.
func TestAuthRoundTripper_DoesNotMutateCallerRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client, err := applyHTTPClientConfig(plainBase(), &core.HTTPClientConfig{
		Authorization: &core.AuthorizationConfig{Credentials: "tok-value"},
	})
	require.NoError(t, err)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, nil)
	require.NoError(t, err)

	resp, err := client.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Empty(t, req.Header.Get("Authorization"),
		"the caller's request must come back exactly as it went in")
}

func TestApplyHTTPClientConfig_FollowRedirects(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	defer target.Close()

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer redirector.Close()

	t.Run("false returns the redirect itself", func(t *testing.T) {
		client, err := applyHTTPClientConfig(plainBase(), &core.HTTPClientConfig{
			FollowRedirects: redirectPolicy(false),
		})
		require.NoError(t, err)

		_, status := httpConfigGet(t, client, redirector.URL)
		assert.Equal(t, http.StatusFound, status)
	})

	t.Run("true follows", func(t *testing.T) {
		client, err := applyHTTPClientConfig(plainBase(), &core.HTTPClientConfig{
			FollowRedirects: redirectPolicy(true),
			// follow_redirects: true is the default, so pair it with something
			// else to force a real per-target client.
			BasicAuth: &core.BasicAuthConfig{Username: "u"},
		})
		require.NoError(t, err)

		_, status := httpConfigGet(t, client, redirector.URL)
		assert.Equal(t, http.StatusTeapot, status)
	})
}

func TestApplyHTTPClientConfig_NilBaseAndOddTransport(t *testing.T) {
	_, err := applyHTTPClientConfig(nil, &core.HTTPClientConfig{ProxyURL: "http://p:1"})
	require.Error(t, err)

	// A nil Transport is legitimate (the base HTTPPublisher has one) and must
	// clone http.DefaultTransport's shape rather than fail.
	client, err := applyHTTPClientConfig(&http.Client{}, &core.HTTPClientConfig{
		BasicAuth: &core.BasicAuthConfig{Username: "u", Password: "p"},
	})
	require.NoError(t, err)
	require.NotNil(t, client.Transport)

	// An unknown RoundTripper cannot be cloned safely and must be rejected
	// rather than silently ignored.
	odd := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("unused")
	})}
	_, err = applyHTTPClientConfig(odd, &core.HTTPClientConfig{ProxyURL: "http://p:1"})
	require.Error(t, err)
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// ============================================================================
// perTargetHTTPClient cache
// ============================================================================

func TestPerTargetHTTPClient_CachesAndSeparates(t *testing.T) {
	factory := newTestPublisherFactory(t)

	var builds atomic.Int64
	newBase := func() *http.Client {
		builds.Add(1)
		return plainBase()
	}

	cfgA := &core.HTTPClientConfig{ProxyURL: "http://proxy-a:8080"}
	cfgB := &core.HTTPClientConfig{ProxyURL: "http://proxy-b:8080"}

	first, err := factory.perTargetHTTPClient(shapeWebhook, cfgA, newBase)
	require.NoError(t, err)
	require.NotNil(t, first)

	second, err := factory.perTargetHTTPClient(shapeWebhook, cfgA.Clone(), newBase)
	require.NoError(t, err)
	assert.Same(t, first, second, "an identical http_config must reuse the cached client")

	other, err := factory.perTargetHTTPClient(shapeWebhook, cfgB, newBase)
	require.NoError(t, err)
	assert.NotSame(t, first, other, "a different proxy must get its own client")

	// A different publisher SHAPE must not reuse the webhook client: the base
	// tunings genuinely differ (100/10 idle conns vs 10/2).
	sameCfgOtherShape, err := factory.perTargetHTTPClient(shapeSlack, cfgA, newBase)
	require.NoError(t, err)
	assert.NotSame(t, first, sameCfgOtherShape)

	assert.Equal(t, int64(3), builds.Load(), "the cache must build once per (shape, http_config)")
}

func TestPerTargetHTTPClient_ZeroConfigReturnsNil(t *testing.T) {
	factory := newTestPublisherFactory(t)

	var builds atomic.Int64
	newBase := func() *http.Client {
		builds.Add(1)
		return plainBase()
	}

	client, err := factory.perTargetHTTPClient(shapeWebhook, nil, newBase)
	require.NoError(t, err)
	assert.Nil(t, client, "nil tells the caller to keep its own built-in client")
	assert.Equal(t, int64(0), builds.Load(), "no client may be built for a target without http_config")
	assert.Empty(t, factory.httpClientMap)
}

func TestPerTargetHTTPClient_BuildErrorIsNotCached(t *testing.T) {
	factory := newTestPublisherFactory(t)

	cfg := &core.HTTPClientConfig{
		TLSConfig: &core.TLSClientConfig{CAFile: filepath.Join(t.TempDir(), "missing.pem")},
	}

	_, err := factory.perTargetHTTPClient(shapeWebhook, cfg, plainBase)
	require.Error(t, err)
	assert.Empty(t, factory.httpClientMap, "a failed build must not poison the cache")

	// Still an error on the second attempt: the target stays skipped until the
	// file appears, rather than being silently downgraded to a plain client.
	_, err = factory.perTargetHTTPClient(shapeWebhook, cfg, plainBase)
	require.Error(t, err)
}

// ============================================================================
// helpers
// ============================================================================

// httpConfigGet issues one GET and returns the body and status. Centralised so
// every test closes its response body exactly once.
func httpConfigGet(t *testing.T, client *http.Client, targetURL string) (string, int) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, targetURL, nil)
	require.NoError(t, err)

	resp, err := client.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return string(body), resp.StatusCode
}

// writeSelfSignedPair writes a throwaway self-signed certificate/key pair,
// generated fresh per test run, and returns their paths. Nothing here has any
// life outside this process.
func writeSelfSignedPair(t *testing.T) (certPath, keyPath string) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "amp-http-config-test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:              []string{"localhost"},
		IsCA:                  true,
		BasicConstraintsValid: true,
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)

	keyDER, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)

	dir := t.TempDir()
	certPath = filepath.Join(dir, "client.pem")
	keyPath = filepath.Join(dir, "client-key.pem")
	require.NoError(t, os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600))
	require.NoError(t, os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600))
	return certPath, keyPath
}
