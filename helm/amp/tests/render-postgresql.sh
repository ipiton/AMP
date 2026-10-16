#!/usr/bin/env bash
# Renders the chart and checks the bundled PostgreSQL contract of ADR-016
# (PROD-POSTGRES-HA-DECISION):
#
#   - no backup CronJob, backup PVC, WAL archiving or Postgres PDB is rendered;
#   - the image is pinned to postgres:16.15;
#   - postgresql.replicas other than 1 and postgresql.backup enabled fail the
#     render, including the old chart default carried by --reuse-values;
#     null/false/empty leftovers of the removed key do not;
#   - NOTES no longer advertise HA or an applied max_connections;
#   - values-production.yaml runs no bundled PostgreSQL (external database,
#     HELM-EXTERNAL-DATABASE).
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
for n in notes-default notes-no-hpa; do
  f="${WORK_DIR}/${n}.notes"
  assert_count "${f}" "HA-Ready" 0 "${n}: standard profile not advertised as HA"
  assert_count "${f}" "external storage (HA)" 0 "${n}: bundled PostgreSQL not advertised as HA"
  assert_count "${f}" "dev/pilot only" 1 "${n}: bundled PostgreSQL marked dev/pilot"
  assert_count "${f}" "PG-CONFIG-NOT-APPLIED" 2 "${n}: config-not-applied warning and step 4 note"
  assert_count "${f}" "Expected: 250" 0 "${n}: no claim that max_connections is 250"
  assert_count "${f}" "(OK)" 0 "${n}: no max_connections OK line"
done
f="${WORK_DIR}/notes-external.notes"
assert_count "${f}" "External PostgreSQL: db.example.com:5432/amp (sslmode=require), password from Secret amp-db/password" 1 "notes-external: external PostgreSQL line"
assert_count "${f}" "dev/pilot only" 0 "notes-external: no bundled PostgreSQL warning"
assert_count "${f}" "Check PostgreSQL" 0 "notes-external: no bundled PostgreSQL steps"

if [ "${failures}" -ne 0 ]; then
  printf '\n%d assertion(s) failed\n' "${failures}" >&2
  exit 1
fi

printf '\nall assertions passed\n'
