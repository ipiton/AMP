#!/usr/bin/env bash
# Renders the chart and checks the defaults that used to keep AMP from
# starting (HELM-DEFAULTS-VALIDATE):
#
#   - every secretKeyRef points at a key the rendered Secret actually has
#     (llm.enabled: true without llm.apiKey referenced a missing llm-api-key),
#     except the operator's externalDatabase.existingSecret, which the chart
#     never creates;
#   - LLM is off by default, and turning it on without a key fails the render;
#   - DATABASE_ALLOW_INSECURE_SSL_IN_PRODUCTION is set only for the bundled
#     PostgreSQL without TLS, never for an external database;
#   - the chart does not set the controller-owned deployment.kubernetes.io/revision
#     annotation: under Helm 4 server-side apply it made every upgrade after a
#     rollout conflict with the Deployment controller.
#
#   helm/amp/tests/render-defaults-validate.sh
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

# render <name> <helm args...>: renders into ${WORK_DIR}/<name>.yaml; a failed
# render is an assertion failure, not an abort.
render() {
  local name="$1"
  shift
  if ! helm template amp "${CHART_DIR}" "$@" >"${WORK_DIR}/${name}.yaml" 2>"${WORK_DIR}/${name}.err"; then
    fail "${name}: render failed: $(cat "${WORK_DIR}/${name}.err")"
    : >"${WORK_DIR}/${name}.yaml"
  fi
}

# secret_keys <file>: "<secret>/<key>" for every data/stringData key of every
# rendered Secret (no yq in the gate's toolchain).
secret_keys() {
  awk '
    function flush() { buf = ""; secret = 0; name = ""; indata = 0 }
    /^---/ { flush(); next }
    /^kind: Secret$/ { secret = 1 }
    secret && name == "" && /^  name: / { name = $2 }
    /^(data|stringData):/ { indata = 1; next }
    /^[^ ]/ { indata = 0 }
    secret && indata && /^  [^ #][^:]*:/ { k = $1; sub(/:$/, "", k); print name "/" k }' "$1"
}

# secret_refs <file>: "<secret>/<key>" for every secretKeyRef.
secret_refs() {
  awk '
    function indent(s) { match(s, /^ */); return RLENGTH }
    /secretKeyRef:/ { on = 1; ind = indent($0); n = ""; k = ""; next }
    on && NF && indent($0) <= ind { on = 0 }
    on && $1 == "name:" { n = $2 }
    on && $1 == "key:" { k = $2 }
    on && n != "" && k != "" { gsub(/"/, "", n); gsub(/"/, "", k); print n "/" k; on = 0 }' "$1"
}

# check_secret_refs <name> [<secret>/<key> ...]: the extra refs point at
# Secrets the operator creates (externalDatabase.existingSecret).
check_secret_refs() {
  local name="$1" file="${WORK_DIR}/$1.yaml" ref missing=0 total=0
  shift
  { secret_keys "${file}"; [ "$#" -eq 0 ] || printf '%s\n' "$@"; } | sort -u >"${WORK_DIR}/${name}.keys"
  while read -r ref; do
    total=$((total + 1))
    if ! grep -qxF -e "${ref}" "${WORK_DIR}/${name}.keys"; then
      fail "${name}: secretKeyRef ${ref} has no such key in the rendered Secret"
      missing=1
    fi
  done < <(secret_refs "${file}" | sort -u)
  if [ "${total}" -eq 0 ]; then
    fail "${name}: no secretKeyRef found (parser broken?)"
  elif [ "${missing}" -eq 0 ]; then
    pass "${name}: all ${total} secretKeyRef targets exist"
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

# values-production.yaml ships no passwords, auth or NetworkPolicy sources
# (guards fail the render until they are supplied) -- placeholders, as
# scripts/release-gate.sh does.
PROD_ARGS=(-f "${CHART_DIR}/values-production.yaml"
  -f "${CHART_DIR}/tests/values-production-placeholders.yaml"
  --set postgresql.password=render-test-placeholder
  --set cache.auth.password=render-test-placeholder)

FLAG="name: DATABASE_ALLOW_INSECURE_SSL_IN_PRODUCTION"
# An external database: postgresql.enabled=false needs one (render-external-database.sh).
EXT=(--set postgresql.enabled=false
  --set externalDatabase.host=db.example.com
  --set externalDatabase.database=amp
  --set externalDatabase.username=amp
  --set externalDatabase.existingSecret=amp-db)

echo "T1 shipped values files:"
render default
render dev -f "${CHART_DIR}/values-dev.yaml"
render production "${PROD_ARGS[@]}"
check_secret_refs default
check_secret_refs dev
# values-production.yaml uses an external database; the placeholder Secret is the operator's.
check_secret_refs production release-gate-placeholder/password
for v in default dev production; do
  assert_count "${WORK_DIR}/${v}.yaml" 'LLM_ENABLED: "false"' 1 "${v}: LLM is off"
done
for v in default dev; do
  assert_count "${WORK_DIR}/${v}.yaml" "${FLAG}" 1 "${v}: insecure-SSL flag set once for the bundled PostgreSQL"
  assert_count "${WORK_DIR}/${v}.yaml" 'value: "disable"' 1 "${v}: DATABASE_SSL_MODE is disable"
done
assert_count "${WORK_DIR}/production.yaml" "${FLAG}" 0 "production: no insecure-SSL flag for the external database"
assert_count "${WORK_DIR}/production.yaml" 'value: "require"' 1 "production: DATABASE_SSL_MODE is require"
for v in default dev production; do
  assert_count "${WORK_DIR}/${v}.yaml" "deployment.kubernetes.io/revision" 0 "${v}: no hard-coded Deployment revision annotation"
done

echo "T2-T4 flag stays off where it does not belong:"
render ssl-on --set postgresql.config.ssl=on
assert_count "${WORK_DIR}/ssl-on.yaml" "${FLAG}" 0 "ssl-on: no flag when PostgreSQL TLS is on"
render external-db "${EXT[@]}"
check_secret_refs external-db amp-db/password
assert_count "${WORK_DIR}/external-db.yaml" "${FLAG}" 0 "external-db: no flag without the bundled PostgreSQL"
render lite --set profile=lite
assert_count "${WORK_DIR}/lite.yaml" "${FLAG}" 0 "lite: no flag in the lite profile"

echo "T5 LLM on without a key fails the render:"
if helm template amp "${CHART_DIR}" --set llm.enabled=true >/dev/null 2>"${WORK_DIR}/llm-nokey.err"; then
  fail "llm-nokey: render succeeded, want failure"
elif grep -q 'llm.enabled=true requires llm.apiKey' "${WORK_DIR}/llm-nokey.err"; then
  pass "llm-nokey: render fails naming llm.apiKey"
else
  fail "llm-nokey: render failed for another reason: $(cat "${WORK_DIR}/llm-nokey.err")"
fi

echo "T6 LLM on with a key source renders:"
render llm-key --set llm.enabled=true --set llm.apiKey=render-test-key
check_secret_refs llm-key
assert_count "${WORK_DIR}/llm-key.yaml" "key: llm-api-key" 1 "llm-key: Deployment reads llm-api-key"
render llm-eso --set llm.enabled=true --set externalSecrets.enabled=true
assert_count "${WORK_DIR}/llm-eso.yaml" "llm-api-key:" 1 "llm-eso: ExternalSecret supplies llm-api-key"

if [ "${failures}" -ne 0 ]; then
  printf '\n%d assertion(s) failed\n' "${failures}" >&2
  exit 1
fi

printf '\nall assertions passed\n'
