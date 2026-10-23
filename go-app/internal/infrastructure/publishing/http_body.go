package publishing

import (
	"errors"
	"io"
	"net/url"

	"github.com/ipiton/AMP/internal/core"
)

// PUBLISH-SECRETS-IN-LOGS: what a target answers, and the URL it was reached
// at, end up in errors that the queue logs ("Failed to publish after retries")
// and keeps in the DLQ.
const (
	// maxResponseBodyBytes caps how much of a target's response is read. The
	// integrations answer with short JSON or a bare "ok"; an unbounded read let
	// one misbehaving endpoint grow a worker's memory without limit.
	maxResponseBodyBytes = 64 << 10

	// maxErrorBodyBytes caps how much of a response body is quoted in an error.
	maxErrorBodyBytes = 512
)

// readLimited reads at most maxResponseBodyBytes of r.
func readLimited(r io.Reader) ([]byte, error) {
	return io.ReadAll(io.LimitReader(r, maxResponseBodyBytes))
}

// errorBody is body as quoted in an error message: at most maxErrorBodyBytes.
func errorBody(body []byte) string {
	if len(body) <= maxErrorBodyBytes {
		return string(body)
	}
	return string(body[:maxErrorBodyBytes]) + "...(truncated)"
}

// redactURLError masks the request URL inside a transport error. http.Client
// returns *url.Error, whose message embeds the full URL — for Teams, Discord,
// Google Chat and Telegram that URL carries the token. The error is modified
// in place, so the chain (errors.Is/As on the cause) is kept.
func redactURLError(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		urlErr.URL = core.RedactURL(urlErr.URL)
	}
	return err
}
