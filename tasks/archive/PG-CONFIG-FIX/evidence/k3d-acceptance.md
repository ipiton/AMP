# K1–K3 — приёмка на k3d

Date: 2026-10-17. Cluster: k3d `pg-config-fix` (k3d v5.9.0, k3s v1.35.5+k3s1, local-path, 1 node), created in research; every command used `--context`/`--kube-context k3d-pg-config-fix`. Helm v4.3.0.
Chart: branch `bugfix/pg-config-fix` at `8c9608d` + working-tree exporter fix (see § K1.3, committed after this run). `main` chart: worktree of `main` at `3a0f723`. Image `amp:pg-config-fix` built from `8c9608d` (Go unchanged against `main`), `k3d image import`.
Values (`drill-values.yaml`): `image.{repository: amp, tag: pg-config-fix, pullPolicy: Never}`, `monitoring.prometheusEnabled: false`, `postgresql.monitoring.prometheusRules.enabled: false` (no prometheus-operator CRDs). Everything else — chart defaults: `profile: standard`, HPA 2–10, `postgresql.resources` 1Gi/2Gi.
K1 in namespace `k1`, K2/K3 in `k2`. API calls from a `curlimages/curl:8.11.1` pod in the namespace to `http://amp:8080` (`kubectl port-forward` to the AMP pod fails on this cluster: `connection refused` inside the netns, while `wget 127.0.0.1:8080/-/ready` from the pod returns `OK`).

Known, outside the task: `amp-valkey` stays `Init:Error`/`Init:CrashLoopBackOff` (BUGS:172). AMP pods restart while PostgreSQL is still starting (`dial tcp …:5432: connect: connection refused`, exit 1) — AMP has no wait for the database; same on `main`.

## K1 — fresh install (branch chart)

```sh
helm install amp helm/amp -n k1 --create-namespace -f drill-values.yaml
kubectl -n k1 rollout status sts/amp-postgres && kubectl -n k1 rollout status deploy/amp   # 68 s
```

### K1.1 Server configuration

```
config_file=/etc/postgresql/postgresql.conf
hba_file=/etc/postgresql/pg_hba.conf
logging_collector=off
max_connections=250
max_wal_size=4096          (MB)
min_wal_size=1024          (MB)
pg_stat_statements.max=10000
pg_stat_statements.track=all
shared_buffers=32768       (8kB pages = 256MB)
shared_preload_libraries=pg_stat_statements
```

`pg_hba_file_rules` (line, type, database, user, address, method; no `error`):

```
4 local all all - trust
6 host all all 127.0.0.1 trust
8 host all all ::1 trust
13 host alert_history alert_history all scram-sha-256
17 host all all all reject
```

`select count(*) from pg_stat_statements` → 382. Extensions: `btree_gin,pg_stat_statements,pg_trgm,plpgsql,uuid-ossp` (init.sql ran to the end).
`df -h /dev/shm` → `tmpfs 256M 1.1M 255M 1% /dev/shm`.
`amp-postgres-0`: `postgres` and `postgres-exporter` ready, restarts `0,0` — first start without a container restart.
`kubectl logs amp-postgres-0 -c postgres` continues after `database system is ready to accept connections` (connection log lines below), i.e. logs stay on stderr.

### K1.2 AMP

```
## /-/ready: 200
POST /api/v2/alerts PgFixk11: 200
POST /api/v2/alerts PgFixk12: 200
POST /api/v2/alerts PgFixk13: 200
POST /api/v2/silences: {"silenceID":"36397d91-63c1-4b59-b409-a823f5f88d63"} 200
```

DB: `3 alerts / 1 silences`; `pg_stat_activity` — 13 backends of `alert_history` (2 AMP pools + exporter). `grep -ciE 'pg_hba|password authentication failed'` in both AMP pod logs → 0. `helm test amp -n k1` → `amp-test-postgresql` `Phase: Succeeded`.

### K1.3 Exporter — found broken, fixed in this run

First run, chart at `8c9608d`: `pg_up 0`. PostgreSQL log: `user=$(POSTGRES_USER),db=$(POSTGRES_DB) … FATAL: role "$(POSTGRES_USER)" does not exist`. `DATA_SOURCE_NAME` referenced `$(POSTGRES_USER)`/`$(POSTGRES_PASSWORD)`/`$(POSTGRES_DB)` defined *below* it in the env list; Kubernetes expands only variables defined earlier, so the exporter connected with the literal string. Present since `05c9ee0` (2025-12-04); on `main` the server never applied the chart's `pg_hba` either, so the exporter has never connected.

