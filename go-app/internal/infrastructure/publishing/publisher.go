package publishing

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ipiton/AMP/internal/core"
	v2 "github.com/ipiton/AMP/pkg/metrics/v2"
)

// AlertPublisher interface for publishing alerts to external systems
type AlertPublisher interface {
	// Publish publishes an enriched alert to the target
	Publish(ctx context.Context, enrichedAlert *core.EnrichedAlert, target *core.PublishingTarget) error

	// Name returns the publisher name/type
	Name() string
}

// BatchAlertPublisher is implemented by publishers that can deliver an
// entire alert group as ONE wire-level request carrying every alert (task
// fwb: wire-level group batching — upstream Alertmanager's webhook shape:
// one POST per group per target, "alerts" array, not one POST per alert).
//
// Only WebhookPublisher/EnhancedWebhookPublisher implement this today —
// the only formats with a native array-of-alerts wire shape (see
// GroupAlertFormatter.FormatGroup). Every other publisher (Rootly,
// PagerDuty, Slack, Telegram, Email) is inherently one-message-per-alert;
// PublishingQueue.publishJob detects the absence of this interface and
// falls back to calling Publish once per alert within the SAME queued job
// instead of submitting one job per alert (see that method's doc comment).
type BatchAlertPublisher interface {
	// PublishBatch delivers every alert in alerts to target as one request.
	// groupKey/receiver/groupLabels populate the wire payload's
	// corresponding fields (see GroupAlertFormatter.FormatGroup). Returns a
	// single error for the whole batch — there is no partial-success
	// concept at the wire level for a single HTTP request, unlike the
	// per-message iteration path.
	PublishBatch(ctx context.Context, alerts []*core.Alert, groupKey string, receiver string, groupLabels map[string]string, target *core.PublishingTarget) error
}

// HTTPPublisher is a base HTTP client for all publishers
type HTTPPublisher struct {
	formatter  AlertFormatter
	httpClient *http.Client
	logger     *slog.Logger
}

// NewHTTPPublisher creates a new HTTP publisher with default settings
func NewHTTPPublisher(formatter AlertFormatter, logger *slog.Logger) *HTTPPublisher {
	if logger == nil {
		logger = slog.Default()
	}

	return &HTTPPublisher{
		formatter: formatter,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		logger: logger,
	}
}

