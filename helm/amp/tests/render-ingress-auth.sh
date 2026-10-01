#!/usr/bin/env bash
# Renders the chart with the Ingress on and asserts it never renders without
# authentication (PROD-INGRESS-HARDENING, ADR-015): either in-process basic auth
# (webConfig.existingSecret) or an explicit ingress.externalAuth=true.
#
# The guard is keyed on the Ingress, not on values-production.yaml, so it is
# checked on the chart defaults too.
#
#   helm/amp/tests/render-ingress-auth.sh
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

# assert_contains <file> <pattern> <description>
assert_contains() {
  if grep -q -F -e "$2" "$1"; then pass "$3"; else fail "$3 (missing: $2)"; fi
}

# assert_absent <file> <pattern> <description>
assert_absent() {
  if grep -q -F -e "$2" "$1"; then fail "$3 (unexpected: $2)"; else pass "$3"; fi
}

# assert_render_fails <description> <message-substring> <helm args...>
assert_render_fails() {
  local description="$1" expected="$2"
  shift 2

  local output
  if output="$(helm template amp "${CHART_DIR}" "$@" 2>&1)"; then
    fail "${description} (render succeeded, expected failure)"
    return
  fi
  if printf '%s' "${output}" | grep -q -F -e "${expected}"; then
    pass "${description}"
  else
    fail "${description} (wrong error: ${output})"
  fi
}

# render <name> <helm args...>: renders into ${WORK_DIR}/<name>.yaml, or records
# a failure (and leaves an empty file, so later assertions fail too).
render() {
  local name="$1"
  shift
  if ! helm template amp "${CHART_DIR}" "$@" >"${WORK_DIR}/${name}.yaml" 2>"${WORK_DIR}/${name}.err"; then
    fail "${name}: render failed: $(cat "${WORK_DIR}/${name}.err")"
    : >"${WORK_DIR}/${name}.yaml"
  fi
}

if ! command -v helm >/dev/null 2>&1; then
  echo "SKIP: helm not installed"
  exit 0
fi

if [ ! -d "${CHART_DIR}/charts" ]; then
  echo "Fetching chart dependencies..."
  helm dependency build "${CHART_DIR}" >/dev/null
fi

GUARD_MSG="exposes the whole AMP API"
PASSWORDS=(--set postgresql.password=render-test-placeholder
  --set cache.auth.password=render-test-placeholder)
PROD_ARGS=(-f "${CHART_DIR}/values-production.yaml" "${PASSWORDS[@]}")
PLACEHOLDERS=(-f "${CHART_DIR}/tests/values-production-placeholders.yaml")
# A NetworkPolicy source only: lets prod past its own guards without supplying
# any auth, so the Ingress guard is the one that answers.
SENDER=(--set-json 'networkPolicy.alertSenders=[{"podSelector":{}}]')

echo "defaults render no Ingress:"
render default
assert_absent "${WORK_DIR}/default.yaml" "kind: Ingress" "no Ingress by default"

echo "an Ingress without auth is refused:"
assert_render_fails "defaults + ingress.enabled (guard not tied to the prod file)" \
  "${GUARD_MSG}" --set ingress.enabled=true
assert_render_fails "values-production.yaml without auth" \
  "${GUARD_MSG}" "${PROD_ARGS[@]}" "${SENDER[@]}"
# The gate renders prod with placeholders; without them prod must not render at
# all, or the placeholders would hide a guard that stopped firing.
assert_render_fails "values-production.yaml as shipped" \
  "execution error" "${PROD_ARGS[@]}"

echo "only a real true waives in-process auth:"
for value in false no '"false"' True 1; do
  assert_render_fails "--set-string ingress.externalAuth=${value}" \
    "${GUARD_MSG}" --set ingress.enabled=true --set-string "ingress.externalAuth=${value}"
done

echo "an Ingress with auth renders:"
render prod-webconfig "${PROD_ARGS[@]}" "${PLACEHOLDERS[@]}"
assert_contains "${WORK_DIR}/prod-webconfig.yaml" "kind: Ingress" "prod + webConfig.existingSecret"
render prod-external "${PROD_ARGS[@]}" "${SENDER[@]}" --set ingress.externalAuth=true
assert_contains "${WORK_DIR}/prod-external.yaml" "kind: Ingress" "prod + ingress.externalAuth=true"
render external-string --set ingress.enabled=true --set-string ingress.externalAuth=true
assert_contains "${WORK_DIR}/external-string.yaml" "kind: Ingress" "--set-string ingress.externalAuth=true"

echo "chart packaging keeps the helm test hook and drops the render tests:"
# .helmignore must exclude the chart-root tests/ (placeholder values are never
# for a real install) but not templates/tests/, the `helm test` hook.
assert_contains "${WORK_DIR}/default.yaml" "# Source: amp/templates/tests/" "helm test hook still rendered"
helm package "${CHART_DIR}" -d "${WORK_DIR}" >/dev/null
tar -tzf "${WORK_DIR}"/amp-*.tgz >"${WORK_DIR}/package.txt"
assert_contains "${WORK_DIR}/package.txt" "amp/templates/tests/" "helm test hook packaged"
assert_absent "${WORK_DIR}/package.txt" "amp/tests/" "render tests not packaged"

if [ "${failures}" -ne 0 ]; then
  printf '\n%d assertion(s) failed\n' "${failures}" >&2
  exit 1
fi

printf '\nall assertions passed\n'
