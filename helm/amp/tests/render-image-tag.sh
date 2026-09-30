#!/usr/bin/env bash
# Renders the chart with each shipped values profile and asserts that the app
# and config-reloader images default to the chart's appVersion, which is the
# tag release.yml publishes (and refuses to publish a tag that differs).
#
# Before PROD-RELEASE-V010-PREP, values.yaml and values-production.yaml pinned
# tags that were never published (0.0.1, 1.0.0) and silently overrode
# .Chart.AppVersion; this test keeps that from coming back.
#
#   helm/amp/tests/render-image-tag.sh
#
# Runs every assertion, then exits non-zero if any failed.
set -euo pipefail

CHART_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK_DIR="$(mktemp -d)"
trap 'rm -rf "${WORK_DIR}"' EXIT

APP_IMAGE="ghcr.io/ipiton/amp"
RELOADER_IMAGE="ghcr.io/ipiton/amp-config-reloader"

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

APP_VERSION="$(sed -n 's/^appVersion: *"\{0,1\}\([^"]*\)"\{0,1\} *$/\1/p' "${CHART_DIR}/Chart.yaml")"
if [ -z "${APP_VERSION}" ]; then
  echo "FAIL: no appVersion in ${CHART_DIR}/Chart.yaml" >&2
  exit 1
fi
echo "Chart appVersion: ${APP_VERSION}"

# values-production.yaml ships no passwords (a `required` guard fails the
# render until they are supplied) -- placeholders, as scripts/release-gate.sh
# does.
PROD_ARGS=(-f "${CHART_DIR}/values-production.yaml"
  --set postgresql.password=render-test-placeholder
  --set cache.auth.password=render-test-placeholder)

# check_app_image <name> <helm args...>
check_app_image() {
  local name="$1"
  shift
  echo "${name}: app image defaults to appVersion"
  helm template amp "${CHART_DIR}" "$@" >"${WORK_DIR}/${name}.yaml"
  assert_count "${WORK_DIR}/${name}.yaml" "image: \"${APP_IMAGE}:${APP_VERSION}\"" 1 "one app container on ${APP_IMAGE}:${APP_VERSION}"
  # An empty tag with no default would render "amp:" -- a pull of `latest`.
  assert_absent "${WORK_DIR}/${name}.yaml" "image: \"${APP_IMAGE}:\"" "tag is never empty"
}

check_app_image default
check_app_image production "${PROD_ARGS[@]}"
check_app_image lite --set profile=lite

echo "config-reloader image defaults to appVersion:"
cat >"${WORK_DIR}/reloader-on.yaml" <<'YAML'
configFile:
  enabled: true
  content: |
    route:
      receiver: default
    receivers:
      - name: default
configReloader:
  enabled: true
YAML
helm template amp "${CHART_DIR}" -f "${WORK_DIR}/reloader-on.yaml" >"${WORK_DIR}/reloader.yaml"
assert_count "${WORK_DIR}/reloader.yaml" "image: \"${RELOADER_IMAGE}:${APP_VERSION}\"" 1 "sidecar on ${RELOADER_IMAGE}:${APP_VERSION}"
assert_count "${WORK_DIR}/reloader.yaml" "image: \"${APP_IMAGE}:${APP_VERSION}\"" 1 "app container unchanged next to the sidecar"

echo "an explicit image.tag still wins:"
helm template amp "${CHART_DIR}" --set image.tag=9.9.9 >"${WORK_DIR}/pinned.yaml"
assert_count "${WORK_DIR}/pinned.yaml" "image: \"${APP_IMAGE}:9.9.9\"" 1 "app container on the pinned tag"
assert_absent "${WORK_DIR}/pinned.yaml" "image: \"${APP_IMAGE}:${APP_VERSION}\"" "appVersion not used when pinned"

if [ "${failures}" -ne 0 ]; then
  printf '\n%d assertion(s) failed\n' "${failures}" >&2
  exit 1
fi

printf '\nall assertions passed\n'
