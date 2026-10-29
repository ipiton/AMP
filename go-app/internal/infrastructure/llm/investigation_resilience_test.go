package llm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ipiton/AMP/internal/core"
	inv "github.com/ipiton/AMP/internal/core/investigation"
)

const investigateReply = `{"choices":[{"message":{"role":"assistant","content":"{\"summary\":\"s\",\"findings\":{},\"recommendations\":[],\"confidence\":0.5}"},"finish_reason":"stop"}]}`

// statusServer answers with statuses[i] on the i-th call (the last one
// repeats) and counts calls.
func statusServer(t *testing.T, statuses ...int) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := int(calls.Add(1)) - 1
		if n >= len(statuses) {
			n = len(statuses) - 1
		}
		if statuses[n] != http.StatusOK {
			w.WriteHeader(statuses[n])
			_, _ = w.Write([]byte(`{"error":"x"}`))
			return
		}
		_, _ = w.Write([]byte(investigateReply))
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func resilienceClient(baseURL string, cb CircuitBreakerConfig) *HTTPLLMClient {
	return NewHTTPLLMClient(Config{
		Provider:       "openai",
		BaseURL:        baseURL,
		Model:          "m",
		MaxRetries:     3,
		RetryDelay:     time.Millisecond,
		RetryBackoff:   2,
		Timeout:        2 * time.Second,
		CircuitBreaker: cb,
	}, nil)
}

// breakerConfig opens after two consecutive failures and stays open for the
// whole test.
func breakerConfig() CircuitBreakerConfig {
	cb := DefaultCircuitBreakerConfig()
	cb.MaxFailures = 2
	cb.ResetTimeout = time.Hour
	return cb
}

func TestInvestigateWithTools_RetriesTransientStepFailure(t *testing.T) {
	srv, calls := statusServer(t, http.StatusServiceUnavailable, http.StatusServiceUnavailable, http.StatusOK)
	c := resilienceClient(srv.URL, CircuitBreakerConfig{})

	resp, err := c.InvestigateWithTools(context.Background(), testAlert(), nil, nil, nil, inv.PromptContext{})
	if err != nil {
		t.Fatalf("503, 503, 200 must succeed after retries: %v", err)
	}
	if resp == nil {
		t.Fatal("nil response")
	}
	if got := calls.Load(); got != 3 {
		t.Errorf("calls = %d, want 3", got)
	}
}

func TestInvestigateWithTools_DoesNotRetryBadRequest(t *testing.T) {
	srv, calls := statusServer(t, http.StatusBadRequest)
	c := resilienceClient(srv.URL, CircuitBreakerConfig{})

	_, err := c.InvestigateWithTools(context.Background(), testAlert(), nil, nil, nil, inv.PromptContext{})
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusBadRequest {
		t.Fatalf("want HTTPError 400, got %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("calls = %d, want 1 (400 is not retryable)", got)
	}
}

// A step must not be refused mid-run by the breaker: the run already passed
// it in InvestigateAlert.
func TestInvestigateWithTools_IgnoresOpenBreaker(t *testing.T) {
	srv, _ := statusServer(t, http.StatusServiceUnavailable, http.StatusServiceUnavailable, http.StatusOK)
	c := resilienceClient(srv.URL, breakerConfig())
	for i := 0; i < 2; i++ {
		_, _ = c.InvestigateAlert(context.Background(), testAlert(), nil)
	}
	if c.investigationBreaker.GetState() != StateOpen {
		t.Fatal("setup: investigation breaker should be open")
	}

	if _, err := c.InvestigateWithTools(context.Background(), testAlert(), nil, nil, nil, inv.PromptContext{}); err != nil {
		t.Fatalf("step must not go through the breaker: %v", err)
	}
}

func TestInvestigateAlert_BreakerOpensOnUnavailability(t *testing.T) {
	srv, calls := statusServer(t, http.StatusServiceUnavailable)
	c := resilienceClient(srv.URL, breakerConfig())

	for i := 0; i < 2; i++ {
		if _, err := c.InvestigateAlert(context.Background(), testAlert(), nil); err == nil {
			t.Fatal("503 must fail")
		}
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("calls = %d, want 2 (no retries inside InvestigateAlert)", got)
	}

	_, err := c.InvestigateAlert(context.Background(), testAlert(), nil)
	if !errors.Is(err, ErrCircuitBreakerOpen) {
		t.Fatalf("want ErrCircuitBreakerOpen, got %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("open breaker must not reach the network, calls = %d", got)
	}
	if c.circuitBreaker.GetState() != StateClosed {
		t.Error("investigation failures must not touch the classification breaker")
	}
}

func TestInvestigateAlert_BadRequestDoesNotOpenBreaker(t *testing.T) {
	srv, calls := statusServer(t, http.StatusBadRequest)
	c := resilienceClient(srv.URL, breakerConfig())

	for i := 0; i < 4; i++ {
		_, err := c.InvestigateAlert(context.Background(), testAlert(), nil)
		var httpErr *HTTPError
		if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusBadRequest {
			t.Fatalf("call %d: want HTTPError 400, got %v", i, err)
		}
	}
	if got := calls.Load(); got != 4 {
		t.Errorf("calls = %d, want 4", got)
	}
	if c.investigationBreaker.GetState() != StateClosed {
		t.Error("400 is the request's fault and must not open the breaker")
	}
}

// slow_call_duration from llm.circuit_breaker must not apply: an agent step
// routinely runs longer than it.
func TestInvestigateAlert_SlowCallsDoNotOpenBreaker(t *testing.T) {
	srv, _ := statusServer(t, http.StatusOK)
	cb := breakerConfig()
	cb.SlowCallDuration = time.Nanosecond
	c := resilienceClient(srv.URL, cb)

	for i := 0; i < 4; i++ {
		if _, err := c.InvestigateAlert(context.Background(), testAlert(), nil); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if c.investigationBreaker.GetState() != StateClosed {
		t.Error("slow successful calls must not open the investigation breaker")
	}
}

func TestInvestigateAlert_CallerDeadlineDoesNotOpenBreaker(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-block:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() { close(block); srv.Close() })
	c := resilienceClient(srv.URL, breakerConfig())

	for i := 0; i < 3; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		_, err := c.InvestigateAlert(ctx, testAlert(), nil)
		cancel()
		if err == nil || errors.Is(err, ErrCircuitBreakerOpen) {
			t.Fatalf("call %d: want deadline error, got %v", i, err)
		}
	}
	if c.investigationBreaker.GetState() != StateClosed {
		t.Error("the caller's own deadline must not count against the provider")
	}
}

func TestInvestigation_UnsupportedProviderIsPermanentWithoutNetwork(t *testing.T) {
	srv, calls := statusServer(t, http.StatusOK)
	c := NewHTTPLLMClient(Config{Provider: "proxy", BaseURL: srv.URL, Timeout: time.Second}, nil)

	_, err := c.InvestigateAlert(context.Background(), testAlert(), nil)
	if !errors.Is(err, core.ErrLLMProviderUnsupported) {
		t.Errorf("InvestigateAlert: want ErrLLMProviderUnsupported, got %v", err)
	}
	_, err = c.InvestigateWithTools(context.Background(), testAlert(), nil, nil, nil, inv.PromptContext{})
	if !errors.Is(err, core.ErrLLMProviderUnsupported) {
		t.Errorf("InvestigateWithTools: want ErrLLMProviderUnsupported, got %v", err)
	}
	if got := calls.Load(); got != 0 {
		t.Errorf("calls = %d, want 0", got)
	}
}
