#!/usr/bin/env bash
#
# Task 6.5 (alertmanager-parity, Phase 6) -- 2-replica HA e2e test.
#
# NOT part of the default `go test ./...` gate (no build tag ties it in,
# nothing under go-app/ imports it) -- run it explicitly:
#
#   ./deploy/e2e-ha/run.sh
#
# Requires Docker/Compose. Brings up redis + postgres + two AMP replicas
# (amp-a, amp-b) sharing both backends, then:
#
#   1. Waits for both replicas' /healthz.
#   2. Checks /api/v2/status on both: cluster.status == "ready" and
#      exactly 2 peers (task 6.5's heartbeat/status wiring).
#   3. POSTs one alert to replica A, waits group_wait (config.yaml: 5s)
#      plus a margin, then asserts EXACTLY ONE notification happened.
#   4. Kills replica A, POSTs a second (distinctly-named) alert to
#      replica B, and asserts B alone still delivers exactly once -- i.e.
#      the cluster survives losing a replica.
#   5. Tears the stack down (unless KEEP_UP=1).
#
# --- Why not a real webhook hit (substitution, investigated up front) ---
# Actual HTTP delivery in this codebase (internal/infrastructure/publishing)
# is driven exclusively by businesspublishing.DefaultTargetDiscoveryManager,
# which discovers publishing targets from live Kubernetes Secrets via
# k8s.NewK8sClient (rest.InClusterConfig() -- in-cluster only, no
# file/env/static fallback exists anywhere in the codebase). Outside a real
# k8s cluster this can only ever fall back to
# application.MetricsOnlyPublisher, which never makes an HTTP call -- see
# initializePublishingRuntime / initializePublishing in
# go-app/internal/application/publishing_runtime.go. config.yaml sets
# publishing.enabled: false explicitly to make that fallback deterministic
# (no in-cluster-detection delay/log noise) rather than incidental.
#
# So "exactly one notification arrived" is proven by two independent
# observable side effects of that same code path, neither requiring a real
# webhook hit:
#   - MetricsOnlyPublisher.PublishGroup logs exactly
#     "Group publishing skipped (metrics-only publisher)" once per
#     successful claim -> dedup -> "publish" pass -- note this log line
#     does NOT carry the alertname/group_key (see PublishGroup in
#     go-app/internal/application/publishing_metrics_only.go), so it is
#     counted per replica/time-window rather than grepped by alertname.
#     Each step below only has one alert in flight, so a bare count is an
#     exact, unambiguous signal.
#   - internal/infrastructure/grouping.RedisNotifyLog.RecordSent then
#     writes nflog:entry:<groupKey> in the shared Redis, keyed by the
#     EXACT group ("receiver=<name>/alertname=<alertname>") -- this is
#     the alert-specific corroboration the log-line count above can't
#     provide on its own.
#
# This is the fallback the brief calls for when webhook delivery is
# impossible outside k8s: nflog/redis state + logs standing in for a real
# webhook hit.
set -euo pipefail

COMPOSE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$COMPOSE_DIR"

COMPOSE=(docker compose -f docker-compose.yml)
PORT_A=18081
PORT_B=18082
KEEP_UP="${KEEP_UP:-0}"
GROUP_WAIT_MARGIN=10 # config.yaml route.group_wait is 5s

log() { printf '[e2e-ha] %s\n' "$1"; }
fail() { printf '[e2e-ha] FAIL: %s\n' "$1" >&2; exit 1; }

cleanup() {
  if [[ "$KEEP_UP" != "1" ]]; then
    log "tearing down stack"
    "${COMPOSE[@]}" down -v --remove-orphans >/dev/null 2>&1 || true
  else
    log "KEEP_UP=1 set -- leaving stack running (docker compose -f $COMPOSE_DIR/docker-compose.yml down -v to clean up)"
  fi
}
trap cleanup EXIT

wait_for_http() {
  local url="$1" tries="${2:-60}"
  for ((i = 0; i < tries; i++)); do
    if curl -fsS -o /dev/null "$url" 2>/dev/null; then
      return 0
    fi
    sleep 1
  done
  return 1
}

# publish_skip_count <service>: total occurrences of the metrics-only
# publisher's skip log line in that service's cumulative logs. Deliberately
# NOT filtered by alertname -- the log line itself carries no group_key
# (see the header comment) -- so callers must only invoke this when exactly
# one NEW alert could plausibly have triggered a fresh occurrence since the
# last time this was checked for that service.
publish_skip_count() {
  "${COMPOSE[@]}" logs "$1" 2>/dev/null | grep -c "Group publishing skipped" || true
}

nflog_entry_exists() {
  # nflog_entry_exists <receiver> <alertname>
  local key="nflog:entry:receiver=${1}/alertname=${2}"
  local redis_container
  redis_container="$("${COMPOSE[@]}" ps -q redis)"
  local hits
  hits=$(docker exec "$redis_container" redis-cli EXISTS "$key" | tr -d '\r')
  [[ "$hits" == "1" ]]
}

