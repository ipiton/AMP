package llm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// classifyServer answers the proxy /classify endpoint with the given statuses
// in order (the last one repeats), counting the calls.
func classifyServer(t *testing.T, body string, statuses ...int) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := int(calls.Add(1)) - 1
		status := statuses[min(n, len(statuses)-1)]
		if status != http.StatusOK {
			http.Error(w, body, status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"classification":{"severity":3,"category":"infrastructure","summary":"ok","confidence":0.8}}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func retryTestClient(baseURL string, maxRetries int) *HTTPLLMClient {
	return NewHTTPLLMClient(Config{
		Provider:     "proxy",
		BaseURL:      baseURL,
		Model:        "m",
		MaxRetries:   maxRetries,
		RetryDelay:   time.Millisecond,
		RetryBackoff: 2,
		Timeout:      2 * time.Second,
	}, nil)
}

func TestClassifyAlert_RetriesServerErrorAndRateLimit(t *testing.T) {
	for _, status := range []int{http.StatusServiceUnavailable, http.StatusTooManyRequests, http.StatusInternalServerError} {
		srv, calls := classifyServer(t, "busy", status, http.StatusOK)
		client := retryTestClient(srv.URL, 3)

		result, err := client.ClassifyAlert(context.Background(), testAlert())
		if err != nil {
			t.Fatalf("status %d: ClassifyAlert error: %v", status, err)
		}
		if result == nil {
			t.Fatalf("status %d: nil result", status)
		}
		if got := calls.Load(); got != 2 {
			t.Errorf("status %d: calls = %d, want 2 (one failure, one retry)", status, got)
		}
	}
}

func TestClassifyAlert_ClientErrorNotRetried(t *testing.T) {
	srv, calls := classifyServer(t, "bad request", http.StatusBadRequest)
	client := retryTestClient(srv.URL, 3)

	if _, err := client.ClassifyAlert(context.Background(), testAlert()); err == nil {
		t.Fatal("expected an error for 400")
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("calls = %d, want 1: a 4xx other than 429 must not be retried", got)
	}
}

func TestClassifyAlert_MaxRetriesZeroMakesOneAttempt(t *testing.T) {
	srv, calls := classifyServer(t, "busy", http.StatusServiceUnavailable)
	client := retryTestClient(srv.URL, 0)

	if _, err := client.ClassifyAlert(context.Background(), testAlert()); err == nil {
		t.Fatal("expected an error for 503")
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("calls = %d, want 1: max_retries 0 means no retries", got)
	}
}

func TestClassifyAlert_ErrorBodyTruncated(t *testing.T) {
	long := strings.Repeat("x", 4096)
	srv, _ := classifyServer(t, long, http.StatusBadRequest)
	client := retryTestClient(srv.URL, 0)

	_, err := client.ClassifyAlert(context.Background(), testAlert())
	var he *HTTPError
	if !errors.As(err, &he) {
		t.Fatalf("expected *HTTPError, got %v", err)
	}
	if strings.Contains(he.Message, long) {
		t.Fatal("full response body leaked into the error")
	}
	if !strings.Contains(he.Message, "[truncated") {
		t.Errorf("missing truncation marker: %q", he.Message[:min(len(he.Message), 120)])
	}
	if !strings.Contains(err.Error(), "HTTP 400:") {
		t.Errorf("status missing from the error text: %q", err.Error()[:min(len(err.Error()), 80)])
	}
}

func TestTruncateBody_RuneBoundary(t *testing.T) {
	short := []byte("short body")
	if got := truncateBody(short); got != "short body" {
		t.Errorf("short body changed: %q", got)
	}

	// "я" is two bytes: an odd-length prefix puts byte 512 inside a rune.
	body := []byte("a" + strings.Repeat("я", 400))
	got := truncateBody(body)
	head := got[:strings.Index(got, "...[truncated")]
	if len(head) > maxErrorBodyBytes {
		t.Errorf("kept %d bytes, want <= %d", len(head), maxErrorBodyBytes)
	}
	if !strings.HasSuffix(head, "я") {
		t.Errorf("cut inside a rune: %q", head[len(head)-4:])
	}
}

func TestIsRetryableError_HTTPError(t *testing.T) {
	cases := map[int]bool{429: true, 500: true, 502: true, 503: true, 400: false, 401: false, 404: false}
	for code, want := range cases {
		if got := IsRetryableError(&HTTPError{StatusCode: code}); got != want {
			t.Errorf("status %d: retryable = %v, want %v", code, got, want)
		}
	}
	if !IsRetryableError(fmtWrap(&HTTPError{StatusCode: 503})) {
		t.Error("a wrapped 503 must stay retryable")
	}
}

func fmtWrap(err error) error { return errors.Join(errors.New("classify"), err) }
