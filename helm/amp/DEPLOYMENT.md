# Alert History Service Deployment Guide

## Quick Start with LLM Integration

### 1. Prerequisites

- Kubernetes cluster (1.20+)
- Helm 3.8+
- kubectl configured
- LLM API key for `https://llm-proxy.b2broker.tech`

### 2. Development Deployment

```bash
# Clone the repository
git clone <your-repo>
cd alert-history

# Install with development configuration
helm install alert-history-dev ./helm/alert-history \
  -f helm/alert-history/values-dev.yaml \
  --namespace monitoring \
  --create-namespace

# Port forward for local access
kubectl port-forward svc/alert-history-dev-alert-history 8080:8080 -n monitoring
```

### 3. Production Deployment

```bash
# Create namespace
kubectl create namespace monitoring

# Install with production configuration
helm install alert-history ./helm/alert-history \
  -f helm/alert-history/values-production.yaml \
  --namespace monitoring

# Set LLM API key via external secrets (recommended)
# Or set directly in values-production.yaml
```

### 4. Test LLM Integration

```bash
# Set enrichment mode
curl -X POST http://localhost:8080/enrichment/mode \
  -H "Content-Type: application/json" \
  -d '{"mode": "transparent_with_recommendations"}'

# Send test webhook
curl -X POST http://localhost:8080/webhook/proxy \
  -H "Content-Type: application/json" \
  -d '{
    "receiver": "test",
    "alerts": [{
      "fingerprint": "test-llm",
      "status": "firing",
      "labels": {"alertname": "HighCPUUsage"},
      "annotations": {"summary": "High CPU usage detected"},
      "startsAt": "2024-01-01T00:00:00Z"
    }]
  }'
```

### 5. Configure Alertmanager

```yaml
# alertmanager-config.yaml
global:
  resolve_timeout: 5m

route:
  group_by: ['alertname']
  group_wait: 10s
  group_interval: 10s
  repeat_interval: 1h
  receiver: 'alert-history'

receivers:
  - name: 'alert-history'
    webhook_configs:
      - url: 'http://alert-history-alert-history:8080/webhook/proxy'
        send_resolved: true
```

### 6. Access Dashboard

```bash
# Port forward dashboard
kubectl port-forward svc/alert-history-alert-history 8080:8080 -n monitoring

# Open in browser
open http://localhost:8080/dashboard
```

## Configuration Options

### LLM Configuration

```yaml
llm:
  enabled: true
  proxyUrl: "https://llm-proxy.b2broker.tech"
  apiKey: "your-api-key"  # Set via secret in production
  model: "openai/gpt-4o"
  timeout: 30
  maxRetries: 3
```

### Enrichment Modes

1. **transparent** - Simple processing without LLM
2. **transparent_with_recommendations** - LLM analysis with recommendations (no filtering)
3. **enriched** - Full LLM processing with filtering

### Publishing Targets

Configure targets in `values-production.yaml`:

```yaml
publishingTargets:
  - name: rootly-production
    type: webhook
    format: rootly
    url: "https://api.rootly.com/webhooks/your-webhook-id"
    enabled: true
    secret:
      apiKey: "your-api-key"
    filterConfig:
      severity: ["critical", "warning"]
      excludeNoise: true
      minConfidence: 0.8
```

## Troubleshooting

### Check LLM Status

```bash
# Check if LLM is working
curl http://localhost:8080/healthz

# Check logs
kubectl logs -f deployment/alert-history-alert-history -n monitoring

# Check LLM configuration
kubectl get configmap alert-history-alert-history-config -n monitoring -o yaml
```

### Common Issues

1. **LLM not working**: Check API key and proxy URL
2. **Webhook errors**: Verify Alertmanager configuration
3. **Database issues**: Check PostgreSQL connection
4. **Cache issues**: Verify Redis/DragonflyDB connection

## Monitoring

- Prometheus metrics: `http://localhost:8080/metrics`
- Health check: `http://localhost:8080/healthz`
- Dashboard: `http://localhost:8080/dashboard`

## Security Notes

- Use external secrets for API keys in production
- Enable RBAC for cross-namespace access
- Configure network policies
- Use TLS for ingress in production
