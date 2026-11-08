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
