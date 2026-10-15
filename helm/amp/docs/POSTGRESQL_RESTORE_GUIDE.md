# PostgreSQL Backup & Restore Runbook

How to back up and restore AMP's PostgreSQL database with `pg_dump` / `pg_restore`.
The decision behind it is ADR-016 in `docs/06-planning/DECISIONS.md`. In short:

- **Production:** use an external PostgreSQL ≥ 16 with backups, PITR and HA on the operator's side — a managed service with PITR, or CloudNativePG with barman/S3. Use the provider's backup/PITR tooling first; this runbook is the portable fallback.
- **Bundled PostgreSQL** (`postgresql.enabled: true`) is for dev/pilot: a single instance, **no automated backups**, no PITR, no PDB. If you run it with data you care about, schedule the backup below yourself and keep the dumps outside the cluster.

Every command in sections 2–4 was run end to end on a k3d cluster (chart with bundled `postgres:16.15`): the data and silences came back, and migrations were not re-applied. Recovery from a total loss took 56 s with a small database.

## 1. Placeholders and conventions

| Placeholder | Meaning |
|---|---|
| `<namespace>` | namespace of the Helm release |
| `<release>` | Helm release name |
| `<fullname>` | chart fullname: `<release>` if the release name contains `amp`, otherwise `<release>-amp` (or `fullnameOverride`) |
| `<N>` | number of AMP replicas to return to (`replicaCount`, or `autoscaling.minReplicas` with HPA) |

- The commands run `pg_dump`/`pg_restore` **inside** the `postgres` container. They use its `$POSTGRES_USER`/`$POSTGRES_DB` over the local socket, so no password goes on the command line or into shell history.
- Address the AMP pods by label: `app.kubernetes.io/instance=<release>,app.kubernetes.io/component=application`. Do not use `svc/<fullname>` or `deploy/<fullname>` with `kubectl port-forward`/`logs`: their selector also matches the postgres and redis pods, and kubectl may pick one of those.
- Use a `pg_restore` that is at least as new as the `pg_dump` that wrote the file. With the commands below both come from the same image.

## 2. Backup

```sh
kubectl -n <namespace> exec <fullname>-postgres-0 -c postgres -- \
  sh -c 'pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Fc' > amp-$(date -u +%Y%m%dT%H%M%SZ).dump
shasum -a 256 amp-*.dump > amp-dumps.sha256

# sanity check: the archive is readable and contains table data
kubectl -n <namespace> exec -i <fullname>-postgres-0 -c postgres -- \
  pg_restore --list < amp-<timestamp>.dump | grep -c 'TABLE DATA'
```

- AMP keeps running during the dump: `pg_dump` takes a consistent snapshot.
- Copy the dump and its checksum off the cluster: to object storage, or wherever your backups live. A dump on a PVC next to the database is lost together with the node or namespace.
- Scheduling, retention and encryption at rest are up to you, for example a CronJob in your own platform tooling or a CI job.

## 3. Restore

AMP must not run while `pg_restore` runs. Otherwise it applies migrations to a half-restored schema and keeps ingesting into tables that are being dropped. While AMP is scaled to 0, `POST /api/v2/alerts` gets no answer: senders that retry (Prometheus/Alertmanager do) resend afterwards, others lose those alerts.

### 3a. Roll a live installation back to a dump

```sh
kubectl -n <namespace> scale deploy/<fullname> --replicas=0
kubectl -n <namespace> wait --for=delete pod \
  -l app.kubernetes.io/instance=<release>,app.kubernetes.io/component=application --timeout=120s

kubectl -n <namespace> exec -i <fullname>-postgres-0 -c postgres -- \
  sh -c 'pg_restore -U "$POSTGRES_USER" -d "$POSTGRES_DB" --clean --if-exists --no-owner' < amp-<timestamp>.dump

kubectl -n <namespace> scale deploy/<fullname> --replicas=<N>
kubectl -n <namespace> rollout status deploy/<fullname> --timeout=240s
```

Everything written after the dump is gone: alerts, silences and investigations return to the dump's state.

With HPA enabled (default `autoscaling.enabled: true`), you do not need to touch the HPA. Kubernetes disables scaling while the Deployment is at 0 replicas (`ScalingActive False ScalingDisabled`) and resumes after the scale back to `<N>`.

### 3b. Rebuild after losing the database (PVC, namespace or cluster)

```sh
# install with AMP at 0 replicas: PostgreSQL comes up with an empty database
helm install <release> <chart> -n <namespace> --create-namespace -f <your-values.yaml> --set replicaCount=0
kubectl -n <namespace> rollout status sts/<fullname>-postgres --timeout=240s

kubectl -n <namespace> exec -i <fullname>-postgres-0 -c postgres -- \
  sh -c 'pg_restore -U "$POSTGRES_USER" -d "$POSTGRES_DB" --clean --if-exists --no-owner' < amp-<timestamp>.dump

kubectl -n <namespace> scale deploy/<fullname> --replicas=<N>
kubectl -n <namespace> rollout status deploy/<fullname> --timeout=240s
```

Afterwards, run a normal `helm upgrade` with your values (without `--set replicaCount=0`) so the release matches them again.

`pg_restore` should exit 0 with no warnings. With `--clean --if-exists`, objects that do not exist yet are skipped silently. `--no-owner` makes the restored objects belong to the chart's database user.

### External PostgreSQL

The same `pg_dump -Fc` / `pg_restore --clean --if-exists --no-owner` pair works against an external server from any host with PostgreSQL ≥ 16 client tools. Pass the connection details through `PGHOST`/`PGUSER`/`PGDATABASE` and `PGPASSWORD` or `~/.pgpass`. The scale-to-0 / scale-back steps are the same. The drill covered only the bundled database. For an external server, prefer the provider's PITR, which also covers changes made after the last dump.

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

- **No PITR.** RPO equals the interval between dumps; anything written after the last dump is lost.
- **RTO** is dominated by pod start-up and dump size. The drill measured 56 s from `helm install` to ready, for a dump of under 100 KB.
- **No automation in the chart.** It does not schedule, ship, rotate or encrypt backups. `postgresql.backup.enabled: true` fails the render on purpose (ADR-016).
- **Downtime.** The bundled database is a single instance: a restart, node drain or restore makes ingest return errors until it is back. Clients must retry.
