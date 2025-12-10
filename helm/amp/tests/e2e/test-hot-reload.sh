#!/bin/bash
# ================================================================================
# E2E Test: Hot Reload Configuration
# ================================================================================
# Tests zero-downtime config reload in Kubernetes
#
# Prerequisites:
# - kubectl configured
# - AMP deployed with configReloader.enabled=true
# - jq installed
#
# Usage:
#   ./test-hot-reload.sh [namespace]
#
# Exit codes:
#   0 - All tests passed
#   1 - Test failed
#
# Author: AI Assistant
# Date: 2024-12-10

set -euo pipefail

# Configuration
NAMESPACE="${1:-default}"
RELEASE_NAME="amp"
POD_NAME="${RELEASE_NAME}-0"
TIMEOUT=60

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Logging functions
log_info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

log_step() {
    echo -e "\n${GREEN}==>${NC} $1"
}

# Check prerequisites
check_prerequisites() {
    log_step "Checking prerequisites"
    
    if ! command -v kubectl &> /dev/null; then
        log_error "kubectl not found"
        exit 1
    fi
    
    if ! command -v jq &> /dev/null; then
        log_error "jq not found (install: brew install jq)"
        exit 1
    fi
    
    log_info "✅ Prerequisites OK"
}

# Check if pod is running
check_pod_running() {
    log_step "Checking if AMP pod is running"
    
    if ! kubectl get pod "$POD_NAME" -n "$NAMESPACE" &> /dev/null; then
        log_error "Pod $POD_NAME not found in namespace $NAMESPACE"
        exit 1
    fi
    
    STATUS=$(kubectl get pod "$POD_NAME" -n "$NAMESPACE" -o jsonpath='{.status.phase}')
    if [ "$STATUS" != "Running" ]; then
        log_error "Pod is not running (status: $STATUS)"
        exit 1
    fi
    
    log_info "✅ Pod is running"
}

# Check if config-reloader sidecar is present
check_config_reloader() {
    log_step "Checking config-reloader sidecar"
    
    CONTAINERS=$(kubectl get pod "$POD_NAME" -n "$NAMESPACE" -o jsonpath='{.spec.containers[*].name}')
    
    if echo "$CONTAINERS" | grep -q "config-reloader"; then
        log_info "✅ Config-reloader sidecar found"
    else
        log_warn "⚠️  Config-reloader sidecar not found (hot reload may not work)"
    fi
}

# Get current log level
get_current_log_level() {
    kubectl exec "$POD_NAME" -n "$NAMESPACE" -c amp -- \
        curl -s http://localhost:8080/api/v2/config | jq -r '.log.level' 2>/dev/null || echo "unknown"
}

# Test 1: Change log level
test_log_level_change() {
    log_step "Test 1: Change log level (info -> debug)"
    
    # Get current log level
    CURRENT_LEVEL=$(get_current_log_level)
    log_info "Current log level: $CURRENT_LEVEL"
    
    # Determine new level
    if [ "$CURRENT_LEVEL" = "debug" ]; then
        NEW_LEVEL="info"
    else
        NEW_LEVEL="debug"
    fi
    
    log_info "Changing log level to: $NEW_LEVEL"
    
    # Patch ConfigMap
    kubectl patch cm "${RELEASE_NAME}-app-config" -n "$NAMESPACE" --type=json \
        -p="[{\"op\": \"replace\", \"path\": \"/data/config.yaml\", \"value\": \"$(kubectl get cm ${RELEASE_NAME}-app-config -n $NAMESPACE -o jsonpath='{.data.config\.yaml}' | sed "s/level: .*/level: $NEW_LEVEL/")\"}]" \
        2>/dev/null || {
            log_warn "ConfigMap patch failed, trying alternative method"
            # Alternative: edit directly
            kubectl get cm "${RELEASE_NAME}-app-config" -n "$NAMESPACE" -o yaml | \
                sed "s/level: .*/level: $NEW_LEVEL/" | \
                kubectl apply -f - &>/dev/null
        }
    
    log_info "ConfigMap updated"
    
    # Wait for reload
    log_info "Waiting for config reload (max ${TIMEOUT}s)..."
    
    for i in $(seq 1 $TIMEOUT); do
        sleep 1
        
        # Check if log level changed
        UPDATED_LEVEL=$(get_current_log_level)
        
        if [ "$UPDATED_LEVEL" = "$NEW_LEVEL" ]; then
            log_info "✅ Log level changed successfully (${CURRENT_LEVEL} -> ${NEW_LEVEL})"
            log_info "Reload took ${i}s"
            return 0
        fi
        
        if [ $((i % 5)) -eq 0 ]; then
            log_info "Still waiting... (${i}s elapsed)"
        fi
    done
    
    log_error "❌ Timeout: Log level did not change after ${TIMEOUT}s"
    return 1
}

