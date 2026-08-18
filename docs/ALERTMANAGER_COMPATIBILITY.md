# Alertmanager API Compatibility Matrix

**Date**: 2026-03-09
**Status**: 🟢 **CONTROLLED REPLACEMENT + RESTORED OPS APIs**
**Alertmanager Version**: v0.31.1 (API v2)
**Alertmanager++ Version**: v0.0.1

---

## Executive Summary

Current active runtime compatibility remains controlled, but key operational Alertmanager endpoints are now restored.

Source of truth:
- `go-app/cmd/server/main.go`
- `go-app/internal/application/router.go`

Current active replacement slice:
- `POST /api/v2/alerts`
- `GET /api/v2/alerts`
- `GET /api/v2/alerts/groups` (restored)
- `GET /api/v2/silences`
- `POST /api/v2/silences`
- `GET /api/v2/silence/{id}`
- `DELETE /api/v2/silence/{id}`
- `GET /api/v2/status` (restored)
- `GET /api/v2/receivers` (restored)
- `POST /-/reload` (restored)
- `/health`, `/healthz`, `/ready`, `/readyz`, `/-/healthy`, `/-/ready`, `/metrics`
- real publishing path with explicit `metrics-only` fallback

---

## Current Active Runtime Surface

| Endpoint | Alertmanager | Alertmanager++ today | Status | Notes |
|----------|--------------|----------------------|--------|-------|
| `GET /api/v2/alerts` | ✅ | ✅ | 🟢 | Active current route; basic filtering support |
| `POST /api/v2/alerts` | ✅ | ✅ | 🟢 | Active current ingest path wired through `AlertProcessor` |
| `GET /api/v2/alerts/groups` | ✅ | ✅ | 🟢 | **Restored**; supports `group_by` query parameter |
| `GET /api/v2/silences` | ✅ | ✅ | 🟡 | Active current route for silence listing |
| `POST /api/v2/silences` | ✅ | ✅ | 🟡 | Active current route for create/update |
| `GET /api/v2/silence/{id}` | ✅ | ✅ | 🟢 | Active current route |
| `DELETE /api/v2/silence/{id}` | ✅ | ✅ | 🟢 | Active current route |
| `GET /api/v2/status` | ✅ | ✅ | 🟢 | **Restored**; returns YAML config, version, and uptime |
| `GET /api/v2/receivers` | ✅ | ✅ | 🟢 | **Restored**; returns list of receivers from config |
| `POST /-/reload` | ✅ | ✅ | 🟢 | **Restored**; triggers hot configuration reload |
| `GET /health`, `GET /healthz`, `GET /ready`, `GET /readyz` | N/A | ✅ | 🟢 | Active current state-aware health/readiness routes |
| `GET /-/healthy`, `GET /-/ready` | ✅ | ✅ | 🟢 | Active current Alertmanager-style liveness/readiness routes |
| `GET /metrics` | ✅ | ✅ | 🟢 | Active current metrics route |

---

## Not Current Active Runtime Surface

The endpoints below are **not** part of the current guaranteed replacement slice:

| Endpoint / Surface | Current status | Notes |
|--------------------|----------------|-------|
| `POST /api/v1/alerts` | Out of current scope | Deprecated v1 alias |
| `GET/POST /api/v2/config*` | Backlog | Full config management API |
| `GET /history*` | Backlog | Extended alert history API |
| `GET/POST /api/v2/inhibition/*` | Backlog | Full inhibition rules API |
| `GET /api/v2/classification/*` | Backlog | ML/LLM specific classification API |
| wider dashboard/UI surface | Partial | Work in progress |

---

## Method Matrix For Current Slice

| Endpoint | Allowed methods in current slice | Notes |
|----------|----------------------------------|-------|
| `/api/v2/alerts` | `GET`, `POST` | Current active runtime route |
| `/api/v2/alerts/groups` | `GET` | Restored |
| `/api/v2/silences` | `GET`, `POST` | Current active runtime route |
| `/api/v2/silence/{id}` | `GET`, `DELETE` | Current active runtime route |
| `/api/v2/status` | `GET` | Restored |
| `/api/v2/receivers` | `GET` | Restored |
| `/-/reload` | `POST` | Restored |
| `/health`, `/healthz`, `/ready`, `/readyz` | `GET` | Current active runtime route |
| `/-/healthy`, `/-/ready` | `GET` | Current active runtime route |
| `/metrics` | `GET` | Current active runtime route |

---

## Receiver Integration Compatibility Matrix

