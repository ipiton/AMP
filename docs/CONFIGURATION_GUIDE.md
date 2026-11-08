# Configuration Guide

AMP v0.1.0 (`amp-upstream`) reads two files and upstream Alertmanager's flags.

| What | Where | Read by | Reloaded |
|---|---|---|---|
| Routes, receivers, inhibit rules, time intervals, templates | `alertmanager.yml` (`--config.file`) | Upstream's `config` package | `POST /-/reload`, `SIGHUP` |
| History database, LLM, investigation, K8s-Secret targets, auth exemptions | AMP configuration, `amp.yaml` (`--amp.config.file`) | AMP | At start only |
| Listen address, external URL, storage, cluster, basic auth | Flags | Upstream's flags, AMP's binary | At start only |

Before v0.1.0 AMP's own core read one combined file (`config.yaml` with `route`, `receivers`, `redis`, `grouping`, `server.port`, … next to AMP's keys). That model is gone: `alertmanager.yml` holds what upstream reads, the AMP file holds the rest. Upgrading from it: [MIGRATION_FROM_LEGACY_CORE.md](MIGRATION_FROM_LEGACY_CORE.md).

## `alertmanager.yml`

Upstream's file, unchanged: [upstream's configuration reference](https://prometheus.io/docs/alerting/latest/configuration/) applies in full, for the version AMP carries (v0.34.1). Check a file with upstream's tool:

```bash
amtool check-config alertmanager.yml
```

- At start a file that does not load stops the process. On reload the running configuration stays, the reload answers with the error, and `alertmanager_config_last_reload_successful` drops to 0.
- `*_file` secrets (`api_url_file`, `password_file`, `routing_key_file`, …) are read by upstream at load; mount the files next to the configuration.
- Receivers from Kubernetes Secrets are added in memory, not to the file ([ALERTMANAGER_COMPATIBILITY.md](ALERTMANAGER_COMPATIBILITY.md#kubernetes-secret-targets)).

## AMP configuration (`amp.yaml`)

Every key with its default and meaning: [`config.yaml.example`](../config.yaml.example). The sections:

| Section | Purpose |
|---|---|
| `profile` | `standard` (default): PostgreSQL for history, investigations and K8s targets. `lite`: none of them; `/api/amp/v1` answers 503. Alerting is the same in both. |
| `server.web_config_file` | Upstream web config (basic auth) when `--web.config.file` is not given. Env: `SERVER_WEB_CONFIG_FILE`. |
| `server.auth.unauthenticated_paths` | Paths that skip basic auth. Default `/-/healthy`, `/-/ready`. |
| `app.environment` | `development` (default) or `production`; `production` enforces the database credential checks. |
| `database.*` | PostgreSQL of the alert history. Migrations run at start. |
| `llm.*` | Classification by an OpenAI-compatible API (`provider: openai` or `openai-compatible`, `base_url`, `model`, `api_key`), `notify_wait`, `drop_noise`, `agent_mode`. |
| `investigation.*` | Investigation workers, retries, agent-mode `tools` and `runbooks`. |
| `publishing.*` | K8s-Secret targets: `enabled`, `discovery.namespace`, `discovery.label_selector`, `refresh.interval`. |

Rules:

- **Without `--amp.config.file`** amp-upstream is upstream Alertmanager plus the AMP routes, which answer 503: no history, classification or targets.
- **A missing file** is not an error: the defaults apply (`profile: standard`, so AMP tries `localhost:5432`).
- **Unknown keys** are ignored, with one warning at start that lists them: `AMP configuration keys ignored ... keys [redis server.port]`. Keys of the removed core (`route`, `receivers`, `grouping`, `redis`, `cache`, `server.port`, `log`, …) end up there.
- **Environment variables** override keys: upper case, `.` → `_`. `DATABASE_PASSWORD`, `LLM_API_KEY`, `INVESTIGATION_TOOLS_DATABASE_DSN` keep secrets out of the file.
- **PostgreSQL unreachable at start**, or a failed migration: AMP logs an error and runs without history and investigations until the next restart. Delivery is unaffected.
- **Invalid values** stop the start: an unknown `profile`, an empty `database.host` or `database.database` under `profile: standard`, a negative `llm.notify_wait`, a username without a password (or the reverse), and under `app.environment: production` a missing username or password, a known weak password, one shorter than 12 characters, or `ssl_mode: disable` without `allow_insecure_ssl_in_production: true`.
- Investigation needs `llm.enabled`, `investigation.enabled` and PostgreSQL; tools and runbooks also need `llm.agent_mode: true`.

A minimal production file:

```yaml
profile: standard
app:
  environment: production
database:
  host: postgres.db.svc
  database: alerthistory
  username: amp              # password: DATABASE_PASSWORD
  ssl_mode: verify-full
llm:
  enabled: true
  provider: openai-compatible
  base_url: https://llm-gateway.example.internal/v1
  model: gpt-4o-mini         # key: LLM_API_KEY
investigation:
  enabled: true
publishing:
  discovery:
    namespace: amp-targets
```

## Flags

`amp-upstream --help` lists them; they are upstream's flags with upstream's defaults, plus `--amp.config.file`.

| Flag | Default | Notes |
|---|---|---|
| `--config.file` | `alertmanager.yml` | |
| `--amp.config.file` | empty | AMP configuration |
| `--storage.path` | `data/` | Silence and notification-log snapshots |
| `--data.retention` | `120h` | |
| `--web.listen-address` | `:9093` | |
| `--web.external-url` | empty | Its path prefixes every route, `/api/amp/v1` and the probes included |
| `--web.config.file` | empty | Upstream web config: basic auth |
| `--cluster.listen-address` | `0.0.0.0:9094` | Empty turns gossip off |
| `--cluster.peer`, `--cluster.advertise-address`, `--cluster.peer-timeout`, `--cluster.label`, `--cluster.tls-config`, … | upstream's | HA |

Not available, unlike upstream: `--silences.max-silences`, `--silences.max-silence-size-bytes`, `--alerts.per-alertname-limit`, `--dispatch.start-delay`, and the UI flags.

## With the Helm chart

The chart writes both files from its values ([helm/amp/README.md](../helm/amp/README.md)):

| Value | Becomes |
|---|---|
| `alertmanager.config` | `alertmanager.yml` in ConfigMap `<fullname>`, verbatim |
| `profile`, `environment` | `profile`, `app.environment` |
| `externalDatabase.*` or the bundled `postgresql.*` | `database.*`; the password goes to `DATABASE_PASSWORD` from a Secret |
| `llm.enabled`, `provider`, `proxyUrl`, `model`, `timeout`, `maxRetries`, `agentMode`, `notifyWait`, `dropNoise` | `llm.*` (`proxyUrl` → `base_url`); the key goes to `LLM_API_KEY` from a Secret |
| `investigation.enabled`, `workerCount`, `queueSize`, `maxRetries`, `retryInterval`, `llmTimeout`, `onlyFiring` | `investigation.*` |
| `publishing.enabled`, `discovery.namespace` (default: the release namespace), `discovery.labelSelector`, `refresh.interval` | `publishing.*` |
| `alertmanager.ampConfig` | Merged over all of the above, for keys without a value of their own: `investigation.tools`, `investigation.runbooks`, `server.auth`, … |
| `webConfig.existingSecret` | `--web.config.file` |
| `alertmanager.replicas`, `externalURL`, `peerTimeout`, `persistence`, `extraArgs` | Flags and the StatefulSet |

Both ConfigMaps are checksummed in the pod template, so a change of either rolls the pods. The rendered AMP file:

```bash
helm template amp ./helm/amp -f values.yaml -s templates/amp-config.yaml
```

`alertmanager.ampConfig` is a ConfigMap: no secrets there. Put them in `extraEnv` from a Secret (`INVESTIGATION_TOOLS_DATABASE_DSN`, …).

Values of the chart before v0.1.0 (`configFile`, `grouping`, `cache`, `server`, `storage`, …) fail the render with a message naming the replacement; the full table is in [MIGRATION_FROM_LEGACY_CORE.md](MIGRATION_FROM_LEGACY_CORE.md#step-2-convert-the-helm-values).

## Kubernetes-Secret targets

Format, scoping and conversion to upstream integrations: [ALERTMANAGER_COMPATIBILITY.md](ALERTMANAGER_COMPATIBILITY.md#kubernetes-secret-targets). With `rootly.enabled` the chart creates one such Secret for a Rootly webhook.
