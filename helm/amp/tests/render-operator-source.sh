#!/usr/bin/env bash
# Renders the chart and asserts alertmanager.configSource (AMCFG-OPERATOR-SOURCE,
# ADR-020): values mode has no operator objects, prepare adds only the
# Alertmanager resource and its base Secret, operator mode mounts the generated
# Secret through a reloader without credentials, and combinations that cannot
# work fail the render.
#
#   helm/amp/tests/render-operator-source.sh
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

assert_contains() {
  if grep -q -F -e "$2" "$1"; then pass "$3"; else fail "$3 (missing: $2)"; fi
}

assert_absent() {
  if grep -q -F -e "$2" "$1"; then fail "$3 (unexpected: $2)"; else pass "$3"; fi
}

CRD=(--api-versions monitoring.coreos.com/v1/Alertmanager)
PREPARE=(--set publishing.enabled=false --set alertmanager.operator.prepare=true "${CRD[@]}")
OPERATOR=(--set publishing.enabled=false --set alertmanager.configSource=operator --set image.tag=0.2.0 "${CRD[@]}")

render() {
  local out="$1"
  shift
  if ! helm template amp "${CHART_DIR}" --namespace amp "$@" >"${out}" 2>"${out}.err"; then
    fail "render failed: $* ($(tail -1 "${out}.err"))"
    : >"${out}"
  fi
}

# assert_render_fails <expected message> <description> <helm args...>
assert_render_fails() {
  local want="$1" desc="$2" err="${WORK_DIR}/fail.err"
  shift 2
  if helm template amp "${CHART_DIR}" --namespace amp "$@" >/dev/null 2>"${err}"; then
    fail "${desc} (render succeeded)"
  elif grep -q -F -e "${want}" "${err}"; then
    pass "${desc}"
  else
    fail "${desc} (other error: $(tail -1 "${err}"))"
  fi
}

# Prints one container of a rendered manifest, up to the next list item at its indent.
container() {
  awk -v name="- name: $2" '
    !found && index($0, name) { found = 1; match($0, /^ */); indent = RLENGTH; print; next }
    found {
      match($0, /^ */)
      if ($0 !~ /^ *$/ && (RLENGTH < indent || (RLENGTH == indent && $0 ~ /^ *- /))) exit
      print
    }' "$1"
}

echo "values (default)"
values="${WORK_DIR}/values.yaml"
render "${values}"
assert_absent "${values}" "kind: Alertmanager" "no Alertmanager resource"
assert_absent "${values}" "operator-base" "no base Secret"
assert_absent "${values}" "config-reloader" "no reloader"
assert_absent "${values}" "web.reload-listen-address" "no reload listener"
assert_contains "${values}" "alertmanager.yml: |" "ConfigMap carries alertmanager.yml"

echo "prepare"
prepare="${WORK_DIR}/prepare.yaml"
render "${prepare}" "${PREPARE[@]}"
assert_contains "${prepare}" "kind: Alertmanager" "Alertmanager resource created"
assert_contains "${prepare}" "name: amp-operator-base" "base Secret created"
assert_contains "${prepare}" "alertmanager.yml: |" "ConfigMap kept"
assert_absent "${prepare}" "config-reloader" "pods unchanged: no reloader"
assert_absent "${prepare}" "web.reload-listen-address" "pods unchanged: no reload listener"

echo "operator"
operator="${WORK_DIR}/operator.yaml"
render "${operator}" "${OPERATOR[@]}"
reloader="${WORK_DIR}/reloader.yaml"
container "${operator}" config-reloader >"${reloader}"
amp="${WORK_DIR}/amp.yaml"
container "${operator}" amp >"${amp}"
assert_contains "${operator}" "replicas: 0" "the operator runs no Alertmanager"
assert_contains "${operator}" 'version: "v0.34.1"' "spec.version"
assert_contains "${operator}" "configSecret: amp-operator-base" "base config Secret"
assert_contains "${operator}" "alertmanagerConfigSelector:" "selector rendered"
assert_absent "${operator}" "alertmanagerConfigNamespaceSelector:" "release namespace only by default"
assert_absent "${operator}" "alertmanager.yml: |" "no ConfigMap"
assert_contains "${operator}" "secretName: alertmanager-amp-generated" "generated Secret mounted"
assert_contains "${amp}" "--config.file=/etc/alertmanager/config_out/alertmanager.env.yaml" "AMP reads the unpacked config"
assert_contains "${amp}" "--web.reload-listen-address=127.0.0.1:9095" "reload listener on loopback"
assert_absent "${amp}" "containerPort: 9095" "reload port not exposed"
if grep -q -E 'mountPath: /etc/alertmanager/config$' "${amp}"; then
  fail "AMP does not mount the generated Secret"
