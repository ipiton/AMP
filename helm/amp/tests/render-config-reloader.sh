#!/usr/bin/env bash
# Renders the chart with the config-reloader sidecar off and on, and asserts the
# guards that stop it being deployed in a shape where it silently does nothing.
#
# The chart had no test harness before this, so this is a plain script rather
# than a framework: run it from anywhere, it needs only helm.
#
#   helm/amp/tests/render-config-reloader.sh
#
# Exits non-zero on the first failed assertion.
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
  # -F -e: the patterns include leading dashes and regex metacharacters
  # ("--config-file=..."), which a bare `grep -- "$2"` would either treat as
  # options or as a regex that matches far too much.
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

if ! command -v helm >/dev/null 2>&1; then
  echo "SKIP: helm not installed"
  exit 0
fi

if [ ! -d "${CHART_DIR}/charts" ]; then
  echo "Fetching chart dependencies..."
  helm dependency build "${CHART_DIR}" >/dev/null
fi

cat >"${WORK_DIR}/on.yaml" <<'YAML'
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

echo "chart renders with the sidecar OFF (default):"
helm template amp "${CHART_DIR}" >"${WORK_DIR}/off.yaml"
assert_absent "${WORK_DIR}/off.yaml" "name: config-reloader" "no sidecar container"
assert_absent "${WORK_DIR}/off.yaml" "amp-configfile" "no config-file ConfigMap"
assert_absent "${WORK_DIR}/off.yaml" "AMP_CONFIG_FILE" "no AMP_CONFIG_FILE env"
assert_absent "${WORK_DIR}/off.yaml" "shareProcessNamespace" "no shared PID namespace"

echo "chart renders with the sidecar ON:"
helm template amp "${CHART_DIR}" -f "${WORK_DIR}/on.yaml" >"${WORK_DIR}/on-rendered.yaml"
assert_contains "${WORK_DIR}/on-rendered.yaml" "name: config-reloader" "sidecar container"
assert_contains "${WORK_DIR}/on-rendered.yaml" "amp-configfile" "config-file ConfigMap"
assert_contains "${WORK_DIR}/on-rendered.yaml" "AMP_CONFIG_FILE" "AMP_CONFIG_FILE env for the app"
assert_contains "${WORK_DIR}/on-rendered.yaml" "--config-file=/etc/amp/config.yaml" "sidecar watches the mounted file"
assert_contains "${WORK_DIR}/on-rendered.yaml" "--health-url=http://127.0.0.1" "reload is verified over loopback"
assert_contains "${WORK_DIR}/on-rendered.yaml" "readOnlyRootFilesystem: true" "sidecar filesystem is read-only"
assert_contains "${WORK_DIR}/on-rendered.yaml" "runAsNonRoot: true" "sidecar runs non-root"
# http is the default method, so no shared PID namespace should be requested.
assert_absent "${WORK_DIR}/on-rendered.yaml" "shareProcessNamespace" "http method needs no shared PID namespace"

echo "signal method requests a shared PID namespace:"
helm template amp "${CHART_DIR}" -f "${WORK_DIR}/on.yaml" \
  --set configReloader.method=signal \
  --set configReloader.shareProcessNamespace=true >"${WORK_DIR}/signal.yaml"
assert_contains "${WORK_DIR}/signal.yaml" "shareProcessNamespace: true" "shared PID namespace"
assert_contains "${WORK_DIR}/signal.yaml" "--method=signal" "signal method"

echo "production values enable the sidecar:"
helm template amp "${CHART_DIR}" -f "${CHART_DIR}/values-production.yaml" >"${WORK_DIR}/prod.yaml"
assert_contains "${WORK_DIR}/prod.yaml" "name: config-reloader" "sidecar enabled in production"

echo "misconfigurations are refused at render time:"
assert_render_fails "sidecar without a config file" \
  "requires configFile.enabled=true" \
  --set configReloader.enabled=true
assert_render_fails "config file without content" \
  "requires configFile.content" \
  --set configFile.enabled=true
assert_render_fails "signal method without a shared PID namespace" \
  "requires configReloader.shareProcessNamespace=true" \
  -f "${WORK_DIR}/on.yaml" --set configReloader.method=signal

if [ "${failures}" -ne 0 ]; then
  printf '\n%d assertion(s) failed\n' "${failures}" >&2
  exit 1
fi

printf '\nall assertions passed\n'
