package publishing

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Final review finding 13: a Slack incoming-webhook URL IS the credential —
// anyone holding it can post to the channel. http.Client.Do returns *url.Error,
// whose Error() embeds the full request URL, and doRequestWithRetry both logged
// that string and wrapped it into the returned error. Any log sink or caller
// that recorded it captured a working Slack credential.

const testSlackWebhookURL = "https://hooks.slack.example/services/T00000000/B00000000/sup3rSecretSlackToken"

func TestSlackClient_NetworkErrorMasksWebhookURL(t *testing.T) {
	// A server that is immediately closed gives a deterministic dial failure,
	// so the *url.Error path is exercised without waiting on DNS.
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	deadURL := srv.URL + "/services/T00000000/B00000000/sup3rSecretSlackToken"
	srv.Close()

	client := NewHTTPSlackWebhookClient(deadURL, slog.New(slog.NewTextHandler(io.Discard, nil)))

	_, err := client.PostMessage(context.Background(), &SlackMessage{Text: "hello"})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "sup3rSecretSlackToken",
		"the returned error must not carry the webhook credential: %s", err)
	assert.Contains(t, err.Error(), slackWebhookURLPlaceholder)
}

func TestSlackClient_MaskWebhookURL(t *testing.T) {
	client := NewHTTPSlackWebhookClient(testSlackWebhookURL, slog.Default()).(*HTTPSlackWebhookClient)

	// Full-URL form, as it appears inside a *url.Error message.
	masked := client.maskWebhookURL(`Post "` + testSlackWebhookURL + `": dial tcp: connection refused`)
	assert.NotContains(t, masked, "sup3rSecretSlackToken")
	assert.Contains(t, masked, "connection refused", "the diagnostic part must survive")

	// Path-only form, in case a wrapper strips scheme/host.
	maskedPath := client.maskWebhookURL("request to /services/T00000000/B00000000/sup3rSecretSlackToken failed")
	assert.NotContains(t, maskedPath, "sup3rSecretSlackToken")

	// An empty webhook URL must not turn the masker into a no-op that panics or
	// replaces everything.
	empty := NewHTTPSlackWebhookClient("", slog.Default()).(*HTTPSlackWebhookClient)
	assert.Equal(t, "unchanged", empty.maskWebhookURL("unchanged"))
}

func TestSlackClient_RetryLogMasksWebhookURL(t *testing.T) {
	var logBuf strings.Builder
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	deadURL := srv.URL + "/services/T00000000/B00000000/sup3rSecretSlackToken"
	srv.Close()

	client := NewHTTPSlackWebhookClient(deadURL,
		slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})))

	_, _ = client.PostMessage(context.Background(), &SlackMessage{Text: "hello"})

	assert.NotContains(t, logBuf.String(), "sup3rSecretSlackToken",
		"no log line may carry the webhook credential:\n%s", logBuf.String())
}

// TestSlackClient_MaskedErrorPreservesChain is wave re-review Minor 3: masking
// via `fmt.Errorf("...: %s", masked)` stripped the secret but also broke the
// error chain, so callers lost errors.Is/errors.As against the underlying
// *url.Error. The masked message and the intact chain must coexist.
func TestSlackClient_MaskedErrorPreservesChain(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	deadURL := srv.URL + "/services/T00000000/B00000000/sup3rSecretSlackToken"
	srv.Close()

	client := NewHTTPSlackWebhookClient(deadURL, slog.New(slog.NewTextHandler(io.Discard, nil)))
	_, err := client.PostMessage(context.Background(), &SlackMessage{Text: "hello"})
	require.Error(t, err)

	assert.NotContains(t, err.Error(), "sup3rSecretSlackToken", "the message must stay masked")

	var urlErr *url.Error
	assert.True(t, errors.As(err, &urlErr),
		"the underlying *url.Error must remain reachable via errors.As, or callers lose all network-error classification")
}

func TestMaskedError_ErrorAndUnwrap(t *testing.T) {
	cause := errors.New("Post \"https://hooks.slack.example/services/AAA/BBB/tok\": dial tcp: refused")
	masked := newMaskedError("Post \"***\": dial tcp: refused", cause)

	assert.Equal(t, "Post \"***\": dial tcp: refused", masked.Error())
	assert.NotContains(t, masked.Error(), "tok\"")
	assert.ErrorIs(t, masked, cause, "Unwrap must expose the cause for errors.Is/As")

	// And through one more fmt.Errorf %w hop, as the client does.
	wrapped := fmt.Errorf("HTTP request failed: %w", masked)
	assert.NotContains(t, wrapped.Error(), "/services/AAA/BBB/tok")
	assert.ErrorIs(t, wrapped, cause)
}
