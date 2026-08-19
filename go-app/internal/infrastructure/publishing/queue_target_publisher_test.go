package publishing

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/ipiton/AMP/internal/core"
	v2 "github.com/ipiton/AMP/pkg/metrics/v2"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Final review finding 6: the publishing queue is the only live publish path in
// the process, and it built publishers with CreatePublisher(target.Type) — a
// type-only factory that cannot pass per-target credentials. Every "enhanced"
// publisher was therefore unreachable at runtime; for Telegram that meant
// EnhancedTelegramPublisher (bot token, chat_id, thread, Bot API sendMessage)
// never ran at all, despite the docs claiming runtime support. Its only
// non-test caller was CreatePublisherForTarget, whose only caller
// (parallel_publisher.go) has no non-test constructor.

func newTestPublisherFactory(t *testing.T) *PublisherFactory {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	f := NewPublisherFactory(NewAlertFormatter(""), logger, nil, "https://amp.example.com")
	// Shutdown stops all three cache cleanup workers (Slack, Rootly,
	// PagerDuty) — see item 2, FU-WAVE3-RELIABILITY.
	t.Cleanup(f.Shutdown)
	return f
}

func newQueueForFactory(t *testing.T, f *PublisherFactory) *PublishingQueue {
	t.Helper()
	// Own Prometheus registry per queue: NewPublishingQueue otherwise falls
	// back to v2.NewRegistry() on the DEFAULT registerer, and a second queue in
	// the same process panics with "duplicate metrics collector registration".
	return NewPublishingQueue(f, nil, nil, PublishingQueueConfig{
		HighPriorityQueueSize:   1,
		MediumPriorityQueueSize: 1,
		LowPriorityQueueSize:    1,
		Metrics:                 v2.NewRegistry(v2.WithPrometheusRegisterer(prometheus.NewRegistry())).Publishing,
	}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestCreatePublisherForJob_TelegramReachesEnhancedPublisher(t *testing.T) {
	f := newTestPublisherFactory(t)
	q := newQueueForFactory(t, f)

	job := &PublishingJob{
		Target: &core.PublishingTarget{
			Name: "oncall-telegram",
			Type: string(TargetTypeTelegram),
			URL:  "https://api.telegram.example",
			Headers: map[string]string{
				"bot_token": "123456:AAAA-test-token",
				"chat_id":   "-1001234567890",
			},
		},
	}

	publisher, err := q.createPublisherForJob(job)
	require.NoError(t, err)
	require.IsType(t, &EnhancedTelegramPublisher{}, publisher,
		"the live queue path must build the ENHANCED telegram publisher, not a bare HTTPPublisher")
}

// TestCreatePublisherForJob_TelegramActuallyCallsBotAPI closes the loop over
// HTTP: the enhanced publisher must hit the Telegram Bot API sendMessage
// endpoint derived from the target's URL + bot_token, with the target's
// chat_id. The basic TelegramPublisher would instead POST the generic webhook
// payload straight at target.URL.
func TestCreatePublisherForJob_TelegramActuallyCallsBotAPI(t *testing.T) {
	const botToken = "123456:AAAA-test-token"
	const chatID = "-1001234567890"

	var (
		mu       sync.Mutex
		gotPath  string
		gotChat  string
		gotCalls int
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var msg TelegramMessage
		_ = json.Unmarshal(body, &msg)

		mu.Lock()
		gotPath = r.URL.Path
		gotChat = msg.ChatID
		gotCalls++
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":42}}`))
	}))
	defer srv.Close()

	f := newTestPublisherFactory(t)
	q := newQueueForFactory(t, f)

	target := &core.PublishingTarget{
		Name:    "oncall-telegram",
		Type:    string(TargetTypeTelegram),
		URL:     srv.URL,
		Headers: map[string]string{"bot_token": botToken, "chat_id": chatID},
	}

	publisher, err := q.createPublisherForJob(&PublishingJob{Target: target})
	require.NoError(t, err)

	err = publisher.Publish(context.Background(), &core.EnrichedAlert{
		Alert: &core.Alert{
			Fingerprint: "fp-telegram-1",
			AlertName:   "HighCPUUsage",
			Status:      core.StatusFiring,
			StartsAt:    time.Now(),
			Labels:      map[string]string{"severity": "critical"},
			Annotations: map[string]string{"summary": "queue path reaches the Bot API"},
		},
	}, target)
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, 1, gotCalls)
	assert.Equal(t, "/bot"+botToken+"/sendMessage", gotPath,
		"must hit the Telegram Bot API sendMessage endpoint, not the raw target URL")
	assert.Equal(t, chatID, gotChat, "the target's chat_id must be honored")
}

