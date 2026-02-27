package application

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	appconfig "github.com/ipiton/AMP/internal/config"
	infrastructurecache "github.com/ipiton/AMP/internal/infrastructure/cache"
)

func TestInitializeClassification_UsesOpenAIHealthEndpoint(t *testing.T) {
	var gotPath string
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	registry := &ServiceRegistry{
		config: &appconfig.Config{
			LLM: appconfig.LLMConfig{
				Enabled:     true,
				Provider:    "openai",
				APIKey:      "sk-test",
				BaseURL:     server.URL + "/v1",
				Model:       "gpt-4o-mini",
				MaxTokens:   200,
				Temperature: 0.1,
				Timeout:     1 * time.Second,
				MaxRetries:  1,
			},
		},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		cache:  infrastructurecache.NewMemoryCache(nil),
	}

	if err := registry.initializeClassification(context.Background()); err != nil {
		t.Fatalf("initializeClassification returned error: %v", err)
	}
	if registry.classificationSvc == nil {
		t.Fatalf("classification service must be initialized")
	}
	if err := registry.classificationSvc.Health(context.Background()); err != nil {
		t.Fatalf("classification service health returned error: %v", err)
	}

	if gotPath != "/v1/models" {
		t.Fatalf("expected openai health path /v1/models, got %q", gotPath)
	}
	if gotAuth != "Bearer sk-test" {
		t.Fatalf("expected bearer auth header, got %q", gotAuth)
	}
}

func TestInitializeClassification_UsesProxyHealthEndpoint(t *testing.T) {
	var gotPath string
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	registry := &ServiceRegistry{
		config: &appconfig.Config{
			LLM: appconfig.LLMConfig{
				Enabled:    true,
				Provider:   "proxy",
				APIKey:     "proxy-key",
				BaseURL:    server.URL,
				Model:      "custom-proxy",
				Timeout:    1 * time.Second,
				MaxRetries: 1,
			},
		},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		cache:  infrastructurecache.NewMemoryCache(nil),
	}

	if err := registry.initializeClassification(context.Background()); err != nil {
		t.Fatalf("initializeClassification returned error: %v", err)
	}
	if registry.classificationSvc == nil {
		t.Fatalf("classification service must be initialized")
	}
	if err := registry.classificationSvc.Health(context.Background()); err != nil {
		t.Fatalf("classification service health returned error: %v", err)
	}

	if gotPath != "/health" {
		t.Fatalf("expected proxy health path /health, got %q", gotPath)
	}
	if gotAuth != "" {
		t.Fatalf("expected no auth header for proxy health, got %q", gotAuth)
	}
}
