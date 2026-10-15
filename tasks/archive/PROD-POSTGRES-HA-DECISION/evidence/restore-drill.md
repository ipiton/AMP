# V1 — logical backup / restore drill (k3d)

Date: 2026-10-15. Cluster: k3d `amp-pg-research` (local-path). Chart: branch `feature/prod-postgres-ha-decision` (after phase 1). AMP image `amp:pg-drill` (branch HEAD).
PostgreSQL: `PostgreSQL 16.15 (Debian 16.15-1.pgdg13+2) on aarch64-unknown-linux-gnu` (image `postgres:16.15`).
No credentials on the command line: `psql`/`pg_dump`/`pg_restore` run inside the `postgres` container with `$POSTGRES_USER`/`$POSTGRES_DB` from its env (local socket).

Source data (seeded on the main chart, carried through the V2 upgrade path): `alerts`=5 (firing 3, resolved 2), `silences`=1 (active, matcher `alertname=drill-silenced`, id `6e2099ea-…`), `alert_investigations`=0.

## 1. Backup (step 2.4)

```sh
kubectl -n amp exec amp-postgres-0 -c postgres -- \
  sh -c 'pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Fc' > amp.dump
shasum -a 256 amp.dump
kubectl -n amp exec -i amp-postgres-0 -c postgres -- pg_restore --list < amp.dump | grep -c 'TABLE DATA'
```

Result: exit 0; size 88797 bytes; sha256 `fe52e19a35e2dfc3edfab428678f5cd9afe6e54838178daee450877590b0b4c4`; 17 `TABLE DATA` entries.

## 2. Loss (step 2.5)

```sh
helm uninstall amp -n amp --wait
kubectl delete ns amp --wait=true
kubectl get pvc -A | grep amp   # empty
kubectl get pv | grep 'amp/'    # empty
```

Result: no PVC, no PV left (local-path reclaim policy Delete) — data gone.

## 3. Restore (step 2.6)

```sh
# T0 = 09:38:25Z
helm install amp helm/amp -n amp --create-namespace -f drill-values.yaml --set replicaCount=0
kubectl -n amp rollout status sts/amp-postgres --timeout=240s          # +31s
kubectl -n amp exec -i amp-postgres-0 -c postgres -- \
  sh -c 'pg_restore -U "$POSTGRES_USER" -d "$POSTGRES_DB" --clean --if-exists --no-owner' < amp.dump
                                                                        # exit 0, no warnings, +34s
kubectl -n amp scale deploy/amp --replicas=1
kubectl -n amp rollout status deploy/amp --timeout=240s
kubectl -n amp port-forward pod/<amp-pod> 18480:8080 &                  # pod name, see side findings
until curl -sf localhost:18480/-/ready; do sleep 1; done                # +56s, 09:39:21Z
```

AMP stays at 0 replicas until `pg_restore` finishes, so it neither runs migrations against nor writes into an empty database.

**RTO (install → `/-/ready` 200): 56 s** (88 KB dump; dominated by PG pod start, 31 s). The restore itself took ~3 s.

## 4. Checks (step 2.7)

| Check | Command | Result |
|---|---|---|
| Migrations not re-applied | `select max(version_id), count(*), max(tstamp) from goose_db_version` | `20260423000000 \| 10 \| 2026-10-15 09:31:12` — last row is from the original install (09:31), none after the restore (09:38); 9 files in `go-app/migrations` + goose base row. Log: `Database migrations completed successfully` |
| Ready | `curl localhost:18480/-/ready` | `OK` 200 |
| Counts | `select count(*)` alerts / silences / alert_investigations | 5 / 1 / 0 — firing 3, resolved 2 (= source) |
| Firing alerts in API | `GET /api/v2/alerts` | DrillFiring1..3, `state=active` |
| Silence active | `GET /api/v2/silences` | `6e2099ea-…` `state=active`; AMP log `Silence store rehydrated from persistent storage, silences=1` |
| Silenced alert suppressed | `POST /api/v2/alerts` `[{labels:{alertname:drill-silenced}}]` | 200; alert absent from `GET /api/v2/alerts` (incl. `?silenced=true`) and from table `alerts` |
| New alert accepted | `POST /api/v2/alerts` `DrillPostRestore` | 200; present in API (`active`) and DB (`firing`) — control for the row above |

Note on "suppressed": the plan expected `status.state=suppressed`. AMP drops firing alerts that match an active silence at ingest (`internal/application/handlers/alerts.go:433`, `SilenceStore.HasActiveMatch`), so a silenced alert is never stored and never shows up with that state. The observable equivalent is recorded instead: the alert is accepted but not stored, while an identically shaped unsilenced alert is stored. `POST /api/v2/silences/check` returns 404 (not routed).

