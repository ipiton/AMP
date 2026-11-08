#!/usr/bin/env bash
# Renders the chart and checks the external PostgreSQL values
# (HELM-EXTERNAL-DATABASE, ADR-016):
#
#   - externalDatabase.* becomes the database section of amp.yaml, the
#     password a DATABASE_PASSWORD secretKeyRef to the operator's Secret; the
#     insecure-SSL flag is never rendered;
#   - missing host companions, an sslMode AMP refuses, both databases at once
#     and no database at all fail the render;
#   - alertmanager.ampConfig.database is the fallback without externalDatabase;
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

# cfg_value <file> <dotted.key>: a scalar of the rendered amp.yaml (the
# <fullname>-amp-config ConfigMap), quotes stripped; no yq in the gate's
# toolchain, so a flat walk of toYaml's two-space nesting.
cfg_value() {
  awk -v want="$2" '
    /^  amp\.yaml: \|$/ { on = 1; next }
    on && !/^    / { on = 0 }
    on {
      match($0, /^ */); lvl = (RLENGTH - 4) / 2
      line = substr($0, RLENGTH + 1)
      k = line; sub(/:.*/, "", k)
      v = line; if (sub(/^[^:]*: /, "", v) == 0) v = ""
      path[lvl] = k; p = path[0]
      for (i = 1; i <= lvl; i++) p = p "." path[i]
      if (p == want && v != "") { gsub(/^"|"$/, "", v); print v; exit }
    }' "$1"
}

# assert_cfg <file> <dotted.key> <expected> <description>
assert_cfg() {
  local got want="${3#\"}"
  want="${want%\"}"
  got="$(cfg_value "$1" "$2")"
  if [ "${got}" = "${want}" ]; then pass "$4"; else fail "$4 (want $2=${want}, got '${got}')"; fi
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

if ! command -v helm >/dev/null 2>&1; then
  echo "SKIP: helm not installed"
  exit 0
fi

EXT=(--set postgresql.enabled=false
  --set externalDatabase.host=db.example.com
  --set externalDatabase.database=amp
  --set externalDatabase.username=amp
  --set externalDatabase.existingSecret=amp-db)
FLAG="allow_insecure_ssl_in_production: true"
NO_DB="postgresql.enabled=false but no database is configured"

echo "T-ext-1 external database env:"
render ext "${EXT[@]}"
f="${WORK_DIR}/ext.yaml"
assert_cfg "${f}" database.host '"db.example.com"' "ext: DATABASE_HOST"
assert_cfg "${f}" database.port '"5432"' "ext: DATABASE_PORT defaults to 5432"
assert_cfg "${f}" database.database '"amp"' "ext: DATABASE_DATABASE"
assert_cfg "${f}" database.username '"amp"' "ext: DATABASE_USERNAME"
assert_cfg "${f}" database.ssl_mode '"require"' "ext: DATABASE_SSL_MODE defaults to require"
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
assert_cfg "${f}" database.port '"6432"' "ext-custom: port"
assert_cfg "${f}" database.ssl_mode '"verify-full"' "ext-custom: sslMode"
assert_count "${f}" 'key: "pw"' 1 "ext-custom: Secret key"
render ext-port-string "${EXT[@]}" --set-string externalDatabase.port=5433
assert_cfg "${WORK_DIR}/ext-port-string.yaml" database.port '"5433"' "ext-port-string: port given as a string"

echo "T-ext-3 sslMode: the four values AMP accepts render, others fail:"
for mode in disable require verify-ca verify-full; do
  render "ssl-${mode}" "${EXT[@]}" --set "externalDatabase.sslMode=${mode}"
  assert_cfg "${WORK_DIR}/ssl-${mode}.yaml" database.ssl_mode "\"${mode}\"" "ssl-${mode}: DATABASE_SSL_MODE"
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

echo "T-ext-7 no database anywhere fails; alertmanager.ampConfig.database is the fallback:"
render_fails no-db "${NO_DB}" --set postgresql.enabled=false
render amp-cfg --set postgresql.enabled=false --set alertmanager.ampConfig.database.host=db
assert_cfg "${WORK_DIR}/amp-cfg.yaml" database.host db "amp-cfg: database from alertmanager.ampConfig"
render_fails amp-cfg-nested "${NO_DB}" --set postgresql.enabled=false --set alertmanager.ampConfig.server.database.host=db
render_fails amp-cfg-null "${NO_DB}" --set postgresql.enabled=false --set-json 'alertmanager.ampConfig={"database":null}'
render_fails amp-cfg-empty "${NO_DB}" --set postgresql.enabled=false --set-json 'alertmanager.ampConfig={"database":{}}'
render cfg-and-ext "${EXT[@]}" --set alertmanager.ampConfig.database.host=db
assert_cfg "${WORK_DIR}/cfg-and-ext.yaml" database.host db "cfg-and-ext: alertmanager.ampConfig overrides externalDatabase"
assert_cfg "${WORK_DIR}/cfg-and-ext.yaml" database.username amp "cfg-and-ext: the rest of externalDatabase stays"

echo "T-ext-8 bundled PostgreSQL, lite and --reuse-values without the key:"
render bundled
assert_cfg "${WORK_DIR}/bundled.yaml" database.host "amp-postgres" "bundled: DATABASE_HOST is the bundled Service"
assert_count "${WORK_DIR}/bundled.yaml" "${FLAG}" 1 "bundled: insecure-SSL flag still set for the bundled PostgreSQL"
render reuse-no-key --set externalDatabase=null
assert_cfg "${WORK_DIR}/reuse-no-key.yaml" database.host "amp-postgres" "reuse-no-key: bundled branch"
render lite --set profile=lite "${EXT[@]}" --set postgresql.enabled=true
assert_count "${WORK_DIR}/lite.yaml" "DATABASE_" 0 "lite: externalDatabase ignored, no guards"

echo "T-ext-9 values-production.yaml renders with an external database:"
PROD=(-f "${CHART_DIR}/values-production.yaml")
render production "${PROD[@]}" -f "${CHART_DIR}/tests/values-production-placeholders.yaml"
f="${WORK_DIR}/production.yaml"
assert_docs "${f}" StatefulSet amp-postgres 0 "production: no bundled PostgreSQL"
assert_count "${f}" "image: postgres:" 0 "production: no postgres image"
assert_cfg "${f}" database.host '"release-gate-placeholder"' "production: DATABASE_HOST from externalDatabase"
assert_cfg "${f}" database.ssl_mode '"require"' "production: sslMode require"
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
