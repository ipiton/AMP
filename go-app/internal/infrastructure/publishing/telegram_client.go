package publishing

import (
	"bytes"
	"container/list"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/ipiton/AMP/pkg/httperror"
	"golang.org/x/time/rate"
)

// telegram_client.go - Telegram Bot API client with rate limiting and retry logic

const (
	// defaultChatRateLimit is the steady-state per-chat send rate. Telegram
	// tolerates roughly 1 msg/sec sustained to a single chat before it
	// starts returning 429 Too Many Requests for that chat.
	defaultChatRateLimit = rate.Limit(1)

	// defaultChatBurst allows a short burst to a single chat (e.g. several
	// related alerts firing at once) before per-chat throttling kicks in.
	// Chosen conservatively inside Telegram's observed 5-20 message burst
	// tolerance per chat.
	defaultChatBurst = 3

	// maxTrackedChatLimiters bounds the memory used by the per-chat rate
	// limiter registry. See chatRateLimiterStore's doc comment for why a
	// bound is needed and how eviction works.
	maxTrackedChatLimiters = 1000
)

// TelegramClient defines the interface for Telegram Bot API operations
type TelegramClient interface {
	// SendMessage sends a text message to a Telegram chat/channel/group
	// Returns TelegramResponse with the sent message details on success
	SendMessage(ctx context.Context, message *TelegramMessage) (*TelegramResponse, error)

	// Health checks if the bot token is valid and the API is reachable
	// Uses the "getMe" endpoint, which does not send any message
	Health(ctx context.Context) error
}

// HTTPTelegramClient implements TelegramClient using HTTP
// Provides rate limiting, retry logic with exponential backoff, and
// comprehensive error handling for the Telegram Bot API.
type HTTPTelegramClient struct {
	httpClient   *http.Client
	apiURL       string // Telegram Bot API base URL (e.g. https://api.telegram.org), no trailing slash
	botToken     string
	rateLimiter  *rate.Limiter         // global limiter: bounds aggregate throughput across all chats
	chatLimiters *chatRateLimiterStore // per-chat limiters: smooths bursts to a single chat
	logger       *slog.Logger
}

// chatRateLimiterStore is a bounded, thread-safe registry of per-chat rate
// limiters, keyed by Telegram chat ID. It exists because HTTPTelegramClient's
// global rate.Limiter only bounds aggregate throughput across all chats: a
// storm of alerts routed to one chat can pass the global limiter's burst
// allowance and still trip Telegram's per-chat ~1 msg/sec limit, producing
// 429s that eat into the client's fixed retry budget.
//
// Bounding strategy: LRU eviction with a fixed capacity
// (maxTrackedChatLimiters). Chat ID cardinality is not bounded by anything
// in this client - alert routing can address any chat the bot has ever been
// added to - so an unbounded map would leak memory for the life of a
// long-running process. LRU (via container/list, same approach as
// LRUCache in this package) evicts the least-recently-used chat in O(1)
// when a new chat needs a limiter and the store is at capacity. This was
// chosen over an idle-TTL sweep because it needs no background goroutine
// and bounds worst-case memory deterministically regardless of traffic
// pattern; the trade-off is that a chat evicted while idle gets a fresh
// (fully-bursted) limiter on its next message, which is acceptable since
// that is also the state a brand-new chat starts in.
type chatRateLimiterStore struct {
	mu       sync.Mutex
	capacity int
	rate     rate.Limit
	burst    int
	items    map[string]*list.Element
	order    *list.List // front = most recently used, back = least recently used
}

// chatLimiterEntry is the value stored in chatRateLimiterStore.order.
type chatLimiterEntry struct {
	chatID  string
	limiter *rate.Limiter
}

// newChatRateLimiterStore creates a per-chat limiter registry bounded to
// capacity entries. Each newly created limiter allows r events/sec with the
// given burst. capacity <= 0 is a constructor misuse (it would make the
// store unbounded, defeating the whole point of the LRU cap) and is
// clamped to maxTrackedChatLimiters rather than silently accepted.
func newChatRateLimiterStore(capacity int, r rate.Limit, burst int) *chatRateLimiterStore {
	if capacity <= 0 {
		capacity = maxTrackedChatLimiters
	}
	return &chatRateLimiterStore{
		capacity: capacity,
		rate:     r,
		burst:    burst,
		items:    make(map[string]*list.Element, capacity),
		order:    list.New(),
	}
}

// getOrCreate returns the rate.Limiter for chatID, creating one lazily on
// first use. If the store is at capacity, the least-recently-used chat's
// limiter is evicted to make room.
func (s *chatRateLimiterStore) getOrCreate(chatID string) *rate.Limiter {
	s.mu.Lock()
	defer s.mu.Unlock()

	if el, ok := s.items[chatID]; ok {
		s.order.MoveToFront(el)
		return el.Value.(*chatLimiterEntry).limiter
	}

	// capacity is always > 0 here: newChatRateLimiterStore clamps it.
	if s.order.Len() >= s.capacity {
		if oldest := s.order.Back(); oldest != nil {
			s.order.Remove(oldest)
			delete(s.items, oldest.Value.(*chatLimiterEntry).chatID)
		}
	}

	limiter := rate.NewLimiter(s.rate, s.burst)
	el := s.order.PushFront(&chatLimiterEntry{chatID: chatID, limiter: limiter})
	s.items[chatID] = el
	return limiter
}

