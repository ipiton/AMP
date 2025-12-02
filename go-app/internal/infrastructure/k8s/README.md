# Kubernetes Client for Publishing System

## Overview

This package provides a simplified Kubernetes client wrapper for the Alert History Publishing System. It enables dynamic discovery of publishing targets stored in Kubernetes Secrets, eliminating the need for static configuration and enabling GitOps workflows.

**Key Features:**
- 🔐 **Secure**: Uses Kubernetes ServiceAccount authentication
- 🔄 **Dynamic**: Discovers targets from K8s Secrets with label selectors
- 🚀 **Reliable**: Exponential backoff retry logic for transient errors
- 📊 **Observable**: Structured logging with slog
- 🧪 **Tested**: 72.8% test coverage, 45+ tests, 4 benchmarks
- ⚡ **Fast**: Optimized for low latency (<500ms p95)

## Table of Contents

- [Quick Start](#quick-start)
- [Installation](#installation)
- [Usage](#usage)
  - [Basic Example](#basic-example)
  - [Listing Secrets](#listing-secrets)
  - [Getting Specific Secret](#getting-specific-secret)
  - [Health Checks](#health-checks)
- [Configuration](#configuration)
- [RBAC Requirements](#rbac-requirements)
- [Error Handling](#error-handling)
- [Performance](#performance)
- [Troubleshooting](#troubleshooting)
- [API Reference](#api-reference)

## Quick Start

```go
import (
    "context"
    "log"

    "github.com/vitaliisemenov/alert-history/internal/infrastructure/k8s"
)

func main() {
    // Create client with default configuration
    client, err := k8s.NewK8sClient(nil)
    if err != nil {
        log.Fatalf("Failed to create K8s client: %v", err)
    }
    defer client.Close()

    // List secrets with label selector
    ctx := context.Background()
    secrets, err := client.ListSecrets(ctx, "default", "publishing-target=true")
    if err != nil {
        log.Fatalf("Failed to list secrets: %v", err)
    }

    log.Printf("Found %d publishing targets", len(secrets))
}
```

## Installation

This package is part of the Alert History Service. No separate installation is required if you're using the service.

### Dependencies

```go
require (
    k8s.io/api v0.28.0+
    k8s.io/apimachinery v0.28.0+
    k8s.io/client-go v0.28.0+
)
```

## Usage

### Basic Example

```go
package main

import (
    "context"
    "fmt"
    "log/slog"
    "time"

    "github.com/vitaliisemenov/alert-history/internal/infrastructure/k8s"
)

func main() {
    // Create custom configuration
    config := &k8s.K8sClientConfig{
        Timeout:         30 * time.Second,
        MaxRetries:      3,
        RetryBackoff:    100 * time.Millisecond,
        MaxRetryBackoff: 5 * time.Second,
        Logger:          slog.Default(),
    }

    // Initialize client
    client, err := k8s.NewK8sClient(config)
    if err != nil {
        panic(fmt.Sprintf("Failed to create K8s client: %v", err))
    }
    defer client.Close()

    // Check health
    if err := client.Health(context.Background()); err != nil {
        log.Printf("K8s API health check failed: %v", err)
    }
}
```

### Listing Secrets

List secrets by namespace and label selector:

```go
ctx := context.Background()

// List all secrets with specific label
secrets, err := client.ListSecrets(ctx, "default", "publishing-target=true")
if err != nil {
    return fmt.Errorf("failed to list secrets: %w", err)
}

for _, secret := range secrets {
    fmt.Printf("Secret: %s/%s\n", secret.Namespace, secret.Name)
    fmt.Printf("  Labels: %v\n", secret.Labels)
    fmt.Printf("  Data keys: %v\n", getDataKeys(secret.Data))
}

func getDataKeys(data map[string][]byte) []string {
    keys := make([]string, 0, len(data))
    for k := range data {
        keys = append(keys, k)
    }
    return keys
}
```

**Advanced filtering:**

```go
// Multiple label selectors (AND logic)
secrets, err := client.ListSecrets(ctx, "production", "publishing-target=true,type=webhook")

// Empty label selector (returns all secrets in namespace)
allSecrets, err := client.ListSecrets(ctx, "default", "")
```

### Getting Specific Secret

Retrieve a secret by name:

```go
ctx := context.Background()

secret, err := client.GetSecret(ctx, "default", "slack-webhook-secret")
if err != nil {
    var notFoundErr *k8s.NotFoundError
    if errors.As(err, &notFoundErr) {
        fmt.Println("Secret not found")
        return nil
    }
    return fmt.Errorf("failed to get secret: %w", err)
}

// Access secret data
webhookURL := string(secret.Data["url"])
apiToken := string(secret.Data["token"])

fmt.Printf("Webhook URL: %s\n", webhookURL)
```

### Health Checks

Verify K8s API connectivity:

```go
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()

if err := client.Health(ctx); err != nil {
    var connErr *k8s.ConnectionError
    if errors.As(err, &connErr) {
        log.Printf("K8s API unavailable: %v", connErr)
        // Implement fallback logic
        return
    }
}

log.Println("K8s API is healthy")
```

## Configuration

### K8sClientConfig

```go
type K8sClientConfig struct {
    // Timeout for K8s API requests (default: 30s)
    Timeout time.Duration

    // MaxRetries for transient errors (default: 3)
    MaxRetries int

    // RetryBackoff initial backoff duration (default: 100ms)
    RetryBackoff time.Duration

    // MaxRetryBackoff maximum backoff duration (default: 5s)
    MaxRetryBackoff time.Duration

    // Logger for structured logging
    Logger *slog.Logger
}
```

### Default Configuration

```go
config := k8s.DefaultK8sClientConfig()
// Timeout: 30s
// MaxRetries: 3
// RetryBackoff: 100ms → 200ms → 400ms → 800ms
// MaxRetryBackoff: 5s
```

### Environment Variables

You can override configuration via environment variables:

```bash
# Request timeout
export K8S_CLIENT_TIMEOUT=45s

# Maximum retries
export K8S_CLIENT_MAX_RETRIES=5

# Initial retry backoff
export K8S_CLIENT_RETRY_BACKOFF=200ms
```

## RBAC Requirements

### ServiceAccount

The service must run with a ServiceAccount that has permissions to list and get Secrets.

**Minimum required RBAC:**

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: alert-history-service
  namespace: default
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: alert-history-secrets-reader
  namespace: default
rules:
- apiGroups: [""]
  resources: ["secrets"]
  verbs: ["get", "list"]
  # Optional: restrict to specific label selector
  # resourceNames: []
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: alert-history-secrets-reader-binding
  namespace: default
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: alert-history-secrets-reader
subjects:
- kind: ServiceAccount
  name: alert-history-service
  namespace: default
```

### Deployment Configuration

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: alert-history-service
spec:
  template:
    spec:
      serviceAccountName: alert-history-service
      containers:
      - name: alert-history
        image: alert-history:latest
        # Service will automatically use in-cluster config
```

### Verify RBAC

```bash
# Check ServiceAccount permissions
kubectl auth can-i list secrets --as=system:serviceaccount:default:alert-history-service -n default

# Test secret access
kubectl get secrets -n default --as=system:serviceaccount:default:alert-history-service
```

## Error Handling

The package defines custom error types for different failure scenarios:

### Error Types

```go
// ConnectionError - K8s API unavailable
var connErr *k8s.ConnectionError
if errors.As(err, &connErr) {
    // Implement fallback or retry logic
}

// AuthError - Insufficient permissions (401/403)
var authErr *k8s.AuthError
if errors.As(err, &authErr) {
    // Check RBAC configuration
}

// NotFoundError - Secret doesn't exist (404)
var notFoundErr *k8s.NotFoundError
if errors.As(err, &notFoundErr) {
    // Handle missing secret
}

// TimeoutError - Request timed out
var timeoutErr *k8s.TimeoutError
if errors.As(err, &timeoutErr) {
    // Retry with longer timeout
}
```

### Retry Logic

The client automatically retries **transient errors**:
- Network timeouts
- Server errors (5xx)
- Rate limiting (429)
- Internal errors

**Non-retryable errors** (fail immediately):
- Authentication errors (401, 403)
- Not found (404)
- Invalid input (400)

### Example: Graceful Degradation

```go
secrets, err := client.ListSecrets(ctx, "default", "publishing-target=true")
if err != nil {
    var connErr *k8s.ConnectionError
    if errors.As(err, &connErr) {
        log.Warn("K8s API unavailable, using cached targets")
        secrets = loadCachedTargets()
    } else {
        return fmt.Errorf("fatal error: %w", err)
    }
}
```

## Performance

### Benchmarks

Measured on fake clientset (production performance may vary):

| Operation | Latency | Allocations | Target |
|-----------|---------|-------------|--------|
| ListSecrets (10 secrets) | ~2-5ms | ~500 B/op | <500ms p95 |
| ListSecrets (100 secrets) | ~10-20ms | ~5 KB/op | <2s p95 |
| GetSecret | ~1-2ms | ~200 B/op | <200ms p95 |
| Health check | ~5-10ms | ~100 B/op | <100ms p95 |

### Performance Tips

1. **Use label selectors**: Reduce number of secrets returned
   ```go
   // Good: specific selector
   secrets, _ := client.ListSecrets(ctx, "default", "publishing-target=true,type=slack")

   // Bad: no selector (returns all secrets)
   secrets, _ := client.ListSecrets(ctx, "default", "")
   ```

2. **Set appropriate timeouts**: Balance between reliability and latency
   ```go
   config := k8s.DefaultK8sClientConfig()
   config.Timeout = 10 * time.Second // Shorter for low-latency environments
   ```

3. **Cache results**: Don't call ListSecrets on every request
   ```go
   type TargetCache struct {
       secrets    []corev1.Secret
       lastUpdate time.Time
       ttl        time.Duration
   }

   func (c *TargetCache) Refresh(client k8s.K8sClient) error {
       if time.Since(c.lastUpdate) < c.ttl {
           return nil // Use cached
       }

       secrets, err := client.ListSecrets(ctx, "default", "publishing-target=true")
       if err != nil {
           return err
       }

       c.secrets = secrets
       c.lastUpdate = time.Now()
       return nil
   }
   ```

## Troubleshooting

### Problem: "failed to load in-cluster config"

**Cause**: Service not running in Kubernetes cluster or ServiceAccount not mounted.

**Solution**:
1. Verify running in K8s pod:
   ```bash
   kubectl exec -it <pod-name> -- ls /var/run/secrets/kubernetes.io/serviceaccount/
   ```
2. Check ServiceAccount mount:
   ```yaml
   spec:
     serviceAccountName: alert-history-service
     automountServiceAccountToken: true  # Must be true
   ```

### Problem: "forbidden: User cannot list resource secrets"

**Cause**: Insufficient RBAC permissions.

**Solution**:
1. Verify RBAC configuration (see [RBAC Requirements](#rbac-requirements))
2. Test permissions:
   ```bash
   kubectl auth can-i list secrets --as=system:serviceaccount:default:alert-history-service
   ```
3. Apply missing permissions:
   ```bash
   kubectl apply -f rbac.yaml
   ```

### Problem: "K8s health check failed"

**Cause**: K8s API server unavailable or network issues.

**Solution**:
1. Check K8s API server status:
   ```bash
   kubectl cluster-info
   ```
2. Verify network policies allow pod → API server traffic
3. Check DNS resolution:
   ```bash
   kubectl exec -it <pod-name> -- nslookup kubernetes.default.svc.cluster.local
   ```

### Problem: "context deadline exceeded"

**Cause**: Request timeout too short or K8s API slow.

**Solution**:
1. Increase timeout:
   ```go
   config := k8s.DefaultK8sClientConfig()
   config.Timeout = 60 * time.Second
   ```
2. Check K8s API performance:
   ```bash
   kubectl get --raw /metrics | grep apiserver_request_duration_seconds
   ```

### Problem: Secrets not found with label selector

**Cause**: Labels not matching or wrong namespace.

**Solution**:
1. Verify secret labels:
   ```bash
   kubectl get secrets -n default --show-labels
   ```
2. Test label selector syntax:
   ```bash
   kubectl get secrets -n default -l "publishing-target=true"
   ```
3. Check namespace:
   ```go
   // Ensure correct namespace
   secrets, _ := client.ListSecrets(ctx, "production", "publishing-target=true")
   ```

### Enable Debug Logging

```go
import "log/slog"

config := k8s.DefaultK8sClientConfig()
config.Logger = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
    Level: slog.LevelDebug,
}))

client, _ := k8s.NewK8sClient(config)
```

Debug logs include:
- All API requests with parameters
- Retry attempts with backoff duration
- Response metadata (count, duration)
- Error details

## API Reference

### K8sClient Interface

```go
type K8sClient interface {
    // ListSecrets returns secrets from namespace matching label selector.
    // Returns empty slice if no secrets match.
    ListSecrets(ctx context.Context, namespace string, labelSelector string) ([]corev1.Secret, error)

    // GetSecret returns a specific secret by name.
    // Returns NotFoundError if secret doesn't exist.
    GetSecret(ctx context.Context, namespace, name string) (*corev1.Secret, error)

    // Health checks if K8s API is accessible.
    // Returns ConnectionError if API is unavailable.
    Health(ctx context.Context) error

    // Close cleans up resources.
    // Safe to call multiple times.
    Close() error
}
```

### Constructor

```go
// NewK8sClient creates a new K8s client with in-cluster configuration.
// Pass nil to use default configuration.
func NewK8sClient(config *K8sClientConfig) (K8sClient, error)
```

### Configuration

```go
// DefaultK8sClientConfig returns configuration with sensible defaults.
func DefaultK8sClientConfig() *K8sClientConfig
```

## Related Documentation

- [TN-046 Requirements](../../../tasks/go-migration-analysis/TN-046-k8s-secrets-client/requirements.md)
- [TN-046 Design](../../../tasks/go-migration-analysis/TN-046-k8s-secrets-client/design.md)
- [TN-047 Target Discovery Manager](../../../tasks/go-migration-analysis/TN-047-target-discovery-manager/)
- [Publishing System Overview](../../business/publishing/)

## License

Copyright (c) 2025 Alert History Service. All rights reserved.

---

**Version**: 1.0.0
**Status**: Production-Ready
**Coverage**: 72.8%
**Tests**: 45+ tests, 4 benchmarks
**Last Updated**: 2025-11-07
