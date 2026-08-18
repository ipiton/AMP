# Alertmanager vs Alertmanager++ - Migration Comparison

**Last Updated**: 2026-08-18
**Alertmanager Version**: v0.27+
**Alertmanager++ Version**: v0.0.1
**Status**: AMP now implements upstream's core notification *mechanics* (routing tree, grouping/dispatch, notify chain, HA clustering) via branch `feat/alertmanager-parity` — not just a compatible API surface. This is a **mid-to-late-stage parity candidate**, not the earlier "controlled replacement slice" baseline.

**AMP is NOT a config-level drop-in.** Its control plane (routing/grouping/timing/inhibition semantics) is
parity-level, but its data plane is different by design: delivery endpoints come from `amp.receiver`-scoped
Kubernetes Secrets, **not** from `receivers[].*_configs`, which AMP parses and validates but never
auto-provisions. Dropping in an upstream `alertmanager.yml` yields correct routing and **zero deliveries** until
those Secrets exist — with no error, no warning, and a clean startup. Plan that step explicitly; see
[Config Migration](#config-migration) below and `docs/ALERTMANAGER_COMPATIBILITY.md`'s "control plane vs data plane"
section. A short list of other gaps remains open; see below and that document's Known Gaps section. The
`amtool`/Grafana live-audit half of task 7.4 (running separately) is the final acceptance check.

---

## Current Recommendation

Treat AMP as a strong parity candidate for the great majority of standard Alertmanager deployments:

- alert ingest via `POST /api/v2/alerts` (and the `POST /api/v1/alerts` alias) — routed through a real
  `route:`/`receivers:` tree with recursive matching, `continue`, and all 4 matcher operators
- grouping and timing driven per-route (`group_by`/`group_wait`/`group_interval`/`repeat_interval`), not global
  defaults only
- notify chain: Inhibit → Silence → TimeMute (mute/active time intervals) → Dedup, evaluated at send time
- silence CRUD and inhibition, both wired into the live pipeline
- HA/multi-replica deployment: Redis-backed cross-replica dedup, distributed timer liveness, leader-elected silence
  GC, peer heartbeat
- config validation (E-codes/W-warnings) enforced at startup and on `/-/reload`
- operational APIs: `GET /api/v2/status` (with `versionInfo`/`cluster`), `GET /api/v2/receivers`,
  `GET /api/v2/alerts/groups`, `GET /api/v2/inhibitions`
- `--web.route-prefix` for reverse-proxy deployments

What is **different**, not just narrower:
- **Delivery targets are not built from `receivers[].*_configs`.** They come from Kubernetes Secrets annotated
  `amp.receiver: <receiver-name>`. This is the one migration step that fails silently if skipped.
- Receiver names may contain anything except `/` (AMP reserves it as the group-key separator). Upstream has no
  restriction; rename any receiver containing a slash.
- `inhibit_rules[].source_matchers`/`target_matchers` (the `matchers:` list syntax) are **not evaluated** — only the
  `source_match`/`match_re` map form is. Such rules are logged as an `ERROR` at load/reload but do not inhibit.
- `GET /api/v2/status`'s `config.original` is the Alertmanager-shaped, secret-redacted subset of your config, not a
  byte copy of the file.

What's still narrower than upstream: a handful of niche receiver integrations, wire-level webhook batching shape,
and the config-write/`/history` APIs (explicitly out of scope for this task). See the gap list below.

---

## Quick Comparison

| Category | Alertmanager | Alertmanager++ today | Recommendation |
|----------|--------------|----------------------|----------------|
| Routing tree (matchers, `continue`, nesting) | Mature | Implemented — recursive matcher, anchored regex, negative matchers, `matchers:` list syntax | Parity-level; validate your specific route tree shape during the live audit |
| Grouping / dispatch (`group_by`, `group_wait`, `group_interval`, `repeat_interval`) | Mature | Implemented per-route, notify chain ordered Inhibit→Silence→TimeMute→Dedup | Parity-level for semantics; see wire-batching note below |
| Silences | Mature | Full CRUD, cross-replica cache sync | Parity-level |
| Inhibition | Mature | `inhibit_rules:` parsed, cached, wired into notify chain, hot-reloadable | Parity-level |
| Time-based muting (`mute_time_intervals`/`active_time_intervals`) | Mature | Implemented against upstream's own fixture corpus | Parity-level |
| Config validation | Mature | Wired into startup + `/-/reload` when a `route:` section is present | Legacy single-receiver configs still skip this validation path |
| HA / clustering | Gossip-based | Redis-based (nflog, timer liveness, leader election, heartbeat); 2-replica e2e demonstrated via standalone script | Functionally equivalent goal, different mechanism; e2e script not CI-gated |
| Operational API (`status`/`receivers`/`groups`/`reload`) | Available | Available, with upstream query params | Parity-level |
| **Receiver → delivery endpoint provisioning** | Built directly from `receivers[].*_configs` | **Not built from config.** Targets discovered from `amp.receiver`-scoped Kubernetes Secrets; `*_configs` parsed/validated only | **Highest-impact difference.** Create one Secret per endpoint before cutting over, or you route correctly and deliver nothing |
| Receiver integrations (publisher availability) | Full set incl. OpsGenie/VictorOps/WeChat/Pushover/SNS/Webex | webhook/email/PagerDuty/Slack/Telegram/Rootly publishers wired (Telegram's enhanced publisher became runtime-reachable in the final fix wave); Discord/Teams via webhook templates; OpsGenie/VictorOps/WeChat validate-but-not-wired; Pushover/SNS/Webex absent | Check the receiver matrix in `ALERTMANAGER_COMPATIBILITY.md` against your actual receiver list |
| Hot reload trigger | `SIGHUP` + `POST /-/reload` | Both. Routing-only edits are applied (they were silently discarded before the final fix wave) | Parity-level |
| Wire-level webhook payload | One POST per target with a full `alerts` JSON array per group | One POST per `(target × alert)` pair | Different request shape/count; functionally delivers all alerts, but a downstream integration parsing the exact payload shape needs to be checked |
| Config write API / `/history*` | Available | Not implemented | Explicitly out of scope for this task; stays backlog |
| Benchmarks / resource claims | Well-known operational profile | Intentionally withheld pending reproducible current benchmarks | Do not make sizing assumptions from old marketing numbers |

---

## What AMP Can Replace Today

AMP is now a realistic candidate for most standard Alertmanager deployments where:

- Prometheus (or compatible senders) post alerts to `/api/v2/alerts`, and you rely on real routing (multiple
  receivers, `group_by`, per-route timings, `matchers:`) rather than a single flat receiver
- you use silences and inhibition as part of your operational workflow
- you rely on maintenance-window-style muting (`mute_time_intervals`/`active_time_intervals`)
- you run (or plan to run) more than one replica and need HA delivery guarantees
- Grafana- or amtool-adjacent flows rely on `/api/v2/alerts/groups`, `/api/v2/status`, `/api/v2/receivers`, or
  `/-/reload` — with the caveat that `/api/v2/alerts/groups`' receiver field is not routing-tree-aware (see gaps)
- your receiver set is limited to webhook/email/PagerDuty/Slack/Telegram (Discord/Teams work via webhook templates)

---

## Where AMP Still Differs

Honest, code-traceable gaps as of this branch:

1. **`GET /api/v2/alerts/groups` receiver field is hardcoded** to the first configured receiver (or `"default"`),
   not the routing-tree-evaluated receiver for each alert. The real notify pipeline is correct; this read-only
   query endpoint is not. (`internal/application/handlers/alerts.go`)
2. **Webhook delivery is one HTTP request per `(target × alert)` pair**, not upstream's single POST with an
   `alerts` array per target per group. (`internal/infrastructure/publishing/coordinator.go`)
3. **Niche receivers**: OpsGenie/VictorOps/WeChat validate configuration but send zero notifications (no runtime
   publisher); Pushover/AWS SNS/Webex have no support at any layer.
4. **Config write API (`/api/v2/config*`) and `/history*`** are not implemented — explicitly out of scope for this
   task.
5. **Telegram rate limiting is global (30 msg/s), not per-chat.** Deferred; see backlog.
6. **A cross-replica DB migration advisory lock** for concurrent startup isn't implemented — deferred.
7. **Repeat-notification continuation under replica restart** is implemented and hardened (task 6.2's timer
   reconciliation loop) but not proven by a long-duration regression test — worth an explicit check during the
   live `amtool`/Grafana audit.

None of these block routing/grouping/silence/inhibition-centric use — they're specific, bounded gaps, not signs of
a shallow implementation.

Source of truth for this comparison:

- `go-app/internal/business/routing/`, `go-app/internal/infrastructure/grouping/`
- `go-app/internal/infrastructure/cluster/`, `go-app/internal/infrastructure/silencing/`
- `go-app/internal/application/router.go`, `go-app/internal/application/handlers/`
- `docs/ALERTMANAGER_COMPATIBILITY.md` (endpoint/receiver detail)
- `docs/06-planning/ALERTMANAGER-REPLACEMENT-GAP-ANALYSIS.md` (remaining-work tracking)

---

## When To Pilot AMP

Use AMP if:

- you want real routing/grouping/silence/inhibition semantics, not just a compatible-shaped ingest API
- you need HA delivery guarantees across multiple replicas
- your receiver set matches the 🟢 rows in the compatibility matrix (webhook, email, PagerDuty, Slack, Telegram,
  Discord/Teams-via-webhook)
- you can create the `amp.receiver`-scoped Secrets that actually carry delivery (see step 3 of the rollout below)
- you can validate the specific gaps above (webhook wire shape, unwired receiver types) against your own
  integrations before cutover

---

## When To Stay On Alertmanager

Stay on Alertmanager if:

- you depend on OpsGenie/VictorOps/WeChat/Pushover/SNS/Webex as an active (not just backlog) receiver
- you depend on the config write API or `/history*` as an active workflow
- a downstream integration parses the exact upstream webhook wire shape (single POST, `alerts` array) and cannot
  tolerate AMP's current one-request-per-alert-per-target shape
- you are choosing purely on performance/resource claims that are not yet backed by a reproducible benchmark report

---

## Migration Recommendation

**Recommendation**: pilot with real routing/grouping/HA validated, but keep the specific known gaps above in your
test plan before a full cutover.

Suggested rollout shape:

1. deploy AMP with the repo-local chart `./helm/amp`
2. bring your real `route:`/`receivers:` config (or a representative subset) — this is now meaningfully exercised,
   unlike the earlier flat-receiver baseline
3. <a id="config-migration"></a>**provision delivery targets — the step that has no config equivalent.** For every
   receiver name your routes reference, create a Kubernetes Secret annotated
   `amp.receiver: <receiver-name>` (comma-separated list for multiple receivers, or the `amp.receiver` *label* for a
   single name) carrying that endpoint's `type`, `url` and credentials. AMP will not derive these from your
   `receivers[].*_configs`; without them the pipeline runs green and delivers nothing. Confirm with
   `GET /api/v2/alerts/groups` that groups report the receivers you expect, then confirm an actual delivery.
4. validate ingest → grouping → notify-chain behavior end to end: group_wait, a repeat past group_interval, a
   silence suppressing an in-flight group, an inhibition rule, a mute_time_interval window, and a resolve (the
   resolved notification must arrive exactly once and then stop)
5. if running multi-replica, validate exactly-once delivery, failover, and adoption of in-flight timers after a
   replica dies (see `deploy/e2e-ha/run.sh` for a reference script covering all three)
6. cross-check your receiver list and any downstream webhook payload parsing against the gaps above
7. keep rollback to Alertmanager straightforward until your covered slice is proven

See:

- [MIGRATION_QUICK_START.md](MIGRATION_QUICK_START.md)
- [ALERTMANAGER_COMPATIBILITY.md](ALERTMANAGER_COMPATIBILITY.md)

---

## Operational Notes

- Helm examples in repo docs use the repository-local chart path `./helm/amp` as the canonical install story.
- Public docs use AGPL-3.0 as the license source of truth.
- Comparative performance/resource numbers are intentionally excluded from this document until a reproducible
  benchmark report is published for the current branch.
- `/health|/healthz` describe liveness, `/ready|/readyz` describe readiness; optional degraded components may still
  produce `200` with a degraded JSON body while required dependency failures move readiness to `503`.

---

## Learn More

- [MIGRATION_QUICK_START.md](MIGRATION_QUICK_START.md)
- [ALERTMANAGER_COMPATIBILITY.md](ALERTMANAGER_COMPATIBILITY.md)
- [CONFIGURATION_GUIDE.md](CONFIGURATION_GUIDE.md)
- [helm/amp/README.md](../helm/amp/README.md)

---

**Maintainer**: Vitalii Semenov
**License**: AGPL 3.0
