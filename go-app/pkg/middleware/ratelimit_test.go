package middleware

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestRateLimiter_PerIPLimit(t *testing.T) {
	// Create rate limiter: 2 requests per second per IP
	limiter := NewRateLimiter(RateLimiterConfig{
		PerIPLimit:  2,
		GlobalLimit: 0, // Disabled
		Logger:      slog.Default(),
	})
	defer limiter.Stop()

	// Create test handler
	handler := limiter.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	}))

	// First 2 requests should succeed
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("POST", "/webhook", nil)
		req.RemoteAddr = "192.168.1.1:12345"
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code, "Request %d should succeed", i+1)
	}

	// 3rd request should be rate limited
	req := httptest.NewRequest("POST", "/webhook", nil)
	req.RemoteAddr = "192.168.1.1:12345"
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusTooManyRequests, w.Code, "3rd request should be rate limited")
	assert.Equal(t, "1", w.Header().Get("Retry-After"), "Should have Retry-After header")
}

func TestRateLimiter_GlobalLimit(t *testing.T) {
	// Create rate limiter: 3 requests per second globally
	limiter := NewRateLimiter(RateLimiterConfig{
		PerIPLimit:  0, // Disabled
		GlobalLimit: 3,
		Logger:      slog.Default(),
	})
	defer limiter.Stop()

	// Create test handler
	handler := limiter.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	}))

	// First 3 requests from different IPs should succeed
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest("POST", "/webhook", nil)
		req.RemoteAddr = "192.168.1." + string(rune('1'+i)) + ":12345"
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code, "Request %d should succeed", i+1)
	}

	// 4th request should be rate limited (global limit)
	req := httptest.NewRequest("POST", "/webhook", nil)
	req.RemoteAddr = "192.168.1.4:12345"
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusTooManyRequests, w.Code, "4th request should be rate limited")
}

func TestRateLimiter_DifferentIPs(t *testing.T) {
	// Create rate limiter: 1 request per second per IP
	limiter := NewRateLimiter(RateLimiterConfig{
		PerIPLimit:  1,
		GlobalLimit: 0,
		Logger:      slog.Default(),
	})
	defer limiter.Stop()

	// Create test handler
	handler := limiter.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Requests from different IPs should not interfere
	ips := []string{"192.168.1.1:12345", "192.168.1.2:12345", "192.168.1.3:12345"}

	for _, ip := range ips {
		req := httptest.NewRequest("POST", "/webhook", nil)
		req.RemoteAddr = ip
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code, "Request from %s should succeed", ip)
	}
}

func TestRateLimiter_Cleanup(t *testing.T) {
	// Create rate limiter with short cleanup interval for testing
	limiter := NewRateLimiter(RateLimiterConfig{
		PerIPLimit:  10,
		GlobalLimit: 0,
		Logger:      slog.Default(),
	})
	defer limiter.Stop()

	// Trigger cleanup manually
	limiter.cleanup()

	// Should have no active limiters initially
	limiter.mu.RLock()
	count := len(limiter.ipLimiters)
	limiter.mu.RUnlock()

	assert.Equal(t, 0, count, "Should have no active limiters initially")

	// Make a request to create a limiter
	handler := limiter.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("POST", "/webhook", nil)
	req.RemoteAddr = "192.168.1.1:12345"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	// Should have 1 active limiter
	limiter.mu.RLock()
	count = len(limiter.ipLimiters)
	limiter.mu.RUnlock()

	assert.Equal(t, 1, count, "Should have 1 active limiter")

	// Wait for tokens to refill
	time.Sleep(2 * time.Second)

	// Cleanup should remove the inactive limiter
	limiter.cleanup()

	limiter.mu.RLock()
	count = len(limiter.ipLimiters)
	limiter.mu.RUnlock()

	assert.Equal(t, 0, count, "Cleanup should remove inactive limiters")
}

func TestRateLimiter_NoLimits(t *testing.T) {
	// Create rate limiter with no limits
	limiter := NewRateLimiter(RateLimiterConfig{
		PerIPLimit:  0,
		GlobalLimit: 0,
		Logger:      slog.Default(),
	})
	defer limiter.Stop()

	// Create test handler
	handler := limiter.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// All requests should succeed
	for i := 0; i < 100; i++ {
		req := httptest.NewRequest("POST", "/webhook", nil)
		req.RemoteAddr = "192.168.1.1:12345"
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code, "Request %d should succeed", i+1)
	}
}
