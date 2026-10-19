#!/usr/bin/env bash
# Renders the chart and checks the external PostgreSQL values
# (HELM-EXTERNAL-DATABASE, ADR-016):
#
#   - externalDatabase.* becomes DATABASE_* env, the password a secretKeyRef
#     to the operator's Secret; the insecure-SSL flag is never rendered;
#   - missing host companions, an sslMode AMP refuses, both databases at once
#     and no database at all fail the render;
#   - the configFile.content fallback is recognized by parsing it as YAML;
#   - the bundled PostgreSQL, lite and --reuse-values without the key render
#     as before;
#   - values-production.yaml uses the external database and does not render
#     without it.
#
#   helm/amp/tests/render-external-database.sh
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

# env_value <file> <name>: the value: line right after "- name: <name>".
env_value() {
  awk -v n="- name: $2" '$0 ~ n"$" { getline; sub(/^ *value: /, ""); print; exit }' "$1"
}

# assert_env <file> <name> <expected value, quoted as rendered> <description>
assert_env() {
  local got
  got="$(env_value "$1" "$2")"
  if [ "${got}" = "$3" ]; then pass "$4"; else fail "$4 (want $2=$3, got '${got}')"; fi
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

# content <name> <yaml>: writes a configFile.content candidate.
content() { printf '%b' "$2" >"${WORK_DIR}/$1.content"; }

if ! command -v helm >/dev/null 2>&1; then
  echo "SKIP: helm not installed"
  exit 0
fi

EXT=(--set postgresql.enabled=false
  --set externalDatabase.host=db.example.com
  --set externalDatabase.database=amp
  --set externalDatabase.username=amp
  --set externalDatabase.existingSecret=amp-db)
FLAG="name: DATABASE_ALLOW_INSECURE_SSL_IN_PRODUCTION"
NO_DB="postgresql.enabled=false but no database is configured"

echo "T-ext-1 external database env:"
render ext "${EXT[@]}"
f="${WORK_DIR}/ext.yaml"
assert_env "${f}" DATABASE_HOST '"db.example.com"' "ext: DATABASE_HOST"
assert_env "${f}" DATABASE_PORT '"5432"' "ext: DATABASE_PORT defaults to 5432"
assert_env "${f}" DATABASE_DATABASE '"amp"' "ext: DATABASE_DATABASE"
assert_env "${f}" DATABASE_USERNAME '"amp"' "ext: DATABASE_USERNAME"
assert_env "${f}" DATABASE_SSL_MODE '"require"' "ext: DATABASE_SSL_MODE defaults to require"
assert_count "${f}" 'name: "amp-db"' 1 "ext: password from the operator's Secret"
assert_count "${f}" 'key: "password"' 1 "ext: default Secret key is password"
assert_count "${f}" "name: DATABASE_PASSWORD" 1 "ext: one DATABASE_PASSWORD"
assert_count "${f}" "STORAGE_FILESYSTEM_PATH" 0 "ext: no filesystem storage path"
assert_docs "${f}" StatefulSet amp-postgres 0 "ext: no bundled PostgreSQL"
assert_docs "${f}" Secret amp-postgresql-secret 0 "ext: no bundled PostgreSQL Secret"

echo "T-ext-2 overrides:"
render ext-custom "${EXT[@]}" --set externalDatabase.port=6432 \
  --set externalDatabase.sslMode=verify-full --set externalDatabase.existingSecretPasswordKey=pw
f="${WORK_DIR}/ext-custom.yaml"
assert_env "${f}" DATABASE_PORT '"6432"' "ext-custom: port"
assert_env "${f}" DATABASE_SSL_MODE '"verify-full"' "ext-custom: sslMode"
assert_count "${f}" 'key: "pw"' 1 "ext-custom: Secret key"
render ext-port-string "${EXT[@]}" --set-string externalDatabase.port=5433
assert_env "${WORK_DIR}/ext-port-string.yaml" DATABASE_PORT '"5433"' "ext-port-string: port given as a string"

echo "T-ext-3 sslMode: the four values AMP accepts render, others fail:"
for mode in disable require verify-ca verify-full; do
  render "ssl-${mode}" "${EXT[@]}" --set "externalDatabase.sslMode=${mode}"
  assert_env "${WORK_DIR}/ssl-${mode}.yaml" DATABASE_SSL_MODE "\"${mode}\"" "ssl-${mode}: DATABASE_SSL_MODE"
done
for mode in allow prefer bogus; do
  render_fails "ssl-${mode}" "externalDatabase.sslMode \"${mode}\" is invalid" "${EXT[@]}" --set "externalDatabase.sslMode=${mode}"
done

echo "T-ext-4 no insecure-SSL flag for an external database, whatever sslMode:"
for mode in disable require verify-ca verify-full; do
  assert_count "${WORK_DIR}/ssl-${mode}.yaml" "${FLAG}" 0 "ssl-${mode}: no insecure-SSL flag"
done
render ext-pg-ssl-off "${EXT[@]}" --set postgresql.config.ssl=off
assert_count "${WORK_DIR}/ext-pg-ssl-off.yaml" "${FLAG}" 0 "ext-pg-ssl-off: bundled ssl setting does not leak"

echo "T-ext-5 host without its companions fails:"
render_fails no-secret "externalDatabase.existingSecret" "${EXT[@]}" --set externalDatabase.existingSecret=
render_fails no-database "externalDatabase.database is required" "${EXT[@]}" --set externalDatabase.database=
render_fails no-username "externalDatabase.username is required" "${EXT[@]}" --set externalDatabase.username=

echo "T-ext-6 both databases at once fail:"
render_fails both "externalDatabase.host is set while postgresql.enabled=true" "${EXT[@]}" --set postgresql.enabled=true

echo "T-ext-7 no database anywhere fails; the configFile fallback is parsed as YAML:"
render_fails no-db "${NO_DB}" --set postgresql.enabled=false
content block 'database:\n  host: db\n'
content flow '{database: {host: db}}\n'
content quoted '"database":\n  host: db\n'
content nested 'server:\n  database:\n    host: db\n'
content null 'database: null\n'
content empty ''
content no-db 'server:\n  port: 8080\n'
content invalid 'database: [\n'
for c in block flow quoted; do
  render "cfg-${c}" --set postgresql.enabled=false --set configFile.enabled=true --set-file "configFile.content=${WORK_DIR}/${c}.content"
done
for c in nested null empty no-db; do
  render_fails "cfg-${c}" "${NO_DB}" --set postgresql.enabled=false --set configFile.enabled=true --set-file "configFile.content=${WORK_DIR}/${c}.content"
done
render_fails cfg-invalid "configFile.content is not valid YAML" \
  --set postgresql.enabled=false --set configFile.enabled=true --set-file "configFile.content=${WORK_DIR}/invalid.content"
render_fails cfg-disabled "${NO_DB}" \
  --set postgresql.enabled=false --set configFile.enabled=false --set-file "configFile.content=${WORK_DIR}/block.content"
render cfg-and-ext "${EXT[@]}" --set configFile.enabled=true --set-file "configFile.content=${WORK_DIR}/block.content"
assert_env "${WORK_DIR}/cfg-and-ext.yaml" DATABASE_HOST '"db.example.com"' "cfg-and-ext: externalDatabase wins over configFile (env)"

echo "T-ext-8 bundled PostgreSQL, lite and --reuse-values without the key:"
render bundled
assert_env "${WORK_DIR}/bundled.yaml" DATABASE_HOST "amp-postgres" "bundled: DATABASE_HOST is the bundled Service"
assert_count "${WORK_DIR}/bundled.yaml" "${FLAG}" 1 "bundled: insecure-SSL flag still set for the bundled PostgreSQL"
render reuse-no-key --set externalDatabase=null
assert_env "${WORK_DIR}/reuse-no-key.yaml" DATABASE_HOST "amp-postgres" "reuse-no-key: bundled branch"
render lite --set profile=lite "${EXT[@]}" --set postgresql.enabled=true
assert_count "${WORK_DIR}/lite.yaml" "DATABASE_" 0 "lite: externalDatabase ignored, no guards"

echo "T-ext-9 values-production.yaml renders with an external database:"
PROD=(-f "${CHART_DIR}/values-production.yaml" --set cache.auth.password=render-test-placeholder)
render production "${PROD[@]}" -f "${CHART_DIR}/tests/values-production-placeholders.yaml"
f="${WORK_DIR}/production.yaml"
assert_docs "${f}" StatefulSet amp-postgres 0 "production: no bundled PostgreSQL"
assert_count "${f}" "image: postgres:" 0 "production: no postgres image"
assert_env "${f}" DATABASE_HOST '"release-gate-placeholder"' "production: DATABASE_HOST from externalDatabase"
assert_env "${f}" DATABASE_SSL_MODE '"require"' "production: sslMode require"
assert_count "${f}" "${FLAG}" 0 "production: no insecure-SSL flag"

echo "T-ext-10 values-production.yaml does not render without it:"
render_fails production-no-db "${NO_DB}" "${PROD[@]}" \
  --set webConfig.existingSecret=x --set monitoring.serviceMonitor.basicAuth.secretName=x \
  --set-json 'networkPolicy.alertSenders=[{"podSelector":{}}]'

if [ "${failures}" -ne 0 ]; then
  printf '\n%d assertion(s) failed\n' "${failures}" >&2
  exit 1
fi

printf '\nall assertions passed\n'
