package publishing

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ipiton/AMP/pkg/httperror"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"
)

// telegram_client_test.go - tests for HTTPTelegramClient against a fake
// Telegram Bot API (httptest server).

func TestNewHTTPTelegramClient(t *testing.T) {
	t.Run("defaults api url when empty", func(t *testing.T) {
		client := NewHTTPTelegramClient("", "test-token", slog.Default())
		assert.NotNil(t, client)

		impl, ok := client.(*HTTPTelegramClient)
		require.True(t, ok)
		assert.Equal(t, DefaultTelegramAPIURL, impl.apiURL)
	})

	t.Run("trims trailing slash from custom api url", func(t *testing.T) {
		client := NewHTTPTelegramClient("https://custom.example/", "test-token", slog.Default())
		impl, ok := client.(*HTTPTelegramClient)
		require.True(t, ok)
		assert.Equal(t, "https://custom.example", impl.apiURL)
	})

	t.Run("nil logger falls back to default", func(t *testing.T) {
		client := NewHTTPTelegramClient("https://custom.example", "test-token", nil)
		assert.NotNil(t, client)
	})
}

func TestHTTPTelegramClient_SendMessage(t *testing.T) {
	t.Run("success - 200 ok", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, http.MethodPost, r.Method)
			assert.Equal(t, "/bottest-token/sendMessage", r.URL.Path)
			assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

			var msg TelegramMessage
			require.NoError(t, json.NewDecoder(r.Body).Decode(&msg))
			assert.Equal(t, "-1001234567890", msg.ChatID)
			assert.Equal(t, "hello", msg.Text)

			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(TelegramResponse{
				OK:     true,
				Result: &TelegramResult{MessageID: 42},
			})
		}))
		defer server.Close()

		client := NewHTTPTelegramClient(server.URL, "test-token", slog.Default())

		resp, err := client.SendMessage(context.Background(), &TelegramMessage{
			ChatID: "-1001234567890",
			Text:   "hello",
		})

		require.NoError(t, err)
		require.NotNil(t, resp)
		assert.True(t, resp.OK)
		require.NotNil(t, resp.Result)
		assert.Equal(t, 42, resp.Result.MessageID)
	})

	t.Run("error - 400 bad request (no retry)", func(t *testing.T) {
		attempts := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			attempts++
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(TelegramResponse{
				OK:          false,
				ErrorCode:   400,
				Description: "Bad Request: chat not found",
			})
		}))
		defer server.Close()

		client := NewHTTPTelegramClient(server.URL, "test-token", slog.Default())

		_, err := client.SendMessage(context.Background(), &TelegramMessage{
			ChatID: "invalid",
			Text:   "hello",
		})

		require.Error(t, err)
		var apiErr *httperror.HTTPAPIError
		require.ErrorAs(t, err, &apiErr)
		assert.Equal(t, 400, apiErr.StatusCode)
		assert.Contains(t, apiErr.Message, "chat not found")
		assert.Equal(t, 1, attempts) // no retry on 400
	})

	t.Run("error - 401 unauthorized (no retry)", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(TelegramResponse{
				OK:          false,
				ErrorCode:   401,
				Description: "Unauthorized",
			})
		}))
		defer server.Close()

		client := NewHTTPTelegramClient(server.URL, "invalid-token", slog.Default())

		_, err := client.SendMessage(context.Background(), &TelegramMessage{
			ChatID: "-1001234567890",
			Text:   "hello",
		})

		require.Error(t, err)
		var apiErr *httperror.HTTPAPIError
		require.ErrorAs(t, err, &apiErr)
		assert.Equal(t, 401, apiErr.StatusCode)
		assert.True(t, IsTelegramAuthError(err))
	})

	t.Run("error - 429 rate limit (retry then success)", func(t *testing.T) {
		attempts := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			attempts++
			if attempts <= 1 {
				w.WriteHeader(http.StatusTooManyRequests)
				_ = json.NewEncoder(w).Encode(TelegramResponse{
					OK:          false,
					ErrorCode:   429,
					Description: "Too Many Requests",
					Parameters:  &TelegramResponseParameters{RetryAfter: 0},
				})
				return
			}
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(TelegramResponse{
				OK:     true,
				Result: &TelegramResult{MessageID: 7},
			})
		}))
		defer server.Close()

		client := NewHTTPTelegramClient(server.URL, "test-token", slog.Default())

		resp, err := client.SendMessage(context.Background(), &TelegramMessage{
			ChatID: "-1001234567890",
			Text:   "hello",
		})

		require.NoError(t, err)
		require.NotNil(t, resp)
		assert.Equal(t, 2, attempts)
	})

	t.Run("error - 500 server error (retry then exhausted)", func(t *testing.T) {
		attempts := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			attempts++
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(TelegramResponse{
				OK:          false,
				ErrorCode:   500,
				Description: "Internal Server Error",
			})
		}))
		defer server.Close()

		client := NewHTTPTelegramClient(server.URL, "test-token", slog.Default())

		_, err := client.SendMessage(context.Background(), &TelegramMessage{
			ChatID: "-1001234567890",
			Text:   "hello",
		})

		require.Error(t, err)
		assert.Equal(t, 3, attempts) // maxRetries=3, all exhausted
	})

	t.Run("context cancellation", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(100 * time.Millisecond)
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(TelegramResponse{OK: true, Result: &TelegramResult{MessageID: 1}})
		}))
		defer server.Close()

		client := NewHTTPTelegramClient(server.URL, "test-token", slog.Default())

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		defer cancel()

		_, err := client.SendMessage(ctx, &TelegramMessage{ChatID: "-1", Text: "hello"})

		require.Error(t, err)
	})
}

