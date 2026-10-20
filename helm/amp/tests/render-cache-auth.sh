#!/usr/bin/env bash
# Renders the chart and checks which password AMP sends to its cache
# (AMP-CACHE-NOAUTH):
#
#   - the chart's Valkey (cache.host names it, valkey.replicas > 0): AMP, Valkey
#     and the exporter read one Secret, the chart's or valkey.password.existingSecret;
#   - an external cache.host: cache.auth.existingSecret or cache.auth.password,
#     also while the chart's Valkey still runs; a same-named Valkey in another
#     namespace is external;
#   - lite and cache.enabled=false send no password;
#   - replicas 0 with cache.host on the chart's Valkey, and cache.auth.existingSecret
#     with cache.auth.enabled, fail the render;
#   - NOTES name the Secret and warn about ignored cache.auth.*;
#   - no password in a URL or in plain text outside a Secret.
#
# The NOTES upgrade hint (.Release.IsUpgrade) needs a release in a cluster and is
# checked on k3d, not here.
#
#   helm/amp/tests/render-cache-auth.sh
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

# assert_count <file> <pattern> <expected> <description>
assert_count() {
  local got
  # grep -c exits 1 on zero matches; the count is still printed.
  got="$(grep -c -F -e "$2" "$1" || true)"
  if [ "${got}" -eq "$3" ]; then pass "$4"; else fail "$4 (want $3 x '$2', got ${got})"; fi
}

# assert_docs <file> <kind> <name> <expected> <description>: number of rendered
# documents of <kind> whose metadata.name is <name>.
assert_docs() {
  local got
  got="$(awk -v kind="$2" -v name="$3" '
    function flush() { if (k && n) c++; k = 0; n = 0 }
    /^---/ { flush(); next }
    $0 == "kind: " kind { k = 1 }
    /^  name: / && $2 == name { n = 1 }
    END { flush(); print c + 0 }' "$1")"
  if [ "${got}" -eq "$4" ]; then pass "$5"; else fail "$5 (want $4 x ${2}/${3}, got ${got})"; fi
}

# secret_refs <file> <kind> <env>: "<secret>/<key>" of the secretKeyRef of env
# <env> in documents of <kind>, one line per container, joined by spaces.
secret_refs() {
  awk -v kind="$2" -v env="- name: $3" '
    /^---/ { k = 0; want = 0; next }
    $0 == "kind: " kind { k = 1 }
    k && $0 ~ env"$" { want = 1; next }
    want && $1 == "name:" { n = $2 }
    want && $1 == "key:" { gsub(/"/, "", n); gsub(/"/, "", $2); print n "/" $2; want = 0 }' "$1" |
    paste -s -d ' ' -
}

