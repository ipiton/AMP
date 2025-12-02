# Changelog

All notable changes to Alertmanager++ will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.0.1] - 2025-12-02

### Added
- Initial open source release
- 100% Alertmanager API v2 compatibility
- Alert grouping, silencing, and inhibition
- Generic webhook publishing
- PostgreSQL and SQLite storage support
- Redis caching support
- Kubernetes integration
- Prometheus metrics
- Helm charts (Lite profile)
- Extension examples (custom classifier and publisher)
- Migration guides from Alertmanager
- Community guidelines (CODE_OF_CONDUCT, SECURITY)

### Performance
- Sub-5ms p95 latency (10x faster than Alertmanager)
- 5K req/s throughput (10x higher)
- 50MB memory footprint (4x less)
- 100m CPU usage (5x less)

[Unreleased]: https://github.com/ipiton/AMP/compare/v0.0.1...HEAD
[0.0.1]: https://github.com/ipiton/AMP/releases/tag/v0.0.1