// TestCreatePublisherForJob_MissingTelegramCredentialsDegradesGracefully pins
// the fallback that makes the wiring change safe: a telegram target without
// bot_token/chat_id must still yield a working (basic) publisher, not an error
// that fails the job.
func TestCreatePublisherForJob_MissingTelegramCredentialsDegradesGracefully(t *testing.T) {
	f := newTestPublisherFactory(t)
	q := newQueueForFactory(t, f)

	publisher, err := q.createPublisherForJob(&PublishingJob{
		Target: &core.PublishingTarget{Name: "broken-telegram", Type: string(TargetTypeTelegram)},
	})
	require.NoError(t, err)
	require.IsType(t, &TelegramPublisher{}, publisher)
}

// TestCreatePublisherForJob_WebhookStaysOnBasicPublisher documents the
// deliberate scope limit: enabling EnhancedWebhookPublisher's
// WebhookValidator over every existing target is a separate behaviour change.
func TestCreatePublisherForJob_WebhookStaysOnBasicPublisher(t *testing.T) {
	f := newTestPublisherFactory(t)
	q := newQueueForFactory(t, f)

	for _, targetType := range []TargetType{TargetTypeWebhook, TargetTypeAlertmanager} {
		publisher, err := q.createPublisherForJob(&PublishingJob{
			Target: &core.PublishingTarget{Name: "ops", Type: string(targetType), URL: "http://ops.internal/hook"},
		})
		require.NoError(t, err)
		require.IsType(t, &WebhookPublisher{}, publisher, "target type %s", targetType)
	}
}

// TestCreatePublisherForTarget_ConcurrentIsRaceFree is the COUPLED MANDATORY
// half of finding 6. CreatePublisherForTarget writes four per-target client
// caches (rootly/pagerduty/slack/telegram); only emailClientMap was guarded.
// Routing the live queue through this function means the worker pool now calls
// it concurrently — one unguarded map write away from a "concurrent map writes"
// runtime fatal. Run with -race to see the guard actually working.
func TestCreatePublisherForTarget_ConcurrentIsRaceFree(t *testing.T) {
	f := newTestPublisherFactory(t)

	targets := []*core.PublishingTarget{
		{Name: "tg-1", Type: string(TargetTypeTelegram), URL: "https://api.telegram.example",
			Headers: map[string]string{"bot_token": "tok-1", "chat_id": "-100"}},
		{Name: "tg-2", Type: string(TargetTypeTelegram), URL: "https://api.telegram.example",
			Headers: map[string]string{"bot_token": "tok-2", "chat_id": "-200"}},
		{Name: "slack-1", Type: string(TargetTypeSlack), URL: "https://hooks.slack.example/services/AAA"},
		{Name: "slack-2", Type: string(TargetTypeSlack), URL: "https://hooks.slack.example/services/BBB"},
		{Name: "pd-1", Type: string(TargetTypePagerDuty), URL: "https://events.pagerduty.example",
			Headers: map[string]string{"routing_key": "rk-1"}},
		{Name: "rootly-1", Type: string(TargetTypeRootly), URL: "https://rootly.example",
			Headers: map[string]string{"Authorization": "Bearer rk-rootly-1"}},
		{Name: "mail-1", Type: string(TargetTypeEmail),
			Headers: map[string]string{"smtp_host": "smtp.example", "smtp_port": "587"}},
	}

	const goroutines = 32
	var wg sync.WaitGroup
	errs := make(chan error, goroutines*len(targets))

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// Every goroutine walks every target, starting at a different
			// offset, so the same map key is created and read concurrently.
			for j := range targets {
				target := targets[(i+j)%len(targets)]
				p, err := f.CreatePublisherForTarget(target)
				if err != nil {
					errs <- fmt.Errorf("%s: %w", target.Name, err)
					continue
				}
				if p == nil {
					errs <- fmt.Errorf("%s: nil publisher", target.Name)
				}
			}
		}(i)
	}

	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	// Each distinct credential must have produced exactly one cached client —
	// the double-checked locking must not create duplicates under contention.
	f.clientMu.RLock()
	defer f.clientMu.RUnlock()
	assert.Len(t, f.telegramClientMap, 2)
	assert.Len(t, f.slackClientMap, 2)
	assert.Len(t, f.pagerDutyClientMap, 1)
	assert.Len(t, f.rootlyClientMap, 1)
}