func TestHTTPTelegramClient_Health(t *testing.T) {
	t.Run("success - bot token valid", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, http.MethodGet, r.Method)
			assert.Equal(t, "/bottest-token/getMe", r.URL.Path)
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(TelegramResponse{OK: true})
		}))
		defer server.Close()

		client := NewHTTPTelegramClient(server.URL, "test-token", slog.Default())

		err := client.Health(context.Background())
		assert.NoError(t, err)
	})

	t.Run("error - invalid bot token", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(TelegramResponse{
				OK:          false,
				ErrorCode:   401,
				Description: "Unauthorized",
			})
		}))
		defer server.Close()

		client := NewHTTPTelegramClient(server.URL, "invalid-token", slog.Default())

		err := client.Health(context.Background())
		require.Error(t, err)
		var apiErr *httperror.HTTPAPIError
		require.ErrorAs(t, err, &apiErr)
		assert.Equal(t, 401, apiErr.StatusCode)
	})

	t.Run("error - unreachable", func(t *testing.T) {
		client := NewHTTPTelegramClient("http://127.0.0.1:1", "test-token", slog.Default())

		err := client.Health(context.Background())
		assert.Error(t, err)
	})

	t.Run("network error does not leak bot token", func(t *testing.T) {
		const secretToken = "SECRET-BOT-TOKEN-DO-NOT-LEAK-98765"

		var logBuf bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

		// Unroutable loopback port - connection refused, classified as a
		// retryable network error by httperror.IsRetryableNetworkError.
		client := NewHTTPTelegramClient("http://127.0.0.1:1", secretToken, logger)

		err := client.Health(context.Background())

		require.Error(t, err)
		assert.NotContains(t, err.Error(), secretToken,
			"returned error must not contain the raw bot token")
		assert.NotContains(t, logBuf.String(), secretToken,
			"log output must not contain the raw bot token")
	})
}