else
  pass "AMP does not mount the generated Secret"
fi
assert_contains "${reloader}" "--reload-url=http://127.0.0.1:9095/-/reload" "reloader calls the loopback listener"
assert_contains "${reloader}" "@sha256:" "reloader image pinned by digest"
assert_absent "${reloader}" "secretKeyRef" "reloader gets no Secret through env"
assert_absent "${operator}" "RELOAD_URL" "no reload URL variable"
assert_absent "${operator}" "shareProcessNamespace" "no shared process namespace"
assert_contains "${operator}" "automountServiceAccountToken: false" "no API token in the pod"
assert_absent "${operator}" "kind: Role" "no Secret-reading role"

echo "operator options"
all_ns="${WORK_DIR}/all-ns.yaml"
render "${all_ns}" "${OPERATOR[@]}" --set-json 'alertmanager.operator.alertmanagerConfigNamespaceSelector={}'
assert_contains "${all_ns}" "alertmanagerConfigNamespaceSelector:" "{} selects every namespace"
none="${WORK_DIR}/none.yaml"
render "${none}" "${OPERATOR[@]}" --set-json 'alertmanager.operator.alertmanagerConfigSelector=null'
assert_absent "${none}" "alertmanagerConfigSelector:" "null selects nothing"
auth="${WORK_DIR}/auth.yaml"
render "${auth}" "${OPERATOR[@]}" --set webConfig.existingSecret=amp-web-config
assert_contains "${auth}" "--web.config.file=" "HTTP authentication works without opening a path"
assert_absent "${auth}" "- /-/reload" "/-/reload stays behind authentication"
prefix="${WORK_DIR}/prefix.yaml"
render "${prefix}" "${OPERATOR[@]}" --set alertmanager.externalURL=https://amp.example/amp/
assert_contains "${prefix}" "--reload-url=http://127.0.0.1:9095/-/reload" "reload URL has no path prefix"

echo "refused"
assert_render_fails "must be values or operator" "unknown configSource" \
  --set alertmanager.configSource=file
assert_render_fails "needs prometheus-operator" "no Alertmanager CRD" \
  --set publishing.enabled=false --set alertmanager.configSource=operator --set image.tag=0.2.0
assert_render_fails "publishing target discovery" "discovery with operator" \
  --set alertmanager.configSource=operator --set image.tag=0.2.0 "${CRD[@]}"
assert_render_fails "publishing target discovery" "discovery with prepare" \
  --set alertmanager.operator.prepare=true "${CRD[@]}"
assert_render_fails "newer than 0.1.0" "default image tag" \
  --set publishing.enabled=false --set alertmanager.configSource=operator "${CRD[@]}"
assert_render_fails "newer than 0.1.0" "image 0.1.0 by digest" \
  "${OPERATOR[@]}" --set 'image.tag=v0.1.0@sha256:abc'
assert_render_fails "at most 50 characters" "name too long" \
  "${OPERATOR[@]}" --set fullnameOverride=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
assert_render_fails "alertmanager.operator.version" "values without operator.version" \
  "${OPERATOR[@]}" --set-json 'alertmanager.operator.version=null'

exact="${WORK_DIR}/exact.yaml"
render "${exact}" "${OPERATOR[@]}" --set fullnameOverride=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
assert_contains "${exact}" "kind: Alertmanager" "name of 50 characters accepted"

if [ "${failures}" -ne 0 ]; then
  printf '%d assertion(s) failed\n' "${failures}" >&2
  exit 1
fi
echo "all assertions passed"
