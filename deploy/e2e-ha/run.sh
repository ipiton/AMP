#!/usr/bin/env bash
#
# Two-replica HA e2e test of the upstream Alertmanager core (ADR-019,
# UPSTREAM-HA-MEMBERLIST): two /app/amp-upstream containers form one
# memberlist gossip cluster, a webhook sink records every notification.
#
# NOT part of the default `go test ./...` gate -- run it explicitly:
#
#   ./deploy/e2e-ha/run.sh
#
# Requires Docker/Compose, curl and jq. Steps:
#
#   1. Both replicas answer /-/ready.
#   2. /api/v2/status on both: cluster.status == "ready", exactly 2 peers.
#   3. A silence created through A becomes visible on B (silence gossip).
#   4. One alert POSTed to A and to B -> exactly one notification in the
#      sink (the shared notification log; B waits peer-timeout first).
#   5. An alert the silence covers, POSTed to both -> no notification.
#   6. Kill A, POST a new alert to B -> exactly one notification.
#   7. Restart B while A is dead -> the silence from step 3 is still there,
#      read back from B's snapshot on its data volume.
#   8. Tear the stack down (unless KEEP_UP=1).
#
# Every wait polls with a deadline instead of sleeping a fixed time; "exactly
# one" and "none" then hold for a quiet period longer than the peer timeout
# (docker-compose.yml: 5s), so a late duplicate from the second replica
# would be counted.
set -euo pipefail

COMPOSE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$COMPOSE_DIR"

COMPOSE=(docker compose -f docker-compose.yml)
URL_A="http://localhost:18081"
URL_B="http://localhost:18082"
KEEP_UP="${KEEP_UP:-0}"
QUIET=12   # > group_wait 2s + peer-timeout 5s, plus slack

log() { printf '[e2e-ha] %s\n' "$1"; }
fail() {
  printf '[e2e-ha] FAIL: %s\n' "$1" >&2
  "${COMPOSE[@]}" logs --tail=40 >&2 2>/dev/null || true
  exit 1
}

for tool in docker curl jq; do
  command -v "$tool" >/dev/null 2>&1 || { log "$tool is required"; exit 1; }
done

cleanup() {
  if [[ "$KEEP_UP" != "1" ]]; then
    log "tearing down stack"
    "${COMPOSE[@]}" down -v --remove-orphans >/dev/null 2>&1 || true
  else
    log "KEEP_UP=1 -- stack left running (docker compose -f $COMPOSE_DIR/docker-compose.yml down -v)"
  fi
}
trap cleanup EXIT

# wait_until <seconds> <command...>: polls once a second until the command
# succeeds; fails with the deadline otherwise.
wait_until() {
  local deadline=$((SECONDS + $1))
  shift
  until "$@"; do
    ((SECONDS < deadline)) || return 1
    sleep 1
  done
}

ready() { curl -fsS -o /dev/null "$1/-/ready" 2>/dev/null; }

cluster_formed() {
  local body
  body="$(curl -fsS "$1/api/v2/status" 2>/dev/null)" || return 1
  [[ "$(jq -r '.cluster.status' <<<"$body")" == "ready" ]] &&
    [[ "$(jq -r '.cluster.peers | length' <<<"$body")" == "2" ]]
}

# sink_count <alertname>: notifications the sink received whose only alert
# is <alertname>. `grep -c` drains the pipe (grep -q would SIGPIPE `docker
# compose logs` under pipefail); `|| true` absorbs the no-match status.
sink_count() {
  "${COMPOSE[@]}" logs --no-log-prefix sink 2>/dev/null | grep -c "^SINK $1\$" || true
}
sink_at_least() { (($(sink_count "$1") >= $2)); }

post_alert() {
  # post_alert <url> <alertname>
  curl -fsS -o /dev/null -X POST "$1/api/v2/alerts" -H 'Content-Type: application/json' \
    -d "[{\"labels\":{\"alertname\":\"$2\",\"severity\":\"warning\"},\"annotations\":{\"summary\":\"e2e-ha\"}}]"
}

