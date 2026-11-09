# Alertmanager Compatibility

**Upstream version**: Prometheus Alertmanager **v0.34.1** (`go.mod`, `upstreamcore.PortedFrom`)
**Applies to**: AMP v0.1.0 (`amp-upstream`), [ADR-019](06-planning/DECISIONS.md)
**Revised**: 2026-11-08 (UPSTREAM-DOCS-MIGRATION)

## Compatible by construction

AMP does not reimplement Alertmanager and does not claim compatibility from a test suite that compares two implementations. The delivery core of `amp-upstream` **is** upstream's code: the binary imports the packages of `github.com/prometheus/alertmanager` at the version pinned in `go-app/go.mod` and wires them the way upstream's `app` package wires them.

| Upstream package | What it gives AMP |
|---|---|
| `config` | Parsing, defaults and validation of `alertmanager.yml`, including `*_file` secrets, `global` fallbacks, time intervals |
| `dispatch` | The routing tree, `group_by`, `group_wait`/`group_interval`/`repeat_interval`, mute and active time intervals |
| `notify` + `notify/*` | The notification pipeline (inhibit, silence, time-interval, dedup, retry stages) and all 18 receiver integrations |
| `template` | Notification templates, the default templates, `templates:` globs |
| `silence`, `nflog` | Silences and the notification log, with their on-disk snapshots in `--storage.path` |
| `inhibit` | `inhibit_rules` |
| `provider/mem` | The alert store and its garbage collection (`--data.retention`) |
| `cluster` | memberlist gossip for HA (`--cluster.*`) |
| `api/v2` | The `/api/v2` handlers, generated from upstream's OpenAPI spec |

Upstream's `app` package itself cannot be built from the Go module (it embeds the UI, whose built assets are not in the module zip), so AMP keeps a port of it in `go-app/internal/upstreamcore/` (`core.go`, `reloader.go`, ported from `app/{app,reloader,cluster,metrics,lifecycle}.go` at v0.34.1). The port is the only upstream-shaped code AMP maintains; [UPSTREAM_UPGRADE.md](UPSTREAM_UPGRADE.md) describes how it follows a version bump, and `TestPortMatchesUpstreamVersion` fails while the port and `go.mod` disagree.

Consequences:

- An `alertmanager.yml` that upstream v0.34.1 accepts, AMP accepts, and one upstream rejects, AMP rejects with the same error. `amtool check-config` is an exact check.
- Behaviour you know from upstream (when a group fires, which silence mutes what, how a receiver formats its payload, how gossip deduplicates notifications) is upstream's behaviour. If AMP behaves differently there, it is a bug in the wiring, not a documented difference.
- When upstream fixes a bug, AMP gets the fix by bumping the module; when upstream changes behaviour, so does AMP. Read upstream's changelog for each bump.

The rest of this document lists what AMP adds around that core and where it deliberately differs.

## Divergences from upstream

