# D9 — external PostgreSQL drill (k3d)

Date: 2026-10-16. Cluster: k3d `amp-extdb` (k3d v5.9.0, local-path), created with `--kubeconfig-switch-context=false`; every command used `--context`/`--kube-context k3d-amp-extdb`. Owner context before and after: `k3d-sema-dev`.
Chart: branch `feature/helm-external-database` (Phase 1–3 commits + the `deployment.kubernetes.io/revision` fix below). Image `amp:extdb-drill` built from branch HEAD `1e4c414`, `k3d image import`. Helm v4.3.0.
External PostgreSQL: docker container `postgres:16.15` (`PostgreSQL 16.15 (Debian 16.15-1.pgdg13+2) on aarch64-unknown-linux-gnu`) on the docker network `k3d-amp-extdb`, address `192.168.97.4` (container IP), `-c ssl=on` with the image's snakeoil certificate.
Role `amp`: `rolsuper=f, rolcreatedb=f, rolcreaterole=f`; databases created by the superuser with `owner amp`. Password of `amp`: `<redacted>`, 20 characters containing `/ % # ? space @ :` and the sequence `%41` (the pre-fix DSN would have decoded it to `A`). Passwords never went on a command line: `PGPASSWORD` was passed by name into the client container, the Secret was created `--from-file`.
`pg_dump`/`pg_restore`/`psql` against the external server ran from a throwaway `postgres:16.15` container on the same docker network — a stand-in for "a host with PostgreSQL client tools".

Drill values (`drill-values.yaml`): `replicaCount: 1`, `autoscaling.enabled: false`, local image, and no prometheus-operator CRDs in the cluster: `monitoring.prometheusEnabled: false`, `postgresql.monitoring.prometheusRules.enabled: false`. `environment` stays `production` (chart default).
External DB values (`move-values.yaml`): `postgresql.enabled: false`, `externalDatabase.{host: 192.168.97.4, database: amp2, username: amp, existingSecret: amp-extdb}`; `port`, `sslMode: require`, `existingSecretPasswordKey: password` from chart defaults.

Known, outside the task (as in the PROD-POSTGRES-HA-DECISION drill): `helm install --wait` never finishes (`amp-pvc` stays `Pending` with no consumer; `amp-valkey` in `Init:Error`, BUGS:172). Installed without `--wait`, waited on `rollout status`.

## 1. Source: bundled PostgreSQL (rev 1)

```sh
helm install amp helm/amp -n amp --create-namespace -f drill-values.yaml
kubectl -n amp rollout status sts/amp-postgres --timeout=240s && kubectl -n amp rollout status deploy/amp --timeout=300s   # 63 s
```

Seeded through the API: 3 firing (`DrillFiring1..3`), 2 resolved (`DrillResolved1..2`), 1 active silence `37715b5f-…` on `alertname=drill-silenced`.
Counts `alerts/silences/alert_investigations` = 5 / 1 / 0; `goose_db_version` max `20260423000000`, 10 rows, max `tstamp 13:38:48`.

## 2. External server

```sh
docker run -d --name amp-extdb-pg --network k3d-amp-extdb -e POSTGRES_PASSWORD=<redacted> postgres:16.15 \
  -c ssl=on -c ssl_cert_file=/etc/ssl/certs/ssl-cert-snakeoil.pem -c ssl_key_file=/etc/ssl/private/ssl-cert-snakeoil.key
psql -U postgres -v pw=<redacted> -c "create role amp login password :'pw'" -c "create database amp owner amp"
kubectl -n amp create secret generic amp-extdb --from-file=password=<file with the password>
# from a pod in the cluster:
psql "host=192.168.97.4 user=amp dbname=amp sslmode=require" -Atc "select 1, ssl, version from pg_stat_ssl where pid = pg_backend_pid()"
```

Result: `1|t|TLSv1.3`.

## 3. First pass — findings (rev 2–4)

Order used: dump (AMP running) → `kubectl scale --replicas=0` → `helm upgrade` to the external DB with `--set replicaCount=0` (rev 2, deployed; `sts/amp-postgres` deleted, PVC `postgres-data-amp-postgres-0` uid `f86231d4-…` stays `Bound`) → `pg_restore` into the empty `amp`.

