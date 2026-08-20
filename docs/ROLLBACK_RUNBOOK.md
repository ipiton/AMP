# Rollback Runbook

**Audience**: Ops/SRE rolling AMP back after a bad release.
**Scope**: what actually survives a rollback, what breaks, and the exact
commands — grounded in the current code (goose migrations, the snapshot
format, the reload path), not in what the docs *say* the system does. Two
places in this repo describe rollback/rollback-adjacent features that do
not actually work as documented; both are called out explicitly below so
you don't discover them mid-incident.

---

## 0. Decision order

Cheapest, fastest, safest first. Only fall through to the next tier if the
one above doesn't fix it:

1. **Kill switch** (config flag flip + reload/restart) — seconds, no
   version change, no data-layer risk. Section 1.
2. **Config-only rollback** (revert `config.yaml`, reload/restart) —
   seconds to a minute. Section 2.
3. **Helm/image rollback** (previous binary version) — minutes, and the
   one with real data/compat traps. Sections 3–5.

---

## 1. Kill switches (try these first)

These flip behavior back to the pre-change default without touching the
binary version at all. All are read at process start; "restart" below
means the container must actually restart (a `/-/reload` or `SIGHUP` is
**not** enough for these three — they're read once in
`initializePublishing`/`initializeTemplating` at boot).

| Symptom | Flag | Effect | Needs restart? |
|---|---|---|---|
| Slack/email/PagerDuty/Telegram notifications changed shape after the templates epic (Block Kit → upstream attachments, etc.) | `publishing.templates.enabled: false` | Restores AMP's pre-epic fixed formatters wholesale; `templates:` files and every per-integration presentation field (`title`, `text`, `description`, …) are ignored exactly as before. | Yes |
| Notifications flowing to receivers you didn't expect, or you need delivery off entirely | `publishing.enabled: false` | Falls back to `MetricsOnlyPublisher` — alerts still ingest, group, silence-match; nothing is ever dialed. | No — read every time `initializePublishing` runs, i.e. effectively on restart only in practice since it's not part of the reload path either; treat as restart-required. |
| Grouping/timer subsystem behaving badly (HA races, reconciliation storms) | `grouping.enabled: false` | Alerts no longer group/dedup/timer at all — every alert reaches the publish step immediately, ungrouped. This is a big behavior change, not a scalpel; only use it as a stop-the-bleeding measure. | Yes |
| Investigation/LLM pipeline misbehaving | `investigation.enabled: false` | Stops the async investigation workers; alert ingest/publish path is untouched. | Yes |

**Verify a flag flip actually landed**: `GET /api/v2/status` → `config.original`
is the redacted, Alertmanager-shaped route/receivers view (see
`AlertmanagerConfigYAML`, `internal/application/handlers/status_api.go`) —
it won't show these flags directly (they're outside the Alertmanager
section), so confirm via the startup log line instead
(`"Publishing disabled by config"` / `"Notification templates wired into
publishing"` absent, etc.) after the restart.

---

## 2. Config-only rollback (no version change)

If the problem is a bad `route:`/`receivers:`/`templates:`/`inhibition:`
edit, you don't need to touch the binary at all.

```bash
# 1. Restore the previous config.yaml (from git, from a ConfigMap history,
#    from your own backup — whatever you have).
cp config.yaml.previous /path/to/config.yaml

# 2a. Trigger reload without a restart (Kubernetes: exec into the pod, or
#     hit the Service directly if /-/reload is reachable):
curl -X POST http://<amp-host>:8080/-/reload

# 2b. Equivalent, upstream-parity trigger (also works, same code path):
kill -HUP <amp-pid>
```

Both routes call the same `ServiceRegistry.ReloadConfig` →
`reloadCoordinator.ReloadFromFile` pipeline
(`internal/application/service_registry.go`). Useful safety property: **a
reload that fails validation leaves the previous config live** — `main.go`'s
SIGHUP handler logs `"SIGHUP config reload failed; previous configuration
remains active"` and `/-/reload` returns a non-200 without touching
`r.config` — so a bad rollback config attempt fails safely instead of
taking the instance down. `deploy/smoke/run.sh` step 4 exercises this exact
reload path end-to-end against a running instance.

