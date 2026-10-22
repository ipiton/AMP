#!/usr/bin/env bash
# Renders the cache StatefulSet and asserts that its volumeClaimTemplates do
# not depend on the chart version (HELM-VCT-CHART-LABELS).
#
# volumeClaimTemplates is immutable on an existing StatefulSet. Before the fix
# the template carried amp.labels (helm.sh/chart, app.kubernetes.io/version),
# so any bump of version/appVersion in Chart.yaml failed `helm upgrade` with
# `spec: Forbidden`. The test renders the chart as is and a copy with another
# version/appVersion and requires byte-identical volumeClaimTemplates.
#
#   helm/amp/tests/render-vct-labels.sh
#
# Runs every assertion, then exits non-zero if any failed.
set -euo pipefail

CHART_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK_DIR="$(mktemp -d)"
trap 'rm -rf "${WORK_DIR}"' EXIT

STATEFULSET="templates/redis-statefulset.yaml"

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

# render_vct <chart-dir> <out-file>: volumeClaimTemplates of the cache
# StatefulSet (it is the last field of the manifest).
render_vct() {
  helm template t "$1" -s "${STATEFULSET}" | sed -n '/^  volumeClaimTemplates:/,$p' >"$2"
}

echo "== volumeClaimTemplates labels =="
render_vct "${CHART_DIR}" "${WORK_DIR}/vct.yaml"
assert_contains "${WORK_DIR}/vct.yaml" "name: redis-data" "VCT redis-data rendered"
assert_contains "${WORK_DIR}/vct.yaml" "app.kubernetes.io/name: amp" "selector label name"
assert_contains "${WORK_DIR}/vct.yaml" "app.kubernetes.io/instance: t" "selector label instance"
assert_contains "${WORK_DIR}/vct.yaml" "app.kubernetes.io/component: redis-storage" "component label"
assert_absent "${WORK_DIR}/vct.yaml" "helm.sh/chart" "no chart label"
assert_absent "${WORK_DIR}/vct.yaml" "app.kubernetes.io/version" "no version label"

echo "== same volumeClaimTemplates after a version bump =="
cp -R "${CHART_DIR}" "${WORK_DIR}/chart"
sed -i.bak -e 's/^version: .*/version: 99.1.0/' -e 's/^appVersion: .*/appVersion: "99.1.0"/' \
  "${WORK_DIR}/chart/Chart.yaml"
helm template t "${WORK_DIR}/chart" -s "${STATEFULSET}" >"${WORK_DIR}/bumped-sts.yaml"
assert_contains "${WORK_DIR}/bumped-sts.yaml" "helm.sh/chart: amp-99.1.0" "bumped chart renders the new version"
render_vct "${WORK_DIR}/chart" "${WORK_DIR}/vct-bumped.yaml"
if diff -u "${WORK_DIR}/vct.yaml" "${WORK_DIR}/vct-bumped.yaml" >"${WORK_DIR}/vct.diff"; then
  pass "volumeClaimTemplates identical across versions"
else
  cat "${WORK_DIR}/vct.diff" >&2
  fail "volumeClaimTemplates differ across versions"
fi

if [ "${failures}" -gt 0 ]; then
  echo "${failures} assertion(s) failed" >&2
  exit 1
fi
echo "all assertions passed"