// TestCreatePublisherForTarget_TelegramCacheKeyIncludesAPIURL is wave re-review
// Minor 5: the Telegram client cache was keyed on bot_token alone, while
// NewHTTPTelegramClient bakes in BOTH the api_url and the token. The first
// target built for a token therefore pinned its api_url for every later target
// sharing that token — a second target pointing the same bot at a different API
// base silently reused the first endpoint.
func TestCreatePublisherForTarget_TelegramCacheKeyIncludesAPIURL(t *testing.T) {
	const botToken = "123456:shared-token"

	newTarget := func(name, apiURL string) *core.PublishingTarget {
		return &core.PublishingTarget{
			Name:    name,
			Type:    string(TargetTypeTelegram),
			URL:     apiURL,
			Headers: map[string]string{"bot_token": botToken, "chat_id": "-100"},
		}
	}

	f := newTestPublisherFactory(t)

	_, err := f.CreatePublisherForTarget(newTarget("tg-primary", "https://api.telegram.example"))
	require.NoError(t, err)
	_, err = f.CreatePublisherForTarget(newTarget("tg-proxy", "https://telegram-proxy.internal"))
	require.NoError(t, err)

	// Read the cache size under the same lock the factory uses. Must not be held
	// across a CreatePublisherForTarget call — that takes the lock itself.
	telegramClients := func() int {
		f.clientMu.RLock()
		defer f.clientMu.RUnlock()
		return len(f.telegramClientMap)
	}

	assert.Equal(t, 2, telegramClients(),
		"the same bot token behind two different API bases must yield two clients, not one pinned to the first base")

	// Re-requesting an existing pair must reuse, not grow the cache.
	_, err = f.CreatePublisherForTarget(newTarget("tg-primary-again", "https://api.telegram.example"))
	require.NoError(t, err)
	assert.Equal(t, 2, telegramClients(), "an identical (api_url, bot_token) pair must reuse its cached client")
}