**What reloads live vs. needs a restart**, from the current code:

- Route tree, `receivers:` (including config-provisioned publishing
  targets), `templates:` files, `inhibition:` rules — all hot-reload via
  the path above.
- Renaming the **root route's** receiver — reloads the tree, but the
  degraded-routing fallback (used only when route evaluation itself fails)
  is a startup snapshot and keeps pointing at the old name until restart.
  Narrow edge case, not the common path.
- `publishing.enabled`, `publishing.templates.enabled`, `grouping.enabled`,
  `investigation.enabled`, `profile`, `storage.backend` — startup-only, see
  Section 1.

### A documented feature that does not exist: don't reach for it

`docs/CONFIGURATION_GUIDE.md` describes a `POST /api/v2/config/rollback`
endpoint (plus `/api/v2/config/history`, hash-addressed rollback targets,
`dryRun=true`). **This endpoint is not mounted.** Verify yourself:

```bash
curl -i -X POST http://<amp-host>:8080/api/v2/config/rollback
# 404
```

`go-app/internal/application/router_contract_test.go` asserts this
directly (`{name: "config api not mounted", ..., path: "/api/v2/config",
status: http.StatusNotFound}`). The backing service
(`internal/config/update_service.go`'s `DefaultConfigUpdateService`,
including `RollbackConfig` and the `config_versions`/`config_audit_log`
Postgres tables from the `20251122000000_config_management.sql` migration)
is real, compiled-in code — it is simply never wired to any HTTP route or
called from anywhere in the request path (`grep -rn ".RollbackConfig(" `
across the module returns nothing but the definition itself). Its only
live effect today is an **automatic**, in-process rollback to the
previous in-memory version if a hot-reload's *critical* component fails
mid-reload (`atomicApply` → `hotReload` → `rollback` inside
`update_service.go`) — that path is not reachable from `/-/reload` either
(which goes through the separate, simpler `reloadCoordinator`, not
`DefaultConfigUpdateService`). Practically: **the only real config
rollback lever is Section 2's file-edit + reload/SIGHUP.** Don't spend
incident time looking for the REST endpoint; it isn't there.

---

## 3. Helm / binary version rollback

```bash
RELEASE=amp
NAMESPACE=monitoring

# See revision history
helm history "$RELEASE" --namespace "$NAMESPACE"

# Roll back to the last-known-good revision
helm rollback "$RELEASE" <REVISION> --namespace "$NAMESPACE"

# If the target version isn't in Helm history (e.g. you're pinning an
# older image tag directly rather than reverting a chart values change):
helm upgrade "$RELEASE" ./helm/amp \
  --namespace "$NAMESPACE" \
  --reuse-values \
  --set image.tag=<previous-tag>
```

Then verify:

```bash
kubectl -n "$NAMESPACE" rollout status deployment/<amp-deployment>
curl -fsS http://<amp-host>:8080/healthz
curl -fsS http://<amp-host>:8080/api/v2/status | jq .versionInfo
```

---

## 4. What survives a binary version rollback

### Postgres — safe, because every migration to date is additive-only

All 9 migrations under `go-app/migrations/` (goose v3, `internal/database/migrations.go`)
were audited for their Down direction:

| Migration | Up is | Down is |
|---|---|---|
| `20250911094416_initial_schema` | `CREATE TABLE`/`INDEX` | drops everything (destructive, but this is the baseline schema) |
| `20251009180500_add_filter_indexes` | `CREATE INDEX` | **commented out** (no-op) |
| `20251104120000_create_silences_table` | `CREATE TABLE silences` | **commented out** (no-op) |
| `20251112150000_create_publishing_dlq` | `CREATE TABLE publishing_dlq` | drops it (destructive) |
| `20251116160000_tn63_history_performance_indexes` | `CREATE INDEX` | **commented out** (no-op) |
| `20251122000000_config_management` | `CREATE TABLE` ×4 (config_versions/audit_log/backups/locks) | drops them (destructive) |
| `20251125000001_create_templates_tables` | `CREATE TABLE templates`, `template_versions` | drops them (destructive) |
| `20260422000000_create_investigation_table` | `CREATE TABLE alert_investigations` | drops it (destructive) |
| `20260423000000_investigation_agent_steps` | `ALTER TABLE ... ADD COLUMN` ×3 | drops the 3 columns (destructive) |