| # | Command | Result | Consequence |
|---|---|---|---|
| F1 | `pg_restore --no-owner --single-transaction --exit-on-error` | `ERROR: permission denied to create extension "pg_stat_statements"` — `HINT: Must be superuser`; rc 1, database left empty (single transaction) | the bundled PostgreSQL's init script creates `pg_stat_statements` (`templates/postgresql-configmap.yaml:159`); it is not a trusted extension and AMP's schema does not use it. Exclude it with a `pg_restore -L` list |
| F2 | same + `-L` list without `EXTENSION - pg_stat_statements` | `extension "pg_stat_statements" does not exist` on `COMMENT ON EXTENSION` | the list filter must drop the `COMMENT - EXTENSION pg_stat_statements` entry too |
| F3 | same, both entries excluded | `role "alert_history" does not exist` on `GRANT USAGE ON SCHEMA public TO alert_history` | across servers `--no-owner` is not enough: add `--no-privileges` |
| — | `--no-owner --no-privileges --single-transaction --exit-on-error -L amp.list` | rc 0; 5 / 1 / 0; goose unchanged; every table owned by `amp`; extensions `btree_gin, pg_trgm, plpgsql, uuid-ossp` | |
| F4 | `helm upgrade` back to `replicaCount: 1` (rev 3, retried as rev 4) | `UPGRADE FAILED: conflict occurred while applying object amp/amp apps/v1, Kind=Deployment: Apply failed with 1 conflict: conflict with "k3s" with subresource "status" using apps/v1: .metadata.annotations.deployment.kubernetes.io/revision` | pre-existing chart bug: `templates/deployment.yaml:9` hard-coded `deployment.kubernetes.io/revision: "1"`. Rev 2 still matched the live value; once the controller bumped it to `2`, every Helm 4 (server-side apply) upgrade that touches the Deployment conflicts. Fixed on the branch (annotation removed); with the fixed chart the same upgrade is rev 5, deployed |

After rev 5 (fixed chart): `/-/ready` OK; `GET /api/v2/alerts` = `DrillFiring1..3` active; silence `37715b5f-…` active; posting `drill-silenced` → 200 and not stored (0 rows); control alert `DrillPostMove` → 200, stored; `pg_stat_ssl` for AMP's backends `t | TLSv1.3 | 6`; AMP log `Connecting to PostgreSQL host=192.168.97.4 database=amp user=amp ssl_mode=require`, `Database migrations completed successfully`, `Silence store rehydrated … silences=1`; 0 log lines with `NOT encrypted`. Pod env: `DATABASE_HOST/PORT/DATABASE/USERNAME/SSL_MODE` from values, `DATABASE_PASSWORD` from `secretKeyRef amp-extdb/password`, no `DATABASE_ALLOW_INSECURE_SSL_IN_PRODUCTION`, no `STORAGE_FILESYSTEM_PATH`.

## 4. env beats `database:` in configFile (premise 1, rev 6)

`helm upgrade` with the external DB values plus `configFile.enabled: true` and a `database:` section pointing at `configfile-db.invalid:5433`, db/user `from_configfile`, `ssl_mode: disable`. The mounted `/etc/amp/config.yaml` contains that section; AMP logs `Connecting to PostgreSQL host=192.168.97.4 database=amp user=amp ssl_mode=require`, `/-/ready` OK, counts unchanged. `externalDatabase` wins.

## 5. Rollback to the bundled PostgreSQL (rev 7–8)

```sh
helm rollback amp 1 -n amp                    # rev 7: FAILED, same revision-annotation conflict (rev 1 was rendered by the unfixed chart)
helm rollback amp 1 -n amp --force-conflicts  # rev 8: "Rollback was a success!"
```

- The failed rev 7 had already re-created `sts/amp-postgres` before failing on the Deployment; AMP itself stayed on the external DB until rev 8.
- Rev 8: `sts/amp-postgres` on the same PVC (uid `f86231d4-…`); counts 5 / 1 / 0, goose unchanged; AMP env back to `amp-postgres`, `sslmode=disable` + the insecure-SSL flag; `GET /api/v2/alerts` = `DrillFiring1..3`; silence active. `DrillPostMove`, written to the external DB after the switch, is not there — as the Spec says.
- `--force-conflicts` takes the annotation over once; the Deployment controller sets it again on the next rollout (it read `4` afterwards).

## 6. Second pass — the runbook sequence, fixed chart (rev 9–10)

Fresh empty database `amp2` owned by `amp` (created by the superuser). One more alert `DrillBeforeMove` posted right before the move; bundled counts 6 / 1 / 0.

