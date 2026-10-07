# Probe: LoadConfig / LoadConfigFromEnv against chart env (2026-10-07)

Method: temporary test in `go-app/internal/config` (deleted after the run) that
sets every env var from `chart-default-env.txt` (secrets taken from the rendered
Secret, not stored here), then calls `LoadConfig("/nonexistent/config.yaml")`
and `LoadConfigFromEnv()`, and checks each env name against `viper.AllKeys()`
(upper-cased, `.`→`_`).

## Default values (`profile: standard`)

- `LoadConfig(nonexistent)` → `failed to read config file: open /nonexistent/config.yaml: no such file or directory`
- env names unknown to viper: `SERVICE_NAME`, `SERVICE_VERSION` (no reader in Go code). All other 58 names map to a viper key with a default.
- `LoadConfigFromEnv()` → `config validation failed: database credentials validation failed: database SSL mode 'disable' is not allowed in production (use 'require' or 'verify-full')`
- with `DATABASE_SSL_MODE=require` added → `<nil>`; resulting config: `Server.Port=8080`, `Profile=standard`, `App.Environment=production`, `LLM.Enabled=true`, `LLM.BaseURL=https://llm-proxy.example.com`, `LLM.APIKey=""`, `Grouping.Enabled=false`, `Redis.Addr=amp-redis:6379`.

## `--set profile=lite`

- `LoadConfigFromEnv()` → `<nil>`.

## Render facts (same render)

- Secret `amp-secrets` has no `llm-api-key` key (`llm.apiKey: ""`), while Deployment `amp` references it via non-optional `secretKeyRef` (`templates/deployment.yaml:127-133`).
- `containerPort`/probes use `service.port: 8080`; the minimal fallback config listens on 9093.
