# 📝 Configuration Guide

**Date:** 9 декабря 2024
**Version:** 1.0.0
**Status:** ✅ Production-Ready

---

## 📋 Overview

Alertmanager++ uses **two separate configuration files** for flexibility and hot reload support:

1. **`config.yaml`** - Application infrastructure configuration
2. **`alertmanager.yaml`** - Alerting routing configuration (Alertmanager-compatible)

This separation follows industry best practices (Prometheus, Grafana, Kubernetes).

---

## 🎯 Configuration Architecture

### Why Two Configs?

```
┌─────────────────────────────────────────────┐
│  config.yaml (Application Config)           │
│  ─────────────────────────────────          │
│  • Database, Redis, Server                  │
│  • Infrastructure settings                  │
│  • Changes rarely                           │
│  • Requires restart                         │
│  • Managed by DevOps                        │
└─────────────────────────────────────────────┘
                    ↓
┌─────────────────────────────────────────────┐
│  alertmanager.yaml (Alerting Config)        │
│  ────────────────────────────────           │
│  • Routes, Receivers, Inhibition            │
│  • Business logic                           │
│  • Changes frequently                       │
│  • Hot reload (no restart!) ✨              │
│  • Managed by SRE/Operations                │
└─────────────────────────────────────────────┘
```

**Benefits:**
- ✅ Separation of concerns
- ✅ Hot reload for routing changes
- ✅ Security (sensitive data separate)
- ✅ Alertmanager compatibility
- ✅ Version control friendly

---

## 📄 Config 1: Application Config (`config.yaml`)

### Purpose

Infrastructure and application settings.

### Location

```
/Users/vitaliisemenov/Documents/Helpfull/AMP-OSS/
├── config.yaml.example  ← Template
└── config.yaml          ← Your config (create from example)
```

### Structure

```yaml
# ============================================================================
# Deployment Profile
# ============================================================================
profile: standard  # "lite" (embedded) or "standard" (Postgres+Redis)

# ============================================================================
# Storage Backend
# ============================================================================
storage:
  backend: postgres  # "filesystem" for lite, "postgres" for standard
  filesystem_path: /data/alerthistory.db

# ============================================================================
# Server Configuration
# ============================================================================
server:
  port: 8080
  host: 0.0.0.0
  read_timeout: 30s
  write_timeout: 30s
  graceful_shutdown_timeout: 30s

# ============================================================================
# Database (PostgreSQL)
# ============================================================================
database:
  host: localhost
  port: 5432
  database: alerthistory
  username: postgres
  password: ${DATABASE_PASSWORD}  # From environment
  ssl_mode: disable
  max_connections: 25

# ============================================================================
# Redis (Optional)
# ============================================================================
redis:
  addr: localhost:6379
  password: ${REDIS_PASSWORD}  # From environment
  db: 0
  pool_size: 10

# ============================================================================
# LLM Configuration (Optional)
# ============================================================================
llm:
  enabled: false  # Set to true to enable AI classification
  provider: openai
  model: gpt-4o
  api_key: ${LLM_API_KEY}  # Your own OpenAI key (BYOK)
  base_url: https://api.openai.com/v1
  timeout: 30s

# ============================================================================
# Logging
# ============================================================================
log:
  level: info  # debug, info, warn, error
  format: json  # json, text
  output: stdout

# ============================================================================
# Metrics
# ============================================================================
metrics:
  enabled: true
  path: /metrics
  namespace: alert_history

# ============================================================================
# Webhook Settings
# ============================================================================
webhook:
  max_request_size: 1048576  # 1MB
  request_timeout: 30s

  rate_limiting:
    enabled: true
    per_ip_limit: 100
    global_limit: 1000

# ============================================================================
# HTTP Client (for outbound requests)
# ============================================================================
http_client:
  timeout: 30s
  max_idle_conns: 100
  max_idle_conns_per_host: 10

# ============================================================================
# Retry Configuration (Unified)
# ============================================================================
retry:
  max_attempts: 4
  base_delay: 100ms
  max_delay: 30s
  multiplier: 2.0
  jitter_ratio: 0.15

# ============================================================================
# OpenTelemetry Tracing (Optional)
# ============================================================================
telemetry:
  enabled: false  # Set to true to enable distributed tracing
  endpoint: localhost:4317
  service_name: alert-history-service
  sampling_ratio: 1.0
```

### Usage

```bash
# Create from example
cp config.yaml.example config.yaml

# Edit values
vi config.yaml

# Set environment variables
export DATABASE_PASSWORD=your_password
export REDIS_PASSWORD=your_password
export LLM_API_KEY=sk-your-openai-key  # Optional

# Run application
./amp-server --config config.yaml
```

### When to Modify

