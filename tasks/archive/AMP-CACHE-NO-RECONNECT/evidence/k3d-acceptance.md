# k3d acceptance: AMP-CACHE-NO-RECONNECT

2026-10-22, k3d v5.9.0, k3s v1.35.5, single node, cluster `ncr` (traefik off), namespace `ncr`, release `amp`. Images built locally and imported (`image.pullPolicy: Never`): `amp:ncr-main` from `git archive main` (`b30a015`), `amp:ncr` from the branch (`41cb924`). Values: `monitoring.prometheusEnabled=false`, `postgresql.monitoring.prometheusRules.enabled=false`, everything else chart defaults (standard profile, chart Valkey).

The race of a `helm upgrade` that restarts both pods is reproduced deterministically without deleting anything: `kubectl scale statefulset amp-redis --replicas=0`, `kubectl rollout restart deploy/amp`, sleep N s, `scale --replicas=1`. The new AMP pod starts while Valkey is down. Log lines are from the newest AMP pod, container `amp`; `ERROR lines` counts `Failed to connect to Redis`.

## A0 — main: chart and image of `main`

Install: `Redis cache initialized` on the first try (Valkey was already up when AMP passed PostgreSQL; AMP restarted 5 times waiting for PostgreSQL — pre-existing, unrelated).

Race, Valkey back 20 s after the AMP restart:

```text
valkey down at 11:29:23, scaled up at 11:29:44
pod amp-8485f75457-xbqgr (2026-10-22T11:29:24Z)
11:29:26.47 INFO  Initializing cache backend...
11:29:29.69 ERROR Failed to connect to Redis  dial tcp 10.43.225.3:6379: connect: connection refused
11:29:29.69 WARN  Redis cache unavailable, falling back to in-memory cache
11:29:29.69 WARN  No Redis cache backend, silence GC leader election disabled …
11:29:29.69 WARN  Standard profile without a Redis cache backend, cross-replica silence sync disabled
11:29:29.69 WARN  No Redis cache backend, cluster heartbeat disabled …
ERROR lines: 1
```

Valkey was Ready 20 s later; the pod stayed on the in-memory cache (the bug).

## A1 — branch: upgrade to the branch chart and image, then the same race

Upgrade (REVISION 2): NOTES show the new text (`redis.startup_wait (60s)`); `Redis cache initialized … "attempts":1`, 0 ERROR lines.

Race, Valkey back 20 s after the AMP restart:

```text
valkey down at 11:30:32, scaled up at 11:30:52
pod amp-94b9ffd9-xtq2v (2026-10-22T11:30:32Z)
11:30:32.97 INFO  Initializing cache backend...
11:30:36.18 ERROR Failed to connect …  connection refused
11:30:36.18 WARN  Redis cache not reachable yet, retrying  attempt=1 retry_in=1s
11:30:42.19 ERROR Failed to connect …  i/o timeout
11:30:42.19 WARN  … retrying  attempt=2 retry_in=2s
11:30:49.19 ERROR Failed to connect …  i/o timeout
11:30:49.19 WARN  … retrying  attempt=3 retry_in=4s
11:30:57.16 ERROR Failed to connect …  connection refused
11:30:57.16 WARN  … retrying  attempt=4 retry_in=5s
11:31:06.26 ERROR Failed to connect …  connection refused
11:31:06.26 WARN  … retrying  attempt=5 retry_in=5s
11:31:11.27 INFO  Connected to Redis
11:31:11.27 INFO  Redis cache initialized  attempts=6
ERROR lines: 5
valkey: amp:cluster:peer:amp-94b9ffd9-xtq2v-fdac4fce present (heartbeat of the new pod), dbsize 2
```

Waited 38.3 s (11:30:32.97 → 11:31:11.27), no manual restart. One attempt takes 3–5 s here: `connection refused` ~3.2 s (go-redis dial retries, `max_retries: 3`), `i/o timeout` ~5 s (PING timeout).

## A2 — branch: Valkey down longer than the window

Race, Valkey back 90 s after the AMP restart:

```text
valkey down at 11:31:24, scaled up at 11:32:54
pod amp-545c544767-wcnsb (2026-10-22T11:31:25Z)
11:31:27.05 INFO  Initializing cache backend...
… 8 × ERROR Failed to connect (refused / i/o timeout), 7 × WARN retrying …
11:32:27.34 WARN  Redis cache unavailable, falling back to in-memory cache  attempts=8 waited=60.285s
11:32:27.34 WARN  No Redis cache backend, … (same three lines as on main)
ERROR lines: 8
```

Fallback after the window, overrun 0.3 s. `kubectl rollout restart deploy/amp` (the NOTES hint): `Redis cache initialized attempts=1`.

## A3 — branch: `cache.enabled=false`

```text
env: REDIS_ADDR=localhost:6379, REDIS_STARTUP_WAIT=0s
11:33:42.14 INFO  Initializing cache backend...
11:33:43.28 ERROR Failed to connect …  dial tcp [::1]:6379: connect: connection refused
11:33:43.28 WARN  Redis cache unavailable, falling back to in-memory cache  attempts=1 waited=1.141s
ERROR lines: 1
```

No wait, same as `main`.

## Observations

- Durations in the JSON log are nanoseconds (`"retry_in":1000000000`, `"waited":60285000000`): slog's JSON handler encodes `time.Duration` as an integer, like the other duration fields AMP logs.
- ERROR lines per startup: the race of A1 gave 5, a spent window 8 (deep-review F2, `defer-tech-debt`).
