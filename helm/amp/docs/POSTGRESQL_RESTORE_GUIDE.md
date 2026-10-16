# PostgreSQL Backup & Restore Runbook

How to back up and restore AMP's PostgreSQL database with `pg_dump` / `pg_restore`.
The decision behind it is ADR-016 in `docs/06-planning/DECISIONS.md`. In short:

- **Production:** use an external PostgreSQL with backups, PITR and HA on the operator's side: a managed service with PITR, or CloudNativePG with barman/S3. Use the provider's backup/PITR tooling first; this runbook is the portable fallback.
- **Bundled PostgreSQL** (`postgresql.enabled: true`) is for dev/pilot: a single instance, **no automated backups**, no PITR, no PDB. If its data matters to you, schedule the backup below yourself and keep the dumps outside the cluster.

The commands in sections 2–3b and 4 were run end to end on a k3d cluster with the bundled `postgres:16.15` and the chart's default HPA: a rollback of a live installation and a rebuild after deleting the namespace. In both cases the data and silences came back and migrations were not re-applied. The rebuild took under a minute with a small database. "External PostgreSQL" and 3c were run on k3d against an external `postgres:16.15` with TLS (`sslmode=require`) and an owner role without superuser rights. That covered the move from the bundled database (with HPA off and with HPA on), a rollback to it, and a restore both into a fresh database and into one with provider-installed extensions.

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

### External PostgreSQL

For a database configured through `externalDatabase.*`. Prefer the provider's PITR: it also covers changes made after the last dump. The commands below are the portable fallback.

Run them from any host with PostgreSQL client tools at least as new as the server. Pass the connection through the environment, with the password in `~/.pgpass` or `PGPASSWORD`, never on the command line:

```sh
export PGHOST=<host> PGPORT=5432 PGUSER=<role> PGDATABASE=<database> PGSSLMODE=require
```

Backup (AMP may keep running):

```sh
set -o pipefail
DUMP=amp-$(date -u +%Y%m%dT%H%M%SZ).dump
pg_dump -Fc -f "$DUMP"
pg_restore -f /dev/null "$DUMP"                       # verify
sha256sum "$DUMP" >> amp-dumps.sha256                 # macOS: shasum -a 256
```

Restore into a freshly created, empty database owned by AMP's role. Create it as an admin, for example `CREATE DATABASE <new database> OWNER <role>;`, or in the provider's console. Stop AMP as in 3a first:

```sh
pg_restore -l "$DUMP" | grep -v -e 'EXTENSION.* pg_stat_statements' -e 'COMMENT - EXTENSION' > amp.list
pg_restore --no-owner --no-privileges --single-transaction --exit-on-error -L amp.list -d <new database> "$DUMP"
```

Then point AMP at it: `helm upgrade` with `externalDatabase.database=<new database>`, and scale AMP back.

- **No `--clean`.** It has to drop the dump's extensions, and only their owner can. AMP's migrations create `uuid-ossp` and `btree_gin`; dumps of the bundled PostgreSQL also contain `pg_trgm`, created by its init script. On a managed service they usually belong to the provider's admin role, so `--clean` fails and the single transaction rolls everything back.
- **The `-L` list** skips two kinds of entries. One is `pg_stat_statements`, which only a superuser can create; dumps of the bundled PostgreSQL contain it, and AMP does not need it. The other is comments on extensions, which only the extension's owner may set: without that filter, a restore fails on a database where the provider pre-installed the extensions. Extensions that are missing are created by the restoring role. `uuid-ossp`, `btree_gin` and `pg_trgm` are trusted extensions, so the database owner may create them.
- **`--no-privileges`** drops the source's `GRANT`s. A dump of the bundled database grants to its role `alert_history`, which does not exist on another server.

### 3c. Move from the bundled to an external PostgreSQL

`helm upgrade` to `postgresql.enabled: false` + `externalDatabase.*` removes the bundled StatefulSet and points AMP at the external database, **which starts empty**. Move the data while AMP is stopped. Prepare the external side first: an empty database owned by AMP's role, and a Secret with that role's password in the release namespace. Create the Secret from a file without a trailing newline: `--from-file` keeps a `\n` as part of the password, and `--from-literal` puts the password into shell history.

```sh
read -rs AMP_DB_PASSWORD                                # prompts; not echoed, not in history
(umask 077; printf '%s' "$AMP_DB_PASSWORD" > ./amp-db-password)    # no trailing newline
kubectl -n <namespace> create secret generic <secret> --from-file=password=./amp-db-password
rm ./amp-db-password
```

Then, with your values file switched to the external database (`postgresql.enabled: false`, `externalDatabase.host/database/username/existingSecret`):

```sh
set -o pipefail
kubectl -n <namespace> scale deploy/<fullname> --replicas=0
kubectl -n <namespace> wait --for=delete pod \
  -l app.kubernetes.io/instance=<release>,app.kubernetes.io/component=application --timeout=120s

# dump only now: nothing is written after it
DUMP=amp-move-$(date -u +%Y%m%dT%H%M%SZ).dump
kubectl -n <namespace> exec <fullname>-postgres-0 -c postgres -- \
  sh -c 'pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Fc' > "$DUMP"
kubectl -n <namespace> exec -i <fullname>-postgres-0 -c postgres -- pg_restore -f /dev/null < "$DUMP"
sha256sum "$DUMP" >> amp-dumps.sha256                 # macOS: shasum -a 256

# switch the release, AMP stays stopped
helm upgrade <release> <chart> -n <namespace> -f <your-values.yaml> \
  --set autoscaling.enabled=false --set replicaCount=0

# restore with PGHOST/PGUSER/PGDATABASE/PGSSLMODE as above
pg_restore -l "$DUMP" | grep -v -e 'EXTENSION.* pg_stat_statements' -e 'COMMENT - EXTENSION' > amp.list
pg_restore --no-owner --no-privileges --single-transaction --exit-on-error -L amp.list -d "$PGDATABASE" "$DUMP"

# back to your values
helm upgrade <release> <chart> -n <namespace> -f <your-values.yaml>
kubectl -n <namespace> rollout status deploy/<fullname> --timeout=300s
kubectl -n <namespace> get deploy/<fullname> -o jsonpath='{.spec.replicas}'    # must not be 0
```

- `rollout status` also succeeds at 0 replicas, hence the last check. With HPA the chart renders no `replicas`, so the upgrade drops the `0` of the previous step and the Deployment comes back with 1 replica (run with `minReplicas: 1`); the HPA then keeps it within `minReplicas`–`maxReplicas`. If it still shows `0`, run `kubectl -n <namespace> scale deploy/<fullname> --replicas=<N>`.
- Check as in section 4, with the `psql` queries run against the external server. AMP's connections should show `ssl = t` in `pg_stat_ssl`.
- The upgrade does not delete the bundled database's PVC `postgres-data-<fullname>-postgres-0`. Delete it yourself once the external database is verified. Until then, `helm rollback` to the revision with the bundled PostgreSQL brings it back on that PVC with the data as of the move. Anything AMP wrote to the external database after the move does not come back.
- If `pg_restore` fails, the external database stays empty. Do not scale AMP up. `helm rollback` returns to the bundled database.
- **Helm 4:** chart versions before this change hard-coded the Deployment's `deployment.kubernetes.io/revision` annotation. A `helm rollback` to a revision rendered by such a version fails with `conflict with "k3s" … deployment.kubernetes.io/revision` (the field manager's name depends on the distribution). Run it once with `--force-conflicts`; the Deployment controller resets the annotation itself. Only do this if the error names this annotation alone: `--force-conflicts` takes over every conflicting field.

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