- Adding new database
- Changing server port
- Enabling/disabling features (LLM, telemetry)
- Adjusting resource limits
- Changing log levels

**Requires:** Application restart

---

## 📄 Config 2: Alertmanager Config (`alertmanager.yaml`)

### Purpose

Alerting routing and notification receivers (100% Alertmanager-compatible).

### Location

**Examples:**
```
go-app/internal/infrastructure/routing/testdata/
├── production.yaml  ← Full production example
└── minimal.yaml     ← Minimal example
```

**Your config:** Create anywhere, load via API or ConfigMap

### Structure

```yaml
# ============================================================================
# Global Settings (Optional)
# ============================================================================
global:
  resolve_timeout: 5m
  http_config:
    proxy_url: http://proxy.corp:8080

# ============================================================================
# Routing Tree (Required)
# ============================================================================
route:
  receiver: default  # Default receiver
  group_by: [alertname, cluster, service]
  group_wait: 30s
  group_interval: 5m
  repeat_interval: 4h

  # Child routes
  routes:
    # Critical alerts to PagerDuty
    - receiver: pagerduty-critical
      match:
        severity: critical
      group_wait: 10s
      repeat_interval: 30m

    # Warnings to Slack
    - receiver: slack-warnings
      match:
        severity: warning
      group_wait: 1m

    # Database alerts to DBA team
    - receiver: email-database
      match_re:
        service: "^(postgres|mysql|redis).*"
      group_by: [alertname, instance]

# ============================================================================
# Receivers (Required, min=1)
# ============================================================================
receivers:
  # Default webhook receiver
  - name: default
    webhook_configs:
      - url: https://webhook.example.com/alerts
        http_method: POST
        send_resolved: true
        max_alerts: 10

  # PagerDuty for critical alerts
  - name: pagerduty-critical
    pagerduty_configs:
      - routing_key: ${PAGERDUTY_ROUTING_KEY_CRITICAL}
        severity: critical
        description: "Critical Alert: {{ .GroupLabels.alertname }}"
        details:
          environment: "{{ .CommonLabels.environment }}"
          cluster: "{{ .CommonLabels.cluster }}"

  # Slack for warnings
  - name: slack-warnings
    slack_configs:
      - api_url: ${SLACK_WEBHOOK_URL}
        channel: "#alerts-warnings"
        username: "Alertmanager++"
        icon_emoji: ":warning:"
        title: "Warning: {{ .GroupLabels.alertname }}"
        text: "{{ range .Alerts }}{{ .Annotations.summary }}\n{{ end }}"
        color: warning
        send_resolved: true

  # Email for database team
  - name: email-database
    email_configs:
      - to: "database-team@example.com"
        from: "alerts@example.com"
        subject: "Database Alert: {{ .GroupLabels.alertname }}"
        html: "<h2>Alerts</h2><ul>{{ range .Alerts }}<li>{{ .Annotations.description }}</li>{{ end }}</ul>"
        send_resolved: true

# ============================================================================
# Inhibition Rules (Optional)
# ============================================================================
inhibit_rules:
  # Inhibit warnings when critical alert is firing
  - source_match:
      severity: critical
    target_match:
      severity: warning
    equal: [alertname, cluster]
```

### Usage

**Option 1: Load via API (Hot Reload)**
```bash
# Load configuration
curl -X POST http://localhost:8080/api/v2/config \
  -H "Content-Type: application/yaml" \
  --data-binary @alertmanager.yaml

# Verify
curl http://localhost:8080/api/v2/config

# Update without restart!
curl -X POST http://localhost:8080/api/v2/config \
  --data-binary @alertmanager-updated.yaml
```

**Option 2: Kubernetes ConfigMap**
```bash
# Create ConfigMap
kubectl create configmap alertmanager-config \
  --from-file=alertmanager.yaml \
  -n alert-history

# Application loads it automatically on startup
```

**Option 3: File Path (if configured)**
```yaml
# config.yaml
app:
  alertmanager_config_path: /etc/alertmanager/alertmanager.yaml
```

### When to Modify

- Adding new receivers (Slack, PagerDuty)
- Changing routing rules
- Adjusting grouping/timing
- Adding inhibition rules
- Updating templates

**Requires:** Hot reload via API (no restart needed!)

---

## 🔄 Hot Reload

### How It Works

```bash
# 1. Edit alertmanager.yaml
vi alertmanager.yaml

# 2. Reload via API
curl -X POST http://localhost:8080/api/v2/config \
  --data-binary @alertmanager.yaml

# 3. Verify
curl http://localhost:8080/api/v2/config/status

# Application continues running! ✨
```

### Rollback Support

