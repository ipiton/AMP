#!/usr/bin/env bash
#
# Dead man's switch e2e, with the production timers. Takes about 20 minutes, so
# it is not part of the release gate:
#
#   ./deploy/e2e-heartbeat/run.sh
#
# Requires Docker Compose, helm, curl and python3. The Alertmanager
# configuration is rendered from the chart with
# examples/k8s/heartbeat-values.yaml, and the ping URL is read from a file
# mounted at the path that example uses. A background loop re-posts Watchdog
# the way Prometheus does.
#
#   1. Pings arrive at the URL from the file, about every 3 minutes.
#   2. AMP stopped: no pings.
#   3. AMP started again: pings resume.
#   4. Watchdog no longer re-posted (Prometheus is dead): it expires, no
#      resolved notification is sent and the pings stop.
set -euo pipefail

COMPOSE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${COMPOSE_DIR}/../.." && pwd)"
cd "$COMPOSE_DIR"

COMPOSE=(docker compose -f docker-compose.yml)
APP_URL="http://localhost:18180"
SINK_URL="http://localhost:18190"
PING_PATH="/ping/amp-heartbeat-e2e"

# Seconds between pings. The route gives 180; the upper bound is what a check
# with a 5m period needs.
MIN_GAP=150
MAX_GAP=240
# Longer than MAX_GAP: a ping in this window could not be a late one.
QUIET_WINDOW=250
ALERT_TTL=90

log() { printf '[heartbeat] %s\n' "$1"; }
fail() { printf '[heartbeat] FAIL: %s\n' "$1" >&2; exit 1; }

AMP_HEARTBEAT_DIR="$(mktemp -d)"
export AMP_HEARTBEAT_DIR
POSTER_PID=""

stop_poster() {
  if [[ -n "$POSTER_PID" ]]; then
    kill "$POSTER_PID" 2>/dev/null || true
    wait "$POSTER_PID" 2>/dev/null || true
    POSTER_PID=""
  fi
}

cleanup() {
  stop_poster
  "${COMPOSE[@]}" down -v --remove-orphans >/dev/null 2>&1 || true
  rm -r "$AMP_HEARTBEAT_DIR"
}
trap cleanup EXIT

hits() { curl -fsS "${SINK_URL}/hits"; }

wait_for_http() {
  local i
  for ((i = 0; i < 120; i++)); do
    curl -fsS -o /dev/null "$1" 2>/dev/null && return 0
    sleep 1
  done
  return 1
}

wait_for_hits() { # <count> <timeout-seconds>
  local deadline=$((SECONDS + $2))
  while ((SECONDS < deadline)); do
    (($(hits) >= $1)) && return 0
    sleep 2
  done
  return 1
}

post_watchdog() {
  python3 - "$ALERT_TTL" <<'PY' | curl -fsS -o /dev/null -X POST "${APP_URL}/api/v2/alerts" -H 'Content-Type: application/json' -d @-
import datetime, json, sys
now = datetime.datetime.now(datetime.timezone.utc)
end = now + datetime.timedelta(seconds=int(sys.argv[1]))
print(json.dumps([{"labels": {"alertname": "Watchdog", "severity": "none"},
                   "startsAt": now.isoformat(), "endsAt": end.isoformat()}]))
PY
}

# Render the configuration exactly as the chart writes it to the ConfigMap.
helm template amp "${REPO_ROOT}/helm/amp" \
  -f "${REPO_ROOT}/examples/k8s/heartbeat-values.yaml" \
  --show-only templates/configmap.yaml |
  awk '/^  alertmanager.yml: \|/ { on = 1; next } on && /^    / { print substr($0, 5); next } on { on = 0 }' \
    >"${AMP_HEARTBEAT_DIR}/alertmanager.yml"
grep -q 'url_file: /etc/amp/secrets/heartbeat_url' "${AMP_HEARTBEAT_DIR}/alertmanager.yml" ||
  fail "rendered configuration has no heartbeat receiver"

mkdir "${AMP_HEARTBEAT_DIR}/secrets"
# The trailing newline is deliberate: files made with echo or an editor have it.
printf 'http://webhook:8090%s\n' "$PING_PATH" >"${AMP_HEARTBEAT_DIR}/secrets/heartbeat_url"
chmod -R a+rX "$AMP_HEARTBEAT_DIR"

"${COMPOSE[@]}" down -v --remove-orphans >/dev/null 2>&1 || true
log "building and starting the stack"
"${COMPOSE[@]}" up -d --build
wait_for_http "${SINK_URL}/healthz" || fail "webhook sink never became healthy"
wait_for_http "${APP_URL}/-/ready" || fail "amp never became ready"

(
  while true; do
    post_watchdog 2>/dev/null || true
    sleep 30
  done
) &
POSTER_PID=$!

log "step 1: waiting for 3 pings (about 6 minutes)"
wait_for_hits 3 480 || fail "expected 3 pings in 8 minutes, got $(hits)"
"${COMPOSE[@]}" logs -t --no-log-prefix webhook | python3 -c '
import datetime, re, sys
path, low, high = sys.argv[1], int(sys.argv[2]), int(sys.argv[3])
times = []
for line in sys.stdin:
    m = re.match(r"(\S{19})\S* webhook hit #\d+ path=(\S+)", line)
    if not m:
        continue
    if m.group(2) != path:
        sys.exit("ping went to an unexpected path")
    times.append(datetime.datetime.strptime(m.group(1), "%Y-%m-%dT%H:%M:%S"))
gaps = [int((b - a).total_seconds()) for a, b in zip(times, times[1:])]
print("[heartbeat] seconds between pings:", gaps)
if len(gaps) < 2 or not all(low <= g <= high for g in gaps):
    sys.exit(f"gaps outside {low}..{high}s")
' "$PING_PATH" "$MIN_GAP" "$MAX_GAP" || fail "ping path or interval is wrong"
curl -fsS "${SINK_URL}/last" | python3 -c '
import json, sys
d = json.load(sys.stdin)
ok = d["receiver"] == "heartbeat" and d["status"] == "firing" and d["alerts"][0]["labels"]["alertname"] == "Watchdog"
sys.exit(0 if ok else "unexpected webhook payload")
' || fail "last ping is not a firing Watchdog"
log "PASS: pings reach the URL from the file at the expected interval"

log "step 2: stopping amp, expecting silence for ${QUIET_WINDOW}s"
"${COMPOSE[@]}" stop amp >/dev/null 2>&1
before="$(hits)"
sleep "$QUIET_WINDOW"
[[ "$(hits)" == "$before" ]] || fail "pings arrived while amp was stopped"
log "PASS: no pings while amp is stopped"

log "step 3: starting amp, expecting pings to resume"
"${COMPOSE[@]}" start amp >/dev/null 2>&1
wait_for_http "${APP_URL}/-/ready" || fail "amp never became ready after the restart"
wait_for_hits $((before + 1)) "$MAX_GAP" || fail "pings did not resume after the restart"
log "PASS: pings resumed after the restart"

log "step 4: Watchdog no longer posted, expecting it to expire and pings to stop"
stop_poster
sleep $((ALERT_TTL + 10))
before="$(hits)"
sleep "$QUIET_WINDOW"
[[ "$(hits)" == "$before" ]] || fail "pings continued after Watchdog expired"
curl -fsS "${SINK_URL}/last" | python3 -c '
import json, sys
sys.exit(0 if json.load(sys.stdin)["status"] == "firing" else "a resolved notification was sent")
' || fail "a resolved notification counted as a ping"
log "PASS: no pings and no resolved notification after Watchdog expired"

log "ALL PASS"
