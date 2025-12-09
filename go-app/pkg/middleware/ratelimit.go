// Package middleware provides HTTP middleware components.
package middleware

import (
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// RateLimiter provides HTTP rate limiting middleware using token bucket algorithm.
//
// Features:
//   - Per-IP rate limiting with configurable limits
//   - Global rate limiting across all IPs
//   - Automatic cleanup of inactive limiters
//   - Thread-safe implementation
//   - Prometheus metrics integration
//
// Usage:
//
//	limiter := middleware.NewRateLimiter(middleware.RateLimiterConfig{
//	    PerIPLimit:  100,  // 100 requests per second per IP
//	    GlobalLimit: 1000, // 1000 requests per second total
//	    Logger:      slog.Default(),
//	})
//	http.Handle("/webhook", limiter.Middleware(webhookHandler))
type RateLimiter struct {
	perIPLimit  int
	globalLimit int
	logger      *slog.Logger

	// Per-IP limiters
	ipLimiters map[string]*rate.Limiter
	mu         sync.RWMutex

	// Global limiter
	globalLimiter *rate.Limiter

	// Cleanup ticker
	cleanupTicker *time.Ticker
	stopCleanup   chan struct{}
}

// RateLimiterConfig holds configuration for rate limiter.
type RateLimiterConfig struct {
	// PerIPLimit is the maximum requests per second per IP (0 = unlimited)
	PerIPLimit int

	// GlobalLimit is the maximum requests per second globally (0 = unlimited)
	GlobalLimit int

	// Logger for rate limit events
	Logger *slog.Logger
}

// NewRateLimiter creates a new rate limiter middleware.
//
// Parameters:
//   - config: Rate limiter configuration
//
// Returns:
//   - *RateLimiter: Configured rate limiter
func NewRateLimiter(config RateLimiterConfig) *RateLimiter {
	if config.Logger == nil {
		config.Logger = slog.Default()
	}

	rl := &RateLimiter{
		perIPLimit:  config.PerIPLimit,
		globalLimit: config.GlobalLimit,
		logger:      config.Logger,
		ipLimiters:  make(map[string]*rate.Limiter),
		stopCleanup: make(chan struct{}),
	}

	// Create global limiter if enabled
	if config.GlobalLimit > 0 {
		rl.globalLimiter = rate.NewLimiter(rate.Limit(config.GlobalLimit), config.GlobalLimit)
	}

	// Start cleanup goroutine (every 10 minutes)
	rl.cleanupTicker = time.NewTicker(10 * time.Minute)
	go rl.cleanupLoop()

	return rl
}

// getIPLimiter returns or creates a rate limiter for the given IP.
func (rl *RateLimiter) getIPLimiter(ip string) *rate.Limiter {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	limiter, exists := rl.ipLimiters[ip]
	if !exists {
		limiter = rate.NewLimiter(rate.Limit(rl.perIPLimit), rl.perIPLimit)
		rl.ipLimiters[ip] = limiter
	}

	return limiter
}

// cleanupLoop periodically removes inactive IP limiters to prevent memory leaks.
func (rl *RateLimiter) cleanupLoop() {
	for {
		select {
		case <-rl.cleanupTicker.C:
			rl.cleanup()
		case <-rl.stopCleanup:
			rl.cleanupTicker.Stop()
			return
		}
	}
}

// cleanup removes IP limiters that haven't been used recently.
func (rl *RateLimiter) cleanup() {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	// Remove limiters with full token buckets (inactive for a while)
	for ip, limiter := range rl.ipLimiters {
		// If limiter has full tokens, it hasn't been used recently
		if limiter.Tokens() == float64(rl.perIPLimit) {
			delete(rl.ipLimiters, ip)
		}
	}

	if rl.logger.Enabled(nil, slog.LevelDebug) {
		rl.logger.Debug("Rate limiter cleanup completed", "active_ips", len(rl.ipLimiters))
	}
}

// Stop stops the rate limiter and cleanup goroutine.
func (rl *RateLimiter) Stop() {
	close(rl.stopCleanup)
}

// Middleware returns an HTTP middleware that enforces rate limiting.
//
// Behavior:
//   - Checks global rate limit first (if enabled)
//   - Then checks per-IP rate limit (if enabled)
//   - Returns 429 Too Many Requests if limit exceeded
//   - Adds Retry-After header with suggested wait time
func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check global rate limit
		if rl.globalLimiter != nil && !rl.globalLimiter.Allow() {
			rl.logger.Warn("Global rate limit exceeded",
				"path", r.URL.Path,
				"method", r.Method,
				"remote_addr", r.RemoteAddr)

			w.Header().Set("Retry-After", "1")
			http.Error(w, "Global rate limit exceeded", http.StatusTooManyRequests)
			return
		}

		// Check per-IP rate limit
		if rl.perIPLimit > 0 {
			ip, _, err := net.SplitHostPort(r.RemoteAddr)
			if err != nil {
				// If we can't parse IP, use full RemoteAddr
				ip = r.RemoteAddr
			}

			limiter := rl.getIPLimiter(ip)
			if !limiter.Allow() {
				rl.logger.Warn("Per-IP rate limit exceeded",
					"ip", ip,
					"path", r.URL.Path,
					"method", r.Method)

				w.Header().Set("Retry-After", "1")
				http.Error(w, "Rate limit exceeded", http.StatusTooManyRequests)
				return
			}
		}

		// Rate limit passed, continue to next handler
		next.ServeHTTP(w, r)
	})
}
