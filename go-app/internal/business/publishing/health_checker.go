package publishing

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/ipiton/AMP/internal/core"
)

// httpConnectivityTest performs TCP + HTTP connectivity test for target.
//
// This function implements comprehensive health check:
//   1. Parse target URL (validate format)
//   2. TCP handshake (fail fast if unreachable)
//   3. HTTP GET request (validate response)
//   4. Measure latency (full execution time)
//   5. Classify errors (timeout/dns/tls/refused/http_error)
//
// Test Strategy:
//   - TCP first: Fail fast if network unreachable (~50ms)
//   - HTTP next: Validate HTTP stack (~200ms)
//   - Timeout: 5s max (configurable)
//
// Error Classification:
//   - Timeout: Connection timeout after 5s
//   - DNS: No such host / DNS resolution failed
//   - TLS: Certificate validation failed
//   - Refused: Connection refused (target down)
//   - HTTP: HTTP status >= 400 (application error)
//   - Unknown: Other errors
//
// Parameters:
//   - ctx: Context (for cancellation)
//   - targetURL: Target URL (e.g., "https://api.rootly.com")
//   - httpClient: HTTP client (with timeout & connection pooling)
//   - config: Health configuration (timeouts, TLS settings)
//
// Returns:
//   - success: true if HTTP 200-299, false otherwise
//   - statusCode: HTTP status code (or nil if TCP error)
//   - latencyMs: Response time in milliseconds (or nil if error)
//   - errorMsg: Error message (or nil if success)
//   - errorType: Classified error type
//
// Performance:
//   - Success: ~100-300ms (typical HTTP roundtrip)
//   - Timeout: ~5s (max timeout)
//   - TCP failure: ~50ms (fail fast)
//
// Example:
//
//	success, code, latency, err, errType := httpConnectivityTest(
//	    ctx,
//	    "https://api.rootly.com/v1",
//	    httpClient,
//	    config,
//	)
//	if success {
//	    log.Info("Target healthy", "latency_ms", *latency)
//	} else {
//	    log.Error("Target unhealthy", "error", *err, "type", errType)
//	}
func httpConnectivityTest(
	ctx context.Context,
	targetURL string,
	httpClient *http.Client,
	config HealthConfig,
) (success bool, statusCode *int, latencyMs *int64, errorMsg *string, errorType ErrorType) {
	startTime := time.Now()

	// Step 1: Parse URL
	parsedURL, err := url.Parse(targetURL)
	if err != nil {
		latency := time.Since(startTime).Milliseconds()
		msg := fmt.Sprintf("invalid URL: %s", err)
		return false, nil, &latency, &msg, ErrorTypeUnknown
	}

	// Step 2: TCP Handshake (fail fast)
	host := parsedURL.Host
	if parsedURL.Port() == "" {
		// Add default port
		if parsedURL.Scheme == "https" {
			host = net.JoinHostPort(host, "443")
		} else {
			host = net.JoinHostPort(host, "80")
		}
	}

	// Perform TCP dial with timeout
	conn, err := net.DialTimeout("tcp", host, config.HTTPTimeout)
	if err != nil {
		latency := time.Since(startTime).Milliseconds()
		msg := sanitizeErrorMessage(fmt.Sprintf("TCP handshake failed: %s", err))

		// Classify error
		errType := classifyNetworkError(err)

		return false, nil, &latency, &msg, errType
	}
	conn.Close() // Close TCP connection (we'll use HTTP client)

	// Step 3: HTTP Request
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		latency := time.Since(startTime).Milliseconds()
		msg := fmt.Sprintf("failed to create HTTP request: %s", err)
		return false, nil, &latency, &msg, ErrorTypeUnknown
	}

	// Set User-Agent
	req.Header.Set("User-Agent", "alert-history-health-checker/1.0")

	// Perform HTTP request
	resp, err := httpClient.Do(req)
	if err != nil {
		latency := time.Since(startTime).Milliseconds()
		msg := sanitizeErrorMessage(fmt.Sprintf("HTTP request failed: %s", err))

		// Classify error
		errType := classifyHTTPError(err)

		return false, nil, &latency, &msg, errType
	}
	defer resp.Body.Close()

	// Measure latency
	latency := time.Since(startTime).Milliseconds()

	// Check HTTP status code
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		// Success: 2xx status
		return true, &resp.StatusCode, &latency, nil, ""
	}

	// Failure: Non-2xx status
	msg := sanitizeErrorMessage(fmt.Sprintf("HTTP %d: %s", resp.StatusCode, resp.Status))
	return false, &resp.StatusCode, &latency, &msg, ErrorTypeHTTP
}

