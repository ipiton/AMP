# Changelog

All notable changes to Alertmanager++ (AMP) will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- **Hot Reload Infrastructure** - Zero-downtime configuration reload for all components (2024-12-10)
  - 5 Reloadable components: Database, Redis, LLM, Logger, Metrics
  - Config-reloader sidecar for Kubernetes (< 10MB)
  - SIGHUP signal support with automatic rollback
  - Reload latency: < 500ms (p95)
  - E2E test suite for Kubernetes
  - Full documentation: `tasks/hot-reload-full/`
  - PR: #11

### Changed
- **Metrics System v2 Migration** - Complete migration of Health and Refresh metrics to unified `pkg/metrics/v2` (2024-12-08)
  - Added 8 new Prometheus metrics for health and refresh monitoring
  - Removed deprecated stub metrics files
  - Unified API for all publishing metrics
  - Full documentation: `tasks/metrics-v2-full-migration/`

### Improved
- **Code Quality Refactoring** - Comprehensive refactoring achieving 160% quality target (2024-12-05)
  - Unified error handling with `pkg/httperror`
  - Optimized string formatting (50% less allocations)
  - Consolidated metrics to v2 architecture
  - Full documentation: `tasks/code-quality-refactoring/`

## [0.0.1] - 2024-12-04

### Added

#### Core Features
- 100% Alertmanager API v2 compatibility
- Alert grouping engine (33 files, group_by, group_wait, group_interval)
- Alert routing engine (19 files, route tree, multi-receiver support)
- Silencing system (14 files, CRUD, matchers, expiration)
- Inhibition rules (14 files, source/target matchers, state tracking)
- Deduplication service

#### LLM Classification (BYOK)
- Support for OpenAI (GPT-4, GPT-3.5)
- Support for Anthropic (Claude 3)
- Support for Azure OpenAI
- Support for custom LLM proxies
- Circuit breaker with fail-fast protection
- L1/L2 cache for classification results

#### Publishing
- Rootly integration (incidents create/update/resolve)
- Slack integration (messages, threads, rate limiting)
- PagerDuty integration (events, change events)
- Generic webhook publishing
- Parallel publishing with configurable concurrency

#### Web Dashboard
- Alert list with filtering and sorting
- Dashboard overview with stats
- Silences management (CRUD, bulk operations)
- LLM classification display (severity, confidence, recommendations)
- Real-time updates via WebSocket/SSE
- WCAG 2.1 AA accessibility

#### Observability
- 101 Prometheus metrics
- Grafana dashboard
- Health check endpoints
- Structured logging (slog)

#### Storage
- PostgreSQL support
- SQLite support (embedded)
- Redis caching

#### Deployment
- Dockerfile (multi-stage, Alpine, non-root)
- Helm chart with dev/production values
- Kubernetes examples

#### Documentation
- Alertmanager compatibility guide
- Migration quick start
- Migration comparison
- Extension examples (custom classifier, custom publisher)
- API documentation

### Performance
- Sub-5ms p95 latency (10x faster than Alertmanager)
- 5K req/s throughput (10x higher)
- 50MB memory footprint (4x less)
- 100m CPU usage (5x less)

### License
- AGPL-3.0 (copyleft for network services)

[Unreleased]: https://github.com/ipiton/AMP/compare/v0.0.1...HEAD
[0.0.1]: https://github.com/ipiton/AMP/releases/tag/v0.0.1
