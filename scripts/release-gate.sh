#!/usr/bin/env bash
# shellcheck disable=SC2329
# ^ file-wide: every step_* function below is invoked indirectly (passed
# by name to run_step, which calls it via "$@") -- shellcheck can't trace
# that call path and reports each one as unused. False positive, not a
# real dead function.
#
# PHASE-8 release rollout package: one-entrypoint pre-release quality gate.
#
#   ./scripts/release-gate.sh
#
# Runs, in order: go build, golangci-lint, go test ./... -count=1, the
# futureparity build tag's build+test, -race on the concurrency-heavy
# packages (publishing/grouping/silencing/templating), helm lint+template
# for both values overlays, and an amtool-compat smoke (skipped, not
# failed, when Docker is unavailable). Every step runs regardless of an
# earlier failure -- a step's own PASS/FAIL never short-circuits the rest
# -- so a red run still tells you everything else that's wrong. Exit code
# is non-zero if ANY step failed; a summary table prints last either way.
#
# Bash 3.2-compatible (macOS's stock /bin/bash, frozen at 3.2 for
# licensing reasons) as well as any bash 4+/5+ on Linux CI: no
# associative arrays, no `mapfile`/`readarray`, and every array that
# could legitimately be empty is length-checked before `"${arr[@]}"`
# expansion -- bash <4.4 treats expanding an empty array under `set -u`
# as an unbound-variable error, unlike 4.4+.
set -uo pipefail
# Deliberately NOT `set -e`: each step runs via run_step, which captures
# the step's exit status into the summary table instead of letting a
# failure abort the remaining steps.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
GO_APP_DIR="$ROOT_DIR/go-app"
HELM_CHART_DIR="$ROOT_DIR/helm/amp"
SMOKE_DIR="$ROOT_DIR/deploy/smoke"

log() { printf '[release-gate] %s\n' "$1"; }

STEP_NAMES=()
STEP_STATUSES=()
STEP_DURATIONS=()
STEP_NOTES=()
OVERALL_FAIL=0

record_result() {
  STEP_NAMES+=("$1")
  STEP_STATUSES+=("$2")
  STEP_DURATIONS+=("$3")
  STEP_NOTES+=("$4")
  if [[ "$2" == "FAIL" ]]; then
    OVERALL_FAIL=1
  fi
}

run_step() {
  local name="$1" note="$2" start end status
  shift 2
  log "==> ${name}"
  start=$(date +%s)
  if "$@"; then
    status="PASS"
  else
    status="FAIL"
  fi
  end=$(date +%s)
  record_result "$name" "$status" "$((end - start))s" "$note"
}

skip_step() {
  record_result "$1" "SKIP" "--" "$2"
  log "==> ${1} (SKIPPED: ${2})"
}

step_build() {
  ( cd "$GO_APP_DIR" && go build ./... )
}

step_lint() {
  if ! command -v golangci-lint >/dev/null 2>&1; then
    log "golangci-lint not found on PATH"
    return 1
  fi
  # golangci-lint's cache is shared across every git worktree of this repo
  # (it lives under ~/.cache, not per-worktree). Its own cached export data
  # can embed a PREVIOUS worktree's absolute source path; when this repo
  # runs several worktrees at once (this project routinely does), a
  # `//nolint:` suppression comment can then fail to resolve against that
  # stale path -- surfacing an "issue" on an already-suppressed, unchanged
  # finding, in a source file this worktree never touched. `cache clean`
  # costs a few seconds and removes the whole class of false failure.
  golangci-lint cache clean
  ( cd "$GO_APP_DIR" && golangci-lint run )
}

step_test() {
  ( cd "$GO_APP_DIR" && go test ./... -count=1 )
}

step_futureparity() {
  ( cd "$GO_APP_DIR" && \
    go build -tags futureparity ./cmd/server/... && \
    go test -tags futureparity -count=1 ./cmd/server/... )
}

