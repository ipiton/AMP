#!/usr/bin/env bash
# Renders the AMP ServiceMonitor (PROD-INGRESS-HARDENING, ADR-015) and asserts:
#   - it selects the AMP Service only (the postgres/redis Services share the
#     chart's selector labels) and scrapes /metrics on the `http` port;
#   - it is gated on monitoring.prometheusEnabled and
#     monitoring.serviceMonitor.enabled;
#   - with web auth on it refuses to render without credentials (it would only
#     get 401) and wires the Secret's keys in when given;
#   - values without monitoring.serviceMonitor (an old release upgraded with
#     --reuse-values) still render it with the defaults, guard included.
#
# Before this task it rendered unconditionally and selected labels the Service
# never had, so it scraped nothing.
#
#   helm/amp/tests/render-servicemonitor.sh
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

# doc <file> <kind> <name>: prints the rendered document with that kind and
# metadata.name (no yq in the gate's toolchain).
doc() {
  awk -v kind="kind: $2" -v name="  name: $3" '
    function flush() { if (k && n) printf "%s", buf; buf = ""; k = 0; n = 0 }
    /^---/ { flush(); next }
    { buf = buf $0 "\n" }
    $0 == kind { k = 1 }
    $0 == name { n = 1 }
    END { flush() }' "$1"
}

# block <file> <start-line> : prints the lines from <start-line> up to the next
# line at the same or a smaller indent (one YAML mapping, e.g. "  selector:").
block() {
  awk -v start="$2" '
    function indent(s) { match(s, /^ */); return RLENGTH }
    !on && $0 == start { on = 1; ind = indent($0); print; next }
    on && NF && indent($0) <= ind { exit }
    on { print }' "$1"
}

# services_matching <file> <label-line>...: names of the rendered Services whose
# metadata.labels contain every given label line (what a ServiceMonitor
# selector with those matchLabels picks).
services_matching() {
  local file="$1"
  shift
  # "|"-joined: BSD awk refuses a newline inside -v.
  awk -v want="$(IFS='|'; printf '%s' "$*")" '
    function flush(   i, ok) {
      if (svc) { ok = 1; for (i = 1; i <= nw; i++) if (!(w[i] in have)) ok = 0; if (ok) print name }
      svc = 0; inlabels = 0; name = ""; split("", have)
    }
    BEGIN { nw = split(want, w, "|") }
    /^---/ { flush(); next }
    $0 == "kind: Service" { svc = 1 }
    /^  name: / && name == "" { name = $2 }
    /^  labels:/ { inlabels = 1; next }
    inlabels && /^    / { sub(/^ +/, ""); have[$0] = 1; next }
    inlabels { inlabels = 0 }
    END { flush() }' "${file}"
}

if ! command -v helm >/dev/null 2>&1; then
  echo "SKIP: helm not installed"
  exit 0
fi

if [ ! -d "${CHART_DIR}/charts" ]; then
  echo "Fetching chart dependencies..."
  helm dependency build "${CHART_DIR}" >/dev/null
fi

LABEL="app.kubernetes.io/component: application"
NO_CREDS="the ServiceMonitor has no credentials"

echo "default ServiceMonitor:"
render default
doc "${WORK_DIR}/default.yaml" ServiceMonitor amp >"${WORK_DIR}/sm.yaml"
assert_contains "${WORK_DIR}/sm.yaml" "kind: ServiceMonitor" "rendered by default"
block "${WORK_DIR}/sm.yaml" "  selector:" >"${WORK_DIR}/sm-selector.yaml"
assert_contains "${WORK_DIR}/sm-selector.yaml" "${LABEL}" "selects by component"
assert_contains "${WORK_DIR}/sm.yaml" "- port: http" "scrapes the http port"
assert_contains "${WORK_DIR}/sm.yaml" "path: /metrics" "...at /metrics"
assert_contains "${WORK_DIR}/sm.yaml" "interval: 30s" "default interval"
assert_contains "${WORK_DIR}/sm.yaml" "scrapeTimeout: 10s" "default scrape timeout"
assert_absent "${WORK_DIR}/sm.yaml" "basicAuth" "no credentials without web auth"

echo "its selector matches the AMP Service only:"
# Every matchLabels line of the selector, as the Service metadata spells it.
# (A read loop, not mapfile: macOS ships bash 3.2.)
selector_labels=()
other_labels=()
while IFS= read -r line; do
  selector_labels+=("${line}")
  [ "${line}" = "${LABEL}" ] || other_labels+=("${line}")
done < <(grep -E '^      [a-z]' "${WORK_DIR}/sm-selector.yaml" | sed 's/^ *//')
matched="$(services_matching "${WORK_DIR}/default.yaml" "${selector_labels[@]}" | tr '\n' ' ')"
if [ "${matched}" = "amp " ]; then
  pass "selector (${#selector_labels[@]} labels) picks exactly: amp"
else
  fail "selector picks '${matched}', want 'amp '"
