# V2 — upgrade path main → branch (k3d)

Date: 2026-10-15. Cluster: k3d `amp-pg-research` (k3s, local-path StorageClass). AMP image `amp:pg-drill` (built from branch HEAD).
Values: drill values (image override, `autoscaling.enabled=false`, `replicaCount=1`, placeholder passwords, `monitoring.prometheusEnabled=false`, `postgresql.monitoring.prometheusRules.enabled=false` — monitoring off due to MONITORING-CRD-DEFAULT). No `postgresql.backup.*` keys set.

Sequence:

1. `helm install amp <main chart> -n amp` (rev 1) — without `--wait` (see "Side findings").
2. Seed: `POST /api/v2/alerts` (3 firing + 2 resolved), `POST /api/v2/silences` (matcher `alertname=drill-silenced`), `POST /api/v2/alerts` `drill-silenced`.
3. `helm upgrade amp helm/amp -n amp -f drill-values.yaml` (rev 2, branch chart).
4. `helm rollback amp 1` (rev 3).
5. `helm upgrade` branch chart again (rev 4).

Snapshot helper: `kubectl get pvc` (name/uid/phase), `kubectl get cronjob,pdb`, PG pod uid/restartCount/image, `psql -Atc "select count(*) ..."` for `alerts`, `silences`, `alert_investigations`.

## Snapshots

```text
== before-upgrade 09:34:14Z
NAME                           UID                                    STATUS
amp-postgres-backup            f7732bf9-3b31-477d-80aa-05b1aacfcc0a   Bound
amp-pvc                        0e91b941-a5af-4d4d-a278-ccff1d3c6b23   Pending
amp-valkey                     1f8ae15c-a2e8-4dd5-acb6-9360c67d7009   Bound
postgres-data-amp-postgres-0   77b8cd0d-77ee-4dff-85ca-a80a2653eec1   Bound
redis-data-amp-redis-0         fed81bb3-5daa-4327-be00-bba0063601f5   Bound
NAME                                SCHEDULE    TIMEZONE   SUSPEND   ACTIVE   LAST SCHEDULE   AGE
cronjob.batch/amp-postgres-backup   0 2 * * *   <none>     False     0        <none>          3m44s

NAME                                      MIN AVAILABLE   MAX UNAVAILABLE   ALLOWED DISRUPTIONS   AGE
poddisruptionbudget.policy/amp-postgres   1               N/A               0                     3m46s
pg pod uid=e3c943b6-f64e-439f-aa08-b456d5b363e6 restarts=1 0 image=postgres:16
counts alerts silences investigations: 5 1 0
rev 1 deployed amp-0.1.0
== after-upgrade 09:35:08Z
NAME                           UID                                    STATUS
amp-pvc                        0e91b941-a5af-4d4d-a278-ccff1d3c6b23   Pending
amp-valkey                     1f8ae15c-a2e8-4dd5-acb6-9360c67d7009   Bound
postgres-data-amp-postgres-0   77b8cd0d-77ee-4dff-85ca-a80a2653eec1   Bound
redis-data-amp-redis-0         fed81bb3-5daa-4327-be00-bba0063601f5   Bound
No resources found in amp namespace.
pg pod uid=e0d3c241-e696-4bc3-960d-0cc5af543bb7 restarts=0 0 image=postgres:16.15
counts alerts silences investigations: 5 1 0
rev 1 superseded amp-0.1.0
rev 2 deployed amp-0.1.0
== after-rollback 09:36:11Z
NAME                           UID                                    STATUS
amp-postgres-backup            31c9fa9e-43aa-44ea-9aa4-61f15d66e198   Bound
amp-pvc                        0e91b941-a5af-4d4d-a278-ccff1d3c6b23   Pending
amp-valkey                     1f8ae15c-a2e8-4dd5-acb6-9360c67d7009   Bound
postgres-data-amp-postgres-0   77b8cd0d-77ee-4dff-85ca-a80a2653eec1   Bound
redis-data-amp-redis-0         fed81bb3-5daa-4327-be00-bba0063601f5   Bound
NAME                                SCHEDULE    TIMEZONE   SUSPEND   ACTIVE   LAST SCHEDULE   AGE
cronjob.batch/amp-postgres-backup   0 2 * * *   <none>     False     0        <none>          38s

NAME                                      MIN AVAILABLE   MAX UNAVAILABLE   ALLOWED DISRUPTIONS   AGE
poddisruptionbudget.policy/amp-postgres   1               N/A               0                     40s
pg pod uid=c860bf6e-58df-4068-8600-b1428f034017 restarts=0 0 image=postgres:16
counts alerts silences investigations: 5 1 0
rev 1 superseded amp-0.1.0
rev 2 superseded amp-0.1.0
rev 3 deployed amp-0.1.0
== after-reupgrade 09:36:55Z
NAME                           UID                                    STATUS
amp-pvc                        0e91b941-a5af-4d4d-a278-ccff1d3c6b23   Pending
amp-valkey                     1f8ae15c-a2e8-4dd5-acb6-9360c67d7009   Bound
postgres-data-amp-postgres-0   77b8cd0d-77ee-4dff-85ca-a80a2653eec1   Bound
redis-data-amp-redis-0         fed81bb3-5daa-4327-be00-bba0063601f5   Bound
No resources found in amp namespace.
pg pod uid=7b37d8bf-89f8-4d6b-b00b-431c18c6e607 restarts=0 0 image=postgres:16.15
counts alerts silences investigations: 5 1 0
rev 1 superseded amp-0.1.0
rev 2 superseded amp-0.1.0
rev 3 superseded amp-0.1.0
rev 4 deployed amp-0.1.0
```

Additional: `/-/ready` → 200 after rev 2; `helm upgrade` + PG StatefulSet rollout took 46 s.

## Result

| Check | rev 1 → rev 2 | rev 3 → rev 4 |
|---|---|---|
| CronJob `amp-postgres-backup` removed | yes | yes |
| PDB `amp-postgres` removed | yes | yes |
| PVC `amp-postgres-backup` removed | yes | yes |
| PVC `postgres-data-amp-postgres-0` uid unchanged (`77b8cd0d-…`) | yes | yes |
| PG pod recreated exactly once (new uid, restarts 0) | yes | yes |
| PG image `postgres:16` → `postgres:16.15` | yes | yes |
| counts alerts/silences/investigations = 5/1/0 | yes | yes |

Rollback (rev 3) restores CronJob, PDB and backup PVC; data on `postgres-data` intact (5/1/0).

**Migration note:** the upgrade deletes PVC `<release>-postgres-backup` together with its contents (rollback recreates it empty — new uid `31c9fa9e-…`). The bundled backup never produced a usable base backup (research: pg_basebackup always rejected by pg_hba), so no data is lost, but operators must be told explicitly.

## Side findings (pre-existing, out of scope)

- Valkey subchart pod `amp-valkey-*` stays in `Init:Error` (`exec /scripts/init.sh: no such file or directory`); with `--wait` the release goes FAILED. AMP uses `amp-redis` by default.
- PVC `amp-pvc` (`templates/pvc.yaml`) stays `Pending` forever: no consumer, local-path is `WaitForFirstConsumer`.
- Selector of Service `amp` and Deployment `amp` (`app.kubernetes.io/{name,instance}` only) also matches `amp-redis-0` and `amp-postgres-0`: Service endpoints = `amp-redis-0 amp-postgres-0 amp-<hash>`; `kubectl port-forward svc/amp` / `deploy/amp` / `logs deploy/amp` land on the redis pod. Traffic through the Service is load-balanced to pods that do not serve HTTP.
