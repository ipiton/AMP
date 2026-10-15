# PostgreSQL Backup & Restore Runbook

How to back up and restore AMP's PostgreSQL database with `pg_dump` / `pg_restore`.
The decision behind it is ADR-016 in `docs/06-planning/DECISIONS.md`. In short:

- **Production:** use an external PostgreSQL with backups, PITR and HA on the operator's side: a managed service with PITR, or CloudNativePG with barman/S3. Use the provider's backup/PITR tooling first; this runbook is the portable fallback.
- **Bundled PostgreSQL** (`postgresql.enabled: true`) is for dev/pilot: a single instance, **no automated backups**, no PITR, no PDB. If its data matters to you, schedule the backup below yourself and keep the dumps outside the cluster.

The commands in sections 2–4 were run end to end on a k3d cluster with the bundled `postgres:16.15` and the chart's default HPA: a rollback of a live installation and a rebuild after deleting the namespace. In both cases the data and silences came back and migrations were not re-applied. The rebuild took under a minute with a small database. The external-PostgreSQL notes in section 3 were not drilled.

## 1. Placeholders and conventions

| Placeholder | Meaning |
|---|---|
| `<namespace>` | namespace of the Helm release |
| `<release>` | Helm release name |
| `<fullname>` | chart fullname, also the name of the AMP Deployment. It is `<release>` when the release name contains `amp` (or `nameOverride`), otherwise `<release>-amp` (or `<release>-<nameOverride>`); `fullnameOverride` replaces it. Look it up below |
| `<N>` | number of AMP replicas to return to in scenario 3a (`replicaCount`, or `autoscaling.minReplicas` with HPA) |

- Look up `<fullname>` from the bundled PostgreSQL StatefulSet, which is named `<fullname>-postgres`:

  ```sh
  kubectl -n <namespace> get sts -l app.kubernetes.io/instance=<release>,app.kubernetes.io/component=database \
    -o jsonpath='{.items[0].metadata.name}'      # prints <fullname>-postgres
  ```

- The commands run `pg_dump`/`pg_restore` **inside** the `postgres` container. They use its `$POSTGRES_USER`/`$POSTGRES_DB` over the local socket, so no password goes on the command line or into shell history.
- Address the AMP pods by label: `app.kubernetes.io/instance=<release>,app.kubernetes.io/component=application`. Do not use `svc/<fullname>` or `deploy/<fullname>` with `kubectl port-forward`/`logs`: their selector also matches the postgres and redis pods, and kubectl may pick one of those.
- Run the commands in `bash` or `zsh` with `set -o pipefail`, and stop at the first non-zero exit code.

## 2. Backup

```sh
set -o pipefail
DUMP=amp-$(date -u +%Y%m%dT%H%M%SZ).dump

kubectl -n <namespace> exec <fullname>-postgres-0 -c postgres -- \
  sh -c 'pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Fc' > "$DUMP"

# verify: reads the whole archive, fails on a truncated or corrupt file
kubectl -n <namespace> exec -i <fullname>-postgres-0 -c postgres -- pg_restore -f /dev/null < "$DUMP"

sha256sum "$DUMP" >> amp-dumps.sha256    # macOS: shasum -a 256 "$DUMP" >> amp-dumps.sha256
```

- Both commands must exit 0. If the `kubectl exec` stream breaks during a long dump, `pg_dump` fails and the file is incomplete; delete it and dump again. Do not keep a file that failed verification.
- AMP keeps running during the dump: `pg_dump` takes a consistent snapshot.
- Copy the dump and its checksum off the cluster: to object storage, or wherever your backups live. A dump on a PVC next to the database is lost together with the node or namespace.
- Scheduling, retention and encryption at rest are up to you, for example a CronJob in your own platform tooling or a CI job.

## 3. Restore

AMP must not run while `pg_restore` runs. Otherwise it applies migrations to a half-restored schema and keeps ingesting into tables that are being dropped. While AMP is down, `POST /api/v2/alerts` gets no answer. Prometheus re-sends active alerts periodically, so they arrive once AMP is back. A resolve notification sent only during the outage can be lost.

Start every restore by pointing `DUMP` at the file and checking it. Both checks must pass:

```sh
set -o pipefail
DUMP=<path to the dump>
grep " $DUMP\$" amp-dumps.sha256 | sha256sum -c -    # macOS: shasum -a 256 -c -
kubectl -n <namespace> exec -i <fullname>-postgres-0 -c postgres -- pg_restore -f /dev/null < "$DUMP"
```

All restores use `--single-transaction --exit-on-error`. If `pg_restore` fails half-way, for example because the dump is damaged, the transaction rolls back and the database stays as it was. Without these flags, `--clean` drops the existing tables and a failed restore leaves them empty.

### 3a. Roll a live installation back to a dump

Also for a lost PostgreSQL volume while the release still exists (for example, the node holding a local volume is gone): run the commented commands below too.