fi
# The selector labels alone, without component, would also pick postgres/redis.
without_component="$(services_matching "${WORK_DIR}/default.yaml" "${other_labels[@]}" | wc -l | tr -d ' ')"
if [ "${without_component}" -gt 1 ]; then
  pass "component is what tells the Services apart (${without_component} match without it)"
else
  fail "expected the selector without component to match several Services, got ${without_component}"
fi

echo "gated on prometheusEnabled and serviceMonitor.enabled:"
render prom-off --set monitoring.prometheusEnabled=false
doc "${WORK_DIR}/prom-off.yaml" ServiceMonitor amp >"${WORK_DIR}/prom-off-sm.yaml"
assert_absent "${WORK_DIR}/prom-off-sm.yaml" "kind: ServiceMonitor" "off with monitoring.prometheusEnabled=false"
render sm-off --set monitoring.serviceMonitor.enabled=false
doc "${WORK_DIR}/sm-off.yaml" ServiceMonitor amp >"${WORK_DIR}/sm-off-sm.yaml"
assert_absent "${WORK_DIR}/sm-off-sm.yaml" "kind: ServiceMonitor" "off with serviceMonitor.enabled=false"
assert_contains "${WORK_DIR}/sm-off.yaml" "# Source: amp/templates/redis-servicemonitor.yaml" "Redis ServiceMonitor unaffected"

echo "web auth needs credentials:"
assert_render_fails "webConfig.existingSecret without basicAuth" "${NO_CREDS}" \
  --set webConfig.existingSecret=sm-test-web
render auth --set webConfig.existingSecret=sm-test-web \
  --set monitoring.serviceMonitor.basicAuth.secretName=sm-test-metrics
doc "${WORK_DIR}/auth.yaml" ServiceMonitor amp >"${WORK_DIR}/auth-sm.yaml"
block "${WORK_DIR}/auth-sm.yaml" "      basicAuth:" >"${WORK_DIR}/auth-ba.yaml"
block "${WORK_DIR}/auth-ba.yaml" "        username:" >"${WORK_DIR}/auth-user.yaml"
block "${WORK_DIR}/auth-ba.yaml" "        password:" >"${WORK_DIR}/auth-pass.yaml"
assert_contains "${WORK_DIR}/auth-user.yaml" "name: sm-test-metrics" "username from the Secret"
assert_contains "${WORK_DIR}/auth-user.yaml" "key: username" "...default key"
assert_contains "${WORK_DIR}/auth-pass.yaml" "name: sm-test-metrics" "password from the Secret"
assert_contains "${WORK_DIR}/auth-pass.yaml" "key: password" "...default key"
render auth-keys --set monitoring.serviceMonitor.basicAuth.secretName=sm-test-metrics \
  --set monitoring.serviceMonitor.basicAuth.usernameKey=u \
  --set monitoring.serviceMonitor.basicAuth.passwordKey=p
doc "${WORK_DIR}/auth-keys.yaml" ServiceMonitor amp >"${WORK_DIR}/auth-keys-sm.yaml"
assert_contains "${WORK_DIR}/auth-keys-sm.yaml" "key: u" "custom username key"
assert_contains "${WORK_DIR}/auth-keys-sm.yaml" "key: p" "custom password key"

echo "labels and timing:"
render labels --set monitoring.serviceMonitor.labels.release=kube-prometheus \
  --set monitoring.serviceMonitor.interval=15s
doc "${WORK_DIR}/labels.yaml" ServiceMonitor amp >"${WORK_DIR}/labels-sm.yaml"
block "${WORK_DIR}/labels-sm.yaml" "  labels:" >"${WORK_DIR}/labels-meta.yaml"
assert_contains "${WORK_DIR}/labels-meta.yaml" "release: kube-prometheus" "extra labels in metadata"
assert_contains "${WORK_DIR}/labels-sm.yaml" "interval: 15s" "interval override"

echo "values without monitoring.serviceMonitor (--reuse-values from an old release):"
# --set <key>=null deletes the key from the merged values -- what an old
# release's stored values look like to this chart.
render reuse --set monitoring.serviceMonitor=null
doc "${WORK_DIR}/reuse.yaml" ServiceMonitor amp >"${WORK_DIR}/reuse-sm.yaml"
assert_contains "${WORK_DIR}/reuse-sm.yaml" "kind: ServiceMonitor" "still rendered"
assert_contains "${WORK_DIR}/reuse-sm.yaml" "interval: 30s" "default interval"
assert_contains "${WORK_DIR}/reuse-sm.yaml" "scrapeTimeout: 10s" "default scrape timeout"
assert_render_fails "...and the credentials guard still runs" "${NO_CREDS}" \
  --set monitoring.serviceMonitor=null --set webConfig.existingSecret=sm-test-web

if [ "${failures}" -ne 0 ]; then
  printf '\n%d assertion(s) failed\n' "${failures}" >&2
  exit 1
fi

printf '\nall assertions passed\n'
