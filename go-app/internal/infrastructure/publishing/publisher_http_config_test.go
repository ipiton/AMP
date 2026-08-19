package publishing

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ipiton/AMP/internal/core"
)

// ============================================================================
// FU-HTTP-CONFIG: client cache keys must include the http_config fingerprint
// ============================================================================
//
// This is the THIRD occurrence of one defect class. The telegram cache was keyed
// on bot_token alone (wave re-review Minor 5) and the pagerduty/rootly caches on
// the routing key alone (wave 5 finding I3): in both cases the first target
// built for a credential pinned its endpoint for every later target sharing that
// credential. Adding http_config re-opens exactly the same hole one level down —
// same URL, same token, DIFFERENT proxy or client certificate — so every cache
// gets an explicit test here rather than a comment.

func newHTTPConfigTestAlert() *core.EnrichedAlert {
	return &core.EnrichedAlert{Alert: &core.Alert{
		Fingerprint: "fp-http-config",
		AlertName:   "HTTPConfigTest",
		Status:      core.StatusFiring,
		StartsAt:    time.Now(),
		Labels:      map[string]string{"severity": "warning"},
	}}
}

func httpConfigTarget(name, targetType, targetURL string, headers map[string]string, cfg *core.HTTPClientConfig) *core.PublishingTarget {
	return &core.PublishingTarget{
		Name:       name,
		Type:       targetType,
		URL:        targetURL,
		Enabled:    true,
		Headers:    headers,
		HTTPConfig: cfg,
	}
}

func TestPublisherFactory_CacheKeysSeparateByHTTPConfig(t *testing.T) {
	proxyA := &core.HTTPClientConfig{ProxyURL: "http://proxy-a:8080"}
	proxyB := &core.HTTPClientConfig{ProxyURL: "http://proxy-b:8080"}

	cases := []struct {
		name       string
		targetType string
		url        string
		headers    map[string]string
		mapLen     func(*PublisherFactory) int
	}{
		{
			name:       "telegram",
			targetType: string(TargetTypeTelegram),
			url:        "https://api.telegram.org",
			headers:    map[string]string{"bot_token": "same-token", "chat_id": "1"},
			mapLen:     func(f *PublisherFactory) int { return len(f.telegramClientMap) },
		},
		{
			name:       "slack",
			targetType: string(TargetTypeSlack),
			url:        "https://hooks.slack.com/services/same",
			mapLen:     func(f *PublisherFactory) int { return len(f.slackClientMap) },
		},
		{
			name:       "pagerduty",
			targetType: string(TargetTypePagerDuty),
			url:        "https://events.pagerduty.com",
			headers:    map[string]string{"routing_key": "same-key"},
			mapLen:     func(f *PublisherFactory) int { return len(f.pagerDutyClientMap) },
		},
		{
			name:       "rootly",
			targetType: string(TargetTypeRootly),
			url:        "https://api.rootly.com/v1",
			headers:    map[string]string{"Authorization": "Bearer same-key"},
			mapLen:     func(f *PublisherFactory) int { return len(f.rootlyClientMap) },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			factory := newTestPublisherFactory(t)

			// Same URL, same credential, no http_config: ONE client (unchanged
			// behaviour for every pre-existing target).
			_, err := factory.CreatePublisherForTarget(httpConfigTarget("plain-1", tc.targetType, tc.url, tc.headers, nil))
			require.NoError(t, err)
			_, err = factory.CreatePublisherForTarget(httpConfigTarget("plain-2", tc.targetType, tc.url, tc.headers, nil))
			require.NoError(t, err)
			require.Equal(t, 1, tc.mapLen(factory), "identical targets must share one client")

			// Adding an http_config must NOT reuse the plain client.
			_, err = factory.CreatePublisherForTarget(httpConfigTarget("proxy-a", tc.targetType, tc.url, tc.headers, proxyA))
			require.NoError(t, err)
			assert.Equal(t, 2, tc.mapLen(factory), "a target with http_config must not reuse the plain client")

			// The SAME http_config reuses its own client.
			_, err = factory.CreatePublisherForTarget(httpConfigTarget("proxy-a-again", tc.targetType, tc.url, tc.headers, proxyA.Clone()))
			require.NoError(t, err)
			assert.Equal(t, 2, tc.mapLen(factory), "an identical http_config must share one client")

			// A DIFFERENT proxy gets its own client — the whole point.
			_, err = factory.CreatePublisherForTarget(httpConfigTarget("proxy-b", tc.targetType, tc.url, tc.headers, proxyB))
			require.NoError(t, err)
			assert.Equal(t, 3, tc.mapLen(factory),
				"same URL + same credential + different proxy must NOT share a client")
		})
	}
}