// TestCreatePublisherForJob_TelegramHonoursTargetAPIURL is the behavioural half:
// two targets sharing a bot token must each hit their OWN API base.
func TestCreatePublisherForJob_TelegramHonoursTargetAPIURL(t *testing.T) {
	const botToken = "123456:shared-token"

	var mu sync.Mutex
	hits := map[string]int{}
	handler := func(label string) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			mu.Lock()
			hits[label]++
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1}}`))
		}
	}
	srvA := httptest.NewServer(handler("a"))
	defer srvA.Close()
	srvB := httptest.NewServer(handler("b"))
	defer srvB.Close()

	f := newTestPublisherFactory(t)
	q := newQueueForFactory(t, f)

	alert := &core.EnrichedAlert{Alert: &core.Alert{
		Fingerprint: "fp-shared-token",
		AlertName:   "SharedToken",
		Status:      core.StatusFiring,
		StartsAt:    time.Now(),
		Labels:      map[string]string{"severity": "warning"},
	}}

	for _, srv := range []*httptest.Server{srvA, srvB} {
		target := &core.PublishingTarget{
			Name:    "tg-" + srv.URL,
			Type:    string(TargetTypeTelegram),
			URL:     srv.URL,
			Headers: map[string]string{"bot_token": botToken, "chat_id": "-100"},
		}
		publisher, err := q.createPublisherForJob(&PublishingJob{Target: target})
		require.NoError(t, err)
		require.NoError(t, publisher.Publish(context.Background(), alert, target))
	}

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, 1, hits["a"], "the first target's own API base must be used")
	assert.Equal(t, 1, hits["b"], "the second target must NOT be silently redirected to the first target's API base")
}

// === FU-SLACK-PAGERDUTY-QUEUE-PATH (wave 5) ===
//
// The wiring itself (createPublisherForJob routing Slack/PagerDuty through
// CreatePublisherForTarget, and the clientMu guard covering
// slackClientMap/pagerDutyClientMap) landed already in the same commit as the
// telegram fix above — both were part of "route live queue to target-aware
// publishers" (see queue.go's createPublisherForJob switch: TargetTypeSlack
// and TargetTypePagerDuty are already in the CreatePublisherForTarget case,
// and TestCreatePublisherForTarget_ConcurrentIsRaceFree above already drives
// slack/pagerduty client-map creation under -race). What was missing was the
// end-to-end proof mirroring the telegram tests: a queue-path job for these
// two target types must reach the ENHANCED publisher and actually hit the
// provider's real API shape, not just resolve to the right Go type.

// TestCreatePublisherForJob_SlackReachesEnhancedPublisher mirrors
// TestCreatePublisherForJob_TelegramReachesEnhancedPublisher: the live queue
// path must build EnhancedSlackPublisher for a Slack target, not the bare
// SlackPublisher webhook-only shim.
func TestCreatePublisherForJob_SlackReachesEnhancedPublisher(t *testing.T) {
	f := newTestPublisherFactory(t)
	q := newQueueForFactory(t, f)

	job := &PublishingJob{
		Target: &core.PublishingTarget{
			Name: "oncall-slack",
			Type: string(TargetTypeSlack),
			URL:  "https://hooks.slack.example/services/T000/B000/XXXX",
		},
	}

	publisher, err := q.createPublisherForJob(job)
	require.NoError(t, err)
	require.IsType(t, &EnhancedSlackPublisher{}, publisher,
		"the live queue path must build the ENHANCED slack publisher, not the basic webhook-only one")
}

// TestCreatePublisherForJob_SlackActuallyPostsToWebhook closes the loop over
// HTTP, mirroring TestCreatePublisherForJob_TelegramActuallyCallsBotAPI: the
// enhanced publisher must POST a Slack message payload straight at the
// target's webhook URL.
func TestCreatePublisherForJob_SlackActuallyPostsToWebhook(t *testing.T) {
	var (
		mu       sync.Mutex
		gotCalls int
		gotMsg   SlackMessage
		gotPath  string
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var msg SlackMessage
		_ = json.Unmarshal(body, &msg)

		mu.Lock()
		gotCalls++
		gotMsg = msg
		gotPath = r.URL.Path
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"ts":"1234567890.000001"}`))
	}))
	defer srv.Close()

	f := newTestPublisherFactory(t)
	q := newQueueForFactory(t, f)

	target := &core.PublishingTarget{
		Name: "oncall-slack",
		Type: string(TargetTypeSlack),
		URL:  srv.URL + "/services/T000/B000/XXXX",
	}

	publisher, err := q.createPublisherForJob(&PublishingJob{Target: target})
	require.NoError(t, err)

	err = publisher.Publish(context.Background(), &core.EnrichedAlert{
		Alert: &core.Alert{
			Fingerprint: "fp-slack-1",
			AlertName:   "HighCPUUsage",
			Status:      core.StatusFiring,
			StartsAt:    time.Now(),
			Labels:      map[string]string{"severity": "critical"},
			Annotations: map[string]string{"summary": "queue path reaches the Slack webhook"},
		},
	}, target)
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, 1, gotCalls)
	assert.Equal(t, "/services/T000/B000/XXXX", gotPath,
		"must POST straight at the target's webhook path, not some derived endpoint")

	// CONTENT, not just transport (review wave 5, finding C1/I1): before the
	// fix this was {"text":""} with no blocks/attachments — Slack's real API
	// answers a body like that with 400 invalid_payload. formatSlack never set
	// "text" at all, and buildMessage's []interface{} assertions never matched
	// formatSlack's []map[string]any blocks/attachments, so every field here
	// came back empty/nil regardless of what the alert said.
	require.NotEmpty(t, gotMsg.Text, "the wire body must carry Slack's required fallback text, not an empty string Slack would reject")
	assert.Contains(t, gotMsg.Text, "HighCPUUsage", "the fallback text must actually describe THIS alert")
	require.NotEmpty(t, gotMsg.Blocks, "the Block Kit body must not be empty — formatSlack's blocks must survive buildMessage intact")
	assert.NotEmpty(t, gotMsg.Attachments, "the color-coded attachment must survive buildMessage intact")
}

