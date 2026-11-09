# Rollback Runbook

**Applies to**: AMP v0.1.0 and later (`amp-upstream`). Rolling back from v0.1.0 to AMP's own core (`cmd/server`, before v0.1.0): [§ 4](#4-rollback-to-amps-own-core-before-v010).
**Companion docs**: [ROLLOUT_PLAN.md](ROLLOUT_PLAN.md), [MIGRATION_FROM_LEGACY_CORE.md](MIGRATION_FROM_LEGACY_CORE.md), [CONFIGURATION_GUIDE.md](CONFIGURATION_GUIDE.md).

## 0. Decision order

Cheapest first; go to the next step only if the one above does not help.

1. **Kill switch in the AMP configuration** (restart): turns AMP's layer off, the upstream core keeps delivering. § 1.
2. **`alertmanager.yml` rollback** (reload, no restart). § 2.
3. **Image or chart rollback** (`helm rollback`). § 3.
4. **Back to AMP's own core** (only right after an upgrade to v0.1.0). § 4.

## 1. Kill switches

AMP's layer sits around the upstream core; turning a part off does not touch routing, silences or delivery. The AMP configuration is read at start: a change needs a restart (with the chart, changing the value rolls the pods).

| Symptom | Setting (chart value) | Effect | Log line after the restart |
|---|---|---|---|
| Notifications late or LLM errors / cost | `llm.enabled: false` (`llm.enabled`) | No classification and no investigation; notifications go out without `llm_*` annotations and without waiting | `alert classification and investigation disabled (llm.enabled=false)` |
| Alerts missing from notifications that should be there | `llm.drop_noise: false` (`llm.dropNoise`) | Nothing is dropped as noise | |
| Notifications wait too long for classification | `llm.notify_wait: 0s` (`llm.notifyWait`) | Notifications do not wait; classification still runs | |
| Investigations misbehave (load, tool calls) | `investigation.enabled: false` (`investigation.enabled`); or `llm.agent_mode: false` | No investigations; or one-shot investigations without tools | `alert investigation disabled (investigation.enabled=false)` |
| A K8s-Secret target delivers where it should not | `publishing.enabled: false` (`publishing.enabled`), or remove the label `publishing-target=true` from that Secret | Only `alertmanager.yml`'s integrations; a Secret without the label leaves at the next refresh (`publishing.refresh.interval`) | `K8s-Secret publishing targets disabled` |
| PostgreSQL trouble | `profile: lite` (`profile`) | No history, investigations or K8s targets; `/api/amp/v1` answers 503. Delivery is unaffected either way: with the database down AMP already runs without it. | `alert history disabled: it needs PostgreSQL (profile standard)` |

With every switch off, `amp-upstream` is upstream Alertmanager v0.34.1 plus routes that answer 503.

## 2. `alertmanager.yml` rollback

A bad route, receiver, inhibit rule or template: restore the previous file and reload. Upstream's reload is atomic: a file that does not load is rejected and the running configuration stays.

```bash
# With the chart: restore alertmanager.config in your values, then
helm upgrade amp ./helm/amp -n monitoring -f values.yaml     # rolls the pods (checksum/config)

# Without the chart: replace the file, then
curl -fsS -X POST http://<amp-host>:9093/-/reload            # or: kill -HUP <pid>
```

Check that it applied:

```bash
curl -fsS http://<amp-host>:9093/metrics | grep alertmanager_config_last_reload_successful   # 1
amtool --alertmanager.url=http://<amp-host>:9093 config show
```

Receivers that come from Kubernetes Secrets are not in the file; `config show` (that is, `GET /api/v2/status`) shows them with the file's.

## 3. Image or chart rollback (v0.1.x)

```bash
helm history amp -n monitoring
helm rollback amp <revision> -n monitoring
kubectl -n monitoring rollout status statefulset/amp
```

To pin an older image without a chart rollback: `helm upgrade amp ./helm/amp -n monitoring --reset-then-reuse-values --set image.tag=<tag>`.

What a rollback within v0.1.x keeps:

- **Silences and the notification log**: in the `data` volume of each pod (`--storage.path=/data`), upstream's snapshot format. An older `amp-upstream` reads them as long as the upstream version did not change the format; the upstream changelog of the bump says so ([UPSTREAM_UPGRADE.md](UPSTREAM_UPGRADE.md)).
- **PostgreSQL**: migrations are additive (the latest two add `alerts.upstream_fingerprint` and its index). An older binary ignores what it does not know. **Do not run `goose down`**: the Down of the initial schema drops all tables, and the others drop what they created.
- **`alertmanager.yml`**: a key that a newer upstream added and an older one does not know fails the start of the older image. Check with the `amtool` of the older upstream version first.

Verify:

```bash
curl -fsS http://<amp-host>:9093/-/ready
curl -fsS http://<amp-host>:9093/api/v2/status | jq .versionInfo
amtool --alertmanager.url=http://<amp-host>:9093 cluster show
```

With basic auth on, add credentials (`-u user:password`, `amtool --http.config.file`).

## 4. Rollback to AMP's own core (before v0.1.0)

Possible right after the upgrade, while the old core's state is still there:

```bash
helm rollback amp <revision before the upgrade> -n monitoring
kubectl -n monitoring rollout status deployment/amp
```

- The rollback recreates the Deployment (port 8080) and deletes the StatefulSet. The StatefulSet's PVCs `data-amp-<i>` stay; delete them if you do not return to v0.1.0.
- **Silences**: the old core reads its `silences` table as it was at the upgrade. Silences created on v0.1.0 are not there: export them first (`amtool silence query -o json`) and recreate those you need.
- **Notification state**: if Valkey's volume was kept, the old core resumes from it; otherwise every firing group notifies once more.
- **PostgreSQL**: the new column and index stay; the old core does not read them. History written by v0.1.0 is in the same table and readable by the old core.
- **Prometheus, probes and clients** changed in [MIGRATION_FROM_LEGACY_CORE.md](MIGRATION_FROM_LEGACY_CORE.md) § Step 8 have to go back to port 8080, `/healthz`/`/readyz` and `/api/v1`.
- An HPA deleted during the upgrade is recreated by the rollback.

## 5. Post-rollback checklist

- [ ] `/-/ready` answers 200 on every pod.
- [ ] `alertmanager_config_last_reload_successful` is 1.
- [ ] `amtool cluster show`: every replica is a member (with more than one).
- [ ] `amtool silence query`: the silences you expect are active.
- [ ] A test alert reaches its receiver once (`amtool alert add` against one pod, or through Prometheus).
- [ ] The log has no `AMP configuration keys ignored` entry for a key you meant to set.
- [ ] With history on: `amp_upstream_history_writes_total{outcome="written"}` grows.
