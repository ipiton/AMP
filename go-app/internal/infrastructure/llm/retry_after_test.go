package llm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// retryAfterServer answers 429 with the given Retry-After first, then 200,
// recording when each call arrived.
func retryAfterServer(t *testing.T, retryAfter string) (*httptest.Server, *atomic.Int32, func() []time.Time) {
	t.Helper()
	var (
		calls atomic.Int32
		mu    sync.Mutex
		at    []time.Time
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		at = append(at, time.Now())
		mu.Unlock()
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", retryAfter)
			http.Error(w, "slow down", http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"classification":{"severity":3,"category":"infrastructure","summary":"ok","confidence":0.8}}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &calls, func() []time.Time {
		mu.Lock()
		defer mu.Unlock()
		return append([]time.Time(nil), at...)
	}
}

func retryAfterClient(baseURL string, retryDelay time.Duration) *HTTPLLMClient {
	return NewHTTPLLMClient(Config{
		Provider:     "proxy",
		BaseURL:      baseURL,
		Model:        "m",
		MaxRetries:   3,
		RetryDelay:   retryDelay,
		RetryBackoff: 2,
		Timeout:      5 * time.Second,
	}, nil)
}

func TestClassifyAlert_WaitsForRetryAfter(t *testing.T) {
	srv, calls, times := retryAfterServer(t, "1")
	// Base delay 200ms: the backoff alone would retry well before 1s, and
	// the 2s cap (10x) lets a 1s hint through.
	client := retryAfterClient(srv.URL, 200*time.Millisecond)

	if _, err := client.ClassifyAlert(context.Background(), testAlert()); err != nil {
		t.Fatalf("ClassifyAlert error: %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("calls = %d, want 2", got)
	}
	at := times()
	if gap := at[1].Sub(at[0]); gap < 950*time.Millisecond {
		t.Errorf("retried after %v, want >= Retry-After 1s", gap)
	}
}

func TestClassifyAlert_RetryAfterAboveCapNotRetried(t *testing.T) {
	srv, calls, _ := retryAfterServer(t, "120")
	client := retryAfterClient(srv.URL, time.Millisecond) // cap 10ms

	start := time.Now()
	_, err := client.ClassifyAlert(context.Background(), testAlert())
	var he *HTTPError
	if !errors.As(err, &he) || he.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("want the 429 HTTPError, got %v", err)
	}
	if he.RetryAfter != 120*time.Second {
		t.Errorf("RetryAfter = %v, want 120s", he.RetryAfter)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("calls = %d, want 1: a Retry-After above the cap fails fast", got)
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("took %v, want an immediate failure", d)
	}
}

func TestLLMErrorChecker_ZeroCapKeepsRetrying(t *testing.T) {
	// investigate_with_tools.go builds the checker without a cap.
	err := &HTTPError{StatusCode: http.StatusTooManyRequests, RetryAfter: time.Hour}
	if !(&llmErrorChecker{}).IsRetryable(err) {
		t.Error("a zero cap must not change the retry decision")
	}
	if (&llmErrorChecker{maxRetryAfter: time.Second}).IsRetryable(err) {
		t.Error("a hint above the cap must not be retried")
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, h string
		want    time.Duration
	}{
		{"empty", "", 0},
		{"seconds", "7", 7 * time.Second},
		{"spaces", " 3 ", 3 * time.Second},
		{"negative", "-5", 0},
		{"huge seconds capped", "99999999999", maxRetryAfter},
		{"http date", now.Add(90 * time.Second).Format(http.TimeFormat), 90 * time.Second},
		{"date in the past", now.Add(-time.Minute).Format(http.TimeFormat), 0},
		{"far date capped", now.Add(72 * time.Hour).Format(http.TimeFormat), maxRetryAfter},
		{"garbage", "soon", 0},
	} {
		if got := parseRetryAfter(tc.h, now); got != tc.want {
			t.Errorf("%s: parseRetryAfter(%q) = %v, want %v", tc.name, tc.h, got, tc.want)
		}
	}
}
