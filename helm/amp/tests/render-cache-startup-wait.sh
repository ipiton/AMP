#!/usr/bin/env bash
# Renders the AMP Deployment and asserts where REDIS_STARTUP_WAIT is set
# (AMP-CACHE-NO-RECONNECT).
#
# The standard profile retries an unreachable cache for redis.startup_wait
# (60s by default) at startup. Where the chart configures no cache it still
# passes REDIS_ADDR=localhost:6379, so it must also pass REDIS_STARTUP_WAIT=0s
# or such a start would wait the whole window for nothing.
#
#   helm/amp/tests/render-cache-startup-wait.sh
#
# Runs every assertion, then exits non-zero if any failed.
set -euo pipefail

CHART_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK_DIR="$(mktemp -d)"
trap 'rm -rf "${WORK_DIR}"' EXIT

DEPLOYMENT="templates/deployment.yaml"

failures=0

pass() { printf '  ok   %s\n' "$1"; }
fail() {
  printf '  FAIL %s\n' "$1" >&2
  failures=$((failures + 1))
}

# assert_contains <file> <pattern> <description>
assert_contains() {
  if grep -q -F -e "$2" "$1"; then pass "$3"; else fail "$3 (missing: $2)"; fi
}

# assert_absent <file> <pattern> <description>
assert_absent() {
  if grep -q -F -e "$2" "$1"; then fail "$3 (unexpected: $2)"; else pass "$3"; fi
}

# assert_zero_wait <file> <description>: REDIS_STARTUP_WAIT is "0s".
assert_zero_wait() {
  if grep -A1 -F -e "name: REDIS_STARTUP_WAIT" "$1" | grep -q -F -e 'value: "0s"'; then
    pass "$2"
  else
    fail "$2 (REDIS_STARTUP_WAIT is not \"0s\")"
  fi
}

echo "== standard defaults: cache on, AMP waits for it =="
helm template t "${CHART_DIR}" -s "${DEPLOYMENT}" >"${WORK_DIR}/default.yaml"
assert_contains "${WORK_DIR}/default.yaml" 'value: "t-amp-redis:6379"' "REDIS_ADDR is the chart's Valkey"
assert_absent "${WORK_DIR}/default.yaml" "REDIS_STARTUP_WAIT" "no REDIS_STARTUP_WAIT (app default 60s)"

echo "== standard, cache.enabled=false =="
helm template t "${CHART_DIR}" -s "${DEPLOYMENT}" --set cache.enabled=false >"${WORK_DIR}/nocache.yaml"
assert_contains "${WORK_DIR}/nocache.yaml" 'value: "localhost:6379"' "REDIS_ADDR falls back to localhost"
assert_zero_wait "${WORK_DIR}/nocache.yaml" "REDIS_STARTUP_WAIT=0s"

echo "== lite profile =="
helm template t "${CHART_DIR}" -s "${DEPLOYMENT}" --set profile=lite >"${WORK_DIR}/lite.yaml"
assert_zero_wait "${WORK_DIR}/lite.yaml" "REDIS_STARTUP_WAIT=0s"

if [ "${failures}" -gt 0 ]; then
  echo "${failures} assertion(s) failed" >&2
  exit 1
fi
echo "all assertions passed"