# assert_refs <file> <kind> <env> <expected> <description>
assert_refs() {
  local got
  got="$(secret_refs "$1" "$2" "$3")"
  if [ "${got}" = "$4" ]; then pass "$5"; else fail "$5 (want $2 $3 -> '$4', got '${got}')"; fi
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

# notes <name> <helm args...>: NOTES.txt as printed by a client-side dry-run.
notes() {
  local name="$1"
  shift
  if helm install amp "${CHART_DIR}" --dry-run=client "$@" 2>"${WORK_DIR}/${name}.err" |
    sed -n '/^NOTES:/,$p' >"${WORK_DIR}/${name}.notes" && [ -s "${WORK_DIR}/${name}.notes" ]; then
    pass "${name}: NOTES rendered"
  else
    fail "${name}: no NOTES: $(cat "${WORK_DIR}/${name}.err")"
    : >"${WORK_DIR}/${name}.notes"
  fi
}

if ! command -v helm >/dev/null 2>&1; then
  echo "SKIP: helm not installed"
  exit 0
fi

OWN="amp-redis-secret/password"
EXT_HOST=(--set cache.host=cache.example.com)
EXT_AUTH=(--set cache.auth.enabled=true --set cache.auth.password=render-test-cache-pw)
AUTH_REF="amp-secrets/redis-password"
NO_CACHE="valkey.replicas is 0 while cache.host still points at the chart's Valkey"
BOTH="cache.auth.existingSecret is set while cache.auth.enabled=true"
IGNORED="cache.auth.* is ignored for the chart's Valkey"
UNUSED="still runs but AMP does not use it"

echo "T1 defaults: AMP, Valkey and the exporter read the chart's Secret:"
render defaults
f="${WORK_DIR}/defaults.yaml"
assert_refs "${f}" Deployment REDIS_PASSWORD "${OWN}" "defaults: AMP REDIS_PASSWORD from the Valkey Secret"
assert_refs "${f}" StatefulSet REDIS_PASSWORD "${OWN} ${OWN}" "defaults: Valkey and exporter from the same Secret"
assert_docs "${f}" Secret amp-redis-secret 1 "defaults: chart creates the Valkey Secret"
assert_count "${f}" "secretName: amp-redis-secret" 1 "defaults: Valkey config-init mounts the same Secret"
assert_count "${f}" "redis-password" 0 "defaults: no cache.auth password in amp-secrets"

echo "T2 valkey.password.existingSecret with a custom key:"
render valkey-existing --set valkey.password.existingSecret=vk --set valkey.password.secretKey=pw
f="${WORK_DIR}/valkey-existing.yaml"
assert_refs "${f}" Deployment REDIS_PASSWORD "vk/pw" "valkey-existing: AMP from the operator's Secret"
assert_refs "${f}" StatefulSet REDIS_PASSWORD "vk/pw vk/pw" "valkey-existing: Valkey and exporter from it"
assert_docs "${f}" Secret amp-redis-secret 0 "valkey-existing: no chart Valkey Secret"
assert_count "${f}" "secretName: vk" 1 "valkey-existing: config-init mounts it"
assert_count "${f}" "- key: pw" 1 "valkey-existing: mounted item is the custom key"
assert_count "${f}" "amp-redis-secret" 0 "valkey-existing: chart Secret not referenced"
render valkey-existing-default-key --set valkey.password.existingSecret=vk
assert_refs "${WORK_DIR}/valkey-existing-default-key.yaml" Deployment REDIS_PASSWORD "vk/password" \
  "valkey-existing-default-key: key defaults to password"

echo "T3 external cache with cache.auth.existingSecret:"
render ext-existing "${EXT_HOST[@]}" --set valkey.replicas=0 --set cache.auth.existingSecret=cache-sec
f="${WORK_DIR}/ext-existing.yaml"
assert_refs "${f}" Deployment REDIS_PASSWORD "cache-sec/password" "ext-existing: AMP from the operator's Secret"
assert_count "${f}" "redis-password" 0 "ext-existing: no redis-password in amp-secrets"
render ext-existing-key "${EXT_HOST[@]}" --set valkey.replicas=0 \
  --set cache.auth.existingSecret=cache-sec --set cache.auth.existingSecretPasswordKey=pw
assert_refs "${WORK_DIR}/ext-existing-key.yaml" Deployment REDIS_PASSWORD "cache-sec/pw" "ext-existing-key: custom key"
render ext-existing-eso "${EXT_HOST[@]}" --set valkey.replicas=0 --set cache.auth.existingSecret=cache-sec \
  --set externalSecrets.enabled=true
assert_count "${WORK_DIR}/ext-existing-eso.yaml" "redis_password" 0 "ext-existing-eso: ExternalSecret does not fetch it"

echo "T4 values-production.yaml (external cache, cache.auth.password) as before:"
render production -f "${CHART_DIR}/values-production.yaml" --set cache.auth.password=render-test-placeholder \
  -f "${CHART_DIR}/tests/values-production-placeholders.yaml"
f="${WORK_DIR}/production.yaml"
assert_refs "${f}" Deployment REDIS_PASSWORD "${AUTH_REF}" "production: AMP from amp-secrets/redis-password"
assert_count "${f}" "redis-password:" 1 "production: amp-secrets carries redis-password"

echo "T5 no cache, no password:"
render lite --set profile=lite
assert_count "${WORK_DIR}/lite.yaml" "REDIS_PASSWORD" 0 "lite: no REDIS_PASSWORD"
render cache-off --set cache.enabled=false
assert_count "${WORK_DIR}/cache-off.yaml" "name: REDIS_PASSWORD" 2 "cache-off: only Valkey and exporter, not AMP"
assert_refs "${WORK_DIR}/cache-off.yaml" Deployment REDIS_PASSWORD "" "cache-off: AMP gets no REDIS_PASSWORD"

echo "T6 contradictory values fail the render:"
render_fails no-cache "${NO_CACHE}" --set valkey.replicas=0
render_fails both "${BOTH}" "${EXT_HOST[@]}" --set valkey.replicas=0 \
  --set cache.auth.existingSecret=cache-sec "${EXT_AUTH[@]}"

echo "T7 chart's Valkey: cache.auth.* is ignored, with a warning:"
render own-auth-enabled --set cache.auth.enabled=true
f="${WORK_DIR}/own-auth-enabled.yaml"
assert_refs "${f}" Deployment REDIS_PASSWORD "${OWN}" "own-auth-enabled: renders without cache.auth.password, Valkey Secret"
assert_count "${f}" "redis-password" 0 "own-auth-enabled: no redis-password"
for args in "--set cache.auth.enabled=true" "--set cache.auth.password=x" "--set cache.auth.existingSecret=x"; do
  name="own-notes-${args##*.}"
  name="${name%%=*}"
  # shellcheck disable=SC2086 # one --set pair per entry
  notes "${name}" ${args}
  assert_count "${WORK_DIR}/${name}.notes" "${IGNORED}" 1 "${name}: NOTES warn that cache.auth.* is ignored"
done
notes own-notes-defaults
f="${WORK_DIR}/own-notes-defaults.notes"
assert_count "${f}" "password from Secret amp-redis-secret/password" 1 "own-notes-defaults: NOTES name the Secret"
assert_count "${f}" "${IGNORED}" 0 "own-notes-defaults: no warning"
assert_count "${f}" "rollout restart" 0 "own-notes-defaults: no upgrade hint on install"

echo "T7a external cache.host while the chart's Valkey still runs:"
render ext-replicas "${EXT_HOST[@]}" "${EXT_AUTH[@]}"
f="${WORK_DIR}/ext-replicas.yaml"
assert_refs "${f}" Deployment REDIS_PASSWORD "${AUTH_REF}" "ext-replicas: AMP from cache.auth.password, not Valkey's"
assert_count "${f}" "redis-password:" 1 "ext-replicas: amp-secrets carries redis-password"
render ext-noauth "${EXT_HOST[@]}"
assert_refs "${WORK_DIR}/ext-noauth.yaml" Deployment REDIS_PASSWORD "" "ext-noauth: no password for an external cache without auth"
notes ext-replicas "${EXT_HOST[@]}" "${EXT_AUTH[@]}"
f="${WORK_DIR}/ext-replicas.notes"
assert_count "${f}" "${UNUSED}" 1 "ext-replicas: NOTES say the chart's Valkey is unused"
assert_count "${f}" "External cache L2 (cache.example.com)" 1 "ext-replicas: NOTES name the external host"

echo "T7b cache.host DNS names: own namespace is the chart's Valkey, another is external (-n prod):"
for host in amp-redis amp-redis.prod amp-redis.prod.svc amp-redis.prod.svc.cluster.local; do
  render "host-${host}" -n prod --set "cache.host=${host}" "${EXT_AUTH[@]}"
  assert_refs "${WORK_DIR}/host-${host}.yaml" Deployment REDIS_PASSWORD "${OWN}" "host-${host}: chart's Valkey"
  render_fails "host-${host}-replicas-0" "${NO_CACHE}" -n prod --set "cache.host=${host}" --set valkey.replicas=0
done
for host in amp-redis.otherns.svc.cluster.local amp-redisx amp-redis.prodx.svc; do
  render "host-${host}" -n prod --set "cache.host=${host}" "${EXT_AUTH[@]}"
  assert_refs "${WORK_DIR}/host-${host}.yaml" Deployment REDIS_PASSWORD "${AUTH_REF}" "host-${host}: external"
  render "host-${host}-replicas-0" -n prod --set "cache.host=${host}" --set valkey.replicas=0
done

echo "T8 no password in a URL or in plain text outside a Secret:"
render plain --set valkey.password.value=render-test-valkey-pw
render plain-ext "${EXT_HOST[@]}" --set valkey.replicas=0 "${EXT_AUTH[@]}"
for name in defaults plain plain-ext production; do
  assert_count "${WORK_DIR}/${name}.yaml" "redis://:" 0 "${name}: no redis://:<password> URL"
done
assert_count "${WORK_DIR}/plain.yaml" "render-test-valkey-pw" 0 "plain: valkey.password.value only base64-encoded"
assert_count "${WORK_DIR}/plain-ext.yaml" "render-test-cache-pw" 0 "plain-ext: cache.auth.password only base64-encoded"

if [ "${failures}" -ne 0 ]; then
  printf '\n%d assertion(s) failed\n' "${failures}" >&2
  exit 1
fi

printf '\nall assertions passed\n'