```sh
kubectl -n <namespace> scale deploy/<fullname> --replicas=0
kubectl -n <namespace> wait --for=delete pod \
  -l app.kubernetes.io/instance=<release>,app.kubernetes.io/component=application --timeout=120s

# only if the PostgreSQL volume was lost: drop the dead PVC, let the StatefulSet recreate an empty one
# kubectl -n <namespace> delete pvc postgres-data-<fullname>-postgres-0 --wait=false
# kubectl -n <namespace> delete pod <fullname>-postgres-0 --wait=true
# kubectl -n <namespace> rollout status sts/<fullname>-postgres --timeout=240s

kubectl -n <namespace> exec -i <fullname>-postgres-0 -c postgres -- \
  sh -c 'pg_restore -U "$POSTGRES_USER" -d "$POSTGRES_DB" --clean --if-exists --no-owner --single-transaction --exit-on-error' < "$DUMP"

kubectl -n <namespace> scale deploy/<fullname> --replicas=<N>
kubectl -n <namespace> rollout status deploy/<fullname> --timeout=240s
```

- Everything written after the dump is gone: alerts, silences and investigations return to the dump's state.
- With HPA enabled (default `autoscaling.enabled: true`), you do not need to touch the HPA. Kubernetes disables scaling while the Deployment is at 0 replicas (`ScalingActive False ScalingDisabled`) and resumes after the scale back to `<N>`.
- If AMP was upgraded after the dump was taken, `--clean` does not drop tables and columns from the newer migrations, and AMP would re-apply those migrations on top of them. In that case restore into an empty database instead. With AMP at 0 replicas, recreate the database, then run the `pg_restore` above:

  ```sh
  kubectl -n <namespace> exec <fullname>-postgres-0 -c postgres -- \
    sh -c 'dropdb --force -U "$POSTGRES_USER" "$POSTGRES_DB" && createdb -U "$POSTGRES_USER" "$POSTGRES_DB"'
  ```

### 3b. Rebuild after losing the namespace or the cluster

```sh
# install with AMP stopped: HPA off, 0 replicas; PostgreSQL comes up with an empty database
helm install <release> <chart> -n <namespace> --create-namespace -f <your-values.yaml> \
  --set autoscaling.enabled=false --set replicaCount=0
kubectl -n <namespace> rollout status sts/<fullname>-postgres --timeout=240s

kubectl -n <namespace> exec -i <fullname>-postgres-0 -c postgres -- \
  sh -c 'pg_restore -U "$POSTGRES_USER" -d "$POSTGRES_DB" --clean --if-exists --no-owner --single-transaction --exit-on-error' < "$DUMP"

# back to your values: replicas and HPA as configured
helm upgrade <release> <chart> -n <namespace> -f <your-values.yaml>
kubectl -n <namespace> rollout status deploy/<fullname> --timeout=300s
```

Both overrides are needed. With HPA enabled the chart does not render `replicas`, so `--set replicaCount=0` alone still starts AMP.

`pg_restore` should exit 0 with no output. With `--clean --if-exists`, objects that do not exist yet are skipped silently. `--no-owner` makes the restored objects belong to the chart's database user.

If `pg_restore` exits non-zero, the database is unchanged. Do not scale AMP up. Verify the dump (section 2) and try the previous one.

### External PostgreSQL (not drilled)

The same `pg_dump -Fc` / `pg_restore` pair should work against an external server from any host with PostgreSQL client tools at least as new as the server. Pass the connection details through `PGHOST`/`PGUSER`/`PGDATABASE` and `PGPASSWORD` or `~/.pgpass`. The scale-to-0 / scale-back steps are the same.

- Restore into a freshly created empty database and drop `--clean`. AMP's schema uses the extensions `uuid-ossp` and `btree_gin`. Without superuser rights, which managed services usually do not grant, `--clean` fails on `DROP EXTENSION`.
- Prefer the provider's PITR: it also covers changes made after the last dump.

## 4. Verify

```sh
POD=$(kubectl -n <namespace> get pod \
  -l app.kubernetes.io/instance=<release>,app.kubernetes.io/component=application -o name | head -1)
kubectl -n <namespace> port-forward "$POD" 18080:8080 &

curl -s localhost:18080/-/ready                       # OK
curl -s localhost:18080/api/v2/alerts                 # firing alerts from the dump are back
curl -s localhost:18080/api/v2/silences               # silences from the dump, state "active" until they expire

# row counts match the source (record them when you take the dump)
kubectl -n <namespace> exec <fullname>-postgres-0 -c postgres -- \
  sh -c 'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Atc "select count(*) from alerts; select count(*) from silences; select count(*) from alert_investigations;"'

# migrations were not re-applied: the newest row predates the restore
kubectl -n <namespace> exec <fullname>-postgres-0 -c postgres -- \
  sh -c 'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Atc "select max(version_id), max(tstamp) from goose_db_version;"'
```

AMP drops a firing alert that matches an active silence at ingest. Such an alert is accepted (HTTP 200) but not stored, and it does not appear in `GET /api/v2/alerts`. To check that a restored silence works, re-send an alert it matches and confirm it does not show up.

## 5. Limitations

- **No PITR.** RPO equals the interval between dumps: anything written after the last dump is lost.
- **RTO** is dominated by pod start-up and dump size. In the drill: 54–56 s from `helm install` to ready, 29–43 s for an in-place rollback; dumps under 100 KB.
- **No automation in the chart.** It does not schedule, ship, rotate or encrypt backups. `postgresql.backup.enabled: true` fails the render on purpose (ADR-016).
- **Downtime.** The bundled database is a single instance: a restart, node drain or restore makes ingest return errors until it is back.