# has_silence <url> <id>: the silence exists and is active on that replica.
has_silence() {
  curl -fsS "$1/api/v2/silence/$2" 2>/dev/null | jq -e '.status.state == "active"' >/dev/null
}

# expect_notifications <alertname> <n>: waits for n notifications (n > 0),
# then checks the count stays at n for $QUIET seconds.
expect_notifications() {
  local name="$1" want="$2" got
  if ((want > 0)); then
    wait_until 30 sink_at_least "$name" "$want" || fail "$name: fewer than $want notification(s) within 30s (got $(sink_count "$name"))"
  fi
  sleep "$QUIET"
  got="$(sink_count "$name")"
  [[ "$got" == "$want" ]] || fail "$name: expected exactly $want notification(s), got $got"
}

log "building and starting the stack (sink, amp-a, amp-b)"
"${COMPOSE[@]}" up -d --build

# --- 1. readiness ----------------------------------------------------------
wait_until 90 ready "$URL_A" || fail "replica A never answered /-/ready"
wait_until 90 ready "$URL_B" || fail "replica B never answered /-/ready"
log "PASS 1: both replicas ready"

# --- 2. cluster ------------------------------------------------------------
wait_until 60 cluster_formed "$URL_A" || fail "replica A: cluster not ready with 2 peers: $(curl -s "$URL_A/api/v2/status" | jq -c .cluster)"
wait_until 60 cluster_formed "$URL_B" || fail "replica B: cluster not ready with 2 peers: $(curl -s "$URL_B/api/v2/status" | jq -c .cluster)"
log "PASS 2: cluster.status=ready with 2 peers on both replicas"

# --- 3. silence gossip -----------------------------------------------------
SILENCED="E2EHaSilenced"
now="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
silence_id="$(curl -fsS -X POST "$URL_A/api/v2/silences" -H 'Content-Type: application/json' \
  -d "{\"matchers\":[{\"name\":\"alertname\",\"value\":\"$SILENCED\",\"isRegex\":false,\"isEqual\":true}],\"startsAt\":\"$now\",\"endsAt\":\"2099-01-01T00:00:00Z\",\"createdBy\":\"e2e-ha\",\"comment\":\"e2e-ha\"}" |
  jq -r .silenceID)"
[[ -n "$silence_id" && "$silence_id" != "null" ]] || fail "creating the silence on A returned no id"
wait_until 20 has_silence "$URL_B" "$silence_id" || fail "silence $silence_id created on A never became visible on B"
log "PASS 3: silence $silence_id created on A is active on B"

# --- 4. one notification for an alert sent to both replicas ----------------
BOTH="E2EHaBoth"
post_alert "$URL_A" "$BOTH"
post_alert "$URL_B" "$BOTH"
expect_notifications "$BOTH" 1
log "PASS 4: '$BOTH' sent to both replicas, exactly one notification"

# --- 5. a silenced alert sent to both replicas -----------------------------
post_alert "$URL_A" "$SILENCED"
post_alert "$URL_B" "$SILENCED"
expect_notifications "$SILENCED" 0
log "PASS 5: '$SILENCED' sent to both replicas, no notification"

# --- 6. failover -----------------------------------------------------------
"${COMPOSE[@]}" kill amp-a >/dev/null
SURVIVOR="E2EHaSurvivor"
post_alert "$URL_B" "$SURVIVOR"
expect_notifications "$SURVIVOR" 1
log "PASS 6: replica A killed, '$SURVIVOR' sent to B, exactly one notification"

# --- 7. snapshot survives a restart ----------------------------------------
# SIGTERM writes the silences snapshot to B's data volume; with A dead, the
# restarted B can only get the silence back from that file.
"${COMPOSE[@]}" restart amp-b >/dev/null
wait_until 60 ready "$URL_B" || fail "replica B never answered /-/ready after the restart"
has_silence "$URL_B" "$silence_id" || fail "silence $silence_id is gone after B restarted with A dead (snapshot not restored)"
log "PASS 7: B restarted with A dead and still has silence $silence_id"

log "ALL PASS"
