# AMP Helm Deployment Guide

This guide covers operator-facing installation and verification for the `helm/amp` chart.
Treat the chart as a controlled replacement deployment path for the active runtime surface documented in `helm/amp/README.md`, not as a verified full Alertmanager drop-in replacement.

## Prerequisites

- Kubernetes cluster with `kubectl` access
- Helm 3.8+
- A release name that contains `amp` if you want resource names to stay short in the examples below
- Optional: BYOK LLM credentials provided via a local override, secret, or external secret

## 1. Lite / Development Deployment

Use the development overrides when you want a single-release evaluation path with embedded storage and no external database dependency.

```bash
RELEASE=amp-dev
NAMESPACE=monitoring

helm upgrade --install "$RELEASE" ./helm/amp \
  --namespace "$NAMESPACE" \
  --create-namespace \
  --set profile=lite \
  -f helm/amp/values-dev.yaml
```

Verify the release:

```bash
kubectl rollout status deployment/"$RELEASE" -n "$NAMESPACE"
kubectl port-forward svc/"$RELEASE" 8080:8080 -n "$NAMESPACE"

curl http://127.0.0.1:8080/healthz
curl http://127.0.0.1:8080/api/v2/status
```

## 2. Standard / Cluster Deployment

Use the production-oriented overrides when you want the chart-managed PostgreSQL and cache path.

```bash
RELEASE=amp
NAMESPACE=monitoring

helm upgrade --install "$RELEASE" ./helm/amp \
  --namespace "$NAMESPACE" \
  --create-namespace \
  --set profile=standard \
  -f helm/amp/values-production.yaml
```

Inspect the resulting resources:

```bash
kubectl get pods -n "$NAMESPACE" -l app.kubernetes.io/instance="$RELEASE"
kubectl get svc -n "$NAMESPACE"
kubectl get statefulset -n "$NAMESPACE"
```

## 3. Optional LLM Configuration

If you enable LLM classification, pass the API key through a local override, secret, or external secret. Do not commit real keys into chart values files.

Example local override:

```bash
LLM_API_KEY=...

helm upgrade --install "$RELEASE" ./helm/amp \
  --namespace "$NAMESPACE" \
  --create-namespace \
  --set profile=lite \
  --set llm.enabled=true \
  --set llm.apiKey="$LLM_API_KEY"
```

The chart also exposes `llm.secret.*` and `llm.externalSecrets.*` values for secret-backed setups.

## 4. Routing Alertmanager Traffic

The chart exposes the active runtime over the service HTTP port (`service.port`, default `8080`).
Only redirect Alertmanager traffic after validating that the covered slice matches your operational needs.

```yaml
alerting:
  alertmanagers:
    - static_configs:
        - targets:
          - amp:8080
```

If your Helm release name differs from `amp`, use the rendered service name instead.

## 5. Post-Install Checks

Helpful checks after install:

```bash
kubectl logs deployment/"$RELEASE" -n "$NAMESPACE"
kubectl get configmap "${RELEASE}-config" -n "$NAMESPACE"

curl http://127.0.0.1:8080/readyz
curl http://127.0.0.1:8080/api/v2/receivers
curl http://127.0.0.1:8080/metrics
```

## 6. Notes

- Keep `helm/amp/README.md` as the chart-level source of truth for compatibility claims and the currently mounted runtime surface.
- Wider parity such as config/history APIs, broader dashboard parity, and other historical compatibility layers remains explicit follow-up work.
