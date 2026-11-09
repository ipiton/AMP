# AMP Helm Deployment Guide

Operator-facing installation and verification for the `helm/amp` chart. The chart runs `amp-upstream` (upstream Alertmanager v0.34.1 plus AMP's layer) as a StatefulSet on port 9093. Values reference: [README.md](README.md). Upgrading a release of the chart before v0.1.0: [MIGRATION_FROM_LEGACY_CORE.md](../../docs/MIGRATION_FROM_LEGACY_CORE.md).

## Prerequisites

- Kubernetes cluster with `kubectl` access
- Helm 3.8+ (3.14+ for `--reset-then-reuse-values`)
- Your `alertmanager.yml`, to put under `alertmanager.config` in a values file
- Optional: LLM credentials in a Secret or an external secret

## 1. Development Deployment

```bash
RELEASE=amp-dev
NAMESPACE=monitoring

helm upgrade --install "$RELEASE" ./helm/amp \
  --namespace "$NAMESPACE" \
  --create-namespace \
  -f helm/amp/values-dev.yaml \
  -f my-alertmanager.yaml          # alertmanager.config: <your alertmanager.yml>
```

`values-dev.yaml` uses the bundled PostgreSQL (dev/pilot only). For the core alone, without PostgreSQL, add `--set profile=lite --set postgresql.enabled=false`.

Verify the release:

```bash
kubectl rollout status statefulset/"$RELEASE" -n "$NAMESPACE"
kubectl port-forward svc/"$RELEASE" 9093:9093 -n "$NAMESPACE"

curl http://127.0.0.1:9093/-/ready
curl http://127.0.0.1:9093/api/v2/status
```

## 2. Production Deployment

`values-production.yaml` runs three replicas against an external PostgreSQL, with a NetworkPolicy, and ships no credentials: the render fails until the database, its Secret, the policy's sources and HTTP authentication are set (it enables an Ingress, which the chart refuses to publish without auth). Put the policy's sources in your values file next to `alertmanager.config`:

```yaml
# my-alertmanager.yaml
alertmanager:
  config: {}                # your alertmanager.yml
networkPolicy:
  alertSenders:             # who may send alerts and call the API
    - namespaceSelector:
        matchLabels:
          kubernetes.io/metadata.name: monitoring
  metricsScrapers: []       # Prometheus, if it is not among alertSenders
```

```bash
RELEASE=amp
NAMESPACE=monitoring

kubectl create secret generic amp-db -n "$NAMESPACE" --from-literal=password="$PG_PASSWORD"

helm upgrade --install "$RELEASE" ./helm/amp \
  --namespace "$NAMESPACE" \
  --create-namespace \
  -f helm/amp/values-production.yaml \
  -f my-alertmanager.yaml \
  --set externalDatabase.host=pg.example.internal \
  --set externalDatabase.database=amp \
  --set externalDatabase.username=amp \
  --set externalDatabase.existingSecret=amp-db \
  --set webConfig.existingSecret=amp-web-config \
  --set monitoring.serviceMonitor.basicAuth.secretName=amp-metrics-auth
```

The two auth Secrets are created as in [§ 4](#4-http-authentication). Without an Ingress, `--set ingress.enabled=false` instead.

Inspect the resulting resources:

```bash
kubectl get pods,statefulset,svc,pvc -n "$NAMESPACE" -l app.kubernetes.io/instance="$RELEASE"
```

The release has two Services: `$RELEASE` (ClusterIP, for clients) and `$RELEASE-headless` (per-pod DNS for Prometheus and gossip). With more than one replica Prometheus must send to every pod: list `$RELEASE-<i>.$RELEASE-headless.$NAMESPACE.svc:9093`, or use DNS service discovery on the headless Service.

## 3. Optional LLM Configuration

Classification and investigation are off by default. Pass the key through a Secret or an external secret; do not commit real keys into values files.

```bash
helm upgrade --install "$RELEASE" ./helm/amp -n "$NAMESPACE" \
  -f my-values.yaml \
  --set llm.enabled=true \
  --set llm.proxyUrl=https://llm.example.internal/v1 \
  --set llm.apiKey="$LLM_API_KEY"
```

The chart also exposes `llm.secret.*` and `llm.externalSecrets.*` for secret-backed setups ([README.md](README.md)).

## 4. HTTP Authentication

Without a web config the API is open to anyone who can reach the Service. To enable basic auth, create a Secret with an upstream-format web config and set `webConfig.existingSecret`:

```bash
printf 'basic_auth_users:\n  prometheus: %s\n' "$(htpasswd -nbBC 10 prometheus "$AMP_PASSWORD" | cut -d: -f2)" > web-config.yml
kubectl create secret generic amp-web-config -n "$NAMESPACE" --from-file=web-config.yml
rm web-config.yml

kubectl create secret generic amp-metrics-auth -n "$NAMESPACE" \
  --from-literal=username=prometheus --from-literal=password="$AMP_PASSWORD"

helm upgrade --install "$RELEASE" ./helm/amp -n "$NAMESPACE" -f my-values.yaml \
  --set webConfig.existingSecret=amp-web-config \
  --set monitoring.serviceMonitor.basicAuth.secretName=amp-metrics-auth
```

Auth protects `/metrics` too: the chart refuses to render a ServiceMonitor without credentials (`monitoring.serviceMonitor.basicAuth.*`, or `monitoring.serviceMonitor.enabled=false`). With auth enabled, add `-u prometheus:"$AMP_PASSWORD"` to the `curl` checks in this guide (only `/-/healthy` and `/-/ready` answer without credentials) and configure `basic_auth` in Prometheus.

Keep a values file per release and pass it on every upgrade. `--reuse-values` keeps the values of the previous chart, including keys this chart no longer reads; on an upgrade from a chart before v0.1.0 the render refuses them.

## 5. Routing Alertmanager Traffic

```yaml
alerting:
  alertmanagers:
    - static_configs:
        - targets:
          - amp-0.amp-headless.monitoring.svc:9093
          - amp-1.amp-headless.monitoring.svc:9093
          - amp-2.amp-headless.monitoring.svc:9093
```

If your release name differs from `amp`, use the rendered Service names. For a staged move from Alertmanager, see [ROLLOUT_PLAN.md](../../docs/ROLLOUT_PLAN.md).

## 6. Post-Install Checks

```bash
kubectl logs statefulset/"$RELEASE" -n "$NAMESPACE"
kubectl get configmap "$RELEASE" "$RELEASE-amp-config" -n "$NAMESPACE"

curl http://127.0.0.1:9093/-/ready
curl http://127.0.0.1:9093/api/v2/receivers
curl http://127.0.0.1:9093/metrics | grep -E '^(alertmanager_cluster_members|alertmanager_config_last_reload_successful)'
amtool --alertmanager.url=http://127.0.0.1:9093 cluster show
```

The `$RELEASE` ConfigMap holds `alertmanager.yml`, `$RELEASE-amp-config` the AMP configuration rendered from the values. The log reports AMP configuration keys it does not know (`AMP configuration keys ignored`).

## 7. Notes

- [README.md](README.md) is the chart-level source of truth for values; [ALERTMANAGER_COMPATIBILITY.md](../../docs/ALERTMANAGER_COMPATIBILITY.md) for API behaviour.
- AMP v0.1.0 serves no UI: use karma, Grafana or `amtool`.
