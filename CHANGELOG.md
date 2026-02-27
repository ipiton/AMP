# Changelog

All notable changes to Alertmanager++ (AMP) will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Changed
- **Metrics System v2 Migration** - Complete migration of Health and Refresh metrics to unified `pkg/metrics/v2` (2024-12-08)
  - Added 8 new Prometheus metrics for health and refresh monitoring
  - Removed deprecated stub metrics files
  - Unified API for all publishing metrics
  - Full documentation: `tasks/metrics-v2-full-migration/`
- **Alertmanager Ops Compatibility Hardening** - Runtime contract aligned with upstream behavior (2026-02-26)
  - `POST /-/reload` returns `200` with empty body on success
  - `POST /-/reload` returns `500` on config reload/parse failures
  - `/debug/*` switched from JSON stub to pprof-backed proxy behavior
  - Added static compatibility routes: `/script.js`, `/favicon.ico`, `/lib/*`
  - `GET /api/v2/status` now reports `cluster.status=disabled` in single-node runtime (closer to upstream behavior when clustering is off)
  - `GET /api/v2/receivers` now returns only configured `receivers[*].name` values (no route-name expansion, no alert-label discovery fallback)
  - `GET /api/v2/alerts` and `GET /api/v2/alerts/groups` query parsing aligned closer to upstream runtime behavior:
    - invalid state-flag bool values (`active/silenced/inhibited/unprocessed/muted`) now fall back to `false` when parameter is present
    - invalid `status`/`resolved` query values no longer return `400` and are ignored (`200` response)
    - invalid `receiver`/`filter` query errors now return upstream-like JSON string payloads on `400` (instead of object-wrapped errors)
    - invalid `receiver` / `filter` error message text now matches upstream wording (`failed to parse receiver param: ...`, `bad matcher format: ...`)
    - `GET /api/v2/alerts/groups` grouping labels now respect runtime `route.group_by` (including upstream-like empty `labels: {}` when `group_by` is omitted/empty in config)
  - API timestamps now use upstream-like millisecond precision (`.000Z`) for core runtime responses (`/api/v2/status` uptime, alerts/silences list payloads)
  - `POST /api/v2/alerts` error contracts aligned closer to upstream runtime behavior:
    - invalid JSON/time payloads return `{code:400,message}` on `400`
    - invalid JSON object parse message now uses upstream-like payload type wording (`models.PostableAlerts`)
    - missing `labels` returns `{code:602,message}` on `422`
    - invalid `generatorURL` returns `{code:601,message}` on `422`
    - empty `labels` returns upstream-like JSON string message on `400`
    - date-only timestamps (`YYYY-MM-DD`) for `startsAt`/`endsAt` are now accepted (upstream-like ingest behavior)
  - `DELETE /api/v2/silence/{id}` now returns `200` with empty body on success (upstream-like)
  - `POST /api/v2/silences` error contracts moved closer to upstream runtime behavior:
    - schema/required validation errors return `422` with `{code,message}` (for example `code=602/612`)
    - update with unknown/invalid `id` returns `404` with JSON string payload (`"silence not found"`)
    - create-time semantic validation keeps upstream-like JSON string payloads on `400` (e.g. invalid matcher regex, invalid timing)
  - `GET /api/v2/silences?filter=...` now returns upstream-like JSON string payload for invalid matcher errors (`400`)
  - `GET|DELETE /api/v2/silence/{id}` now return `422` + `{code,message}` for invalid UUID path values and `404` with empty body for unknown valid UUID (closer to upstream runtime behavior)
  - `GET /api/v2/silences` and `GET /api/v2/silence/{id}` now always include `matchers[].isRegex` (including `false`)
  - Added upstream parity regression coverage for reload/debug/static compatibility
- **Runtime Config API Baseline** - Added minimal config read/write path in active runtime (2026-02-26)
  - Added `GET /api/v2/config` (`format=json` default, `format=yaml`)
  - Added `POST /api/v2/config` (payload validation, atomic file write, runtime apply of inhibition/receivers)
  - Added `GET /api/v2/config/status` (last apply/reload result + source + timestamp + error + runtime counters)
  - Added `GET /api/v2/config/history` (newest-first runtime apply timeline with `limit` and config hash)
  - Added `POST /api/v2/config/rollback` (rollback to previous successful runtime revision; `409` when no previous revision exists)
  - Extended rollback with target hash selection: `POST /api/v2/config/rollback?configHash=<sha256>` (`400` invalid hash, `404` unknown hash, `409` when target already active)
  - Extended config history with filters: `GET /api/v2/config/history?status=ok|failed&source=<...>` for targeted audit and rollback prep
  - Added `GET /api/v2/config/revisions` (unique successful revision catalog with `isCurrent` for rollback target selection)
  - Added `DELETE /api/v2/config/revisions/prune?keep=<n>` to trim stale revision targets while keeping current active revision
  - Added non-mutating preview mode: `dryRun=true` for `POST /api/v2/config/rollback` and `DELETE /api/v2/config/revisions/prune`
  - `POST /api/v2/config` returns `400` for invalid payload, `413` for oversized payload, `405` for unsupported methods
  - Added Phase0 contract coverage for route inventory, format handling, method contracts and runtime-apply semantics

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
