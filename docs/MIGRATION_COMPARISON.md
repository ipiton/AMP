# Alertmanager vs Alertmanager++ - Controlled Replacement Comparison

**Last Updated**: 2026-03-09
**Alertmanager Version**: v0.27+
**Alertmanager++ Version**: v0.0.1
**Status**: AMP should currently be evaluated as a **controlled replacement slice**, not as a verified full Alertmanager drop-in replacement.

---

## Current Recommendation

Treat AMP today as a pilot-oriented runtime for a controlled, explicit surface:

- alert ingest via `POST /api/v2/alerts`
- alert query via `GET /api/v2/alerts`
- grouped alert queries via `GET /api/v2/alerts/groups`
- operational APIs via `GET /api/v2/status`, `GET /api/v2/receivers`, `POST /-/reload`
- silence CRUD via `GET/POST /api/v2/silences` and `GET/DELETE /api/v2/silence/{id}`
- state-aware liveness/readiness probes and `/metrics`
- real publishing path with explicit `metrics-only` fallback

Anything wider than this should be treated as future parity or environment-specific validation work, not as an assumed baseline.

---

## Quick Comparison

| Category | Alertmanager | Alertmanager++ today | Recommendation |
|----------|--------------|----------------------|----------------|
| Runtime surface | Broad established Alertmanager API/runtime | Controlled active replacement surface with restored operational APIs | Prefer Alertmanager if you need wide parity today |
| Alert ingest | Mature and battle-tested | Active and wired through real publishing path | AMP is viable for controlled ingest/publish pilots |
| Silence CRUD | Mature and battle-tested | Active and covered in current runtime | Suitable for controlled replacement scenarios |
| Grafana / grouped alerts | Available | Active via `GET /api/v2/alerts/groups` | Validate your exact dashboard and filter assumptions |
| Status / receivers / reload | Available | Active in current runtime | Suitable for operational smoke checks and controlled automation |
| Publishing | Native receiver delivery | Real queue/coordinator-based publishing path with explicit degraded mode | Validate your target set and fallback expectations |
| Wider parity (`config/history`, inhibition, classification, broader dashboard APIs) | Available | Not current active runtime guarantee | Treat as backlog/future work |
| Benchmarks / resource claims | Well-known operational profile | Top-level comparative numbers intentionally withheld pending reproducible current benchmarks | Do not make sizing assumptions from old marketing numbers |

---

## What AMP Can Replace Today

AMP is a realistic candidate when you want to pilot a controlled replacement path and you can keep the integration scope narrow:

- Prometheus or compatible senders post alerts to `/api/v2/alerts`
- Grafana- or amtool-adjacent flows rely on `/api/v2/alerts/groups`, `/api/v2/status`, `/api/v2/receivers`, or `/-/reload`
- operators rely on current silence CRUD endpoints
- liveness/readiness JSON probes and `/metrics` are enough for runtime checks
- outbound delivery is validated through the current publishing path
- your team accepts that broader Alertmanager parity is not yet the active contract

---

## Where AMP Still Differs

These gaps are the main reason AMP is not yet documented as a general-purpose replacement:

- config/history/inhibition/classification surfaces are not part of the current active replacement guarantee
- dashboard surface is partial
- top-level benchmark and resource claims are intentionally not treated as current verified facts

Source of truth for this comparison:

- `go-app/cmd/server/main.go`
- `go-app/internal/application/router.go`
- `docs/06-planning/DECISIONS.md`
- `docs/06-planning/BUGS.md`

---

## When To Pilot AMP

Use AMP if:

- you want a controlled rollout with explicit smoke validation
- you care about the current publishing path more than full Alertmanager parity
- you can validate your exact sender, status/receivers/groups/reload usage, silence, health, and delivery workflow
- you want to track future parity incrementally instead of assuming it today

---

## When To Stay On Alertmanager

Stay on Alertmanager if:

- you need broad API/runtime parity today without additional validation
- you depend on wider config/history, inhibition, classification, or broader dashboard surfaces as active guarantees
- you need a long-proven operational story without current-scope caveats
- you are choosing purely on performance/resource claims that are not yet backed by a reproducible benchmark report for current `main`

---

## Migration Recommendation

**Recommendation**: use a **pilot / controlled rollout**, not a blanket swap.

Suggested rollout shape:

1. deploy AMP with the repo-local chart `./helm/amp`
2. point a controlled Prometheus/VMAlert sender or environment at AMP
3. validate `/api/v2/alerts`, `/api/v2/alerts/groups`, `/api/v2/status`, `/api/v2/receivers`, `/-/reload`, `/api/v2/silences`, state-aware health/readiness, `/metrics`, and real target delivery
4. keep rollback to Alertmanager straightforward until your covered slice is proven

See:

- [MIGRATION_QUICK_START.md](MIGRATION_QUICK_START.md)
- [ALERTMANAGER_COMPATIBILITY.md](ALERTMANAGER_COMPATIBILITY.md)

---

## Compatibility Guarantee

Alertmanager++ current active runtime guarantees only this controlled replacement slice:

- `POST /api/v2/alerts`
- `GET /api/v2/alerts`
- `GET /api/v2/alerts/groups`
- `GET /api/v2/status`
- `GET /api/v2/receivers`
- `GET /api/v2/silences`
- `POST /api/v2/silences`
- `GET /api/v2/silence/{id}`
- `DELETE /api/v2/silence/{id}`
- `POST /-/reload`
- `/health`, `/healthz`, `/ready`, `/readyz`, `/-/healthy`, `/-/ready`, `/metrics`
- real publishing path with explicit `metrics-only` fallback

Anything beyond this surface should be treated as:

- backlog parity
- historical analysis
- or deployment-specific validation work

---

## Operational Notes

- Helm examples in repo docs use the repository-local chart path `./helm/amp` as the canonical install story.
- Public docs use AGPL-3.0 as the license source of truth.
- Comparative performance/resource numbers are intentionally excluded from this document until a reproducible benchmark report is published for the current branch.
- `/health|/healthz` describe liveness, `/ready|/readyz` describe readiness; optional degraded components may still produce `200` with a degraded JSON body while required dependency failures move readiness to `503`.

---

## Learn More

- [MIGRATION_QUICK_START.md](MIGRATION_QUICK_START.md)
- [ALERTMANAGER_COMPATIBILITY.md](ALERTMANAGER_COMPATIBILITY.md)
- [CONFIGURATION_GUIDE.md](CONFIGURATION_GUIDE.md)
- [helm/amp/README.md](../helm/amp/README.md)

---

**Maintainer**: Vitalii Semenov
**License**: AGPL 3.0
