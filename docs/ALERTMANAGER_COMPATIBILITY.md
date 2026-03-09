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