```bash
# Rollback to previous version
curl -X POST http://localhost:8080/api/v2/config/rollback

# View config history
curl http://localhost:8080/api/v2/config/history
```

---

## 📊 Comparison with Other Systems

### Prometheus + Alertmanager

```
prometheus/
├── prometheus.yml      ← Scrape config, storage (restart required)
└── alertmanager.yml    ← Routes, receivers (hot reload)
```

### Alertmanager++ (AMP)

```
amp/
├── config.yaml         ← Infrastructure config (restart required)
└── alertmanager.yaml   ← Routing config (hot reload)
```

**Same architecture!** ✅

---

### Grafana

```
grafana/
├── grafana.ini         ← Server config (restart required)
└── provisioning/
    └── datasources.yaml ← Data sources (hot reload)
```

### Kubernetes

```
k8s/
├── kubelet-config.yaml ← Node config (restart required)
└── pod-spec.yaml       ← Workload config (hot reload)
```

**Industry standard pattern!** ✅

---

## 🎯 Quick Start Examples

### Development (Lite Profile)

**config.yaml:**
```yaml
profile: lite
storage:
  backend: filesystem
  filesystem_path: /tmp/alerthistory.db

server:
  port: 8080

log:
  level: debug
  format: text
```

**alertmanager.yaml:**
```yaml
route:
  receiver: webhook

receivers:
  - name: webhook
    webhook_configs:
      - url: http://localhost:9000/webhook
```

**Start:**
```bash
./amp-server --config config.yaml
curl -X POST http://localhost:8080/api/v2/config \
  --data-binary @alertmanager.yaml
```

---

### Production (Standard Profile)

**config.yaml:**
```yaml
profile: standard
storage:
  backend: postgres

database:
  host: postgres.prod.svc.cluster.local
  port: 5432
  database: alerthistory
  username: amp
  password: ${DATABASE_PASSWORD}
  max_connections: 50

redis:
  addr: redis.prod.svc.cluster.local:6379
  password: ${REDIS_PASSWORD}
  pool_size: 20

server:
  port: 8080

log:
  level: info
  format: json

telemetry:
  enabled: true
  endpoint: jaeger-collector:4317
  sampling_ratio: 0.1  # 10% sampling

llm:
  enabled: true
  provider: openai
  api_key: ${LLM_API_KEY}
  model: gpt-4o
```

**alertmanager.yaml:**
```yaml
global:
  resolve_timeout: 5m

route:
  receiver: default
  group_by: [alertname, cluster, service]
  group_wait: 30s
  group_interval: 5m
  repeat_interval: 4h
  routes:
    - receiver: pagerduty-critical
      match:
        severity: critical
      group_wait: 10s
    - receiver: slack-warnings
      match:
        severity: warning
    - receiver: email-database
      match_re:
        service: "^(postgres|mysql|redis).*"

receivers:
  - name: default
    webhook_configs:
      - url: https://webhook.prod.example.com/alerts

  - name: pagerduty-critical
    pagerduty_configs:
      - routing_key: ${PAGERDUTY_KEY_CRITICAL}

  - name: slack-warnings
    slack_configs:
      - api_url: ${SLACK_WEBHOOK_PROD}
        channel: "#alerts-prod"

  - name: email-database
    email_configs:
      - to: "dba-team@example.com"
        from: "alerts@example.com"

inhibit_rules:
  - source_match:
      severity: critical
    target_match:
      severity: warning
    equal: [alertname, cluster]
```

**Deploy:**
```bash
# Kubernetes deployment
kubectl create secret generic amp-secrets \
  --from-literal=database-password=${DATABASE_PASSWORD} \
  --from-literal=redis-password=${REDIS_PASSWORD} \
  --from-literal=llm-api-key=${LLM_API_KEY}

kubectl create configmap amp-config \
  --from-file=config.yaml

kubectl create configmap alertmanager-config \
  --from-file=alertmanager.yaml

helm install amp ./helm/amp -n monitoring
```

---

## 📚 Configuration Reference

### Application Config Fields

| Section | Purpose | Required | Restart |
|---------|---------|----------|---------|
| `profile` | Deployment profile | Yes | Yes |
| `storage` | Storage backend | Yes | Yes |
| `server` | HTTP server | Yes | Yes |
| `database` | PostgreSQL | Conditional | Yes |
| `redis` | Redis cache | Optional | Yes |
| `llm` | AI classification | Optional | Yes |
| `log` | Logging | Yes | Yes |
| `metrics` | Prometheus | Yes | Yes |
| `webhook` | Webhook endpoint | Yes | Yes |
| `http_client` | HTTP client | Yes | Yes |
| `retry` | Retry strategy | Yes | Yes |
| `telemetry` | OpenTelemetry | Optional | Yes |

### Alertmanager Config Fields

