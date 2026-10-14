#!/usr/bin/env bash
# PROD-HARDCODED-FILTER smoke: real binary (deploy/smoke stack), real webhook.
#   A) stock smoke config: TestRunnerDown / environment=testing / namespace=tmp
#      and namespace=dev-sandbox alerts are delivered.
#   B) config + blackhole routes from the CHANGELOG migration note: loads with
#      W024, the same kind of alerts are not delivered, a control alert is.
set -uo pipefail

REPO=/Users/vit/Documents/Projects/AMP/amp
SMOKE_DIR="$REPO/deploy/smoke"
APP=http://localhost:18080
HOOK=http://localhost:18090
WAIT=8
COMPOSE=(docker compose -f "$SMOKE_DIR/docker-compose.yml")

AMP_SMOKE_CONFIG_DIR="$(mktemp -d)"
export AMP_SMOKE_CONFIG_DIR
FAILS=0

log() { printf '[smoke] %s\n' "$*"; }
check() { # check <desc> <got> <want>
  if [[ "$2" == "$3" ]]; then log "PASS: $1 (got $2)"; else log "FAIL: $1 (got $2, want $3)"; FAILS=$((FAILS + 1)); fi
}
cleanup() { "${COMPOSE[@]}" down -v --remove-orphans >/dev/null 2>&1 || true; rm -r "$AMP_SMOKE_CONFIG_DIR"; }
trap cleanup EXIT

wait_http() { for _ in $(seq 1 90); do curl -fsS -o /dev/null "$1" 2>/dev/null && return 0; sleep 1; done; return 1; }
hits() { curl -fsS "$HOOK/hits"; }
last_alertname() { curl -fsS "$HOOK/last" | jq -r '.alerts[0].labels.alertname // ""'; }

post() { # post <alertname> <extra labels json fragment>
  local now; now="$(date -u +%Y-%m-%dT%H:%M:%S.000Z)"
  curl -fsS -o /dev/null -w '%{http_code}' -X POST "$APP/api/v2/alerts" -H 'Content-Type: application/json' \
    -d "[{\"labels\":{\"alertname\":\"$1\",\"severity\":\"warning\"$2},\"annotations\":{\"summary\":\"filter smoke\"},\"startsAt\":\"$now\"}]"
}

up() {
  "${COMPOSE[@]}" down -v --remove-orphans >/dev/null 2>&1 || true
  "${COMPOSE[@]}" up -d --build >/dev/null 2>&1 || { log "compose up failed"; exit 1; }
  wait_http "$HOOK/healthz" || { log "webhook never healthy"; exit 1; }
  wait_http "$APP/healthz" || { log "amp never healthy"; "${COMPOSE[@]}" logs amp | tail -40; exit 1; }
}

# expect_delivery <alertname> <labels> <want delta>
expect_delivery() {
  local before after code
  before="$(hits)"
  code="$(post "$1" "$2")"
  check "POST $1 {$2} accepted" "$code" "200"
  sleep "$WAIT"
  after="$(hits)"
  check "webhook hits for $1" "$((after - before))" "$3"
  if [[ "$3" -gt 0 ]]; then check "delivered payload alertname" "$(last_alertname)" "$1"; fi
}

# ---------- A: stock config ----------
cp "$SMOKE_DIR/config.yaml" "$AMP_SMOKE_CONFIG_DIR/config.yaml"
log "=== A: stock deploy/smoke/config.yaml ==="
up
expect_delivery "TestRunnerDown" ',"namespace":"tmp","environment":"testing"' 1
expect_delivery "testlowercase"  ',"environment":"test"' 1
expect_delivery "SandboxDiskFull" ',"namespace":"dev-sandbox"' 1
blocked="$(curl -fsS "$APP/metrics" | grep -E '^alert_history_filter_blocked_total\{reason="(test_alert|disabled_namespace)"' || true)"
check "no test_alert/disabled_namespace drops in /metrics" "${blocked:-none}" "none"

# ---------- B: migration-note blackhole routes ----------
python3 - "$SMOKE_DIR/config.yaml" "$AMP_SMOKE_CONFIG_DIR/config.yaml" <<'PY'
import sys
src, dst = sys.argv[1], sys.argv[2]
text = open(src).read()
text = text.replace("""  repeat_interval: 1h
""", """  repeat_interval: 1h
  routes:
    - receiver: blackhole
      matchers:
        - alertname=~"(?i)test.*"
    - receiver: blackhole
      matchers:
        - environment=~"test|testing"
    - receiver: blackhole
      matchers:
        - namespace=~"dev-sandbox|tmp"
""", 1)
text += "  - name: blackhole\n"
open(dst, "w").write(text)
PY
log "=== B: config with blackhole routes from the migration note ==="
sed -n '/^route:/,$p' "$AMP_SMOKE_CONFIG_DIR/config.yaml"
up
w024="$("${COMPOSE[@]}" logs amp 2>&1 | grep -c 'W024' || true)"
check "W024 logged at startup (count>0)" "$([[ "$w024" -gt 0 ]] && echo yes || echo no)" "yes"
"${COMPOSE[@]}" logs amp 2>&1 | grep 'W024' | head -2
expect_delivery "TestRunnerDown2" ',"namespace":"prod"' 0
expect_delivery "testLowerCase2"  ',"namespace":"prod"' 0
expect_delivery "EnvOnlyAlert"    ',"environment":"testing"' 0
expect_delivery "NsOnlyAlert"     ',"namespace":"dev-sandbox"' 0
expect_delivery "ProdDiskFull"    ',"namespace":"prod","environment":"production"' 1
log "blackhole drops metric:"
curl -fsS "$APP/metrics" | grep -E '^alert_history_publishing_blackhole_drops_total' || log "(metric absent)"

log "TOTAL FAILS: $FAILS"
[[ "$FAILS" -eq 0 ]]
