package publishing

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ipiton/AMP/internal/core"
)

// Slice-1 review finding I2: this client posted trigger events to "/v2/events",
// which is not a PagerDuty endpoint (Events API v2 enqueues at "/v2/enqueue"),
// so every PagerDuty notification 404'd. These tests pin the final request path
// for each URL shape a target can legitimately carry — a bare base, the full
// upstream endpoint (routing.PagerDutyConfig.Defaults() produces exactly that),
// the historical "/v2/events" shape a K8s Secret may have been written to
// compensate with, and trailing slashes.
func TestPagerDutyClient_TriggerEvent_HitsEnqueuePath(t *testing.T) {
	tests := []struct {
		name      string
		urlSuffix string
	}{
		{name: "bare base URL", urlSuffix: ""},
		{name: "base URL with trailing slash", urlSuffix: "/"},
		{name: "full upstream events endpoint", urlSuffix: "/v2/enqueue"},
		{name: "full endpoint with trailing slash", urlSuffix: "/v2/enqueue/"},
		{name: "legacy (wrong) /v2/events endpoint", urlSuffix: "/v2/events"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var (
				mu      sync.Mutex
				gotPath string
				calls   int
			)

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				gotPath = r.URL.Path
				calls++
				mu.Unlock()
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte(`{"status":"success","message":"Event processed","dedup_key":"dk"}`))
			}))
			defer srv.Close()

			client := NewPagerDutyEventsClient(PagerDutyClientConfig{
				BaseURL:    srv.URL + tt.urlSuffix,
				Timeout:    2 * time.Second,
				MaxRetries: 1,
				RateLimit:  600,
			}, testLogger())

			_, err := client.TriggerEvent(context.Background(), &TriggerEventRequest{
				RoutingKey: "rk-1",
				Payload: TriggerEventPayload{
					Summary:  "endpoint test",
					Source:   "amp",
					Severity: SeverityCritical,
				},
			})
			require.NoError(t, err)

			mu.Lock()
			defer mu.Unlock()
			assert.Equal(t, 1, calls)
			assert.Equal(t, "/v2/enqueue", gotPath,
				"whatever URL shape the target carries, the request must land on the real Events API v2 path exactly once")
		})
	}
}

// The change-events endpoint is a different path and must not be affected by
// the base-URL normalisation.
func TestPagerDutyClient_SendChangeEvent_HitsChangeEnqueuePath(t *testing.T) {
	var (
		mu      sync.Mutex
		gotPath string
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotPath = r.URL.Path
		mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"status":"success","message":"Change event processed"}`))
	}))
	defer srv.Close()

	client := NewPagerDutyEventsClient(PagerDutyClientConfig{
		BaseURL:    srv.URL + "/v2/enqueue",
		Timeout:    2 * time.Second,
		MaxRetries: 1,
		RateLimit:  600,
	}, testLogger())

	_, err := client.SendChangeEvent(context.Background(), &ChangeEventRequest{
		RoutingKey: "rk-1",
		Payload:    ChangeEventPayload{Summary: "deploy"},
	})
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, "/v2/change/enqueue", gotPath)
}

// A config-provisioned PagerDuty target (built by
// businesspublishing.BuildConfigTargets, which strips the upstream endpoint
// path itself) must reach the same place as a K8s-sourced one. This asserts the
// combination end to end through the live queue's publisher construction.
func TestPagerDutyTarget_ConfigAndK8sShapesReachSameEndpoint(t *testing.T) {
	var (
		mu    sync.Mutex
		paths []string
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"status":"success","message":"Event processed","dedup_key":"dk"}`))
	}))
	defer srv.Close()

	f := newTestPublisherFactory(t)
	q := newQueueForFactory(t, f)

	alert := &core.EnrichedAlert{
		Alert: &core.Alert{
			Fingerprint: "fp-1",
			AlertName:   "HighCPUUsage",
			Status:      core.StatusFiring,
			StartsAt:    time.Now(),
			Labels:      map[string]string{"severity": "critical"},
			Annotations: map[string]string{"summary": "s"},
		},
	}

	for _, target := range []*core.PublishingTarget{
		// K8s-sourced shape: bare base.
		{Name: "pd-k8s", Type: string(TargetTypePagerDuty), URL: srv.URL, Headers: map[string]string{"routing_key": "rk-k8s"}},
		// Config-provisioned shape: cfg: name, base already stripped by the builder.
		{Name: "cfg:team-x/pagerduty0", Type: string(TargetTypePagerDuty), URL: srv.URL, Headers: map[string]string{"routing_key": "rk-cfg"}, Receivers: []string{"team-x"}},
	} {
		publisher, err := q.createPublisherForJob(&PublishingJob{Target: target})
		require.NoError(t, err)
		require.NoError(t, publisher.Publish(context.Background(), alert, target))
	}

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []string{"/v2/enqueue", "/v2/enqueue"}, paths)
}
