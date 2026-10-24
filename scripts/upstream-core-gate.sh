#!/usr/bin/env bash
# Regression gate for the upstream Alertmanager core (ADR-019):
#
#   ./scripts/upstream-core-gate.sh
#
# 1. Runs the futureparity contract suites against the upstream core (build
#    tags futureparity,upstreamcore) and compares the leaf test failures
#    with go-app/cmd/server/testdata/upstreamcore-divergences.tsv. Any
#    failure missing from the baseline fails the gate, and so does a "fails"
#    entry that passed or did not run: the baseline must describe exactly
#    what diverges, which is what makes an upstream bump reviewable.
# 2. Builds cmd/amp-upstream, starts it on a random port and runs amtool at
#    the upstream version pinned in go-app/go.mod against it.
#
# The upstream version comes from go.mod only (scripts/upstream-bump.sh,
# docs/UPSTREAM_UPGRADE.md). Bash 3.2-compatible, like release-gate.sh.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
GO_APP_DIR="$ROOT_DIR/go-app"
BASELINE="$GO_APP_DIR/cmd/server/testdata/upstreamcore-divergences.tsv"
AM_CONFIG="$GO_APP_DIR/cmd/server/testdata/upstreamcore-alertmanager.yml"
UPSTREAM_MODULE="github.com/prometheus/alertmanager"

log() { printf '[upstream-core-gate] %s\n' "$1"; }

for tool in go jq curl; do
  command -v "$tool" >/dev/null 2>&1 || { log "$tool is required"; exit 1; }
done

WORK_DIR="$(mktemp -d)"
AMP_PID=""
cleanup() {
  if [[ -n "$AMP_PID" ]] && kill -0 "$AMP_PID" 2>/dev/null; then
    kill -KILL "$AMP_PID" 2>/dev/null || true
  fi
  rm -r "$WORK_DIR"
}
trap cleanup EXIT

FAILED=0

check_suites() {
  local json="$WORK_DIR/suites.json" stderr="$WORK_DIR/suites.stderr"
  log "==> contract suites on the upstream core"

  # Every entry needs a known class and expectation: a typo such as "fail"
  # must not quietly turn an entry into one that tolerates any outcome.
  local bad
  bad="$(grep -v '^#' "$BASELINE" | awk -F'\t' 'NF > 0 && (NF < 4 || $2 !~ /^[abcd]$/ || $3 !~ /^(fails|flaky)$/)')"
  if [[ -n "$bad" ]]; then
    log "malformed baseline entries (test<TAB>a|b|c|d<TAB>fails|flaky<TAB>reason):"
    printf '%s\n' "$bad"
    return 1
  fi

  # A non-zero exit is expected: the baseline failures are real failures.
  ( cd "$GO_APP_DIR" && go test -tags futureparity,upstreamcore -count=1 -json ./cmd/server/ ) >"$json" 2>"$stderr"

  if ! jq -e 'select(.Action=="run")' "$json" >/dev/null 2>&1; then
    log "no test ran -- build or setup failure:"
    jq -r 'select(.Action=="output" or .Action=="build-output") | .Output' "$json" 2>/dev/null || cat "$json"
    cat "$stderr"
    return 1
  fi
  if jq -e 'select(.Test == null and .Action=="output" and (.Output | startswith("panic:")))' "$json" >/dev/null; then
    log "the test binary panicked outside a test:"
    jq -r 'select(.Test == null and .Action=="output") | .Output' "$json"
    return 1
  fi

  jq -r 'select(.Test != null and .Action=="fail") | .Test' "$json" | sort -u >"$WORK_DIR/failed"
  jq -r 'select(.Test != null and .Action=="pass") | .Test' "$json" | sort -u >"$WORK_DIR/passed"
  # A parent fails whenever a subtest does; only leaves carry the cause.
  awk '{ t[NR] = $0 } END {
    for (i = 1; i <= NR; i++) {
      leaf = 1
      for (j = 1; j <= NR; j++) if (index(t[j], t[i] "/") == 1) { leaf = 0; break }
      if (leaf) print t[i]
    }
  }' "$WORK_DIR/failed" >"$WORK_DIR/leaf"
  # A parent that also failed on its own (t.Error outside its subtests)
  # counts as a failure of its own.
  jq -r 'select(.Test != null and .Action=="output" and (.Output | test("^\\s+\\S+_test\\.go:[0-9]+: "))) | .Test' "$json" |
    sort -u | comm -12 - "$WORK_DIR/failed" >>"$WORK_DIR/leaf"
  sort -u -o "$WORK_DIR/leaf" "$WORK_DIR/leaf"

  grep -v '^#' "$BASELINE" | awk -F'\t' 'NF >= 4 { print $1 }' | sort >"$WORK_DIR/known"
  grep -v '^#' "$BASELINE" | awk -F'\t' 'NF >= 4 && $3 == "fails" { print $1 }' | sort >"$WORK_DIR/must_fail"

  local rc=0 t
  comm -23 "$WORK_DIR/leaf" "$WORK_DIR/known" >"$WORK_DIR/new"
  if [[ -s "$WORK_DIR/new" ]]; then
    rc=1
    log "failures missing from $(basename "$BASELINE"):"
    while IFS= read -r t; do
      log "  NEW $t"
      jq -r --arg t "$t" 'select(.Test==$t and .Action=="output") | "      " + .Output' "$json" | grep -v -e '^      === ' -e '^      --- ' || true
    done <"$WORK_DIR/new"
  fi

  # A "fails" entry must fail; any entry, "flaky" included, must still run.
  sort -u "$WORK_DIR/failed" "$WORK_DIR/passed" >"$WORK_DIR/ran"
  { comm -23 "$WORK_DIR/must_fail" "$WORK_DIR/leaf"; comm -23 "$WORK_DIR/known" "$WORK_DIR/ran"; } | sort -u >"$WORK_DIR/stale"
  if [[ -s "$WORK_DIR/stale" ]]; then
    rc=1
    log "baseline entries that are stale -- remove, rename or reclassify them:"
    while IFS= read -r t; do
      if grep -qxF "$t" "$WORK_DIR/passed"; then
        log "  PASSED  $t"
      else
        log "  NOT RUN $t (renamed or removed?)"
      fi
    done <"$WORK_DIR/stale"
  fi

  log "suites: $(wc -l <"$WORK_DIR/passed" | tr -d ' ') passed, $(wc -l <"$WORK_DIR/leaf" | tr -d ' ') known-divergent failures"
  return "$rc"
}

