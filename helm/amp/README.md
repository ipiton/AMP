# AMP (Alertmanager++) Helm Chart

[![Artifact Hub](https://img.shields.io/endpoint?url=https://artifacthub.io/badge/repository/amp)](https://artifacthub.io/packages/search?repo=amp)

## Overview

Alertmanager++ (AMP) is an enhanced Alertmanager with:
- 🤖 **LLM Classification** - AI-powered alert categorization (BYOK)
- 🔄 **100% API Compatible** - Drop-in replacement for Alertmanager
- 📊 **Web Dashboard** - Built-in UI for alert history and management
- 🚀 **10x Performance** - Handles 5K+ alerts/second

## Quick Start

```bash
# Add Helm repo (if published)
helm repo add amp https://ipiton.github.io/AMP

# Install with default values (Lite profile)
helm install amp amp/amp

# Install with LLM enabled
helm install amp amp/amp \
  --set llm.enabled=true \
  --set llm.provider=openai \
  --set llm.apiKey=sk-your-key
```

## Deployment Profiles

### Lite Profile (Default)
Single-node, no external dependencies:
```bash
helm install amp amp/amp --set profile=lite
```
- SQLite storage (PVC-based)
- Memory cache
- Perfect for: dev, testing, <1K alerts/day

### Standard Profile
HA-ready with PostgreSQL + Redis:
```bash
helm install amp amp/amp \
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

AMP is a **drop-in replacement** for Alertmanager:

```yaml
# prometheus.yml - just change the URL
alerting:
  alertmanagers:
    - static_configs:
        - targets:
          - amp:9093  # Was: alertmanager:9093
```

All Alertmanager API endpoints are supported:
- `POST /api/v2/alerts`
- `GET /api/v2/alerts`
- `GET /api/v2/status`
- `GET /api/v2/receivers`
- `POST/GET/DELETE /api/v2/silences`

## Upgrading

```bash
helm upgrade amp amp/amp --reuse-values
```

## Uninstalling

```bash
helm uninstall amp
```

## License

Apache-2.0 License - see [LICENSE](https://github.com/ipiton/AMP/blob/main/LICENSE)