// TestHTTPTelegramClient_SendMessage_NetworkErrorDoesNotLeakToken verifies
// that a *url.Error from http.Client.Do (which embeds the full request URL,
// including the bot token from endpoint()) never reaches a log line or a
// returned error message unmasked - across every retry attempt.
func TestHTTPTelegramClient_SendMessage_NetworkErrorDoesNotLeakToken(t *testing.T) {
	const secretToken = "SECRET-BOT-TOKEN-DO-NOT-LEAK-13579"

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// Unroutable loopback port - connection refused, retried up to
	// maxRetries times before doRequestWithRetry gives up.
	client := NewHTTPTelegramClient("http://127.0.0.1:1", secretToken, logger)

	_, err := client.SendMessage(context.Background(), &TelegramMessage{
		ChatID: "-1001234567890",
		Text:   "hello",
	})

	require.Error(t, err)
	assert.NotContains(t, err.Error(), secretToken,
		"returned error must not contain the raw bot token")

	logOutput := logBuf.String()
	assert.NotContains(t, logOutput, secretToken,
		"log output must not contain the raw bot token")
	// Sanity check the log actually captured the retry path (otherwise this
	// test would pass vacuously because nothing was logged at all).
	assert.True(t, strings.Contains(logOutput, "Retrying after network error"),
		"expected at least one retry log line to assert masking against")
}

// telegram_client_test.go (per-chat rate limiting) - tests for
// chatRateLimiterStore and its wiring into HTTPTelegramClient.SendMessage.
// Timing assertions use rate/burst parameters chosen for the test (fast,
// deterministic) rather than the client's real defaults (1 msg/sec per
// chat), and use floor/ceiling ranges instead of exact durations to stay
// stable under CI scheduling jitter.

func TestNewHTTPTelegramClient_ChatRateLimiters(t *testing.T) {
	client := NewHTTPTelegramClient("", "test-token", slog.Default())
	impl, ok := client.(*HTTPTelegramClient)
	require.True(t, ok)
	require.NotNil(t, impl.chatLimiters)
	assert.Equal(t, maxTrackedChatLimiters, impl.chatLimiters.capacity)
	assert.Equal(t, defaultChatRateLimit, impl.chatLimiters.rate)
	assert.Equal(t, defaultChatBurst, impl.chatLimiters.burst)
}

func TestChatRateLimiterStore_GetOrCreate(t *testing.T) {
	store := newChatRateLimiterStore(10, rate.Limit(1), 3)

	a1 := store.getOrCreate("chatA")
	a2 := store.getOrCreate("chatA")
	b1 := store.getOrCreate("chatB")

	assert.Same(t, a1, a2, "repeated lookups of the same chat must return the same limiter")
	assert.NotSame(t, a1, b1, "different chats must get independent limiters")
	assert.Equal(t, 2, store.len())
}

func TestChatRateLimiterStore_BoundedCapacity(t *testing.T) {
	const capacity = 5
	store := newChatRateLimiterStore(capacity, rate.Limit(1), 1)

	for i := 0; i < capacity*3; i++ {
		store.getOrCreate(fmt.Sprintf("chat-%d", i))
	}

	assert.Equal(t, capacity, store.len(), "store must not grow past its configured capacity")
}

func TestChatRateLimiterStore_LRUEviction(t *testing.T) {
	store := newChatRateLimiterStore(2, rate.Limit(1), 1)

	a := store.getOrCreate("a")
	store.getOrCreate("b")
	// Touch "a" again so "b" becomes the least-recently-used entry.
	store.getOrCreate("a")
	store.getOrCreate("c") // should evict "b", not "a"

	assert.Equal(t, 2, store.len())
	assert.Same(t, a, store.getOrCreate("a"), "recently-touched chat must survive eviction")
}