This matrix covers `receivers[].*_configs` notification integrations, as
distinct from the HTTP API surface above. "Config accepted" means
`internal/alertmanager/config.Receiver` (parsed by `pkg/configvalidator`)
has a matching field; "runtime wired" means
`internal/infrastructure/routing.Receiver` and the publishing factory
(`internal/infrastructure/publishing.PublisherFactory.CreatePublisher*`)
actually dispatch notifications for it. A receiver can validate cleanly
and still send zero notifications if it is config-accepted but not
runtime-wired — see `pkg/configvalidator/doc.go` for the authoritative
statement of this divergence, which this table mirrors. Note also that
`pkg/configvalidator` itself is not yet wired into the running server's
`/-/reload` path (Task 5.4, pending) — "config accepted" below is a
static-validation claim only, not something the live server enforces today.

| Integration | Config accepted | Runtime wired | Status | Notes |
|-------------|:---:|:---:|--------|-------|
| `webhook_configs` | ✅ | ✅ | 🟢 Supported | Generic HTTP POST; also the delivery path for Discord/Teams (see below) |
| `email_configs` | ✅ | ✅ | 🟢 Supported | SMTP, enhanced publisher |
| `pagerduty_configs` | ✅ | ✅ | 🟢 Supported | Events API v2 |
| `slack_configs` | ✅ | ✅ | 🟢 Supported | Incoming Webhooks, message threading cache |
| `telegram_configs` | ❌ | ✅ | 🟠 Runtime-wired, not config-accepted | Inverse divergence: `internal/alertmanager/config.Receiver` has no `TelegramConfigs` field and `hasAnyIntegration()`/E024 doesn't check it, so a telegram-only receiver fails configvalidator (E024 "no integrations configured") even though `internal/infrastructure/routing.Receiver` and the publisher fully support it at runtime. See `pkg/configvalidator/doc.go`; fixing the validator is pending Task 5.4 |
| Rootly (`alertmanager`/`rootly` target type) | N/A (AMP-native target, not an upstream receiver type) | ✅ | 🟢 Supported | AMP-specific addition, not part of upstream Alertmanager |
| `opsgenie_configs` | ✅ | ❌ | 🟡 Config-accepted, not wired | Validates (E126-E129); no `OpsGenieConfigs` field on the runtime receiver and no publisher target type — zero notifications sent |
| `victorops_configs` | ✅ | ❌ | 🟡 Config-accepted, not wired | Validates (E130-E134); same gap as OpsGenie. Deferred "по потребности" (on demand) — build only if a concrete need arises |
| `wechat_configs` | ✅ | ❌ | 🟡 Config-accepted, not wired | Validates (E138-E141); same gap. Deferred "по потребности" |
| Discord (native receiver type) | ❌ | ❌ | 🟢 Effectively supported via webhook | No dedicated `discord_configs`; ships as a built-in Discord-embed template (`internal/notification/template/defaults/webhook.go`) rendered through `webhook_configs`. Not a blocker |
| Microsoft Teams (native receiver type) | ❌ | ❌ | 🟢 Effectively supported via webhook | Same pattern: built-in Adaptive Card template via `webhook_configs`, not a dedicated receiver type. Not a blocker |
| Pushover | ❌ | ❌ | 🔴 Unsupported | No config type, no template, no publisher anywhere in the codebase |
| AWS SNS | ❌ | ❌ | 🔴 Unsupported | No config type, no template, no publisher anywhere in the codebase |
| Webex | ❌ | ❌ | 🔴 Unsupported | No config type, no template, no publisher anywhere in the codebase |

**Decision (Task 7.2)**: VictorOps and WeChat publishers are not built in
this task. Their config types and validators (Phase 5) already exist and
are kept as-is; wiring a runtime publisher is deferred until a concrete
need appears ("по потребности"). Pushover/SNS/Webex are fixed as
unsupported and are not a blocker, since most third-party chat/incident
tools (including Discord and Teams) integrate through the generic
`webhook_configs` receiver instead of a bespoke native integration.

---

## Replacement Guidance

AMP now provides a stronger controlled-replacement foundation, covering core ingest, grouped queries, silence CRUD, status/receivers/reload operations, and runtime probes.

Treat AMP as a replacement if you rely on:
- standard alert ingestion from Prometheus
- standard silence management
- Grafana dashboard integration (via `/api/v2/alerts/groups`)
- standard health/readiness monitoring
- hot configuration reload

---

**Maintainer**: Vitalii Semenov
**License**: AGPL 3.0
