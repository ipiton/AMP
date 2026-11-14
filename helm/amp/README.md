# AMP (Alertmanager++) Helm Chart

[![Artifact Hub](https://img.shields.io/endpoint?url=https://artifacthub.io/badge/repository/amp)](https://artifacthub.io/packages/search?repo=amp)

## Overview

Alertmanager++ (AMP) chart deploys one workload, `amp-upstream`: upstream Prometheus Alertmanager v0.34.1, embedded as a library, with AMP's own layer on top (ADR-019):
- ✅ **Upstream Alertmanager core**: `api/v2`, routing, grouping, inhibition, silences, notifications and the gossip cluster, configured with an upstream `alertmanager.yml`
- 🗄️ **Alert history and investigations** in PostgreSQL (profile `standard`)
- 🤖 **Optional LLM classification and investigation** (BYOK, off by default)
- 🔌 **Publishing targets from Kubernetes Secrets**, appended to the receivers of `alertmanager.yml`

The breaking changes against earlier chart versions are listed in `CHANGELOG.md`.

## Quick Start

> **Read before installing.** The chart defaults start. LLM is off by default (`llm.enabled: false`); to turn it on, set `llm.enabled: true` together with `llm.apiKey` (or `externalSecrets.enabled` with `llm_api_key` in the store) — without a key the render fails instead of leaving a pod in `CreateContainerConfigError`. The bundled PostgreSQL has no TLS, so with it the chart writes `ssl_mode: disable` plus `allow_insecure_ssl_in_production: true` into the AMP configuration: under `environment: production` (the default) AMP then accepts the unencrypted in-cluster connection. The chart never sets that flag for an external database, and AMP still rejects `sslmode=disable` in production without it. External PostgreSQL: set `postgresql.enabled: false` and `externalDatabase.*`, with the password in a Secret you create (see [PostgreSQL](#postgresql)). With `postgresql.enabled: false` and no database configured, the render fails. `values-production.yaml` uses an external database and does not render until `externalDatabase` is filled in. The defaults target one node: one replica, about 210m CPU / 416Mi requested in total (see [Sizing](#sizing)). The image is `ghcr.io/ipiton/amp:<appVersion>` from tagged releases (see [CI And Image Publishing](../../docs/CI.md)). The chart's `ServiceMonitor`s and `PrometheusRule`s render only on a cluster that serves the prometheus-operator CRDs (kube-prometheus-stack); elsewhere they are skipped. With `helm template` and no cluster, see `monitoring.assumeCRDs` under [Network Exposure](#network-exposure). Set your own `postgresql.password` for the bundled PostgreSQL: the default is published in this repository.

```yaml
# my-values.yaml — standard profile, one replica (the defaults)
alertmanager:
  config:                 # alertmanager.yml, upstream format
    route:
      receiver: default
      group_by: ['alertname']
    receivers:
      - name: default
        webhook_configs:
          - url: https://example.invalid/hook
    inhibit_rules: []
```

```bash
helm install amp ./helm/amp -f my-values.yaml
# about 1 vCPU / 1Gi free: add the chart's small-node file
helm install amp ./helm/amp -f ./helm/amp/values-small.yaml -f my-values.yaml
```

`values-small.yaml` is for new installs: it shrinks the StatefulSet volumes, which Kubernetes does not allow on an existing release.

### Sizing

Requests are set from a measurement on one node under light load (`tasks/archive/HELM-SINGLE-NODE-DEFAULTS/evidence/idle-top.txt`: the whole stack used about 50m CPU / 90Mi); limits leave room for bursts.

| Container | Requests | Limits |
|-----------|----------|--------|
| AMP (`amp-upstream`) | 100m / 128Mi | 1000m / 1Gi |
| PostgreSQL | 100m / 256Mi | 1000m / 1Gi |
| postgres-exporter | 10m / 32Mi | 100m / 128Mi |
| **Total** | **210m / 416Mi** | |

With `valkey.enabled: true` add Valkey (`<fullname>-redis`, 50m / 64Mi requests, 500m / 512Mi limits) and its redis-exporter (10m / 32Mi, 100m / 128Mi): 270m / 512Mi in total.

Volumes: PostgreSQL 10Gi, AMP 1Gi per pod (`alertmanager.persistence`), Valkey 5Gi when enabled. `values-small.yaml` lowers the limits (AMP, PostgreSQL 512Mi; Valkey 256Mi with `maxmemory 192mb` if enabled), PostgreSQL to `max_connections=60` and `work_mem=2MB`, and the volumes to 5Gi / 1Gi; keep `alertmanager.replicas: 1` with it.

There is no HPA: the replica count is `alertmanager.replicas`, a fixed gossip cluster ([Alertmanager Core](#alertmanager-core)). For HA use `values-production.yaml` (3 replicas, PodDisruptionBudget `minAvailable: 2`, external PostgreSQL) or set `alertmanager.replicas` yourself; with the bundled PostgreSQL, check its connection budget first ([PostgreSQL](#postgresql)).

## Deployment Profiles

### Standard Profile (Default)
The core plus AMP's layer:
```bash
helm install amp ./helm/amp
```
- PostgreSQL for alert history and investigations (single primary, no replication — see `PROD-POSTGRES-HA-DECISION`)
- LLM classification and investigation when `llm.enabled` / `investigation.enabled` are set
- Publishing targets discovered from Kubernetes Secrets (`publishing.*`, see [RBAC](#rbac))
- No Valkey by default: `amp-upstream` does not use it yet ([Valkey](#valkey))
- If PostgreSQL is unreachable when a pod starts, the pod runs without history and investigations until it is restarted; alert delivery does not depend on the database

### Lite Profile
The upstream core alone:
```bash
helm install amp ./helm/amp --set profile=lite --set postgresql.enabled=false
```
- No alert history, investigations or K8s-Secret targets; no Valkey, no RBAC, no service account token
- Silences and the notification log still persist on the pod's volume (`alertmanager.persistence`)
- The bundled PostgreSQL is not tied to the profile: set `postgresql.enabled=false`, or it keeps running unused
- Perfect for: dev, testing, smoke checks, a plain Alertmanager

## Configuration

### Basic Values

| Parameter | Description | Default |
|-----------|-------------|---------|
| `profile` | Deployment profile (lite/standard) | `standard` |
| `environment` | `app.environment` of the AMP configuration; `production` tightens the database checks | `production` |
| `image.repository` | Image repository | `ghcr.io/ipiton/amp` |
| `image.tag` | Image tag | `""` (defaults to `.Chart.AppVersion`) |
| `service.port` | Service port (`web`) | `9093` |
| `resources` | Resources of the `amp` container | see [Sizing](#sizing) |
| `podDisruptionBudget.enabled` / `.minAvailable` | PDB for the AMP pods | `false` / `1` |

### Alertmanager Core

The chart's one workload is StatefulSet `<fullname>` running `amp-upstream` (pods labelled `app.kubernetes.io/component: application`). It reads two files, both mounted from ConfigMaps at `/etc/amp/config`:

- `alertmanager.yml` (`--config.file`), ConfigMap `<fullname>`: `alertmanager.config`, in the upstream Alertmanager format (`route`, `receivers`, `inhibit_rules`, `templates`, ...). Routing lives only here.
- `amp.yaml` (`--amp.config.file`), ConfigMap `<fullname>-amp-config`: generated from `profile`, `environment`, the database values, `llm`, `investigation` and `publishing`, then `alertmanager.ampConfig` is merged over it (its keys win). It is a ConfigMap: no secrets there. Passwords come from Secrets as environment variables (`DATABASE_PASSWORD`, `LLM_API_KEY`).

```yaml
alertmanager:
  replicas: 3          # > 1: a memberlist gossip cluster, port 9094 TCP+UDP
  config:              # alertmanager.yml, upstream format
    route:
      receiver: default
    receivers:
      - name: default
  persistence:
    size: 1Gi          # silences and notification log snapshots
```

| Parameter | Description | Default |
|-----------|-------------|---------|
| `alertmanager.replicas` | Pods in the StatefulSet; `0` fails the render | `1` |
| `alertmanager.config` | `alertmanager.yml`, upstream format | a `default` receiver without integrations |
| `alertmanager.ampConfig` | Merged over the generated `amp.yaml` | `{}` |
| `alertmanager.externalURL` | `--web.external-url`; empty: `http://<fullname>.<namespace>.svc:<service.port>/` | `""` |
| `alertmanager.peerTimeout` | `--cluster.peer-timeout` | `15s` |
| `alertmanager.persistence.size` / `.storageClass` / `.accessMode` | volumeClaimTemplate `data`, mounted at `/data` (`--storage.path`) | `1Gi` / `""` / `ReadWriteOnce` |
| `alertmanager.extraArgs` | Further `amp-upstream` flags | `[]` |

- With `replicas: 1` the cluster is off (`--cluster.listen-address=`). With more, every pod lists all peers (`--cluster.peer`) by their stable DNS names in the headless Service `<fullname>-headless` (`app.kubernetes.io/component: cluster`, `publishNotReadyAddresses`), and silences and the notification log are gossiped, so an alert sent to every replica is notified once.
- With more than one replica, Prometheus must send every alert to every pod (`<fullname>-<i>.<fullname>-headless.<namespace>.svc:9093`), not to one through the Service `<fullname>`, as with upstream Alertmanager HA. NOTES prints the addresses.
- With `llm.enabled` and more than one replica every pod classifies every alert: N LLM calls per alert until `UPSTREAM-AI-HA-DEDUP`. NOTES warns about it.
- `externalURL` is also what links in notifications (silence URLs) point to. Its path becomes the route prefix of every endpoint, the probes and the ServiceMonitor path included.
- `extraArgs` passes further flags, e.g. `--cluster.label=<name>` or `--cluster.tls-config=<file>`. Gossip is unencrypted unless `--cluster.tls-config` is set.
- A change of `alertmanager.config`, the generated `amp.yaml` or the chart's Secret rolls the pods on `helm upgrade` (checksum annotations). A StatefulSet rolls one pod at a time, without a surge pod.
- The `data` volumes are not removed with the release (see [Uninstalling](#uninstalling)).

### LLM Configuration (BYOK)

| Parameter | Description | Default |
|-----------|-------------|---------|
| `llm.enabled` | Enable LLM classification; needs `llm.apiKey` or `externalSecrets.enabled` | `false` |
| `llm.provider` | LLM provider (openai/anthropic/azure) | `openai` |
| `llm.proxyUrl` | `llm.base_url` | `https://llm-proxy.example.com` |
| `llm.apiKey` | API key, stored in `<fullname>-secrets` (`llm-api-key`, env `LLM_API_KEY`); use a secret store in production | `""` |
| `llm.model` | Model name | `openai/gpt-4o` |
| `llm.timeout` / `llm.maxRetries` | Per call: seconds or a Go duration / retries | `30` / `3` |
| `llm.agentMode` | Agentic investigation loop (tools, runbooks) instead of a single call | `false` |
| `llm.notifyWait` | How long a notification waits for the alert's classification before it goes out without it | `5s` |
| `llm.dropNoise` | Drop notifications for alerts classified as noise | `false` |
| `investigation.enabled` | Async investigation queue; needs profile standard, PostgreSQL and `llm.enabled` | `false` |
| `investigation.workerCount` / `.queueSize` / `.maxRetries` / `.retryInterval` / `.llmTimeout` / `.onlyFiring` | Queue settings | `3` / `500` / `3` / `5s` / `60s` / `true` |

Investigation tools and runbooks are configured through `alertmanager.ampConfig` (`investigation.tools`, `investigation.runbooks`).

### Storage

| Parameter | Description | Default |
|-----------|-------------|---------|
| `alertmanager.persistence.*` | Silences and notification log, one volume per AMP pod | `1Gi` |
| `postgresql.enabled` | Enable bundled PostgreSQL; the lite profile does not use it, set `false` there | `true` |
| `externalDatabase.*` | External PostgreSQL ([PostgreSQL](#postgresql)) | `host: ""` |
| `valkey.enabled` | Render Valkey and its objects (profile standard); unused by `amp-upstream` today | `false` |
| `valkey.replicas` | Valkey pods with `valkey.enabled`; `0` stops the pod, its other objects still render | `1` |
| `valkey.image.repository` / `.tag` | Valkey image | `valkey/valkey` / `8.1.10-alpine` |
| `valkey.password.existingSecret` / `.secretKey` | Secret with the chart's Valkey password (otherwise `<fullname>-redis-secret`) | `""` / `password` |
| `valkey.password.value` | Password for `<fullname>-redis-secret`; empty: generated on first install and kept by upgrades | `""` |

### Valkey

`amp-upstream` does not connect to Valkey: it is kept for the cross-replica investigation dedup (`UPSTREAM-AI-HA-DEDUP`). The chart does not deploy it by default (`valkey.enabled: false`). With `valkey.enabled: true` (standard profile) it runs with a password and NOTES says it is unused; `valkey.replicas: 0` then stops its pod while its Services, Secret, ServiceMonitor and alert rules still render (HELM-CHART-GAPS).

### PostgreSQL

The standard profile uses PostgreSQL for alert history and investigations; the chart ships and tests 16 (`postgres:16.15`). The history is written off the ingest path and is best-effort: alert delivery never waits for the database. If the database is unreachable when a pod starts, that pod runs without history and investigations until it is restarted.

| Topology | Use for | Backups / HA |
|----------|---------|--------------|
| External PostgreSQL (16 tested): a managed service with PITR, or CloudNativePG with barman/S3 | production | operator's side |
| Bundled (`postgresql.enabled: true`) | dev, pilot | none |

The bundled PostgreSQL (`postgres:16.15`) is a single instance without replication. It has no PodDisruptionBudget and no backup job. A pod restart, node drain or upgrade that touches it means database downtime. The chart fails to render if `postgresql.replicas` is not `1` (several replicas would be independent databases behind one Service) or if `postgresql.backup.enabled` is `true` (the bundled backup was removed).

The server runs with `postgresql.config` (rendered into `postgresql.conf`) and the chart's `pg_hba.conf`, passed as server arguments, so they also apply to an existing data volume. Each AMP pod opens up to 25 connections (`database.max_connections`), so `postgresql.config.maxConnections` must be at least `alertmanager.replicas` × 25 + 5 (the exporter and a few `psql` sessions). AMP connects as `postgresql.username`, the bootstrap superuser, so `superuserReservedConnections` holds nothing back from it. The default 100 covers 3 AMP pods (a StatefulSet rolls without a surge pod) and the exporter; NOTES warns when the budget is short. Before raising `alertmanager.replicas` above 3, raise it to at least `replicas` × 25 + 5, together with `sharedBuffers` and `postgresql.resources` (the default limit is 1Gi with `shared_buffers=128MB`). From the network, `pg_hba.conf` admits only `postgresql.username` to `postgresql.database` with a SCRAM password and rejects everything else, including replication; loopback inside the pod (and `kubectl port-forward`) needs the password too, only `kubectl exec … psql` over the Unix socket does not; to restrict which pods may connect, enable `postgresql.networkPolicy.enabled`. `/dev/shm` is a 256Mi memory volume and counts against `postgresql.resources.limits.memory`.

#### External PostgreSQL

Create the database and an owner role for AMP (no superuser rights needed: AMP's migrations create only the trusted extensions `uuid-ossp` and `btree_gin`), then a Secret with the role's password in the release namespace. The chart never creates this Secret. Create it from a file without a trailing newline: `--from-file` keeps a `\n` as part of the password, and `--from-literal` puts the password into shell history:

```bash
read -rs AMP_DB_PASSWORD                                # prompts; not echoed, not in history
(umask 077; printf '%s' "$AMP_DB_PASSWORD" > ./amp-db-password)    # no trailing newline
kubectl -n monitoring create secret generic amp-db --from-file=password=./amp-db-password
rm ./amp-db-password
```

```yaml
postgresql:
  enabled: false
externalDatabase:
  host: db.example.com
  port: 5432
  database: amp
  username: amp
  sslMode: require          # disable | require | verify-ca | verify-full
  existingSecret: amp-db
  existingSecretPasswordKey: password
```

The chart writes host, port, database, username and `ssl_mode` into the `database:` section of `amp.yaml` (`<fullname>-amp-config`), and `DATABASE_PASSWORD` comes from the Secret through `secretKeyRef`. The render fails if `host` is set without `database`, `username` or `existingSecret`, if `sslMode` is not one of the four values AMP accepts (libpq's `allow` and `prefer` are not), or if `postgresql.enabled` is still `true`.

- A missing Secret or key is not caught at render time (the chart does not read the cluster): the pod stays in `CreateContainerConfigError`.
- The password reaches AMP as an environment variable, read at pod start. After changing the Secret, run `kubectl -n <namespace> rollout restart statefulset/<fullname>`.
- With `externalSecrets.enabled`, the chart's ExternalSecret maps database keys only for the bundled PostgreSQL. Create your own ExternalSecret whose target Secret is `externalDatabase.existingSecret`.
- Each AMP pod opens up to 25 connections (`database.max_connections` default). Size `max_connections` on the server for `alertmanager.replicas` × 25: `values-production.yaml` runs 3 replicas, up to 75 connections.
- Monitoring the external database (alerts, exporter) is on the operator's side: the chart's PostgreSQL rules and exporter cover only the bundled one.
- Under `environment: production` AMP refuses `sslMode: disable` and passwords shorter than 12 characters. The chart never sets the insecure-SSL override for an external database.
- `verify-ca`/`verify-full` check the server certificate against the image's system roots only. A provider CA (RDS and the like) is not among them, and the chart mounts no client TLS material: use `require`, or an image with that CA.
- `alertmanager.ampConfig.database` is merged over the section the chart generates, so its keys win over `externalDatabase`.
- Fallback without a Secret: leave `externalDatabase.host` empty and put the connection into `alertmanager.ampConfig.database` (`host`, `port`, `database`, `username`, `password`, `ssl_mode`). The password then sits in plain text in a ConfigMap; NOTES warns about it.
- Moving an installation from the bundled to an external database: [docs/POSTGRESQL_RESTORE_GUIDE.md § 3c](docs/POSTGRESQL_RESTORE_GUIDE.md#3c-move-from-the-bundled-to-an-external-postgresql).

Backup and restore with `pg_dump`/`pg_restore`, for both topologies: [docs/POSTGRESQL_RESTORE_GUIDE.md](docs/POSTGRESQL_RESTORE_GUIDE.md). The decision record is ADR-016 in `docs/06-planning/DECISIONS.md`.

### HTTP Authentication

The API is unauthenticated unless a web config is mounted (AMP logs a `WARN` at startup). Put an upstream-format `web-config.yml` (`basic_auth_users` with bcrypt hashes) into a Secret and point the chart at it; the chart passes it as `--web.config.file`:

```bash
kubectl create secret generic amp-web-config --from-file=web-config.yml
helm upgrade --install amp ./helm/amp --set webConfig.existingSecret=amp-web-config
```

| Parameter | Description | Default |
|-----------|-------------|---------|
| `webConfig.existingSecret` | Secret with the web config; enables basic auth | `""` |
| `webConfig.secretKey` | Key inside the Secret | `web-config.yml` |
| `webConfig.mountPath` | Directory the Secret is mounted at | `/etc/amp/web` |

Liveness/readiness probes use `/-/healthy` and `/-/ready` (under the `alertmanager.externalURL` path), which stay reachable without credentials (`server.auth.unauthenticated_paths`, settable through `alertmanager.ampConfig`). Password edits in the Secret apply without a pod restart once kubelet syncs the volume. See `docs/CONFIGURATION_GUIDE.md` → "Enable HTTP Authentication".

### Receiver Credentials From A Secret

`alertmanager.config` is rendered into a ConfigMap, so a `bot_token` or Slack webhook URL written there is stored in plain text. Put the credentials into a Secret you create, mount it with `extraVolumes` / `extraVolumeMounts`, and point the receivers at the files through their `*_file` fields:

```bash
# one file per credential
kubectl create secret generic amp-receiver-secrets \
  --from-file=telegram-bot-token=./telegram-bot-token \
  --from-file=slack-webhook-url=./slack-webhook-url
```

```yaml
extraVolumes:
  - name: receiver-secrets
    secret:
      secretName: amp-receiver-secrets
      defaultMode: 0440
extraVolumeMounts:
  - name: receiver-secrets
    mountPath: /etc/amp/secrets
    readOnly: true
alertmanager:
  config:
    route:
      receiver: slack
    receivers:
      - name: slack
        slack_configs:
          - api_url_file: /etc/amp/secrets/slack-webhook-url
            channel: '#alerts'
      - name: telegram-panic
        telegram_configs:
          - bot_token_file: /etc/amp/secrets/telegram-bot-token
            chat_id: -1001234567890
```

| Parameter | Description | Default |
|-----------|-------------|---------|
| `extraVolumes` | Extra pod volumes (any volume type: Secret, ConfigMap, projected, CSI) | `[]` |
| `extraVolumeMounts` | Extra mounts for the `amp` container | `[]` |

- Use absolute paths in `*_file` fields. Setting both a field and its `*_file` variant (`bot_token` and `bot_token_file`) is a config error, as in upstream Alertmanager.
- A directory below the chart's own mounts (`/etc/amp/secrets`) is fine. A volume name or `mountPath` the chart already uses fails the render: volumes `config`, `data` and (with `webConfig.existingSecret`) `web-config`; paths `/etc/amp/config` and its `alertmanager.yml` / `amp.yaml`, `/data`, `webConfig.mountPath` and the web config file in it.
- With the chart's default `podSecurityContext` (`runAsUser: 65534`, `fsGroup: 65534`) the files stay readable with a strict `defaultMode` such as `0440`: kubelet gives them the `fsGroup` group. If you override `podSecurityContext` without `fsGroup`, keep the default mode (`0644`).
- After changing the Secret, wait for kubelet to sync the volume (about a minute), then `POST /-/reload` to every pod (authenticated when `webConfig` is enabled) or `kubectl rollout restart statefulset/<fullname>`. Mounts with `subPath` are never updated.
- The same keys mount a ConfigMap with runbooks or any other file AMP should read.

### Graceful Shutdown

On termination the pod first runs a preStop `sleep` while Kubernetes removes it from Service endpoints, then `amp-upstream` gets `SIGTERM`: in-flight HTTP requests drain (up to 5s), the core stops and writes its final silence and notification-log snapshots, then AMP's workers stop. Details: `docs/CONFIGURATION_GUIDE.md` → "Graceful Shutdown".

| Parameter | Description | Default |
|-----------|-------------|---------|
| `gracefulShutdown.terminationGracePeriodSeconds` | Total budget before `SIGKILL`, counted from the start of preStop | `40` |
| `gracefulShutdown.preStopDelay` | Seconds the preStop hook sleeps; `0` removes the hook | `5` |

Keep `terminationGracePeriodSeconds` well above `preStopDelay` + 5s. Below `preStopDelay` + 10 install/upgrade still succeeds, but the notes print a `WARNING`: the kubelet may `SIGKILL` the pod before the drain and the final snapshot.

Limitations:

- The hook runs `sleep` from the image (`alpine` today). In an image without `sleep` the hook fails, the kubelet logs `FailedPreStopHook` and sends `SIGTERM` immediately: no delay, the pod still stops.

### RBAC

The chart grants the AMP ServiceAccount exactly what the app calls: publishing target discovery lists Secrets labelled `publishing.discovery.labelSelector` in **one** namespace. It renders one `Role` (`list` on `secrets`) and its `RoleBinding`, and nothing cluster-scoped. The AMP pods mount a service account token only when discovery is on.

| Condition | RBAC rendered |
|-----------|---------------|
| `profile: standard`, `publishing.enabled: true`, `serviceAccount.create: true`, `serviceAccount.rbac.create: true` | `Role` + `RoleBinding` `<fullname>-secrets-lister` in `publishing.discovery.namespace` (default: the release namespace) |
| `profile: lite`, or any of the three flags `false` | none — the pods never talk to the Kubernetes API |

- **Keep target Secrets in their own namespace.** RBAC cannot restrict `list` to a label selector, so the Role can read every Secret in the discovery namespace — including the chart's own database and LLM credentials when that is the release namespace. Setting `publishing.discovery.namespace` to a dedicated namespace keeps them out of reach. The Role is then created in that namespace, so whoever runs `helm install` needs permission to create Roles there.
- **Managing RBAC yourself**: set `serviceAccount.rbac.create=false` and grant the same `list secrets` in the discovery namespace. Without it discovery fails with `403`, AMP logs a `WARN` and runs without discovered targets (the receivers of `alertmanager.config` keep working); it retries every `publishing.refresh.interval`.
- The Valkey StatefulSet shares the ServiceAccount but does not mount its token.
- Verify: `kubectl auth can-i list secrets -n <discovery-namespace> --as=system:serviceaccount:<release-namespace>:<serviceaccount>` → `yes`; the same for `get`, or for any other namespace → `no`.

### Network Exposure

The whole API — including `POST /api/v2/silences` and `POST /-/reload` — sits on one port (`web`, 9093), so whoever reaches it can silence every alert. The chart closes the two ways in, and fails the render instead of guessing (ADR-015).

**Ingress requires authentication.** `ingress.enabled: true` renders only with one of:

- `webConfig.existingSecret` — in-process basic auth (see HTTP Authentication above);
- `ingress.externalAuth: true` — your controller or a proxy enforces auth. The chart does not check this; it is your statement. Add the controller's auth annotations, for ingress-nginx for example:

  ```yaml
  ingress:
    externalAuth: true
    annotations:
      nginx.ingress.kubernetes.io/auth-type: basic
      nginx.ingress.kubernetes.io/auth-secret: amp-ingress-auth   # htpasswd file
      # or oauth2-proxy:
      # nginx.ingress.kubernetes.io/auth-url: https://oauth2.example.com/oauth2/auth
      # nginx.ingress.kubernetes.io/auth-signin: https://oauth2.example.com/oauth2/start?rd=$escaped_request_uri
  ```

  The NetworkPolicy below admits the controller's pods, not this one Ingress: any other Ingress on the same controller can route to AMP without your auth annotations — from the same namespace, or from anywhere through an `ExternalName` Service unless ingress-nginx runs with `--disable-svc-external-name`. Prefer `webConfig.existingSecret`: it does not depend on the controller.

The guard covers the Ingress only. `service.type: NodePort` or `LoadBalancer` (`values-dev.yaml` uses NodePort) exposes the API without either check — pair it with `webConfig.existingSecret`.

**NetworkPolicy for the AMP pods.** `networkPolicy.enabled: true` admits traffic to the `web` port from the listed sources only, plus gossip (9094 TCP/UDP) between the AMP pods when `alertmanager.replicas` > 1. Egress is not restricted: notification targets are arbitrary.

| Parameter | Who | Default |
|-----------|-----|---------|
| `networkPolicy.ingressController` | ingress controller pods; used only with `ingress.enabled` | `[]` |
| `networkPolicy.alertSenders` | Prometheus / vmalert posting to the pods directly | `[]` |
| `networkPolicy.metricsScrapers` | the Prometheus scraping `/metrics` | `[]` |
| `networkPolicy.extraIngress` | raw `NetworkPolicyIngressRule`s, rendered as-is | `[]` |

Each list holds `NetworkPolicyPeer`s. Select namespaces by the automatic `kubernetes.io/metadata.name` label:

```yaml
networkPolicy:
  enabled: true
  ingressController:
    - namespaceSelector: {matchLabels: {kubernetes.io/metadata.name: ingress-nginx}}
  alertSenders:
    - namespaceSelector: {matchLabels: {kubernetes.io/metadata.name: monitoring}}
      podSelector: {matchLabels: {app.kubernetes.io/name: prometheus}}
  metricsScrapers:
    - namespaceSelector: {matchLabels: {kubernetes.io/metadata.name: monitoring}}
      podSelector: {matchLabels: {app.kubernetes.io/name: prometheus}}
```

- An enabled policy with no effective source fails the render: it would drop every alert sender.
- Senders outside the cluster that come through the Ingress are covered by `ingressController`. Through a LoadBalancer or NodePort they arrive from node or LB addresses: add an `ipBlock`.
- An ingress controller with `hostNetwork: true` connects from the node IPs, which no `namespaceSelector` matches: list the node CIDR as an `ipBlock` in `ingressController`. Whether an `ipBlock` matches node traffic depends on the CNI (Cilium, for one, classifies it as `host`/`remote-node`, not by CIDR): confirm with the check below.
- Probes come from the kubelet on the node and are not affected on conformant CNIs. If pods start failing `Readiness probe failed: ... timeout` right after enabling the policy, your CNI filters node traffic: add the node CIDR as an `ipBlock` in `extraIngress`.
- On a CNI without NetworkPolicy support the object is accepted and does nothing. Check after install, from both sides (`9093` is the default `service.port`; with an `alertmanager.externalURL` path, prefix `/-/healthy` with it; run the first check from a namespace you did not list):

  ```bash
  SVC=$(kubectl get svc -n <namespace> -l app.kubernetes.io/instance=<release>,app.kubernetes.io/component=application -o name)
  # from a namespace that is not listed: must fail (timeout or connection refused, depending on the CNI)
  kubectl run np-check -n default --rm -it --image=curlimages/curl --restart=Never -- \
    curl -m 5 http://${SVC#service/}.<namespace>:9093/-/healthy
  # from a listed source, e.g. the Prometheus pod: must answer
  kubectl exec -n monitoring <prometheus-pod> -- wget -qO- -T 5 http://${SVC#service/}.<namespace>:9093/-/healthy
  ```

- **If alerts stop arriving**, the sender sees it first: Prometheus `rate(prometheus_notifications_errors_total[5m]) > 0` or `prometheus_notifications_dropped_total` growing; vmalert `rate(vmalert_alerts_send_errors_total[5m]) > 0`. Kill-switch, no pod restart: `helm upgrade <release> ./helm/amp --reuse-values --set networkPolicy.enabled=false` (on a release already running this chart version; see the migration notes in `CHANGELOG.md` for the first upgrade).

AMP pods carry `app.kubernetes.io/component: application` (the postgres/redis pods share the chart's selector labels and have their own component; the headless Service has `cluster`), so external policies can select them too.

**ServiceMonitor.** Rendered when `monitoring.prometheusEnabled` and `monitoring.serviceMonitor.enabled` are both true (default) and the cluster serves the prometheus-operator CRDs (`monitoring.coreos.com/v1`). Without the CRDs every ServiceMonitor and PrometheusRule of the chart is skipped and `NOTES.txt` says so, instead of failing the install. `helm template` without a cluster does not see the CRDs: pass `--api-versions monitoring.coreos.com/v1` or set `monitoring.assumeCRDs: true` (Argo CD and Flux pass the cluster's API versions themselves). It scrapes `/metrics` (under the `alertmanager.externalURL` path) on the `web` port of every AMP pod behind the Service `<fullname>`, the core's `alertmanager_*` metrics included.

| Parameter | Description | Default |
|-----------|-------------|---------|
| `monitoring.assumeCRDs` | render ServiceMonitors/PrometheusRules even when the prometheus-operator CRDs are not detected (`helm template` without a cluster) | `false` |
| `monitoring.serviceMonitor.enabled` | render the AMP ServiceMonitor | `true` |
| `monitoring.serviceMonitor.labels` | extra labels, e.g. what your Prometheus `serviceMonitorSelector` matches; also on the Valkey ServiceMonitor | `{}` |
| `monitoring.prometheusRule.labels` | extra labels on the chart's `PrometheusRule`s (PostgreSQL, Valkey), e.g. what your Prometheus `ruleSelector` matches | `{}` |
| `monitoring.serviceMonitor.interval` / `scrapeTimeout` | scrape timing | `30s` / `10s` |
| `monitoring.serviceMonitor.basicAuth.secretName` | Secret with the plain username/password; **required** with `webConfig.existingSecret` whenever the ServiceMonitor renders (CRDs served or `monitoring.assumeCRDs`) | `""` |
| `monitoring.serviceMonitor.basicAuth.usernameKey` / `passwordKey` | keys in that Secret | `username` / `password` |

With web auth on, `/metrics` needs credentials and the render fails without them. Create the Secret in the namespace AMP runs in (`namespace`, by default the release namespace) from the plain password of a `web-config.yml` user (not its bcrypt hash):

```bash
kubectl create secret generic amp-metrics-auth -n <namespace> \
  --from-literal=username=prometheus --from-literal=password='<plain password>'
helm upgrade amp ./helm/amp --reuse-values \
  --set monitoring.serviceMonitor.basicAuth.secretName=amp-metrics-auth
```

With a NetworkPolicy, list that Prometheus in `networkPolicy.metricsScrapers` (or `alertSenders`), or its scrapes time out.

**`values-production.yaml`** enables the Ingress and the NetworkPolicy and ships none of the above, so it renders only once you set: `webConfig.existingSecret` (or `ingress.externalAuth`), `monitoring.serviceMonitor.basicAuth.secretName` (with `webConfig`, on a cluster with the prometheus-operator CRDs), and at least one NetworkPolicy source. The render reports one missing setting at a time.

## Alertmanager Compatibility

The chart runs upstream Alertmanager v0.34.1 as the core, so `api/v2`, `/-/healthy`, `/-/ready`, `/-/reload` and `/metrics` are upstream's own handlers, under the `alertmanager.externalURL` path, and `alertmanager.config` is read as upstream reads `alertmanager.yml`. The Service port is 9093, as with upstream Alertmanager.

```yaml
# prometheus.yml — every pod, as with upstream Alertmanager HA
alerting:
  alertmanagers:
    - static_configs:
        - targets:
          - amp-0.amp-headless.<namespace>.svc:9093
          - amp-1.amp-headless.<namespace>.svc:9093
          - amp-2.amp-headless.<namespace>.svc:9093
```

With one replica, the Service `<fullname>.<namespace>.svc:9093` is enough. With kube-prometheus-stack, add AMP as a second endpoint next to the existing Alertmanager (`prometheus.prometheusSpec.additionalAlertManagerConfigs`) and point AMP's receivers at test channels until you have compared both. More on compatibility: [docs/ALERTMANAGER_COMPATIBILITY.md](../../docs/ALERTMANAGER_COMPATIBILITY.md). A Prometheus with a `ruleSelector` loads the chart's `PrometheusRule`s only with the matching label: kube-prometheus-stack selects `release: <its release name>`, so set `monitoring.prometheusRule.labels.release` to it.

## Upgrading

```bash
helm upgrade amp ./helm/amp --reuse-values
```

From a chart before v0.1.0 (AMP's own core, a Deployment on port 8080), follow [docs/MIGRATION_FROM_LEGACY_CORE.md](../../docs/MIGRATION_FROM_LEGACY_CORE.md): the values, the port, the probes and the silences change.

`--reuse-values` keeps the values of the old chart version, defaults included. When a chart version removes or renames keys, read the migration notes in `CHANGELOG.md` first. From a chart before `UPSTREAM-CORE-REMOVAL` (no `alertmanager.*` section) the render fails with `--reuse-values`: use `--reset-then-reuse-values` (Helm ≥ 3.14) or a values file for this chart; a removed key left in your values also fails and names its replacement. Upgrading from a version that still had the bundled PostgreSQL backup (`postgresql.backup`) fails with `--reuse-values`: use `--reset-then-reuse-values` (Helm ≥ 3.14), or add `--set postgresql.backup.enabled=false`.

Upgrading a `profile: standard` release installed by a chart from before `HELM-VCT-CHART-LABELS` fails with `StatefulSet.apps "<fullname>-redis" is invalid: spec: Forbidden`: the labels of its volume claim template changed, and that field cannot be updated. Delete the StatefulSet object once, keeping its pod and volume, then upgrade:

```bash
kubectl -n <namespace> delete statefulset <fullname>-redis --cascade=orphan
helm upgrade amp ./helm/amp --reset-then-reuse-values
```

## Uninstalling

```bash
helm uninstall amp
```

The volumes created from the StatefulSets' volumeClaimTemplates (`data-<fullname>-<i>` with silences and the notification log, and those of PostgreSQL and Valkey) stay; delete their PVCs yourself if you no longer need them.

## License

AGPL-3.0 License - see [LICENSE](https://github.com/ipiton/AMP/blob/main/LICENSE)
