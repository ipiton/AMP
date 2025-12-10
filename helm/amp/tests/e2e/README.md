# E2E Tests for Hot Reload

**Version:** 1.0.0  
**Purpose:** End-to-end testing of hot reload functionality in Kubernetes  
**Status:** ✅ Ready

---

## 📋 Overview

This directory contains E2E tests for validating hot reload functionality in Kubernetes environments.

**Test Coverage:**
- ✅ Log level change (info ↔ debug)
- ✅ Config-reloader metrics verification
- ✅ Reload health endpoint check
- ✅ SIGHUP handler registration

---

## 🚀 Quick Start

### Prerequisites

```bash
# Install dependencies
brew install jq  # macOS
apt-get install jq  # Ubuntu

# Verify kubectl access
kubectl get nodes

# Deploy AMP with hot reload enabled
helm install amp ./helm/amp \
  --set configReloader.enabled=true \
  --wait
```

---

### Run Tests

```bash
# Run all tests (default namespace)
./helm/amp/tests/e2e/test-hot-reload.sh

# Run tests in specific namespace
./helm/amp/tests/e2e/test-hot-reload.sh production

# Expected output:
# ===============================================
#   AMP Hot Reload E2E Test
# ===============================================
# [INFO] ✅ Prerequisites OK
# [INFO] ✅ Pod is running
# [INFO] ✅ Config-reloader sidecar found
# 
# ==> Test 1: Change log level (info -> debug)
# [INFO] Current log level: info
# [INFO] Changing log level to: debug
# [INFO] ConfigMap updated
# [INFO] Waiting for config reload (max 60s)...
# [INFO] ✅ Log level changed successfully (info -> debug)
# [INFO] Reload took 7s
# 
# ==> Test 2: Check config-reloader metrics
# [INFO] Reload attempts: 5
# [INFO] Reload successes: 5
# [INFO] Reload failures: 0
# [INFO] ✅ Config-reloader metrics OK
# 
# ==> Test 3: Check reload health endpoint
# [INFO] ✅ Reload endpoint is healthy
# [INFO] Last reload: 2024-12-10T15:30:45Z
# [INFO] Last version: 42
# 
# ==> Test 4: Check SIGHUP signal handler
# [INFO] ✅ SIGHUP handler is registered
# 
# ===============================================
# [INFO] ✅ All tests passed!
# ===============================================
```

---

## 🧪 Test Cases

### Test 1: Log Level Change

**Purpose:** Verify zero-downtime config change

**Steps:**
1. Get current log level from API
2. Patch ConfigMap with new log level
3. Wait for reload (max 60s)
4. Verify log level changed

**Success Criteria:**
- Log level changes within 60s
- No pod restart
- No downtime

---

### Test 2: Config-Reloader Metrics

**Purpose:** Verify config-reloader is working

**Steps:**
1. Fetch metrics from config-reloader (port 9091)
2. Parse Prometheus metrics
3. Verify successful reloads

**Success Criteria:**
- Metrics endpoint responds
- `config_reload_successes_total > 0`

---

### Test 3: Reload Health Endpoint

**Purpose:** Verify reload status API

**Steps:**
1. Call `/health/reload` endpoint
2. Parse JSON response
3. Verify status is "healthy"

**Success Criteria:**
- Endpoint returns 200 OK
- Status is "healthy"
- Last reload timestamp is recent

---

### Test 4: SIGHUP Handler

**Purpose:** Verify signal handler is registered

**Steps:**
1. Fetch pod logs
2. Search for "signal handlers registered"

**Success Criteria:**
- Log message found
- SIGHUP handler is active

---

## 🐛 Troubleshooting

### Test Fails: "Pod not found"

**Solution:**
```bash
# Check pod name
kubectl get pods -n <namespace>

# Update POD_NAME in script if different
export POD_NAME="amp-0"
```

---

### Test Fails: "Config-reloader not found"

**Solution:**
```bash
# Verify config-reloader is enabled
helm get values amp | grep configReloader

# Re-deploy with config-reloader
helm upgrade amp ./helm/amp \
  --set configReloader.enabled=true \
  --wait
```

---

### Test Fails: "Timeout: Log level did not change"

**Possible Causes:**
1. Config-reloader not running
2. SIGHUP not reaching main container
3. shareProcessNamespace not enabled

**Debug:**
```bash
# Check config-reloader logs
kubectl logs amp-0 -c config-reloader

# Check main container logs
kubectl logs amp-0 -c amp | grep reload

# Verify shareProcessNamespace
kubectl get pod amp-0 -o yaml | grep shareProcessNamespace
# Should show: shareProcessNamespace: true
```

---

### Test Fails: "Metrics not available"

**Solution:**
```bash
# Port-forward to config-reloader
kubectl port-forward amp-0 9091:9091

# Check metrics manually
curl http://localhost:9091/metrics
```

---

## 📊 CI/CD Integration

### GitHub Actions

```yaml
name: E2E Hot Reload Test

on:
  pull_request:
    paths:
      - 'helm/amp/**'
      - 'go-app/internal/config/**'

jobs:
  test-hot-reload:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      
      - name: Setup Kubernetes
        uses: helm/kind-action@v1
      
      - name: Install AMP
        run: |
          helm install amp ./helm/amp \
            --set configReloader.enabled=true \
            --wait --timeout=5m
      
      - name: Run E2E Tests
        run: |
          ./helm/amp/tests/e2e/test-hot-reload.sh default
```

---

### GitLab CI

```yaml
test:hot-reload:
  stage: test
  image: alpine/k8s:latest
  script:
    - helm install amp ./helm/amp --set configReloader.enabled=true --wait
    - ./helm/amp/tests/e2e/test-hot-reload.sh default
  only:
    changes:
      - helm/amp/**
      - go-app/internal/config/**
```

---

## 🔒 Security Considerations

**Test Environment:**
- Use dedicated test namespace
- Limit RBAC permissions
- Clean up after tests

**Production:**
- Run tests in staging first
- Monitor metrics during test
- Have rollback plan ready

---

## 📚 References

- [Hot Reload Design](../../../../tasks/hot-reload-full/design.md)
- [Configuration Guide](../../../../docs/CONFIGURATION_GUIDE.md)
- [Config-Reloader README](../../../../go-app/cmd/config-reloader/README.md)

---

## 📝 License

Copyright © 2024 AMP Team  
Licensed under Apache 2.0

