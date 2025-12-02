package publishing

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// ==================== WebhookHTTPClient Tests ====================

func TestWebhookHTTPClient_Post_Success(t *testing.T) {
	// Create test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("Expected POST, got %s", r.Method)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer server.Close()

	client := NewWebhookHTTPClient(DefaultWebhookRetryConfig, nil)
	payload := map[string]interface{}{"alert": "test"}
	headers := map[string]string{"Content-Type": "application/json"}

	ctx := context.Background()
	resp, err := client.Post(ctx, server.URL, payload, headers, nil)

	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}
}

func TestWebhookHTTPClient_Post_WithAuth(t *testing.T) {
	// Create test server that checks Authorization header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer test_token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewWebhookHTTPClient(DefaultWebhookRetryConfig, nil)
	payload := map[string]interface{}{"alert": "test"}
	headers := map[string]string{}
	authConfig := &AuthConfig{
		Type:  AuthTypeBearer,
		Token: "test_token",
	}

	ctx := context.Background()
	resp, err := client.Post(ctx, server.URL, payload, headers, authConfig)

	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}
}

func TestWebhookHTTPClient_Post_Retry429(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 2 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	config := WebhookRetryConfig{
		MaxRetries:  3,
		BaseBackoff: 10 * time.Millisecond,
		MaxBackoff:  1 * time.Second,
		Multiplier:  2.0,
	}
	client := NewWebhookHTTPClient(config, nil)
	payload := map[string]interface{}{"alert": "test"}

	ctx := context.Background()
	resp, err := client.Post(ctx, server.URL, payload, nil, nil)

	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("Expected status 200 after retry, got %d", resp.StatusCode)
	}
	if attempts != 2 {
		t.Errorf("Expected 2 attempts, got %d", attempts)
	}
}

func TestWebhookHTTPClient_Post_RetryServerError(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	config := WebhookRetryConfig{
		MaxRetries:  3,
		BaseBackoff: 10 * time.Millisecond,
		MaxBackoff:  1 * time.Second,
		Multiplier:  2.0,
	}
	client := NewWebhookHTTPClient(config, nil)
	payload := map[string]interface{}{"alert": "test"}

	ctx := context.Background()
	resp, err := client.Post(ctx, server.URL, payload, nil, nil)

	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("Expected status 200 after retry, got %d", resp.StatusCode)
	}
	if attempts != 3 {
		t.Errorf("Expected 3 attempts, got %d", attempts)
	}
}

func TestWebhookHTTPClient_Post_NoPermanentRetry(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusBadRequest) // Permanent error
	}))
	defer server.Close()

	config := WebhookRetryConfig{
		MaxRetries:  3,
		BaseBackoff: 10 * time.Millisecond,
		MaxBackoff:  1 * time.Second,
		Multiplier:  2.0,
	}
	client := NewWebhookHTTPClient(config, nil)
	payload := map[string]interface{}{"alert": "test"}

	ctx := context.Background()
	_, err := client.Post(ctx, server.URL, payload, nil, nil)

	if err == nil {
		t.Error("Expected error for 400 Bad Request")
	}
	if attempts != 1 {
		t.Errorf("Expected 1 attempt (no retry for permanent error), got %d", attempts)
	}
}

func TestWebhookHTTPClient_Post_ContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewWebhookHTTPClient(DefaultWebhookRetryConfig, nil)
	payload := map[string]interface{}{"alert": "test"}

	// Create context that cancels immediately
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := client.Post(ctx, server.URL, payload, nil, nil)

	if err == nil {
		t.Error("Expected error for cancelled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Expected context.Canceled error, got %v", err)
	}
}

func TestWebhookHTTPClient_Post_InvalidURL(t *testing.T) {
	client := NewWebhookHTTPClient(DefaultWebhookRetryConfig, nil)
	payload := map[string]interface{}{"alert": "test"}

	ctx := context.Background()
	_, err := client.Post(ctx, "://invalid-url", payload, nil, nil)

	if err == nil {
		t.Error("Expected error for invalid URL")
	}
}

func TestWebhookHTTPClient_Post_EmptyPayload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewWebhookHTTPClient(DefaultWebhookRetryConfig, nil)

	ctx := context.Background()
	resp, err := client.Post(ctx, server.URL, nil, nil, nil)

	if err != nil {
		t.Errorf("Unexpected error for empty payload: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}
}

func TestWebhookHTTPClient_Post_CustomHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		customHeader := r.Header.Get("X-Custom-Header")
		if customHeader != "custom-value" {
			t.Errorf("Expected X-Custom-Header=custom-value, got %s", customHeader)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewWebhookHTTPClient(DefaultWebhookRetryConfig, nil)
	payload := map[string]interface{}{"alert": "test"}
	headers := map[string]string{
		"X-Custom-Header": "custom-value",
	}

	ctx := context.Background()
	resp, err := client.Post(ctx, server.URL, payload, headers, nil)

	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}
}
