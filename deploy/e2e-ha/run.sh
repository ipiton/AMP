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
#   3. POSTs one alert to replica A, waits group_wait (config.yaml: 8s)
#      plus a margin, then asserts EXACTLY ONE notification happened.
#   4. CROSS-REPLICA CONCURRENT FIRE: restarts replica B after ingest so its
#      RestoreTimers arms a LOCAL timer for a group replica A also holds a
#      timer for -- both then fire at the same group_interval instant, and
#      exactly one publish must get through.
#   5. ORPHAN ADOPTION: POSTs an alert to A and kills A BEFORE group_wait
#      expires, so the timer is left in shared Redis with no owner. Replica
#      B's reconciliation loop must adopt it and publish.
#   6. POSTs a further alert to replica B (A still dead) and asserts B alone
#      still delivers exactly once -- the cluster survives losing a replica.
#   7. Tears the stack down (unless KEEP_UP=1).
#
# Steps 4 and 5 were added by final review finding 9. Before them, step 3's
# "exactly once" was trivially true (only ONE replica ever held a timer for
# the group, so there was nothing to arbitrate) and the "failover" step only
# proved that a surviving replica can serve a BRAND NEW alert -- never that
# work already in flight on the dead replica gets picked up.
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
# So "exactly one replica reached delivery" is proven by two independent
# observable side effects of that same code path, neither requiring a real
# webhook hit:
#   - DefaultGroupManager.publishGroupAlerts logs "group notification not
#     delivered ..." exactly once per successful claim -> dedup -> publish
#     pass, WITH group_key ("receiver=<name>/alertname=<alertname>"), so it
#     is an exact per-alert signal. The claim loser returns before the
#     publish step, so only the claim winner can emit it.
#     (MetricsOnlyPublisher's own "Group publishing skipped" line is also
#     counted, but it carries no group_key -- see PublishGroup in
#     go-app/internal/application/publishing_metrics_only.go -- so it only
#     works as a bare per-replica count.)
#   - The SHARED nflog entry (nflog:entry:<groupKey>) must be ABSENT.
#     Final review finding 4: MetricsOnlyPublisher.PublishGroup used to
#     return nil, which publishGroupAlerts read as "delivered" and recorded
#     via RedisNotifyLog.RecordSent with TTL = repeat_interval -- silencing
#     the group on every HEALTHY replica for that entire interval. It now
#     returns grouping.ErrDeliveryNotConfirmed, so nothing is recorded, and
#     this script asserts the key does NOT exist.
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
GROUP_WAIT_MARGIN=14     # config.yaml route.group_wait is 8s
GROUP_INTERVAL=30        # config.yaml route.group_interval
RECONCILE_MARGIN=20      # reconciliation_interval 5s + grace 3s, plus slack

log() { printf '[e2e-ha] %s\n' "$1"; }
fail() { printf '[e2e-ha] FAIL: %s\n' "$1" >&2; exit 1; }

# Pick a JSON parser for peer_count() up front (not inside the function
# itself): peer_count runs inside a `$(...)` command substitution, and an
# `exit` there would only kill that subshell, not the script -- so a
# missing-parser failure has to be caught here, in the main shell, instead.
if command -v jq >/dev/null 2>&1; then
  JSON_PARSER="jq"
elif command -v python3 >/dev/null 2>&1; then
  JSON_PARSER="python3"
else
  fail "neither jq nor python3 available to parse cluster.peers -- install one (this script refuses to fall back to a substring-count heuristic that already caused a false-positive bug)"
fi

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

