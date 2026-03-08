# Quick Start: Controlled Migration from Alertmanager

**Target Audience**: Ops/SRE evaluating a controlled replacement slice
**Time Required**: Environment-dependent
**Difficulty**: Easy

Current runtime note (2026-03-08): this guide covers AMP's current controlled replacement slice. It does not imply full Alertmanager drop-in parity.

---

## 🚀 3-Step Migration

### Step 1: Deploy Alertmanager++

#### Kubernetes (Helm)
```bash
# Install from the repository-local chart
helm install amp ./helm/amp \
  --set profile=standard \
  --namespace monitoring
```

If you expect real outbound notifications in Kubernetes, also configure at least one publishing target through Helm values or a canonical target Secret. Without discovered targets AMP will ingest alerts but stay in `metrics-only` mode.

#### Docker
```bash
docker run -d \
  -p 9093:9093 \
  -v $(pwd)/config.yaml:/app/config.yaml \
  --name amp \
  ghcr.io/ipiton/amp:latest
```

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
# Check health
curl http://localhost:9093/health

# Test alert ingestion
curl -X POST http://localhost:9093/api/v2/alerts \
  -H "Content-Type: application/json" \
  -d '[{"labels":{"alertname":"test","severity":"info"}}]'

# Query alerts (Alertmanager-compatible)
curl http://localhost:9093/api/v2/alerts
```

---

## ✅ Pilot Ready!

**That's it!** Your alerts are now flowing through AMP's current active runtime slice.

### What Just Happened?

- ✅ Alert ingest works through the active `/api/v2/alerts` path
- ✅ Silence CRUD and health/readiness endpoints are available
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
- Verify dashboard uses `/api/v2/alerts` endpoint (should work automatically)
- Check datasource URL points to `amp:9093`

**Need help?**
- [GitHub Issues](https://github.com/ipiton/AMP/issues)
- [Documentation](https://github.com/ipiton/AMP/tree/main/docs)

---

**Last Updated**: 2026-03-08
**Version**: v1.0.0
**Compatibility**: Alertmanager v0.25+ API v2 (current non-deprecated active slice only)