// The per-shape *http.Client cache must be reused across all of those, once per
// (publisher shape, http_config).
func TestPublisherFactory_HTTPClientCacheIsSharedPerShape(t *testing.T) {
	factory := newTestPublisherFactory(t)
	cfg := &core.HTTPClientConfig{ProxyURL: "http://proxy:8080"}

	for _, name := range []string{"t1", "t2", "t3"} {
		_, err := factory.CreatePublisherForTarget(httpConfigTarget(
			name,
			string(TargetTypeTelegram),
			"https://api.telegram.org",
			map[string]string{"bot_token": "tok-" + name, "chat_id": "1"},
			cfg.Clone(),
		))
		require.NoError(t, err)
	}

	assert.Len(t, factory.telegramClientMap, 3, "three distinct bot tokens are three clients")
	assert.Len(t, factory.httpClientMap, 1, "but they share ONE *http.Client for one http_config")
}

// A target whose http_config cannot be built must fail loudly and be skipped,
// never silently downgraded to a plain client. Every other target keeps working.
func TestPublisherFactory_UnreadableTLSFileSkipsTargetOnly(t *testing.T) {
	factory := newTestPublisherFactory(t)

	broken := &core.HTTPClientConfig{
		TLSConfig: &core.TLSClientConfig{CAFile: filepath.Join(t.TempDir(), "missing-ca.pem")},
	}

	for _, tc := range []struct {
		targetType string
		url        string
		headers    map[string]string
	}{
		{string(TargetTypeWebhook), "https://hooks.example.com/a", nil},
		{string(TargetTypeSlack), "https://hooks.slack.com/services/a", nil},
		{string(TargetTypeTelegram), "https://api.telegram.org", map[string]string{"bot_token": "t", "chat_id": "1"}},
		{string(TargetTypePagerDuty), "https://events.pagerduty.com", map[string]string{"routing_key": "k"}},
		{string(TargetTypeRootly), "https://api.rootly.com/v1", map[string]string{"Authorization": "Bearer k"}},
	} {
		t.Run(tc.targetType, func(t *testing.T) {
			publisher, err := factory.CreatePublisherForTarget(
				httpConfigTarget("broken", tc.targetType, tc.url, tc.headers, broken))
			require.Error(t, err, "an unreadable ca_file must fail the target build")
			assert.Nil(t, publisher)
			assert.Contains(t, err.Error(), "ca_file")
			assert.Contains(t, err.Error(), `"broken"`, "the error must name the skipped target")
		})
	}

	// A healthy target built from the same factory still works — the failure is
	// per-target, not process-wide.
	healthy, err := factory.CreatePublisherForTarget(
		httpConfigTarget("healthy", string(TargetTypeWebhook), "https://hooks.example.com/b", nil, nil))
	require.NoError(t, err)
	assert.NotNil(t, healthy)
}