// TestCreatePublisherForJob_MissingSlackURLDegradesGracefully mirrors the
// telegram credential-fallback test: a Slack target with no webhook URL must
// still yield a working (basic) publisher rather than failing the job.
func TestCreatePublisherForJob_MissingSlackURLDegradesGracefully(t *testing.T) {
	f := newTestPublisherFactory(t)
	q := newQueueForFactory(t, f)

	publisher, err := q.createPublisherForJob(&PublishingJob{
		Target: &core.PublishingTarget{Name: "broken-slack", Type: string(TargetTypeSlack)},
	})
	require.NoError(t, err)
	require.IsType(t, &SlackPublisher{}, publisher)
}

// TestCreatePublisherForJob_PagerDutyReachesEnhancedPublisher mirrors the
// telegram type-check test for PagerDuty.
func TestCreatePublisherForJob_PagerDutyReachesEnhancedPublisher(t *testing.T) {
	f := newTestPublisherFactory(t)
	q := newQueueForFactory(t, f)

	job := &PublishingJob{
		Target: &core.PublishingTarget{
			Name:    "oncall-pagerduty",
			Type:    string(TargetTypePagerDuty),
			URL:     "https://events.pagerduty.example",
			Headers: map[string]string{"routing_key": "rk-oncall-1"},
		},
	}

	publisher, err := q.createPublisherForJob(job)
	require.NoError(t, err)
	require.IsType(t, &EnhancedPagerDutyPublisher{}, publisher,
		"the live queue path must build the ENHANCED pagerduty publisher, not the basic HTTP one")
}

