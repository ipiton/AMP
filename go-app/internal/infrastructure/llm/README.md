# LLM Client - BYOK (Bring Your Own Key)

**Status:** ✅ Production-Ready
**Type:** Optional OSS Feature
**License:** Apache 2.0

---

## 📚 Overview

LLM-based alert classification using **YOUR OWN API keys** (BYOK).

### Supported Providers:
- ✅ **OpenAI** (GPT-4, GPT-3.5)
- ✅ **Anthropic** (Claude 3)
- ✅ **Azure OpenAI**
- ✅ **Custom LLM Proxy**

### Key Features:
- 🔐 **BYOK** - You control your API keys
- 🛡️ **Circuit Breaker** - Fail-fast when LLM unavailable
- 🔄 **Retry Logic** - Exponential backoff for transient failures
- 📊 **Prometheus Metrics** - Full observability
- ⚡ **Performance** - Sub-millisecond overhead
- 🎯 **Fallback** - Graceful degradation to rule-based

---

## 🚀 Quick Start

### 1. OpenAI Configuration

```yaml
# config.yaml
llm:
  enabled: true
  base_url: "https://api.openai.com/v1/chat/completions"
  api_key: "sk-YOUR-OPENAI-API-KEY"  # Your own key
  model: "gpt-4o"
  timeout: 30s
  max_retries: 3
```

### 2. Anthropic Configuration

```yaml
# config.yaml
llm:
  enabled: true
  base_url: "https://api.anthropic.com/v1/messages"
  api_key: "sk-ant-YOUR-ANTHROPIC-KEY"  # Your own key
  model: "claude-3-opus-20240229"
  timeout: 30s
```

### 3. Azure OpenAI Configuration

```yaml
# config.yaml
llm:
  enabled: true
  base_url: "https://YOUR-RESOURCE.openai.azure.com/openai/deployments/YOUR-DEPLOYMENT/chat/completions?api-version=2024-02-15-preview"
  api_key: "YOUR-AZURE-API-KEY"  # Your Azure key
  model: "gpt-4"
  timeout: 30s
```

### 4. Custom Proxy Configuration

```yaml
# config.yaml
llm:
  enabled: true
  base_url: "https://your-custom-proxy.example.com/classify"
  api_key: "your-custom-api-key"
  model: "your-model"
  timeout: 30s
```

---

## 💻 Usage Example

```go
package main

import (
    "context"
    "log/slog"

    "github.com/ipiton/AMP/go-app/internal/infrastructure/llm"
    "github.com/ipiton/AMP/go-app/pkg/core/domain"
)

func main() {
    // Configure LLM client
    config := llm.Config{
        BaseURL:    "https://api.openai.com/v1/chat/completions",
        APIKey:     "sk-YOUR-KEY",  // Your own OpenAI key
        Model:      "gpt-4o",
        Timeout:    30 * time.Second,
        MaxRetries: 3,
        CircuitBreaker: llm.CircuitBreakerConfig{
            Enabled:           true,
            FailureThreshold:  5,
            SuccessThreshold:  2,
            Timeout:           60 * time.Second,
        },
    }

    // Create client
    client := llm.NewHTTPLLMClient(config, slog.Default())

    // Classify alert
    alert := &domain.Alert{
        Labels: map[string]string{
            "alertname": "HighCPU",
            "severity":  "critical",
        },
        Annotations: map[string]string{
            "summary": "CPU usage above 90%",
        },
    }

    result, err := client.ClassifyAlert(context.Background(), alert)
    if err != nil {
        log.Printf("Classification failed: %v", err)
        // Fallback to rule-based classifier
            return
    }

    log.Printf("Classification: %+v", result)
}
```

---

## 🛡️ Circuit Breaker

Protects your application when LLM API is down.

### How It Works:

```
CLOSED (Normal Operation)
    ↓ (5 consecutive failures)
OPEN (Fail-fast, block all requests)
    ↓ (60s timeout)
HALF_OPEN (Test with single request)
    ↓ (2 consecutive successes)
CLOSED (Recovery complete)
```

### Configuration:

```go
CircuitBreaker: llm.CircuitBreakerConfig{
    Enabled:           true,              // Enable circuit breaker
    FailureThreshold:  5,                 // Open after 5 failures
    SuccessThreshold:  2,                 // Close after 2 successes
    Timeout:           60 * time.Second,  // OPEN → HALF_OPEN delay
    HalfOpenMaxCalls:  1,                 // Test requests in HALF_OPEN
}
```

---

## 📊 Prometheus Metrics

### Available Metrics:

```prometheus
# Request metrics
llm_client_requests_total{status="success|error|circuit_open"}
llm_client_request_duration_seconds{quantile="0.5|0.95|0.99"}
llm_client_errors_total{error_type="timeout|transient|prolonged"}

# Circuit breaker metrics
llm_circuit_breaker_state{state="closed|open|half_open"}
llm_circuit_breaker_failures_total
llm_circuit_breaker_successes_total
llm_circuit_breaker_transitions_total{from="*",to="*"}
```

### Example Queries:

```promql
# Success rate
rate(llm_client_requests_total{status="success"}[5m])
  / rate(llm_client_requests_total[5m])

# P95 latency
histogram_quantile(0.95,
  rate(llm_client_request_duration_seconds_bucket[5m]))

# Circuit breaker is open
llm_circuit_breaker_state{state="open"} == 1
```

