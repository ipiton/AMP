#!/usr/bin/env bash
# Access matrix for the local pilot replica (k3s with sema-prod's flags).
# Usage: KUBECONFIG=<lab> LAB_USER=loki LAB_PASS=<test password> lab-check.sh <title>
# HTTP 000 means the connection did not complete (blocked by the policy).
# Never point it at sema-prod: it posts test alerts.
set -uo pipefail

case "$(kubectl config current-context)" in
  sema-prod*) echo "refusing to run against sema-prod" >&2; exit 2 ;;
esac

SVC=http://amp.amp.svc:9093
POD=http://amp-0.amp-headless.amp.svc:9093
ALERT='[{"labels":{"alertname":"LabProbe","source":"lab-probe"}}]'
SILENCE='{"matchers":[{"name":"alertname","value":"LabProbe","isRegex":false}],"startsAt":"2026-01-01T00:00:00Z","endsAt":"2036-01-01T00:00:00Z","createdBy":"lab","comment":"lab"}'
ALLOWED=(-n sema-monitoring probe-allowed)
PROM=(-n sema-monitoring prometheus-kube-prometheus-stack-prometheus-0 -c prometheus)

code() { # <ns> <pod> <curl args...>
  local ns="$1" pod="$2"
  shift 2
  kubectl exec -n "$ns" "$pod" -- curl -s -m 5 -o /dev/null -w '%{http_code}' "$@" 2>/dev/null
}

post() { code "$1" "$2" "${@:4}" -H 'Content-Type: application/json' -d "$ALERT" "$3/api/v2/alerts"; }

prom() { # <promql>: "labels=value" per series
  kubectl exec "${PROM[@]}" -- wget -qO- "http://localhost:9090/api/v1/query?query=$(python3 -c 'import sys,urllib.parse as u;print(u.quote(sys.argv[1]))' "$1")" 2>/dev/null |
    python3 -c 'import json,sys
for r in json.load(sys.stdin)["data"]["result"]:
    m={k:v for k,v in r["metric"].items() if k in ("alertmanager","job","instance")}
    print("   ",m,r["value"][1])'
}

echo "### $1"
echo "--- POST /api/v2/alerts without credentials (Service / pod)"
for p in default/probe-default sema-monitoring/probe-monitoring-unlabeled amp/probe-amp-ns default/probe-hostnet sema-monitoring/probe-allowed; do
  ns="${p%/*}"; name="${p#*/}"
  printf '  %-45s %s / %s\n' "$p" "$(post "$ns" "$name" "$SVC")" "$(post "$ns" "$name" "$POD")"
done

echo "--- from the allowed pod"
printf '  POST silences, no credentials:   %s\n' "$(code sema-monitoring probe-allowed -H 'Content-Type: application/json' -d "$SILENCE" "$SVC/api/v2/silences")"
printf '  GET  /metrics, no credentials:   %s\n' "$(code sema-monitoring probe-allowed "$SVC/metrics")"
printf '  GET  /-/healthy, no credentials: %s\n' "$(code sema-monitoring probe-allowed "$SVC/-/healthy")"
printf '  GET  /-/ready, no credentials:   %s\n' "$(code sema-monitoring probe-allowed "$SVC/-/ready")"
if [ -n "${LAB_PASS:-}" ]; then
  printf '  POST alerts, valid credentials:  %s\n' "$(post sema-monitoring probe-allowed "$SVC" -u "${LAB_USER}:${LAB_PASS}")"
  printf '  POST alerts, wrong password:     %s\n' "$(post sema-monitoring probe-allowed "$SVC" -u "${LAB_USER}:wrong-password")"
fi

echo "--- AMP pod"
kubectl get pod -n amp amp-0 --no-headers -o custom-columns=READY:.status.containerStatuses[0].ready,RESTARTS:.status.containerStatuses[0].restartCount,STARTED:.status.startTime

echo "--- alerts held by AMP (name, updatedAt)"
AUTH=()
[ -n "${LAB_PASS:-}" ] && AUTH=(-u "${LAB_USER}:${LAB_PASS}")
date -u +'  now %Y-%m-%dT%H:%M:%SZ'
kubectl exec "${ALLOWED[@]}" -- curl -s -m 5 "${AUTH[@]}" "$SVC/api/v2/alerts" 2>/dev/null |
  python3 -c 'import json,sys
try: alerts=json.load(sys.stdin)
except Exception: alerts=[]
for a in alerts:
    if a["labels"].get("alertname","").startswith("Lab") and a["labels"]["alertname"]!="LabProbe":
        print("  ",a["labels"]["alertname"],a["updatedAt"])'

echo "--- Prometheus"
echo "  up{namespace=amp}:"; prom 'up{namespace="amp"}'
echo "  notifications sent:"; prom 'prometheus_notifications_sent_total'
echo "  notification errors:"; prom 'prometheus_notifications_errors_total'
echo "  notifications dropped:"; prom 'prometheus_notifications_dropped_total'

echo "--- Loki ruler (per Alertmanager)"
LOKI_IP="$(kubectl get pod -n sema-monitoring loki-0 -o jsonpath='{.status.podIP}')"
kubectl exec "${ALLOWED[@]}" -- curl -s -m 5 "http://${LOKI_IP}:3100/metrics" 2>/dev/null |
  grep -E '^[a-z_]*prometheus_notifications_(sent|errors|dropped)_total' | cut -c1-150 | sed 's/^/  /'
