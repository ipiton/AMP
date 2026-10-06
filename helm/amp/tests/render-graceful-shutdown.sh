#!/usr/bin/env bash
# Renders the chart and asserts the graceful-shutdown wiring of the AMP
# container (PROD-GRACEFUL-SHUTDOWN): preStop sleep from
# gracefulShutdown.preStopDelay, SERVER_GRACEFUL_SHUTDOWN_TIMEOUT from
# gracefulShutdown.timeoutSeconds, and a grace period that fits both.
#
# Before PROD-GRACEFUL-SHUTDOWN, preStopDelay was a dead key and the grace
# period equalled AMP's own shutdown timeout, leaving no room for a preStop.
# An inconsistent budget does not fail the render on purpose (owner decision,
# Spec.md): NOTES.txt warns instead, and that warning is asserted here.
#
#   helm/amp/tests/render-graceful-shutdown.sh
#
# Runs every assertion, then exits non-zero if any failed.
set -euo pipefail

CHART_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK_DIR="$(mktemp -d)"
trap 'rm -rf "${WORK_DIR}"' EXIT

DEPLOYMENT="templates/deployment.yaml"
WARNING="WARNING: gracefulShutdown.terminationGracePeriodSeconds"

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

# render_deployment <name> <helm args...>
render_deployment() {
  local name="$1"
  shift
  helm template amp "${CHART_DIR}" --show-only "${DEPLOYMENT}" "$@" >"${WORK_DIR}/${name}.yaml"
}

# render_notes <name> <helm args...> -- NOTES.txt is only rendered by install.
render_notes() {
  local name="$1"
  shift
  helm install amp "${CHART_DIR}" --dry-run=client "$@" >"${WORK_DIR}/${name}-notes.txt"
}

echo "defaults: preStop, timeout env and a grace period that fits both"
render_deployment default
assert_contains "${WORK_DIR}/default.yaml" 'command: ["sleep", "5"]' "preStop sleeps preStopDelay (5)"
assert_contains "${WORK_DIR}/default.yaml" "name: SERVER_GRACEFUL_SHUTDOWN_TIMEOUT" "timeout env set"
assert_contains "${WORK_DIR}/default.yaml" 'value: "30s"' "timeout env carries timeoutSeconds (30s)"
assert_contains "${WORK_DIR}/default.yaml" "terminationGracePeriodSeconds: 40" "grace period 40 >= 5 + 30"
assert_absent "${WORK_DIR}/default.yaml" "No preStop hook needed" "stale 'no preStop' comment gone"
render_notes default
assert_absent "${WORK_DIR}/default-notes.txt" "${WARNING}" "no budget warning with defaults"

echo "preStopDelay=0 removes the hook"
render_deployment no-prestop --set gracefulShutdown.preStopDelay=0
assert_absent "${WORK_DIR}/no-prestop.yaml" "preStop:" "no preStop hook"
assert_absent "${WORK_DIR}/no-prestop.yaml" "lifecycle:" "no lifecycle block"

echo "custom values flow through"
render_deployment custom --set gracefulShutdown.preStopDelay=10 \
  --set gracefulShutdown.timeoutSeconds=45 \
  --set gracefulShutdown.terminationGracePeriodSeconds=60
assert_contains "${WORK_DIR}/custom.yaml" 'command: ["sleep", "10"]' "preStop sleeps 10"
assert_contains "${WORK_DIR}/custom.yaml" 'value: "45s"' "timeout env 45s"
assert_contains "${WORK_DIR}/custom.yaml" "terminationGracePeriodSeconds: 60" "grace period 60"
render_notes custom --set gracefulShutdown.preStopDelay=10 \
  --set gracefulShutdown.timeoutSeconds=45 \
  --set gracefulShutdown.terminationGracePeriodSeconds=60
assert_absent "${WORK_DIR}/custom-notes.txt" "${WARNING}" "no warning when 60 >= 10 + 45"

echo "an old grace period (30) still renders, with a warning"
render_deployment old-grace --set gracefulShutdown.terminationGracePeriodSeconds=30
assert_contains "${WORK_DIR}/old-grace.yaml" "terminationGracePeriodSeconds: 30" "render does not fail"
render_notes old-grace --set gracefulShutdown.terminationGracePeriodSeconds=30
assert_contains "${WORK_DIR}/old-grace-notes.txt" "${WARNING}" "budget warning printed"
assert_contains "${WORK_DIR}/old-grace-notes.txt" "Raise terminationGracePeriodSeconds to at least 35" "warning names the required minimum"

echo "production values"
render_deployment production "${PROD_ARGS[@]}"
assert_contains "${WORK_DIR}/production.yaml" 'command: ["sleep", "5"]' "preStop in production"
assert_contains "${WORK_DIR}/production.yaml" "terminationGracePeriodSeconds: 40" "grace period 40 in production"

if [ "${failures}" -ne 0 ]; then
  printf '\n%d assertion(s) failed\n' "${failures}" >&2
  exit 1
fi

printf '\nall assertions passed\n'
