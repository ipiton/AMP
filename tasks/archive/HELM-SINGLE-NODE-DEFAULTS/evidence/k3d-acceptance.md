# k3d acceptance: HELM-SINGLE-NODE-DEFAULTS

2026-10-19, k3d v5.9.0, k3s v1.35.5, helm v4.3.0. Image `amp:single-node` (Go code unchanged by the task), `image.pullPolicy: Never`; ServiceMonitor/PrometheusRule off (no CRDs on k3d). Chart `main` = `dbaf049` (+ its valkey 2.1.3 subchart).

- **acc**: `--kubelet-arg=kube-reserved=cpu=7` (allocatable 1 vCPU), traefik off — A1, A4.
- **single-node**: 8 vCPU — A2, A3 (namespace a2).

## A1 — chart main on 1 vCPU: Pending

```text
## A1 main@dbaf049 on k3d acc (kube-reserved cpu=7 -> allocatable cpu 1)
N                  CPU   MEM
k3d-acc-server-0   1     7060812Ki
NAME                         READY   STATUS             RESTARTS      AGE
amp-7dd7cc64c9-vv2j2         0/1     CrashLoopBackOff   5 (40s ago)   3m54s
amp-7dd7cc64c9-wkjvn         0/1     Pending            0             3m38s
amp-postgres-0               0/2     Pending            0             3m53s
amp-redis-0                  0/2     Pending            0             3m53s
amp-valkey-7f8bccd8c-pbpdv   0/1     Init:Error         3             3m54s
amp-7dd7cc64c9-wkjvn   0/1 nodes are available: 1 Insufficient cpu. no new claims to deallocate, preemption: 0/1 nodes are available: 1 No preemption victims found for incoming pod.
amp-postgres-0         0/1 nodes are available: 1 Insufficient cpu. no new claims to deallocate, preemption: 0/1 nodes are available: 1 No preemption victims found for incoming pod.
amp-redis-0            0/1 nodes are available: 1 Insufficient cpu. no new claims to deallocate, preemption: 0/1 nodes are available: 1 No preemption victims found for incoming pod.
OBJ                    MSG
```

Release scaled to 0 afterwards (Helm uninstall needs owner confirmation).

## A1 — branch on 1 vCPU: all Ready

```text
## A1 branch (feature/helm-single-node-defaults @ 84c3e56) on k3d acc, default values + test image
NAME                   READY   STATUS    RESTARTS      AGE
amp-594d8f98df-f8mxj   1/1     Running   4 (97s ago)   2m19s
amp-postgres-0         2/2     Running   0             2m19s
amp-redis-0            2/2     Running   0             2m19s

No resources found in a1 namespace.

$ kubectl describe node | Allocated resources (k3s system pods included)
Allocated resources:
  (Total limits may be over 100 percent, i.e., overcommitted.)
  Resource           Requests    Limits
  --------           --------    ------
  cpu                470m (47%)  2700m (270%)
  memory             652Mi (9%)  2986Mi (43%)

$ POST /api/v2/alerts (service proxy)
Response verb="POST" url=".../proxy/api/v2/alerts" status="200 OK" milliseconds=35

$ redis_exporter / postgres_exporter
redis_instance_info{maxmemory_policy="allkeys-lru",process_id="1",redis_build_id="2cb63ac0e4567989",redis_mode="",redis_version="7.2.4",role="master",tcp_port="6379"} 1
redis_up 1
pg_up 1
Valkey server v=8.1.10 sha=00000000:1 malloc=jemalloc-5.3.0 bits=64 build=2cb63ac0e4567989

$ AMP log: cache (bug AMP-CACHE-NOAUTH, pre-existing)
{"time":"2026-10-19T11:01:35.862591212Z","level":"ERROR","msg":"Failed to connect to Redis","error":"NOAUTH Authentication required.","addr":"amp-redis:6379"}
{"time":"2026-10-19T11:01:35.862614629Z","level":"WARN","msg":"Redis cache unavailable, falling back to in-memory cache","error":"failed to connect to Redis: NOAUTH Authentication required.","addr":"a
{"time":"2026-10-19T11:02:17.34698792Z","level":"WARN","msg":"LLM client not configured, falling back to transparent mode"}

$ AMP restarts: crash until PostgreSQL accepts connections (pre-existing, not changed here)
{"time":"2026-10-19T11:00:39.927112091Z","level":"ERROR","msg":"Failed to ping database","error":"failed to connect to `user=alert_history database=alert_history`: 10.43.73.46:5432 (amp-postgres): dia

$ config-init log
No Redis >= 7.4 data on the volume
```

