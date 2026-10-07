# Quick Start: Controlled Migration from Alertmanager

**Target Audience**: Ops/SRE evaluating a controlled replacement slice
**Time Required**: Environment-dependent
**Difficulty**: Easy

Current runtime note (2026-03-09): this guide covers AMP's current controlled replacement surface. It includes the restored operational APIs `GET /api/v2/status`, `GET /api/v2/receivers`, `GET /api/v2/alerts/groups`, and `POST /-/reload`, but it still does not imply full Alertmanager drop-in parity.

---

## 🚀 3-Step Migration

### Step 1: Deploy Alertmanager++

#### Kubernetes (Helm)
```bash
# Install from the repository-local chart with a values file —
# helm/amp/README.md § Quick Start has a working values-small.yaml
helm install amp ./helm/amp \
  -f values-small.yaml \
  --namespace monitoring
```

Do not install with chart defaults alone (status 2026-10-07): with the default `llm.enabled: true` and no `llm.apiKey` the pod cannot start (the `llm-api-key` Secret key is missing); with the bundled PostgreSQL the `standard` profile does not start either — the bundled PostgreSQL has no TLS support, and `environment: production` (the default) rejects `sslmode=disable`, so AMP exits with `database SSL mode 'disable' is not allowed in production`. The values file above has the same problem; it starts in the `lite` profile, or with an external PostgreSQL with TLS: with `postgresql.enabled: false` the chart passes no `DATABASE_*` variables, so set `database:` (host, port, database, username, password) in `configFile.content` — `ssl_mode` defaults to `require`, the password must be at least 12 characters under `environment: production`, and it is stored in plain text in the config ConfigMap. Tracked as `HELM-DEFAULTS-VALIDATE`. Also, grouping stays off unless `grouping.enabled: true`; inhibition rules must sit under `inhibition:`, not at the top level. See [Known Gaps](ALERTMANAGER_COMPATIBILITY.md#known-gaps-honesty-notes) #9 and #13–#15.

Put your routes and receivers into `configFile.content`; receivers declared there become delivery targets. Without any target AMP ingests alerts but stays in `metrics-only` mode.

#### Docker
```bash
docker run -d \
  -p 9093:9093 \
  -v $(pwd)/config.yaml:/app/config.yaml \
  --name amp \
  ghcr.io/ipiton/amp:latest
```

The image is published to GHCR by tagged releases; until the first release, build it locally (`docker build -t amp .`) and use `amp` as the image name. See [CI And Image Publishing](CI.md).

---

### Step 2: Update Prometheus

```yaml
# prometheus.yml
alerting:
  alertmanagers:
    - static_configs:
        - targets:
          # OLD: - 'alertmanager:9093'
          - 'amp:9093'  # NEW: point to Alertmanager++
```

Apply:
```bash
kubectl rollout restart deployment prometheus -n monitoring
# OR
docker restart prometheus
```

---

### Step 3: Verify

```bash
# Check liveness and readiness
curl http://localhost:9093/health
curl http://localhost:9093/ready

# Test alert ingestion
curl -X POST http://localhost:9093/api/v2/alerts \
  -H "Content-Type: application/json" \
  -d '[{"labels":{"alertname":"test","severity":"info"}}]'

# Query alerts (Alertmanager-compatible)
curl http://localhost:9093/api/v2/alerts

# Check grouped alerts for Grafana-style integrations
curl http://localhost:9093/api/v2/alerts/groups

# Check status and configured receivers
curl http://localhost:9093/api/v2/status
curl http://localhost:9093/api/v2/receivers
```

---

## ✅ Pilot Ready!

**That's it!** Your alerts are now flowing through AMP's current active runtime slice.

### What Just Happened?

- ✅ Alert ingest works through the active `/api/v2/alerts` path
- ✅ Status, receivers, grouped alerts, and reload APIs are mounted in the active runtime
- ✅ Silence CRUD and health/readiness endpoints are available
- ✅ `/health` reports liveness, `/ready` reports readiness; optional degraded components can still return `200` with a degraded JSON body
- ✅ Real publishing path is active when targets are discovered
- 🟡 Wider Alertmanager parity remains phased/backlog work
- 🟡 Validate dashboards, `amtool`, routing semantics and config APIs explicitly before claiming full replacement

---

## 🔄 Rollback (if needed)

```bash
# Stop Alertmanager++
kubectl delete deployment amp -n monitoring

# Redeploy Alertmanager
helm install alertmanager prometheus-community/alertmanager

# Update Prometheus targets back to :9093
```

---

## 📚 Next Steps

- **Migration details**: See [MIGRATION_COMPARISON.md](MIGRATION_COMPARISON.md)
- **Feature comparison**: See [MIGRATION_COMPARISON.md](MIGRATION_COMPARISON.md)
- **Configuration**: Validate your `alertmanager.yml` against the active runtime surface and check [CONFIGURATION_GUIDE.md](CONFIGURATION_GUIDE.md) for current caveats

---

## 🆘 Troubleshooting

**Alerts not showing up?**
```bash
# Check Prometheus is sending to correct endpoint
kubectl logs -n monitoring prometheus-0 | grep amp

# Check Alertmanager++ is receiving
kubectl logs -n monitoring amp-0 | grep "POST /api/v2/alerts"
```

**Alerts are ingested, but Slack/PagerDuty/Rootly delivery does not happen?**
- Verify `publishing.enabled=true`
- Verify `kubectl get secret -n monitoring -l publishing-target=true`
- Verify `publishing.discovery.namespace` matches the namespace where target Secrets live
- If zero targets are discovered, the runtime remains in `metrics-only`

**Grafana dashboard broken?**
- Verify dashboard uses `/api/v2/alerts/groups` for grouped-alert views
- Check datasource URL points to `amp:9093`

**Need help?**
- [GitHub Issues](https://github.com/ipiton/AMP/issues)
- [Documentation](https://github.com/ipiton/AMP/tree/main/docs)

---

**Last Updated**: 2026-03-09
**Version**: v0.0.1
**Compatibility**: Alertmanager v0.25+ API v2 (current non-deprecated active slice only)
