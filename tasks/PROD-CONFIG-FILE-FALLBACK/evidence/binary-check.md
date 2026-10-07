# Binary check (tasks.md 1.4, 2.2) — 2026-10-07

Binary: `go build ./cmd/server` from branch `bugfix/prod-config-file-fallback`
(working tree after steps 1.1–1.3), macOS arm64. Each run in an empty
directory under `env -i` (only `HOME` plus the listed variables).

| # | Setup | Exit | Log (ERROR/INFO) |
|---|---|---|---|
| a | no `config.yaml`, `PROFILE=lite STORAGE_BACKEND=filesystem STORAGE_FILESYSTEM_PATH=… SERVER_HOST=127.0.0.1 SERVER_PORT=18093` | running | INFO `no config file, using environment and defaults` `path=config.yaml`; `GET :18093/-/healthy` → 200, `:9093` → connection refused (env port applied, old fallback port gone) |
| b | `AMP_CONFIG_FILE=/nonexistent.yaml` | 1 | `failed to load configuration` — `config file "/nonexistent.yaml" from AMP_CONFIG_FILE does not exist` |
| c | `AMP_CONFIG_FILE=bad.yaml` (`server:\n  port: [unclosed`) | 1 | `failed to load configuration` — `failed to read config file: While parsing config: yaml: line 1: did not find expected ',' or ']'` |
| d | env of the default chart render (`chart-default-env.txt`, DB password replaced by a 19-char test value), no file | 1 | `failed to load configuration` — `config validation failed: database credentials validation failed: database SSL mode 'disable' is not allowed in production (use 'require' or 'verify-full')` |
| e | `helm/amp/README.md` `values-small.yaml` snippet: rendered env + rendered `config.yaml` via `AMP_CONFIG_FILE` | 1 | same SSL-mode validation error as (d) |
| f | as (e) but `DATABASE_SSL_MODE=require` | running → exits on DB | config loads; `Failed to ping database` (no Postgres locally) — validation passes |

The test DB password did not appear in any log (`grep -c` = 0 for all runs).