// len returns the number of chats currently tracked. Used by tests to
// assert the store stays within capacity.
func (s *chatRateLimiterStore) len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.order.Len()
}

// NewHTTPTelegramClient creates a new Telegram Bot API client.
// apiURL is the Telegram Bot API base URL (defaults to https://api.telegram.org when empty).
// botToken is the Telegram bot token issued by the BotFather bot in Telegram.
// logger is the structured logger used for debug/info/warn/error logging.
func NewHTTPTelegramClient(
	apiURL string,
	botToken string,
	logger *slog.Logger,
) TelegramClient {
	return NewHTTPTelegramClientWithHTTPClient(apiURL, botToken, logger, nil)
}

// NewHTTPTelegramClientWithHTTPClient is NewHTTPTelegramClient with an explicit
// *http.Client, used by PublisherFactory to hand in a client built from the
// target's `http_config` (FU-HTTP-CONFIG). A nil httpClient falls back to the
// built-in shape, so the two constructors are identical for every target
// without http_config.
func NewHTTPTelegramClientWithHTTPClient(
	apiURL string,
	botToken string,
	logger *slog.Logger,
	httpClient *http.Client,
) TelegramClient {
	if apiURL == "" {
		apiURL = DefaultTelegramAPIURL
	}
	if logger == nil {
		logger = slog.Default()
	}
	if httpClient == nil {
		httpClient = newTelegramBaseHTTPClient()
	}

	return &HTTPTelegramClient{
		httpClient:   httpClient,
		apiURL:       strings.TrimRight(apiURL, "/"),
		botToken:     botToken,
		rateLimiter:  rate.NewLimiter(rate.Limit(30), 5), // Telegram global limit: ~30 msg/sec, burst 5
		chatLimiters: newChatRateLimiterStore(maxTrackedChatLimiters, defaultChatRateLimit, defaultChatBurst),
		logger:       logger.With("component", "telegram_client"),
	}
}

// newTelegramBaseHTTPClient builds the Telegram client's built-in HTTP client.
// Extracted so a per-target http_config can be layered on top of it rather than
// replacing it — see newWebhookBaseHTTPClient.
func newTelegramBaseHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				MinVersion: tls.VersionTLS12, // TLS 1.2+ required
			},
			MaxIdleConns:        10,
			MaxIdleConnsPerHost: 2,
			IdleConnTimeout:     30 * time.Second,
			DialContext: (&net.Dialer{
				Timeout:   5 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
		},
	}
}

// endpoint builds the full Telegram Bot API URL for the given method.
// The bot token becomes part of the URL path here; callers must never log
// the result of this function directly (log c.apiURL instead).
func (c *HTTPTelegramClient) endpoint(method string) string {
	return fmt.Sprintf("%s/bot%s/%s", c.apiURL, c.botToken, method)
}

// maskBotToken redacts the client's bot token wherever it appears in s.
// Network-layer failures from http.Client.Do surface as *url.Error, whose
// Error() message embeds the full request URL - including the bot token
// from endpoint(). Any error string derived from such a failure must be
// masked before it is logged or returned to a caller that might log it.
func (c *HTTPTelegramClient) maskBotToken(s string) string {
	if c.botToken == "" {
		return s
	}
	return strings.ReplaceAll(s, c.botToken, "***")
}

// SendMessage sends a text message to Telegram
// Blocks until a rate limit token is available.
// Retries transient errors (429, 5xx, network) with exponential backoff.
func (c *HTTPTelegramClient) SendMessage(ctx context.Context, message *TelegramMessage) (*TelegramResponse, error) {
	c.logger.DebugContext(ctx, "Sending message to Telegram",
		slog.String("api_url", c.apiURL))

	// Rate limit check (blocks until tokens available). Per-chat first: it
	// smooths bursts to a single chat, then the global limiter bounds
	// aggregate throughput across all chats. Both honor ctx cancellation.
	//
	// Note: if ctx is canceled between the two Waits (chat token consumed,
	// global Wait then fails), that per-chat token is spent on a message
	// that never sends. Accepted: worst case is one wasted token per
	// cancellation, and defaultChatBurst=3 with a 1/s refill makes the
	// effect on that chat's next legitimate send negligible.
	chatLimiter := c.chatLimiters.getOrCreate(message.ChatID)
	if err := chatLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("per-chat rate limiter wait failed: %w", err)
	}
	if err := c.rateLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limiter wait failed: %w", err)
	}

	// Build HTTP request
	body, err := json.Marshal(message)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal message: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint("sendMessage"), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	// Execute with retry logic
	resp, err := c.doRequestWithRetry(ctx, req, body)
	if err != nil {
		return nil, err
	}

	return resp, nil
}