Fix 1 — `DATA_SOURCE_URI` / `DATA_SOURCE_USER` / `DATA_SOURCE_PASS` (password from the Secret, not in a URL). Result: connected, but `/metrics` → HTTP 500:

```
Error running query on database "localhost:5432": pg_index_usage pq: column "tablename" does not exist
… pg_table_size_bytes pq: column "tablename" does not exist
… pg_vacuum_stats pq: column "tablename" does not exist
```

Fix 2 — `queries.yaml`: `pg_stat_user_tables`/`pg_stat_user_indexes` have `relname`/`indexrelname`; sizes via `relid`. Result: no query errors, still HTTP 500:

```
23 error(s) occurred:
* collected metric "pg_connection_pool_count" { label:{name:"server" value:"localhost:5432"} label:{name:"state" value:""} gauge:{value:5}} was collected before with the same name and label values
… pg_txid_wraparound_*, pg_checkpoint_stats_* — same
```

`PG_EXPORTER_AUTO_DISCOVER_DATABASES: "true"` reran the cluster-wide queries in every database (`postgres`, `alert_history`) with the same labels. Fix 3 — `"false"` (one database). Result: `scrape rc=0`, `pg_up 1`, 1231 `pg_*` series, `Error running` in the exporter log → 0.

Note: the exporter's `queries.yaml` ConfigMap is not in the pod's `checksum/config`; after fix 2 the pod needed `kubectl rollout restart sts/amp-postgres`. On a real upgrade the pod restarts anyway (the PostgreSQL ConfigMap changes too).

### K1.4 Load — `/dev/shm` and memory

Scenario of `k3d-shm-probe.txt` on `amp-postgres-0` (database `shmprobe`, `t1`, `t2` 3,000,000 rows each, `parallel_setup_cost=0`, `parallel_tuple_cost=0`), chart settings `work_mem=4096` (kB), `max_parallel_workers_per_gather=4`, `max_parallel_workers=8`:

```
explain: Workers Launched: 3; -> Parallel Hash Join (actual time=2919.271..4205.638 rows=750000 loops=4)
4 concurrent: 3000000 / 3000000 / 3000000 / 3000000      (image-default pod: 2 of 4 failed, k3d-shm-probe.txt)
/dev/shm after: tmpfs 256M 1.1M 255M 1%
postgres restarts=0 last={}, postgres-exporter restarts=0 last={}
memory.peak 1880 MiB
```

`memory.peak` is dominated by page cache from loading 2 × 3M rows: right after, `memory.current` 598 MiB = `anon` 27 MiB + `file` 571 MiB (`inactive_file` 474 MiB), `shmem` 94 MiB. No OOMKill.

## K2 — upgrade over a volume initialized by `main`

```sh
helm install amp <main worktree>/helm/amp -n k2 --create-namespace -f drill-values.yaml   # rev 1
```

`amp-postgres-0` was `Pending` (`Insufficient memory`: K1 still held its requests) until K1 was scaled to 0 (`helm upgrade … --set autoscaling.enabled=false --set replicaCount=0`, `kubectl scale sts amp-postgres amp-redis --replicas=0`, `kubectl scale deploy amp-valkey --replicas=0`). Then, `main` reproduces the bug: `amp-postgres-0` restarts `1`, previous container log:

```
psql:/docker-entrypoint-initdb.d/init.sql:56: ERROR:  unrecognized configuration parameter "pg_stat_statements.max"
```

State on `main` (rev 1):

```
config_file=/var/lib/postgresql/data/pgdata/postgresql.conf
hba_file=/var/lib/postgresql/data/pgdata/pg_hba.conf
logging_collector=off
max_connections=20
max_wal_size=1024
min_wal_size=80
shared_buffers=50          (8kB pages = 400kB)
shared_preload_libraries=
pg_hba_file_rules: 117 local all all trust; 119/121 host all all loopback trust; 124–126 replication local/loopback trust; 128 host all all all scram-sha-256
pg_stat_statements: ERROR: pg_stat_statements must be loaded via shared_preload_libraries
/dev/shm: shm 64M
```

Seeded: `PgFixk2main1..3` → 200 each, silence `3f003039-024e-42a4-bc79-284048d1be6c` → 200. DB: `3 alerts / 1 silences / goose max 20260423000000`.

```sh
helm upgrade amp helm/amp -n k2 -f drill-values.yaml   # rev 2, branch chart
```