// checkSingleTarget performs health check for single target.
//
// This function:
//   1. Validates target (skip if disabled or invalid URL)
//   2. Calls httpConnectivityTest
//   3. Builds HealthCheckResult
//   4. Returns result for processing
//
// Parameters:
//   - ctx: Context (for cancellation)
//   - target: Publishing target
//   - checkType: Check type (periodic/manual)
//   - httpClient: HTTP client
//   - config: Health configuration
//
// Returns:
//   - HealthCheckResult: Result of health check
//
// Performance: <500ms (includes HTTP request)
//
// Example:
//
//	result := checkSingleTarget(ctx, target, CheckTypePeriodic, httpClient, config)
//	if result.Success {
//	    log.Info("Target healthy", "latency_ms", *result.LatencyMs)
//	}
func checkSingleTarget(
	ctx context.Context,
	target *core.PublishingTarget,
	checkType CheckType,
	httpClient *http.Client,
	config HealthConfig,
) HealthCheckResult {
	startTime := time.Now()

	// Validate target URL
	if target.URL == "" {
		latency := time.Since(startTime).Milliseconds()
		msg := "target URL is empty"
		errType := ErrorTypeUnknown
		return HealthCheckResult{
			TargetName:   target.Name,
			TargetURL:    target.URL,
			Success:      false,
			LatencyMs:    &latency,
			StatusCode:   nil,
			ErrorMessage: &msg,
			ErrorType:    &errType,
			CheckedAt:    time.Now(),
			CheckType:    checkType,
		}
	}

	// Perform HTTP connectivity test
	success, statusCode, latencyMs, errorMsg, errorType := httpConnectivityTest(
		ctx,
		target.URL,
		httpClient,
		config,
	)

	// Build result
	result := HealthCheckResult{
		TargetName:   target.Name,
		TargetURL:    target.URL,
		Success:      success,
		LatencyMs:    latencyMs,
		StatusCode:   statusCode,
		ErrorMessage: errorMsg,
		CheckedAt:    time.Now(),
		CheckType:    checkType,
	}

	if !success && errorMsg != nil {
		result.ErrorType = &errorType
	}

	return result
}

// checkTargetWithRetry performs health check with retry on transient errors.
//
// This function:
//   1. Performs initial health check
//   2. If failure is transient (timeout/network), retries once
//   3. Returns final result
//
// Note: Only 1 retry to keep check duration <1s total.
//
// Parameters:
//   - ctx: Context (for cancellation)
//   - target: Publishing target
//   - checkType: Check type
//   - httpClient: HTTP client
//   - config: Health configuration
//
// Returns:
//   - HealthCheckResult: Final result (after retry if needed)
//
// Performance:
//   - Success: ~100-300ms (single attempt)
//   - Transient failure: ~200-600ms (1 retry)
//   - Permanent failure: ~100-300ms (no retry)
//
// Example:
//
//	result := checkTargetWithRetry(ctx, target, CheckTypePeriodic, httpClient, config)
func checkTargetWithRetry(
	ctx context.Context,
	target *core.PublishingTarget,
	checkType CheckType,
	httpClient *http.Client,
	config HealthConfig,
) HealthCheckResult {
	// First attempt
	result := checkSingleTarget(ctx, target, checkType, httpClient, config)

	// If success, return immediately
	if result.Success {
		return result
	}

	// If permanent error, don't retry
	if result.ErrorType != nil {
		errType := *result.ErrorType
		if errType == ErrorTypeHTTP || errType == ErrorTypeUnknown {
			// HTTP errors (4xx/5xx) and unknown errors are not retried
			return result
		}
	}

	// Transient error: Retry once after 100ms
	select {
	case <-time.After(100 * time.Millisecond):
		// Continue to retry
	case <-ctx.Done():
		// Context cancelled, return original result
		return result
	}

	// Second attempt (retry)
	retryResult := checkSingleTarget(ctx, target, checkType, httpClient, config)

	// Return retry result (success or final failure)
	return retryResult
}
