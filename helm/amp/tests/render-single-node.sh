#!/usr/bin/env bash
# Renders the chart and checks the one-node defaults and the Valkey cache
# (HELM-SINGLE-NODE-DEFAULTS, ADR-017):
#
#   - defaults: one AMP replica, no HPA, requests of the whole stack
#     270m / 512Mi, no valkey subchart objects, no <fullname>-pvc under
#     profile: standard, the cache image is valkey/valkey;
#   - autoscaling on: the HPA renders without helm.sh/resource-policy: keep;
#   - profile: lite keeps <fullname>-pvc; values-production.yaml keeps HPA 3-20;
#   - valkey.image.tag is required; a repository without "valkey" renders no
#     data move; values-small.yaml renders with its limits;
#   - NOTES: the cache line follows cache.enabled / valkey.replicas, lite shows
#     requests from values;
#   - the config-init data move, run with sh on fixture files: RDB v12 data
#     moves aside, dump.rdb and appendonlydir separately, Valkey data stays.
#
#   helm/amp/tests/render-single-node.sh
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

# requests_total <file>: "<millicores>m/<MiB>Mi" summed over the container
# requests of Deployments and StatefulSets, times their replicas (no yq in the
# gate's toolchain). Handles the units the chart uses: m / whole cores, Mi / Gi.
requests_total() {
  awk '
    function flush() { if (w) { cpu += dcpu * r; mem += dmem * r } w = 0; r = 1; dcpu = 0; dmem = 0; inreq = 0 }
    /^---/ { flush(); next }
    /^kind: (Deployment|StatefulSet)$/ { w = 1 }
    /^  replicas: / { r = $2 }
    /^ *requests:$/ { inreq = 1; ind = index($0, "r"); next }
    inreq {
      if (index($0, $1) <= ind) { inreq = 0 }
      else if ($1 == "cpu:") { v = $2; gsub(/"/, "", v); dcpu += (v ~ /m$/) ? v + 0 : v * 1000 }
      else if ($1 == "memory:") { v = $2; gsub(/"/, "", v); dmem += (v ~ /Gi$/) ? v * 1024 : v + 0 }
    }
    END { flush(); printf "%dm/%dMi\n", cpu, mem }' "$1"
}

if ! command -v helm >/dev/null 2>&1; then
  echo "SKIP: helm not installed"
  exit 0
fi

# values-production.yaml ships no passwords, auth or NetworkPolicy sources
# (guards fail the render until they are supplied) -- placeholders, as
# scripts/release-gate.sh does.
PROD_ARGS=(-f "${CHART_DIR}/values-production.yaml"
  -f "${CHART_DIR}/tests/values-production-placeholders.yaml"
  --set postgresql.password=render-test-placeholder
  --set cache.auth.password=render-test-placeholder)

echo "T1 defaults fit one node:"
render default
f="${WORK_DIR}/default.yaml"
assert_docs "${f}" HorizontalPodAutoscaler amp 0 "default: no HPA"
got="$(doc_line "${f}" Deployment amp '^  replicas:')"
if [ "${got}" = "  replicas: 1" ]; then pass "default: Deployment has replicas: 1"; else fail "default: Deployment replicas line is '${got}'"; fi
got="$(requests_total "${f}")"
if [ "${got}" = "270m/512Mi" ]; then pass "default: requests total 270m/512Mi"; else fail "default: requests total ${got}, want 270m/512Mi"; fi
assert_count "${f}" "amp-valkey" 0 "default: no valkey subchart objects"
assert_docs "${f}" PersistentVolumeClaim amp-pvc 0 "default: no amp-pvc under profile: standard"
assert_count "${f}" "image: valkey/valkey:8.1.10-alpine" 1 "default: cache image valkey/valkey:8.1.10-alpine"
assert_count "${f}" "image: redis:" 0 "default: no redis image"
assert_count "${f}" "redis-pre-valkey-" 1 "default: config-init moves Redis >= 7.4 data"

echo "T2 autoscaling on: HPA without keep:"
render hpa --set autoscaling.enabled=true
f="${WORK_DIR}/hpa.yaml"
assert_docs "${f}" HorizontalPodAutoscaler amp 1 "hpa: HPA rendered"
got="$(doc_line "${f}" HorizontalPodAutoscaler amp 'helm.sh/resource-policy')"
if [ -z "${got}" ]; then pass "hpa: no helm.sh/resource-policy on the HPA"; else fail "hpa: HPA carries '${got}'"; fi

echo "T3 lite and production:"
render lite --set profile=lite
assert_docs "${WORK_DIR}/lite.yaml" PersistentVolumeClaim amp-pvc 1 "lite: amp-pvc rendered"
assert_docs "${WORK_DIR}/lite.yaml" HorizontalPodAutoscaler amp 0 "lite: no HPA"
render production "${PROD_ARGS[@]}"
f="${WORK_DIR}/production.yaml"
assert_docs "${f}" HorizontalPodAutoscaler amp 1 "production: HPA rendered"
got="$(doc_line "${f}" HorizontalPodAutoscaler amp '^  (minReplicas|maxReplicas):' | tr -s ' ' | tr '\n' ' ')"
if [ "${got}" = " minReplicas: 3  maxReplicas: 20 " ]; then pass "production: HPA 3-20"; else fail "production: HPA replicas '${got}', want 3-20"; fi
assert_count "${f}" "amp-valkey" 0 "production: no valkey subchart objects"
assert_docs "${f}" PersistentVolumeClaim amp-pvc 0 "production: no amp-pvc"

echo "T4 cache image:"
render_fails empty-tag "valkey.image.tag is required" --set valkey.image.tag=
render own-redis --set valkey.image.repository=redis --set valkey.image.tag=7.4-alpine
assert_count "${WORK_DIR}/own-redis.yaml" "image: redis:7.4-alpine" 1 "own-redis: image redis:7.4-alpine"
assert_count "${WORK_DIR}/own-redis.yaml" "redis-pre-valkey-" 0 "own-redis: no data move"

echo "T5 values-small.yaml:"
render small -f "${CHART_DIR}/values-small.yaml"
f="${WORK_DIR}/small.yaml"
assert_docs "${f}" HorizontalPodAutoscaler amp 0 "small: no HPA"
assert_count "${f}" "maxmemory 192mb" 1 "small: Valkey maxmemory 192mb"
assert_count "${f}" "of maxmemory (192mb)" 2 "small: alert descriptions show maxmemory from values"
assert_count "${f}" "max_connections = 60" 1 "small: max_connections 60"
assert_count "${f}" "max_wal_size = 1GB" 1 "small: max_wal_size 1GB"

echo "T6 NOTES:"
notes notes-default
notes notes-cache-off --set cache.enabled=false
notes notes-external-cache --set valkey.replicas=0 --set cache.host=dragonfly.example.com
notes notes-lite --set profile=lite
assert_count "${WORK_DIR}/notes-default.notes" "Valkey L2 + Memory L1 cache" 1 "notes-default: Valkey cache line"
assert_count "${WORK_DIR}/notes-default.notes" "HPA off: fixed 1 replica(s)" 1 "notes-default: HPA off line"
assert_count "${WORK_DIR}/notes-default.notes" "requests 100m CPU / 128Mi, limits 1000m CPU / 1Gi" 1 "notes-default: AMP resources from values"
assert_count "${WORK_DIR}/notes-cache-off.notes" "Cache off" 1 "notes-cache-off: cache off line"
assert_count "${WORK_DIR}/notes-cache-off.notes" "Valkey L2" 0 "notes-cache-off: no Valkey line"
assert_count "${WORK_DIR}/notes-external-cache.notes" "External cache L2 (dragonfly.example.com)" 1 "notes-external-cache: external cache line"
assert_count "${WORK_DIR}/notes-lite.notes" "Resources: requests 100m CPU / 128Mi" 1 "notes-lite: resources from values"

echo "T7 config-init data move on fixtures (sh, not busybox):"
# The script as rendered, up to the redis.conf copy, with /data pointing at a
# fixture directory. Only the 9-byte RDB header matters to it; real Redis 7.4
# and Valkey files were checked in tasks/archive/HELM-SINGLE-NODE-DEFAULTS/evidence/.
awk '
  /^---/ { s = 0 }
  /^kind: StatefulSet$/ { s = 1 }
  s && /^  name: amp-redis$/ { r = 1 }
  r && /- name: config-init/ { c = 1 }
  c && /^        - \|$/ { b = 1; next }
  b && /Copying redis.conf/ { exit }
  b { sub(/^          /, ""); print }' "${WORK_DIR}/default.yaml" >"${WORK_DIR}/move.sh"
if grep -q "rdb_v12" "${WORK_DIR}/move.sh"; then pass "config-init script extracted"; else fail "config-init script not found in the render"; fi

# fixture <dir> <dump header|-> <base header|->
fixture() {
  mkdir -p "$1"
  if [ "$2" != "-" ]; then printf '%sxxxx' "$2" >"$1/dump.rdb"; fi
  if [ "$3" != "-" ]; then
    mkdir -p "$1/appendonlydir"
    printf '%sxxxx' "$3" >"$1/appendonlydir/appendonly.aof.2.base.rdb"
    printf '*2\r\n' >"$1/appendonlydir/appendonly.aof.2.incr.aof"
    printf 'file appendonly.aof.2.base.rdb seq 2 type b\n' >"$1/appendonlydir/appendonly.aof.manifest"
  fi
}

# check_move <case> <dump header|-> <base header|-> <moved: none|dump|aof|both>
check_move() {
  local name="$1" dir="${WORK_DIR}/fx-$1" want="$4" got
  fixture "${dir}" "$2" "$3"
  sed "s#/data#${dir}#g" "${WORK_DIR}/move.sh" >"${WORK_DIR}/move-${name}.sh"
  if ! sh "${WORK_DIR}/move-${name}.sh" >"${WORK_DIR}/move-${name}.out" 2>&1; then
    fail "${name}: script failed: $(cat "${WORK_DIR}/move-${name}.out")"
    return
  fi
  got=""
  for b in "${dir}"/redis-pre-valkey-*; do
    [ -f "${b}/dump.rdb" ] && got="${got}dump"
    [ -d "${b}/appendonlydir" ] && got="${got}aof"
  done
  case "${got}" in "") got=none ;; dumpaof) got=both ;; esac
  if [ "${got}" = "${want}" ]; then pass "${name}: moved ${want}"; else fail "${name}: moved ${got}, want ${want}"; fi
  # Run again: nothing left to move.
  sh "${WORK_DIR}/move-${name}.sh" >"${WORK_DIR}/move-${name}.out2" 2>&1
  assert_count "${WORK_DIR}/move-${name}.out2" "No Redis >= 7.4 data on the volume" 1 "${name}: second run moves nothing"
}

check_move redis-7.4 REDIS0012 REDIS0012 both
check_move after-rollback REDIS0012 REDIS0011 dump
check_move aof-only - REDIS0012 aof
check_move rdb-only REDIS0012 - dump
check_move valkey-8.1 REDIS0011 REDIS0011 none
check_move valkey-9.0 VALKEY080 VALKEY080 none
check_move empty - - none

if [ "${failures}" -ne 0 ]; then
  printf '\n%d assertion(s) failed\n' "${failures}" >&2
  exit 1
fi

printf '\nall assertions passed\n'
