# AMP (Alertmanager++) Helm Chart

[![Artifact Hub](https://img.shields.io/endpoint?url=https://artifacthub.io/badge/repository/amp)](https://artifacthub.io/packages/search?repo=amp)

## Overview

Alertmanager++ (AMP) chart packages the current repository runtime with:
- ✅ **Controlled replacement surface** for alert ingest/query, alert groups, `status`/`receivers`/`reload`, silence CRUD, health/readiness, metrics, and the real publishing path
- 🤖 **Optional LLM-related values** for environments that wire them explicitly
- 📊 **Partial dashboard surface** with broader UI/runtime parity still tracked as follow-up work
- 🟡 **Phased compatibility** rather than a verified full Alertmanager drop-in claim

## Quick Start

```bash
# Install with default values (Lite profile)
helm install amp ./helm/amp

# Install with LLM enabled
helm install amp ./helm/amp \
  --set llm.enabled=true \
  --set llm.provider=openai \
  --set llm.apiKey=sk-your-key
```

## Deployment Profiles

### Lite Profile (Default)
Single-node, no external dependencies:
```bash
helm install amp ./helm/amp --set profile=lite
```
- SQLite storage (PVC-based)
- Memory cache
- Perfect for: dev, testing, <1K alerts/day

### Standard Profile
HA-ready with PostgreSQL + Redis:
```bash
helm install amp ./helm/amp \
  --set profile=standard \
  --set postgresql.enabled=true \
  --set cache.enabled=true
```
- PostgreSQL storage
- Redis/Valkey cache
- HPA (2-10 replicas)
- Perfect for: production, >1K alerts/day

## Configuration

### Basic Values

| Parameter | Description | Default |
|-----------|-------------|---------|
| `profile` | Deployment profile (lite/standard) | `lite` |
| `replicaCount` | Number of replicas | `1` |
| `image.repository` | Image repository | `ghcr.io/ipiton/amp` |
| `image.tag` | Image tag | `latest` |

### LLM Configuration (BYOK)

| Parameter | Description | Default |
|-----------|-------------|---------|
| `llm.enabled` | Enable LLM classification | `false` |
| `llm.provider` | LLM provider (openai/anthropic/azure) | `openai` |
| `llm.apiKey` | API key (use secret in production) | `""` |
| `llm.model` | Model name | `gpt-4o-mini` |

### Storage

| Parameter | Description | Default |
|-----------|-------------|---------|
| `postgresql.enabled` | Enable PostgreSQL | `false` |
| `cache.enabled` | Enable Redis/Valkey | `false` |
| `persistence.enabled` | Enable PVC (Lite) | `true` |
| `persistence.size` | PVC size | `5Gi` |

## Alertmanager Compatibility

AMP chart should currently be treated as a **controlled replacement** deployment path, not as a verified full Alertmanager drop-in replacement:

```yaml
# prometheus.yml - change the URL only after validating the covered slice
alerting:
  alertmanagers:
    - static_configs:
        - targets:
          - amp:9093  # Was: alertmanager:9093
```

Current active runtime surface mounted by the repository bootstrap:
- `POST /api/v2/alerts`
- `GET /api/v2/alerts`
- `GET /api/v2/alerts/groups`
- `GET /api/v2/status`
- `GET /api/v2/receivers`
- `GET/POST /api/v2/silences`
- `GET/DELETE /api/v2/silence/{id}`
- `POST /-/reload`
- `/health`, `/ready`, `/-/healthy`, `/-/ready`, `/metrics`

Wider parity such as config/history APIs, inhibition/classification surfaces, and broader dashboard surfaces remains explicit follow-up work.

## Upgrading

```bash
helm upgrade amp ./helm/amp --reuse-values
```

## Uninstalling

```bash
helm uninstall amp
```

## License

AGPL-3.0 License - see [LICENSE](https://github.com/ipiton/AMP/blob/main/LICENSE)
