#!/usr/bin/env bash
# Renders the chart and asserts extraVolumes / extraVolumeMounts
# (HELM-EXTRA-VOLUMES): a Secret with receiver credentials is mounted into the
# amp container only, configFile.content can point *_file fields at it, empty
# values change nothing, and a volume name or mountPath the chart already uses
# fails the render instead of producing a Pod that the API server rejects or
# that shadows config.yaml.
#
#   helm/amp/tests/render-extra-volumes.sh
#
# Runs every assertion, then exits non-zero if any failed.
set -euo pipefail

CHART_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK_DIR="$(mktemp -d)"
trap 'rm -rf "${WORK_DIR}"' EXIT

DEPLOYMENT="templates/deployment.yaml"

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

# assert_same <file-a> <file-b> <description>
assert_same() {
  if cmp -s "$1" "$2"; then pass "$3"; else fail "$3 ($(basename "$1") != $(basename "$2"))"; fi
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

# render_deployment <name> <helm args...>
render_deployment() {
  local name="$1"
  shift
  helm template amp "${CHART_DIR}" --show-only "${DEPLOYMENT}" "$@" >"${WORK_DIR}/${name}.yaml"
}

# split_containers <name>: <name>-amp.yaml holds the containers list up to the
# config-reloader sidecar, <name>-reloader.yaml the sidecar up to the next
# pod-spec key, <name>-volumes.yaml the pod volumes list.
split_containers() {
  awk -v amp="${WORK_DIR}/$1-amp.yaml" -v rel="${WORK_DIR}/$1-reloader.yaml" -v vol="${WORK_DIR}/$1-volumes.yaml" '
    /^      containers:/ { out = amp; next }
    /^        - name: config-reloader$/ { out = rel }
    /^      volumes:/ { out = vol; next }
    /^      [a-zA-Z]/ { out = "" }
    out != "" { print > out }
  ' "${WORK_DIR}/$1.yaml"
  touch "${WORK_DIR}/$1-amp.yaml" "${WORK_DIR}/$1-reloader.yaml" "${WORK_DIR}/$1-volumes.yaml"
}

if ! command -v helm >/dev/null 2>&1; then
  echo "SKIP: helm not installed"
  exit 0
fi

# The chart generates a random Valkey password per render; pin it so two
# renders of the same values compare byte for byte.
PIN=(--set valkey.password.value=render-test-placeholder)

cat >"${WORK_DIR}/values-secret.yaml" <<'YAML'
extraVolumes:
  - name: receiver-secrets
    secret:
      secretName: amp-receiver-secrets
      defaultMode: 0440
extraVolumeMounts:
  - name: receiver-secrets
    mountPath: /etc/amp/secrets
    readOnly: true
configFile:
  enabled: true
  content: |
    route:
      receiver: slack
    receivers:
      - name: slack
        slack_configs:
          - api_url_file: /etc/amp/secrets/slack-webhook-url
configReloader:
  enabled: true
YAML

echo "empty values render exactly what no values render"
for profile in standard lite; do
  render_deployment "${profile}-default" "${PIN[@]}" --set profile="${profile}"
  render_deployment "${profile}-empty" "${PIN[@]}" --set profile="${profile}" \
    --set-json 'extraVolumes=[]' --set-json 'extraVolumeMounts=[]'
  render_deployment "${profile}-null" "${PIN[@]}" --set profile="${profile}" \
    --set-json 'extraVolumes=null' --set-json 'extraVolumeMounts=null'
  assert_same "${WORK_DIR}/${profile}-default.yaml" "${WORK_DIR}/${profile}-empty.yaml" "${profile}: [] == default"
  assert_same "${WORK_DIR}/${profile}-default.yaml" "${WORK_DIR}/${profile}-null.yaml" "${profile}: null == default"
done
# The default standard profile has no volumes at all; the block must stay out.
assert_absent "${WORK_DIR}/standard-default.yaml" "volumes:" "standard default: no volumes block"

echo "a Secret mounted for receiver credentials"
render_deployment secret "${PIN[@]}" -f "${WORK_DIR}/values-secret.yaml"
split_containers secret
assert_contains "${WORK_DIR}/secret-volumes.yaml" "- name: receiver-secrets" "volume rendered"
assert_contains "${WORK_DIR}/secret-volumes.yaml" "secretName: amp-receiver-secrets" "volume points at the Secret"
# 0440 octal is 288 once helm reads the values as YAML 1.1.
assert_contains "${WORK_DIR}/secret-volumes.yaml" "defaultMode: 288" "defaultMode kept"
assert_contains "${WORK_DIR}/secret-volumes.yaml" "- name: config-file" "chart volumes kept next to it"
assert_contains "${WORK_DIR}/secret-amp.yaml" "mountPath: /etc/amp/secrets" "mounted in the amp container"
assert_contains "${WORK_DIR}/secret-amp.yaml" "mountPath: /etc/amp" "config-file mount kept"
assert_contains "${WORK_DIR}/secret-reloader.yaml" "name: config-reloader" "sidecar rendered"
assert_absent "${WORK_DIR}/secret-reloader.yaml" "receiver-secrets" "not mounted in the config-reloader sidecar"
helm template amp "${CHART_DIR}" "${PIN[@]}" -f "${WORK_DIR}/values-secret.yaml" >"${WORK_DIR}/secret-all.yaml"
assert_contains "${WORK_DIR}/secret-all.yaml" "api_url_file: /etc/amp/secrets/slack-webhook-url" "configFile.content references the mounted path"

echo "mounts alone (volume from elsewhere, e.g. a CSI driver) render"
render_deployment mount-only "${PIN[@]}" \
  --set-json 'extraVolumes=[{"name":"csi","csi":{"driver":"secrets-store.csi.k8s.io"}}]' \
  --set-json 'extraVolumeMounts=[{"name":"csi","mountPath":"/mnt/secrets"}]'
split_containers mount-only
assert_contains "${WORK_DIR}/mount-only-volumes.yaml" "driver: secrets-store.csi.k8s.io" "standard profile gets a volumes block for extraVolumes"
assert_contains "${WORK_DIR}/mount-only-amp.yaml" "mountPath: /mnt/secrets" "standard profile gets a volumeMounts block for extraVolumeMounts"

echo "names and paths the chart already uses fail the render"
CONFIG=(--set configFile.enabled=true --set-string 'configFile.content=route: {receiver: default}')
# webConfig protects /metrics; the default ServiceMonitor has no credentials.
WEB=(--set webConfig.enabled=true --set webConfig.existingSecret=amp-web-config
  --set monitoring.serviceMonitor.enabled=false)
assert_render_fails "volume name config-file" 'volume name "config-file" is already used by the chart' \
  "${CONFIG[@]}" --set-json 'extraVolumes=[{"name":"config-file","emptyDir":{}}]'
assert_render_fails "volume name data in lite" 'volume name "data" is already used by the chart' \
  --set profile=lite --set-json 'extraVolumes=[{"name":"data","emptyDir":{}}]'
assert_render_fails "mountPath /etc/amp" 'mountPath "/etc/amp" is already used' \
  "${CONFIG[@]}" --set-json 'extraVolumeMounts=[{"name":"x","mountPath":"/etc/amp"}]'
assert_render_fails "mountPath /etc/amp/ (trailing slash)" 'mountPath "/etc/amp/" is already used' \
  "${CONFIG[@]}" --set-json 'extraVolumeMounts=[{"name":"x","mountPath":"/etc/amp/"}]'
assert_render_fails "mountPath of config.yaml itself" 'mountPath "/etc/amp/config.yaml" is already used' \
  "${CONFIG[@]}" --set-json 'extraVolumeMounts=[{"name":"x","mountPath":"/etc/amp/config.yaml"}]'
assert_render_fails "mountPath /data in lite" 'mountPath "/data" is already used' \
  --set profile=lite --set-json 'extraVolumeMounts=[{"name":"x","mountPath":"/data"}]'
assert_render_fails "mountPath of the web config dir" 'mountPath "/etc/amp/web" is already used' \
  "${WEB[@]}" --set-json 'extraVolumeMounts=[{"name":"x","mountPath":"/etc/amp/web"}]'
assert_render_fails "mountPath of the web config file" 'mountPath "/etc/amp/web/web-config.yml" is already used' \
  "${WEB[@]}" --set-json 'extraVolumeMounts=[{"name":"x","mountPath":"/etc/amp/web/web-config.yml"}]'

echo "a directory below the chart's mounts is fine"
render_deployment nested "${PIN[@]}" "${CONFIG[@]}" \
  --set-json 'extraVolumes=[{"name":"s","secret":{"secretName":"x"}}]' \
  --set-json 'extraVolumeMounts=[{"name":"s","mountPath":"/etc/amp/secrets"}]'
assert_contains "${WORK_DIR}/nested.yaml" "mountPath: /etc/amp/secrets" "/etc/amp/secrets accepted"
# Without configFile the chart does not own /etc/amp, so the guard stays out.
render_deployment no-config "${PIN[@]}" \
  --set-json 'extraVolumes=[{"name":"s","secret":{"secretName":"x"}}]' \
  --set-json 'extraVolumeMounts=[{"name":"s","mountPath":"/etc/amp"}]'
assert_contains "${WORK_DIR}/no-config.yaml" "mountPath: /etc/amp" "/etc/amp accepted when configFile is off"

if [ "${failures}" -ne 0 ]; then
  echo "${failures} assertion(s) failed" >&2
  exit 1
fi
echo "all extraVolumes assertions passed"
