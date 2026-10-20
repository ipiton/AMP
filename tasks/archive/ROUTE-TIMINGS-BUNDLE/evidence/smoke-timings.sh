#!/usr/bin/env bash
# ROUTE-TIMINGS-BUNDLE smoke: real binary, real webhook sink.
# Usage: smoke-timings.sh <amp-server-binary> <out-dir>
# Config: root group_wait 3s / group_interval 10s; child "inherit" without
# timings (+ grandchild "grand" without timings); child "zero" with
# group_wait: 0s. Prints send->delivery latency per route.
set -euo pipefail
BIN=$1; OUT=$2; HERE=$(cd "$(dirname "$0")" && pwd)
AMP_PORT=18093; HOOK_PORT=18094
mkdir -p "$OUT"
cat > "$OUT/config.yaml" <<YAML
profile: lite
storage: {backend: filesystem, filesystem_path: $OUT/amp.db, path: ""}
server: {port: $AMP_PORT, host: 127.0.0.1}
log: {level: info, format: json, output: stdout}
publishing: {enabled: true}
grouping: {enabled: true}
route:
  receiver: root
  group_by: [alertname]
  group_wait: 3s
  group_interval: 10s
  repeat_interval: 1h
  routes:
    - matchers: ['route="inherit"']
      receiver: inherit
      routes:
        - matchers: ['sub="grand"']
          receiver: grand
    - matchers: ['route="zero"']
      receiver: zero
      group_wait: 0s
receivers:
  - {name: root, webhook_configs: [{url: "http://127.0.0.1:$HOOK_PORT/root"}]}
  - {name: inherit, webhook_configs: [{url: "http://127.0.0.1:$HOOK_PORT/inherit"}]}
  - {name: grand, webhook_configs: [{url: "http://127.0.0.1:$HOOK_PORT/grand"}]}
  - {name: zero, webhook_configs: [{url: "http://127.0.0.1:$HOOK_PORT/zero"}]}
YAML
python3 "$HERE/timing_receiver.py" $HOOK_PORT > "$OUT/hooks.log" 2>&1 & HOOK=$!
ENVIRONMENT=development AMP_CONFIG_FILE="$OUT/config.yaml" "$BIN" > "$OUT/amp.log" 2>&1 & AMP=$!
trap 'kill $AMP $HOOK 2>/dev/null || true' EXIT
for _ in $(seq 60); do curl -fs "http://127.0.0.1:$AMP_PORT/-/ready" >/dev/null 2>&1 && break; sleep 0.5; done
post() { # alertname, extra-labels-json
  curl -fs -XPOST "http://127.0.0.1:$AMP_PORT/api/v2/alerts" -H 'Content-Type: application/json' \
    -d "[{\"labels\":{\"alertname\":\"$1\"$2},\"startsAt\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\"}]" >/dev/null
  echo "$(python3 -c 'import time;print(int(time.time()*1000))') $1" >> "$OUT/sent.log"
}
: > "$OUT/sent.log"
post RootAlert ''
post InheritAlert ',"route":"inherit"'
post GrandAlert ',"route":"inherit","sub":"grand"'
post ZeroAlert ',"route":"zero"'
sleep "${WAIT:-40}"
echo "route        sent->delivered"
while read -r sent name; do
  got=$(awk -v n="$name" '$3==n {print $1; exit}' "$OUT/hooks.log")
  if [ -n "$got" ]; then printf '%-13s %6.2fs\n' "$name" "$(echo "($got-$sent)/1000" | bc -l)"; else printf '%-13s not delivered\n' "$name"; fi
done < "$OUT/sent.log"
grep '"Alert routed to group"' "$OUT/amp.log" | sed -E 's/.*"receiver":"([^"]*)".*"group_wait":([0-9]+).*"group_interval":([0-9]+).*/receiver=\1 group_wait=\2 group_interval=\3/' || true
