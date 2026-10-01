#!/usr/bin/env bash
# Renders the AMP NetworkPolicy (PROD-INGRESS-HARDENING, ADR-015) and the pod
# label it selects on, and asserts:
#   - enabled with no effective source, it refuses to render (it would drop
#     every alert sender);
#   - one rule per source role, the `http` port only, no Egress;
#   - the AMP pods carry app.kubernetes.io/component: application in the pod
#     template only -- never in the immutable Deployment selector or the
#     Service selector -- and podLabels cannot override it;
#   - the chart's Redis NetworkPolicy admits those pods;
#   - values without the networkPolicy section (an old release upgraded with
#     --reuse-values) render no policy instead of crashing.
#
#   helm/amp/tests/render-networkpolicy.sh
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

# assert_count <file> <pattern> <expected> <description>
assert_count() {
  local got
  # grep -c exits 1 on zero matches; the count is still printed.
  got="$(grep -c -F -e "$2" "$1" || true)"
  if [ "${got}" -eq "$3" ]; then pass "$4"; else fail "$4 (want $3 x '$2', got ${got})"; fi
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

if ! command -v helm >/dev/null 2>&1; then
  echo "SKIP: helm not installed"
  exit 0
fi

if [ ! -d "${CHART_DIR}/charts" ]; then
  echo "Fetching chart dependencies..."
  helm dependency build "${CHART_DIR}" >/dev/null
fi

LABEL="app.kubernetes.io/component: application"
NO_SOURCES="with no sources would block every alert sender"
PROD_ARGS=(-f "${CHART_DIR}/values-production.yaml"
  --set postgresql.password=render-test-placeholder
  --set cache.auth.password=render-test-placeholder)
PLACEHOLDERS=(-f "${CHART_DIR}/tests/values-production-placeholders.yaml")
CONTROLLER=(--set-json 'networkPolicy.ingressController=[{"namespaceSelector":{"matchLabels":{"kubernetes.io/metadata.name":"np-test-controller"}}}]')
SENDER=(--set-json 'networkPolicy.alertSenders=[{"namespaceSelector":{"matchLabels":{"kubernetes.io/metadata.name":"np-test-sender"}}}]')
SCRAPER=(--set-json 'networkPolicy.metricsScrapers=[{"podSelector":{"matchLabels":{"np-test":"scraper"}}}]')

echo "defaults render no AMP NetworkPolicy:"
render default
doc "${WORK_DIR}/default.yaml" NetworkPolicy amp >"${WORK_DIR}/default-np.yaml"
assert_absent "${WORK_DIR}/default-np.yaml" "kind: NetworkPolicy" "no AMP policy by default"

echo "an enabled policy with no effective source is refused:"
assert_render_fails "values-production.yaml with no sources" "${NO_SOURCES}" "${PROD_ARGS[@]}"
assert_render_fails "defaults + networkPolicy.enabled" "${NO_SOURCES}" --set networkPolicy.enabled=true
assert_render_fails "ingressController only, Ingress off" "${NO_SOURCES}" \
  --set networkPolicy.enabled=true "${CONTROLLER[@]}"

echo "production policy (placeholder sender):"
render prod "${PROD_ARGS[@]}" "${PLACEHOLDERS[@]}"
doc "${WORK_DIR}/prod.yaml" NetworkPolicy amp >"${WORK_DIR}/prod-np.yaml"
block "${WORK_DIR}/prod-np.yaml" "  podSelector:" >"${WORK_DIR}/prod-np-selector.yaml"
assert_contains "${WORK_DIR}/prod-np-selector.yaml" "${LABEL}" "selects the AMP pods by component"
assert_contains "${WORK_DIR}/prod-np.yaml" "# alert-senders" "alert-senders rule"
assert_contains "${WORK_DIR}/prod-np.yaml" "kubernetes.io/metadata.name: release-gate-placeholder" "peers rendered as given"
assert_count "${WORK_DIR}/prod-np.yaml" "port: http" 1 "one rule, http port only"
assert_absent "${WORK_DIR}/prod-np.yaml" "Egress" "no Egress policyType"
assert_absent "${WORK_DIR}/prod-np.yaml" "# ingress-controller" "no controller rule with no controller peers"

echo "one rule per role; the controller only with an Ingress:"
render roles --set networkPolicy.enabled=true "${CONTROLLER[@]}" "${SENDER[@]}" "${SCRAPER[@]}"
doc "${WORK_DIR}/roles.yaml" NetworkPolicy amp >"${WORK_DIR}/roles-np.yaml"
assert_absent "${WORK_DIR}/roles-np.yaml" "np-test-controller" "controller peers dropped while ingress.enabled=false"
assert_contains "${WORK_DIR}/roles-np.yaml" "# metrics-scrapers" "metrics-scrapers rule"
assert_count "${WORK_DIR}/roles-np.yaml" "port: http" 2 "sender + scraper rules, http only"
render roles-ingress --set networkPolicy.enabled=true "${CONTROLLER[@]}" "${SENDER[@]}" \
  --set ingress.enabled=true --set ingress.externalAuth=true
doc "${WORK_DIR}/roles-ingress.yaml" NetworkPolicy amp >"${WORK_DIR}/roles-ingress-np.yaml"
assert_contains "${WORK_DIR}/roles-ingress-np.yaml" "# ingress-controller" "controller rule with ingress.enabled"
assert_contains "${WORK_DIR}/roles-ingress-np.yaml" "np-test-controller" "controller peers rendered"
assert_count "${WORK_DIR}/roles-ingress-np.yaml" "port: http" 2 "controller + sender rules"

echo "extraIngress is rendered as-is:"
render extra --set networkPolicy.enabled=true \
  --set-json 'networkPolicy.extraIngress=[{"from":[{"ipBlock":{"cidr":"10.99.0.0/16"}}],"ports":[{"protocol":"TCP","port":"http"}]}]'
doc "${WORK_DIR}/extra.yaml" NetworkPolicy amp >"${WORK_DIR}/extra-np.yaml"
assert_contains "${WORK_DIR}/extra-np.yaml" "cidr: 10.99.0.0/16" "raw rule rendered"

echo "the component label lives in the pod template only:"
doc "${WORK_DIR}/default.yaml" Deployment amp >"${WORK_DIR}/deploy.yaml"
block "${WORK_DIR}/deploy.yaml" "  selector:" >"${WORK_DIR}/deploy-selector.yaml"
block "${WORK_DIR}/deploy.yaml" "    metadata:" >"${WORK_DIR}/deploy-pod-meta.yaml"
assert_contains "${WORK_DIR}/deploy-pod-meta.yaml" "${LABEL}" "pod template carries the label"
assert_contains "${WORK_DIR}/deploy-selector.yaml" "app.kubernetes.io/instance: amp" "Deployment selector found"
assert_absent "${WORK_DIR}/deploy-selector.yaml" "app.kubernetes.io/component" "Deployment selector unchanged (immutable)"
doc "${WORK_DIR}/default.yaml" Service amp >"${WORK_DIR}/svc.yaml"
block "${WORK_DIR}/svc.yaml" "  selector:" >"${WORK_DIR}/svc-selector.yaml"
assert_contains "${WORK_DIR}/svc-selector.yaml" "app.kubernetes.io/instance: amp" "Service selector found"
assert_absent "${WORK_DIR}/svc-selector.yaml" "app.kubernetes.io/component" "Service selector unchanged"

echo "podLabels cannot override the component label:"
assert_render_fails "podLabels with app.kubernetes.io/component" \
  "podLabels must not set app.kubernetes.io/component" \
  --set-string 'podLabels.app\.kubernetes\.io/component=other'
render podlabels --set podLabels.team=np-test
assert_contains "${WORK_DIR}/podlabels.yaml" "team: np-test" "other podLabels still render"

echo "the Redis NetworkPolicy admits the AMP pods:"
render redis --set valkey.networkPolicy.enabled=true
doc "${WORK_DIR}/redis.yaml" NetworkPolicy amp-redis >"${WORK_DIR}/redis-np.yaml"
block "${WORK_DIR}/redis-np.yaml" "  - from:" >"${WORK_DIR}/redis-np-from.yaml"
assert_contains "${WORK_DIR}/redis-np-from.yaml" "${LABEL}" "first Redis rule selects component: application"
assert_contains "${WORK_DIR}/redis-np-from.yaml" "port: 6379" "...on the Redis port"

echo "values without the networkPolicy section (--reuse-values from an old release):"
# --set <key>=null deletes the key from the merged values -- what an old
# release's stored values look like to this chart.
render reuse --set networkPolicy=null
doc "${WORK_DIR}/reuse.yaml" NetworkPolicy amp >"${WORK_DIR}/reuse-np.yaml"
assert_contains "${WORK_DIR}/reuse.yaml" "kind: Deployment" "renders"
assert_absent "${WORK_DIR}/reuse-np.yaml" "kind: NetworkPolicy" "no AMP policy"

if [ "${failures}" -ne 0 ]; then
  printf '\n%d assertion(s) failed\n' "${failures}" >&2
  exit 1
fi

printf '\nall assertions passed\n'
