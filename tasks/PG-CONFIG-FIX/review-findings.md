# Deep Review Findings: встроенный PostgreSQL применяет конфиг чарта

**Trigger classification:** mandatory (`S`: `pg_hba` — доступ к БД; профиль `S R`, tier Full)
**Reviewer perspective:** два независимых агента без доступа к рассуждениям автора и друг к другу:
- Reviewer A: security (`pg_hba`, пароль, supply chain), корректность шаблонов. Рендеры ветки против worktree `main`; живые прогоны в docker: `postgres:16.15` с отрендеренным конфигом на свежем томе и на томе, инициализированном раскладкой `main`; матрица `pg_hba` из соседнего контейнера; `postgres-exporter:v0.15.0` с отрендеренным `queries.yaml`, сверка имён метрик правил с `/metrics`.
- Reviewer B: Design Premises и их классы, migration note и откат против кода и evidence, доки против рендера, git-история, правила алертов против исходников exporter v0.15.0.

Кластер k3d ревьюеры не меняли. Автор свёл находки, объединил пересечения (B2 + A2 → R3) и перепроверил: R2 — по scrape K2 (`evidence/k3d-acceptance.md`) 17 рядов `pg_stat_user_tables_last_autovacuum … 0`; R3 — в том же scrape есть `pg_replication_lag_seconds`, `pg_stat_bgwriter_checkpoints_req_total`, нет `pg_replication_lag`, `pg_stat_bgwriter_checkpoints_req`, `pg_stat_statements_mean_exec_time_seconds`.
**Reviewed at:** 2026-10-17
**Reviewed tree:** bugfix/pg-config-fix @ 3271a45
**Verdict:** fix_required @ 3271a45 (см. `review-verdict.json`)

## Findings

