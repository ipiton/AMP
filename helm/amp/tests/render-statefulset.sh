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
#   - negative replicas fail the render;
#   - values removed by UPSTREAM-CORE-REMOVAL, top-level and nested, fail the
#     render naming the replacement.
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

echo "replicas 0 stops AMP (database restore), negative fails"
render zero --set alertmanager.replicas=0
doc "${WORK_DIR}/zero.yaml" StatefulSet "${STS}" >"${WORK_DIR}/zero-sts.yaml"
assert_count "${WORK_DIR}/zero-sts.yaml" "replicas: 0" 1 "zero: StatefulSet scaled to 0"
render_fails negative "alertmanager.replicas must not be negative" --set alertmanager.replicas=-1

echo "values of the chart before UPSTREAM-CORE-REMOVAL fail with the replacement"
render_fails old-values "--reset-then-reuse-values" --set-json 'alertmanager=null'
render_fails old-persistence "--reset-then-reuse-values" --set-json 'alertmanager.persistence=null'
render_fails old-configfile "configFile is no longer read" --set configFile.content=x
render_fails old-upstreamcore "renamed to alertmanager" --set upstreamCore.enabled=true
render_fails old-replicacount "use alertmanager.replicas" --set replicaCount=2
render_fails old-cache "cache is no longer read" --set cache.enabled=true
render_fails old-queue "publishing.queue is no longer read" --set publishing.queue.workers=4
render_fails old-retrydelay "llm.retryDelay is no longer read" --set llm.retryDelay=1s
render_fails old-metricsport "service.metricsPort is no longer read" --set service.metricsPort=9090
render_fails old-timeout "gracefulShutdown.timeoutSeconds is no longer read" --set gracefulShutdown.timeoutSeconds=30

echo "webConfig.mountPath must not overlap the chart's mounts"
for p in /etc/amp/config /etc/amp/config/web /etc/amp /data /data/web; do
  render_fails "webconfig-${p//\//_}" "overlaps the chart's mount" \
    --set webConfig.existingSecret=amp-web --set "webConfig.mountPath=${p}"
done
render webconfig-ok --set webConfig.existingSecret=amp-web --set webConfig.mountPath=/etc/amp/web \
  --set monitoring.serviceMonitor.enabled=false

echo "extraEnv after the chart's env"
render extraenv --set 'extraEnv[0].name=INVESTIGATION_TOOLS_DATABASE_DSN' \
  --set 'extraEnv[0].valueFrom.secretKeyRef.name=amp-inv' --set 'extraEnv[0].valueFrom.secretKeyRef.key=dsn'
assert_count "${WORK_DIR}/extraenv.yaml" "name: INVESTIGATION_TOOLS_DATABASE_DSN" 1 "extraenv: env rendered"
assert_count "${WORK_DIR}/extraenv.yaml" "name: amp-inv" 1 "extraenv: secretKeyRef kept"

echo "web auth: the Secret is mounted and passed to --web.config.file"
render web --set webConfig.existingSecret=amp-web --set monitoring.serviceMonitor.enabled=false
doc "${WORK_DIR}/web.yaml" StatefulSet "${STS}" >"${WORK_DIR}/web-sts.yaml"
assert_count "${WORK_DIR}/web-sts.yaml" "--web.config.file=/etc/amp/web/web-config.yml" 1 "web: flag points at the mounted file"
assert_count "${WORK_DIR}/web-sts.yaml" "secretName: amp-web" 1 "web: volume from the operator's Secret"
assert_count "${WORK_DIR}/web-sts.yaml" "mountPath: /etc/amp/web" 1 "web: mounted at webConfig.mountPath"
doc "${WORK_DIR}/one.yaml" StatefulSet "${STS}" >"${WORK_DIR}/one-sts-web.yaml"
assert_count "${WORK_DIR}/one-sts-web.yaml" "--web.config.file" 0 "defaults: no web config flag"

echo "service account token and RBAC only for K8s-Secret discovery"
assert_count "${WORK_DIR}/one-sts-web.yaml" "automountServiceAccountToken: true" 1 "defaults (standard, publishing): token mounted"
assert_line "${WORK_DIR}/one.yaml" "^kind: Role$" 1 "defaults: Role rendered"
render no-publishing --set publishing.enabled=false
doc "${WORK_DIR}/no-publishing.yaml" StatefulSet "${STS}" >"${WORK_DIR}/no-publishing-sts.yaml"
assert_count "${WORK_DIR}/no-publishing-sts.yaml" "automountServiceAccountToken: false" 1 "no publishing: no token"
assert_line "${WORK_DIR}/no-publishing.yaml" "^kind: Role(Binding)?$" 0 "no publishing: no Role, no RoleBinding"
render lite --set profile=lite
doc "${WORK_DIR}/lite.yaml" StatefulSet "${STS}" >"${WORK_DIR}/lite-sts.yaml"
assert_count "${WORK_DIR}/lite-sts.yaml" "automountServiceAccountToken: false" 1 "lite: no token"
assert_line "${WORK_DIR}/lite.yaml" "^kind: Role(Binding)?$" 0 "lite: no Role, no RoleBinding"
assert_count "${WORK_DIR}/lite.yaml" "DATABASE_PASSWORD" 0 "lite: no database password"

echo "llm keys of amp.yaml and the N x LLM warning"
LLM=(--set llm.enabled=true --set llm.apiKey=render-test --set llm.agentMode=true)
render llm "${LLM[@]}"
amp_cfg() { awk '/^  amp\.yaml: \|$/ { on = 1; next } on && !/^    / { on = 0 } on' "$1"; }
amp_cfg "${WORK_DIR}/llm.yaml" >"${WORK_DIR}/llm-amp.yaml"
assert_count "${WORK_DIR}/llm-amp.yaml" "agent_mode: true" 1 "llm: agent_mode from llm.agentMode"
assert_count "${WORK_DIR}/llm-amp.yaml" "render-test" 0 "llm: the key is not in the ConfigMap"
assert_count "${WORK_DIR}/llm.yaml" "name: LLM_API_KEY" 1 "llm: key from the Secret as env"
if helm install amp "${CHART_DIR}" --dry-run=client "${LLM[@]}" --set alertmanager.replicas=3 >"${WORK_DIR}/llm-notes.txt" 2>&1; then
  assert_count "${WORK_DIR}/llm-notes.txt" "llm.enabled with 3 replicas" 1 "NOTES: N x LLM warning with 3 replicas"
else
  fail "llm NOTES: install --dry-run failed: $(tail -3 "${WORK_DIR}/llm-notes.txt")"
fi
helm install amp "${CHART_DIR}" --dry-run=client "${LLM[@]}" >"${WORK_DIR}/llm1-notes.txt" 2>&1 || true
assert_count "${WORK_DIR}/llm1-notes.txt" "LLM calls" 0 "NOTES: no N x LLM warning with one replica"

echo "the AMP pods carry the release selector labels (PostgreSQL NetworkPolicy)"
assert_count "${WORK_DIR}/one-sts-web.yaml" "app.kubernetes.io/instance: amp" 4 "StatefulSet, selector, pods and volumes carry the instance label"

if [ "${failures}" -ne 0 ]; then
  printf '\n%d assertion(s) failed\n' "${failures}" >&2
  exit 1
fi

printf '\nall assertions passed\n'