// Health checks bot token validity and API connectivity via the "getMe"
// endpoint. Unlike SendMessage, this never delivers a visible message.
func (c *HTTPTelegramClient) Health(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint("getMe"), nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	httpResp, err := c.httpClient.Do(req)
	if err != nil {
		// Same token-in-URL exposure risk as doRequestWithRetry: err may be
		// a *url.Error embedding the full request URL. Mask before it can
		// reach any caller's logs via this error's message.
		return fmt.Errorf("health check request failed: %s", c.maskBotToken(err.Error()))
	}
	defer func() { _ = httpResp.Body.Close() }()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return fmt.Errorf("failed to read health check response: %w", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		return parseTelegramError(httpResp, respBody)
	}

	var tgResp TelegramResponse
	if err := json.Unmarshal(respBody, &tgResp); err != nil {
		return fmt.Errorf("failed to parse health check response: %w", err)
	}
	if !tgResp.OK {
		return httperror.NewHTTPError(httpResp.StatusCode, tgResp.Description, ProviderTelegram)
	}

	return nil
}

// doRequestWithRetry executes HTTP request with retry logic
// Retries transient errors (429, 5xx, network) up to 3 times
// Uses exponential backoff: 100ms → 200ms → 400ms → ... → 5s max
func (c *HTTPTelegramClient) doRequestWithRetry(ctx context.Context, req *http.Request, bodyBytes []byte) (*TelegramResponse, error) {
	const maxRetries = 3
	backoff := 100 * time.Millisecond

	var lastErr error
	for i := 0; i < maxRetries; i++ {
		// Clone request body for each attempt (HTTP request body is consumed on first use)
		if len(bodyBytes) > 0 {
			req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		}

		// Execute HTTP request
		httpResp, err := c.httpClient.Do(req)
		if err != nil {
			// err may be a *url.Error embedding the full request URL (which
			// contains the bot token) - mask before it enters lastErr's
			// message or any log line. Retryability is still checked
			// against the original err, not the masked message.
			maskedErr := c.maskBotToken(err.Error())
			lastErr = fmt.Errorf("HTTP request failed: %s", maskedErr)
			if !httperror.IsRetryableNetworkError(err) {
				return nil, lastErr // Don't retry non-network-transient errors
			}
			c.logger.WarnContext(ctx, "Retrying after network error",
				slog.Int("attempt", i+1),
				slog.String("error", maskedErr))
			time.Sleep(backoff)
			backoff *= 2
			if backoff > 5*time.Second {
				backoff = 5 * time.Second
			}
			continue
		}
		defer func() { _ = httpResp.Body.Close() }()

		// Read response body
		respBody, err := io.ReadAll(httpResp.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to read response body: %w", err)
		}

		// Check status code
		if httpResp.StatusCode == http.StatusOK {
			// Success - parse response
			var tgResp TelegramResponse
			if err := json.Unmarshal(respBody, &tgResp); err != nil {
				return nil, fmt.Errorf("failed to parse response: %w", err)
			}

			if !tgResp.OK {
				// Telegram returned ok=false with a 200 status (rare, but defensive)
				return nil, httperror.NewHTTPError(httpResp.StatusCode, tgResp.Description, ProviderTelegram)
			}

			return &tgResp, nil
		}

		// Error - parse Telegram API error
		apiErr := parseTelegramError(httpResp, respBody)
		lastErr = apiErr

		// Check if retryable
		if !(&httperror.PublishingClassifier{}).IsRetryable(apiErr) {
			c.logger.ErrorContext(ctx, "Permanent error, not retrying",
				slog.Int("status_code", httpResp.StatusCode),
				slog.String("error", apiErr.Error()))
			return nil, apiErr
		}

		// Retry transient errors (429, 5xx)
		c.logger.WarnContext(ctx, "Retrying after transient error",
			slog.Int("attempt", i+1),
			slog.Int("status_code", httpResp.StatusCode),
			slog.String("error", apiErr.Error()))

		// Respect retry_after for 429 (rate limit)
		if apiErr.StatusCode == http.StatusTooManyRequests && apiErr.RetryAfter > 0 {
			retryAfter := time.Duration(apiErr.RetryAfter) * time.Second
			c.logger.InfoContext(ctx, "Rate limited, respecting retry_after",
				slog.Duration("retry_after", retryAfter))
			select {
			case <-time.After(retryAfter):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		} else {
			// Exponential backoff for other errors
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			backoff *= 2
			if backoff > 5*time.Second {
				backoff = 5 * time.Second
			}
		}
	}

	return nil, fmt.Errorf("max retries (%d) exceeded: %w", maxRetries, lastErr)
}