```sh
set -o pipefail
kubectl -n amp scale deploy/amp --replicas=0
kubectl -n amp wait --for=delete pod -l app.kubernetes.io/instance=amp,app.kubernetes.io/component=application --timeout=120s   # +7 s
kubectl -n amp exec amp-postgres-0 -c postgres -- sh -c 'pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Fc' > amp-move.dump        # rc 0
kubectl -n amp exec -i amp-postgres-0 -c postgres -- pg_restore -f /dev/null < amp-move.dump                                          # rc 0, 88857 bytes
shasum -a 256 amp-move.dump                                                                                                           # 45db0a05…0500
helm upgrade amp helm/amp -n amp -f drill-values.yaml -f move-values.yaml --set autoscaling.enabled=false --set replicaCount=0       # rev 9, +10 s
# PGHOST=192.168.97.4 PGUSER=amp PGSSLMODE=require PGPASSWORD=<redacted>
pg_restore -l amp-move.dump | grep -v 'EXTENSION.* pg_stat_statements' > amp.list                                                     # 2 entries excluded
pg_restore --no-owner --no-privileges --single-transaction --exit-on-error -L amp.list -d amp2 amp-move.dump                           # rc 0, +16 s
helm upgrade amp helm/amp -n amp -f drill-values.yaml -f move-values.yaml                                                             # rev 10
kubectl -n amp rollout status deploy/amp --timeout=300s                                                                               # +28 s total
```

Rev 9 upgraded from rev 8 (the forced rollback to an unfixed-chart revision) without a conflict.

Checks (runbook § 4): `/-/ready` OK; alerts `DrillBeforeMove, DrillFiring1..3` active (nothing lost between the stop and the dump); silence `37715b5f-…` active; `drill-silenced` → 200, 0 rows; `DrillPostMove` → 200, stored; counts 7 / 1 / 0 (6 moved + the control alert); goose max `20260423000000`, 10 rows, max `tstamp 13:38:48` (original install — no migration re-applied); AMP backends `pg_stat_ssl` `t | TLSv1.3 | 5`; 0 `NOT encrypted` log lines; PVC `postgres-data-amp-postgres-0` (uid `f86231d4-…`) still `Bound` — `helm upgrade` does not delete it.

## 7. Backup and restore of the external database (runbook § "External PostgreSQL")

Client environment `PGHOST/PGUSER/PGSSLMODE=require/PGPASSWORD`, AMP running on `amp2`.

| Case | Command | Result |
|---|---|---|
| backup | `pg_dump -Fc -d amp2 -f amp-ext.dump`; `pg_restore -f /dev/null amp-ext.dump` | rc 0 / rc 0, 85476 bytes; no `pg_stat_statements` in the dump |
| restore into a fresh empty DB (`amp3`, owner `amp`), no `--clean` | `pg_restore --no-owner --no-privileges --single-transaction --exit-on-error -d amp3 amp-ext.dump` | rc 0; 7 / 1 / 0, goose 10 rows (= source) |
| `--clean` as the owner role, extensions owned by `amp` | `pg_restore --clean --if-exists …` into `amp3` | rc 0 — `DROP EXTENSION` works for the extension owner |
| extensions pre-created by the superuser (`amp4`: `uuid-ossp`, `btree_gin`, `pg_trgm` as `postgres`), no `--clean` | `pg_restore --no-owner --no-privileges …` | rc 1: `must be owner of extension btree_gin` on `COMMENT ON EXTENSION` |
| same, `--clean` | | rc 1: `must be owner of extension uuid-ossp` on `DROP EXTENSION` |
| `amp4` with the list filter | `pg_restore -l amp-ext.dump \| grep -v -e 'EXTENSION.* pg_stat_statements' -e 'COMMENT - EXTENSION' > amp.list`; `pg_restore … -L amp.list -d amp4 amp-ext.dump` | rc 0 (3 entries excluded); 7 / 1 / 0; extensions stay owned by `postgres` |
| bundled dump into a fresh empty DB (`amp5`) with the same filter | same two commands with `amp-move.dump` | rc 0 (5 entries excluded); 6 / 1 / 0; extensions created by `amp` |

So one restore recipe covers every case drilled: fresh empty database, no `--clean`, `--no-owner --no-privileges`, and a `-L` list without `pg_stat_statements` and without the extension comments. `--clean` fails whenever the restoring role does not own the extensions, which is the usual case on a managed service.

## 8. Cleanup

`k3d cluster delete amp-extdb`; container `amp-extdb-pg` stopped and removed. `k3d cluster list` / `docker ps -a` — no `amp-extdb`; `kubectl config current-context` = `k3d-sema-dev`; no `k3d-amp-extdb` context left in the kubeconfig.

Release history (rev / status / description):

```text
1 superseded Install complete
2 superseded Upgrade complete
3 failed Upgrade "amp" failed: conflict occurred while applying object amp/amp apps/v1, Kind=Deployment: Apply failed w
4 failed Upgrade "amp" failed: conflict occurred while applying object amp/amp apps/v1, Kind=Deployment: Apply failed w
5 superseded Upgrade complete
6 superseded Upgrade complete
7 failed Rollback "amp" failed: conflict occurred while applying object amp/amp apps/v1, Kind=Deployment: Apply failed
8 superseded Rollback to 1
9 superseded Upgrade complete
10 deployed Upgrade complete
```

## 9. Third pass — § 3c with HPA and the final runbook text (deep-review R5, R6, R9)