---

## 🎯 Error Handling

### Error Types:

1. **Transient** - Temporary failures (retry)
   - Network timeouts
   - 429 Rate Limit
   - 503 Service Unavailable

2. **Prolonged** - Persistent failures (circuit breaker)
   - Connection refused
   - DNS failures
   - 5xx errors (repeated)

3. **Permanent** - Cannot retry
   - 401 Unauthorized (bad API key)
   - 400 Bad Request (invalid input)

### Example:

```go
result, err := client.ClassifyAlert(ctx, alert)
if err != nil {
    var llmErr *llm.LLMError
    if errors.As(err, &llmErr) {
        switch llmErr.Type {
        case llm.ErrorTypeTransient:
            // Will retry automatically
        case llm.ErrorTypeProlonged:
            // Circuit breaker will open
        case llm.ErrorTypePermanent:
            // Check your API key/config
        }
    }

    // Fallback to rule-based classifier
    return fallbackClassifier.Classify(ctx, alert)
}
```

---

## 🎛️ Configuration Reference

```go
type Config struct {
    // API endpoint (REQUIRED - your own endpoint)
    BaseURL string

    // API key (REQUIRED - your own key)
    APIKey string

    // Model name (e.g., "gpt-4o", "claude-3-opus")
    Model string

    // Request timeout
    Timeout time.Duration

    // Retry configuration
    MaxRetries   int
    RetryDelay   time.Duration
    RetryBackoff float64  // 2.0 = exponential

    // Circuit breaker (optional)
    CircuitBreaker CircuitBreakerConfig

    // Metrics (optional)
    EnableMetrics bool
}
```

---

## 💰 Cost Tracking

```go
result, err := client.ClassifyAlert(ctx, alert)
if err == nil {
    log.Printf("Tokens used: %d", result.TokensUsed)
    log.Printf("Cost: $%.6f", result.CostUSD)
}
```

---

## 🔒 Security

### Best Practices:

1. **Never hardcode API keys** - Use environment variables
2. **Rotate keys regularly** - Follow your provider's recommendations
3. **Monitor usage** - Set budget alerts
4. **Limit rate** - Use circuit breaker to prevent runaway costs

### Environment Variables:

```bash
# Recommended approach
export LLM_BASE_URL="https://api.openai.com/v1/chat/completions"
export LLM_API_KEY="sk-your-key-here"

# Or use Kubernetes Secrets
kubectl create secret generic llm-credentials \
  --from-literal=api-key="sk-your-key"
```

---

## 🧪 Testing

### Mock Client:

```go
// For testing without real API calls
mockClient := llm.NewMockLLMClient()
mockClient.SetResponse(&core.ClassificationResult{
    Severity:   "critical",
    Confidence: 0.95,
})

result, err := mockClient.ClassifyAlert(ctx, alert)
// No API call made, instant response
```

---

## 📈 Performance

### Benchmarks:

```
Operation                    Time        Allocations
---------------------------------------------------
Circuit Breaker Check       17.35 ns     0 allocs
Request (cache hit)         ~50 ns       0 allocs
Request (LLM API)           ~500 ms      8 allocs
Retry Logic                 3.22 ns      0 allocs
```

### Optimization Tips:

1. **Enable Circuit Breaker** - Fail-fast when LLM down
2. **Use Caching** - Cache classification results
3. **Batch Requests** - Group alerts when possible
4. **Set Reasonable Timeouts** - Don't wait forever

---

## 🆚 Comparison with Rule-Based

| Feature | Rule-Based (Free) | LLM (BYOK) |
|---------|------------------|------------|
| **Cost** | Free | Pay per API call |
| **Latency** | <1ms | ~500ms |
| **Accuracy** | Good (80-85%) | Better (90-95%) |
| **Setup** | Zero config | API key required |
| **Offline** | ✅ Yes | ❌ No |
| **Reasoning** | Rule matching | AI reasoning |

**Recommendation:** Start with rule-based, add LLM when needed.

---

## 🐛 Troubleshooting

### "401 Unauthorized"
- Check your API key
- Verify key hasn't expired
- Ensure correct provider URL

### "Circuit breaker is open"
- LLM API is down/unreachable
- Check circuit breaker metrics
- Wait for automatic recovery (60s)

### "Timeout"
- Increase `Timeout` config
- Check network connectivity
- Try different LLM provider

### High Costs
- Enable caching
- Reduce classification frequency
- Use cheaper model (gpt-3.5)

---

## 📚 References

### Provider Documentation:
- **OpenAI:** https://platform.openai.com/docs/api-reference
- **Anthropic:** https://docs.anthropic.com/claude/reference
- **Azure OpenAI:** https://learn.microsoft.com/en-us/azure/ai-services/openai/

### Pattern Documentation:
- **Circuit Breaker:** https://martinfowler.com/bliki/CircuitBreaker.html
- **Retry Pattern:** https://docs.microsoft.com/en-us/azure/architecture/patterns/retry

---

## 📄 License

Apache 2.0 - See LICENSE file

---

## 🤝 Contributing

See CONTRIBUTING.md in repository root

---

**🎊 BYOK = You Control Your Data & Costs!** 🎊
