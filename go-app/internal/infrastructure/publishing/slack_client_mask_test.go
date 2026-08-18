package publishing

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
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