check_amtool() {
  local version addr="" tries amtool url
  version="$(cd "$GO_APP_DIR" && go list -m -f '{{.Version}}' "$UPSTREAM_MODULE")" || return 1
  log "==> amtool $version against cmd/amp-upstream"

  ( cd "$GO_APP_DIR" && go build -o "$WORK_DIR/amp-upstream" ./cmd/amp-upstream ) || return 1
  # amtool is built from the upstream module at the pinned version, against
  # upstream's own go.mod -- it moves with the core on every bump.
  GOBIN="$WORK_DIR/bin" go install "$UPSTREAM_MODULE/cmd/amtool@$version" || return 1
  amtool="$WORK_DIR/bin/amtool"

  "$WORK_DIR/amp-upstream" \
    --config.file="$AM_CONFIG" \
    --storage.path="$WORK_DIR/data" \
    --web.listen-address=127.0.0.1:0 \
    >"$WORK_DIR/amp-upstream.log" 2>&1 &
  AMP_PID=$!

  # The binary logs the bound address once it serves. The first exec of a
  # freshly built binary on macOS can take over 10s (system checks).
  tries=600
  while (( tries > 0 )); do
    addr="$(jq -r 'select(.msg=="listening") | .address' "$WORK_DIR/amp-upstream.log" 2>/dev/null | head -n 1)"
    [[ -n "$addr" ]] && break
    if ! kill -0 "$AMP_PID" 2>/dev/null; then
      break
    fi
    sleep 0.1
    tries=$((tries - 1))
  done
  if [[ -z "$addr" ]]; then
    if kill -0 "$AMP_PID" 2>/dev/null; then
      log "amp-upstream is running but logged no \"listening\" line within 60s:"
    else
      log "amp-upstream exited before listening:"
    fi
    cat "$WORK_DIR/amp-upstream.log"
    return 1
  fi
  url="http://$addr"

  "$amtool" alert add alertname=UpstreamCoreGate --alertmanager.url="$url" || { log "amtool alert add failed"; return 1; }
  "$amtool" alert query alertname=UpstreamCoreGate --alertmanager.url="$url" | grep -q UpstreamCoreGate ||
    { log "amtool alert query did not return the alert"; return 1; }
  "$amtool" config show --alertmanager.url="$url" >/dev/null || { log "amtool config show failed"; return 1; }
  "$amtool" silence add alertname=UpstreamCoreGate --comment="upstream-core-gate" --author="upstream-core-gate" --alertmanager.url="$url" >/dev/null ||
    { log "amtool silence add failed"; return 1; }
  "$amtool" silence query alertname=UpstreamCoreGate --alertmanager.url="$url" | grep -q UpstreamCoreGate ||
    { log "amtool silence query did not return the silence"; return 1; }

  # The harness sets Content-Type for the suites; check the divergence
  # itself here, so a bump that changes it shows up (baseline header).
  local code
  code="$(curl -s -o /dev/null -w '%{http_code}' -X POST -d '[]' "$url/api/v2/alerts")"
  if [[ "$code" != "415" ]]; then
    log "POST /api/v2/alerts without Content-Type answered $code, the baseline header says 415"
    return 1
  fi

  kill -TERM "$AMP_PID"
  if ! wait "$AMP_PID"; then
    log "amp-upstream did not exit cleanly on SIGTERM:"
    cat "$WORK_DIR/amp-upstream.log"
    AMP_PID=""
    return 1
  fi
  AMP_PID=""
  log "amtool: alert add/query, config show, silence add/query ok; 415 without Content-Type; clean SIGTERM"
}

check_suites || FAILED=1
check_amtool || FAILED=1

if [[ "$FAILED" -ne 0 ]]; then
  log "RESULT: FAIL"
  exit 1
fi
log "RESULT: PASS"
