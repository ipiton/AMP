# Deep Review Findings: API среза 6 ADR-019

**Trigger classification:** mandatory (S, M; 5 сигналов Risk Profile)
**Reviewer perspective:** два независимых агента (general-purpose, не автор кода): A — security, data/migration, correctness, premises; B — contract, cross-boundary/lifecycle, runtime, maintainability. Только чтение.
**Reviewed at:** 2026-11-08
**Reviewed tree:** `feature/upstream-api-ui` @ `f0c8f89` (раунд 1)
**Verdict:** раунд 1 — `fix_required`; повторное ревью см. ниже (`review-verdict.json`)

Находки обоих ревьюеров сведены; если о чём-то сказали оба, в скобках указан второй.

## Findings

### F1 — Миграция держит ACCESS EXCLUSIVE на `alerts` всё время построения индекса
- **Severity:** major (A1; B m3)
- **Location:** `go-app/migrations/20261108000000_alerts_upstream_fingerprint.sql:10-11`
- **Issue:** `ADD COLUMN` и `CREATE INDEX` идут в одной транзакции goose. Блокировка `ADD COLUMN` держится до коммита, то есть на всё построение индекса, и блокирует и чтения, и записи `alerts`. Обоснование premise 6 (`cleanup_old_data`) ложное: retention никто не вызывает. Если не уложились в `migrationTimeout`, история выключается на каждом старте.
- **Recommendation:** две миграции: `ADD COLUMN` и `NO TRANSACTION` с `CREATE INDEX CONCURRENTLY`; учесть INVALID-индекс; исправить premise.
- **Disposition:** fix-here. Индекс вынесен в `20261108000001_alerts_upstream_fingerprint_index.sql` (`NO TRANSACTION`, `CONCURRENTLY IF NOT EXISTS`, down `DROP INDEX CONCURRENTLY`). На старте `UpstreamFingerprintIndexValid` даёт `Warn` с командой починки, если индекса нет или он INVALID. Premise 6, Rollout и CHANGELOG (ручное построение до выкатки) переписаны. Блокировку на живой БД проверит тест 3.3 (up/down/up).
- **Follow-up:** n/a

### F2 — Rollback в Spec ссылается на несуществующую `RollbackMigrations`; `RunMigrationsDown` опасна
- **Severity:** minor (A2; B m1)
- **Location:** `tasks/UPSTREAM-API-UI/Spec.md` § Rollback; `go-app/internal/database/migrations.go` (`RunMigrationsDown`)
- **Issue:** `goose.DownTo` получает число шагов там, где ожидает версию: `steps=1` откатывает всё. `docs/ROLLBACK_RUNBOOK.md` запрещает `goose down`.
- **Recommendation:** в Spec — откат образа без схемы; дефект записать.
- **Disposition:** fix-here (Spec) + defer-bug (код существовал до задачи).
- **Follow-up:** `docs/06-planning/BUGS.md` `MIGRATIONS-DOWN-STEPS-IS-VERSION`

### F3 — `--web.external-url` с `{` в пути роняет старт panic'ом
- **Severity:** minor (A3)
- **Location:** `go-app/internal/ampapi/ampapi.go` (`New`, шаблоны `ServeMux`)
- **Issue:** префикс вклеивался в шаблон `ServeMux`; `/a{b}` даёт panic `bad wildcard segment`, `/{x}` превращается в wildcard.
- **Recommendation:** не делать префикс частью синтаксиса шаблонов.
- **Disposition:** fix-here. `ServeMux` убран, путь разбирается вручную (`ServeHTTP`); тест с префиксом `/a{b}` — в write-tests 3.1. Smoke показал, что такой префикс роняет и сам upstream (`api.Register`, `alertmanager@v0.34.1/api/api.go:180`, panic `bad wildcard segment`), поэтому в бинаре находка недостижима. Ручной разбор оставлен: `ampapi` перестаёт зависеть от синтаксиса префикса, а 404 и 405 отдаются в JSON (F9). Evidence: `evidence/manual-smoke.md` § Раунд 2.
- **Follow-up:** n/a

### F4 — У AMP-маршрутов нет таймаута и лимита конкурентности
- **Severity:** minor (A4; B m2)
- **Location:** `go-app/cmd/amp-upstream/main.go` (`withAMPRoutes`), `go-app/internal/ampapi/ampapi.go`
- **Issue:** маршруты идут мимо `limitHandler` upstream; пул общий с записью истории; повисший GET держит соединение.
- **Recommendation:** таймаут на запрос; семафор или принятый риск.
- **Disposition:** fix-here: таймаут 10 с на запрос (`requestTimeout`). Лимит конкурентности — принятый риск (Spec § Security: соединения ограничены `MaxConns` пула, каждое — таймаутом). Трейсинг-middleware не добавлен: у AMP-слоя нет своих спанов.
- **Follow-up:** n/a

### F5 — Backfill на каждом старте проходит таблицу; нечитаемые labels дают `Warn` каждый раз
- **Severity:** minor (A5; B n3)
- **Location:** `go-app/internal/infrastructure/postgres_alert_records.go` (`unfilledBatch`)
- **Issue:** даже когда всё заполнено, на старте идёт проход `IS NULL … ORDER BY id`.
- **Recommendation:** partial index или sentinel.
- **Disposition:** reject (reason: `idx_alerts_upstream_fingerprint` — B-tree, а он индексирует NULL, так что `IS NULL` идёт по индексу. После backfill NULL-строк почти нет. Второй индекс ради одного запроса на старте не оправдан; строки с нечитаемыми labels — аномалия, которую `Warn` и должен показывать). Описано в Spec § Backfill. План запроса — `EXPLAIN` в 3.3.
- **Follow-up:** n/a

