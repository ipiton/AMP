# Binary check B1 (tasks.md 5.3) — 2026-10-14

Binary: `go build ./cmd/server` from `bugfix/helm-defaults-validate` @ `2aaea1b`, macOS arm64.
Each run in an empty directory under `env -i` (only `HOME` plus the listed variables), no
`config.yaml`. Env: `chart-default-env.txt` (default render: container `amp` env + ConfigMap
`amp-config`), DB password replaced by a 19-char test value; local overrides `SERVER_HOST=127.0.0.1`,
`SERVER_PORT=18095`.

| # | Setup | Exit | Log (WARN/ERROR) |
|---|---|---|---|
| d | env of the default chart render (includes `DATABASE_SSL_MODE=disable`, `DATABASE_ALLOW_INSECURE_SSL_IN_PRODUCTION=true`) | 1 (on DB) | no `failed to load configuration`; WARN `Database connection is NOT encrypted in production (database.allow_insecure_ssl_in_production=true): acceptable only for a database inside the cluster` `host=amp-postgres`; then ERROR `Failed to ping database` — `lookup amp-postgres: no such host` (no Postgres locally): validation passes |
| d-control | as (d) but `DATABASE_ALLOW_INSECURE_SSL_IN_PRODUCTION=false` | 1 | `failed to load configuration` — `config validation failed: database credentials validation failed: database SSL mode 'disable' is not allowed in production (use 'require' or 'verify-full')` (the error of `PROD-CONFIG-FILE-FALLBACK` run d) |

The test DB password did not appear in either log (`grep -c` = 0).

Compared with `tasks/archive/PROD-CONFIG-FILE-FALLBACK/evidence/binary-check.md` run (d): the same
default-render env now passes validation; the remaining exit is the missing database, which in the
cluster is the bundled `amp-postgres`. Confirms review finding F9.