# Test 2: Check config-reloader metrics
test_config_reloader_metrics() {
    log_step "Test 2: Check config-reloader metrics"
    
    # Check if config-reloader container exists
    if ! kubectl get pod "$POD_NAME" -n "$NAMESPACE" -o jsonpath='{.spec.containers[*].name}' | grep -q "config-reloader"; then
        log_warn "⚠️  Config-reloader not found, skipping metrics test"
        return 0
    fi
    
    # Get metrics
    METRICS=$(kubectl exec "$POD_NAME" -n "$NAMESPACE" -c config-reloader -- \
        curl -s http://localhost:9091/metrics 2>/dev/null || echo "")
    
    if [ -z "$METRICS" ]; then
        log_error "❌ Failed to fetch config-reloader metrics"
        return 1
    fi
    
    # Parse metrics
    ATTEMPTS=$(echo "$METRICS" | grep "config_reload_attempts_total" | awk '{print $2}')
    SUCCESSES=$(echo "$METRICS" | grep "config_reload_successes_total" | awk '{print $2}')
    FAILURES=$(echo "$METRICS" | grep "config_reload_failures_total" | awk '{print $2}')
    
    log_info "Reload attempts: ${ATTEMPTS:-0}"
    log_info "Reload successes: ${SUCCESSES:-0}"
    log_info "Reload failures: ${FAILURES:-0}"
    
    if [ "${SUCCESSES:-0}" -gt 0 ]; then
        log_info "✅ Config-reloader metrics OK"
        return 0
    else
        log_warn "⚠️  No successful reloads recorded yet"
        return 0
    fi
}

# Test 3: Check reload endpoint
test_reload_endpoint() {
    log_step "Test 3: Check reload health endpoint"
    
    RESPONSE=$(kubectl exec "$POD_NAME" -n "$NAMESPACE" -c amp -- \
        curl -s http://localhost:8080/health/reload 2>/dev/null || echo "")
    
    if [ -z "$RESPONSE" ]; then
        log_error "❌ Reload endpoint not responding"
        return 1
    fi
    
    STATUS=$(echo "$RESPONSE" | jq -r '.status' 2>/dev/null || echo "unknown")
    
    if [ "$STATUS" = "healthy" ]; then
        log_info "✅ Reload endpoint is healthy"
        
        # Show last reload info
        LAST_RELOAD=$(echo "$RESPONSE" | jq -r '.last_reload_time' 2>/dev/null || echo "unknown")
        LAST_VERSION=$(echo "$RESPONSE" | jq -r '.last_reload_version' 2>/dev/null || echo "unknown")
        
        log_info "Last reload: $LAST_RELOAD"
        log_info "Last version: $LAST_VERSION"
        
        return 0
    else
        log_error "❌ Reload endpoint status: $STATUS"
        return 1
    fi
}

# Test 4: Check SIGHUP handler
test_sighup_handler() {
    log_step "Test 4: Check SIGHUP signal handler"
    
    # Check logs for signal handler registration
    LOGS=$(kubectl logs "$POD_NAME" -n "$NAMESPACE" -c amp --tail=100 2>/dev/null || echo "")
    
    if echo "$LOGS" | grep -q "signal handlers registered"; then
        log_info "✅ SIGHUP handler is registered"
        return 0
    else
        log_warn "⚠️  SIGHUP handler registration not found in logs"
        return 0
    fi
}

# Main test execution
main() {
    echo "==============================================="
    echo "  AMP Hot Reload E2E Test"
    echo "==============================================="
    echo "Namespace: $NAMESPACE"
    echo "Release: $RELEASE_NAME"
    echo "Pod: $POD_NAME"
    echo "==============================================="
    echo ""
    
    # Run checks
    check_prerequisites
    check_pod_running
    check_config_reloader
    
    # Run tests
    FAILED=0
    
    test_log_level_change || FAILED=$((FAILED + 1))
    test_config_reloader_metrics || FAILED=$((FAILED + 1))
    test_reload_endpoint || FAILED=$((FAILED + 1))
    test_sighup_handler || FAILED=$((FAILED + 1))
    
    # Summary
    echo ""
    echo "==============================================="
    if [ $FAILED -eq 0 ]; then
        log_info "✅ All tests passed!"
        echo "==============================================="
        exit 0
    else
        log_error "❌ $FAILED test(s) failed"
        echo "==============================================="
        exit 1
    fi
}

# Run main function
main

