#!/usr/bin/env bash
# Renders the chart and asserts the value of GROUPING_ENABLED it passes to the
# application.
#
# The chart always sets GROUPING_ENABLED from values, and the environment
# overrides the config file, so the chart default is what a Helm installation
# gets regardless of the default in the code. PROD-GROUPING-DEFAULT turned
# both on; this test keeps them from drifting apart again.
#
#   helm/amp/tests/render-grouping-default.sh
#
# Runs every assertion, then exits non-zero if any failed.
set -euo pipefail

CHART_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK_DIR="$(mktemp -d)"
trap 'rm -rf "${WORK_DIR}"' EXIT

failures=0

pass() { printf '  ok   %s\n' "$1"; }
fail() {
  printf '  FAIL %s\n' "$1" >&2
  failures=$((failures + 1))
}

# assert_count <file> <pattern> <expected> <description>
assert_count() {
  local got
  # grep -c exits 1 on zero matches; the count is still printed.
  got="$(grep -c -F -e "$2" "$1" || true)"
  if [ "${got}" -eq "$3" ]; then pass "$4"; else fail "$4 (want $3 x '$2', got ${got})"; fi
}

if ! command -v helm >/dev/null 2>&1; then
  echo "SKIP: helm not installed"
  exit 0
fi

if [ ! -d "${CHART_DIR}/charts" ]; then
  echo "Fetching chart dependencies..."
  helm dependency build "${CHART_DIR}" >/dev/null
fi

# values-production.yaml ships no passwords, auth or NetworkPolicy sources
# (guards fail the render until they are supplied) -- placeholders, as
# scripts/release-gate.sh does.
PROD_ARGS=(-f "${CHART_DIR}/values-production.yaml"
  -f "${CHART_DIR}/tests/values-production-placeholders.yaml"
  --set postgresql.password=render-test-placeholder
  --set cache.auth.password=render-test-placeholder)

# check_grouping <name> <expected> <helm args...>
check_grouping() {
  local name="$1" want="$2"
  shift 2
  echo "${name}:"
  helm template amp "${CHART_DIR}" "$@" >"${WORK_DIR}/${name}.yaml"
  assert_count "${WORK_DIR}/${name}.yaml" "GROUPING_ENABLED: \"${want}\"" 1 "GROUPING_ENABLED is \"${want}\""
  assert_count "${WORK_DIR}/${name}.yaml" "GROUPING_ENABLED:" 1 "GROUPING_ENABLED is set exactly once"
}

check_grouping default true
check_grouping lite true --set profile=lite
check_grouping production true "${PROD_ARGS[@]}"
check_grouping opt-out false --set grouping.enabled=false

if [ "${failures}" -ne 0 ]; then
  printf '\n%d assertion(s) failed\n' "${failures}" >&2
  exit 1
fi

printf '\nall assertions passed\n'