**Verdict: V1 passed.** `pg_dump -Fc` → `pg_restore --clean --if-exists --no-owner` into a fresh bundled PostgreSQL brings AMP back with alerts and silences intact and without re-running migrations.

## 5. Scenario A — roll back a live installation to a dump

Same dump, applied over the running installation from section 3 (DB now has 6 alerts: the 5 restored + `DrillPostRestore`).

```sh
kubectl -n amp scale deploy/amp --replicas=0
kubectl -n amp wait --for=delete pod -l app.kubernetes.io/instance=amp,app.kubernetes.io/component=application --timeout=120s
kubectl -n amp exec -i amp-postgres-0 -c postgres -- \
  sh -c 'pg_restore -U "$POSTGRES_USER" -d "$POSTGRES_DB" --clean --if-exists --no-owner' < amp.dump
kubectl -n amp scale deploy/amp --replicas=1
kubectl -n amp rollout status deploy/amp --timeout=240s
```

Result: `pg_restore` exit 0, no warnings; counts 6/1/0 → 5/1/0; `GET /api/v2/alerts` = DrillFiring1..3 (`DrillPostRestore` gone — state is the dump's); silence `6e2099ea-…` active; `goose_db_version` max `tstamp` unchanged (09:31:12); scale 0 → `/-/ready` 200 in 43 s.

AMP pod selector `app.kubernetes.io/instance=<release>,app.kubernetes.io/component=application` returns only the AMP pod (ADR-015 label).

## 6. HPA interaction (default `autoscaling.enabled: true`, `minReplicas: 2`)

`helm upgrade … --set autoscaling.enabled=true`, then `kubectl scale deploy/amp --replicas=0` and wait 75 s: `.spec.replicas` stays 0; `kubectl describe hpa amp` → `ScalingActive False ScalingDisabled scaling is disabled since the replica count of the target is zero`. `kubectl scale deploy/amp --replicas=2` → 2/2 ready, HPA resumes (`cpu: 23%/70%`). The HPA does not fight the scale-down during a restore.

## Side findings (pre-existing, out of scope)

- The silence manager background worker fails to start on every boot: `initial cache sync failed: invalid filter parameters: limit must be <= 1000, got 10000` (AMP continues without the GC/stats worker).
- The `amp` Service/Deployment selectors also match `amp-redis-0`/`amp-postgres-0` (BUGS `SERVICE-METRICS-PORT-MISROUTED`): `port-forward svc/amp` / `deploy/amp` land on redis. The runbook must address the AMP pod by name (see upgrade-path.md).

## 7. Fix round after deep review (R1–R3), 2026-10-15

Runbook commands after the fix round, re-run on the same k3d cluster (the cluster was restarted in between after a Docker restart). "User values" = drill values with `autoscaling.enabled: true` (chart default HPA, `minReplicas: 2`) and no `replicaCount` override.

### 7.1 Upgrade with `--reuse-values` from a main-chart release (R3)

Install the main chart with user values (2 AMP replicas), seed 5 alerts (3 firing, 2 resolved) and 1 silence (`3b7aabdf-…`) → counts 5/1/0.

| Command | Result |
|---|---|
| `helm upgrade amp helm/amp --reuse-values` | `UPGRADE FAILED: execution error at (amp/templates/postgresql-statefulset.yaml:8:4): postgresql.backup was removed: … Upgrading with --reuse-values carries the old chart default (backup.enabled: true): use --reset-then-reuse-values, or add --set postgresql.backup.enabled=false.` — nothing changed in the cluster |
| `helm upgrade … --reset-then-reuse-values --dry-run=server` | passes |
| `helm upgrade … --reuse-values --set postgresql.backup.enabled=false --dry-run=server` | passes |
| `helm upgrade … --reset-then-reuse-values` (real) | rev 2 deployed; CronJob/PDB/backup PVC gone, `postgres:16.15`, HPA min 2, counts 5/1/0 |

### 7.2 Backup and dump verification (R2)

```sh
set -o pipefail
kubectl -n amp exec amp-postgres-0 -c postgres -- \
  sh -c 'pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Fc' > amp.dump                 # exit 0
kubectl -n amp exec -i amp-postgres-0 -c postgres -- pg_restore -f /dev/null < amp.dump   # exit 0
sha256sum amp.dump >> amp-dumps.sha256     # shasum -a 256 on macOS
```

Dump 88800 bytes, sha256 `232defe9f2ca216d185fd790f93b616d20290956b020ce83289356b69d3a2750`. A truncated copy (`head -c 30000`) fails `pg_restore -f /dev/null` with `could not read from input file: end of file`, exit 1.

### 7.3 Scenario 3a with `--single-transaction --exit-on-error` (R2)

A marker silence row was added after the dump (counts 5/2/0). `kubectl scale deploy/amp --replicas=0`, wait until the AMP pods are gone, then:

| Input | `pg_restore -U … --clean --if-exists --no-owner --single-transaction --exit-on-error` | Counts after |
|---|---|---|
| truncated dump | `could not read from input file: end of file`, exit 1 | 5/2/0 — live data untouched (transaction rolled back) |
| full dump | exit 0, no output | 5/1/0 — dump state, marker gone |

`kubectl scale deploy/amp --replicas=2` → rollout complete; scale 0 → ready in 29 s. HPA `ScalingActive True` afterwards.

### 7.4 Scenario 3b with HPA-enabled user values (R1)

```sh
helm uninstall amp -n amp --wait; kubectl delete ns amp --wait=true     # no PVC left
# T0 = 11:55:54Z
helm install amp helm/amp -n amp --create-namespace -f user-values-hpa.yaml \
  --set autoscaling.enabled=false --set replicaCount=0                  # Deployment replicas=0, no HPA object
kubectl -n amp rollout status sts/amp-postgres --timeout=240s            # +38s
kubectl -n amp exec -i amp-postgres-0 -c postgres -- \
  sh -c 'pg_restore -U "$POSTGRES_USER" -d "$POSTGRES_DB" --clean --if-exists --no-owner --single-transaction --exit-on-error' < amp.dump
                                                                         # exit 0, +40s; AMP pods: 0
helm upgrade amp helm/amp -n amp -f user-values-hpa.yaml                 # rev 2: HPA back
kubectl -n amp rollout status deploy/amp --timeout=300s                  # ready +54s
```

Checks: Deployment 2/2, HPA active (`cpu 3%/70%`); counts 5/1/0; `goose_db_version` max `20260423000000`, `tstamp` 11:52:19 (original install, before T0); `GET /api/v2/alerts` = DrillFiring1..3 `active`; silence `3b7aabdf-…` `active`; `POST drill-silenced` → 200 and absent from the API; `POST DrillPostRestore2` → 200 and present.

**RTO (install → `/-/ready`) with HPA user values: 54 s.**

### 7.5 Restore into a recreated empty database (R7)

AMP scaled to 0, pods gone. While the database was dropped, `pg_stat_activity` showed only the `psql` session of the check itself; the exporter sidecar held no connection to it.

```sh
kubectl -n amp exec amp-postgres-0 -c postgres -- sh -c 'dropdb -U "$POSTGRES_USER" "$POSTGRES_DB"'     # exit 0
kubectl -n amp exec amp-postgres-0 -c postgres -- sh -c 'createdb -U "$POSTGRES_USER" "$POSTGRES_DB" && dropdb --force -U "$POSTGRES_USER" "$POSTGRES_DB" && createdb -U "$POSTGRES_USER" "$POSTGRES_DB"'   # exit 0
kubectl -n amp exec -i amp-postgres-0 -c postgres -- sh -c 'pg_restore … --clean --if-exists --no-owner --single-transaction --exit-on-error' < amp.dump   # exit 0
kubectl -n amp scale deploy/amp --replicas=2
```

Counts 5/1/0. AMP log `Database migrations completed successfully`. `goose_db_version`: 10 rows, max `tstamp` 11:52:19, so no migration was re-applied.

### 7.6 PVC lost, release alive (R8)

```sh
kubectl -n amp scale deploy/amp --replicas=0      # + wait for the AMP pods to go
kubectl -n amp delete pvc postgres-data-amp-postgres-0 --wait=false
kubectl -n amp delete pod amp-postgres-0 --wait=true
kubectl -n amp rollout status sts/amp-postgres    # new PVC uid 5c291136-… (was 77d7999f-…); `alerts` does not exist
kubectl -n amp exec -i amp-postgres-0 -c postgres -- sh -c 'pg_restore … --single-transaction --exit-on-error' < amp.dump   # exit 0
kubectl -n amp scale deploy/amp --replicas=2
```

Counts 5/1/0, `goose_db_version` unchanged (11:52:19), `/-/ready` OK, silence `3b7aabdf-…` active. Scenario 3a covers this case.

### 7.7 Re-review follow-ups (R18, R20)

- `<fullname>` lookup: `kubectl -n amp get sts -l app.kubernetes.io/instance=amp,app.kubernetes.io/component=database -o jsonpath='{.items[0].metadata.name}'` → `amp-postgres`, so `<fullname>` = `amp`. The AMP Deployment has no `component` label (only its pod template does), so a lookup by `component=application` on `deploy` finds nothing.
- Checksum check before restore (macOS `shasum`): `grep " $DUMP$" amp-dumps.sha256 | shasum -a 256 -c -` → `OK`, exit 0; after appending one byte to the dump → `FAILED`, exit 1.
