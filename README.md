# Alertmanager++ 🚀

> An alert management runtime with Alertmanager-inspired APIs, a real publishing path, and phased parity work

[![License](https://img.shields.io/badge/License-AGPL%203.0-blue.svg)](LICENSE)
[![Go Report Card](https://goreportcard.com/badge/github.com/ipiton/AMP)](https://goreportcard.com/report/github.com/ipiton/AMP)

## ✨ Features

- **Controlled Replacement Slice** - Active runtime currently covers alert ingest, silence CRUD, health/readiness, metrics, and a real publishing path
- **Lean Go Runtime** - Current top-level docs avoid unverified benchmark/resource claims until a reproducible benchmark report is published
- **Extensible Architecture** - Code-level extension points for custom classifiers and publishers
- **Phased Compatibility** - Wider Alertmanager parity remains explicit follow-up work, not an implied full drop-in claim
- **Controlled Migration** - Pilot-oriented quick start for scoped deployments and explicit verification

Current runtime note (2026-03-08): AMP is currently positioned as a **controlled replacement** for scoped deployments, not as a general-purpose Alertmanager drop-in replacement. The active runtime source of truth is `go-app/cmd/server/main.go` + `go-app/internal/application/router.go`.

## 📊 Performance Note

Top-level benchmark and resource comparison numbers have been removed from the README until they are backed by a reproducible benchmark report for the current `main` branch.

## 🚀 Quick Start

### Using Docker

```bash
docker run -p 9093:9093 ghcr.io/ipiton/amp:latest
```

### Using Helm

```bash
helm install amp ./helm/amp
```

### From Source

```bash
git clone https://github.com/ipiton/AMP.git
cd AMP/go-app
make build
./bin/server
```

### Configuration

Alertmanager++ uses **two configuration files** (like Prometheus + Alertmanager):

#### 1. Application Config (`config.yaml`)

Infrastructure settings (database, Redis, server, etc.):

```yaml
# config.yaml - Application infrastructure
profile: standard  # lite or standard

server:
  port: 8080
  host: 0.0.0.0

database:
  host: localhost
  port: 5432
  database: alerthistory
  username: postgres
  password: ${DATABASE_PASSWORD}

redis:
  addr: localhost:6379
  password: ${REDIS_PASSWORD}

log:
  level: info
  format: json
```

See `config.yaml.example` for all options.

#### 2. Alertmanager Config (`alertmanager.yaml`)

Routing and receivers (Alertmanager-compatible core syntax):

```yaml
# alertmanager.yaml - Routing configuration
global:
  resolve_timeout: 5m

route:
  receiver: default
  group_by: [alertname]
  routes:
    - receiver: pagerduty-critical
      match:
        severity: critical
    - receiver: slack-warnings
      match:
        severity: warning

receivers:
  - name: default
    webhook_configs:
      - url: https://webhook.example.com/alerts

  - name: pagerduty-critical
    pagerduty_configs:
      - routing_key: ${PAGERDUTY_KEY}

  - name: slack-warnings
    slack_configs:
      - api_url: ${SLACK_WEBHOOK}
        channel: "#alerts"
```

See `go-app/internal/infrastructure/routing/testdata/production.yaml` for full example.

**Historical runtime config contract (not active in current bootstrap):**
```bash
# Read current runtime config snapshot (JSON)
curl http://localhost:8080/api/v2/config

# Read runtime config snapshot as YAML
curl "http://localhost:8080/api/v2/config?format=yaml"

# Update runtime config from file (applies inhibition/receivers immediately)
curl -X POST http://localhost:8080/api/v2/config \
  --data-binary @alertmanager.yaml

# Check runtime config apply status
curl http://localhost:8080/api/v2/config/status

# Check runtime config apply history
curl "http://localhost:8080/api/v2/config/history?limit=20"

# Filter history by apply status/source
curl "http://localhost:8080/api/v2/config/history?status=ok&source=rollback&limit=20"

# List unique successful config revisions for rollback target selection
curl "http://localhost:8080/api/v2/config/revisions?limit=20"

# Prune old revisions (keep N newest unique successful revisions)
curl -X DELETE "http://localhost:8080/api/v2/config/revisions/prune?keep=20"

# Preview prune result without applying changes
curl -X DELETE "http://localhost:8080/api/v2/config/revisions/prune?keep=20&dryRun=true"

# Roll back to previous successful runtime config revision
curl -X POST http://localhost:8080/api/v2/config/rollback

# Roll back to a specific successful revision by config hash
curl -X POST "http://localhost:8080/api/v2/config/rollback?configHash=<sha256>"

# Preview rollback result without applying changes
curl -X POST "http://localhost:8080/api/v2/config/rollback?configHash=<sha256>&dryRun=true"

# Apply config file changes and reload runtime metadata
curl -X POST http://localhost:8080/-/reload

# Or via Kubernetes ConfigMap
kubectl create configmap alertmanager-config \
  --from-file=alertmanager.yaml
```

`POST /api/v2/config/rollback` returns `409` if there is no previous successful revision to roll back to.
`POST /api/v2/config/rollback?configHash=...` returns `400` for invalid hash, `404` for unknown revision, `409` if the requested revision is already active.
`GET /api/v2/config/history` supports `status=ok|failed` and `source=<startup|api|reload|rollback>` filters.
`GET /api/v2/config/revisions` returns unique successful revisions (`configHash`, `source`, `appliedAt`, `isCurrent`) for targeted rollback selection.
`DELETE /api/v2/config/revisions/prune?keep=...` prunes old revision targets and keeps newest unique successful revisions.
Both rollback and prune support `dryRun=true` preview mode without mutating runtime/file state.

## 📚 Documentation

- **[Migration from Alertmanager](docs/MIGRATION_QUICK_START.md)** - Controlled migration quick start
- **[API Compatibility](docs/ALERTMANAGER_COMPATIBILITY.md)** - Current active slice and future parity gaps
- **[Extension Examples](examples/README.md)** - Custom classifiers and publishers
- **[Security Policy](SECURITY.md)** - Vulnerability reporting

Compatibility note: current active runtime is intentionally narrower than the historical parity/test narrative. Treat AMP today as a controlled replacement slice; wider Alertmanager parity is tracked as follow-up work.

## 🏗️ Architecture

Alertmanager++ is built on a clean, extensible architecture:

```
pkg/core/                   # Core interfaces (zero dependencies)
├── interfaces/             # Pluggable interfaces
│   ├── storage.go         # Storage abstraction
│   ├── classifier.go      # Alert classification
│   └── publisher.go       # Publishing targets
└── domain/                 # Domain models
    ├── alert.go           # Alert structures
    ├── silence.go         # Silence management
    └── classification.go  # Classification metadata
```

### Key Components

- **Alert Grouping** - Smart alert aggregation
- **Silencing** - Flexible silence management with matchers
- **Inhibition** - Alert suppression based on rules
- **Publishing** - Multi-target alert routing (Slack, PagerDuty, Rootly, Webhook)
- **Dashboard** - Modern web UI

## 🔌 Extensibility

Build your own extensions using the existing extension interfaces:

### Custom Classifier Example

```go
import "github.com/ipiton/AMP/pkg/core/interfaces"

type MyClassifier struct{}

func (c *MyClassifier) ClassifyAlert(ctx context.Context, alert core.Alert) (*core.ClassificationResult, error) {
    // Your ML model here
    return &core.ClassificationResult{
        Severity: "high",
        Confidence: 0.95,
    }, nil
}
```

See [examples/](examples/) for complete working implementations:
- **Custom ML Classifier** - ML-based alert classification
- **Custom Publisher** - Microsoft Teams integration

## 🔐 Security

Security is a top priority. We follow industry best practices:

- TLS 1.2+ for all connections
- No secrets in logs
- RBAC for Kubernetes deployments
- Regular security audits

See [SECURITY.md](SECURITY.md) for vulnerability reporting.

## 🤝 Contributing

We welcome contributions! See [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md) for community guidelines.

### How to Contribute

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Add tests
5. Submit a pull request

## 📄 License

AGPL 3.0 - See [LICENSE](LICENSE) for details.

## 🙏 Acknowledgments

- Prometheus Alertmanager team for the excellent original project
- Open source community for inspiration and contributions

## 📞 Support

- **Documentation**: [docs/](docs/)
- **Issues**: [GitHub Issues](https://github.com/ipiton/AMP/issues)
- **Discussions**: [GitHub Discussions](https://github.com/ipiton/AMP/discussions)

---

**Made with ❤️ by the Alertmanager++ team**