// TestCreatePublisherForJob_PagerDutyActuallyCallsEventsAPI closes the loop
// over HTTP: a firing alert must reach PagerDuty's Events API v2 trigger
// endpoint (POST /v2/events) carrying the target's routing_key.
func TestCreatePublisherForJob_PagerDutyActuallyCallsEventsAPI(t *testing.T) {
	const routingKey = "rk-oncall-1"

	var (
		mu            sync.Mutex
		gotPath       string
		gotRoutingKey string
		gotSummary    string
		gotSeverity   string
		gotSource     string
		gotCalls      int
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req TriggerEventRequest
		_ = json.Unmarshal(body, &req)

		mu.Lock()
		gotPath = r.URL.Path
		gotRoutingKey = req.RoutingKey
		gotSummary = req.Payload.Summary
		gotSeverity = req.Payload.Severity
		gotSource = req.Payload.Source
		gotCalls++
		mu.Unlock()

		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"status":"success","message":"Event processed","dedup_key":"fp-pagerduty-1"}`))
	}))
	defer srv.Close()

	f := newTestPublisherFactory(t)
	q := newQueueForFactory(t, f)

	target := &core.PublishingTarget{
		Name:    "oncall-pagerduty",
		Type:    string(TargetTypePagerDuty),
		URL:     srv.URL,
		Headers: map[string]string{"routing_key": routingKey},
	}

	publisher, err := q.createPublisherForJob(&PublishingJob{Target: target})
	require.NoError(t, err)

	err = publisher.Publish(context.Background(), &core.EnrichedAlert{
		Alert: &core.Alert{
			Fingerprint: "fp-pagerduty-1",
			AlertName:   "HighCPUUsage",
			Status:      core.StatusFiring,
			StartsAt:    time.Now(),
			Labels:      map[string]string{"severity": "critical"},
			Annotations: map[string]string{"summary": "queue path reaches the PagerDuty Events API"},
		},
	}, target)
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, 1, gotCalls)
	assert.Equal(t, "/v2/events", gotPath, "must hit the PagerDuty Events API v2 endpoint, not some other path")
	assert.Equal(t, routingKey, gotRoutingKey, "the target's routing_key must be honored")

	// CONTENT, not just transport (review wave 5, finding C2/I1): buildPayload
	// read summary/severity/timestamp/source at the TOP level of the formatted
	// map, but formatPagerDuty nests all four under a "payload" key — so every
	// trigger event shipped payload.summary/payload.severity empty, and the
	// real Events API v2 requires both non-blank (400 otherwise). Only
	// custom_details was ever read correctly, since that access already went
	// through the nested map.
	require.NotEmpty(t, gotSummary, "payload.summary is REQUIRED by the real Events API v2 — an empty one is a guaranteed 400")
	assert.Contains(t, gotSummary, "HighCPUUsage", "the summary must actually describe THIS alert")
	require.NotEmpty(t, gotSeverity, "payload.severity is REQUIRED by the real Events API v2")
	assert.Equal(t, "alert-history-service", gotSource, "payload.source must be honored, not silently dropped")
}

// TestCreatePublisherForJob_MissingPagerDutyRoutingKeyDegradesGracefully
// mirrors the telegram credential-fallback test: a PagerDuty target with no
// routing_key must still yield a working (basic) publisher rather than
// failing the job.
func TestCreatePublisherForJob_MissingPagerDutyRoutingKeyDegradesGracefully(t *testing.T) {
	f := newTestPublisherFactory(t)
	q := newQueueForFactory(t, f)

	publisher, err := q.createPublisherForJob(&PublishingJob{
		Target: &core.PublishingTarget{Name: "broken-pagerduty", Type: string(TargetTypePagerDuty)},
	})
	require.NoError(t, err)
	require.IsType(t, &PagerDutyPublisher{}, publisher)
}

// === I3 (review wave 5): pagerDutyClientMap/rootlyClientMap cache keys must
// include the base URL, mirroring TestCreatePublisherForTarget_
// TelegramCacheKeyIncludesAPIURL/TestCreatePublisherForJob_
// TelegramHonoursTargetAPIURL. Both were keyed on the credential alone
// (routing_key / api key) while the cached client bakes in target.URL too, so
// the FIRST target built for a given credential silently pinned its base URL
// for every later target sharing that credential — exactly the telegram
// defect this item was supposed to carry the fix for, and it wasn't. Critically,
// this only shows up with a SHARED factory across targets — a fresh factory
// per target (as in TestCreatePublisherForJob_PagerDutyActuallyCallsEventsAPI)
// cannot reproduce it, which is why it slipped through.

// TestCreatePublisherForTarget_PagerDutyCacheKeyIncludesBaseURL mirrors the
// telegram cache-key test.
func TestCreatePublisherForTarget_PagerDutyCacheKeyIncludesBaseURL(t *testing.T) {
	const routingKey = "rk-shared"

	newTarget := func(name, url string) *core.PublishingTarget {
		return &core.PublishingTarget{
			Name:    name,
			Type:    string(TargetTypePagerDuty),
			URL:     url,
			Headers: map[string]string{"routing_key": routingKey},
		}
	}

	f := newTestPublisherFactory(t)

	_, err := f.CreatePublisherForTarget(newTarget("pd-primary", "https://events.pagerduty.example"))
	require.NoError(t, err)
	_, err = f.CreatePublisherForTarget(newTarget("pd-proxy", "https://pagerduty-proxy.internal"))
	require.NoError(t, err)

	pagerDutyClients := func() int {
		f.clientMu.RLock()
		defer f.clientMu.RUnlock()
		return len(f.pagerDutyClientMap)
	}

	assert.Equal(t, 2, pagerDutyClients(),
		"the same routing_key behind two different base URLs must yield two clients, not one pinned to the first URL")

	_, err = f.CreatePublisherForTarget(newTarget("pd-primary-again", "https://events.pagerduty.example"))
	require.NoError(t, err)
	assert.Equal(t, 2, pagerDutyClients(), "an identical (base_url, routing_key) pair must reuse its cached client")
}

// TestCreatePublisherForJob_PagerDutyHonoursTargetURL is the behavioural half,
// using ONE SHARED factory across two targets — the exact case the review
// found untested: each of the two existing PagerDuty httptest tests builds
// its own fresh factory via newTestPublisherFactory, so neither could
// reproduce the cache collision a shared factory (the real one, built once at
// startup) hits immediately.
func TestCreatePublisherForJob_PagerDutyHonoursTargetURL(t *testing.T) {
	const routingKey = "rk-shared"

	var mu sync.Mutex
	hits := map[string]int{}
	handler := func(label string) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			mu.Lock()
			hits[label]++
			mu.Unlock()
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"status":"success","message":"ok","dedup_key":"fp-shared"}`))
		}
	}
	srvA := httptest.NewServer(handler("a"))
	defer srvA.Close()
	srvB := httptest.NewServer(handler("b"))
	defer srvB.Close()

	f := newTestPublisherFactory(t)
	q := newQueueForFactory(t, f)

	alert := &core.EnrichedAlert{Alert: &core.Alert{
		Fingerprint: "fp-shared-routing-key",
		AlertName:   "SharedRoutingKey",
		Status:      core.StatusFiring,
		StartsAt:    time.Now(),
		Labels:      map[string]string{"severity": "warning"},
	}}

	for _, srv := range []*httptest.Server{srvA, srvB} {
		target := &core.PublishingTarget{
			Name:    "pd-" + srv.URL,
			Type:    string(TargetTypePagerDuty),
			URL:     srv.URL,
			Headers: map[string]string{"routing_key": routingKey},
		}
		publisher, err := q.createPublisherForJob(&PublishingJob{Target: target})
		require.NoError(t, err)
		require.NoError(t, publisher.Publish(context.Background(), alert, target))
	}

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, 1, hits["a"], "the first target's own base URL must be used")
	assert.Equal(t, 1, hits["b"], "the second target sharing the routing_key must NOT be silently redirected to the first target's base URL")
}

