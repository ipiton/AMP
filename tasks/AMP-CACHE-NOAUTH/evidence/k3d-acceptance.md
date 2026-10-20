# k3d acceptance: AMP-CACHE-NOAUTH

2026-10-20, k3d v5.9.0, k3s v1.35.5, helm v4.3.0, cluster `cache-noauth` (8 vCPU, traefik off). Image `amp:cache-noauth` built from `941092a` (Go code unchanged by the task), `k3d image import`, `image.pullPolicy: Never`. Values: `monitoring.prometheusEnabled=false`, `postgresql.monitoring.prometheusRules.enabled=false` (no prometheus-operator CRDs on k3d); everything else chart defaults. Chart `main` = `git archive main` (`ddc02ae`). Passwords are never printed: the Secret is shown as the first 16 hex of sha256 of its base64 value.

AMP restarts 3–6 times on a fresh install until PostgreSQL accepts connections (`Failed to ping database … dial error`) — pre-existing, seen in HELM-SINGLE-NODE-DEFAULTS acceptance too, not changed here. `Initial cache sync failed: limit must be <= 1000, got 10000` in every AMP log is the open silence-manager bug in `BUGS.md`, unrelated.

`kubectl logs deploy/amp` picks the Valkey pod (shared selectorLabels, see `deployment.yaml:25`); logs below are taken from the AMP pod by name.

## A1 — branch, defaults, fresh install (namespace a1)

```text
$ helm install amp helm/amp -n a1 -f values.yaml --wait
STATUS: deployed  REVISION: 1
NOTES: ✅ Valkey L2 + Memory L1 cache, password from Secret amp-redis-secret/password

NAME                   READY   STATUS    RESTARTS       AGE
amp-648d65d7bd-c5mj4   1/1     Running   6 (3m1s ago)   5m52s
amp-postgres-0         2/2     Running   0              5m53s
amp-redis-0            2/2     Running   0              5m53s

$ AMP log (cache)
{"level":"INFO","msg":"Initializing cache backend..."}
{"level":"INFO","msg":"Connected to Redis","addr":"amp-redis:6379","db":0}
{"level":"INFO","msg":"Redis cache initialized","addr":"amp-redis:6379","db":0}
{"level":"INFO","msg":"Initialized Redis silence event bus (cross-replica cache invalidation)"}

$ POST /api/v2/alerts (service proxy)
verb="POST" status="200 OK"

$ valkey: redis-cli --scan (REDISCLI_AUTH from the pod env) / without password
amp:cluster:peer:amp-648d65d7bd-c5mj4-29844d09
dbsize=1
noauth=NOAUTH Authentication required.

$ valkey: CLIENT LIST from the AMP pod IP 10.42.0.5
7 connections (one flags=P: pub/sub silence bus)

$ redis-exporter /metrics
redis_up 1
redis_connected_clients 8
```

On `main` the same install logs `Failed to connect to Redis … NOAUTH Authentication required.` → `falling back to in-memory cache` (A2.0 below; HELM-SINGLE-NODE-DEFAULTS acceptance).

## A2 — install main, upgrade with the branch twice, rollout restart (namespace a2)

Script `a2-check.sh <step>`: Secret hash and uid, pods, the AMP pod's `REDIS_PASSWORD` secretKeyRef, AMP cache log lines, `PING` with the Secret's password, number of `amp:*` keys.

