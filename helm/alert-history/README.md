# alert-history

Helm chart для деплоя Alert History Service (Intelligent Alert Proxy)

## Features
- **Intelligent Alert Proxy** (`POST /webhook/proxy`) - LLM-powered alert processing
- **Enrichment Modes** (`GET/POST /enrichment/mode`) - Three modes: transparent, transparent_with_recommendations, enriched
- **LLM Integration** - GPT-4 powered alert classification and recommendations
- **Dynamic Target Discovery** (Rootly, PagerDuty, Slack)
- **Prometheus метрики** и ServiceMonitor
- **Horizontal Scaling** - PostgreSQL + Redis for stateless design
- **Deployment Profiles** - Two profiles for different use cases (Lite & Standard)

## Deployment Profiles (TN-96)

This Helm chart supports **two deployment profiles** to fit different use cases and infrastructure requirements:

### 🪶 Lite Profile

**Use Case:** Development, testing, small deployments, single-node setups

**Features:**
- **Embedded Storage:** SQLite database (no external PostgreSQL required)
- **Memory-Only Cache:** No Redis/Valkey required
- **Zero External Dependencies:** Single binary deployment
- **PVC-Based:** Uses PersistentVolumeClaim for SQLite database
- **Resource Efficient:** Lower CPU/memory requirements

**Configuration:**
```yaml
profile: lite

liteProfile:
  persistence:
    enabled: true
    size: 5Gi
    storageClass: ""
    mountPath: "/data"
  resources:
    limits:
      cpu: 500m
      memory: 512Mi
    requests:
      cpu: 250m
      memory: 256Mi
```

**Installation:**
```bash
helm install alert-history ./helm/alert-history \
  --set profile=lite \
  --set image.repository=<your-registry>/alert-history \
  --set image.tag=latest
```

### ⚡ Standard Profile (Default)

**Use Case:** Production, high-availability, distributed systems, 2-10 replicas

**Features:**
- **PostgreSQL Storage:** External database for HA
- **Redis/Valkey L2 Cache:** Distributed caching
- **Horizontal Scaling:** 2-10 replicas with HPA
- **Production-Grade:** Full observability, metrics, ServiceMonitor

**Configuration:**
```yaml
profile: standard

postgresql:
  enabled: true
  database: "alert_history"
  username: "alert_history"
  password: "secure_password_123"
  persistence:
    enabled: true
    size: 10Gi

cache:
  enabled: true
  host: "{{ include \"alerthistory.fullname\" . }}-valkey"
  port: 6379
```

**Installation:**
```bash
helm install alert-history ./helm/alert-history \
  --set profile=standard \
  --set image.repository=<your-registry>/alert-history \
  --set image.tag=latest \
  --set postgresql.enabled=true \
  --set cache.enabled=true
```

### Profile Comparison

| Feature | 🪶 Lite Profile | ⚡ Standard Profile |
|---------|----------------|-------------------|
| **Storage** | SQLite (embedded) | PostgreSQL (external) |
| **Cache** | Memory-only (L1) | Redis L2 + Memory L1 |
| **External Deps** | **Zero** | Postgres + Redis |
| **Replicas** | 1 (single-node) | 2-10 (HA) |
| **Use Case** | Dev, test, small | Production, HA |
| **Resource Usage** | Low (250m CPU, 256Mi RAM) | Higher (500m CPU, 512Mi RAM+) |
| **Data Persistence** | PVC (local) | PostgreSQL (distributed) |

## Monitoring and Metrics

- Service exports Prometheus metrics on the `/metrics` endpoint (port 8080).
- ServiceMonitor is enabled for automatic metric collection (if kube-prometheus-stack is installed).
- Metrics exported:
  - `alert_history_webhook_events_total` — webhook events (by status, alertname, namespace)
  - `alert_history_webhook_errors_total` — webhook processing errors
  - `alert_history_history_queries_total` — history queries
  - `alert_history_report_queries_total` — report queries
  - `alert_history_db_alerts` — number of alerts in the database
  - `alert_history_request_latency_seconds` — request processing time (histogram)

## Quick Start

1. Build Docker image:
   ```bash
   docker build -t alert-history:latest .
   ```

2. Push image to your registry (if needed):
   ```bash
   docker tag alert-history:latest <your-registry>/alert-history:latest
   docker push <your-registry>/alert-history:latest
   ```

3. Install Helm chart with LLM support:
   ```bash
   helm install alert-history ./helm/alert-history \
     --set image.repository=<your-registry>/alert-history \
     --set image.tag=latest \
     --set postgresql.enabled=true \
     --set cache.enabled=true \
     --set llm.enabled=true \
     --set llm.apiKey="your-llm-api-key" \
     --set llm.proxyUrl="https://llm-proxy.b2broker.tech" \
     --set llm.model="openai/gpt-4o"
   ```

4. Forward port for local test:
   ```bash
   kubectl port-forward svc/alert-history-alert-history 8080:8080
   ```

5. Configure Alertmanager webhook:
   ```yaml
   receivers:
     - name: 'alert-history'
       webhook_configs:
          - url: 'http://alert-history-alert-history:8080/webhook/proxy'
   ```

6. Test LLM integration:
   ```bash
   # Set enrichment mode to transparent_with_recommendations
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

## Example History Query

```bash
curl 'http://localhost:8080/history?alertname=CPUThrottlingHigh&status=firing&since=2024-06-01T00:00:00'
```

## values.yaml Variables

### Core Configuration
- `image.repository` — image name
- `image.tag` — image tag
- `service.port` — service port
- `retentionDays` — alert history retention period in days (default: 30)

### LLM Configuration
- `llm.enabled` — enable LLM integration (default: true)
- `llm.apiKey` — LLM API key (required for LLM features)
- `llm.proxyUrl` — LLM proxy URL (default: https://llm-proxy.b2broker.tech)
- `llm.model` — LLM model (default: openai/gpt-4o)
- `llm.timeout` — LLM request timeout (default: 30)
- `llm.maxRetries` — LLM retry attempts (default: 3)

### Database Configuration
- `postgresql.enabled` — enable PostgreSQL (recommended for production)
- `cache.enabled` — enable Redis/DragonflyDB cache
- `persistence.enabled` — enable PVC (legacy SQLite)

## PVC
Alert history is stored in `/data/alert_history.sqlite3` (persistent volume). Old records are automatically deleted after `retentionDays` days.

## ServiceMonitor Example

ServiceMonitor is created automatically:

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: <release>-alert-history
spec:
  selector:
    matchLabels:
      app: alert-history
      release: <release>
  endpoints:
    - port: http
      path: /metrics
      interval: 30s
      scrapeTimeout: 10s
```

---

**Author:** @your-team