`amp-postgres-0` back in 24 s. State after (rev 2): identical to K1.1 — `config_file=/etc/postgresql/postgresql.conf`, `hba_file=/etc/postgresql/pg_hba.conf`, `max_connections=250`, `shared_buffers=32768`, `shared_preload_libraries=pg_stat_statements`, the same 5 `pg_hba_file_rules`, `pg_stat_statements` works, `/dev/shm` 256M, `amp-postgres-0` restarts `0,0`. AMP pods were not restarted by the upgrade (restart counts unchanged) and reconnected.

Data: `3 alerts / 1 silences / goose max 20260423000000 rows 10`, alerts `PgFixk2main1..3`, silence `3f003039-…` — unchanged. New ingest: `PgFixk2upg1..3` → 200, silence `55f59285-b86f-40e3-83a2-fcabcd010fbc` → 200. Exporter `pg_up 1`, scrape OK, no query errors. Auth errors in AMP logs → 0. `helm test` → `Succeeded`.

## K3 — rollback

```sh
helm rollback amp 1 -n k2   # "Rollback was a success!"
```

`amp-postgres-0` replaced by the StatefulSet on its own (no manual pod deletion needed), restarts `0`. State: back to the image defaults (`config_file=/var/lib/postgresql/data/pgdata/postgresql.conf`, `max_connections=20`, `shared_buffers=50`, `shared_preload_libraries=`). Data: `6 alerts / 2 silences` — the rev-1 rows plus those written on rev 2.

## Edge case 2 — `--reuse-values` (dry-run against rev 3 = `main` values)

```
helm get values amp -n k2 --revision 1 -a   → loggingCollector: "on"
helm upgrade amp helm/amp -n k2 --reuse-values --dry-run=client                                        → logging_collector = on
helm upgrade amp helm/amp -n k2 --reset-then-reuse-values --dry-run=client                             → logging_collector = off
helm upgrade amp helm/amp -n k2 --reuse-values --set postgresql.config.loggingCollector=off --dry-run=client → logging_collector = off
```

## Re-check after deep-review fixes (R2, R6)

`helm upgrade amp helm/amp -n k2 -f drill-values.yaml` from rev 3 (`main`) → rev 4 (branch + loopback `scram-sha-256`, `time()` in `PostgreSQLVacuumOld`) — the upgrade path once more.

```
socket (kubectl exec psql, no -h):            socket ok
psql -w -h 127.0.0.1 -U alert_history -d postgres: fe_sendauth: no password supplied
psql -w -h ::1       -U alert_history -d postgres: fe_sendauth: no password supplied
PGPASSWORD=… psql -h 127.0.0.1 … alert_history:   2
exporter: scrape rc=0, pg_up 1, query errors 0
amp-postgres-0: postgres ready restarts=0, postgres-exporter ready restarts=0   (probes: pg_isready -h 127.0.0.1 needs no auth)
/-/ready 200; POST /api/v2/alerts PgFixk2r6{1..3} 200; silence 62376771-… 200; helm test Succeeded; alerts 9
```

Rules (`helm template … -s templates/postgresql-prometheus-rules.yaml | yq .spec`, promtool 3.15.0):

```
before: FAILED: … rule 9, "PostgreSQLVacuumOld": could not parse expression: 1:2: parse error: unknown function with name "now"
after:  SUCCESS: 11 rules found
promtool test rules: last_autovacuum 0 for 150m → no alert; last_autovacuum 1 (t=1s), eval 26h → alert, description "… has not been vacuumed for 93599 seconds."
```

## write-tests: `helm test` checks the running server (deep review R7)

`templates/tests/postgresql-test-connection.yaml` gained Test 5: `SHOW config_file` / `SHOW hba_file` over the Service must equal `/etc/postgresql/postgresql.conf` / `/etc/postgresql/pg_hba.conf`. `helm upgrade amp helm/amp --reuse-values` in `k2` (rev 5; only the test hook changed, `amp-postgres-0` not restarted), then `helm test amp --logs`:

```
✅ pg_stat_statements extension verified
✅ Chart config applied
🎉 All tests passed!
Phase: Succeeded
```

Rev 5 is `failed` in `helm history`: `--wait` timed out on `amp-valkey` (`Init:Error`, `exec /scripts/init.sh: no such file or directory`, BUGS:172, since the `main` install) and the unused PVC `amp-pvc` (`WaitForFirstConsumer`). Same state as rev 4, which ran without `--wait`; not related to PostgreSQL.

## Cleanup

2026-10-18, with the owner's confirmation: cluster `pg-config-fix` deleted (with releases `k1`/`k2`, research pods and the `shmprobe` database), kubectl context back to `k3d-sema-dev`.
