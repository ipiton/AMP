#!/usr/bin/env bash
# Renders the chart and checks the bundled PostgreSQL contract of ADR-016
# (PROD-POSTGRES-HA-DECISION):
#
#   - no backup CronJob, backup PVC, WAL archiving or Postgres PDB is rendered;
#   - the image is pinned to postgres:16.15;
#   - postgresql.replicas other than 1 and postgresql.backup enabled fail the
#     render, including the old chart default carried by --reuse-values;
#     null/false/empty leftovers of the removed key do not;
#   - NOTES no longer advertise HA;
#   - values-production.yaml runs no bundled PostgreSQL (external database,
#     HELM-EXTERNAL-DATABASE);
#   - the server runs with the chart postgresql.conf and pg_hba.conf, passed as
#     server arguments, and the exporter connects (PG-CONFIG-FIX).
#
#   helm/amp/tests/render-postgresql.sh
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

# doc_line <file> <kind> <name> <regex>: lines matching <regex> inside the
# rendered document of <kind> named <name>.
doc_line() {
  awk -v kind="$2" -v name="$3" -v re="$4" '
    function flush() { if (k && n) printf "%s", buf; k = 0; n = 0; buf = "" }
    /^---/ { flush(); next }
    $0 == "kind: " kind { k = 1 }
    /^  name: / && $2 == name { n = 1 }
    $0 ~ re { buf = buf $0 "\n" }
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

# notes <name> <helm args...>: NOTES.txt as printed by a client-side dry-run.
notes() {
  local name="$1"
  shift
  if helm install amp "${CHART_DIR}" --dry-run=client "$@" 2>"${WORK_DIR}/${name}.err" |
    sed -n '/^NOTES:/,$p' >"${WORK_DIR}/${name}.notes" && [ -s "${WORK_DIR}/${name}.notes" ]; then
    pass "${name}: NOTES rendered"
  else
    fail "${name}: no NOTES: $(cat "${WORK_DIR}/${name}.err")"
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

# An external database: postgresql.enabled=false needs one (render-external-database.sh).
EXT=(--set postgresql.enabled=false
  --set externalDatabase.host=db.example.com
  --set externalDatabase.database=amp
  --set externalDatabase.username=amp
  --set externalDatabase.existingSecret=amp-db)

echo "T1 shipped values: no backup, no PDB, pinned image:"
render default
render production "${PROD_ARGS[@]}"
v=default
f="${WORK_DIR}/${v}.yaml"
assert_docs "${f}" StatefulSet amp-postgres 1 "${v}: bundled PostgreSQL StatefulSet rendered"
got="$(doc_line "${f}" StatefulSet amp-postgres '^  replicas:')"
if [ "${got}" = "  replicas: 1" ]; then pass "${v}: Postgres StatefulSet has replicas: 1"; else fail "${v}: Postgres StatefulSet replicas line is '${got}'"; fi
assert_docs "${f}" PodDisruptionBudget amp-postgres 0 "${v}: no Postgres PDB"
assert_docs "${f}" CronJob amp-postgres-backup 0 "${v}: no backup CronJob"
assert_docs "${f}" PersistentVolumeClaim amp-postgres-backup 0 "${v}: no backup PVC"
assert_count "${f}" "archive_mode" 0 "${v}: no WAL archiving settings"
assert_count "${f}" "PostgreSQLBackupOld" 0 "${v}: no PostgreSQLBackupOld rule"
assert_count "${f}" "PostgreSQLWALArchivingFailed" 0 "${v}: no PostgreSQLWALArchivingFailed rule"
assert_count "${f}" "image: postgres:16.15" 2 "${v}: StatefulSet and test pod use postgres:16.15"
assert_count "${f}" "image: postgres:16" 2 "${v}: no other postgres:16* image"
f="${WORK_DIR}/production.yaml"
assert_docs "${f}" StatefulSet amp-postgres 0 "production: no bundled PostgreSQL (external database)"
assert_docs "${f}" CronJob amp-postgres-backup 0 "production: no backup CronJob"
assert_count "${f}" "image: postgres:" 0 "production: no postgres image"

echo "T2 replicas other than 1 fail the render:"
render_fails replicas-2 "postgresql.replicas must be 1" --set postgresql.replicas=2
render_fails replicas-0 "postgresql.replicas must be 1" --set postgresql.replicas=0
render replicas-1-string --set-string postgresql.replicas=1

echo "T3 an enabled backup fails the render, with the --reuse-values hint:"
HINT="use --reset-then-reuse-values, or add --set postgresql.backup.enabled=false"
render_fails backup-enabled "${HINT}" --set postgresql.backup.enabled=true
render_fails backup-enabled-string "${HINT}" --set-string postgresql.backup.enabled=true
render_fails backup-scalar-true "${HINT}" --set postgresql.backup=true
# What --reuse-values carries over from a release of the previous chart: its
# default postgresql.backup block.
cat >"${WORK_DIR}/old-defaults.yaml" <<'EOF'
postgresql:
  backup:
    enabled: true
    schedule: "0 2 * * *"
    retention: 30
EOF
render_fails backup-old-defaults "${HINT}" -f "${WORK_DIR}/old-defaults.yaml"
render backup-old-defaults-override -f "${WORK_DIR}/old-defaults.yaml" --set postgresql.backup.enabled=false

echo "T4 leftovers of the removed key render:"
printf 'postgresql:\n  backup:\n' >"${WORK_DIR}/empty-backup.yaml"
render backup-empty -f "${WORK_DIR}/empty-backup.yaml"
render backup-null --set postgresql.backup=null
render backup-false --set postgresql.backup=false
render backup-enabled-false --set postgresql.backup.enabled=false
# Accepted (deep review R22): only true/"true" count as enabled.
render backup-enabled-one --set postgresql.backup.enabled=1
render backup-enabled-title-case --set-string postgresql.backup.enabled=True

echo "T5 guards do not apply without the bundled PostgreSQL:"
render external-db "${EXT[@]}" --set postgresql.replicas=3 --set postgresql.backup.enabled=true
assert_docs "${WORK_DIR}/external-db.yaml" StatefulSet amp-postgres 0 "external-db: no bundled PostgreSQL"
render lite --set profile=lite

echo "T6 NOTES:"
notes notes-default
notes notes-no-hpa --set autoscaling.enabled=false
notes notes-external "${EXT[@]}"
notes notes-max-8 --set autoscaling.maxReplicas=8
for n in notes-default notes-no-hpa; do
  f="${WORK_DIR}/${n}.notes"
  assert_count "${f}" "HA-Ready" 0 "${n}: standard profile not advertised as HA"
  assert_count "${f}" "external storage (HA)" 0 "${n}: bundled PostgreSQL not advertised as HA"
  assert_count "${f}" "dev/pilot only" 1 "${n}: bundled PostgreSQL marked dev/pilot"
  assert_count "${f}" "PG-CONFIG-NOT-APPLIED" 0 "${n}: no config-not-applied warning"
  assert_count "${f}" "Expected: 250" 1 "${n}: step 4 expects max_connections 250"
  assert_count "${f}" "(OK)" 0 "${n}: no max_connections OK line"
done
# 10 replicas x 25 = 250 exhaust max_connections; 8 x 25 do not.
assert_count "${WORK_DIR}/notes-default.notes" "allows max_connections=250" 1 "notes-default: connection budget warning"
assert_count "${WORK_DIR}/notes-max-8.notes" "allows max_connections=" 0 "notes-max-8: no connection budget warning"
f="${WORK_DIR}/notes-external.notes"
assert_count "${f}" "External PostgreSQL: db.example.com:5432/amp (sslmode=require), password from Secret amp-db/password" 1 "notes-external: external PostgreSQL line"
assert_count "${f}" "dev/pilot only" 0 "notes-external: no bundled PostgreSQL warning"
assert_count "${f}" "Check PostgreSQL" 0 "notes-external: no bundled PostgreSQL steps"

echo "T7 the server runs with the chart config (PG-CONFIG-FIX):"
f="${WORK_DIR}/default.yaml"
sts="${WORK_DIR}/default-sts.yaml"
cm="${WORK_DIR}/default-cm.yaml"
doc_line "${f}" StatefulSet amp-postgres '' >"${sts}"
doc_line "${f}" ConfigMap amp-postgres-config '' >"${cm}"
# T7.1: server arguments, so an initialized PVC picks them up too.
assert_count "${sts}" '- "config_file=/etc/postgresql/postgresql.conf"' 1 "args: config_file"
assert_count "${sts}" '- "hba_file=/etc/postgresql/pg_hba.conf"' 1 "args: hba_file"
# T7.2: no initdb-time config; init.sql stays.
assert_count "${sts}" "POSTGRES_INITDB_ARGS" 0 "no POSTGRES_INITDB_ARGS"
assert_count "${sts}" "mountPath: /docker-entrypoint-initdb.d/postgresql.conf" 0 "no postgresql.conf in initdb.d"
assert_count "${sts}" "mountPath: /docker-entrypoint-initdb.d/pg_hba.conf" 0 "no pg_hba.conf in initdb.d"
assert_count "${sts}" "mountPath: /docker-entrypoint-initdb.d/init.sql" 1 "init.sql in initdb.d"
# T7.3: /etc/postgresql is the ConfigMap, not an emptyDir.
assert_count "${sts}" "mountPath: /etc/postgresql" 1 "one /etc/postgresql mount"
got="$(grep -B1 -x -e " *mountPath: /etc/postgresql" "${sts}" | head -1 | awk '{ print $NF }')"
if [ "${got}" = "custom-config" ]; then pass "/etc/postgresql is volume custom-config"; else fail "/etc/postgresql is volume '${got}', want custom-config"; fi
got="$(grep -A3 -F -e "- name: custom-config" "${sts}" | grep -c -F -e "name: amp-postgres-config" || true)"
if [ "${got}" -eq 1 ]; then pass "custom-config volume is ConfigMap amp-postgres-config"; else fail "custom-config volume is not ConfigMap amp-postgres-config"; fi
assert_count "${sts}" "name: postgres-config" 0 "no postgres-config emptyDir"
# T7.4: no init-time server settings or views.
assert_count "${cm}" "ALTER SYSTEM" 0 "no ALTER SYSTEM in init.sql"
assert_count "${cm}" "CREATE OR REPLACE VIEW" 0 "no views in init.sql"
# A whole line: init.sql on main had "ALTER SYSTEM SET pg_stat_statements.max = 10000;".
got="$(grep -c -x -e " *pg_stat_statements\.max = 10000" "${cm}" || true)"
if [ "${got}" -eq 1 ]; then pass "pg_stat_statements settings in postgresql.conf"; else fail "pg_stat_statements.max = 10000 lines in postgresql.conf: ${got}, want 1"; fi
# T7.5: the full pg_hba.conf rule set; trust only on the Unix socket (deep review R6).
cat >"${WORK_DIR}/hba-want.txt" <<'EOF'
local all all trust
host all all 127.0.0.1/32 scram-sha-256
host all all ::1/128 scram-sha-256
host alert_history alert_history all scram-sha-256
host all all all reject
EOF
doc_line "${f}" ConfigMap amp-postgres-config '^    (local|host)[[:space:]]' |
  awk '{ $1 = $1; print }' >"${WORK_DIR}/hba-got.txt"
if diff -u "${WORK_DIR}/hba-want.txt" "${WORK_DIR}/hba-got.txt" >"${WORK_DIR}/hba.diff"; then
  pass "pg_hba.conf: socket trust, SCRAM on loopback and for the app, reject the rest"
else
  fail "pg_hba.conf rules differ: $(cat "${WORK_DIR}/hba.diff")"
fi
# T7.6: logs stay in kubectl logs.
assert_count "${cm}" "logging_collector = off" 1 "logging_collector off"
# T7.8: /dev/shm is a memory volume.
assert_count "${sts}" "mountPath: /dev/shm" 1 "/dev/shm mounted"
assert_count "${sts}" "medium: Memory" 1 "/dev/shm is a memory emptyDir"
# T7.9: the exporter connects and its queries match the catalog.
assert_count "${sts}" "name: DATA_SOURCE_URI" 1 "exporter: DATA_SOURCE_URI"
assert_count "${sts}" "name: DATA_SOURCE_USER" 1 "exporter: DATA_SOURCE_USER"
assert_count "${sts}" "name: DATA_SOURCE_PASS" 1 "exporter: DATA_SOURCE_PASS"
assert_count "${sts}" "DATA_SOURCE_NAME" 0 "exporter: no DATA_SOURCE_NAME"
assert_count "${sts}" '$(POSTGRES_' 0 "exporter: no \$(POSTGRES_*) references"
got="$(grep -A1 -F -e "- name: PG_EXPORTER_AUTO_DISCOVER_DATABASES" "${sts}" | grep -c -F -e 'value: "false"' || true)"
if [ "${got}" -eq 1 ]; then pass "exporter: database auto-discovery off"; else fail "exporter: database auto-discovery not off"; fi
exp="${WORK_DIR}/default-exporter-cm.yaml"
doc_line "${f}" ConfigMap amp-postgres-exporter-config '' >"${exp}"
assert_count "${exp}" "tablename" 0 "exporter queries: no tablename column"
assert_count "${exp}" "indexname as" 0 "exporter queries: no indexname column"
# T7.10: the rule group parses (deep review R2).
rules="${WORK_DIR}/default-rules.yaml"
doc_line "${f}" PrometheusRule amp-postgres '' >"${rules}"
assert_count "${rules}" "now()" 0 "rules: no now() (not PromQL)"
assert_count "${rules}" "and pg_stat_user_tables_last_autovacuum > 0" 1 "rules: never-autovacuumed tables skipped"
# T7.11: helm test checks the running server, not only the render (deep review R7).
assert_count "${f}" '"config_file /etc/postgresql/postgresql.conf"' 1 "helm test: checks config_file"
assert_count "${f}" '"hba_file /etc/postgresql/pg_hba.conf"' 1 "helm test: checks hba_file"

if [ "${failures}" -ne 0 ]; then
  printf '\n%d assertion(s) failed\n' "${failures}" >&2
  exit 1
fi

printf '\nall assertions passed\n'
