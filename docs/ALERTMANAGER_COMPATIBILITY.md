# Alertmanager API Compatibility Matrix

**Date**: 2026-03-08
**Status**: 🟡 **CONTROLLED REPLACEMENT SLICE ACTIVE**
**Alertmanager Version**: v0.31.1 (API v2)
**Alertmanager++ Version**: v0.0.1

---

## Executive Summary

Current active runtime compatibility is intentionally narrow.

Source of truth:

- `go-app/cmd/server/main.go`
- `go-app/internal/application/router.go`

Current active replacement slice:

- `POST /api/v2/alerts`
- `GET /api/v2/alerts`
- `GET /api/v2/silences`
- `POST /api/v2/silences`
- `GET /api/v2/silence/{id}`
- `DELETE /api/v2/silence/{id}`
- `/health`, `/ready`, `/-/healthy`, `/-/ready`, `/metrics`
- real publishing path with explicit `metrics-only` fallback

Wider Alertmanager parity remains backlog or future restoration work.

---

## Current Active Runtime Surface

| Endpoint | Alertmanager | Alertmanager++ today | Status | Notes |
|----------|--------------|----------------------|--------|-------|
| `GET /api/v2/alerts` | ✅ | ✅ | 🟡 | Active current route; treat advanced matcher/routing parity as partial and validate explicitly |
| `POST /api/v2/alerts` | ✅ | ✅ | 🟡 | Active current ingest path wired through `AlertProcessor` and the real publishing runtime |
| `GET /api/v2/silences` | ✅ | ✅ | 🟡 | Active current route for silence listing |
| `POST /api/v2/silences` | ✅ | ✅ | 🟡 | Active current route for create/update |
| `GET /api/v2/silence/{id}` | ✅ | ✅ | 🟢 | Active current route |
| `DELETE /api/v2/silence/{id}` | ✅ | ✅ | 🟢 | Active current route |
| `GET /health`, `GET /ready` | N/A | ✅ | 🟢 | Active current health/readiness routes |
| `GET /-/healthy`, `GET /-/ready` | ✅ | ✅ | 🟢 | Active current Alertmanager-style readiness routes |
| `GET /metrics` | ✅ | ✅ | 🟢 | Active current metrics route |

---

## Not Current Active Runtime Surface

The endpoints below are **not** part of the current guaranteed replacement slice, even if older docs/tests/historical code discuss them:

| Endpoint / Surface | Current status | Notes |
|--------------------|----------------|-------|
| `GET /api/v2/status` | Backlog / future parity | Do not treat as active current route |
| `GET /api/v2/receivers` | Backlog / future parity | Do not treat as active current route |
| `GET /api/v2/alerts/groups` | Backlog / future parity | Do not treat as active current route |
| `POST /-/reload` | Backlog / future parity | Not part of current active bootstrap |
| `POST /api/v1/alerts` | Out of current scope | Deprecated v1 alias is not part of the current replacement claim |
| `GET/POST /api/v2/config*` | Historical / future parity | Not a current active-runtime guarantee |
| `GET /history*` | Historical / future parity | Not a current active-runtime guarantee |
| `GET/POST /api/v2/inhibition/*` | Historical / future parity | Not a current active-runtime guarantee |
| `GET /api/v2/classification/*` | Historical / future parity | Not a current active-runtime guarantee |
| wider dashboard/UI surface | Partial | Validate explicitly; placeholders still exist |

---

## Method Matrix For Current Slice

| Endpoint | Allowed methods in current slice | Notes |
|----------|----------------------------------|-------|
| `/api/v2/alerts` | `GET`, `POST` | Current active runtime route |
| `/api/v2/silences` | `GET`, `POST` | Current active runtime route |
| `/api/v2/silence/{id}` | `GET`, `DELETE` | Current active runtime route |
| `/health`, `/ready` | `GET` | Current active runtime route |
| `/-/healthy`, `/-/ready` | `GET` | Current active runtime route |
| `/metrics` | `GET` | Current active runtime route |

Historical wide-surface method expectations are no longer the default current-runtime gate and should be treated as future parity work.

---

## Replacement Guidance

Treat AMP today as a controlled replacement only if you have explicitly validated:

- sender compatibility for `/api/v2/alerts`
- silence CRUD behavior you rely on
- health/readiness/metrics integration
- target discovery and outbound publishing behavior

Do not assume unchanged parity for:

- `amtool` commands beyond the covered slice
- runtime config APIs
- route grouping and receiver discovery APIs
- historical dashboard/config/history surfaces

---

## FAQ

### Is Alertmanager++ 100% compatible with Alertmanager?

No. Current active runtime should be treated as a controlled replacement slice, not as a 100% compatible Alertmanager replacement.

### What is the biggest current difference?

Scope. AMP's active runtime is narrower than Alertmanager and wider parity remains explicit follow-up work.

### Can I use existing Prometheus configuration?

Usually yes, if the only required change is the Alertmanager target URL and your workflow stays within the current active slice.

### Can I use existing `alertmanager.yml`?

Partially. Existing routing configuration remains a compatibility target, but you should validate it against the current mounted runtime surface instead of assuming every historical config/runtime API in older docs is active today.

### Does `amtool` work?

Only for the subset you explicitly validate against the current active slice. Do not assume broad CLI parity from this document.

### Can I migrate back to Alertmanager?

Yes, but treat rollback/export/import as an explicit operational procedure to validate, not as an assumed no-risk compatibility guarantee.

### Is there commercial support?

The OSS edition is AGPL-licensed. This document does not define any commercial support contract.

---

## Compatibility Certification

**Status**: historical wide-surface certification withdrawn pending re-certification of the current active runtime.

Current recommendation:

- treat AMP as a controlled replacement slice
- use explicit smoke validation for your covered endpoints
- do not position current runtime as a verified full Alertmanager drop-in replacement

---

## Additional Resources

- [MIGRATION_QUICK_START.md](MIGRATION_QUICK_START.md)
- [MIGRATION_COMPARISON.md](MIGRATION_COMPARISON.md)
- [CONFIGURATION_GUIDE.md](CONFIGURATION_GUIDE.md)
- [helm/amp/README.md](../helm/amp/README.md)
- [docs/06-planning/ALERTMANAGER-REPLACEMENT-GAP-ANALYSIS.md](06-planning/ALERTMANAGER-REPLACEMENT-GAP-ANALYSIS.md)

---

**Maintainer**: Vitalii Semenov
**License**: AGPL 3.0
