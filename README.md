# Alertmanager++ 🚀

> A high-performance, Alertmanager-compatible alert management system with 10-20x better performance

[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)
[![Go Report Card](https://goreportcard.com/badge/github.com/ipiton/AMP)](https://goreportcard.com/report/github.com/ipiton/AMP)

## ✨ Features

- **100% Alertmanager API v2 Compatible** - Drop-in replacement for Prometheus Alertmanager
- **10-20x Faster Performance** - Optimized Go implementation with sub-5ms latency
- **75% Less Resources** - 50MB memory footprint (vs 200MB Alertmanager)
- **Extensible Architecture** - Plugin system for custom classifiers and publishers
- **Production-Ready** - Comprehensive documentation, tests, and monitoring
- **Easy Migration** - 5-minute quick start guide with blue-green deployment

## 📊 Performance Comparison

| Metric | Alertmanager | Alertmanager++ | Improvement |
|--------|--------------|----------------|-------------|
| Latency (p95) | 50ms | <5ms | **10x faster** ⚡ |
| Throughput | 500 req/s | 5,000 req/s | **10x higher** 🚀 |
| Memory | 200MB | 50MB | **4x less** 💾 |
| CPU | 500m | 100m | **5x less** ⚡ |

## 🚀 Quick Start

### Using Docker

```bash
docker run -p 9093:9093 ghcr.io/ipiton/amp:latest
```

### Using Helm

```bash
helm repo add amp https://ipiton.github.io/AMP
helm install alertmanager-plus-plus amp/alertmanager-plus-plus
```

### From Source

```bash
git clone https://github.com/ipiton/AMP.git
cd AMP/go-app
make build
./bin/server
```

### Configuration

Create `config/config.yaml`:

```yaml
server:
  port: 9093

database:
  type: sqlite  # or postgres
  path: /data/alertmanager.db

profile: lite  # or standard
```

## 📚 Documentation

- **[Migration from Alertmanager](docs/MIGRATION_QUICK_START.md)** - 5-minute migration guide
- **[API Compatibility](docs/ALERTMANAGER_COMPATIBILITY.md)** - Full compatibility matrix
- **[Extension Examples](examples/README.md)** - Custom classifiers and publishers
- **[Security Policy](SECURITY.md)** - Vulnerability reporting

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

Build your own extensions using the plugin system:

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

Apache 2.0 - See [LICENSE](LICENSE) for details.

## 🙏 Acknowledgments

- Prometheus Alertmanager team for the excellent original project
- Open source community for inspiration and contributions

## 📞 Support

- **Documentation**: [docs/](docs/)
- **Issues**: [GitHub Issues](https://github.com/ipiton/AMP/issues)
- **Discussions**: [GitHub Discussions](https://github.com/ipiton/AMP/discussions)

---

**Made with ❤️ by the Alertmanager++ team**