func TestChatRateLimiterStore_ConcurrentAccess(t *testing.T) {
	const capacity = 10
	store := newChatRateLimiterStore(capacity, rate.Limit(50), 5)

	// 30 distinct chat IDs against a 10-entry store: eviction must trigger
	// repeatedly while goroutines race on it, not just capacity-check pass.
	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			chatID := fmt.Sprintf("chat-%d", i%30)
			limiter := store.getOrCreate(chatID)
			limiter.Allow()
		}(i)
	}
	wg.Wait()

	assert.LessOrEqual(t, store.len(), capacity, "concurrent access must not push the store past capacity")
}

func TestNewChatRateLimiterStore_NonPositiveCapacityClampedToDefault(t *testing.T) {
	assert.Equal(t, maxTrackedChatLimiters, newChatRateLimiterStore(0, rate.Limit(1), 1).capacity)
	assert.Equal(t, maxTrackedChatLimiters, newChatRateLimiterStore(-5, rate.Limit(1), 1).capacity)
}

func TestHTTPTelegramClient_PerChatRateLimit(t *testing.T) {
	newOKServer := func() *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(TelegramResponse{OK: true, Result: &TelegramResult{MessageID: 1}})
		}))
	}

	t.Run("two chats interleave without blocking each other", func(t *testing.T) {
		server := newOKServer()
		defer server.Close()

		client := NewHTTPTelegramClient(server.URL, "test-token", slog.Default()).(*HTTPTelegramClient)
		// Fast, deterministic override: burst 1 so a second send to the same
		// chat must wait, while an unrelated chat is unaffected.
		client.chatLimiters = newChatRateLimiterStore(10, rate.Limit(20), 1)

		ctx := context.Background()
		_, err := client.SendMessage(ctx, &TelegramMessage{ChatID: "chatA", Text: "1"})
		require.NoError(t, err) // consumes chatA's only burst token

		start := time.Now()
		_, err = client.SendMessage(ctx, &TelegramMessage{ChatID: "chatB", Text: "1"})
		elapsed := time.Since(start)

		require.NoError(t, err)
		assert.Less(t, elapsed, 25*time.Millisecond,
			"chat B send should not be delayed by chat A's exhausted limiter")
	})

	t.Run("single chat burst is smoothed", func(t *testing.T) {
		server := newOKServer()
		defer server.Close()

		client := NewHTTPTelegramClient(server.URL, "test-token", slog.Default()).(*HTTPTelegramClient)
		client.chatLimiters = newChatRateLimiterStore(10, rate.Limit(20), 2) // burst 2, refill every 50ms

		ctx := context.Background()
		start := time.Now()
		for i := 0; i < 4; i++ {
			_, err := client.SendMessage(ctx, &TelegramMessage{ChatID: "chatA", Text: "1"})
			require.NoError(t, err)
		}
		elapsed := time.Since(start)

		// 4 sends, burst 2 free, 2 remaining wait ~50ms each. Floor is lower
		// than the theoretical ~100ms to tolerate CI jitter; ceiling only
		// guards against an outright hang.
		assert.GreaterOrEqual(t, elapsed, 60*time.Millisecond)
		assert.Less(t, elapsed, 2*time.Second)
	})

	t.Run("context cancellation during per-chat wait returns promptly", func(t *testing.T) {
		server := newOKServer()
		defer server.Close()

		client := NewHTTPTelegramClient(server.URL, "test-token", slog.Default()).(*HTTPTelegramClient)
		client.chatLimiters = newChatRateLimiterStore(10, rate.Limit(1), 1) // burst 1, next token in ~1s

		ctx := context.Background()
		_, err := client.SendMessage(ctx, &TelegramMessage{ChatID: "chatA", Text: "1"})
		require.NoError(t, err) // consumes the only token

		cctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()

		start := time.Now()
		_, err = client.SendMessage(cctx, &TelegramMessage{ChatID: "chatA", Text: "2"})
		elapsed := time.Since(start)

		require.Error(t, err)
		assert.Less(t, elapsed, 200*time.Millisecond,
			"ctx cancellation must return promptly, not wait out the full rate-limit delay")
	})
}