### F6 — Rolling update: старые поды вставляют строки без колонки после backfill
- **Severity:** minor (A6; B m4)
- **Location:** Spec premise 2
- **Issue:** premise «единственный писатель» в деплое ложна: до рестарта такие строки отвечают 404 по FNV.
- **Recommendation:** описать окно и самовосстановление.
- **Disposition:** fix-here (docs): premise 2 переписана, Edge Case 9, CHANGELOG.
- **Follow-up:** n/a

### F7 — Backfill на нескольких репликах может словить deadlock
- **Severity:** minor (A7)
- **Location:** `go-app/internal/infrastructure/postgres_alert_records.go` (`UPDATE … FROM unnest`)
- **Issue:** порядок блокировок не гарантирован; одна реплика получит ошибку и `Warn`.
- **Recommendation:** записать или `SKIP LOCKED`.
- **Disposition:** fix-here (docs): Spec § Backfill. Корректность не страдает (`AND upstream_fingerprint IS NULL`, FNV детерминирован).
- **Follow-up:** n/a

### F8 — Backfill через триггер сдвигает `updated_at` старых строк
- **Severity:** nit (A8)
- **Location:** `go-app/internal/infrastructure/postgres_alert_records.go` (doc `BackfillUpstreamFingerprints`); `internal/core/history.go` (`Sorting`)
- **Issue:** будущий `HISTORY-API` с сортировкой по `updated_at` получит смещение.
- **Recommendation:** записать у `HISTORY-API`.
- **Disposition:** defer-backlog.
- **Follow-up:** `docs/06-planning/BACKLOG.md` `HISTORY-API`

### F9 — Spec расходится с кодом: `NULLS LAST`, ответы 404/405 в `text/plain`
- **Severity:** nit (A9; B n1, n4)
- **Location:** `Spec.md` Edge Cases 4, 7; `ampapi.go`
- **Issue:** в Spec указан `NULLS LAST`, в коде его нет (`timestamp` NOT NULL). 404 и 405 от `ServeMux` отдавались не в JSON и не попадали в метрику.
- **Recommendation:** выровнять.
- **Disposition:** fix-here: Spec исправлена; после F3 404 и 405 отдаются в JSON, 404 считается в метрике (`handler="other"`).
- **Follow-up:** n/a

### F10 — Совпадение FNV у разных labels клиенту не видно
- **Severity:** nit (A10)
- **Location:** `postgres_alert_records.go` (`alertRecordByUpstreamFingerprintQuery`)
- **Issue:** отдаётся строка с последним `timestamp`, без признака того, что строк несколько.
- **Recommendation:** принять и описать.
- **Disposition:** fix-here (docs): Spec § Data Model.
- **Follow-up:** n/a

### F11 — Pre-existing: `GetAlertByFingerprint` падает на `starts_at = NULL`
- **Severity:** nit (A11)
- **Location:** `postgres_storage_adapter.go` (`GetAlertByFingerprint`)
- **Issue:** на legacy-строке с NULL запись истории этого алерта падает на каждом событии.
- **Recommendation:** BUGS.
- **Disposition:** defer-bug.
- **Follow-up:** `docs/06-planning/BUGS.md` `HISTORY-NULL-STARTS-AT-SCAN`

### F12 — Breaking changes и class-a расхождения закрыты не полностью
- **Severity:** major (B M1)
- **Location:** `internal/upstreamcore/contract/testdata/divergences.tsv`; Spec § Impact «Breaking»
- **Issue:** `global.group_by` и `global.group_wait`, `group_interval`, `repeat_interval` не «не поддерживаются» — конфиг с ними не загружается (`UnmarshalStrict`). Решения по class-a записям (алиасы здоровья, UI, `/api/v1`, `?status=resolved`) не приняты, шапка tsv устарела.
- **Recommendation:** описать режим отказа и все четыре ключа; решить каждую class-a запись.
- **Disposition:** fix-here. Spec и CHANGELOG описывают отказ загрузки и замену (корневой `route:`), `?status=resolved` (замена — `GET /api/amp/v1/alerts/{fingerprint}`) и алиасы здоровья. Все class-a записи переведены в class d с причинами, шапка обновлена, research Q5. Решение по алиасам и `?status=resolved` принял автор по инварианту «`api/v2` = upstream», вынесено на ревью владельца.
- **Follow-up:** n/a

### F13 — Поле `alertname` выбивается из snake_case
- **Severity:** nit (B n2)
- **Location:** `ampapi.go` (`alertResponse`)
- **Disposition:** fix-here: `alert_name`, как у `core.Alert`.
- **Follow-up:** n/a

### F14 — Ответ 500 на investigation говорит «alert history»
- **Severity:** nit (B n5)
- **Location:** `ampapi.go` (`internalError`)
- **Disposition:** fix-here: «failed to read AMP data».
- **Follow-up:** n/a

### F15 — `promauto` с `DefaultRegisterer` в тестах композиции
- **Severity:** nit (B n6)
- **Disposition:** fix-here в write-tests: `prometheus.NewRegistry()` на каждый тест.
- **Follow-up:** n/a

### F16 — CHANGELOG: «no schema change» в записи среза 5 устарело
- **Severity:** nit (B n4)
- **Location:** `CHANGELOG.md` `[Unreleased]` Added, UPSTREAM-CORE-REMOVAL «One key»
- **Disposition:** fix-here на finalize (4.2): уточнить, что колонка добавлена срезом 6.
- **Follow-up:** n/a

## Anti-Pattern Check

- [x] Self-audit was not treated as a substitute for independent review.
