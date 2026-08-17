package publishing

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ipiton/AMP/pkg/httperror"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
