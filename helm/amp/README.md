# AMP (Alertmanager++) Helm Chart

[![Artifact Hub](https://img.shields.io/endpoint?url=https://artifacthub.io/badge/repository/amp)](https://artifacthub.io/packages/search?repo=amp)

## Overview

Alertmanager++ (AMP) chart packages the current repository runtime with:
- ✅ **Controlled replacement surface** for alert ingest/query, alert groups, `status`/`receivers`/`reload`, silence CRUD, health/readiness, metrics, and the real publishing path
- 🤖 **Optional LLM-related values** for environments that wire them explicitly
- 📊 **Partial dashboard surface** with broader UI/runtime parity still tracked as follow-up work
- 🟡 **Phased compatibility** rather than a verified full Alertmanager drop-in claim

## Quick Start

```bash
# Install with default values (Lite profile)
helm install amp ./helm/amp

# Install with LLM enabled
helm install amp ./helm/amp \
  --set llm.enabled=true \
  --set llm.provider=openai \
  --set llm.apiKey=sk-your-key
```

## Deployment Profiles

### Lite Profile (Default)
Single-node, no external dependencies:
```bash
helm install amp ./helm/amp --set profile=lite
```
- SQLite storage (PVC-based)
- Memory cache
- Perfect for: dev, testing, <1K alerts/day

### Standard Profile
HA-ready with PostgreSQL + Redis:
```bash
helm install amp ./helm/amp \
  --set profile=standard \
  --set postgresql.enabled=true \
  --set cache.enabled=true
```
- PostgreSQL storage
- Redis/Valkey cache
- HPA (2-10 replicas)
- Perfect for: production, >1K alerts/day

## Configuration

### Basic Values

| Parameter | Description | Default |
|-----------|-------------|---------|
| `profile` | Deployment profile (lite/standard) | `lite` |
| `replicaCount` | Number of replicas | `1` |
| `image.repository` | Image repository | `ghcr.io/ipiton/amp` |
| `image.tag` | Image tag | `""` (defaults to `.Chart.AppVersion`) |

### LLM Configuration (BYOK)

| Parameter | Description | Default |
|-----------|-------------|---------|
| `llm.enabled` | Enable LLM classification | `false` |
| `llm.provider` | LLM provider (openai/anthropic/azure) | `openai` |
| `llm.apiKey` | API key (use secret in production) | `""` |
| `llm.model` | Model name | `gpt-4o-mini` |

### Storage

| Parameter | Description | Default |
|-----------|-------------|---------|
| `postgresql.enabled` | Enable PostgreSQL | `false` |
| `cache.enabled` | Enable Redis/Valkey | `false` |
| `persistence.enabled` | Enable PVC (Lite) | `true` |
| `persistence.size` | PVC size | `5Gi` |

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
- Without `configFile.enabled`, AMP currently ignores its environment (`BUGS.md` → `CONFIG-MISSING-FILE-DROPS-ENV`). Then `timeoutSeconds` does not reach the process, and AMP uses its own `30s` default whatever the value says.

### RBAC

The chart grants the AMP ServiceAccount exactly what the app calls: publishing target discovery lists Secrets labelled `publishing.discovery.labelSelector` in **one** namespace. It renders one `Role` (`list` on `secrets`) and its `RoleBinding`, and nothing cluster-scoped.

| Condition | RBAC rendered |
|-----------|---------------|
| `profile: standard`, `publishing.enabled: true`, `serviceAccount.create: true`, `serviceAccount.rbac.create: true` | `Role` + `RoleBinding` `<release>-secrets-lister` in `publishing.discovery.namespace` (default: the release namespace) |
| `profile: lite`, or any of the three flags `false` | none — lite never talks to the Kubernetes API |

- **Keep target Secrets in their own namespace.** RBAC cannot restrict `list` to a label selector, so the Role can read every Secret in the discovery namespace — including the chart's own database and LLM credentials when that is the release namespace. Setting `publishing.discovery.namespace` to a dedicated namespace keeps them out of reach. The Role is then created in that namespace, so whoever runs `helm install` needs permission to create Roles there.
- **Managing RBAC yourself**: set `serviceAccount.rbac.create=false` and grant the same `list secrets` in the discovery namespace. Without it discovery fails with `403`, AMP logs a `WARN` and starts with no discovered targets (targets from `receivers:` keep working).
- The Redis StatefulSet and the PostgreSQL backup CronJob share the ServiceAccount but do not mount its token.
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
          - amp:9093  # Was: alertmanager:9093
```

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

Wider parity such as config/history APIs, inhibition/classification surfaces, and broader dashboard surfaces remains explicit follow-up work.

## Upgrading

```bash
helm upgrade amp ./helm/amp --reuse-values
```

## Uninstalling

```bash
helm uninstall amp
```

## License

AGPL-3.0 License - see [LICENSE](https://github.com/ipiton/AMP/blob/main/LICENSE)
