# Alert History - Extension Examples

This directory contains examples of how to extend Alert History Service with custom implementations.

## 📚 Available Examples

### 1. Custom Classifier (`custom-classifier/`)
**Example**: ML-based alert classifier

Shows how to:
- Implement `interfaces.AlertClassifier` interface
- Extract features from alerts
- Call ML model for predictions
- Generate recommendations
- Track metrics and performance

**Use Cases**:
- Machine learning-based classification
- External API integration
- Custom rule engines
- Multi-model ensembles

[View Example →](./custom-classifier/main.go)

---

### 2. Custom Publisher (`custom-publisher/`)
**Example**: Microsoft Teams publisher

Shows how to:
- Implement `interfaces.AlertPublisher` interface
- Format alerts for target systems
- Handle HTTP requests with retries
- Create rich formatted messages (Adaptive Cards)
- Implement health checks

**Use Cases**:
- MS Teams integration
- Discord integration
- Jira ticket creation
- ServiceNow incidents
- Custom internal systems

[View Example →](./custom-publisher/main.go)

---

## 🚀 Quick Start

### Running an Example

```bash
# Navigate to example directory
cd examples/custom-classifier

# Run the example
go run main.go
```

### Integrating into Alert History

1. **Implement the interface** (see examples above)

2. **Register your implementation**:
```go
// In main.go or plugin loader
classifier := NewMLClassifier("my-model", "1.0.0")
registry.Register("ml-classifier", classifier)
```

3. **Configure via config file**:
```yaml
# config.yml
classification:
  default_classifier: ml-classifier

publishing:
  targets:
    - name: ops-team
      type: ms-teams
      webhook_url: https://...
```

4. **Deploy and monitor**:
```bash
# Build with your custom extension
go build -o alert-history ./cmd/server

# Run
./alert-history --config config.yml
```

---

## 🎯 Extension Points

Alert History provides clean extension points for:

### 1. Alert Classification
**Interface**: `interfaces.AlertClassifier`

**Methods**:
- `Classify(ctx, alert) -> ClassificationResult`
- `ClassifyBatch(ctx, alerts) -> []ClassificationResult`
- `Health(ctx) -> error`

**Built-in Implementations**:
- Rule-based classifier (OSS, free)
- LLM classifier (optional, BYOK)

**Custom Implementations** (examples):
- Machine learning models
- External API classifiers
- Hybrid classifiers

---

### 2. Alert Publishing
**Interface**: `interfaces.AlertPublisher`

**Methods**:
- `Publish(ctx, alert, target) -> error`
- `Health(ctx) -> error`
- `Shutdown(ctx) -> error`

**Built-in Implementations** (OSS):
- Slack
- PagerDuty
- Email (SMTP)
- Generic Webhook

**Custom Implementations** (examples):
- Microsoft Teams
- Discord
- Jira
- ServiceNow
- Opsgenie
- Datadog

---

### 3. Storage Backends
**Interface**: `interfaces.StorageBackend`

**Built-in Implementations** (OSS):
- PostgreSQL
- SQLite (for development)

**Custom Implementations** (possible):
- TimescaleDB (time-series optimization)
- ClickHouse (analytics queries)
- MongoDB (document store)
- Elasticsearch (full-text search)

---

### 4. Caching
**Interface**: `interfaces.CacheBackend`

**Built-in Implementations** (OSS):
- Redis (distributed)
- In-memory (local)

**Custom Implementations** (possible):
- Memcached
- Hazelcast
- Custom caching logic

---

### 5. Enrichment
**Interface**: `interfaces.AlertEnricher`

**Custom Implementations** (examples):
- Add runbook URLs from internal wiki
- Add on-call information
- Add cost estimates
- Add historical context

---

## 📖 Best Practices

### 1. Error Handling
```go
func (p *MyPublisher) Publish(ctx context.Context, alert *domain.EnrichedAlert, target *Target) error {
    // Always handle errors gracefully
    if err := p.validate(alert, target); err != nil {
        return fmt.Errorf("validation failed: %w", err)
    }

    // Use context for timeouts
    ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
    defer cancel()

    // Implement retries for transient failures
    return p.publishWithRetry(ctx, alert, target, 3)
}
```

### 2. Metrics & Observability
```go
func (c *MyClassifier) Classify(ctx context.Context, alert *domain.Alert) (*domain.ClassificationResult, error) {
    start := time.Now()
    defer func() {
        // Track latency
        metrics.ObserveLatency("classify", time.Since(start))
    }()

    result, err := c.doClassify(ctx, alert)
    if err != nil {
        metrics.IncrementErrors("classify")
        return nil, err
    }

    metrics.IncrementSuccess("classify")
    return result, nil
}
```

### 3. Health Checks
```go
func (p *MyPublisher) Health(ctx context.Context) error {
    // Check external dependencies
    if err := p.checkWebhookReachable(ctx); err != nil {
        return fmt.Errorf("webhook unreachable: %w", err)
    }

    // Check configuration
    if !p.isConfigured() {
        return fmt.Errorf("publisher not configured")
    }

    return nil
}
```

### 4. Graceful Shutdown
```go
func (p *MyPublisher) Shutdown(ctx context.Context) error {
    // Flush pending messages
    p.flushQueue(ctx)

    // Close connections
    p.httpClient.CloseIdleConnections()

    // Wait for in-flight requests
    p.wg.Wait()

    return nil
}
```

---

## 🧪 Testing Your Extensions

### Unit Tests
```go
func TestMyClassifier_Classify(t *testing.T) {
    classifier := NewMyClassifier()

    alert := &domain.Alert{
        AlertName: "HighCPU",
        // ... setup alert
    }

    result, err := classifier.Classify(context.Background(), alert)
    assert.NoError(t, err)
    assert.Equal(t, domain.SeverityCritical, result.Severity)
    assert.Greater(t, result.Confidence, 0.8)
}
```

### Integration Tests
```go
func TestMyPublisher_Integration(t *testing.T) {
    // Setup test server
    server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(http.StatusOK)
    }))
    defer server.Close()

    // Test publishing
    publisher := NewMyPublisher()
    target := &Target{WebhookURL: server.URL}

    err := publisher.Publish(context.Background(), testAlert, target)
    assert.NoError(t, err)
}
```

---

## 🤝 Contributing

Have a great extension example? Please contribute!

1. Create your example in a new directory
2. Add comprehensive comments
3. Include usage instructions
4. Submit a pull request

---

## 📚 Resources

- [pkg/core Interfaces](../pkg/core/interfaces/) - Core interface definitions
- [pkg/core Domain](../pkg/core/domain/) - Domain models
- [Architecture Decision Records](../docs/adrs/) - Design decisions
- [API Documentation](../docs/) - API specs

---

## 🆘 Support

- **Issues**: [GitHub Issues](https://github.com/ipiton/alert-history-service/issues)
- **Discussions**: [GitHub Discussions](https://github.com/ipiton/alert-history-service/discussions)
- **Docs**: [Full Documentation](../docs/)

---

**Last Updated**: 2025-12-01
**Version**: v1.0.0
**License**: Apache 2.0