peer_count() {
  # peer_count <status_json>: exact count of cluster.peers[], via whichever
  # parser JSON_PARSER picked above. Deliberately NOT a
  # `grep -o '"name"' | wc -l` over the whole payload -- that also matches
  # the top-level cluster.name field, so with exactly 1 real peer it
  # miscounts 2 and a `-ge 2` check passed on that false positive (fix
  # round 1 finding). On parse failure (malformed/empty JSON) both paths
  # print 0 rather than erroring, so callers' `-eq 2` checks fail loudly
  # with a real assertion message instead of a raw parser stack trace.
  local json="$1"
  if [[ "$JSON_PARSER" == "jq" ]]; then
    printf '%s' "$json" | jq -r '(.cluster.peers // []) | length' 2>/dev/null || echo 0
  else
    printf '%s' "$json" | python3 -c '
import json, sys
try:
    data = json.load(sys.stdin)
except Exception:
    print(0)
else:
    print(len((data.get("cluster") or {}).get("peers") or []))
' 2>/dev/null || echo 0
  fi
}

nflog_entry_exists() {
  # nflog_entry_exists <receiver> <alertname>
  #
  # NOTE (final review finding 4): with a metrics-only publisher this key must
  # NOT exist. MetricsOnlyPublisher.PublishGroup now returns
  # grouping.ErrDeliveryNotConfirmed instead of nil, so publishGroupAlerts
  # deliberately skips RecordSent — nothing was delivered, so nothing may be
  # recorded in the SHARED cross-replica notification log (TTL =
  # repeat_interval), which would otherwise silence the group on every healthy
  # replica. This helper is therefore asserted NEGATIVELY below; it is kept
  # (rather than deleted) precisely so a regression that starts writing the key
  # again fails loudly.
  #
  # Key format changed (task fwb, alertmanager-parity wave 2: per-target nflog
  # granularity): entries are now "nflog:entry:<groupKey>:<target>", one per
  # target that confirmed delivery, instead of one bare "nflog:entry:<groupKey>"
  # key covering the whole group+receiver. This checks a wildcard/prefix match
  # rather than an exact key so the assertion still catches a regression
  # regardless of which target segment would be appended.
  local prefix="nflog:entry:receiver=${1}/alertname=${2}"
  local redis_container
  redis_container="$("${COMPOSE[@]}" ps -q redis)"
  local hits
  hits=$(docker exec "$redis_container" redis-cli --scan --pattern "${prefix}*" | wc -l | tr -d ' \r')
  [[ "$hits" -gt 0 ]]
}

log_count() {
  # log_count <service> <pattern>: occurrences of a fixed pattern in that
  # service's cumulative logs.
  #
  # Always `grep -c`, never `grep -q`: under `set -o pipefail`, grep -q exits
  # on the first match and closes the pipe, so `docker compose logs` dies of
  # SIGPIPE (141) and the whole pipeline reports failure even though the
  # pattern WAS found. That bit this script's first version of the step-4
  # assertion. grep -c drains the input, and `|| true` absorbs the no-match
  # exit status.
  "${COMPOSE[@]}" logs "$1" 2>/dev/null | grep -c "$2" || true
}