Date: 2026-10-16. Fresh k3d `amp-extdb` (same setup as above), image `amp:extdb-drill` rebuilt from `ef30631` plus the review fixes in the working tree (sslMode enum, `fromYaml` guard, IPv6 in `DSN()`). Fresh `postgres:16.15` container with TLS, role `amp` (`rolsuper=f, rolcreatedb=f, rolcreaterole=f`), empty database `amp6` owned by `amp`. Password `<redacted>`, 23 characters containing `/ % # ? space @ :` and `%41`.

Values (`hpa-values.yaml`): `autoscaling.enabled: true`, `minReplicas: 1`, `maxReplicas: 2`, local image, no prometheus-operator CRDs (as above); nothing else overridden. External DB values: `move-values.yaml` with `database: amp6`.

Source (rev 1, bundled, HPA on): seeded 3 firing, 2 resolved, 1 silence `86e301d7-…` on `alertname=drill-silenced`, then `DrillBeforeMove`. Counts 6 / 1 / 0, goose max `20260423000000`, 10 rows, max `tstamp 14:35:36`.

The § 3c sequence as written in the runbook:

```sh
printf '%s' "$PASSWORD" > pwfile; kubectl -n amp create secret generic amp-extdb --from-file=password=pwfile; rm pwfile
# Secret value 23 bytes = the password, no trailing newline
kubectl -n amp scale deploy/amp --replicas=0
kubectl -n amp wait --for=delete pod -l app.kubernetes.io/instance=amp,app.kubernetes.io/component=application --timeout=120s   # +9 s
kubectl -n amp exec amp-postgres-0 -c postgres -- sh -c 'pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Fc' > amp-move6.dump       # rc 0
kubectl -n amp exec -i amp-postgres-0 -c postgres -- pg_restore -f /dev/null < amp-move6.dump                                         # rc 0, 88817 bytes
helm upgrade amp helm/amp -n amp -f hpa-values.yaml -f move-values.yaml --set autoscaling.enabled=false --set replicaCount=0          # rev 2, +13 s
# spec.replicas = 0; `kubectl get hpa` right after the upgrade (no --wait) still listed amp. Rev 2 renders no HPA, so Helm deletes it; not rechecked. Rev 3 renders it again
pg_restore -l amp-move6.dump | grep -v -e 'EXTENSION.* pg_stat_statements' -e 'COMMENT - EXTENSION' > amp.list
pg_restore --no-owner --no-privileges --single-transaction --exit-on-error -L amp.list -d "$PGDATABASE" amp-move6.dump               # rc 0, +22 s
helm upgrade amp helm/amp -n amp -f hpa-values.yaml -f move-values.yaml                                                               # rev 3
kubectl -n amp rollout status deploy/amp --timeout=300s                                                                               # +45 s total
```

The two-pattern filter excluded 5 entries: `EXTENSION - pg_stat_statements` and the `COMMENT - EXTENSION` entries of `btree_gin`, `pg_stat_statements`, `pg_trgm`, `uuid-ossp`.

HPA after the last `helm upgrade` (R5): `spec.replicas` = 1, `readyReplicas` = 1, the same 20 s later. HPA conditions `AbleToScale=True ReadyForNewScale`, `ScalingActive=True ValidMetricFound`, `ScalingLimited=False DesiredWithinRange`. The upgrade back to HPA values drops the `replicas: 0` the previous revision rendered, so the Deployment does not stay at 0 and the HPA is not `ScalingDisabled`.

Checks (runbook § 4): `/-/ready` OK; alerts `DrillBeforeMove, DrillFiring1..3` active; silence `86e301d7-…` active, rehydrated (`silences: 1`); `drill-silenced` → 200, 0 rows; `DrillPostMove` → 200, stored; counts 7 / 1 / 0; goose max `20260423000000`, 10 rows, max `tstamp 14:35:36` (no migration re-applied); pod env `DATABASE_HOST=192.168.97.4`, `DATABASE_DATABASE=amp6`, `DATABASE_USERNAME=amp`, `DATABASE_SSL_MODE=require`, `DATABASE_PASSWORD` from `amp-extdb/password`; AMP log `Connecting to PostgreSQL` with host `192.168.97.4`, `ssl_mode: require`; 0 `NOT encrypted` lines; AMP backends `pg_stat_ssl` `t | TLSv1.3 | 6`; PVC `postgres-data-amp-postgres-0` still `Bound`.

Release history: `1 superseded Install`, `2 superseded Upgrade`, `3 deployed Upgrade` — no conflicts.

Cleanup: `k3d cluster delete amp-extdb`, container removed; no `amp-extdb` cluster, container or kubeconfig context left; `kubectl config current-context` = `k3d-sema-dev`.
