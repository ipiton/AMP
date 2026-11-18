#!/usr/bin/env bash
# Checks the rendered PostgreSQL alert rules (PG-ALERT-RULES-METRICS):
#   - every pg_* metric they use is one the exporter really serves
#     (postgresql-exporter-metrics.txt: names scraped from postgres-exporter
#     v0.15.0 with the chart's queries on postgres:16.15; regenerate it when
#     either version or the queries change);
#   - every selector is scoped to this release's scrape job;
#   - promtool accepts them and postgresql-rules-test.yaml passes (skipped
#     without promtool).
#
#   helm/amp/tests/render-postgresql-rules.sh
set -euo pipefail

TESTS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CHART_DIR="$(cd "${TESTS_DIR}/.." && pwd)"
WORK_DIR="$(mktemp -d)"
trap 'rm -rf "${WORK_DIR}"' EXIT

if ! command -v helm >/dev/null 2>&1; then
  echo "SKIP: helm not installed"
  exit 0
fi

failures=0
pass() { printf '  ok   %s\n' "$1"; }
fail() {
  printf '  FAIL %s\n' "$1" >&2
  failures=$((failures + 1))
}

helm template amp "${CHART_DIR}" -n amp --api-versions monitoring.coreos.com/v1 \
  --show-only templates/postgresql-prometheus-rules.yaml >"${WORK_DIR}/rendered.yaml"
# The rule file is the object's spec, one level up.
sed -n '/^spec:$/,$p' "${WORK_DIR}/rendered.yaml" | sed '1d; s/^  //' >"${WORK_DIR}/rules.yaml"
grep -E '^ *expr: ' "${WORK_DIR}/rules.yaml" >"${WORK_DIR}/exprs.txt"

echo "metrics the rules use exist:"
while IFS= read -r metric; do
  if grep -q -x -F -e "${metric}" "${TESTS_DIR}/postgresql-exporter-metrics.txt"; then
    pass "${metric}"
  else
    fail "${metric}: not served by the exporter"
  fi
done < <(grep -o -E 'pg_[a-z0-9_]+' "${WORK_DIR}/exprs.txt" | sort -u)

echo "selectors are scoped to the release's scrape job:"
total="$(grep -o -E 'pg_[a-z0-9_]+' "${WORK_DIR}/exprs.txt" | wc -l | tr -d ' ')"
scoped="$(grep -o -E 'pg_[a-z0-9_]+\{namespace="amp",job="amp-postgres-exporter"' "${WORK_DIR}/exprs.txt" | wc -l | tr -d ' ')"
if [ "${total}" -gt 0 ] && [ "${total}" = "${scoped}" ]; then
  pass "all ${total} metric references"
else
  fail "${scoped} of ${total} metric references are scoped"
fi

if command -v promtool >/dev/null 2>&1; then
  echo "promtool:"
  cp "${TESTS_DIR}/postgresql-rules-test.yaml" "${WORK_DIR}/test.yaml"
  if output="$(promtool check rules "${WORK_DIR}/rules.yaml" 2>&1)"; then pass "check rules"; else fail "check rules: ${output}"; fi
  if output="$(cd "${WORK_DIR}" && promtool test rules test.yaml 2>&1)"; then pass "test rules"; else fail "test rules: ${output}"; fi
else
  echo "SKIP: promtool not installed"
fi

if [ "${failures}" -ne 0 ]; then
  printf '\n%d assertion(s) failed\n' "${failures}" >&2
  exit 1
fi
printf '\nall assertions passed\n'