undelivered_count() {
  # undelivered_count <service> <alertname>: how many times <service> reached
  # the publish step for this specific group and reported "delivered nothing"
  # (the metrics-only outcome). Unlike publish_skip_count, this log line comes
  # from DefaultGroupManager.publishGroupAlerts and DOES carry group_key, so it
  # is an exact per-alert signal — the claim loser returns before publishing, so
  # only the replica that won the nflog claim can emit it.
  "${COMPOSE[@]}" logs "$1" 2>/dev/null |
    grep "group notification not delivered" |
    grep -c "receiver=default/alertname=${2}" || true
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

# --- Step 2: cluster status shows exactly 2 peers on both replicas -------
log "waiting for cluster heartbeat convergence (both replicas see 2 peers), using $JSON_PARSER"
converged=0
status_a=""
for ((i = 0; i < 30; i++)); do
  status_a="$(curl -fsS "http://localhost:${PORT_A}/api/v2/status" 2>/dev/null || echo '{}')"
  peers_a="$(peer_count "$status_a")"
  if [[ "$peers_a" -eq 2 ]]; then
    converged=1
    break
  fi
  sleep 1
done
[[ "$converged" == "1" ]] || fail "replica A's /api/v2/status never showed exactly 2 peers (last count: $peers_a, body: $status_a)"

status_b="$(curl -fsS "http://localhost:${PORT_B}/api/v2/status")"
peers_b="$(peer_count "$status_b")"
[[ "$peers_b" -eq 2 ]] || fail "replica B's /api/v2/status never showed exactly 2 peers (got: $peers_b, body: $status_b)"

printf '%s' "$status_a" | grep -q '"status":"ready"' || fail "replica A cluster.status is not ready: $status_a"
printf '%s' "$status_b" | grep -q '"status":"ready"' || fail "replica B cluster.status is not ready: $status_b"
log "PASS: both replicas report cluster.status=ready with exactly 2 peers"

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

# Exactly one replica reached the publish step FOR THIS GROUP (group_key-scoped,
# unlike publish_skip_count above).
undelivered_a=$(undelivered_count amp-a "$ALERT_1")
undelivered_b=$(undelivered_count amp-b "$ALERT_1")
undelivered_total=$((undelivered_a + undelivered_b))
[[ "$undelivered_total" -eq 1 ]] || fail "expected exactly 1 replica to reach the publish step for $ALERT_1, got $undelivered_total (a=$undelivered_a b=$undelivered_b)"
log "PASS: exactly one replica reached the publish step for '$ALERT_1' (a=$undelivered_a b=$undelivered_b)"

# Finding 4: a metrics-only publish delivered NOTHING, so it must NOT record a
# shared nflog entry. Before the fix it did, and every healthy replica then
# skipped this group for a full repeat_interval.
if nflog_entry_exists "default" "$ALERT_1"; then
  fail "nflog:entry:receiver=default/alertname=$ALERT_1 exists, but nothing was delivered -- a metrics-only publish must never record a send (finding 4)"
fi
log "PASS: no nflog:entry recorded for '$ALERT_1' -- metrics-only publish did not poison the shared dedup log"

# --- Step 4: CROSS-REPLICA CONCURRENT FIRE (finding 9a) -----------------
# Step 3 proved "exactly one publish" while only ONE replica held a timer for
# the group -- trivially true, nothing to arbitrate. Force the real race:
# replica A published '$ALERT_1' at group_wait and armed a group_interval
# continuation in SHARED Redis. Restarting replica B makes its RestoreTimers
# load that same entry and arm its OWN local Go timer for it, so both replicas
# fire the same group at the same instant. Exactly one publish must get
# through (RedisTimerStorage.AcquireLock + the nflog TryClaim).
restores_before=$(log_count amp-b "Timer restoration completed")
log "restarting replica B so RestoreTimers arms a local timer for '$ALERT_1' on BOTH replicas"
"${COMPOSE[@]}" restart amp-b >/dev/null
wait_for_http "http://localhost:${PORT_B}/healthz" 60 || fail "replica B never became healthy after restart"

restores_after=$(log_count amp-b "Timer restoration completed")
[[ "$restores_after" -gt "$restores_before" ]] ||
  fail "replica B did not re-run RestoreTimers after restart (before=$restores_before after=$restores_after) -- the concurrent-fire scenario would be vacuous"

restored_one=$("${COMPOSE[@]}" logs amp-b 2>/dev/null | grep "Timer restoration completed" | tail -1)
printf '%s' "$restored_one" | grep -q '"restored":0' &&
  fail "replica B restored 0 timers ($restored_one) -- it does not hold a local timer for '$ALERT_1', so the concurrent-fire scenario is vacuous"
log "replica B restored timers from shared Redis: $restored_one"

log "waiting for group_interval (${GROUP_INTERVAL}s) so both replicas' timers for '$ALERT_1' fire together"
sleep "$GROUP_INTERVAL"

concurrent_a=$(undelivered_count amp-a "$ALERT_1")
concurrent_b=$(undelivered_count amp-b "$ALERT_1")
concurrent_total=$((concurrent_a + concurrent_b))
# 1 from step 3's group_wait fire + exactly 1 from the group_interval fire that
# BOTH replicas raced. A double publish shows up as 3.
[[ "$concurrent_total" -eq 2 ]] || fail "expected exactly 2 cumulative publishes for $ALERT_1 (group_wait + one arbitrated group_interval), got $concurrent_total (a=$concurrent_a b=$concurrent_b)"
log "PASS: both replicas held a timer for '$ALERT_1' and exactly one publish got through (a=$concurrent_a b=$concurrent_b)"

# --- Step 5: ORPHAN ADOPTION after the owner dies (finding 9b) ----------
# POST to replica A and kill A BEFORE group_wait expires, so the group_wait
# timer is left in shared Redis with no live owner and no local handle
# anywhere. Nothing re-arms it: AddAlertToGroup only arms group_wait for BRAND
# NEW groups, and RestoreTimers is startup-only. Replica B's reconciliation
# loop is the only thing that can save this group -- and it could not before
# finding 2's fix, because the adoption grace equalled the storage TTL grace.
ALERT_3="E2EHaTestAlertAdopted"
log "posting alert '$ALERT_3' to replica A, then killing A before group_wait expires"
post_alert "$PORT_A" "$ALERT_3" >/dev/null
"${COMPOSE[@]}" kill amp-a >/dev/null
log "replica A killed"

wait_for_http "http://localhost:${PORT_B}/healthz" 30 || fail "replica B unhealthy after replica A was killed"

# A must NOT have published: it died inside group_wait.
adopted_a=$(undelivered_count amp-a "$ALERT_3")
[[ "$adopted_a" -eq 0 ]] || fail "replica A published $ALERT_3 before dying ($adopted_a) -- the kill was too slow, the adoption scenario is vacuous (raise route.group_wait)"

log "waiting for replica B's reconciliation loop to adopt the orphaned timer (${RECONCILE_MARGIN}s)"
sleep "$RECONCILE_MARGIN"

adopted_b=$(undelivered_count amp-b "$ALERT_3")
[[ "$adopted_b" -eq 1 ]] || fail "expected replica B to adopt the orphaned timer for $ALERT_3 and publish exactly once, got $adopted_b (finding 2: the reconciliation adoption window was ~0s)"
adoptions=$(log_count amp-b "reconciliation: adopting orphaned group timer")
[[ "$adoptions" -ge 1 ]] ||
  fail "replica B published $ALERT_3 but never logged an adoption -- the assertion is not proving adoption"
log "PASS: replica B adopted the dead replica's in-flight timer and published '$ALERT_3'"

# --- Step 6: replica B alone still delivers exactly once ----------------
# Replica A is already dead (killed in step 5); this step is about a BRAND NEW
# alert ingested by the survivor, which is a different property from step 5's
# adoption of work already in flight.
ALERT_2="E2EHaTestAlertTwo"
log "posting alert '$ALERT_2' to replica B (replica A is dead)"
post_alert "$PORT_B" "$ALERT_2" >/dev/null

sleep "$GROUP_WAIT_MARGIN"

# group_key-scoped, so this is exact regardless of what else B has published.
undelivered_b2=$(undelivered_count amp-b "$ALERT_2")
[[ "$undelivered_b2" -eq 1 ]] || fail "expected replica B alone to publish exactly once for $ALERT_2 after A's death, got $undelivered_b2"
if nflog_entry_exists "default" "$ALERT_2"; then
  fail "nflog:entry:receiver=default/alertname=$ALERT_2 exists, but nothing was delivered -- a metrics-only publish must never record a send (finding 4)"
fi
log "PASS: replica B alone still delivers exactly once after replica A's death (failover)"

log "ALL PASS"