post_alert() {
  # post_alert <port> <alertname>
  local port="$1" alertname="$2" starts_at
  starts_at="$(date -u +%Y-%m-%dT%H:%M:%S.000Z)"
  curl -fsS -X POST "http://localhost:${port}/api/v2/alerts" \
    -H 'Content-Type: application/json' \
    -d "[{\"labels\":{\"alertname\":\"${alertname}\",\"severity\":\"warning\"},\"annotations\":{\"summary\":\"e2e-ha test\"},\"startsAt\":\"${starts_at}\"}]"
}

log "building + starting stack (redis, postgres, amp-a, amp-b)"
"${COMPOSE[@]}" up -d --build

log "waiting for both replicas' /healthz"
wait_for_http "http://localhost:${PORT_A}/healthz" 90 || fail "replica A never became healthy"
wait_for_http "http://localhost:${PORT_B}/healthz" 90 || fail "replica B never became healthy"
log "both replicas healthy"

# --- Step 2: cluster status shows 2 peers on both replicas ---------------
log "waiting for cluster heartbeat convergence (both replicas see 2 peers)"
converged=0
status_a=""
for ((i = 0; i < 30; i++)); do
  status_a="$(curl -fsS "http://localhost:${PORT_A}/api/v2/status" 2>/dev/null || echo '{}')"
  peers_a="$(printf '%s' "$status_a" | grep -o '"name"' | wc -l | tr -d ' ')"
  if [[ "$peers_a" -ge 2 ]]; then
    converged=1
    break
  fi
  sleep 1
done
[[ "$converged" == "1" ]] || fail "replica A's /api/v2/status never showed 2 peers (last: $status_a)"

status_b="$(curl -fsS "http://localhost:${PORT_B}/api/v2/status")"
peers_b="$(printf '%s' "$status_b" | grep -o '"name"' | wc -l | tr -d ' ')"
[[ "$peers_b" -ge 2 ]] || fail "replica B's /api/v2/status never showed 2 peers (got: $status_b)"

printf '%s' "$status_a" | grep -q '"status":"ready"' || fail "replica A cluster.status is not ready: $status_a"
printf '%s' "$status_b" | grep -q '"status":"ready"' || fail "replica B cluster.status is not ready: $status_b"
log "PASS: both replicas report cluster.status=ready with >=2 peers"

# --- Step 3: exactly one notification for an alert posted to replica A ---
ALERT_1="E2EHaTestAlertOne"
log "posting alert '$ALERT_1' to replica A"
post_alert "$PORT_A" "$ALERT_1" >/dev/null

log "waiting group_wait + margin (${GROUP_WAIT_MARGIN}s)"
sleep "$GROUP_WAIT_MARGIN"

matches_a=$(publish_skip_count amp-a)
matches_b=$(publish_skip_count amp-b)
total=$((matches_a + matches_b))
[[ "$total" -eq 1 ]] || fail "expected exactly 1 total publish-skip log across both replicas after posting $ALERT_1, got $total (a=$matches_a b=$matches_b)"
log "PASS: exactly one replica published '$ALERT_1' (metrics-only log line, a=$matches_a b=$matches_b)"

nflog_entry_exists "default" "$ALERT_1" || fail "expected nflog:entry:receiver=default/alertname=$ALERT_1 to exist in Redis"
log "PASS: nflog:entry:receiver=default/alertname=$ALERT_1 present in shared Redis -- RecordSent completed"

# --- Step 4: kill replica A; replica B alone still delivers exactly once -
log "killing replica A"
"${COMPOSE[@]}" kill amp-a >/dev/null

wait_for_http "http://localhost:${PORT_B}/healthz" 30 || fail "replica B unhealthy after replica A was killed"

ALERT_2="E2EHaTestAlertTwo"
log "posting alert '$ALERT_2' to replica B (replica A is dead)"
post_alert "$PORT_B" "$ALERT_2" >/dev/null

sleep "$GROUP_WAIT_MARGIN"

# amp-b's cumulative count was 0 before this step (amp-a alone published
# $ALERT_1) -- so this being exactly 1 now proves $ALERT_2 was published
# exactly once, with no double-publish on B alone.
matches_b2=$(publish_skip_count amp-b)
[[ "$matches_b2" -eq 1 ]] || fail "expected replica B alone to publish exactly once after A's death, got $matches_b2 cumulative"
nflog_entry_exists "default" "$ALERT_2" || fail "expected nflog:entry:receiver=default/alertname=$ALERT_2 to exist in Redis"
log "PASS: replica B alone still delivers exactly once after replica A's death (failover)"

log "ALL PASS"