Every **Up** migration only creates tables/indexes or adds columns — none
drops or renames an existing column an older binary's hand-written SQL
depends on. That means:

- **Do not run `goose down` / `make migrate-down` as part of a rollback.**
  There is no reason to: the older binary works fine against the *newer*
  schema, since it simply never references the tables/columns it doesn't
  know about. Running `down` would actively destroy data (several Down
  blocks `DROP TABLE`/`DROP COLUMN` for real) for no benefit, and if you're
  rolling back one of several replicas during a rolling deployment, a
  concurrent `goose down` would break the *other*, still-newer replicas
  outright.
- Practical rollback = deploy the older binary image against the
  **unmodified, current** Postgres schema. Nothing to do here beyond that.

### Redis — self-healing via TTL, with one real gap to know about

- nflog dedup entries, timer state, and lock keys all carry their own TTLs
  (`repeat_interval`, `timer_lock_ttl`, etc.) and expire on their own — no
  manual cleanup needed after a rollback either direction.
- **Key-shape gap**: nflog entries moved from one bare key per
  group+receiver (`nflog:entry:{groupKey}`) to one key per group+receiver
  **+target** (`nflog:entry:{groupKey}:{target}`) in the wave-2 change
  (`AMP-PARITY-WAVE6-EPIC`, wave 2). The forward direction is documented as
  safe ("old bare keys simply expire, new lookup never reads them"). The
  **rollback** direction is the mirror image and is NOT automatically
  safe: an older binary that only knows the bare-key shape will not see
  the newer, target-suffixed keys as "already notified." **Net effect: a
  group that was notified in the few minutes before a rollback across this
  boundary may fire a duplicate notification once, until its own TTL
  window passes.** Not data corruption, not a crash — a bounded,
  self-resolving duplicate-notification window. Worth a heads-up to
  whoever's on the receiving end of alerts during the rollback.

### Lite profile file snapshot — versioned, and the version check is strict

`internal/infrastructure/snapshot/snapshot.go`: the snapshot file (silences
+ nflog only — alerts/groups are never snapshotted, matching upstream) is
JSON with a `version` field (`CurrentVersion = 1` as of this writing).
`Load` **rejects** a file whose version doesn't match exactly:

```go
if data.Version != CurrentVersion {
    return Data{}, fmt.Errorf("snapshot file %s has unsupported version %d (want %d)", path, data.Version, CurrentVersion)
}
```

The caller treats that identically to a corrupt/missing file: log a
`Warn`, **start empty**. So: rolling back a lite-profile deployment past a
future snapshot-format version bump silently loses every silence and nflog
entry that was only in the snapshot — the instance boots clean, not
degraded, and there is no error surfaced beyond the boot-time log line.
**As of this rollback runbook's writing there has only ever been version
1**, so this is a forward-looking warning, not a currently-live trap — but
check `CurrentVersion` in the target rollback binary's source against what
the current binary wrote before assuming this is a no-op.

Lite-profile silences are memory-only *regardless* of rollback — that's
true on any restart, rollback or not, unless `storage.path` (the snapshot
file) is set. Standard-profile silences are Postgres-persisted and
unaffected by a binary rollback (see the Postgres section above).

---

## 5. Config rollback traps — the breaking-changes list, reversed

Rolling the binary back re-activates whatever the corresponding forward
change fixed. From `CHANGELOG.md`'s "Breaking changes / migration notes,"
in rollback order (most recent epic first):

1. **Past the templates epic**: Slack/email/PagerDuty/Telegram
   notifications revert from upstream-shaped output back to AMP's
   original fixed formatters (Block Kit `blocks` array for Slack, AMP's
   own email HTML, etc.) — **use `publishing.templates.enabled: false`
   instead of a binary rollback** if this specific behavior is the only
   problem; it's the documented, instant equivalent (Section 1).
