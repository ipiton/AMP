# Manual smoke: amp-upstream + PostgreSQL 16 (2026-11-08)

Скрипт: бинарь из ветки, postgres:16-alpine в Docker, `deploy/smoke/alertmanager.yml`. Шаги: (1) без `--amp.config.file`; (2) с PG: POST в `api/v2`, чтение по FNV и SHA, investigation до и после вставки строки, 400, 405, метрика; (3) строка «до миграции» с NULL `upstream_fingerprint` и NULL `starts_at` — backfill на старте и поиск по FNV; (4) версия goose. Первый прогон дал 500 на шаге 3 (NULL `starts_at`), исправлено в `AlertRecordBy*` до этого прогона.

```text
== 1. no AMP config: 503
{"error":"alert history is unavailable: it needs PostgreSQL (--amp.config.file with profile standard and a reachable database)"}
 [503]
/api/v2/status [200]
{"code":404,"message":"path /api/v2/inhibitions was not found"} [404]
== 2. with PostgreSQL
POST [200]
api/v2 fingerprint: acd4077c98b42680
{"fingerprint":"acd4077c98b42680","amp_fingerprint":"b47d46ae86a22dbe18238b751af08fc8ab6dd81fb5f0623453e6855e2e630742","alertname":"SmokeAPI","status":"firing","labels":{"alertname":"SmokeAPI","instance":"a","severity":"warning"},"annotations":{"summary":"s"},"starts_at":"2026-11-08T19:28:28.155011Z","ends_at":"2026-11-08T19:33:28.155011Z","first_seen_at":"2026-11-08T19:28:28.158362Z","last_event_at":"2026-11-08T19:28:28.157651Z"}
 [200]
by SHA [200]
{"error":"no investigation for this fingerprint"}
 [404]
{"id":"ebf601ad-c3b2-4e9f-9e0e-ff16649c5285","fingerprint":"acd4077c98b42680","amp_fingerprint":"b47d46ae86a22dbe18238b751af08fc8ab6dd81fb5f0623453e6855e2e630742","status":"completed","retry_count":0,"queued_at":"2026-11-08T19:28:30.417233Z","created_at":"2026-11-08T19:28:30.417233Z","updated_at":"2026-11-08T19:28:30.417233Z","result":{"summary":"smoke summary","findings":null,"recommendations":null,"confidence":0}}
 [200]
{"error":"fingerprint must be 16 hex characters (as in api/v2) or 64 (AMP's SHA-256)"}
 [400]
POST amp [405]
amp_api_request_duration_seconds_count{code="200",handler="alert",method="get"} 2
amp_api_request_duration_seconds_count{code="200",handler="investigation",method="get"} 1
amp_api_request_duration_seconds_count{code="400",handler="alert",method="get"} 1
== 3. legacy row (NULL upstream_fingerprint) is backfilled on start
1
SmokeAPI|acd4077c98b42680
Legacy|2f4d1024e3a0728c
"msg":"alert history: upstream fingerprints backfilled","rows":1
legacy by FNV [200]
== 4. migration down/up
20261108000000
```

## Раунд 2: после deep-review (2026-11-08)

Тот же скрипт плюс проверки: 405/404 в JSON, валидность индекса, две версии goose, `Warn` при удалённом индексе, префикс `/a{b}`. В шаге 5 panic даёт сам upstream: `api.(*API).Register` в `alertmanager@v0.34.1/api/api.go:180` регистрирует `"/a{b}/api/v2/"` в `ServeMux`. Такой `--web.external-url` upstream тоже не запускает, так что F3 на практике недостижим.

```text
== 1. no AMP config: 503
{"error":"alert history is unavailable: it needs PostgreSQL (--amp.config.file with profile standard and a reachable database)"}
 [503]
/api/v2/status [200]
{"code":404,"message":"path /api/v2/inhibitions was not found"} [404]
== 2. with PostgreSQL
POST [200]
api/v2 fingerprint: acd4077c98b42680
{"fingerprint":"acd4077c98b42680","amp_fingerprint":"b47d46ae86a22dbe18238b751af08fc8ab6dd81fb5f0623453e6855e2e630742","alert_name":"SmokeAPI","status":"firing","labels":{"alertname":"SmokeAPI","instance":"a","severity":"warning"},"annotations":{"summary":"s"},"starts_at":"2026-11-08T19:43:37.326695Z","ends_at":"2026-11-08T19:48:37.326695Z","first_seen_at":"2026-11-08T19:43:37.33388Z","last_event_at":"2026-11-08T19:43:37.330412Z"}
 [200]
by SHA [200]
{"error":"no investigation for this fingerprint"}
 [404]
{"id":"b5a29ad1-3596-4f3d-9db8-45469b5999ce","fingerprint":"acd4077c98b42680","amp_fingerprint":"b47d46ae86a22dbe18238b751af08fc8ab6dd81fb5f0623453e6855e2e630742","status":"completed","retry_count":0,"queued_at":"2026-11-08T19:43:39.592725Z","created_at":"2026-11-08T19:43:39.592725Z","updated_at":"2026-11-08T19:43:39.592725Z","result":{"summary":"smoke summary","findings":null,"recommendations":null,"confidence":0}}
 [200]
{"error":"fingerprint must be 16 hex characters (as in api/v2) or 64 (AMP's SHA-256)"}
 [400]
HTTP/1.1 405 Method Not Allowed
Allow: GET, HEAD
Content-Type: application/json
{"error":"method not allowed"}
 [405]
{"error":"not found"}
 unknown [404]
{"error":"not found"}
 trailing slash [404]
index valid: true
0
amp_api_request_duration_seconds_count{code="200",handler="alert",method="get"} 2
amp_api_request_duration_seconds_count{code="200",handler="investigation",method="get"} 1
amp_api_request_duration_seconds_count{code="400",handler="alert",method="get"} 1
== 3. legacy row (NULL upstream_fingerprint) is backfilled on start
1
SmokeAPI|acd4077c98b42680
Legacy|2f4d1024e3a0728c
"msg":"alert history: upstream fingerprints backfilled","rows":1
legacy by FNV [200]
== 4. goose version
20261108000001
20261108000000
== 5. INVALID index warns; external URL with braces
not ready
{"time":"2026-11-08T20:43:41.782107+01:00","level":"WARN","msg":"alert history: index idx_alerts_upstream_fingerprint is missing or INVALID (an interrupted build); lookups by upstream fingerprint scan the table","fix":"DROP INDEX CONCURRENTLY IF EXISTS idx_alerts_upstream_fingerprint; CREATE INDEX CONCURRENTLY idx_alerts_upstream_fingerprint ON alerts(upstream_fingerprint);"}
panic: parsing "/a{b}/api/v2/": at offset 1: bad wildcard segment (must start with '{')
"msg":"alert history: index idx_alerts_upstream_fingerprint is missing or INVALID (an interrupted build); lookups by upstream fingerprint scan the table"
prefixed by FNV [000]
/private/tmp/claude-501/-Users-vit-code-own-AMP-amp--claude-worktrees-upstream-api-ui-b6a952/7a72e0cd-f659-490d-ba9c-b1d72a7182c2/scratchpad/smoke/run.sh: line 67: kill: (92786) - No such process
```
