package publishing

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/ipiton/AMP/pkg/httperror"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// SLACK-WEBHOOK-PLAIN-OK-RESPONSE: an Incoming Webhook answers a successful
// post with a bare `ok` in text/plain, not the Web API's JSON. The client used
// to require JSON on 200, so every delivered webhook message came back as
// "failed to parse response", the group path recorded no nflog entry, and the
// group was re-sent on every group_interval.
func TestSlackClient_ResponseHandling(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		contentType string // "" leaves Go's content sniffing in charge
		body        string

		wantErr      bool
		wantErrText  string // substring of err.Error()
		wantStatus   int    // HTTPAPIError.StatusCode, 0 = not an HTTPAPIError
		wantTS       string
		wantAttempts int32
	}{
		{name: "webhook ok", status: 200, contentType: "text/plain; charset=utf-8", body: "ok", wantAttempts: 1},
		{name: "webhook ok with trailing newline", status: 200, contentType: "text/plain", body: "ok\n", wantAttempts: 1},
		{name: "2xx with empty body", status: 204, wantAttempts: 1},
		{name: "web API ok=true keeps ts", status: 200, contentType: "application/json", body: `{"ok":true,"ts":"1700000000.000100"}`, wantTS: "1700000000.000100", wantAttempts: 1},
		{
			name: "web API ok=false is an error", status: 200, contentType: "application/json", body: `{"ok":false,"error":"channel_not_found"}`,
			wantErr: true, wantErrText: "channel_not_found", wantStatus: 200, wantAttempts: 1,
		},
		{
			name: "JSON body without JSON content type is still parsed", status: 200, contentType: "text/plain", body: `{"ok":false,"error":"not_in_channel"}`,
			wantErr: true, wantErrText: "not_in_channel", wantStatus: 200, wantAttempts: 1,
		},
		{
			name: "2xx with error text is a permanent error", status: 200, contentType: "text/plain", body: "invalid_token",
			wantErr: true, wantErrText: "invalid_token", wantStatus: 200, wantAttempts: 1,
		},
		{
			name: "400 invalid_payload is not retried", status: 400, contentType: "text/plain", body: "invalid_payload",
			wantErr: true, wantErrText: "invalid_payload", wantStatus: 400, wantAttempts: 1,
		},
		{
			name: "404 no_service is not retried", status: 404, contentType: "text/plain", body: "no_service",
			wantErr: true, wantErrText: "no_service", wantStatus: 404, wantAttempts: 1,
		},
		{
			name: "500 text is retried", status: 500, contentType: "text/plain", body: "rollup_error",
			wantErr: true, wantErrText: "rollup_error", wantStatus: 500, wantAttempts: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var attempts atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				attempts.Add(1)
				if tt.contentType != "" {
					w.Header().Set("Content-Type", tt.contentType)
				}
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.body)
			}))
			defer srv.Close()

			webhookURL := srv.URL + "/services/T00000000/B00000000/sup3rSecretSlackToken"
			client := NewHTTPSlackWebhookClient(webhookURL, slog.New(slog.NewTextHandler(io.Discard, nil)))

			resp, err := client.PostMessage(context.Background(), &SlackMessage{Text: "hello"})
			assert.Equal(t, tt.wantAttempts, attempts.Load(), "attempts")

			if !tt.wantErr {
				require.NoError(t, err)
				require.NotNil(t, resp)
				assert.True(t, resp.OK)
				assert.Equal(t, tt.wantTS, resp.TS)
				return
			}

			require.Error(t, err)
			assert.Nil(t, resp)
			assert.Contains(t, err.Error(), tt.wantErrText, "the error must carry Slack's own reason")
			assert.NotContains(t, err.Error(), "sup3rSecretSlackToken", "the error must not carry the webhook credential")
			var apiErr *httperror.HTTPAPIError
			require.True(t, errors.As(err, &apiErr), "want *HTTPAPIError, got %T: %v", err, err)
			assert.Equal(t, tt.wantStatus, apiErr.StatusCode)
		})
	}
}