### R1 — `render-postgresql.sh` красный на ветке (T6)
- **Severity:** major
- **Location:** `helm/amp/tests/render-postgresql.sh:194` (T6 notes-default/notes-no-hpa)
- **Issue:** 4 assertion падают (`want 2 x 'PG-CONFIG-NOT-APPLIED', got 0`, `want 0 x 'Expected: 250', got 1`); на `main` скрипт зелёный. `release-gate.sh` берёт `tests/*.sh` ⇒ гейт красный. T7 (args, `hba_file`, отсутствие `ALTER SYSTEM`, env exporter'а) ещё нет. (Reviewer A, A1.)
- **Recommendation:** Phase 6 до мержа; в T7 проверить `hba_file` в args и отсутствие `DATA_SOURCE_NAME`/`$(POSTGRES_`.
- **Disposition:** fix-here — в `write-tests` (тесты пишутся после вердикта по пайплайну; T7.9 уже в плане)
- **Follow-up:** `tasks.md` 6.1–6.2

### R2 — после починки exporter'а `PostgreSQLVacuumOld` ложно срабатывает на каждой таблице, которую autovacuum ещё не трогал
- **Severity:** major
- **Location:** `helm/amp/templates/postgresql-prometheus-rules.yaml:103`
- **Issue:** коллектор `pg_stat_user_tables` (v0.15.0, включён по умолчанию) отдаёт `COALESCE(last_autovacuum, '1970-01-01Z')` ⇒ `pg_stat_user_tables_last_autovacuum = 0`; `(now() - …) > 86400` истинно всегда, `for: 1h` ⇒ алерт на каждой такой таблице. На свежей/тихой установке — почти все таблицы AMP (scrape K2: 17 рядов со значением 0). До задачи метрики не было (`pg_up 0`), т.е. ложное срабатывание появляется из-за этой правки. `postgresql.monitoring.prometheusRules.enabled: true` по умолчанию; в k3d-приёмке правила были выключены. (Reviewer B, B1.)
- **Recommendation:** исключить нулевое значение: `pg_stat_user_tables_last_autovacuum > 0 and (time() - pg_stat_user_tables_last_autovacuum) > 86400`.
- **Disposition:** fix-here
- **Follow-up:** n/a

### R3 — CHANGELOG/Spec: «alert rules now get data» неверно; 7 метрик из правил exporter не отдаёт
- **Severity:** major
- **Location:** `CHANGELOG.md:32`, `tasks/PG-CONFIG-FIX/Spec.md` § Observability, `helm/amp/templates/postgresql-prometheus-rules.yaml:30,40,50,63,73,116,126`
- **Issue:** правила по-прежнему молчат: `:30`/`:63` — `sum by (instance)(pg_stat_activity_count) > (pg_settings_max_connections * 0.9)` без `on(instance)` (наборы меток не совпадают ⇒ пусто) — в т.ч. алерт на исчерпание соединений, ровно класс этого бага; `:40` `pg_replication_lag` (есть `…_seconds`); `:50` `pg_database_age_seconds` (нет); `:73` `pg_stat_statements_mean_exec_time_seconds` (коллектор `stat_statements` выключен по умолчанию, такой метрики нет); `:116`/`:126` bgwriter без суффикса `_total`. Было до задачи; правка делает видимым и CHANGELOG утверждает обратное. (Reviewer B, B2; Reviewer A, A2.)
- **Recommendation:** точный текст в CHANGELOG и Spec (`PostgreSQLDown` больше не ложный; остальные правила ссылаются на неверные имена — баг); баг на правила со списком; `promtool test rules` — в BACKLOG.
- **Disposition:** fix-here (текст CHANGELOG/Spec) + defer-bug (правила)
- **Follow-up:** BUGS `PG-ALERT-RULES-METRICS` (finalize)

### R4 — P7 (бюджет памяти) не подтверждён для сценария, ради которого стоит `max_connections=250`
- **Severity:** minor
- **Location:** `Spec.md` § Design Premises P7; `CHANGELOG.md:269` («within `limits.memory: 2Gi`»)
- **Issue:** K1.4 — 13 backend'ов и 4 запроса. При 250 backend'ах (10 × 25): `shared_buffers` 256MB + shm до 256Mi + 250 × (приватная память + `work_mem` × узлы × воркеры) легко > 2Gi. «within 2Gi» верно только для `shared_buffers` + shm. (Reviewer B, B3.)
- **Recommendation:** P7 → `assumed (≤ ~20 backends measured)`; фраза в CHANGELOG/README: при росте backend'ов к `maxConnections` нужен больший `limits.memory`.
- **Disposition:** fix-here (текст); сам бюджет — defer-backlog `HELM-SINGLE-NODE-DEFAULTS`
- **Follow-up:** `HELM-SINGLE-NODE-DEFAULTS` (finalize)

### R5 — migration note: неточность и пропуски
- **Severity:** minor
- **Location:** `CHANGELOG.md:269-272`
- **Issue:** (a) «nothing is written to the volume» буквально неверно (WAL при `min_wal_size` 1GB, `pg_stat_statements.stat`, `pg_log` при `--reuse-values`) — имелось в виду «конфигурация в PVC не сохраняется»; (b) `profile: lite` тоже рендерит `amp-postgres` (AMP им не пользуется) — тоже рестарт и 256MB + shm; (c) после rollback exporter снова `pg_up 0` ⇒ `PostgreSQLDown`; (d) `postgresql.config.ssl: "on"` теперь действует ⇒ FATAL без сертификатов. (Reviewer B, B4.)
- **Recommendation:** переформулировать (a), дописать (b)–(d).
- **Disposition:** fix-here
- **Follow-up:** n/a

### R6 — trust на loopback: `kubectl port-forward` даёт суперпользователя без пароля
- **Severity:** minor
- **Location:** `helm/amp/templates/postgresql-configmap.yaml` (`host all all 127.0.0.1/32 trust`, `::1/128 trust`)
- **Issue:** `POSTGRES_USER` — суперпользователь; соединения через `kubectl port-forward` приходят с loopback ⇒ `psql -h 127.0.0.1 -U alert_history -d postgres` без пароля проходит. Не расширение против `main` (дефолт образа — тот же trust), но этот `pg_hba` чарт теперь пишет сам. Exporter уже передаёт пароль (`DATA_SOURCE_PASS`) и по SCRAM подключается (проверено с паролем `p@ss:w/rd#1`); probes — `pg_isready` (без аутентификации) и `psql` через socket. (Reviewer A, A3.)
- **Recommendation:** loopback → `scram-sha-256`, `local` оставить `trust`.
- **Disposition:** fix-here (две строки; отклонение от Spec D3 — записать)
- **Follow-up:** n/a

### R7 — план тестов не ловит runtime-класс дефектов, найденных в K1
- **Severity:** minor
- **Location:** `tasks.md` Phase 6; `Spec.md` § Test Plan
- **Issue:** три дефекта exporter'а и R2/R3 видны только на живом кластере; T7.9 ловит этот регресс, а не новый неверный столбец; невалидный ключ `postgresql.conf` (FATAL) рендер не увидит. (Reviewer B, B5.)
- **Recommendation:** (1) `templates/tests/postgresql-test-connection.yaml`: `SHOW config_file` = `/etc/postgresql/postgresql.conf` и `SHOW hba_file` — `helm test` ловит регресс P1 на любой установке; (2) `promtool check/test rules` и прогон exporter-запросов против `postgres:16.15` — BACKLOG.
- **Disposition:** (1) fix-here — в `write-tests`; (2) defer-backlog
- **Follow-up:** `tasks.md` 6.x; BACKLOG `PG-MONITORING-CHECKS` (finalize)

### R8 — дефолты чарта нарушают собственный бюджет: предупреждение NOTES на каждой установке по умолчанию
- **Severity:** minor
- **Location:** `helm/amp/values.yaml:355-358`, `helm/amp/templates/NOTES.txt:55-58`
- **Issue:** 10 × 25 = 250 = `maxConnections` ⇒ warning при дефолтах. Логика NOTES верна (maxReplicas 9 — нет; 20 при 600 — нет; `--set-string maxConnections=300` при 12 — есть; autoscaling off — блока нет). (Reviewer A, A4.)
- **Recommendation:** поднять `maxConnections` или снизить `maxReplicas` — решение о дефолтах.
- **Disposition:** defer-backlog — `HELM-SINGLE-NODE-DEFAULTS` (Non-Goal requirements: пересмотр дефолтов — отдельная задача); warning по умолчанию — честный сигнал до неё
- **Follow-up:** `HELM-SINGLE-NODE-DEFAULTS` (finalize)

### R9 — physical replication закрыта и изнутри pod'а
- **Severity:** nit
- **Location:** `helm/amp/templates/postgresql-configmap.yaml` (pg_hba)
- **Issue:** `replication=true` через socket → `no pg_hba.conf entry for replication connection from host "[local]"`; на `main` образ давал `local replication trust` ⇒ `pg_basebackup` через `kubectl exec` перестанет работать (restore-гайд на `pg_dump`, не затронут). CHANGELOG говорит только про сеть. (Reviewer A, A5.)
- **Recommendation:** одна фраза в CHANGELOG.
- **Disposition:** fix-here (текст); правило не возвращаем — бэкапа на `pg_basebackup` нет (ADR-016)
- **Follow-up:** n/a

### R10 — `checksum/config` не покрывает ConfigMap exporter'а
- **Severity:** nit
- **Location:** `helm/amp/templates/postgresql-statefulset.yaml:36`
- **Issue:** правка `queries.yaml` без смены конфига PG не перезапустит pod (в этом апгрейде pod рестартует и так). Было до задачи. (Reviewer A, A6; совпадает с follow-up (a) автора.)
- **Disposition:** defer-tech-debt
- **Follow-up:** TECH-DEBT `HELM-CHART-GAPS` (finalize)

### R11 — неточности Spec
- **Severity:** nit
- **Location:** `Spec.md` P5, § Observability
- **Issue:** P5 по факту grep (code-read), а не call-path-traced (вывод верен — перепроверен grep'ом по `go-app/`, `grafana/`, `docs/`, `deploy/`, `examples/`, `scripts/`); «exporter без изменений» устарело. (Reviewer B, B6.)
- **Disposition:** fix-here
- **Follow-up:** n/a

### R12 — `superuser_reserved_connections` ничего не резервирует: роль AMP — суперпользователь
- **Severity:** nit
- **Location:** `postgresql-configmap.yaml` (Connection Settings); `NOTES.txt`
- **Issue:** AMP и exporter под одной суперпользовательской ролью ⇒ при насыщении пулов не войдут ни exporter, ни `kubectl exec psql`. (Reviewer B, B7.)
- **Disposition:** defer-tech-debt — вместе с follow-up «`alert_history` суперпользователь»
- **Follow-up:** TECH-DEBT `HELM-CHART-GAPS` (finalize)

## Anti-Pattern Check

- [x] Self-audit was not treated as a substitute for independent review.
