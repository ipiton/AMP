# Quick Start: Replace Alertmanager in 5 Minutes

**Target Audience**: Ops/SRE wanting immediate replacement
**Time Required**: 5 minutes
**Difficulty**: Easy

---

## 🚀 3-Step Migration

### Step 1: Deploy Alertmanager++ (2 minutes)

#### Kubernetes (Helm)
```bash
# Add repo
helm repo add amp https://ipiton.github.io/AMP
helm repo update

# Install (standard profile)
helm install amp amp/amp \
  --set profile=standard \
  --namespace monitoring
```

#### Docker
```bash
docker run -d \
  -p 9093:9093 \
  -v $(pwd)/config.yaml:/app/config.yaml \
  --name amp \
  ghcr.io/ipiton/amp:latest
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
          - 'amp:9093'  # NEW: point to Alertmanager++
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
curl http://localhost:9093/health

# Test alert ingestion
curl -X POST http://localhost:9093/api/v2/alerts \
  -H "Content-Type: application/json" \
  -d '[{"labels":{"alertname":"test","severity":"info"}}]'

# Query alerts (Alertmanager-compatible)
curl http://localhost:9093/api/v2/alerts
```

---

## ✅ Done!

**That's it!** Your alerts are now flowing through Alertmanager++.

### What Just Happened?

- ✅ 100% Alertmanager API compatible - no other changes needed
- ✅ Your existing `alertmanager.yml` works unchanged
- ✅ Grafana dashboards work automatically
- ✅ `amtool` commands work without modification
- ✅ **BONUS**: Now you have extended history, better performance, and optional AI classification

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
- **Configuration**: Your `alertmanager.yml` works as-is, but check [CONFIGURATION_GUIDE.md](CONFIGURATION_GUIDE.md) for new features

---

## 🆘 Troubleshooting

**Alerts not showing up?**
```bash
# Check Prometheus is sending to correct endpoint
kubectl logs -n monitoring prometheus-0 | grep amp

# Check Alertmanager++ is receiving
kubectl logs -n monitoring amp-0 | grep "POST /api/v2/alerts"
```

**Grafana dashboard broken?**
- Verify dashboard uses `/api/v2/alerts` endpoint (should work automatically)
- Check datasource URL points to `amp:9093`

**Need help?**
- [GitHub Issues](https://github.com/ipiton/AMP/issues)
- [Documentation](https://github.com/ipiton/AMP/tree/main/docs)

---

**Last Updated**: 2025-12-01
**Version**: v1.0.0
**Compatibility**: Alertmanager v0.25+ API v2
