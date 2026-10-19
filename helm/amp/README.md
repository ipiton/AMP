# AMP (Alertmanager++) Helm Chart

[![Artifact Hub](https://img.shields.io/endpoint?url=https://artifacthub.io/badge/repository/amp)](https://artifacthub.io/packages/search?repo=amp)

## Overview

Alertmanager++ (AMP) chart packages the current repository runtime with:
- ✅ **Controlled replacement surface** for alert ingest/query, alert groups, `status`/`receivers`/`reload`, silence CRUD, health/readiness, metrics, and the real publishing path
- 🤖 **Optional LLM-related values** for environments that wire them explicitly
- 📊 **Partial dashboard surface** with broader UI/runtime parity still tracked as follow-up work
- 🟡 **Phased compatibility** rather than a verified full Alertmanager drop-in claim

## Quick Start

> **Read before installing (status 2026-10-16).** The chart defaults start. LLM is off by default (`llm.enabled: false`); to turn it on, set `llm.enabled: true` together with `llm.apiKey` (or `externalSecrets.enabled` with `llm_api_key` in the store) — without a key the render fails instead of leaving a pod in `CreateContainerConfigError`. The bundled PostgreSQL has no TLS, so with it the chart sets `DATABASE_SSL_MODE=disable` plus `DATABASE_ALLOW_INSECURE_SSL_IN_PRODUCTION=true`: under `environment: production` (the default) AMP accepts the unencrypted in-cluster connection and logs a `Database connection is NOT encrypted in production` warning with the database host. The chart never sets that flag for an external database, and AMP still rejects `sslmode=disable` in production without it. External PostgreSQL: set `postgresql.enabled: false` and `externalDatabase.*`, with the password in a Secret you create (see [PostgreSQL](#postgresql)). With `postgresql.enabled: false` and no database configured, the render fails. `values-production.yaml` uses an external database and does not render until `externalDatabase` is filled in. The defaults target one node: one replica, HPA off, about 270m CPU / 512Mi requested in total (see [Sizing](#sizing)). No image is published to GHCR yet; build it locally until the first release (see [CI And Image Publishing](../../docs/CI.md)).

```yaml
# my-values.yaml — standard profile, one replica (the defaults)
configFile:
  enabled: true           # optional: without a file AMP reads its environment only
  content: |
    route:
      receiver: default
      group_by: ['alertname']
    receivers:
      - name: default
        webhook_configs:
          - url: https://example.invalid/hook   # plain http:// fails validation
    inhibit_rules: []     # upstream placement; `inhibition: inhibit_rules:` works too
```

```bash
helm install amp ./helm/amp -f my-values.yaml
# about 1 vCPU / 1Gi free: add the chart's small-node file
helm install amp ./helm/amp -f ./helm/amp/values-small.yaml -f my-values.yaml
```

### Sizing

Requests are set from a measurement on one node under light load (`tasks/archive/HELM-SINGLE-NODE-DEFAULTS/evidence/idle-top.txt`: the whole stack used about 50m CPU / 90Mi); limits leave room for bursts.

| Container | Requests | Limits |
|-----------|----------|--------|
| AMP | 100m / 128Mi | 1000m / 1Gi |
| PostgreSQL | 100m / 256Mi | 1000m / 1Gi |
| postgres-exporter | 10m / 32Mi | 100m / 128Mi |
| Valkey (`<fullname>-redis`) | 50m / 64Mi | 500m / 512Mi |
| redis-exporter | 10m / 32Mi | 100m / 128Mi |
| **Total** | **270m / 512Mi** | |

Volumes: PostgreSQL 10Gi, Valkey 5Gi. `values-small.yaml` lowers the limits (AMP, PostgreSQL 512Mi; Valkey 256Mi with `maxmemory 192mb`), PostgreSQL to `max_connections=60`, and the volumes to 5Gi / 1Gi. For HA use `values-production.yaml` (HPA 3–20, external PostgreSQL) or set `autoscaling.enabled: true` — with the bundled PostgreSQL, raise its connection budget first ([PostgreSQL](#postgresql)).

## Deployment Profiles

### Standard Profile (Default)
PostgreSQL + Valkey; grouping state is shared across replicas:
```bash
helm install amp ./helm/amp
```
- PostgreSQL storage (single primary, no replication — see `PROD-POSTGRES-HA-DECISION`)
- Valkey cache (`<fullname>-redis`, image `valkey/valkey`), one instance. Valkey, not Redis: Redis 7.4 and later is RSALv2/SSPL (`docs/06-planning/DECISIONS.md` ADR-017). An external Redis-compatible cache: `cache.host` (and `valkey.replicas: 0`), as in `values-production.yaml`
- One replica, HPA off by default; `autoscaling.enabled: true` for 2–10 replicas
- Group timers and notification dedup live in Valkey, so replicas share them

### Lite Profile
Single process, no external dependencies:
```bash
helm install amp ./helm/amp --set profile=lite
```
- SQLite storage (PVC-based), memory cache
- Grouping works (`group_wait`/`group_interval`/`repeat_interval`), with groups and timers kept in memory — one replica only
- Turn grouping off with the `grouping.enabled` value, not in `configFile.content`: the chart passes the value as `GROUPING_ENABLED`, which overrides the config file
- Perfect for: dev, testing, smoke checks

## Configuration

### Basic Values

| Parameter | Description | Default |
|-----------|-------------|---------|
| `profile` | Deployment profile (lite/standard) | `standard` |
| `replicaCount` | Number of replicas | `1` |
| `image.repository` | Image repository | `ghcr.io/ipiton/amp` |
| `image.tag` | Image tag | `""` (defaults to `.Chart.AppVersion`) |

### LLM Configuration (BYOK)

| Parameter | Description | Default |
|-----------|-------------|---------|
| `llm.enabled` | Enable LLM classification | `false` |
| `llm.provider` | LLM provider (openai/anthropic/azure) | `openai` |
| `llm.apiKey` | API key (use secret in production) | `""` |
| `llm.model` | Model name | `openai/gpt-4o` |

### Storage

| Parameter | Description | Default |
|-----------|-------------|---------|
| `postgresql.enabled` | Enable bundled PostgreSQL; the lite profile does not use it, set `false` there | `true` |
| `cache.enabled` | Point AMP at the Valkey cache (`cache.host`); `false` leaves AMP on in-memory state | `true` |
| `valkey.image.repository` / `.tag` | Cache image | `valkey/valkey` / `8.1.10-alpine` |
| `persistence.enabled` | Enable PVC (Lite) | `true` |
| `persistence.size` | PVC size | `5Gi` |

### PostgreSQL

The standard profile needs PostgreSQL; the chart ships and tests 16 (`postgres:16.15`). AMP writes every incoming alert to it synchronously: if the database is unreachable, `POST /api/v2/alerts` returns `500`. Prometheus re-sends active alerts periodically, so they arrive once the database is back; a resolve notification sent only during the outage can be lost.

| Topology | Use for | Backups / HA |
|----------|---------|--------------|
| External PostgreSQL (16 tested): a managed service with PITR, or CloudNativePG with barman/S3 | production | operator's side |
| Bundled (`postgresql.enabled: true`) | dev, pilot | none |

The bundled PostgreSQL (`postgres:16.15`) is a single instance without replication. It has no PodDisruptionBudget and no backup job. A pod restart, node drain or upgrade that touches it means database downtime, and ingest returns errors until the database is back. The chart fails to render if `postgresql.replicas` is not `1` (several replicas would be independent databases behind one Service) or if `postgresql.backup.enabled` is `true` (the bundled backup was removed).

The server runs with `postgresql.config` (rendered into `postgresql.conf`) and the chart's `pg_hba.conf`, passed as server arguments, so they also apply to an existing data volume. Each AMP pod opens up to 25 connections (`database.max_connections`), so `autoscaling.maxReplicas` × 25 must stay below `postgresql.config.maxConnections`. The default 100 (10 of them reserved for the superuser) covers the default replica, a rolling-update surge pod and the exporter, with room for a third pod; NOTES warns when `autoscaling.maxReplicas` × 25 reaches it. Before enabling autoscaling, raise it to at least `maxReplicas` × 25 + 10, together with `sharedBuffers` and `postgresql.resources` (the default limit is 1Gi with `shared_buffers=128MB`). From the network, `pg_hba.conf` admits only `postgresql.username` to `postgresql.database` with a SCRAM password and rejects everything else, including replication; loopback inside the pod (and `kubectl port-forward`) needs the password too, only `kubectl exec … psql` over the Unix socket does not; to restrict which pods may connect, enable `postgresql.networkPolicy.enabled`. `/dev/shm` is a 256Mi memory volume and counts against `postgresql.resources.limits.memory`.

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

The chart passes these to AMP as `DATABASE_HOST/PORT/DATABASE/USERNAME/SSL_MODE`, and `DATABASE_PASSWORD` comes from the Secret through `secretKeyRef`. The render fails if `host` is set without `database`, `username` or `existingSecret`, if `sslMode` is not one of the four values AMP accepts (libpq's `allow` and `prefer` are not), or if `postgresql.enabled` is still `true`.

- A missing Secret or key is not caught at render time (the chart does not read the cluster): the pod stays in `CreateContainerConfigError`.
- The password reaches AMP as an environment variable, read at pod start. After changing the Secret, run `kubectl -n <namespace> rollout restart deploy/<fullname>`.
- With `externalSecrets.enabled`, the chart's ExternalSecret maps database keys only for the bundled PostgreSQL. Create your own ExternalSecret whose target Secret is `externalDatabase.existingSecret`.
- Each AMP pod opens up to 25 connections (`database.max_connections` default). Size `max_connections` on the server for the HPA maximum: `values-production.yaml` allows 20 replicas, up to 500 connections. Small managed tiers often allow about 100.
- Monitoring the external database (alerts, exporter) is on the operator's side: the chart's PostgreSQL rules and exporter cover only the bundled one.
- Under `environment: production` AMP refuses `sslMode: disable` and passwords shorter than 12 characters. The chart never sets the insecure-SSL override for an external database.
- `verify-ca`/`verify-full` check the server certificate against the image's system roots only. A provider CA (RDS and the like) is not among them, and the chart mounts no client TLS material: use `require`, or an image with that CA.
- `externalDatabase` takes precedence over a `database:` section in `configFile.content` (environment variables win over the config file).
- Fallback without a Secret: leave `externalDatabase.host` empty and put a top-level `database:` section into `configFile.content` (`configFile.enabled: true`). The password then sits in plain text in a ConfigMap. The render check parses `configFile.content` as YAML and looks for a top-level `database` key, spelled in lower case; invalid YAML fails the render with the parse error.
- Moving an installation from the bundled to an external database: [docs/POSTGRESQL_RESTORE_GUIDE.md § 3c](docs/POSTGRESQL_RESTORE_GUIDE.md#3c-move-from-the-bundled-to-an-external-postgresql).

Backup and restore with `pg_dump`/`pg_restore`, for both topologies: [docs/POSTGRESQL_RESTORE_GUIDE.md](docs/POSTGRESQL_RESTORE_GUIDE.md). The decision record is ADR-016 in `docs/06-planning/DECISIONS.md`.

### HTTP Authentication

The API is unauthenticated unless a web config is mounted (AMP logs a `WARN` at startup). Put an upstream-format `web-config.yml` (`basic_auth_users` with bcrypt hashes) into a Secret and point the chart at it:

```bash
kubectl create secret generic amp-web-config --from-file=web-config.yml
helm upgrade --install amp ./helm/amp --set webConfig.existingSecret=amp-web-config
```

| Parameter | Description | Default |
|-----------|-------------|---------|
| `webConfig.existingSecret` | Secret with the web config; enables basic auth | `""` |
| `webConfig.secretKey` | Key inside the Secret | `web-config.yml` |
| `webConfig.mountPath` | Directory the Secret is mounted at | `/etc/amp/web` |

Liveness/readiness probes use `/-/healthy` and `/-/ready`, which stay reachable without credentials. Password edits in the Secret apply without a pod restart once kubelet syncs the volume. `configReloader.enabled` cannot be combined with `webConfig` yet (rendering fails). See `docs/CONFIGURATION_GUIDE.md` → "Enable HTTP Authentication".

### Graceful Shutdown

On termination the pod first runs a preStop `sleep` while Kubernetes removes it from Service endpoints, then AMP gets `SIGTERM`: readiness turns `503`, in-flight requests drain, services stop (the lite profile writes its final snapshot). Details: `docs/CONFIGURATION_GUIDE.md` → "Graceful Shutdown".

| Parameter | Description | Default |
|-----------|-------------|---------|
| `gracefulShutdown.terminationGracePeriodSeconds` | Total budget before `SIGKILL`, counted from the start of preStop | `40` |
| `gracefulShutdown.preStopDelay` | Seconds the preStop hook sleeps; `0` removes the hook | `5` |
| `gracefulShutdown.timeoutSeconds` | AMP's budget for drain + service shutdown (`server.graceful_shutdown_timeout`) | `30` |

Keep `terminationGracePeriodSeconds` ≥ `preStopDelay` + `timeoutSeconds`. Otherwise install/upgrade still succeeds, but the notes print a `WARNING` and a slow drain can be cut off by `SIGKILL`.

Limitations:

- The hook runs `sleep` from the image (`alpine` today). In an image without `sleep` the hook fails, the kubelet logs `FailedPreStopHook` and sends `SIGTERM` immediately: no delay, the pod still stops.

### RBAC

The chart grants the AMP ServiceAccount exactly what the app calls: publishing target discovery lists Secrets labelled `publishing.discovery.labelSelector` in **one** namespace. It renders one `Role` (`list` on `secrets`) and its `RoleBinding`, and nothing cluster-scoped.

| Condition | RBAC rendered |
|-----------|---------------|
| `profile: standard`, `publishing.enabled: true`, `serviceAccount.create: true`, `serviceAccount.rbac.create: true` | `Role` + `RoleBinding` `<release>-secrets-lister` in `publishing.discovery.namespace` (default: the release namespace) |
| `profile: lite`, or any of the three flags `false` | none — lite never talks to the Kubernetes API |

- **Keep target Secrets in their own namespace.** RBAC cannot restrict `list` to a label selector, so the Role can read every Secret in the discovery namespace — including the chart's own database and LLM credentials when that is the release namespace. Setting `publishing.discovery.namespace` to a dedicated namespace keeps them out of reach. The Role is then created in that namespace, so whoever runs `helm install` needs permission to create Roles there.
- **Managing RBAC yourself**: set `serviceAccount.rbac.create=false` and grant the same `list secrets` in the discovery namespace. Without it discovery fails with `403`, AMP logs a `WARN` and starts with no discovered targets (targets from `receivers:` keep working).
- The Redis StatefulSet shares the ServiceAccount but does not mount its token.
- Verify: `kubectl auth can-i list secrets -n <discovery-namespace> --as=system:serviceaccount:<release-namespace>:<serviceaccount>` → `yes`; the same for `get`, or for any other namespace → `no`.

### Network Exposure

The whole API — including `POST /api/v2/silences` and `POST /-/reload` — sits on one port (`http`), so whoever reaches it can silence every alert. The chart closes the two ways in, and fails the render instead of guessing (ADR-015).

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

**NetworkPolicy for the AMP pods.** `networkPolicy.enabled: true` admits traffic to the `http` port from the listed sources only. Egress is not restricted: notification targets are arbitrary.

| Parameter | Who | Default |
|-----------|-----|---------|
| `networkPolicy.ingressController` | ingress controller pods; used only with `ingress.enabled` | `[]` |
| `networkPolicy.alertSenders` | Prometheus / vmalert posting to the Service directly | `[]` |
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
- On a CNI without NetworkPolicy support the object is accepted and does nothing. Check after install, from both sides (`8080` is the default `service.port`; run the first check from a namespace you did not list):

  ```bash
  SVC=$(kubectl get svc -n <namespace> -l app.kubernetes.io/instance=<release>,app.kubernetes.io/component=application -o name)
  # from a namespace that is not listed: must fail (timeout or connection refused, depending on the CNI)
  kubectl run np-check -n default --rm -it --image=curlimages/curl --restart=Never -- \
    curl -m 5 http://${SVC#service/}.<namespace>:8080/-/healthy
  # from a listed source, e.g. the Prometheus pod: must answer
  kubectl exec -n monitoring <prometheus-pod> -- wget -qO- -T 5 http://${SVC#service/}.<namespace>:8080/-/healthy
  ```

- **If alerts stop arriving**, the sender sees it first: Prometheus `rate(prometheus_notifications_errors_total[5m]) > 0` or `prometheus_notifications_dropped_total` growing; vmalert `rate(vmalert_alerts_send_errors_total[5m]) > 0`. Kill-switch, no pod restart: `helm upgrade <release> ./helm/amp --reuse-values --set networkPolicy.enabled=false` (on a release already running this chart version; see the migration notes in `CHANGELOG.md` for the first upgrade).

AMP pods carry `app.kubernetes.io/component: application` (the postgres/redis pods share the chart's selector labels and have their own component), so external policies can select them too.

**ServiceMonitor.** Rendered when `monitoring.prometheusEnabled` and `monitoring.serviceMonitor.enabled` are both true (default); it needs the prometheus-operator CRDs. It scrapes `/metrics` on the AMP Service only.

| Parameter | Description | Default |
|-----------|-------------|---------|
| `monitoring.serviceMonitor.enabled` | render the AMP ServiceMonitor | `true` |
| `monitoring.serviceMonitor.labels` | extra labels, e.g. what your Prometheus `serviceMonitorSelector` matches | `{}` |
| `monitoring.serviceMonitor.interval` / `scrapeTimeout` | scrape timing | `30s` / `10s` |
| `monitoring.serviceMonitor.basicAuth.secretName` | Secret with the plain username/password; **required** with `webConfig.existingSecret` | `""` |
| `monitoring.serviceMonitor.basicAuth.usernameKey` / `passwordKey` | keys in that Secret | `username` / `password` |

With web auth on, `/metrics` needs credentials and the render fails without them. Create the Secret in the namespace AMP runs in (`namespace`, by default the release namespace) from the plain password of a `web-config.yml` user (not its bcrypt hash):

```bash
kubectl create secret generic amp-metrics-auth -n <namespace> \
  --from-literal=username=prometheus --from-literal=password='<plain password>'
helm upgrade amp ./helm/amp --reuse-values \
  --set monitoring.serviceMonitor.basicAuth.secretName=amp-metrics-auth
```

With a NetworkPolicy, list that Prometheus in `networkPolicy.metricsScrapers` (or `alertSenders`), or its scrapes time out.

**`values-production.yaml`** enables the Ingress and the NetworkPolicy and ships none of the above, so it renders only once you set: `webConfig.existingSecret` (or `ingress.externalAuth`), `monitoring.serviceMonitor.basicAuth.secretName` (with `webConfig`), and at least one NetworkPolicy source. The render reports one missing setting at a time.

## Alertmanager Compatibility

AMP chart should currently be treated as a **controlled replacement** deployment path, not as a verified full Alertmanager drop-in replacement:

```yaml
# prometheus.yml - change the URL only after validating the covered slice
alerting:
  alertmanagers:
    - static_configs:
        - targets:
          - amp.<namespace>.svc:8080  # Service port is 8080 (was: alertmanager:9093)
```

With kube-prometheus-stack, add AMP as a second endpoint next to the existing Alertmanager (`prometheus.prometheusSpec.additionalAlertManagerConfigs`) and point AMP's receivers at test channels until you have compared both. Known gaps that bite on a copied `alertmanager.yml` — a built-in filter that drops some alerts, partial per-integration fields — are listed in [docs/ALERTMANAGER_COMPATIBILITY.md](../../docs/ALERTMANAGER_COMPATIBILITY.md) § Known Gaps. The chart's `PrometheusRule` objects carry no configurable labels, so a Prometheus with a `ruleSelector` (kube-prometheus-stack selects `release: <name>`) will not load them (`HELM-PROMRULE-LABELS`).

Current active runtime surface mounted by the repository bootstrap:
- `POST /api/v2/alerts`
- `GET /api/v2/alerts`
- `GET /api/v2/alerts/groups`
- `GET /api/v2/status`
- `GET /api/v2/receivers`
- `GET/POST /api/v2/silences`
- `GET/DELETE /api/v2/silence/{id}`
- `POST /-/reload`
- `/health`, `/ready`, `/-/healthy`, `/-/ready`, `/metrics`

Inhibition rules are applied (`GET /api/v2/inhibitions` shows active ones), both upstream's top-level `inhibit_rules:` and AMP's `inhibition: inhibit_rules:`. Config write and history APIs, classification surfaces and broader dashboard surfaces remain explicit follow-up work.

## Upgrading

```bash
helm upgrade amp ./helm/amp --reuse-values
```

`--reuse-values` keeps the values of the old chart version, defaults included. When a chart version removes or renames keys, read the migration notes in `CHANGELOG.md` first. Upgrading from a version that still had the bundled PostgreSQL backup (`postgresql.backup`) fails with `--reuse-values`: use `--reset-then-reuse-values` (Helm ≥ 3.14), or add `--set postgresql.backup.enabled=false`.

## Uninstalling

```bash
helm uninstall amp
```

## License

AGPL-3.0 License - see [LICENSE](https://github.com/ipiton/AMP/blob/main/LICENSE)