2. **Past `AMP-PARITY-WAVE6-EPIC` (config-target auto-provisioning)**: a
   `receivers:` block's `webhook_configs`/`slack_configs`/etc. **stops
   provisioning any delivery target** on the older binary — it only knows
   how to discover targets from Kubernetes Secrets. **If your only
   publishing targets are config-provisioned (`cfg:<receiver>/...`
   names), rolling back past this epic silently drops to metrics-only
   publishing with zero deliveries.** Before rolling back this far,
   confirm K8s-Secret-sourced targets exist for every receiver that needs
   to keep delivering, or accept the outage.
3. **Past the same epic's blackhole-receiver support**: a receiver
   declared with no integrations (upstream's `- name: 'null'`, or one
   whose only integration is `opsgenie_configs`/`victorops_configs`/`wechat_configs`)
   is valid on the new binary and invalid (hard config-load error) on the
   old one. **A config carrying any such receiver will fail to boot on
   the rolled-back binary — this is a boot-loop risk, not just a behavior
   change.** Audit `receivers:` for blackhole/unsupported-only entries
   before rolling back this far.
4. **Past the PagerDuty endpoint fix**: the client posts to
   `/v2/enqueue` (correct, current) vs. `/v2/events` (the old, wrong
   path that PagerDuty rejects). If any PagerDuty target Secret/URL was
   edited to adapt to the fix (or was already using the full
   `.../v2/enqueue` URL), rolling back to a pre-fix binary means it goes
   back to POSTing at `/v2/events` — **verify PagerDuty delivery
   specifically after any rollback crossing this boundary**, since a
   silent failure here doesn't crash anything, it just stops paging.
5. **Past the implicit-`group_by` fix**: a route with no `group_by` at
   any level (route/ancestor/`global.group_by`) groups upstream-style
   (one notification per receiver) on the new binary, and AMP's older
   implicit `[alertname]` substitution on the old one. Only matters for
   configs that never set `group_by` explicitly; harmless if yours does.
6. **Past wave 3's `reconciliation_grace` default change (90s → 20s on
   rollback)**: HA failover/adoption recovery time reverts from ~90s+tick
   to ~20s+tick. Not dangerous by itself — the old binary has no startup
   check requiring `reconciliation_grace` to exceed the publish-claim TTL,
   so a config that pins `reconciliation_grace: 90s` to satisfy the *new*
   binary's validation still works fine, just with a longer-than-default
   adoption window, on the old one.
7. **Past the `DELETE /api/v2/silence/{id}` semantics change**: reverts
   from expire-in-place back to a hard delete. Only matters if external
   tooling depends on the silence row surviving (in expired state) after
   deletion.
8. **Past receiver-name-uniqueness enforcement / the `email_configs`
   global-SMTP requirement**: both changes made config validation
   *stricter*. Rolling back makes it *more permissive* again — not a
   trap, the old binary just silently tolerates what the new one would
   have rejected at boot.

**General rule**: before any rollback that crosses an epic boundary,
`grep` the live `config.yaml` for the specific shapes called out above
(blackhole/no-integration receivers, config-only-provisioned receivers
with no K8s Secret fallback, pinned `reconciliation_grace`) rather than
assuming "it booted before, it'll boot now."

---

## 6. Post-rollback verification checklist

```bash
curl -fsS http://<amp-host>:8080/healthz
curl -fsS http://<amp-host>:8080/api/v2/status | jq '.versionInfo, .cluster'
curl -fsS http://<amp-host>:8080/api/v2/receivers
```

- `versionInfo.version`/`.revision` match the rolled-back build.
- `cluster.status` is `ready` (standard profile) — a rollback that broke
  Redis connectivity would degrade this.
- Watch `alert_history_publishing_blackhole_drops_total` — a sudden rise
  after rollback means trap #2/#3 above just bit you (targets vanished or
  a receiver validation gap silently blackholed a route).
- Real amtool spot-check (bundled in `quay.io/prometheus/alertmanager`'s
  image, `--entrypoint amtool`): `amtool --alertmanager.url=http://<amp-host>:8080
  alert query` and `amtool ... silence query` should both return the
  expected current state — see `docs/ROLLOUT_PLAN.md`'s amtool
  spot-checks for the full list, and `scripts/release-gate.sh`'s
  `amtool-compat` step for a scripted example.
