# Changelog

## [Unreleased]

### Added
- App-level `PodDisruptionBudget` (`templates/poddisruptionbudget.yaml` + `podDisruptionBudget.*` values, disabled by default) — previously only `postgresql.podDisruptionBudget` existed; the app `Deployment` itself had none despite `autoscaling`/pod anti-affinity assuming HA.
- `values.yaml` gained real value-shape homes for the alertmanager-parity config surface: `publishing.queue.deliveryConfirmationTimeout`, `publishing.templates.enabled`, `grouping.*`, `storage.*`, `silencing.*`, wired into `templates/configmap.yaml`.
- `configReloader.*` values shape (disabled, no template yet) — placeholder for a parallel track's sidecar.

### Fixed
- `templates/redis-statefulset.yaml`: `replicas: {{ .Values.valkey.replicas | default 1 }}` silently coerced an explicit `valkey.replicas: 0` back to `1` (Helm/sprig `default` treats `0` as empty). Now `{{ .Values.valkey.replicas | int }}` — `replicas: 0` is the documented way to disable this chart's own Redis/Valkey pod when pointing `cache.*` at an external Redis-compatible service.

### Changed
- `values-production.yaml` rewritten after auditing every key against the app's actual config surface and the chart's real template capabilities (not just BACKLOG's wishlist):
  - Removed `postgresql.cluster.*` (rendered by no template — a 3-instance Postgres "cluster" this chart cannot build) in favor of the real `postgresql.replicas`/`postgresql.config.*` keys, with the single-primary-only limitation documented inline.
  - Removed the dead `dragonfly.*` block (no `dragonfly-*.yaml` template exists) in favor of pointing the already-wired `cache.host`/`cache.port`/`cache.auth` at an external DragonflyDB service, plus `valkey.replicas: 0` to stop deploying the chart's own unused Redis.
  - Added an explicit `postgresql.password: ""` override that shadows the base chart's weak literal default (`values.yaml` still ships it — this file never carried its own key, so it silently inherited that default before; corrected description, was previously misstated as "removed from this file"). Empty fails obviously (Postgres itself refuses to start on an empty password) rather than being supplied at all — must be set via `--set`/CI/`existingSecret`+ESO before install.
  - Enabled `postgresql.networkPolicy.enabled` and both PodDisruptionBudgets for production.

### Fixed (fix round)
- `templates/secret.yaml`'s `redis-password` had a `default (randAlphaNum 32)` fallback for `cache.auth.password` — unlike `postgresql.password`'s fallback (safe: the same StatefulSet reads the same generated value back), this one is read by the app alone against a cache the chart doesn't own (an external DragonflyDB), so a generated fallback silently bakes in a value that can't match the real credential — a well-formed but wrong Secret, not an obvious failure. Replaced with a `required` guard: `cache.auth.enabled: true` with an empty `cache.auth.password` now fails `helm template`/`helm install` outright instead of rendering successfully.

### Known gaps (see `docs/06-planning/TECH-DEBT.md`)
- `valkey.enabled`/`cache.enabled` still don't gate whether `redis-statefulset.yaml` deploys (only `profile: standard` does); `valkey.replicas: 0` is a workaround, not a fix.
- `postgresql.existingSecret` is honored by `templates/secret.yaml` but ignored by `templates/postgresql-secret.yaml`/`amp.postgresql.secretName` — has no effect on the credential actually used by the app and the StatefulSet.
- Base `values.yaml`'s hardcoded weak `postgresql.password` default is unchanged (out of scope for the production-file audit) but now inconsistent with the rationale above — ledgered, not fixed.

## [1.1.0] - 2024-08-27

### Added
- **LLM Integration Support**
  - GPT-4 powered alert classification
  - Three enrichment modes: transparent, transparent_with_recommendations, enriched
  - LLM API key configuration via secrets or external secrets
  - LLM proxy URL configuration (https://llm-proxy.b2broker.tech)
  - LLM timeout and retry configuration

### Enhanced
- **Deployment Configuration**
  - Updated deployment template with LLM environment variables
  - Added LLM secret template support
  - Enhanced configmap with LLM configuration
  - Updated secret template to include LLM API keys

### Configuration
- **New LLM Settings**
  - `llm.enabled` - Enable/disable LLM integration
  - `llm.apiKey` - LLM API key (set via secret in production)
  - `llm.proxyUrl` - LLM proxy URL
  - `llm.model` - LLM model (default: openai/gpt-4o)
  - `llm.timeout` - Request timeout (default: 30s)
  - `llm.maxRetries` - Retry attempts (default: 3)
  - `llm.retryDelay` - Retry delay (default: 1.0s)
  - `llm.cacheTtl` - Cache TTL (default: 3600s)
  - `llm.batchSize` - Batch size (default: 10)

### Documentation
- **Updated README.md**
  - Added LLM integration examples
  - Updated installation instructions
  - Added LLM configuration documentation
  - Enhanced troubleshooting section

### Files Added
- `values-production.yaml` - Production configuration with LLM
- `values-dev.yaml` - Development configuration with LLM
- `DEPLOYMENT.md` - Deployment guide with LLM integration
- `CHANGELOG.md` - This changelog

### Breaking Changes
- None

### Migration Guide
- For existing deployments, add LLM configuration to your values file:
  ```yaml
  llm:
    enabled: true
    apiKey: "your-api-key"
    proxyUrl: "https://llm-proxy.b2broker.tech"
    model: "openai/gpt-4o"
  ```

## [1.0.0] - 2024-08-20

### Initial Release
- Basic Alert History Service
- PostgreSQL and SQLite support
- Redis/DragonflyDB caching
- Prometheus metrics
- Horizontal scaling support
- Basic webhook processing