// CreateBasicPublisherForTarget is the queue's path for webhook targets. Its
// http_config must reach the wire — proven end to end against an httptest
// server rather than by inspecting the client.
func TestCreateBasicPublisherForTarget_AppliesHTTPConfigOnTheWire(t *testing.T) {
	var gotAuth atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth.Store(r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	factory := newTestPublisherFactory(t)

	target := httpConfigTarget("wire", string(TargetTypeWebhook), server.URL, nil, &core.HTTPClientConfig{
		Authorization: &core.AuthorizationConfig{Credentials: "tok-on-the-wire"},
	})
	target.Format = core.FormatAlertmanager

	publisher, err := factory.CreateBasicPublisherForTarget(target)
	require.NoError(t, err)

	require.NoError(t, publisher.Publish(t.Context(), newHTTPConfigTestAlert(), target))
	assert.Equal(t, "Bearer tok-on-the-wire", gotAuth.Load(),
		"the queue's basic webhook path must honour http_config")
}

func TestCreateBasicPublisherForTarget_NoHTTPConfigStillPublishes(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		assert.Empty(t, r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	factory := newTestPublisherFactory(t)

	target := httpConfigTarget("plain", string(TargetTypeWebhook), server.URL, nil, nil)
	target.Format = core.FormatAlertmanager

	publisher, err := factory.CreateBasicPublisherForTarget(target)
	require.NoError(t, err)
	require.NoError(t, publisher.Publish(t.Context(), newHTTPConfigTestAlert(), target))
	assert.Equal(t, int64(1), calls.Load())
	assert.Empty(t, factory.httpClientMap, "no per-target client may be allocated without http_config")
}

func TestCreateBasicPublisherForTarget_UnreadableFileSkipsTarget(t *testing.T) {
	factory := newTestPublisherFactory(t)

	target := httpConfigTarget("broken", string(TargetTypeWebhook), "https://hooks.example.com/a", nil,
		&core.HTTPClientConfig{
			BasicAuth: &core.BasicAuthConfig{Username: "u", PasswordFile: filepath.Join(t.TempDir(), "nope")},
		})

	publisher, err := factory.CreateBasicPublisherForTarget(target)
	require.Error(t, err)
	assert.Nil(t, publisher)
	assert.Contains(t, err.Error(), "password_file")
}

func TestCreateBasicPublisherForTarget_NilTarget(t *testing.T) {
	factory := newTestPublisherFactory(t)
	_, err := factory.CreateBasicPublisherForTarget(nil)
	require.Error(t, err)
}

// The factory is called concurrently by the queue's worker pool, so the new
// httpClientMap must be race-free like every other cache. Run with -race.
func TestPublisherFactory_HTTPClientCacheIsRaceFree(t *testing.T) {
	factory := newTestPublisherFactory(t)

	configs := []*core.HTTPClientConfig{
		{ProxyURL: "http://proxy-a:8080"},
		{ProxyURL: "http://proxy-b:8080"},
		{Authorization: &core.AuthorizationConfig{Credentials: "tok"}},
		nil,
	}

	targetTypes := []string{
		string(TargetTypeWebhook),
		string(TargetTypeSlack),
		string(TargetTypeTelegram),
		string(TargetTypePagerDuty),
		string(TargetTypeRootly),
	}
	headersFor := map[string]map[string]string{
		string(TargetTypeTelegram):  {"bot_token": "t", "chat_id": "1"},
		string(TargetTypePagerDuty): {"routing_key": "k"},
		string(TargetTypeRootly):    {"Authorization": "Bearer k"},
	}
	urlFor := map[string]string{
		string(TargetTypeWebhook):   "https://hooks.example.com/a",
		string(TargetTypeSlack):     "https://hooks.slack.com/services/a",
		string(TargetTypeTelegram):  "https://api.telegram.org",
		string(TargetTypePagerDuty): "https://events.pagerduty.com",
		string(TargetTypeRootly):    "https://api.rootly.com/v1",
	}

	var wg sync.WaitGroup
	for i := range 40 {
		for _, targetType := range targetTypes {
			wg.Add(1)
			go func(idx int, tt string) {
				defer wg.Done()
				target := httpConfigTarget("t", tt, urlFor[tt], headersFor[tt], configs[idx%len(configs)].Clone())
				_, err := factory.CreatePublisherForTarget(target)
				assert.NoError(t, err)

				_, err = factory.CreateBasicPublisherForTarget(target)
				assert.NoError(t, err)
			}(i, targetType)
		}
	}
	wg.Wait()

	// 3 non-nil configs x (5 enhanced shapes minus the two that share none) —
	// assert only the invariant that matters: no config produced more than one
	// client per shape, i.e. the cache actually deduped under contention.
	assert.LessOrEqual(t, len(factory.httpClientMap), 3*(len(targetTypes)+1))
	assert.NotEmpty(t, factory.httpClientMap)
}