// publish is a helper method to perform HTTP POST with formatted payload
func (p *HTTPPublisher) publish(ctx context.Context, enrichedAlert *core.EnrichedAlert, target *core.PublishingTarget) error {
	// Format alert for target format
	payload, err := p.formatter.FormatAlert(ctx, enrichedAlert, target.Format)
	if err != nil {
		return fmt.Errorf("failed to format alert: %w", err)
	}

	// Marshal to JSON
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, "POST", target.URL, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	for key, value := range target.Headers {
		req.Header.Set(key, value)
	}

	// Execute request
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("HTTP request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Read response body for error details
	body, _ := io.ReadAll(resp.Body)

	// Check status code
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	p.logger.Debug("Alert published successfully",
		"target", target.Name,
		"status_code", resp.StatusCode,
	)

	return nil
}

// publishBatch is publish's group counterpart (task fwb): formats the WHOLE
// alert group as one wire payload via GroupAlertFormatter.FormatGroup and
// POSTs it once, instead of once per alert. Returns a descriptive error if
// the configured formatter doesn't implement GroupAlertFormatter at all, or
// returns one for this target's format (e.g. a formatter wired for a
// per-message-only format) — callers (WebhookPublisher/
// EnhancedWebhookPublisher) only ever call this for webhook/alertmanager
// targets, where DefaultAlertFormatter always supports it, but this stays
// defensive for any other AlertFormatter implementation (tests, future
// middleware) that might not.
func (p *HTTPPublisher) publishBatch(ctx context.Context, alerts []*core.Alert, groupKey string, receiver string, groupLabels map[string]string, target *core.PublishingTarget) error {
	groupFormatter, ok := p.formatter.(GroupAlertFormatter)
	if !ok {
		return fmt.Errorf("formatter %T does not support wire-level group batching (GroupAlertFormatter)", p.formatter)
	}

	payload, err := groupFormatter.FormatGroup(ctx, alerts, groupKey, receiver, groupLabels, target.Format)
	if err != nil {
		return fmt.Errorf("failed to format alert group: %w", err)
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal group payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", target.URL, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	for key, value := range target.Headers {
		req.Header.Set(key, value)
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("HTTP request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	p.logger.Debug("Alert group published successfully",
		"target", target.Name,
		"group_key", groupKey,
		"alert_count", len(alerts),
		"status_code", resp.StatusCode,
	)

	return nil
}

// RootlyPublisher publishes alerts to Rootly
type RootlyPublisher struct {
	*HTTPPublisher
}

// NewRootlyPublisher creates a new Rootly publisher
func NewRootlyPublisher(formatter AlertFormatter, logger *slog.Logger) AlertPublisher {
	return &RootlyPublisher{
		HTTPPublisher: NewHTTPPublisher(formatter, logger),
	}
}

// Publish publishes alert to Rootly
func (p *RootlyPublisher) Publish(ctx context.Context, enrichedAlert *core.EnrichedAlert, target *core.PublishingTarget) error {
	return p.publish(ctx, enrichedAlert, target)
}

// Name returns publisher name
func (p *RootlyPublisher) Name() string {
	return "Rootly"
}

// PagerDutyPublisher publishes alerts to PagerDuty
type PagerDutyPublisher struct {
	*HTTPPublisher
}

// NewPagerDutyPublisher creates a new PagerDuty publisher
func NewPagerDutyPublisher(formatter AlertFormatter, logger *slog.Logger) AlertPublisher {
	return &PagerDutyPublisher{
		HTTPPublisher: NewHTTPPublisher(formatter, logger),
	}
}

// Publish publishes alert to PagerDuty
func (p *PagerDutyPublisher) Publish(ctx context.Context, enrichedAlert *core.EnrichedAlert, target *core.PublishingTarget) error {
	return p.publish(ctx, enrichedAlert, target)
}

// Name returns publisher name
func (p *PagerDutyPublisher) Name() string {
	return "PagerDuty"
}

// SlackPublisher publishes alerts to Slack
type SlackPublisher struct {
	*HTTPPublisher
}

// NewSlackPublisher creates a new Slack publisher
func NewSlackPublisher(formatter AlertFormatter, logger *slog.Logger) AlertPublisher {
	return &SlackPublisher{
		HTTPPublisher: NewHTTPPublisher(formatter, logger),
	}
}

// Publish publishes alert to Slack
func (p *SlackPublisher) Publish(ctx context.Context, enrichedAlert *core.EnrichedAlert, target *core.PublishingTarget) error {
	return p.publish(ctx, enrichedAlert, target)
}

// Name returns publisher name
func (p *SlackPublisher) Name() string {
	return "Slack"
}

// TelegramPublisher publishes alerts to Telegram (basic, no enhanced client)
type TelegramPublisher struct {
	*HTTPPublisher
}

// NewTelegramPublisher creates a new basic Telegram publisher
func NewTelegramPublisher(formatter AlertFormatter, logger *slog.Logger) AlertPublisher {
	return &TelegramPublisher{
		HTTPPublisher: NewHTTPPublisher(formatter, logger),
	}
}

// Publish publishes alert to Telegram
func (p *TelegramPublisher) Publish(ctx context.Context, enrichedAlert *core.EnrichedAlert, target *core.PublishingTarget) error {
	return p.publish(ctx, enrichedAlert, target)
}

// Name returns publisher name
func (p *TelegramPublisher) Name() string {
	return "Telegram"
}

// WebhookPublisher publishes alerts to generic webhooks
type WebhookPublisher struct {
	*HTTPPublisher
}

// NewWebhookPublisher creates a new generic webhook publisher
func NewWebhookPublisher(formatter AlertFormatter, logger *slog.Logger) AlertPublisher {
	return &WebhookPublisher{
		HTTPPublisher: NewHTTPPublisher(formatter, logger),
	}
}

// Publish publishes alert to generic webhook
func (p *WebhookPublisher) Publish(ctx context.Context, enrichedAlert *core.EnrichedAlert, target *core.PublishingTarget) error {
	return p.publish(ctx, enrichedAlert, target)
}

// PublishBatch implements BatchAlertPublisher (task fwb): one POST carrying
// every alert in the group, matching upstream Alertmanager's webhook shape.
func (p *WebhookPublisher) PublishBatch(ctx context.Context, alerts []*core.Alert, groupKey string, receiver string, groupLabels map[string]string, target *core.PublishingTarget) error {
	return p.publishBatch(ctx, alerts, groupKey, receiver, groupLabels, target)
}

// Name returns publisher name
func (p *WebhookPublisher) Name() string {
	return "Webhook"
}

var _ BatchAlertPublisher = (*WebhookPublisher)(nil)

// PublisherFactory creates publishers based on target type.
//
// Thread-safety: CreatePublisher/CreatePublisherForTarget are called
// concurrently by the publishing queue's worker pool (one call per job, see
// PublishingQueue.processJob), so every per-target client cache below MUST be
// guarded. Before final review finding 6 only emailClientMap was — the Rootly,
// PagerDuty, Slack and Telegram maps were written unguarded from
// CreatePublisherForTarget. That path had no non-test caller at the time, which
// is the only reason it had not produced a "concurrent map writes" fatal;
// routing the live queue through it (same finding) makes the guard mandatory,
// not optional.
type PublisherFactory struct {
	formatter   AlertFormatter
	logger      *slog.Logger
	externalURL string // AMP public URL for callback links

	// clientMu guards rootlyClientMap, pagerDutyClientMap, slackClientMap and
	// telegramClientMap. One mutex for all four: these maps are only touched
	// on the get-or-create path of publisher construction, so contention is
	// negligible compared to the cost of the HTTP publish that follows, and a
	// single lock is far harder to get wrong than four.
	//
	// emailClientMap keeps its own pre-existing emailClientMu rather than being
	// folded in here, to keep this change to the minimum needed.
	clientMu sync.RWMutex

	rootlyCache        IncidentIDCache                  // Shared Rootly incident cache
	rootlyClientMap    map[string]RootlyIncidentsClient // Cache of Rootly clients by API key (clientMu)
	pagerDutyCache     EventKeyCache                    // Shared PagerDuty event key cache
	pagerDutyClientMap map[string]PagerDutyEventsClient // Cache of PagerDuty clients by routing key (clientMu)
	slackCache         MessageIDCache                   // Shared Slack message cache (for threading)
	slackClientMap     map[string]SlackWebhookClient    // Cache of Slack clients by webhook URL (clientMu)
	slackCleanupWorker func()                           // Slack cache cleanup worker cancel function
	emailClientMu      sync.RWMutex                     // Guards emailClientMap for concurrent access
	emailClientMap     map[string]SMTPClient            // Cache of SMTP clients by smtp_host:port
	telegramClientMap  map[string]TelegramClient        // Cache of Telegram clients by "api_url|bot_token" (clientMu)
	metrics            *v2.PublishingMetrics            // Unified publishing metrics (v2)
}

// NewPublisherFactory creates a new publisher factory with unified v2 metrics.
// externalURL is the public AMP base URL used in notification callback links (e.g. silence URLs).
func NewPublisherFactory(formatter AlertFormatter, logger *slog.Logger, metrics *v2.PublishingMetrics, externalURL string) *PublisherFactory {
	// Create Slack cache and start background cleanup worker
	slackCache := NewMessageCache()
	slackCleanupWorker := StartCleanupWorker(slackCache, 5*time.Minute, 24*time.Hour)

	return &PublisherFactory{
		formatter:          formatter,
		logger:             logger,
		externalURL:        externalURL,
		rootlyCache:        NewIncidentIDCache(24 * time.Hour), // 24h TTL for Rootly incident tracking
		rootlyClientMap:    make(map[string]RootlyIncidentsClient),
		pagerDutyCache:     NewEventKeyCache(24 * time.Hour), // 24h TTL for PagerDuty event tracking
		pagerDutyClientMap: make(map[string]PagerDutyEventsClient),
		slackCache:         slackCache, // Slack message cache for threading
		slackClientMap:     make(map[string]SlackWebhookClient),
		slackCleanupWorker: slackCleanupWorker,
		emailClientMap:     make(map[string]SMTPClient),
		telegramClientMap:  make(map[string]TelegramClient),
		metrics:            metrics, // Unified v2 metrics
	}
}

// CreatePublisher creates a publisher for the given target type
func (f *PublisherFactory) CreatePublisher(targetType string) (AlertPublisher, error) {
	switch TargetType(targetType) {
	case TargetTypeRootly:
		return NewRootlyPublisher(f.formatter, f.logger), nil
	case TargetTypePagerDuty:
		return NewPagerDutyPublisher(f.formatter, f.logger), nil
	case TargetTypeSlack:
		return NewSlackPublisher(f.formatter, f.logger), nil
	case TargetTypeWebhook, TargetTypeAlertmanager:
		return NewWebhookPublisher(f.formatter, f.logger), nil
	case TargetTypeEmail:
		return NewEnhancedEmailPublisher(
			NewSMTPDialer(SMTPConfig{Port: 587}, f.logger),
			f.metrics,
			f.formatter,
			f.logger,
			f.externalURL,
		), nil
	case TargetTypeTelegram:
		return NewTelegramPublisher(f.formatter, f.logger), nil
	default:
		return NewWebhookPublisher(f.formatter, f.logger), nil // Default to webhook
	}
}

// CreatePublisherForTarget creates a publisher for a specific target with full configuration
func (f *PublisherFactory) CreatePublisherForTarget(target *core.PublishingTarget) (AlertPublisher, error) {
	switch TargetType(target.Type) {
	case TargetTypeRootly:
		return f.createEnhancedRootlyPublisher(target)
	case TargetTypePagerDuty:
		return f.createEnhancedPagerDutyPublisher(target)
	case TargetTypeSlack:
		return f.createEnhancedSlackPublisher(target)
	case TargetTypeWebhook, TargetTypeAlertmanager:
		return f.createEnhancedWebhookPublisher(target)
	case TargetTypeEmail:
		return f.createEnhancedEmailPublisher(target)
	case TargetTypeTelegram:
		return f.createEnhancedTelegramPublisher(target)
	default:
		return f.createEnhancedWebhookPublisher(target) // Default to enhanced webhook
	}
}

// createEnhancedRootlyPublisher creates an EnhancedRootlyPublisher with full Rootly API integration
func (f *PublisherFactory) createEnhancedRootlyPublisher(target *core.PublishingTarget) (AlertPublisher, error) {
	// Extract API key from target headers
	apiKey := ""
	if auth, ok := target.Headers["Authorization"]; ok {
		// Remove "Bearer " prefix if present
		apiKey = strings.TrimPrefix(auth, "Bearer ")
	}

	if apiKey == "" {
		f.logger.Warn("Rootly target missing API key, falling back to HTTP publisher", "target", target.Name)
		return NewRootlyPublisher(f.formatter, f.logger), nil
	}

	// Get or create Rootly client for this API key (clientMu — see
	// PublisherFactory's doc comment; read-lock fast path, double-check after
	// upgrading, same pattern as createEnhancedEmailPublisher).
	f.clientMu.RLock()
	client, ok := f.rootlyClientMap[apiKey]
	f.clientMu.RUnlock()
	if !ok {
		f.clientMu.Lock()
		if client, ok = f.rootlyClientMap[apiKey]; !ok {
			client = NewRootlyIncidentsClient(ClientConfig{
				BaseURL: target.URL,
				APIKey:  apiKey,
				Timeout: 10 * time.Second,
			}, f.logger)
			f.rootlyClientMap[apiKey] = client
		}
		f.clientMu.Unlock()
	}

	// Create EnhancedRootlyPublisher with shared cache and unified metrics
	return NewEnhancedRootlyPublisher(
		client,
		f.rootlyCache,
		f.metrics,
		f.formatter,
		f.logger,
	), nil
}

// createEnhancedPagerDutyPublisher creates an EnhancedPagerDutyPublisher with full PagerDuty Events API v2 integration
func (f *PublisherFactory) createEnhancedPagerDutyPublisher(target *core.PublishingTarget) (AlertPublisher, error) {
	// Extract routing key from target headers
	routingKey := ""
	if rk, ok := target.Headers["routing_key"]; ok {
		routingKey = rk
	}

	// Check for Authorization header (Bearer token format)
	if auth, ok := target.Headers["Authorization"]; ok {
		// Remove "Bearer " prefix if present
		const bearerPrefix = "Bearer "
		if len(auth) > len(bearerPrefix) && auth[:len(bearerPrefix)] == bearerPrefix {
			routingKey = auth[len(bearerPrefix):]
		} else {
			routingKey = auth
		}
	}

	if routingKey == "" {
		f.logger.Warn("PagerDuty target missing routing_key, falling back to HTTP publisher", "target", target.Name)
		return NewPagerDutyPublisher(f.formatter, f.logger), nil
	}

	// Get or create PagerDuty client for this routing key (clientMu — see
	// PublisherFactory's doc comment).
	f.clientMu.RLock()
	client, ok := f.pagerDutyClientMap[routingKey]
	f.clientMu.RUnlock()
	if !ok {
		f.clientMu.Lock()
		if client, ok = f.pagerDutyClientMap[routingKey]; !ok {
			config := PagerDutyClientConfig{
				BaseURL:    target.URL,
				Timeout:    10 * time.Second,
				MaxRetries: 3,
				RateLimit:  120.0, // 120 req/min
			}
			if config.BaseURL == "" {
				config.BaseURL = "https://events.pagerduty.com"
			}
			client = NewPagerDutyEventsClient(config, f.logger)
			f.pagerDutyClientMap[routingKey] = client
		}
		f.clientMu.Unlock()
	}

	// Create EnhancedPagerDutyPublisher with shared cache and unified metrics
	return NewEnhancedPagerDutyPublisher(
		client,
		f.pagerDutyCache,
		f.metrics,
		f.formatter,
		f.logger,
	), nil
}

// createEnhancedSlackPublisher creates an EnhancedSlackPublisher with full Slack Webhook API integration
func (f *PublisherFactory) createEnhancedSlackPublisher(target *core.PublishingTarget) (AlertPublisher, error) {
	// Use target.URL as webhook URL
	webhookURL := target.URL
	if webhookURL == "" {
		f.logger.Warn("Slack target missing webhook URL, falling back to HTTP publisher", "target", target.Name)
		return NewSlackPublisher(f.formatter, f.logger), nil
	}

	// Get or create Slack client for this webhook URL (clientMu — see
	// PublisherFactory's doc comment).
	f.clientMu.RLock()
	client, ok := f.slackClientMap[webhookURL]
	f.clientMu.RUnlock()
	if !ok {
		f.clientMu.Lock()
		if client, ok = f.slackClientMap[webhookURL]; !ok {
			client = NewHTTPSlackWebhookClient(webhookURL, f.logger)
			f.slackClientMap[webhookURL] = client
		}
		f.clientMu.Unlock()
	}

	// Create EnhancedSlackPublisher with shared cache and unified metrics
	return NewEnhancedSlackPublisher(
		client,
		f.slackCache,
		f.metrics,
		f.formatter,
		f.logger,
	), nil
}

// createEnhancedTelegramPublisher creates an EnhancedTelegramPublisher with full Telegram Bot API integration
func (f *PublisherFactory) createEnhancedTelegramPublisher(target *core.PublishingTarget) (AlertPublisher, error) {
	// Extract bot token from target headers (bot_token, falling back to Authorization: Bearer)
	botToken := target.Headers["bot_token"]
	if botToken == "" {
		if auth, ok := target.Headers["Authorization"]; ok {
			botToken = strings.TrimPrefix(auth, "Bearer ")
		}
	}

	chatID := target.Headers["chat_id"]

	if botToken == "" || chatID == "" {
		f.logger.Warn("Telegram target missing bot_token or chat_id, falling back to HTTP publisher", "target", target.Name)
		return NewTelegramPublisher(f.formatter, f.logger), nil
	}

	apiURL := target.URL
	if apiURL == "" {
		apiURL = DefaultTelegramAPIURL
	}

	// Get or create Telegram client for this (api_url, bot_token) pair
	// (clientMu — see PublisherFactory's doc comment; MANDATORY now that the
	// live queue path reaches this function concurrently, per final review
	// finding 6).
	//
	// The cache key is COMPOUND because NewHTTPTelegramClient bakes BOTH values
	// in (wave re-review, Minor 5). Keying on botToken alone meant the first
	// target to be built for a token pinned its api_url for every later target
	// sharing that token — so a second target pointing the same bot at a
	// different API base (a proxy, or a test server) silently reused the first
	// one's endpoint. Same shape as the SMTP cache's "host:port" key.
	clientKey := apiURL + "|" + botToken

	f.clientMu.RLock()
	client, ok := f.telegramClientMap[clientKey]
	f.clientMu.RUnlock()
	if !ok {
		f.clientMu.Lock()
		if client, ok = f.telegramClientMap[clientKey]; !ok {
			client = NewHTTPTelegramClient(apiURL, botToken, f.logger)
			f.telegramClientMap[clientKey] = client
		}
		f.clientMu.Unlock()
	}

	messageThreadID := 0
	if raw, ok := target.Headers["message_thread_id"]; ok {
		if v, err := strconv.Atoi(raw); err == nil {
			messageThreadID = v
		}
	}
	disableNotifications := target.Headers["disable_notifications"] == "true"

	// Create EnhancedTelegramPublisher with unified metrics
	return NewEnhancedTelegramPublisher(
		client,
		chatID,
		messageThreadID,
		disableNotifications,
		f.metrics,
		f.formatter,
		f.logger,
	), nil
}

// createEnhancedWebhookPublisher creates an EnhancedWebhookPublisher with full validation and metrics
func (f *PublisherFactory) createEnhancedWebhookPublisher(target *core.PublishingTarget) (AlertPublisher, error) {
	f.logger.Info("Creating enhanced webhook publisher",
		"target", target.Name,
		"url", target.URL)

	// Create HTTP client with default retry config
	client := NewWebhookHTTPClient(DefaultWebhookRetryConfig, f.logger)

	// Create validator with default config
	validator := NewWebhookValidator(f.logger)

	// Create enhanced webhook publisher with unified metrics
	publisher := NewEnhancedWebhookPublisher(
		client,
		validator,
		f.formatter,
		f.metrics, // Unified v2 metrics instance
		f.logger,
	)

	f.logger.Info("Enhanced webhook publisher created successfully",
		"target", target.Name,
		"features", "4 auth strategies, 6 validation rules, exponential backoff retry")

	return publisher, nil
}

// createEnhancedEmailPublisher создаёт EnhancedEmailPublisher с конфигурацией из target.Headers.
// SMTP-клиент кешируется по ключу "host:port" для переиспользования.
func (f *PublisherFactory) createEnhancedEmailPublisher(target *core.PublishingTarget) (AlertPublisher, error) {
	// Извлечь SMTP конфиг из target.Headers
	smtpCfg := extractSMTPConfig(target)

	if smtpCfg.Host == "" {
		f.logger.Warn("Email target missing smtp_host, publisher will fail on send",
			slog.String("target", target.Name))
	}

	// Ключ кеша — smtp_host:port (одинаковый SMTP сервер переиспользуется)
	cacheKey := strings.Join([]string{smtpCfg.Host, strconv.Itoa(smtpCfg.Port)}, ":")

	f.emailClientMu.RLock()
	client, ok := f.emailClientMap[cacheKey]
	f.emailClientMu.RUnlock()
	if !ok {
		f.emailClientMu.Lock()
		// double-check после upgrade до write lock — другая горутина могла создать клиент
		if client, ok = f.emailClientMap[cacheKey]; !ok {
			client = NewSMTPDialer(smtpCfg, f.logger)
			f.emailClientMap[cacheKey] = client
		}
		f.emailClientMu.Unlock()
	}

	return NewEnhancedEmailPublisher(
		client,
		f.metrics,
		f.formatter,
		f.logger,
		f.externalURL,
	), nil
}

// Shutdown stops all background workers owned by this factory: the Slack
// message cache cleanup worker plus the Rootly and PagerDuty cache cleanup
// goroutines started in NewPublisherFactory. Without this, each of those
// goroutines runs forever (see their respective cleanupWorker/cleanup loops),
// which leaks one goroutine per factory instance for the life of the
// process and drowns full-package -race runs in stacks from unrelated tests
// that never tear down their own factory.
func (f *PublisherFactory) Shutdown() {
	// Stop Slack cache cleanup worker
	if f.slackCleanupWorker != nil {
		f.slackCleanupWorker()
		f.logger.Info("Stopped Slack cache cleanup worker")
	}

	// Stop Rootly incident cache cleanup worker
	if f.rootlyCache != nil {
		f.rootlyCache.Stop()
		f.logger.Info("Stopped Rootly cache cleanup worker")
	}

	// Stop PagerDuty event key cache cleanup worker
	if f.pagerDutyCache != nil {
		f.pagerDutyCache.Stop()
		f.logger.Info("Stopped PagerDuty cache cleanup worker")
	}
}
