#!/usr/bin/env bash
#
# PHASE-8 release rollout package: single-node smoke e2e.
#
# NOT part of the default `go test ./...` gate -- run it explicitly:
#
#   ./deploy/smoke/run.sh
#
# Requires Docker/Compose. Complements deploy/e2e-ha/run.sh's 2-replica HA
# test with a fast (<2min on a warm Docker build cache) single-node
# pre-release check:
#
#   1. Brings up the app (lite profile, no Postgres/Redis) + a minimal
#      webhook sink (webhook_receiver.py), waits for both /healthz.
#   2. POSTs an alert, waits group_wait+margin, asserts the webhook sink
#      recorded EXACTLY ONE hit AND inspects that hit's actual JSON body
#      (v4 shape: version/receiver/groupLabels, plus the posted alertname
#      and labels) -- not just the count. Unlike deploy/e2e-ha (which
#      predates AMP-PARITY-WAVE6-EPIC's config-target auto-provisioning
#      and disables publishing.enabled because real delivery needed a live
#      Kubernetes API), this is a REAL HTTP delivery -- config.yaml's
#      `receivers:` block provisions the webhook target with zero
#      Kubernetes Secrets.
#   3. Creates a silence matching a second alertname, THEN posts an alert
#      with that alertname, and asserts NO hit ever arrives. This is a
#      deliberate substitution for the brief's literal "post alert, let it
#      fire, silence it, assert the NEXT fire is suppressed" sequence:
#      that would need waiting out a full group_interval (on top of
#      group_wait) to observe a real second fire, which doesn't fit this
#      script's <2min budget. Silencing BEFORE the first post exercises
#      the same matcher/suppression code path (silence lookup happens on
#      every fire attempt, first or Nth) for a fraction of the wall-clock
#      cost -- it does not, on its own, prove that an ALREADY-notified
#      group's later repeat is suppressed; that specific case is
#      deploy/e2e-ha's territory (longer timers, HA-focused already).
#   4. Overwrites the running app's config with a modified copy (one added
#      blackhole receiver) and posts /-/reload; asserts GET /api/v2/status's
#      config.original now contains it -- proving the reload actually
#      re-read the file.
#   5. Tears the stack down (unless KEEP_UP=1) and removes its scratch
#      config dir.
#
# Idempotent: every resource (containers, scratch config dir) is created
# fresh under a mktemp'd AMP_SMOKE_CONFIG_DIR and torn down in the EXIT
# trap, and the stack is torn down before `up` too, so a prior crashed run
# doesn't leave stale containers/ports behind.
set -euo pipefail

COMPOSE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$COMPOSE_DIR"

COMPOSE=(docker compose -f docker-compose.yml)
APP_PORT=18080
WEBHOOK_PORT=18090
KEEP_UP="${KEEP_UP:-0}"
GROUP_WAIT_MARGIN=8

log() { printf '[smoke] %s\n' "$1"; }
fail() { printf '[smoke] FAIL: %s\n' "$1" >&2; exit 1; }

# Pick a JSON parser up front (same pattern as deploy/e2e-ha/run.sh) so a
# missing-parser failure is a clear message here rather than a cryptic
# empty-string comparison deep in Step 2's payload assertions.
if command -v jq >/dev/null 2>&1; then
  JSON_PARSER="jq"
elif command -v python3 >/dev/null 2>&1; then
  JSON_PARSER="python3"
else
  fail "neither jq nor python3 available to inspect the webhook payload"
fi

AMP_SMOKE_CONFIG_DIR="$(mktemp -d)"
export AMP_SMOKE_CONFIG_DIR
cp "$COMPOSE_DIR/config.yaml" "$AMP_SMOKE_CONFIG_DIR/config.yaml"

remove_scratch_dir() {
  # AMP_SMOKE_CONFIG_DIR is always this run's own mktemp -d output (an
  # absolute path under the OS temp dir, never user-supplied), so removing
  # it recursively on exit is safe cleanup, not a destructive operation on
  # anything the caller owns.
  [[ -n "$AMP_SMOKE_CONFIG_DIR" && -d "$AMP_SMOKE_CONFIG_DIR" ]] && rm -r "$AMP_SMOKE_CONFIG_DIR"
}

cleanup() {
  if [[ "$KEEP_UP" != "1" ]]; then
    log "tearing down stack"
    "${COMPOSE[@]}" down -v --remove-orphans >/dev/null 2>&1 || true
  else
    log "KEEP_UP=1 set -- leaving stack running (docker compose -f $COMPOSE_DIR/docker-compose.yml down -v to clean up)"
  fi
  remove_scratch_dir
}
trap cleanup EXIT

wait_for_http() {
  local url="$1" tries="${2:-60}"
  for ((i = 0; i < tries; i++)); do
    if curl -fsS -o /dev/null "$url" 2>/dev/null; then
      return 0
    fi
    sleep 1
  done
  return 1
}

hits() {
  curl -fsS "http://localhost:${WEBHOOK_PORT}/hits" 2>/dev/null || echo 0
}

last_payload() {
  curl -fsS "http://localhost:${WEBHOOK_PORT}/last" 2>/dev/null || echo '{}'
}

payload_field() {
  # payload_field <json> <jq-filter> <python-expr-over-"data">
  # One shared extractor for every field check below, mirroring
  # deploy/e2e-ha/run.sh's peer_count()'s jq/python3 split. Prints '' (not
  # an error) on any parse/lookup failure so callers get a clean mismatch
  # message instead of a raw traceback.
  local json="$1" jq_filter="$2" py_expr="$3"
  if [[ "$JSON_PARSER" == "jq" ]]; then
    printf '%s' "$json" | jq -r "$jq_filter" 2>/dev/null || true
  else
    printf '%s' "$json" | python3 -c "
import json, sys
try:
    data = json.load(sys.stdin)
except Exception:
    print('')
else:
    try:
        print($py_expr)
    except Exception:
        print('')
" 2>/dev/null || true
  fi
}