## A2 — upgrade main (HPA on, Redis 7.4 data) → branch

```text
## A2 before upgrade: main@dbaf049 in a2 (k3d-single-node), HPA on, Redis 7.4
NAME                              READY   STATUS       RESTARTS      AGE
pod/amp-6956d8f88f-chffh          1/1     Running      3 (57s ago)   74s
pod/amp-6956d8f88f-rcz92          1/1     Running      3 (44s ago)   59s
pod/amp-postgres-0                2/2     Running      0             74s
pod/amp-redis-0                   2/2     Running      0             74s
pod/amp-valkey-5b59658b7d-62gdl   0/1     Init:Error   3 (44s ago)   74s

NAME                                      REFERENCE        TARGETS                                    MINPODS   MAXPODS   REPLICAS   AGE
horizontalpodautoscaler.autoscaling/amp   Deployment/amp   cpu: 10%/70%, memory: 4%/80% + 3 more...   2         10        2          75s

Redis server v=7.4.11 sha=00000000:0 malloc=jemalloc-5.3.0 bits=64 build=dc395baf3ab4da8b
OK
1
OK
$ ls /data with RDB headers
dump.rdb REDIS0012 118 bytes
appendonlydir/appendonly.aof.1.base.rdb REDIS0012 89 bytes
appendonlydir/appendonly.aof.1.incr.aof .2...6..S 68 bytes
appendonlydir/appendonly.aof.manifest file.appe 88 bytes
```

```text
$ helm upgrade amp helm/amp -n a2 -f values.yaml   (branch, no --force-conflicts)
rc=0

## after upgrade
1       	Mon Oct 19 13:11:14 2026	superseded	amp-0.1.0	0.1.0      	Install complete
2       	Mon Oct 19 13:14:17 2026	deployed  	amp-0.1.0	0.1.0      	Upgrade complete
NAME                       READY   STATUS        RESTARTS        AGE
pod/amp-6956d8f88f-chffh   1/1     Terminating   3 (4m28s ago)   4m45s
pod/amp-6c96b9f6d7-9cq57   1/1     Running       3 (78s ago)     89s
pod/amp-6c96b9f6d7-rvdng   1/1     Running       4 (64s ago)     101s
pod/amp-postgres-0         2/2     Running       0               71s
pod/amp-redis-0            2/2     Running       0               99s

NAME                                      REFERENCE        TARGETS                                   MINPODS   MAXPODS   REPLICAS   AGE
horizontalpodautoscaler.autoscaling/amp   Deployment/amp   cpu: 6%/70%, memory: 8%/80% + 3 more...   2         10        2          4m46s

NAME                                                 STATUS   VOLUME                                     CAPACITY   ACCESS MODES   STORAGECLASS   VOLUMEATTRIBUTESCLASS   AGE
persistentvolumeclaim/postgres-data-amp-postgres-0   Bound    pvc-524a245f-d1e7-4093-9da5-94c540eaba2c   10Gi       RWO            local-path     <unset>                 4m46s
persistentvolumeclaim/redis-data-amp-redis-0         Bound    pvc-2c827acd-9080-4d29-8937-e4ca93699100   5Gi        RWO            local-path     <unset>                 4m46s

spec.replicas=2 managedFields(replicas): helm/ k3s/scale k3s/status

$ config-init log
Redis >= 7.4 data (RDB v12+: dump.rdb appendonlydir) moved to /data/redis-pre-valkey-1792408462
Copying redis.conf from ConfigMap...

$ ls /data
appendonlydir/appendonly.aof.1.base.rdb REDIS0011 90 bytes
appendonlydir/appendonly.aof.1.incr.aof  0 bytes
appendonlydir/appendonly.aof.manifest file.appe 88 bytes
redis-pre-valkey-1792408462/dump.rdb REDIS0012 119 bytes
redis-pre-valkey-1792408462/appendonlydir/appendonly.aof.1.base.rdb REDIS0012 89 bytes
redis-pre-valkey-1792408462/appendonlydir/appendonly.aof.1.incr.aof .2...6..S 68 bytes
redis-pre-valkey-1792408462/appendonlydir/appendonly.aof.manifest file.appe 88 bytes
Valkey server v=8.1.10 sha=00000000:1 malloc=jemalloc-5.3.0 bits=64 build=2cb63ac0e4567989
0

$ helm get notes (HPA / cache lines)
✅ Valkey L2 + Memory L1 cache
ℹ️  HPA off: fixed 1 replica(s) (autoscaling.enabled or values-production.yaml for HA)
⚠️  An HPA amp left by an older chart (helm.sh/resource-policy: keep) still
   scales this Deployment, up to its maxReplicas. Delete it:
   kubectl -n a2 delete hpa amp

$ POST /api/v2/alerts
Response verb="POST" url=".../proxy/api/v2/alerts" status="200 OK" milliseconds=144

$ AMP log: cache
{"time":"2026-10-19T11:15:07.110284817Z","level":"ERROR","msg":"Failed to connect to Redis","error":"NOAUTH Authentication required.","addr":"amp-redis:6379"}
{"time":"2026-10-19T11:15:51.163902807Z","level":"ERROR","msg":"Failed to connect to Redis","error":"NOAUTH Authentication required.","addr":"amp-redis:6379"}

$ objects of the removed subchart / amp-pvc
none
```

