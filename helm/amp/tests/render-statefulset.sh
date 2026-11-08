#!/usr/bin/env bash
# Renders the chart and checks its workload, amp-upstream as a StatefulSet
# (alertmanager.*, UPSTREAM-HA-MEMBERLIST, UPSTREAM-CORE-REMOVAL, ADR-019):
#
#   - one replica (the default): StatefulSet with a data volumeClaimTemplate,
#     gossip off (--cluster.listen-address= and no --cluster.peer, no gossip
#     ports), alertmanager.yml and amp.yaml from ConfigMaps;
#   - three replicas: Parallel pods, one --cluster.peer per pod over the
#     headless Service, which publishes not-ready pods and gossip on 9094
#     TCP and UDP;
#   - the Service selects the AMP pods by component=application; the headless
#     Service is component=cluster, so the ServiceMonitor does not scrape twice;
#   - an external URL with a path prefixes the probe paths;
#   - replicas: 0 fails the render.
#
#   helm/amp/tests/render-statefulset.sh
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

# assert_count <file> <pattern> <expected> <description>: fixed-string lines.
assert_count() {
  local got
  # grep -c exits 1 on zero matches; the count is still printed.
  got="$(grep -c -F -e "$2" "$1" || true)"
  if [ "${got}" -eq "$3" ]; then pass "$4"; else fail "$4 (want $3 x '$2', got ${got})"; fi
}

# assert_line <file> <regex> <expected> <description>: extended-regex lines.
assert_line() {
  local got
  got="$(grep -c -E -e "$2" "$1" || true)"
  if [ "${got}" -eq "$3" ]; then pass "$4"; else fail "$4 (want $3 x /$2/, got ${got})"; fi
}

# doc <file> <kind> <name>: the rendered document of <kind> named <name>.
doc() {
  awk -v kind="$2" -v name="$3" '
    function flush() { if (k && n) printf "%s", buf; k = 0; n = 0; buf = "" }
    /^---/ { flush(); next }
    $0 == "kind: " kind { k = 1 }
    /^  name: / && $2 == name { n = 1 }
    { buf = buf $0 "\n" }
    END { flush() }' "$1"
}

# render <name> <helm args...>: renders into ${WORK_DIR}/<name>.yaml; a failed
# render is an assertion failure, not an abort.
render() {
  local name="$1"
  shift
  if helm template amp "${CHART_DIR}" "$@" >"${WORK_DIR}/${name}.yaml" 2>"${WORK_DIR}/${name}.err"; then
    pass "${name}: renders"
  else
    fail "${name}: render failed: $(cat "${WORK_DIR}/${name}.err")"
    : >"${WORK_DIR}/${name}.yaml"
  fi
}

# render_fails <name> <expected message fragment> <helm args...>
render_fails() {
  local name="$1" want="$2"
  shift 2
  if helm template amp "${CHART_DIR}" "$@" >/dev/null 2>"${WORK_DIR}/${name}.err"; then
    fail "${name}: render succeeded, want failure"
  elif grep -q -F -e "${want}" "${WORK_DIR}/${name}.err"; then
    pass "${name}: render fails with '${want}'"
  else
    fail "${name}: render failed for another reason: $(cat "${WORK_DIR}/${name}.err")"
  fi
}

STS=amp
HEADLESS=amp-headless

echo "one replica (defaults)"
render one
doc "${WORK_DIR}/one.yaml" StatefulSet "${STS}" >"${WORK_DIR}/one-sts.yaml"
assert_count "${WORK_DIR}/one-sts.yaml" "replicas: 1" 1 "one: replicas 1"
assert_line "${WORK_DIR}/one-sts.yaml" '^ +- --cluster\.listen-address=$' 1 "one: gossip off (empty --cluster.listen-address)"
assert_count "${WORK_DIR}/one-sts.yaml" "--cluster.peer=" 0 "one: no --cluster.peer"
assert_count "${WORK_DIR}/one-sts.yaml" "name: gossip-" 0 "one: no gossip container ports"
assert_count "${WORK_DIR}/one-sts.yaml" "volumeClaimTemplates:" 1 "one: data volumeClaimTemplate"
assert_count "${WORK_DIR}/one-sts.yaml" "--storage.path=/data" 1 "one: snapshots on the volume"
assert_count "${WORK_DIR}/one-sts.yaml" "--config.file=/etc/amp/config/alertmanager.yml" 1 "one: alertmanager.yml passed"
assert_count "${WORK_DIR}/one-sts.yaml" "--amp.config.file=/etc/amp/config/amp.yaml" 1 "one: amp.yaml passed"
assert_count "${WORK_DIR}/one-sts.yaml" "path: /-/ready" 1 "one: readiness probe at the root"
doc "${WORK_DIR}/one.yaml" ConfigMap "${STS}" >"${WORK_DIR}/one-cm.yaml"
assert_count "${WORK_DIR}/one-cm.yaml" "alertmanager.yml: |" 1 "one: alertmanager.yml ConfigMap"
assert_count "${WORK_DIR}/one-cm.yaml" "receiver: default" 1 "one: default route receiver"
doc "${WORK_DIR}/one.yaml" ConfigMap "${STS}-amp-config" >"${WORK_DIR}/one-amp-cm.yaml"
assert_count "${WORK_DIR}/one-amp-cm.yaml" "amp.yaml: |" 1 "one: amp.yaml ConfigMap"
assert_count "${WORK_DIR}/one.yaml" "kind: Deployment" 0 "one: no Deployment"

