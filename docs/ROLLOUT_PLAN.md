# Rollout Plan

**Audience**: Ops/SRE moving live alerting from Alertmanager to AMP v0.1.0, or rolling a new AMP version out to a fleet already on it.
**Companion docs**: [ROLLBACK_RUNBOOK.md](ROLLBACK_RUNBOOK.md) (when a stage goes wrong), [MIGRATION_QUICK_START.md](MIGRATION_QUICK_START.md) (the commands), [ALERTMANAGER_COMPATIBILITY.md](ALERTMANAGER_COMPATIBILITY.md). Upgrading AMP's own core (before v0.1.0): [MIGRATION_FROM_LEGACY_CORE.md](MIGRATION_FROM_LEGACY_CORE.md).

AMP's core is upstream Alertmanager v0.34.1, so the risk of a move is not that routing or delivery behave differently: they are the same code. The risks are the version gap between your Alertmanager and v0.34.1, the deployment (targets, HA, auth), and AMP's layer (LLM latency and cost, PostgreSQL, K8s-Secret targets). The stages below take them one at a time.

## Stages

### Stage 1: shadow, core only

- Deploy AMP with your `alertmanager.yml`, **but with receivers that page no one**: a webhook sink, or the same receivers renamed to blackholes (`- name: team` with no integrations).
- LLM off, `profile: lite` (no PostgreSQL).
- Prometheus sends to both: add AMP as a second entry under `alerting.alertmanagers`.

```yaml
# prometheus.yml
alerting:
  alertmanagers:
    - static_configs:
        - targets: ["alertmanager.monitoring.svc:9093"]
    - static_configs:
        - targets: ["amp.monitoring.svc:9093"]
```

**Exit**: `amtool check-config` passes; `amtool alert query` shows the same alerts on both, and `GET /api/v2/alerts/groups` the same groups (allow for the lag of `group_wait`); `alertmanager_notifications_total` on AMP grows with the sink receiving what you expect; no config reload failures for a week of normal config changes.

### Stage 2: AMP's layer on, still shadow

- Turn on what you will use: PostgreSQL (`profile: standard`, an external database for production), `llm.enabled`, `investigation.enabled`, K8s-Secret targets.
- Watch the cost of classification (one LLM call per firing alert per replica) and `amp_upstream_ai_notify_waits_total{outcome="timeout"}`: notifications wait up to `llm.notify_wait`.

**Exit**: `amp_upstream_history_writes_total{outcome="error"}` and `{outcome="dropped_queue_full"}` flat; classification results look right in the sink's payloads (`llm_*` annotations); `GET /api/amp/v1/alerts/{fingerprint}` returns records; `amp_upstream_k8s_targets{state="invalid"}` is 0.

### Stage 3: one live route

- Copy the silences from Alertmanager to AMP (`amtool silence query -o json` / `silence import`; once, a second import duplicates them).
- Give one low-criticality route its real receivers on AMP and turn them off on Alertmanager (or route that team to a blackhole there).
- Use the replica count and authentication of the final setup: `alertmanager.replicas: 2` or more, every pod in Prometheus' targets, `webConfig.existingSecret` with Prometheus' `basic_auth`.

**Exit**: a week with the team's notifications arriving once each, on time; `alertmanager_cluster_members` equal to the replica count; silences created on AMP mute as expected.

### Stage 4: cutover

- Copy the silences again (only those created since stage 3) and switch the remaining routes to real receivers.
- Remove Alertmanager from Prometheus' `alertmanagers`; keep it running, with receivers off, for the rollback window.
- Move the tools: karma or Grafana to AMP's URL, `amtool` configs, links in runbooks.

**Exit**: a full on-call rotation without a missed or duplicated page traced to AMP. Then decommission Alertmanager.

## Metric watch-list

| Metric | What it tells you |
|---|---|
| `alertmanager_notifications_total`, `alertmanager_notifications_failed_total{integration,reason}` | Delivery per integration; failures should match the receiver's own availability |
| `alertmanager_notification_latency_seconds` | Delivery latency; with LLM on, includes the wait up to `llm.notify_wait` |
| `alertmanager_config_last_reload_successful` | 0 means the last reload was rejected and an older configuration runs |
| `alertmanager_cluster_members`, `alertmanager_cluster_health_score`, `alertmanager_peer_position` | Gossip health in HA; members should equal replicas |
| `alertmanager_nflog_gossip_messages_propagated_total`, `alertmanager_silences_gossip_messages_propagated_total` | Gossip actually flows |
| `amp_upstream_ai_classifications_total{outcome}` | `error` and `dropped_queue_full` should stay near zero; `fallback` means the LLM was unavailable |
| `amp_upstream_ai_notify_waits_total{outcome}` | `timeout` growing: the LLM is slower than `llm.notify_wait` |
| `amp_upstream_ai_noise_dropped_total` | With `llm.drop_noise`: what was not delivered |
| `amp_upstream_history_writes_total{outcome}` | History lost on `error`/`dropped_queue_full` |
| `amp_upstream_k8s_targets{state}` | `skipped` and `invalid` targets need a look |
| `amp_http_auth_failures_total` | Clients without credentials after auth was turned on |

## amtool spot-checks

```bash
AMP=http://amp.monitoring.svc:9093
amtool --alertmanager.url=$AMP config show
amtool --alertmanager.url=$AMP config routes test --verify.receivers=team alertname=Example
amtool --alertmanager.url=$AMP alert query
amtool --alertmanager.url=$AMP silence query
amtool --alertmanager.url=$AMP cluster show
```

`config routes test` runs upstream's routing against the configuration AMP has loaded: the answer is what the dispatcher does.

## Rolling out a new AMP version

AMP versions differ in two ways: the upstream version they carry (read upstream's changelog for every step between the two) and AMP's layer (read AMP's `CHANGELOG.md`). In the chart a version change rolls the StatefulSet one pod at a time; the other replicas keep gossip and delivery going. Check `amtool check-config` with the new upstream version's `amtool` before the rollout, then watch the metrics above for one `repeat_interval`.

## Rollback triggers

Any of these is "stop and read [ROLLBACK_RUNBOOK.md](ROLLBACK_RUNBOOK.md)", not "wait and see":

- a missed notification with no explanation in `alertmanager_notifications_failed_total` or the receiver's side;
- a duplicated notification while `alertmanager_cluster_members` is below the replica count, or outside a pod restart;
- `alertmanager_config_last_reload_successful` at 0 after a change that loaded on your old Alertmanager (check the upstream changelog between the two versions first);
- notifications delayed by about `llm.notify_wait` on every group (`amp_upstream_ai_notify_waits_total{outcome="timeout"}` close to the total): turn LLM off first (runbook § 1).