Notes:
- No `.spec.replicas` conflict on this release (the conflict in `hpa-upgrade-off.txt` A is conditional; migration note 2 says "if").
- The orphan HPA keeps the Deployment at 2 replicas; NOTES prints the warning with the delete command. Deleting it is left to the owner (hook-guarded); the HPA-gone path is in `hpa-upgrade-off.txt` B.
- Re-notification count (Spec Open Question) not measured: plain-http webhooks fail AMP validation, and on the defaults AMP does not use the cache (NOAUTH above, `AMP-CACHE-NOAUTH`), so the upgrade re-notifies as on any AMP restart, independent of the data move.

## A3 — rollback and restore

```text
## A3 rollback
OK
$ helm rollback amp 1 -n a2
level=WARN msg="resource exists on cluster but not in original release, using cluster state as baseline" namespace=a2 name=amp kind=HorizontalPodAutoscaler
Rollback was a success! Happy Helming!
rc=0
NAME                              READY   STATUS       RESTARTS        AGE     IP           NODE
pod/amp-6956d8f88f-qxcz6          0/1     Running      0               17s     10.42.0.44   k3d-single-node-se
pod/amp-6c96b9f6d7-9cq57          0/1     Running      3 (3m27s ago)   3m38s   10.42.0.42   k3d-single-node-se
pod/amp-6c96b9f6d7-rvdng          0/1     Running      4 (3m13s ago)   3m50s   10.42.0.39   k3d-single-node-se
pod/amp-postgres-0                1/2     Running      0               15s     10.42.0.47   k3d-single-node-se
pod/amp-redis-0                   2/2     Running      0               15s     10.42.0.46   k3d-single-node-se
pod/amp-valkey-5b59658b7d-722zf   0/1     Init:Error   1 (12s ago)     17s     10.42.0.48   k3d-single-node-se

NAME                                      REFERENCE        TARGETS                                   MINPODS
horizontalpodautoscaler.autoscaling/amp   Deployment/amp   cpu: 3%/70%, memory: 8%/80% + 3 more...   2

NAME                                                 STATUS    VOLUME                                     CAPA
persistentvolumeclaim/amp-pvc                        Pending
persistentvolumeclaim/amp-valkey                     Bound     pvc-844b9c40-0203-4c9f-99bd-e8677e16cb95   5Gi
persistentvolumeclaim/postgres-data-amp-postgres-0   Bound     pvc-524a245f-d1e7-4093-9da5-94c540eaba2c   10Gi
persistentvolumeclaim/redis-data-amp-redis-0         Bound     pvc-2c827acd-9080-4d29-8937-e4ca93699100   5Gi

$ Redis after rollback
1:C 19 Oct 2026 11:17:55.527 * Redis version=7.4.11, bits=64, commit=00000000, modified=0, pid=1, just started
1:M 19 Oct 2026 11:17:55.529 * DB loaded from append only file: 0.000 seconds
Redis server v=7.4.11 sha=00000000:0 malloc=jemalloc-5.3.0 bits=64 build=dc395baf3ab4da8b
a2key=
a3key=from-valkey

## A3 restore of the pre-upgrade data (CHANGELOG note 6)
$ kubectl scale statefulset/amp-redis --replicas=0
statefulset.apps/amp-redis scaled
pod/amp-redis-0 condition met
pod/redis-restore created
pod/redis-restore condition met
before:
appendonlydir
dump.rdb
redis-pre-valkey-1792408462
redis.conf
after:
appendonlydir
dump.rdb
redis.conf
$ kubectl scale statefulset/amp-redis --replicas=1
statefulset.apps/amp-redis scaled
Waiting for 1 pods to be ready...
partitioned roll out complete: 1 new pods have been updated...
Redis server v=7.4.11 sha=00000000:0 malloc=jemalloc-5.3.0 bits=64 build=dc395baf3ab4da8b
a2key=from-redis-7.4
a3key=
```

