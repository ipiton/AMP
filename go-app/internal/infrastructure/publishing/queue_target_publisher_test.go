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
	t.Cleanup(func() {
		if f.slackCleanupWorker != nil {
			f.slackCleanupWorker()
		}
	})
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