// TestCreatePublisherForTarget_RootlyCacheKeyIncludesBaseURL mirrors the
// telegram/pagerduty cache-key test for Rootly: NewRootlyIncidentsClient bakes
// in both target.URL and the API key, so the cache key must too.
func TestCreatePublisherForTarget_RootlyCacheKeyIncludesBaseURL(t *testing.T) {
	const apiKey = "rootly-shared-key"

	newTarget := func(name, url string) *core.PublishingTarget {
		return &core.PublishingTarget{
			Name:    name,
			Type:    string(TargetTypeRootly),
			URL:     url,
			Headers: map[string]string{"Authorization": "Bearer " + apiKey},
		}
	}

	f := newTestPublisherFactory(t)

	_, err := f.CreatePublisherForTarget(newTarget("rootly-primary", "https://api.rootly.example"))
	require.NoError(t, err)
	_, err = f.CreatePublisherForTarget(newTarget("rootly-proxy", "https://rootly-proxy.internal"))
	require.NoError(t, err)

	rootlyClients := func() int {
		f.clientMu.RLock()
		defer f.clientMu.RUnlock()
		return len(f.rootlyClientMap)
	}

	assert.Equal(t, 2, rootlyClients(),
		"the same API key behind two different base URLs must yield two clients, not one pinned to the first URL")

	_, err = f.CreatePublisherForTarget(newTarget("rootly-primary-again", "https://api.rootly.example"))
	require.NoError(t, err)
	assert.Equal(t, 2, rootlyClients(), "an identical (base_url, api_key) pair must reuse its cached client")
}