echo "three replicas"
render three --set alertmanager.replicas=3 --namespace mon
doc "${WORK_DIR}/three.yaml" StatefulSet "${STS}" >"${WORK_DIR}/three-sts.yaml"
assert_count "${WORK_DIR}/three-sts.yaml" "podManagementPolicy: Parallel" 1 "three: Parallel pods"
assert_count "${WORK_DIR}/three-sts.yaml" "--cluster.listen-address=0.0.0.0:9094" 1 "three: gossip on 9094"
assert_count "${WORK_DIR}/three-sts.yaml" "--cluster.advertise-address=[\$(POD_IP)]:9094" 1 "three: advertises the pod IP"
assert_line "${WORK_DIR}/three-sts.yaml" "--cluster\.peer=${STS}-[0-2]\.${HEADLESS}\.mon\.svc:9094$" 3 "three: one peer per pod over the headless Service"
assert_count "${WORK_DIR}/three-sts.yaml" "--cluster.peer-timeout=15s" 1 "three: upstream peer timeout"
assert_count "${WORK_DIR}/three-sts.yaml" "serviceName: ${HEADLESS}" 1 "three: governed by the headless Service"
doc "${WORK_DIR}/three.yaml" Service "${HEADLESS}" >"${WORK_DIR}/three-headless.yaml"
assert_count "${WORK_DIR}/three-headless.yaml" "clusterIP: None" 1 "three: headless"
assert_count "${WORK_DIR}/three-headless.yaml" "publishNotReadyAddresses: true" 1 "three: publishes not-ready pods"
assert_count "${WORK_DIR}/three-headless.yaml" "protocol: UDP" 1 "three: gossip UDP port"
assert_count "${WORK_DIR}/three-headless.yaml" "port: 9094" 2 "three: gossip TCP and UDP on 9094"

echo "selectors"
doc "${WORK_DIR}/three.yaml" Service "${STS}" >"${WORK_DIR}/three-amp-svc.yaml"
assert_count "${WORK_DIR}/three-amp-svc.yaml" "app.kubernetes.io/component: application" 2 "selectors: Service labelled and selecting component=application"
assert_count "${WORK_DIR}/three-headless.yaml" "app.kubernetes.io/component: cluster" 1 "selectors: headless Service is component=cluster"
assert_count "${WORK_DIR}/three-sts.yaml" "app.kubernetes.io/component: application" 4 "selectors: StatefulSet, selector, pods and volumes carry component=application"

echo "external URL with a path"
render prefixed --set alertmanager.externalURL=https://am.example.com/alertmanager/
doc "${WORK_DIR}/prefixed.yaml" StatefulSet "${STS}" >"${WORK_DIR}/prefixed-sts.yaml"
assert_count "${WORK_DIR}/prefixed-sts.yaml" "--web.external-url=https://am.example.com/alertmanager/" 1 "prefixed: external URL passed through"
assert_count "${WORK_DIR}/prefixed-sts.yaml" "path: /alertmanager/-/ready" 1 "prefixed: readiness under the prefix"
assert_count "${WORK_DIR}/prefixed-sts.yaml" "path: /alertmanager/-/healthy" 2 "prefixed: liveness and startup under the prefix"

echo "invalid"
render_fails zero "alertmanager.replicas must be at least 1" --set alertmanager.replicas=0

if [ "${failures}" -ne 0 ]; then
  printf '\n%d assertion(s) failed\n' "${failures}" >&2
  exit 1
fi

printf '\nall assertions passed\n'