| Section | Purpose | Required | Reload |
|---------|---------|----------|--------|
| `global` | Global settings | Optional | Hot |
| `route` | Routing tree | Yes | Hot |
| `receivers` | Notification targets | Yes | Hot |
| `inhibit_rules` | Inhibition rules | Optional | Hot |
| `templates` | Template files | Optional | Hot |

---

## 🔐 Security Best Practices

### 1. Use Environment Variables

**config.yaml:**
```yaml
database:
  password: ${DATABASE_PASSWORD}  # ✅ From environment

redis:
  password: ${REDIS_PASSWORD}  # ✅ From environment

llm:
  api_key: ${LLM_API_KEY}  # ✅ From environment
```

**Set variables:**
```bash
export DATABASE_PASSWORD=secret
export REDIS_PASSWORD=secret
export LLM_API_KEY=sk-your-key
```

---

### 2. Use Kubernetes Secrets

```bash
# Create secret
kubectl create secret generic amp-secrets \
  --from-literal=database-password=secret \
  --from-literal=redis-password=secret \
  --from-literal=llm-api-key=sk-your-key

# Reference in deployment
env:
  - name: DATABASE_PASSWORD
    valueFrom:
      secretKeyRef:
        name: amp-secrets
        key: database-password
```

---

### 3. Never Commit Secrets

**`.gitignore`:**
```
config.yaml
*.secret
*.key
.env
```

**Use examples:**
```
config.yaml.example  ✅ Commit (no secrets)
config.yaml          ❌ Don't commit (has secrets)
```

---

## 🚀 Deployment Scenarios

### Scenario 1: Docker Compose

```yaml
# docker-compose.yml
services:
  amp:
    image: amp:latest
    volumes:
      - ./config.yaml:/etc/amp/config.yaml
      - ./alertmanager.yaml:/etc/amp/alertmanager.yaml
    environment:
      - DATABASE_PASSWORD=${DATABASE_PASSWORD}
    command: ["--config", "/etc/amp/config.yaml"]
```

---

### Scenario 2: Kubernetes

```yaml
# deployment.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: amp
spec:
  template:
    spec:
      containers:
      - name: amp
        image: amp:latest
        env:
        - name: DATABASE_PASSWORD
          valueFrom:
            secretKeyRef:
              name: amp-secrets
              key: database-password
        volumeMounts:
        - name: config
          mountPath: /etc/amp/config.yaml
          subPath: config.yaml
      volumes:
      - name: config
        configMap:
          name: amp-config
```

**Load alertmanager.yaml:**
```bash
# Via init container or API call
kubectl exec -it amp-0 -- curl -X POST http://localhost:8080/api/v2/config \
  --data-binary @/etc/alertmanager/alertmanager.yaml
```

---

### Scenario 3: Bare Metal

```bash
# 1. Create configs
cp config.yaml.example /etc/amp/config.yaml
vi /etc/amp/config.yaml

cp examples/alertmanager.yaml /etc/amp/alertmanager.yaml
vi /etc/amp/alertmanager.yaml

# 2. Set environment
export DATABASE_PASSWORD=secret
export REDIS_PASSWORD=secret

# 3. Start service
/usr/local/bin/amp-server --config /etc/amp/config.yaml

# 4. Load alerting config
curl -X POST http://localhost:8080/api/v2/config \
  --data-binary @/etc/amp/alertmanager.yaml
```

---

## 📖 Related Documentation

- **`config.yaml.example`** - Full application config example
- **`go-app/internal/infrastructure/routing/testdata/production.yaml`** - Full alertmanager config example
- **`docs/ALERTMANAGER_COMPATIBILITY.md`** - API compatibility guide
- **`helm/amp/DEPLOYMENT.md`** - Kubernetes deployment guide
- **`docs/MIGRATION_QUICK_START.md`** - Migration from Alertmanager

---

## 🏆 Summary

### Two Configs = Best Practice ✅

**Application Config (`config.yaml`):**
- Infrastructure settings
- Restart required
- Managed by DevOps

**Alerting Config (`alertmanager.yaml`):**
- Routing and receivers
- Hot reload supported
- Managed by SRE

**This is the standard approach used by:**
- ✅ Prometheus + Alertmanager
- ✅ Grafana
- ✅ Kubernetes
- ✅ Industry best practices

**Benefits:**
- ✅ Separation of concerns
- ✅ Hot reload support
- ✅ Security (secrets separate)
- ✅ Alertmanager compatibility
- ✅ Flexible management

---

_Guide completed: 9 декабря 2024, 16:35 MSK_
_Status: Configuration architecture is CORRECT_
_Standard: Industry best practices_

**DOCUMENTATION COMPLETE! ✅**
