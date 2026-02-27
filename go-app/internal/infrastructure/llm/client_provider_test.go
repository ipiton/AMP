package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ipiton/AMP/internal/core"
)

func TestHTTPLLMClient_ClassifyAlert_OpenAIProvider(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("expected openai path /v1/chat/completions, got %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-test" {
			t.Fatalf("expected bearer auth header, got %q", got)
		}

		var reqBody map[string]any
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			t.Fatalf("failed to decode request body: %v", err)
		}
		if reqBody["model"] != "gpt-4o-mini" {
			t.Fatalf("expected model gpt-4o-mini, got %v", reqBody["model"])
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"choices": [{
				"message": {
					"content": "{\"severity\":4,\"category\":\"infrastructure\",\"summary\":\"CPU critical\",\"confidence\":0.92,\"reasoning\":\"high cpu on node\",\"suggestions\":[\"scale up\",\"check noisy workload\"]}"
				}
			}]
		}`))
	}))
	defer server.Close()

	client := NewHTTPLLMClient(Config{
		Provider:    "openai",
		BaseURL:     server.URL + "/v1",
		APIKey:      "sk-test",
		Model:       "gpt-4o-mini",
		MaxRetries:  1,
		RetryDelay:  1 * time.Millisecond,
		Timeout:     2 * time.Second,
		MaxTokens:   400,
		Temperature: 0.1,
	}, nil)

	result, err := client.ClassifyAlert(context.Background(), testAlert())
	if err != nil {
		t.Fatalf("ClassifyAlert returned error: %v", err)
	}
	if result == nil {
		t.Fatalf("expected non-nil result")
	}
	if result.Severity != core.SeverityCritical {
		t.Fatalf("expected severity critical, got %s", result.Severity)
	}
	if result.Confidence != 0.92 {
		t.Fatalf("expected confidence 0.92, got %v", result.Confidence)
	}
}

func TestHTTPLLMClient_Health_OpenAIProvider(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("expected openai health path /v1/models, got %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-health" {
			t.Fatalf("expected bearer auth header, got %q", got)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewHTTPLLMClient(Config{
		Provider:   "openai",
		BaseURL:    server.URL + "/v1",
		APIKey:     "sk-health",
		MaxRetries: 1,
		Timeout:    2 * time.Second,
	}, nil)

	if err := client.Health(context.Background()); err != nil {
		t.Fatalf("Health returned error: %v", err)
	}
}

func TestHTTPLLMClient_ClassifyAlert_ProxyProviderCompatibility(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/classify" {
			t.Fatalf("expected legacy proxy path /classify, got %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer proxy-key" {
			t.Fatalf("expected bearer auth header, got %q", got)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"classification": {
				"severity": 3,
				"category": "application",
				"summary": "legacy proxy",
				"confidence": 0.8,
				"reasoning": "proxy response",
				"suggestions": ["check app logs"]
			},
			"request_id": "r-1",
			"processing_time": "120ms"
		}`))
	}))
	defer server.Close()

	client := NewHTTPLLMClient(Config{
		Provider:   "proxy",
		BaseURL:    strings.TrimRight(server.URL, "/"),
		APIKey:     "proxy-key",
		Model:      "custom",
		MaxRetries: 1,
		RetryDelay: 1 * time.Millisecond,
		Timeout:    2 * time.Second,
	}, nil)

	result, err := client.ClassifyAlert(context.Background(), testAlert())
	if err != nil {
		t.Fatalf("ClassifyAlert returned error: %v", err)
	}
	if result == nil {
		t.Fatalf("expected non-nil result")
	}
	if result.Severity != core.SeverityWarning {
		t.Fatalf("expected severity warning, got %s", result.Severity)
	}
}

func testAlert() *core.Alert {
	started := time.Now().UTC()
	return &core.Alert{
		Fingerprint: "fp-test-1",
		AlertName:   "CPUHigh",
		Status:      core.StatusFiring,
		Labels: map[string]string{
			"severity":  "critical",
			"service":   "api",
			"namespace": "prod",
		},
		Annotations: map[string]string{
			"summary": "CPU usage above threshold",
		},
		StartsAt: started,
	}
}
