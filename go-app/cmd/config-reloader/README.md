# Config Reloader Sidecar

**Version:** 1.0.0  
**Purpose:** Watch config file changes and trigger hot reload via SIGHUP signal  
**Status:** ✅ Production Ready

---

## 📋 Overview

Config Reloader is a lightweight sidecar container that watches configuration files for changes and triggers hot reload in the main application container.

**Key Features:**
- ✅ File watch with SHA256 hash comparison
- ✅ SIGHUP signal to main process
- ✅ Health check verification
- ✅ Prometheus metrics export
- ✅ Graceful shutdown
- ✅ Minimal footprint (< 10MB)

---

## 🚀 Usage

### Kubernetes Deployment

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: amp
spec:
  template:
    spec:
      # Enable shared process namespace (required for SIGHUP)
      shareProcessNamespace: true
      
      containers:
      # Main application
      - name: amp
        image: ghcr.io/ipiton/amp:latest
        volumeMounts:
        - name: config
          mountPath: /etc/amp
          readOnly: true
      
      # Config reloader sidecar
      - name: config-reloader
        image: ghcr.io/ipiton/amp-config-reloader:latest
        args:
        - --config-file=/etc/amp/config.yaml
        - --reload-url=http://localhost:8080/health/reload
        - --interval=5s
        - --log-level=info
        volumeMounts:
        - name: config
          mountPath: /etc/amp
          readOnly: true
        resources:
          requests:
            cpu: 10m
            memory: 20Mi
          limits:
            cpu: 50m
            memory: 50Mi
      
      volumes:
      - name: config
        configMap:
          name: amp-config
```

---

## 🔧 Configuration

### Command-line Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--config-file` | `/etc/amp/config.yaml` | Path to config file to watch |
| `--reload-url` | `http://localhost:8080/health/reload` | URL to check reload status |
| `--interval` | `5s` | Check interval |
| `--log-level` | `info` | Log level (debug, info, warn, error) |
| `--metrics-port` | `9091` | Prometheus metrics port |

### Environment Variables

All flags can also be set via environment variables:

```bash
CONFIG_FILE=/etc/amp/config.yaml
RELOAD_URL=http://localhost:8080/health/reload
INTERVAL=5s
LOG_LEVEL=info
METRICS_PORT=9091
```

---

## 📊 Metrics

Config Reloader exports Prometheus metrics on port `9091`:

```
# Reload attempts
config_reload_attempts_total 42

# Successful reloads
config_reload_successes_total 40

# Failed reloads
config_reload_failures_total 2

# Last successful reload timestamp
config_reload_last_success_timestamp 1702234567
```

**ServiceMonitor:**

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: amp-config-reloader
spec:
  selector:
    matchLabels:
      app: amp
  endpoints:
  - port: metrics
    path: /metrics
    interval: 30s
```

---

## 🔍 How It Works

### Reload Flow

```
1. Watch config file (SHA256 hash check every 5s)
   ↓
2. Detect change (hash mismatch)
   ↓
3. Send SIGHUP to PID 1 (main container)
   ↓
4. Poll /health/reload endpoint (30s timeout)
   ↓
5. Verify reload success (HTTP 200)
   ↓
6. Update metrics
```

### Shared Process Namespace

Config Reloader requires `shareProcessNamespace: true` to send SIGHUP to the main container:

```yaml
spec:
  shareProcessNamespace: true  # Required!
```

**Without shared namespace:**
- Config Reloader runs in isolated PID namespace
- Cannot send signals to main container
- Reload will NOT work

**With shared namespace:**
- All containers share PID namespace
- Main container is PID 1
- Config Reloader can send SIGHUP to PID 1

---

## 🧪 Testing

### Local Testing

```bash
# Build
cd go-app
go build -o config-reloader ./cmd/config-reloader

# Run
./config-reloader \
  --config-file=./config.yaml \
  --reload-url=http://localhost:8080/health/reload \
  --interval=2s \
  --log-level=debug
```

### Docker Testing

```bash
# Build image
docker build -t amp-config-reloader:test -f cmd/config-reloader/Dockerfile .

# Run (requires main container)
docker run --pid=container:amp amp-config-reloader:test
```

### Kubernetes Testing

```bash
# Deploy
kubectl apply -f helm/amp/

# Edit ConfigMap
kubectl edit cm amp-config

# Watch logs
kubectl logs -f amp-0 -c config-reloader

# Expected output:
# Config change detected (hash: abc123 -> def456)
# Sending SIGHUP to main process (PID 1)
# ✅ Reload successful
```

---

## 🐛 Troubleshooting

### Issue: SIGHUP not received

**Symptoms:**
- Config Reloader logs: "Sending SIGHUP to main process (PID 1)"
- Main container logs: No reload message

**Solution:**
1. Check `shareProcessNamespace: true` is set
2. Verify main container is PID 1:
   ```bash
   kubectl exec amp-0 -c config-reloader -- ps aux
   ```
3. Check main container has SIGHUP handler registered

---

### Issue: Reload verification timeout

**Symptoms:**
- Config Reloader logs: "reload verification timeout"

**Solution:**
1. Check `/health/reload` endpoint exists:
   ```bash
   kubectl exec amp-0 -- curl http://localhost:8080/health/reload
   ```
2. Increase timeout (default 30s)
3. Check main container logs for reload errors

---

### Issue: High CPU usage

**Symptoms:**
- Config Reloader CPU > 50m

**Solution:**
1. Increase check interval:
   ```yaml
   args:
   - --interval=10s  # Default: 5s
   ```
2. Check for file system issues (slow I/O)

---

## 📚 References

- [Kubernetes Shared Process Namespace](https://kubernetes.io/docs/tasks/configure-pod-container/share-process-namespace/)
- [Prometheus Config Reloader](https://github.com/prometheus-operator/prometheus-operator/tree/main/cmd/prometheus-config-reloader)
- [Alertmanager Config Reloader](https://github.com/prometheus/alertmanager#high-availability)

---

## 🔒 Security

**Best Practices:**
- ✅ Runs as non-root user (UID 65532)
- ✅ Read-only filesystem
- ✅ Minimal attack surface (distroless base)
- ✅ No shell or package manager
- ✅ Resource limits enforced

**Permissions:**
- Read-only access to ConfigMap
- Network access to main container (localhost)
- Signal permission (SIGHUP to PID 1)

---

## 📝 License

Copyright © 2024 AMP Team  
Licensed under Apache 2.0