```text
## A2.0 install main@ddc02ae
secret sha256: 168aae4aea580d56
valkey secret uid: 05bbc4ca-489e-4858-9e00-9d7474b03533
NAME                   READY   STATUS    RESTARTS      AGE
amp-687dfc487d-jswbw   1/1     Running   3 (47s ago)   64s
amp-postgres-0         2/2     Running   0             64s
amp-redis-0            2/2     Running   0             64s
pod/amp-687dfc487d-jswbw REDIS_PASSWORD ref: 
{"time":"2026-10-20T08:54:59.489083379Z","level":"ERROR","msg":"Failed to connect to Redis","error":"NOAUTH Authentication required.","addr":"amp-redis:6379"}
{"time":"2026-10-20T08:54:59.489112337Z","level":"WARN","msg":"Redis cache unavailable, falling back to in-memory cache","error":"failed to connect to Redis: NOAUTH Authentication required.","addr":"a
valkey auth with Secret: PONG
amp keys: 0
## A2.1 helm upgrade -> branch @ 941092a
secret sha256: 168aae4aea580d56
valkey secret uid: 05bbc4ca-489e-4858-9e00-9d7474b03533
NAME                   READY   STATUS    RESTARTS   AGE
amp-6cf6d6467d-8hs2p   1/1     Running   0          46s
amp-postgres-0         2/2     Running   0          2m3s
amp-redis-0            2/2     Running   0          40s
pod/amp-6cf6d6467d-8hs2p REDIS_PASSWORD ref: amp-redis-secret/password
{"time":"2026-10-20T08:55:45.464149598Z","level":"WARN","msg":"Redis cache unavailable, falling back to in-memory cache","error":"failed to connect to Redis: dial tcp 10.43.50.231:6379: i/o timeout","
valkey auth with Secret: PONG
amp keys: 0
2026-10-20T08:55:40Z  amp started
## A2.1b rollout restart after the race
secret sha256: 168aae4aea580d56
valkey secret uid: 05bbc4ca-489e-4858-9e00-9d7474b03533
NAME                   READY   STATUS    RESTARTS   AGE
amp-6c7cb5b499-6qtpk   1/1     Running   0          27s
amp-postgres-0         2/2     Running   0          2m44s
amp-redis-0            2/2     Running   0          81s
pod/amp-6c7cb5b499-6qtpk REDIS_PASSWORD ref: amp-redis-secret/password
{"time":"2026-10-20T08:56:35.582500372Z","level":"INFO","msg":"Redis cache initialized","addr":"amp-redis:6379","db":0}
valkey auth with Secret: PONG
amp keys: 1
## A2.2 second helm upgrade, same values
secret sha256: 168aae4aea580d56
valkey secret uid: 05bbc4ca-489e-4858-9e00-9d7474b03533
NAME                   READY   STATUS    RESTARTS   AGE
amp-6c7cb5b499-6qtpk   1/1     Running   0          49s
amp-postgres-0         2/2     Running   0          3m6s
amp-redis-0            2/2     Running   0          103s
pod/amp-6c7cb5b499-6qtpk REDIS_PASSWORD ref: amp-redis-secret/password
{"time":"2026-10-20T08:56:35.582500372Z","level":"INFO","msg":"Redis cache initialized","addr":"amp-redis:6379","db":0}
valkey auth with Secret: PONG
amp keys: 1
## A2.3 rollout restart deploy/amp
secret sha256: 168aae4aea580d56
valkey secret uid: 05bbc4ca-489e-4858-9e00-9d7474b03533
NAME                   READY   STATUS    RESTARTS   AGE
amp-66487bd567-6c76k   1/1     Running   0          26s
amp-postgres-0         2/2     Running   0          3m34s
amp-redis-0            2/2     Running   0          2m11s
pod/amp-66487bd567-6c76k REDIS_PASSWORD ref: amp-redis-secret/password
{"time":"2026-10-20T08:57:26.021635868Z","level":"INFO","msg":"Redis cache initialized","addr":"amp-redis:6379","db":0}
valkey auth with Secret: PONG
amp keys: 1
```

NOTES of the first `helm upgrade` (revision 2, T7c of `tasks.md` 4.1):

```text
✅ Valkey L2 + Memory L1 cache, password from Secret amp-redis-secret/password
ℹ️  After the upgrade the AMP log should say "Redis cache initialized". On "falling back to
   in-memory cache" (AMP started before Valkey was ready):
   kubectl -n a2 rollout restart deploy/amp
```

Result:
- Password hash `168aae4aea580d56` and Secret uid identical over install (main), two upgrades (branch) and the restart: `lookup` keeps the password.
- The second upgrade with the same values restarts nothing (AMP pod `amp-6c7cb5b499-6qtpk` survives it).
- After the first upgrade AMP lost the start race (`AMP-CACHE-NO-RECONNECT`): the new AMP pod started at 08:55:40, Valkey (restarted by the new volume `items`) at 08:55:43; AMP got `dial tcp …:6379: i/o timeout` and stayed on the in-memory fallback while Ready. The NOTES hint (`rollout restart`) fixed it; after every later step AMP logs `Redis cache initialized`. `redis_up 1` at the end.

## B3 — valkey.password.existingSecret with a trailing newline (namespace b3)

```text
$ printf 'b3-test-<random>\n' > pw; kubectl create secret generic amp-valkey --from-file=password=pw
$ helm install amp helm/amp -n b3 -f values.yaml --set valkey.password.existingSecret=amp-valkey
amp-5d45499c6d-jvg82   1/1     Running   3 (59s ago)   78s
amp-postgres-0         2/2     Running   0             78s
amp-redis-0            2/2     Running   0             78s     <- Ready: probes use unquoted -a $REDIS_PASSWORD, the shell drops the newline

AMP:      {"level":"ERROR","msg":"Failed to connect to Redis","error":"WRONGPASS invalid username-password pair or user is disabled."}
          {"level":"WARN","msg":"Redis cache unavailable, falling back to in-memory cache", ...}
exporter: redis_up 0; level=error msg="Couldn't connect to redis instance (localhost:6379)"
valkey:   REDISCLI_AUTH="$REDIS_PASSWORD" redis-cli ping -> WRONGPASS (env keeps the newline; config-init trims it)

$ same Secret recreated with printf '%s' (no newline), rollout restart sts/amp-redis and deploy/amp
AMP:      {"level":"INFO","msg":"Redis cache initialized","addr":"amp-redis:6379","db":0}
exporter: redis_up 1
```

Confirms the documented behaviour (`values.yaml` `valkey.password`, README § Cache): a trailing newline breaks AMP and the exporter while Valkey looks healthy; `printf '%s'` works.