| Area | Upstream v0.34.1 | AMP v0.1.0 |
|---|---|---|
| Web UI (`/`, `/#/alerts`, `/#/silences`) | Built-in Elm UI | **None**: 404. The UI is not in the Go module; AMP's own UI is planned after v0.1.0 (`UPSTREAM-UI`). See [Viewing alerts](#viewing-alerts-karma-grafana-amtool). |
| Event recorder | Behind a feature flag, off by default | Not wired |
| Flags | `--silences.max-silences`, `--silences.max-silence-size-bytes`, `--alerts.per-alertname-limit`, `--dispatch.start-delay` | Not available; the binary refuses unknown flags |
| Extra flag | | `--amp.config.file`: the AMP configuration ([CONFIGURATION_GUIDE.md](CONFIGURATION_GUIDE.md)) |
| Configuration | `alertmanager.yml` | The same file, plus receivers taken from Kubernetes Secrets when the AMP configuration enables them ([below](#kubernetes-secret-targets)). The file on disk is never rewritten. |
| Notification payloads | As the integration builds them | As upstream, plus `llm_severity`, `llm_confidence`, `llm_reasoning`, `llm_recommendations` annotations when classification is on ([below](#classification)). With `llm.drop_noise: true` alerts classified as noise are not delivered. |
| Shutdown | Abandons a running reload after its shutdown timeout | Waits for the reload; a failed gossip leave is logged, not returned |
| Routes | `/api/v2`, `/-/*`, `/metrics`, `/debug/*` | The same, plus `/api/amp/v1/*` ([AMP API](#amp-api)) and an answer for removed AMP paths (below) |

Everything else in the list below is upstream behaviour that AMP's own core (`cmd/server`, removed in v0.1.0) did differently. It is listed because clients written against that core notice it; for upstream users nothing changes.

| Behaviour | What you get |
|---|---|
| `POST /api/v2/alerts` without `Content-Type: application/json` | 415. Prometheus and `amtool` set the header. |
| Invalid payloads | 400 or 422 with a plain-string body, upstream's error format |
| `GET /api/v2/alerts?active=maybe` | An invalid boolean is read as `false`, as upstream's generated parser does |
| `GET /api/v2/alerts/groups` right after a `POST` | Trails ingest by about a millisecond (the dispatcher's subscription to the alert store) |
| A broken `alertmanager.yml` at start | The process exits. On `POST /-/reload` the running configuration stays and the reload returns an error. |
| `GET /api/v2/alerts?status=resolved`, `?group_by=` | Unknown query parameters, **ignored**: the answer is the unfiltered list (200), not an error. Resolved alerts are not kept by upstream; read them from `/api/amp/v1/alerts/{fingerprint}`. |
| `GET /api/v2/inhibitions` | 404. Upstream has no such route; `status.inhibitedBy` in `/api/v2/alerts` lists the inhibiting alerts. |
| `/api/v1/*` | 410 Gone, as upstream answers |
| `/health`, `/healthz`, `/ready`, `/readyz`, `/dashboard*` | 404. Probes are `/-/healthy` and `/-/ready`. |
| `/debug/pprof/*`, `/debug/vars` | Served on the API port, as upstream: the process command line is visible there, so do not pass secrets in flags, and keep the port off public networks |

The futureparity contract suites that used to compare AMP's own core with upstream still run on `amp-upstream` (`scripts/upstream-core-gate.sh`); `go-app/internal/upstreamcore/contract/testdata/divergences.tsv` lists every test that is expected to fail and why. Each entry is one of the rows above.

## AMP API

AMP's routes sit next to `/api/v2`, under the path of `--web.external-url`, behind the same authentication (`--web.config.file`). They read the alert history in PostgreSQL; without it (`profile: lite`, or the database unreachable) they answer 503.

| Route | Answer |
|---|---|
| `GET /api/amp/v1/alerts/{fingerprint}` | The alert's history record: `fingerprint`, `amp_fingerprint`, `alert_name`, `status` (`firing`/`resolved`), `labels`, `annotations`, `starts_at`, `ends_at`, `generator_url`, `first_seen_at`, `last_event_at`. Kept after the alert resolved. |
| `GET /api/amp/v1/alerts/{fingerprint}/investigation` | The latest investigation of that alert: `id`, `status`, `retry_count`, `queued_at`, `created_at`, `updated_at`, `started_at`, `completed_at`, `error_message`, `error_type`, `result`, and in agent mode `steps`, `iterations_count`, `tool_calls_count` |

- `{fingerprint}` is the fingerprint `/api/v2/alerts` reports (16 hex digits), or AMP's own 64-hex fingerprint of the old core; case does not matter. Anything else is 400.
- 404: no such alert in the history, or no investigation for it. 405 for methods other than `GET` and `HEAD`. Errors are JSON, `{"error": "..."}`.
- Latency: `amp_api_request_duration_seconds`.

Rows written before v0.1.0 get their upstream fingerprint from a background backfill at start (`alert history: upstream fingerprints backfilled` in the log); until it finishes, find them by the AMP fingerprint.

## Classification

With `llm.enabled: true` every firing alert is classified by the configured OpenAI-compatible model, asynchronously and off the ingest path. A notify stage ahead of every receiver waits up to `llm.notify_wait` (5s) for the result and adds it as annotations on the copy of the alert that goes to the receiver:

| Annotation | Value |
|---|---|
| `llm_severity` | The model's severity |
| `llm_confidence` | `0.00`–`1.00` |
| `llm_reasoning` | The model's reasoning, truncated |
| `llm_recommendations` | Recommendations joined with `; ` (when present) |

Templates can use them like any annotation (`{{ .CommonAnnotations.llm_severity }}`). On a timeout, an LLM error or LLM off, the alert goes out unchanged. The alert in `/api/v2/alerts` is never modified.

## Kubernetes-Secret targets

With PostgreSQL available and `publishing.enabled: true`, AMP lists Secrets labelled `publishing-target=true` in `publishing.discovery.namespace` every `publishing.refresh.interval` (5m) and adds each one to the in-memory configuration as an integration of the receivers it is scoped to. Delivery, retries, the notification log and templates are upstream's; `/api/v2/status` shows the integrations with their secrets masked, as it masks the file's.

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: oncall-slack
  namespace: monitoring
  labels:
    publishing-target: "true"
  annotations:
    amp.receiver: team-critical        # comma-separated receiver names
stringData:
  config: |
    {"name": "oncall-slack", "type": "slack", "format": "slack", "enabled": true,
     "url": "https://hooks.slack.com/services/T000/B000/XXXX",
     "headers": {}}
```

| `type` | Becomes |
|---|---|
| `webhook`, `rootly` | `webhook_configs` (`url`). A Rootly target pointing at Rootly's REST API (`api.rootly.com`, `api.rootly.io`) is skipped: use the webhook URL of an Alertmanager alert source. |
| `slack` | `slack_configs` (`api_url`; `templates.title`, `text`, `channel`, … set the fields of the same name) |
| `pagerduty` | `pagerduty_configs` (`headers.routing_key` required; the URL is a base URL, events go to `/v2/enqueue`) |

- Without `amp.receiver` (annotation, or a single-name label) a target joins **every** receiver.
- A target that does not parse or validate is skipped and logged; the rest of the configuration is applied. `amp_upstream_k8s_targets{state}` counts attached, skipped and invalid targets.
- Resolved notifications are sent unless the target's `filter_config.send_resolved` is `false`.

The chart's RBAC grants only `list` on Secrets, in the discovery namespace only ([helm/amp/README.md](../helm/amp/README.md#rbac)); keep target Secrets in a namespace of their own.

## Alert history and investigation

Every alert the core accepts goes to PostgreSQL through the alert-store callback, silenced and inhibited ones included, with the upstream fingerprint stored next to AMP's. The write is asynchronous: a slow or unavailable database does not slow ingest or delivery, and events that fail to write or find the queue full are lost (`amp_upstream_history_writes_total{outcome}` counts them); the alert's next update rewrites its row.

With `investigation.enabled: true` each new firing incident is queued for an LLM investigation (`investigation.worker_count` workers); with `llm.agent_mode` the model can query Prometheus, Loki, the Kubernetes API and PostgreSQL within `investigation.tools` and read runbooks. Results are read through the [AMP API](#amp-api). Investigation is not on the notification path.

## HA

Replicas form upstream's memberlist cluster: silences and the notification log are gossiped, and each replica waits `--cluster.peer-timeout` per peer ahead of it before notifying, so a group goes out once. As with upstream, **every replica must receive every alert**: list all pods in Prometheus' `alertmanagers`, not a load-balanced Service. The Helm chart does this through the headless Service ([helm/amp/README.md](../helm/amp/README.md)). Alert history and investigations are shared through PostgreSQL; each replica classifies the alerts it receives.

Redis/Valkey is not used by `amp-upstream`.

## Metrics

- Upstream's `alertmanager_*` metrics, unchanged, including `alertmanager_build_info` with the real upstream version (`0.34.1`). karma and other tools that detect the API version from it see a supported version.
- `amp_build_info`: AMP's own version, revision and build date.
- AMP's layer: `amp_api_request_duration_seconds`, `amp_upstream_k8s_targets`, the classification, history and investigation metrics (`amp_*`).

## Viewing alerts: karma, Grafana, amtool

AMP v0.1.0 serves no UI. Anything that reads an Alertmanager reads AMP:

- **amtool**: `amtool --alertmanager.url=http://amp.monitoring.svc:9093 alert query`, `silence add|query|expire|import`, `config show`, `cluster show`.
- **[karma](https://github.com/prymitive/karma)**: `ALERTMANAGER_URI=http://amp.monitoring.svc:9093`. karma reads the version from `alertmanager_build_info` on `/metrics`; keep `/metrics` reachable for it (with basic auth on, add karma's credentials or list `/metrics` in `server.auth.unauthenticated_paths`). With several replicas, list each of them in karma: it merges the alerts of several Alertmanagers into one view.
- **Grafana**: an Alertmanager data source (implementation "Prometheus") with the AMP URL; Grafana's alerting UI then shows AMP's alerts and silences.

None of them knows the AMP API: alert history and investigations are read from `/api/amp/v1` directly.

## Receivers

All 18 upstream integrations (`discord`, `email`, `incidentio`, `jira`, `mattermost`, `msteams`, `msteamsv2`, `opsgenie`, `pagerduty`, `pushover`, `rocketchat`, `slack`, `sns`, `telegram`, `victorops`, `webex`, `webhook`, `wechat`) with every field upstream documents, `http_config` and `*_file` secrets included. They are upstream's code; their documentation is [upstream's](https://prometheus.io/docs/alerting/latest/configuration/#receiver-integration-settings).

## Upgrading the upstream version

See [UPSTREAM_UPGRADE.md](UPSTREAM_UPGRADE.md). A bump changes AMP's behaviour exactly as it changes upstream's; the release notes of the AMP version name the upstream version it carries.
