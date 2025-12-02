# Resilience Package

**Package**: `github.com/vitaliisemenov/alert-history/internal/core/resilience`

Production-ready resilience patterns for distributed systems in Go.

## Table of Contents

- [Overview](#overview)
- [Features](#features)
- [Installation](#installation)
- [Quick Start](#quick-start)
- [Retry Logic](#retry-logic)
  - [Basic Usage](#basic-usage)
  - [Advanced Configuration](#advanced-configuration)
  - [Error Classification](#error-classification)
  - [Metrics Integration](#metrics-integration)
- [API Reference](#api-reference)
- [Best Practices](#best-practices)
- [Performance](#performance)
- [Examples](#examples)

---

## Overview

The `resilience` package provides battle-tested reliability patterns for handling transient failures in distributed systems. It implements:

- **Retry Logic**: Exponential backoff with jitter
- **Error Classification**: Smart detection of retryable vs permanent errors
- **Metrics Integration**: Built-in Prometheus metrics tracking
- **Context Support**: Full support for cancellation and deadlines
- **Zero Allocations**: Optimized hot path with zero heap allocations

Designed for production use with **93.2% test coverage** and **sub-microsecond overhead**.

---

## Features

✅ **Exponential Backoff** - Configurable multiplier and max delay
✅ **Jitter** - Prevent thundering herd with random delays (10% jitter)
✅ **Context Cancellation** - Respect ctx.Done() immediately
✅ **Error Classification** - Network, timeout, rate limit, DNS errors
✅ **Prometheus Metrics** - Track attempts, durations, backoffs
✅ **Generic Support** - `WithRetryFunc[T]` for any return type
✅ **Structured Logging** - Built-in slog integration
✅ **Thread-Safe** - Safe for concurrent use
✅ **Production-Ready** - Used in Alert History Service for LLM calls

---

## Installation

```go
import "github.com/vitaliisemenov/alert-history/internal/core/resilience"
```

---

## Quick Start

### Simple Retry (Default Policy)

```go
ctx := context.Background()

err := resilience.WithRetry(ctx, nil, func() error {
    resp, err := http.Get("https://api.example.com/data")
    if err != nil {
        return err
    }
    defer resp.Body.Close()

    if resp.StatusCode != 200 {
        return fmt.Errorf("API error: %d", resp.StatusCode)
    }

    return nil
})

if err != nil {
    log.Fatal("Operation failed:", err)
}
```

**Default Policy**:
- MaxRetries: 3
- BaseDelay: 100ms
- MaxDelay: 5s
- Multiplier: 2.0 (exponential backoff)
- Jitter: true

---

## Retry Logic

### Basic Usage

#### WithRetry (for error-only operations)

```go
policy := &resilience.RetryPolicy{
    MaxRetries: 3,
    BaseDelay:  100 * time.Millisecond,
    MaxDelay:   5 * time.Second,
    Multiplier: 2.0,
    Jitter:     true,
}

err := resilience.WithRetry(ctx, policy, func() error {
    return doSomething()
})
```

#### WithRetryFunc[T] (for operations that return a result)

```go
data, err := resilience.WithRetryFunc(ctx, policy, func() ([]byte, error) {
    resp, err := http.Get("https://api.example.com/data")
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()
    return io.ReadAll(resp.Body)
})
```

---

### Advanced Configuration

#### Custom Error Checker

```go
type MyErrorChecker struct{}

func (c *MyErrorChecker) IsRetryable(err error) bool {
    // Custom logic to determine if error is retryable
    if errors.Is(err, ErrRateLimited) {
        return true
    }
    if errors.Is(err, ErrUnauthorized) {
        return false // Don't retry auth errors
    }
    return true
}

policy := &resilience.RetryPolicy{
    MaxRetries:   5,
    BaseDelay:    200 * time.Millisecond,
    MaxDelay:     10 * time.Second,
    Multiplier:   2.0,
    Jitter:       true,
    ErrorChecker: &MyErrorChecker{},
    Logger:       slog.Default(),
}
```

#### HTTP Error Checker (Built-in)

Retries on 5xx, 429, 408 status codes:

```go
httpChecker := resilience.NewHTTPErrorChecker()
httpChecker.RetryOn5xx = true  // Server errors
httpChecker.RetryOn429 = true  // Rate limits
httpChecker.RetryOn408 = true  // Request timeouts

policy := &resilience.RetryPolicy{
    MaxRetries:   3,
    BaseDelay:    500 * time.Millisecond,
    MaxDelay:     5 * time.Second,
    Multiplier:   2.0,
    Jitter:       true,
    ErrorChecker: httpChecker,
}
```

#### Chained Error Checkers

Retry if ANY checker says yes:

```go
chainedChecker := &resilience.ChainedErrorChecker{
    Checkers: []resilience.RetryableErrorChecker{
        resilience.NewHTTPErrorChecker(),
        &resilience.DefaultErrorChecker{},
        &MyCustomChecker{},
    },
}

policy.ErrorChecker = chainedChecker
```

---

### Error Classification

The package automatically classifies errors for metrics labeling:

| Error Type | Description | Examples |
|------------|-------------|----------|
| `timeout` | Timeout or deadline exceeded | "i/o timeout", "context deadline exceeded" |
| `network` | Network connectivity errors | ECONNREFUSED, ECONNRESET, ENETUNREACH |
| `rate_limit` | Rate limiting errors | "429 Too Many Requests", "rate limit exceeded" |
| `dns` | DNS resolution errors | DNS lookup failures |
| `context_cancelled` | Context cancellation | context.Canceled |
| `context_deadline` | Context deadline | context.DeadlineExceeded |
| `unknown` | All other errors | Generic errors |

---

### Metrics Integration

Integrate with Prometheus metrics:

```go
import "github.com/vitaliisemenov/alert-history/pkg/metrics"

policy := &resilience.RetryPolicy{
    MaxRetries:    3,
    BaseDelay:     100 * time.Millisecond,
    MaxDelay:      5 * time.Second,
    Multiplier:    2.0,
    Jitter:        true,
    Metrics:       metrics.DefaultRegistry().Technical().Retry,
    OperationName: "my_operation",
}

err := resilience.WithRetry(ctx, policy, func() error {
    return callExternalAPI()
})
```

**Metrics collected**:

```promql
# Total retry attempts by operation, outcome, error type
alert_history_technical_retry_attempts_total{operation="my_operation",outcome="success",error_type="timeout"}

# Duration of retry operations (p50, p95, p99)
alert_history_technical_retry_duration_seconds{operation="my_operation",outcome="success"}

# Backoff delays between retries
alert_history_technical_retry_backoff_seconds{operation="my_operation"}

# Number of attempts until completion
alert_history_technical_retry_final_attempts_total{operation="my_operation",outcome="success"}
```

---

## API Reference

### Types

#### `RetryPolicy`

```go
type RetryPolicy struct {
    MaxRetries    int                     // Maximum retry attempts (0 = no retries)
    BaseDelay     time.Duration           // Initial delay before first retry
    MaxDelay      time.Duration           // Maximum delay between retries
    Multiplier    float64                 // Exponential backoff multiplier (1.5-3.0)
    Jitter        bool                    // Add 10% random jitter
    ErrorChecker  RetryableErrorChecker   // Custom error classification
    Logger        *slog.Logger            // Structured logger
    Metrics       *metrics.RetryMetrics   // Prometheus metrics
    OperationName string                  // Name for metrics labels
}
```

#### `RetryableErrorChecker`

```go
type RetryableErrorChecker interface {
    IsRetryable(err error) bool
}
```

**Built-in Implementations**:
- `DefaultErrorChecker` - Network, timeout, temporary errors
- `HTTPErrorChecker` - HTTP 5xx, 429, 408 errors
- `ChainedErrorChecker` - Combines multiple checkers (OR logic)
- `NeverRetryChecker` - Never retry (useful for testing)
- `AlwaysRetryChecker` - Always retry (dangerous!)

---

### Functions

#### `WithRetry`

```go
func WithRetry(ctx context.Context, policy *RetryPolicy, operation func() error) error
```

Retries an error-only operation according to the policy.

**Parameters**:
- `ctx`: Context for cancellation/deadline
- `policy`: Retry configuration (nil = default policy)
- `operation`: Function to execute

**Returns**: Last error or nil on success

---

#### `WithRetryFunc[T]`

```go
func WithRetryFunc[T any](ctx context.Context, policy *RetryPolicy, operation func() (T, error)) (T, error)
```

Retries an operation that returns a result.

**Parameters**:
- `ctx`: Context for cancellation/deadline
- `policy`: Retry configuration (nil = default policy)
- `operation`: Function to execute

**Returns**: Result and error

---

#### `DefaultRetryPolicy`

```go
func DefaultRetryPolicy() *RetryPolicy
```

Returns a sensible default policy:
- MaxRetries: 3
- BaseDelay: 100ms
- MaxDelay: 5s
- Multiplier: 2.0
- Jitter: true

---

## Best Practices

### 1. Always Use Context

```go
// ✅ GOOD
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()
err := resilience.WithRetry(ctx, policy, operation)
```

```go
// ❌ BAD - No timeout, could run forever
err := resilience.WithRetry(context.Background(), policy, operation)
```

---

### 2. Choose Appropriate Retry Counts

```go
// ✅ GOOD - User-facing operations (3 retries)
policy := &resilience.RetryPolicy{
    MaxRetries: 3,
    BaseDelay:  100 * time.Millisecond,
    MaxDelay:   5 * time.Second,
}
```

```go
// ✅ GOOD - Background jobs (10 retries)
policy := &resilience.RetryPolicy{
    MaxRetries: 10,
    BaseDelay:  1 * time.Second,
    MaxDelay:   1 * time.Minute,
}
```

```go
// ❌ BAD - Too many retries for user-facing operation
policy := &resilience.RetryPolicy{
    MaxRetries: 100, // User will wait too long!
    BaseDelay:  1 * time.Second,
}
```

---

### 3. Use Jitter to Prevent Thundering Herd

```go
// ✅ GOOD - Jitter enabled (default)
policy := &resilience.RetryPolicy{
    Jitter: true, // Adds 10% random jitter
}
```

```go
// ❌ BAD - All clients retry at same time
policy := &resilience.RetryPolicy{
    Jitter: false,
}
```

---

### 4. Add Metrics for Production Monitoring

```go
// ✅ GOOD - Track retry metrics
policy := &resilience.RetryPolicy{
    Metrics:       metrics.DefaultRegistry().Technical().Retry,
    OperationName: "external_api_call",
}
```

```go
// ⚠️ ACCEPTABLE - For non-critical operations
policy := &resilience.RetryPolicy{
    Metrics: nil, // No metrics tracked
}
```

---

### 5. Use Smart Error Classification

```go
// ✅ GOOD - Don't retry permanent errors
type SmartErrorChecker struct{}

func (c *SmartErrorChecker) IsRetryable(err error) bool {
    // Don't retry validation errors
    if errors.Is(err, ErrInvalidInput) {
        return false
    }
    // Don't retry auth errors
    if errors.Is(err, ErrUnauthorized) {
        return false
    }
    // Retry everything else
    return true
}
```

---

## Performance

### Benchmarks

```bash
$ go test ./internal/core/resilience/... -bench=. -benchmem

BenchmarkWithRetry_NoRetries-8           361083565    3.22 ns/op    0 B/op    0 allocs/op
BenchmarkWithRetryFunc_NoRetries-8       375136572    3.22 ns/op    0 B/op    0 allocs/op
BenchmarkCalculateNextDelay-8            162065378    7.44 ns/op    0 B/op    0 allocs/op
BenchmarkDefaultErrorChecker-8             6565705  182.0 ns/op   16 B/op    2 allocs/op
BenchmarkHTTPErrorChecker-8               19990503   60.6 ns/op   16 B/op    2 allocs/op
```

**Key Metrics**:
- **3.22 ns/op** overhead when operation succeeds immediately
- **Zero allocations** in hot path (no retries)
- **60 ns/op** for HTTP error classification
- **31,000x faster** than target (<100µs overhead goal)

---

## Examples

### Example 1: HTTP API Call with Retries

```go
func fetchUserData(ctx context.Context, userID string) (*User, error) {
    policy := &resilience.RetryPolicy{
        MaxRetries: 3,
        BaseDelay:  100 * time.Millisecond,
        MaxDelay:   2 * time.Second,
        Multiplier: 2.0,
        Jitter:     true,
        ErrorChecker: resilience.NewHTTPErrorChecker(),
        Metrics:      metrics.DefaultRegistry().Technical().Retry,
        OperationName: "fetch_user_data",
    }

    user, err := resilience.WithRetryFunc(ctx, policy, func() (*User, error) {
        url := fmt.Sprintf("https://api.example.com/users/%s", userID)
        req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)

        resp, err := httpClient.Do(req)
        if err != nil {
            return nil, err
        }
        defer resp.Body.Close()

        if resp.StatusCode != 200 {
            return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
        }

        var user User
        if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
            return nil, err
        }

        return &user, nil
    })

    return user, err
}
```

---

### Example 2: Database Query with Custom Error Handling

```go
type DBErrorChecker struct{}

func (c *DBErrorChecker) IsRetryable(err error) bool {
    // Retry on connection errors
    if errors.Is(err, sql.ErrConnDone) {
        return true
    }
    // Retry on deadlocks
    if strings.Contains(err.Error(), "deadlock") {
        return true
    }
    // Don't retry constraint violations
    if strings.Contains(err.Error(), "constraint") {
        return false
    }
    return true
}

func getUserByEmail(ctx context.Context, db *sql.DB, email string) (*User, error) {
    policy := &resilience.RetryPolicy{
        MaxRetries:   5,
        BaseDelay:    50 * time.Millisecond,
        MaxDelay:     500 * time.Millisecond,
        Multiplier:   2.0,
        Jitter:       true,
        ErrorChecker: &DBErrorChecker{},
        OperationName: "db_get_user_by_email",
        Metrics:      metrics.DefaultRegistry().Technical().Retry,
    }

    user, err := resilience.WithRetryFunc(ctx, policy, func() (*User, error) {
        var u User
        err := db.QueryRowContext(ctx,
            "SELECT id, name, email FROM users WHERE email = $1", email,
        ).Scan(&u.ID, &u.Name, &u.Email)

        if err != nil {
            return nil, err
        }
        return &u, nil
    })

    return user, err
}
```

---

### Example 3: LLM API Call (Real Production Code)

```go
// From internal/infrastructure/llm/client.go

func (c *HTTPLLMClient) classifyAlertWithRetry(ctx context.Context, alert *core.Alert) (*core.ClassificationResult, error) {
    policy := &resilience.RetryPolicy{
        MaxRetries:    c.config.MaxRetries,    // 3
        BaseDelay:     c.config.RetryDelay,    // 1s
        MaxDelay:      c.config.RetryDelay * 10, // 10s
        Multiplier:    c.config.RetryBackoff,  // 2.0
        Jitter:        true,
        ErrorChecker:  &llmErrorChecker{},
        Logger:        c.logger,
        Metrics:       metrics.DefaultRegistry().Technical().Retry,
        OperationName: "llm_classify_alert",
    }

    result, err := resilience.WithRetryFunc(ctx, policy, func() (*core.ClassificationResult, error) {
        return c.classifyAlertOnce(ctx, alert)
    })

    if err != nil {
        return nil, err
    }

    return result, nil
}
```

---

### Example 4: Combining with Circuit Breaker

```go
func callServiceWithResiliencePatterns(ctx context.Context, cb *resilience.CircuitBreaker) (*Response, error) {
    // Circuit breaker prevents calls when service is down
    if cb.GetState() == resilience.StateOpen {
        return nil, resilience.ErrCircuitBreakerOpen
    }

    // Retry logic handles transient failures
    policy := &resilience.RetryPolicy{
        MaxRetries: 3,
        BaseDelay:  100 * time.Millisecond,
        Multiplier: 2.0,
    }

    result, err := resilience.WithRetryFunc(ctx, policy, func() (*Response, error) {
        // Call protected by circuit breaker
        return cb.Call(ctx, func(ctx context.Context) (*Response, error) {
            return externalService.Fetch(ctx)
        })
    })

    return result, err
}
```

---

## Testing

### Test Coverage

```bash
$ go test ./internal/core/resilience/... -cover

ok   github.com/vitaliisemenov/alert-history/internal/core/resilience   0.821s   coverage: 93.2% of statements
```

**55 tests** covering:
- ✅ Retry success/failure scenarios
- ✅ Context cancellation
- ✅ Exponential backoff calculation
- ✅ Jitter application
- ✅ Error classification (network, timeout, HTTP, DNS)
- ✅ All error checker implementations
- ✅ Edge cases and wrapped errors

---

## License

Part of Alert History Service
Copyright © 2025

---

## Related Packages

- `internal/core/processing` - Async webhook processing
- `internal/infrastructure/llm` - LLM client with Circuit Breaker
- `pkg/metrics` - Prometheus metrics integration

---

**Questions?** See [CONTRIBUTING-GO.md](../../../CONTRIBUTING-GO.md)