Restore was run after the rollback (Redis 7.4 already in place); CHANGELOG note 6 orders it before the rollback — same file operations, same result.

## A4 — values-small.yaml on 1 vCPU, PostgreSQL memory (review R10)

Installed with `-f values-small.yaml`: all pods Ready, PVCs 5Gi / 1Gi.

Load: inside the postgres container, fill max_connections with sessions that each sort 100k random rows (beyond work_mem), then pg_sleep(30); sample cgroup memory.stat (`pgload.sh`).

### work_mem 4MB (values-small before the fix)

```text
max_connections=60 shared_buffers=64MB work_mem=4MB max_wal_size=1GB min_wal_size=256MB
client backends before: 6; opening 52 sessions
memory.max=536870912 current=47058944 peak=536870912
max anon during load: 469Mi (anon=459Mi file=52Mi shmem=28Mi )
client backends during load: 56
memory during load: current=184930304 peak=536870912 oom_kill=0
after: current=44896256 peak=536870912 oom_kill=0
restarts: 1 0
```

Anonymous memory 469Mi of the 512Mi limit, no OOM kill. The first attempt with 300k-row sorts saturated the 500m CPU limit; the liveness probe timed out and the kubelet restarted PostgreSQL (crash recovery, AMP reconnected):

```text
max_connections=60 shared_buffers=64MB work_mem=4MB max_wal_size=1GB min_wal_size=256MB
client backends before: 6; opening 52 sessions
memory.max=536870912 current=57630720 peak=136847360
command terminated with exit code 137
```

### work_mem 2MB (values-small after the fix)

```text
max_connections=60 shared_buffers=64MB work_mem=2MB max_wal_size=1GB min_wal_size=256MB
client backends before: 7; opening 51 sessions
memory.max=536870912 current=60960768 peak=65638400
max anon during load: 274Mi (anon=275Mi file=118Mi shmem=26Mi )
client backends during load: 50
memory during load: current=173408256 peak=439701504 oom_kill=0
after: current=53514240 peak=439701504 oom_kill=0
restarts: 0 0
```

### chart defaults (A1 namespace): 1Gi limit, 100 connections, work_mem 4MB

```text
max_connections=100 shared_buffers=128MB work_mem=4MB max_wal_size=4GB min_wal_size=1GB
client backends before: 7; opening 91 sessions
memory.max=1073741824 current=67698688 peak=126107648
max anon during load: 657Mi (anon=634Mi file=152Mi shmem=34Mi )
client backends during load: 81
memory during load: current=279355392 peak=868151296 oom_kill=0
after: current=72212480 peak=868151296 oom_kill=0
restarts: 0 0
```

Defaults keep ~200Mi headroom at 81 sorting sessions (peak incl. page cache 828Mi of 1Gi); unchanged.