post_alert() {
  local alertname="$1" starts_at
  starts_at="$(date -u +%Y-%m-%dT%H:%M:%S.000Z)"
  curl -fsS -X POST "http://localhost:${APP_PORT}/api/v2/alerts" \
    -H 'Content-Type: application/json' \
    -d "[{\"labels\":{\"alertname\":\"${alertname}\",\"severity\":\"warning\"},\"annotations\":{\"summary\":\"smoke test\"},\"startsAt\":\"${starts_at}\"}]"
}

post_silence() {
  local alertname="$1" body
  body=$(printf '{"matchers":[{"name":"alertname","value":"%s","isRegex":false}],"startsAt":"2020-01-01T00:00:00Z","endsAt":"2099-01-01T00:00:00Z","createdBy":"smoke-test","comment":"smoke test silence"}' "$alertname")
  curl -fsS -X POST "http://localhost:${APP_PORT}/api/v2/silences" \
    -H 'Content-Type: application/json' \
    -d "$body"
}

log "cleaning up any stale stack from a prior crashed run"
"${COMPOSE[@]}" down -v --remove-orphans >/dev/null 2>&1 || true

log "building + starting stack (webhook sink, amp)"
"${COMPOSE[@]}" up -d --build

log "waiting for webhook sink /healthz"
wait_for_http "http://localhost:${WEBHOOK_PORT}/healthz" 30 || fail "webhook sink never became healthy"

log "waiting for amp /healthz"
wait_for_http "http://localhost:${APP_PORT}/healthz" 90 || fail "amp never became healthy"
log "both services healthy"

# --- Step 2: real webhook delivery -----------------------------------------
ALERT_1="SmokeTestAlertDelivered"
before="$(hits)"
log "posting alert '$ALERT_1'"
post_alert "$ALERT_1" >/dev/null

log "waiting group_wait + margin (${GROUP_WAIT_MARGIN}s) for delivery"
sleep "$GROUP_WAIT_MARGIN"

after="$(hits)"
delivered=$((after - before))
[[ "$delivered" -eq 1 ]] || fail "expected exactly 1 webhook hit for '$ALERT_1', got $delivered (before=$before after=$after)"
log "PASS: webhook received exactly 1 real delivery for '$ALERT_1'"

# Content, not just count: the delivered body must actually be the v4
# group payload (formatGroupUpstream, internal/infrastructure/publishing/
# formatter.go) carrying THIS alert -- a stale/wrong/empty body would
# still pass the count-only check above.
payload="$(last_payload)"

got_version="$(payload_field "$payload" '.version' "data.get('version','')")"
[[ "$got_version" == "4" ]] || fail "webhook payload .version = '$got_version', expected '4' (v4 shape). payload: $payload"

got_receiver="$(payload_field "$payload" '.receiver' "data.get('receiver','')")"
[[ "$got_receiver" == "default" ]] || fail "webhook payload .receiver = '$got_receiver', expected 'default'. payload: $payload"

got_alertname="$(payload_field "$payload" '.alerts[0].labels.alertname' "(data.get('alerts') or [{}])[0].get('labels',{}).get('alertname','')")"
[[ "$got_alertname" == "$ALERT_1" ]] || fail "webhook payload .alerts[0].labels.alertname = '$got_alertname', expected '$ALERT_1'. payload: $payload"

got_severity="$(payload_field "$payload" '.alerts[0].labels.severity' "(data.get('alerts') or [{}])[0].get('labels',{}).get('severity','')")"
[[ "$got_severity" == "warning" ]] || fail "webhook payload .alerts[0].labels.severity = '$got_severity', expected 'warning'. payload: $payload"

log "PASS: webhook payload is v4-shaped (version=4, receiver=default) and carries alertname='$ALERT_1' severity=warning"

# --- Step 3: silence suppresses the next fire ------------------------------
ALERT_2="SmokeTestAlertSilenced"
log "creating silence matching alertname='$ALERT_2'"
post_silence "$ALERT_2" >/dev/null

before="$(hits)"
log "posting alert '$ALERT_2' (should be silenced)"
post_alert "$ALERT_2" >/dev/null

log "waiting group_wait + margin (${GROUP_WAIT_MARGIN}s) -- expecting NO delivery"
sleep "$GROUP_WAIT_MARGIN"

after="$(hits)"
suppressed=$((after - before))
[[ "$suppressed" -eq 0 ]] || fail "expected 0 webhook hits for silenced '$ALERT_2', got $suppressed (before=$before after=$after)"
log "PASS: silence suppressed the notification for '$ALERT_2'"

# --- Step 4: /-/reload with a modified config is actually applied ---------
log "swapping in config-reloaded.yaml and posting /-/reload"
cp "$COMPOSE_DIR/config-reloaded.yaml" "$AMP_SMOKE_CONFIG_DIR/config.yaml"

reload_status="$(curl -s -o /dev/null -w '%{http_code}' -X POST "http://localhost:${APP_PORT}/-/reload" \
  -H 'Content-Type: application/json' -d '{}')"
[[ "$reload_status" == "200" ]] || fail "POST /-/reload returned HTTP $reload_status, expected 200"

status_body="$(curl -fsS "http://localhost:${APP_PORT}/api/v2/status")"
printf '%s' "$status_body" | grep -q "smoke-reload-marker" ||
  fail "GET /api/v2/status's config.original does not contain 'smoke-reload-marker' after reload -- the new config was not applied. body: $status_body"
log "PASS: /-/reload applied the modified config (config.original now contains the reload marker receiver)"

log "ALL PASS"
