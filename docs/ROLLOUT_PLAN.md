# Controlled Rollout Plan

**Audience**: Ops/SRE migrating live alerting traffic from Alertmanager
to AMP, or rolling out a new AMP release to a fleet already on AMP.
**Companion docs**: `docs/ROLLBACK_RUNBOOK.md` (what to do when a stage
goes wrong), `docs/ALERTMANAGER_COMPATIBILITY.md` (field-level parity
detail), `deploy/e2e-ha/` and `deploy/smoke/` (the two automated checks
this plan's gates point back to).

---

## Stages

Each stage is a strict superset of the previous one's config; don't skip a
stage on a real migration (a from-scratch AMP release rollout can start at
stage 3 or 4 if there's no legacy Alertmanager in the picture).

### Stage 1 — Lite, single-node, shadow

**Setup**: `profile: lite`, no Postgres/Redis. Prometheus's `alerting:`
block lists **both** the existing Alertmanager and this AMP instance —
Prometheus natively fans every alert notification out to every configured
Alertmanager target, so this needs no AMP-side feature, just a Prometheus
config change:

```yaml
# prometheus.yml
alerting:
  alertmanagers:
    - static_configs:
        - targets: ["alertmanager.existing:9093"]   # unchanged, stays authoritative
    - static_configs:
        - targets: ["amp-shadow.new:8080"]           # new, shadow only
```

AMP's `receivers:` route everything to a dead-end receiver — either a
literal blackhole (`- name: 'null'`, no integrations — valid since
`AMP-PARITY-WAVE6-EPIC`) or a `webhook_configs` target pointed at a
sink nobody watches (a test channel, a `/dev/null`-style HTTP endpoint).
Either way: **real Prometheus alert volume, zero real notifications.**

**Entry criteria**: `deploy/smoke/run.sh` green against the target build.

**Exit criteria** (run for at least one full business cycle — a week
covering a normal on-call rotation, not just a quiet afternoon):
- `alert_history_publishing_blackhole_drops_total` (or the dead-end
  receiver's own delivery count) tracks Prometheus's alert firing rate —
  confirms alerts are actually arriving and routing, not silently
  dropped before reaching the receiver.
- No unexpected panics/restarts (`kubectl get pods` restart count flat).
- `GET /api/v2/alerts/groups` output shape spot-checked against the old
  Alertmanager's `amtool alert query` for the same time window — group
  membership and label sets should match for any route with an explicit
  `group_by`.

**Rollback trigger**: any crash loop, or grouping shape mismatch against
the legacy Alertmanager for routes that DO set `group_by` explicitly (a
mismatch there is a real bug, not the known implicit-`group_by` divergence
— see the compatibility doc).

---

### Stage 2 — Lite, single-node, live for one low-criticality route

**Setup**: same `profile: lite`, same shadow dual-run, but ONE route (pick
something low-blast-radius — a noisy-but-non-paging warning-severity
route) gets a real receiver instead of the dead end. Everything else stays
shadowed.

**Entry criteria**: Stage 1 exit criteria held for the full observation
window, plus an amtool parity spot-check (Section "amtool spot-checks"
below) passing against this instance.

**Exit criteria**:
- Real deliveries for the live route confirmed by the receiving side
  (Slack channel/webhook sink actually shows the messages) — cross-check
  against `alert_history_publishing_jobs_processed_total{target,status}`
  for that target trending with the alert volume, not flat.
- `alert_history_publishing_resolved_suppressed_total` stays at the
  expected baseline (0 unless the route uses `send_resolved: false`
  somewhere) — a rising count means resolved notifications are being
  dropped unexpectedly.
- `alert_history_publishing_template_fallbacks_total{integration,reason}`
  stays flat (or absent) if `publishing.templates.enabled` — a rising
  count means templates are failing and silently falling back
  (`exec_error`/`timeout`/`output_cap`/`not_defined` — check `reason`).
- No `alert_history_storage_reconcile_failures_total` increments (would
  indicate the grouping storage backend is unhealthy even at this small
  scale).

**Rollback trigger**: any missed real notification for the live route, or
a duplicate notification not explained by an expected retry.

---

### Stage 3 — HA pair, Redis + Postgres

**Setup**: `profile: standard`, real Redis + Postgres, 2 replicas. This is
exactly `deploy/e2e-ha/`'s topology — run that suite against the release
candidate before promoting live traffic to it.

**Entry criteria**: `deploy/e2e-ha/run.sh` green (all 6 steps: cluster
convergence, single-publish-per-group, cross-replica concurrent fire
arbitration, orphan adoption after a replica dies, failover delivery,
nflog dedup poisoning guard).

**Exit criteria** (widen the live-route set from Stage 2's one route to a
representative slice — several severities, at least one route that
crosses receivers, one with `group_by` unset if that's a real production
shape):
- `cluster.status == "ready"` with `peers` count == replica count, stable
  over the observation window (no flapping).
- `alert_history_storage_backend_active{backend}` shows exactly one
  backend active at `1` per replica, consistently (a flapping split
  between memory/postgres fallback indicates a Redis/Postgres reachability
  problem worth chasing before widening further).
- `alert_history_publishing_dlq_size{target}` and
  `..._circuit_breaker_trips_total{target}` flat at/near zero for every
  live target — rising DLQ size or breaker trips under real load is the
  earliest signal of an unhealthy downstream target, not an AMP bug.
- Kill one replica deliberately during the observation window (chaos
  drill, not an accident) and confirm the survivor adopts orphaned work —
  same assertion `deploy/e2e-ha/run.sh` step 5 makes, now against real
  traffic instead of synthetic alerts.

**Rollback trigger**: any duplicate/missed delivery not explained by the
same class of timing race `deploy/e2e-ha` already covers, or a
reconciliation-failure rate that doesn't return to zero after the
underlying Redis/Postgres issue clears.

---

### Stage 4 — Full cutover

**Setup**: every route's receiver is real; Prometheus's `alerting:` block
drops the legacy Alertmanager target (or keeps it as a cold standby,
operator's call). Replica count sized for real load, not the 2-replica
smoke topology.

**Entry criteria**: Stage 3 held for a full observation window with the
representative route slice, zero unresolved rollback triggers.

**Exit criteria**: same metric watch-list as Stage 3, now over the full
route set, for a full business cycle before removing the legacy
Alertmanager as a standby.

---

## Metric watch-list (all stages)

All under the `alert_history_` Prometheus namespace (`pkg/metrics/v2`,
`pkg/metrics/metrics.go`) — not literally `amp_`-prefixed; that's shorthand
this plan's brief used and one of the metric doc-comments in the codebase
uses the same shorthand while pointing at the real name, so it's worth
being explicit here:

| Metric | What it tells you |
|---|---|
| `alert_history_publishing_blackhole_drops_total{receiver}` | Notifications intentionally dropped (no integrations on that receiver). Expected to track Stage 1's shadow volume; unexpected elsewhere. |
| `alert_history_publishing_resolved_suppressed_total{receiver}` | Resolved notifications suppressed by `send_resolved: false`. Should match your config's intent, not drift. |
| `alert_history_publishing_template_renders_total{integration,outcome}` | Template render attempts and outcome. |
| `alert_history_publishing_template_fallbacks_total{integration,reason}` | Template failures falling back to fixed formatters — `reason` ∈ `exec_error`/`timeout`/`output_cap`/`not_defined`. Non-zero and rising = investigate before widening rollout. |
| `alert_history_publishing_template_abandoned_executions` / `..._template_in_flight_abandoned_executions` | Template executions that blew the render timeout. |
| `alert_history_storage_reconcile_failures_total` | Grouping storage write-through reconciliation failing while the primary backend is reachable — distinguishes "Redis is down" from "Redis is up but writes are failing." |
| `alert_history_storage_backend_active{backend}` | Which grouping storage backend is live (1) vs. standby (0) — should be stable, not flapping. |
| `alert_history_publishing_jobs_processed_total{target,status}` | Closest proxy to "delivery confirmations" — there is no single dedicated confirmation-count metric; watch this rising in step with alert volume per live target instead. |
| `alert_history_publishing_retry_attempts_total{target,error_type}` | Retry volume by target/error class — an early signal of a flaky downstream target. |
| `alert_history_publishing_circuit_breaker_state{target}` / `..._circuit_breaker_trips_total{target}` | Per-target breaker health; a trip means that target is being protected from, not delivered to. |
| `alert_history_publishing_dlq_size{target}` | Dead-letter queue depth per target — should stay near zero. |

---

## amtool spot-checks

Real upstream `amtool` (bundled in `quay.io/prometheus/alertmanager`,
`--entrypoint amtool`) against the AMP instance at each stage:

```bash
AMTOOL_IMG=quay.io/prometheus/alertmanager:v0.34.0
AM_URL=http://<amp-host>:8080

docker run --rm --network <net> --entrypoint amtool "$AMTOOL_IMG" \
  --alertmanager.url="$AM_URL" alert query

docker run --rm --network <net> --entrypoint amtool "$AMTOOL_IMG" \
  --alertmanager.url="$AM_URL" config show

docker run --rm --network <net> --entrypoint amtool "$AMTOOL_IMG" \
  --alertmanager.url="$AM_URL" silence add alertname=SpotCheck \
  --comment="rollout spot-check" --author="rollout-plan"

docker run --rm --network <net> --entrypoint amtool "$AMTOOL_IMG" \
  --alertmanager.url="$AM_URL" silence query
```

All four are exercised by `scripts/release-gate.sh`'s `amtool-compat`
step against `deploy/smoke/`'s single-node stack, and verified working
during this rollout package's own testing — `alert query`, `config show`,
`silence add`, and `silence query` all round-trip correctly against a
live AMP instance with real amtool.

---

## Silence migration (legacy Alertmanager → AMP)

**What AMP's silence API accepts**: `POST /api/v2/silences` takes exactly
one silence per request (`core.SilenceInput` — `matchers`, `startsAt`,
`endsAt`, `createdBy`, `comment`), the same upstream Alertmanager API v2
shape. **There is no bulk-import endpoint** — this is not an AMP gap,
upstream Alertmanager's own API doesn't have one either.

**What does work, verified end-to-end against a live AMP instance**:

```bash
# Export every silence from the OLD Alertmanager as JSON
docker run --rm --entrypoint amtool "$AMTOOL_IMG" \
  --alertmanager.url=http://legacy-alertmanager:9093 \
  silence query -o json > silences.json

# Re-create them on AMP — amtool's own import command loops over the
# array and POSTs each one individually; no custom tooling needed.
cat silences.json | docker run --rm -i --entrypoint amtool "$AMTOOL_IMG" \
  --alertmanager.url=http://amp:8080 silence import
```

This is a genuine, tested round-trip (`amtool silence query -o json` →
`amtool silence import`, both against AMP directly during this package's
testing) — not a theoretical compatibility claim. Be honest with whoever
runs this about the manual parts:

- It's a **one-time CLI operation**, not a live dual-write — silences
  created on the legacy Alertmanager *after* the export won't appear on
  AMP until re-run.
- Already-**expired** silences in the export (`endsAt` in the past) may
  be rejected on import — the same validation upstream Alertmanager
  itself applies (`endsAt` must be strictly after `startsAt` and not
  already in the past), not an AMP-specific restriction.
- Imported silences land with an ID amtool preserves from the export;
  re-running the same import is safe to repeat (verified: importing the
  same export twice against the same instance did not create a
  duplicate), but that's a courtesy, not a guarantee to lean on across
  arbitrary time gaps if the silence has since expired and been
  garbage-collected on the source side.

**When to do it**: right before promoting a route to real traffic in
Stage 2 or 3 — no earlier (Stage 1's shadow stage has no real receivers
that a stale silence would affect) and no later than the cutover in
Stage 4 (an on-call engineer's active silence dropping on cutover is
exactly the kind of thing that pages someone at 3am for a known issue).

---

## Rollback trigger thresholds (summary)

Any of these, at any stage, is "stop and go read
`docs/ROLLBACK_RUNBOOK.md`," not "wait and see":

- A missed real notification with no explanation in the retry/circuit
  breaker metrics.
- A duplicate notification not explained by the known, bounded rollback
  key-shape gap (only relevant when actually rolling back, see the
  runbook) or by an in-flight cross-replica race already covered by
  `deploy/e2e-ha`.
- `alert_history_storage_reconcile_failures_total` not returning to zero
  after the underlying Redis/Postgres issue is confirmed cleared.
- Any config that fails to load/reload on the new version but loaded
  fine on the old one — check `docs/ROLLBACK_RUNBOOK.md` section 5's
  breaking-changes list before assuming it's a new bug.
- `cluster.status` flapping between `ready` and degraded outside of a
  deliberate chaos drill.
