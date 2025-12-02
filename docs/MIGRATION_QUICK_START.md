# Quick Start: Replace Alertmanager in 5 Minutes

**Target Audience**: Ops/SRE wanting immediate replacement
**Time Required**: 5 minutes
**Difficulty**: Easy

---

## 🚀 3-Step Migration

### Step 1: Deploy Alert History (2 minutes)

#### Kubernetes (Helm)
```bash
# Add repo
helm repo add alertmanager-plusplus https://github.com/ipiton/alert-history-service
helm repo update

# Install (same config as Alertmanager!)
helm install alert-history alertmanager-plusplus/alert-history \
  --set profile=standard \
  --set-file config=alertmanager.yml \
  --namespace monitoring
```

#### Docker
```bash
docker run -d \
  -p 8080:8080 \
  -v $(pwd)/alertmanager.yml:/etc/alert-history/config.yml \
  --name alert-history \
  yourusername/alertmanager-plusplus:latest
```

---

### Step 2: Update Prometheus (1 minute)

```yaml
# prometheus.yml
alerting:
  alertmanagers:
    - static_configs:
        - targets:
          # OLD: - 'alertmanager:9093'
          - 'alert-history:8080'  # NEW: Just change the port!
```

Apply:
```bash
kubectl rollout restart deployment prometheus -n monitoring
# OR
docker restart prometheus
```

---

### Step 3: Verify (2 minutes)

```bash
# Check health
curl http://localhost:8080/healthz

# Test alert ingestion
curl -X POST http://localhost:8080/api/v2/alerts \
  -H "Content-Type: application/json" \
  -d '[{"labels":{"alertname":"test","severity":"info"}}]'

# Query alerts (Alertmanager-compatible)
curl http://localhost:8080/api/v2/alerts
```

---

## ✅ Done!

**That's it!** Your alerts are now flowing through Alert History.

### What Just Happened?

- ✅ 100% Alertmanager API compatible - no other changes needed
- ✅ Your existing `alertmanager.yml` works unchanged
- ✅ Grafana dashboards work automatically
- ✅ `amtool` commands work without modification
- ✅ **BONUS**: Now you have extended history, better performance, and optional AI classification

---

## 🔄 Rollback (if needed)

```bash
# Stop Alert History
kubectl delete deployment alert-history -n monitoring

# Redeploy Alertmanager
helm install alertmanager prometheus-community/alertmanager

# Update Prometheus targets back to :9093
```

---

## 📚 Next Steps

- **Production checklist**: See [MIGRATION_DETAILED.md](MIGRATION_DETAILED.md)
- **Feature comparison**: See [MIGRATION_COMPARISON.md](MIGRATION_COMPARISON.md)
- **Configuration**: Your `alertmanager.yml` works as-is, but check [CONFIGURATION.md](CONFIGURATION.md) for new features

---

## 🆘 Troubleshooting

**Alerts not showing up?**
```bash
# Check Prometheus is sending to correct endpoint
kubectl logs -n monitoring prometheus-0 | grep alert-history

# Check Alert History is receiving
kubectl logs -n monitoring alert-history-0 | grep "POST /api/v2/alerts"
```

**Grafana dashboard broken?**
- Verify dashboard uses `/api/v2/alerts` endpoint (should work automatically)
- Check datasource URL points to `alert-history:8080`

**Need help?**
- [GitHub Issues](https://github.com/ipiton/alert-history-service/issues)
- [Documentation](https://github.com/ipiton/alert-history-service/docs)

---

**Last Updated**: 2025-12-01
**Version**: v1.0.0
**Compatibility**: Alertmanager v0.25+ API v2