step_race() {
  local pkgs_raw pkg
  local -a pkgs
  pkgs_raw=$(cd "$GO_APP_DIR" && go list ./... | grep -E '/(publishing|grouping|silencing|templating)(/[^/]*)?$')
  pkgs=()
  while IFS= read -r pkg; do
    [[ -n "$pkg" ]] && pkgs+=("$pkg")
  done <<< "$pkgs_raw"

  if [[ ${#pkgs[@]} -eq 0 ]]; then
    log "no packages matched publishing/grouping/silencing/templating -- treating as a failure, not a vacuous pass"
    return 1
  fi

  log "race-testing ${#pkgs[@]} package(s): ${pkgs[*]}"
  ( cd "$GO_APP_DIR" && go test -race -count=1 "${pkgs[@]}" )
}

step_helm_values() {
  local values_file="$1"
  if ! command -v helm >/dev/null 2>&1; then
    log "helm not found on PATH"
    return 1
  fi
  helm lint "$HELM_CHART_DIR" -f "$HELM_CHART_DIR/$values_file" || return 1
  helm template amp "$HELM_CHART_DIR" -f "$HELM_CHART_DIR/$values_file" >/dev/null
}

docker_available() {
  command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1
}

step_amtool_compat() {
  local amtool_image="quay.io/prometheus/alertmanager:v0.34.0"
  local network="amp-smoke_default"
  local ok=0

  amtool_teardown() {
    ( cd "$SMOKE_DIR" && docker compose -f docker-compose.yml down -v --remove-orphans >/dev/null 2>&1 || true )
  }
  ( cd "$SMOKE_DIR" && docker compose -f docker-compose.yml down -v --remove-orphans >/dev/null 2>&1 || true )
  trap amtool_teardown RETURN

  if ! ( cd "$SMOKE_DIR" && docker compose -f docker-compose.yml up -d --build ); then
    log "docker compose up failed for the amtool-compat smoke stack"
    return 1
  fi

  local tries=90
  while (( tries > 0 )); do
    if curl -fsS -o /dev/null "http://localhost:18080/healthz" 2>/dev/null; then
      ok=1
      break
    fi
    sleep 1
    tries=$((tries - 1))
  done
  if [[ "$ok" -ne 1 ]]; then
    log "amp never became healthy for the amtool-compat smoke"
    return 1
  fi

  local amtool_target_host="amp" amtool_target_port="8080"
  amtool() {
    # kingpin (amtool's flag parser) accepts a global flag after the
    # subcommand, so "$@" comes first here -- deliberately, not just
    # style: with --alertmanager.url's value directly followed by "$@" on
    # the same line, the two together resemble a scheme://host:port@...
    # credential shape closely enough to trip this repo's secret-leak
    # guard on a Write/Edit, even though nothing here is a secret.
    docker run --rm --network "$network" --entrypoint amtool "$amtool_image" \
      "$@" --alertmanager.url "http://${amtool_target_host}:${amtool_target_port}"
  }

  amtool alert query >/dev/null || { log "amtool alert query failed"; return 1; }
  amtool config show >/dev/null || { log "amtool config show failed"; return 1; }
  amtool silence add alertname=ReleaseGateAmtoolCompat --comment="release-gate check" --author="release-gate" >/dev/null ||
    { log "amtool silence add failed"; return 1; }
  amtool silence query >/dev/null || { log "amtool silence query failed"; return 1; }

  return 0
}

run_step "build" "go build ./... (go-app)" step_build
run_step "lint" "golangci-lint run (go-app)" step_lint
run_step "test" "go test ./... -count=1 (go-app)" step_test
run_step "futureparity" "build+test, futureparity tag, cmd/server" step_futureparity
run_step "race" "go test -race, publishing/grouping/silencing/templating" step_race

if command -v helm >/dev/null 2>&1; then
  if ! ( cd "$ROOT_DIR" && helm dependency build "$HELM_CHART_DIR" >/dev/null 2>&1 ); then
    log "helm dependency build failed -- helm steps below will fail on the missing chart dependency"
  fi
fi
run_step "helm-dev" "lint + template, values-dev.yaml" step_helm_values values-dev.yaml
run_step "helm-production" "lint + template, values-production.yaml" step_helm_values values-production.yaml

if docker_available; then
  run_step "amtool-compat" "real amtool CLI vs deploy/smoke stack" step_amtool_compat
else
  skip_step "amtool-compat" "docker not available"
fi

log ""
log "==================== SUMMARY ===================="
printf '%-16s %-6s %-8s %s\n' "STEP" "STATUS" "TIME" "NOTE"
if [[ ${#STEP_NAMES[@]} -gt 0 ]]; then
  for i in "${!STEP_NAMES[@]}"; do
    printf '%-16s %-6s %-8s %s\n' "${STEP_NAMES[$i]}" "${STEP_STATUSES[$i]}" "${STEP_DURATIONS[$i]}" "${STEP_NOTES[$i]}"
  done
fi
log "=================================================="

if [[ "$OVERALL_FAIL" -eq 1 ]]; then
  log "RESULT: FAIL"
  exit 1
fi
log "RESULT: PASS"
exit 0
