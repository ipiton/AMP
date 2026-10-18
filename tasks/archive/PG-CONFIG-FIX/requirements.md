---
id: PG-CONFIG-FIX
slug: pg-config-fix
stream: Helm
type: bug
priority: critical
status: active
created_at: 2026-10-17
updated_at: 2026-10-17
---

# Requirements: встроенный PostgreSQL применяет конфиг чарта

## Problem Framing

- **Symptom:** встроенный PostgreSQL работает на дефолтах образа: измерено на k3d 2026-10-15 — `config_file` дефолтный, `max_connections=20` (чарт задумывал 250), `shared_buffers=400kB`, `pg_hba` — дефолт образа. Первый старт контейнера падает на `init.sql` (строка 56, `ALTER SYSTEM SET pg_stat_statements.max`), после рестарта init пропускается ⇒ скрипт применён частично. Пул AMP 25 соединений на pod × `minReplicas: 2` против 17 доступных ⇒ под нагрузкой ошибки записи и 500 на `POST /api/v2/alerts`.
- **Root Cause:** `postgresql.conf` и `pg_hba.conf` из ConfigMap монтируются в `/docker-entrypoint-initdb.d/` (`helm/amp/templates/postgresql-statefulset.yaml:96-107`), откуда entrypoint исполняет только `*.sh`/`*.sql`; `/etc/postgresql` — пустой `emptyDir` (`:227-228`); `POSTGRES_INITDB_ARGS="-c config_file=…"` (`:86-88`) — аргументы `initdb`, а не сервера. `init.sql` вызывает `ALTER SYSTEM` для параметров `pg_stat_statements`, который без `shared_preload_libraries` не загружен.
- **Why Now:** P0 по решению владельца 2026-10-15: встроенный PG — dev/pilot (ADR-016), но пилот с ним падает под нагрузкой.
- **How We Measure:** на k3d — и на свежей установке, и после `helm upgrade` поверх PVC, инициализированного версией до фикса: `SHOW config_file` указывает на конфиг чарта, `SHOW max_connections` = `postgresql.config.maxConnections`, `SHOW shared_preload_libraries` содержит `pg_stat_statements`, `pg_hba_file_rules` совпадает с `pg_hba.conf` чарта, `pg_stat_statements` работает; первый старт без рестарта контейнера (`RESTARTS 0`); AMP подключается, ingest отвечает 200. Render-тест фиксирует аргументы сервера и отсутствие `ALTER SYSTEM`.

## Risk Profile

- **Signals:** `S R`
  - `S` — впервые применяется `pg_hba.conf` чарта: правила аутентификации к БД меняются против дефолта образа (`trust` на loopback/socket, `md5` для pod-сетей `10/8`, `172.16/12`, `192.168/16`, `replication` из pod-сетей, `reject` остальным). Это изменение permissions на доступ к данным.
  - `R` — существующие установки меняют поведение при рестарте pod'а PG: `max_connections`, `shared_buffers` против лимитов памяти, `logging_collector`, новый `pg_hba`. Закрывается записью в `CHANGELOG.md` `[Unreleased]` с migration note (в AMP нет `deploy`).
- **Tier:** Full
- **Notes:** `M` рассмотрен и не ставится: данные и схема не трогаются, `volumeClaimTemplates` неизменен, откат — прошлая ревизия чарта. Но на уже инициализированном PVC фикс обязан работать без `initdb` (init-путь там не исполняется) — это главный критерий проверки. `C` не ставится: values `postgresql.config.*` уже публичны, меняется только то, что они начинают действовать. `X` не ставится: только Helm-чарт и его доки, Go не трогается.

## User Stories

1. Как оператор пилота на встроенном PostgreSQL, я хочу, чтобы `postgresql.config` из values реально применялся, чтобы AMP с двумя репликами не упирался в `max_connections` и не отдавал 500 на ingest.
2. Как оператор существующей установки, я хочу, чтобы конфиг применился после `helm upgrade` без переинициализации PVC, и migration note говорил, что изменится при рестарте PG.
3. Как оператор, я хочу, чтобы первый старт PG проходил без ошибки `init.sql` и рестарта контейнера.

## Success Criteria

- [ ] Сервер PG стартует с конфигом чарта через аргументы сервера (`-c config_file=…`, `hba_file`), а не через `initdb.d`/`POSTGRES_INITDB_ARGS`; путь к файлам не пустой `emptyDir`.
- [ ] `init.sql` не содержит `ALTER SYSTEM` для параметров непредзагруженных расширений; параметры `pg_stat_statements` — в `postgresql.conf` (за `shared_preload_libraries`); свежая установка стартует с `RESTARTS 0`.
- [ ] k3d, свежая установка: `config_file`, `max_connections`, `shared_buffers`, `shared_preload_libraries`, `pg_hba_file_rules` — из чарта; AMP (2 реплики) подключается, `POST /api/v2/alerts` → 200; `postgres-exporter` подключается.
- [ ] k3d, апгрейд: PVC инициализирован ревизией до фикса ⇒ `helm upgrade` на ветку ⇒ те же проверки зелёные, данные на месте.
- [ ] Логи PG остаются в stdout/stderr контейнера (`kubectl logs`), либо осознанное решение в research/Spec, почему нет (`logging_collector = on` по умолчанию уводит их в файлы `pg_log/`).
- [ ] `shared_buffers` и `max_connections` по умолчанию совместимы с `postgresql.resources` дефолта и `values-production.yaml` (pod не OOM'ится и не падает на старте).
- [ ] Предупреждение `PG-CONFIG-NOT-APPLIED` снято из `templates/NOTES.txt`, `values.yaml`, `helm/amp/README.md`; `tests/render-postgresql.sh` (T6) переписан на новое поведение.
- [ ] Render-тест: аргументы сервера, монтирование конфига, отсутствие `ALTER SYSTEM` в `init.sql`; подключён туда же, где остальные `render-*.sh` (release-gate).
- [ ] `CHANGELOG.md` `[Unreleased]`: Fixed + migration note для существующих установок (что меняется при рестарте PG, как откатить).
- [ ] `BUGS.md`: `PG-CONFIG-NOT-APPLIED` закрыт; BACKLOG `PG-CONFIG-FIX` закрыт.
- [ ] Гейты: затронутые render-тесты, `helm lint`, `scripts/release-gate.sh`, `git diff --check`.

## Non-Goals

- HA, репликация, бэкапы встроенного PG — по ADR-016 он dev/pilot, прод-путь — внешний PG (`HELM-EXTERNAL-DATABASE`, закрыт 2026-10-16).
- TLS для встроенного PG (`ssl = off` остаётся).
- Пересмотр всех значений тюнинга `postgresql.conf` (TN-97/TN-98) — только то, что мешает старту или явно противоречит дефолтным ресурсам.
- Дефолты чарта под одну ноду (`HELM-SINGLE-NODE-DEFAULTS`) — отдельная задача; здесь только совместимость `shared_buffers`/`max_connections` с текущими ресурсами.
- `helm test` для внешней БД (`HELM-TEST-EXTERNAL-DB`).

## Constraints

- **Scope:** `helm/amp/templates/postgresql-{statefulset,configmap}.yaml`, `values*.yaml` (секция `postgresql`), `NOTES.txt`, `tests/render-postgresql.sh` (+ новый/расширенный render-тест), `helm/amp/README.md`, `CHANGELOG.md`, planning-файлы.
- **Security:** `pg_hba` чарта не должен открывать доступ шире, чем сейчас даёт дефолт образа (`host all all all scram-sha-256`), кроме осознанно описанного; метод `md5` против `scram-sha-256` образа — решить в research (с PG 14+ `password_encryption=scram-sha-256`, правило `md5` принимает SCRAM-пароли, но выглядит как ослабление). Пароль по-прежнему только из Secret.
- **Compatibility:** установки на уже инициализированном PVC получают конфиг при рестарте pod'а без `initdb`; StatefulSet `volumeClaimTemplates` не меняется; `checksum/config` уже есть — изменение ConfigMap рестартует pod. Откат — `helm rollback` на прошлую ревизию (PG вернётся к дефолтам образа). Образ остаётся `postgres:16.15`.

## Discovery Notes

- Similar tasks: `tasks/archive/PROD-POSTGRES-HA-DECISION/` (research § Findings 2, `evidence/k3d-probe-effective-config.txt`, `evidence/upgrade-path.md`), `tasks/archive/HELM-EXTERNAL-DATABASE/` (k3d-прогоны, render-тест `tests/render-external-database.sh`).
- Relevant patterns: `helm/amp/tests/render-postgresql.sh` (assert-хелперы, T6 проверяет предупреждение), `scripts/release-gate.sh` (как подключаются render-тесты), `checksum/config` в `postgresql-statefulset.yaml:36`.
- Open unknowns:
  - `config_file` вне `PGDATA` + `PGDATA` из env: где сервер ищет `hba_file`/`ident_file` по умолчанию — явно задавать оба или нет.
  - Монтировать ConfigMap напрямую (read-only, `subPath` не обновляется на лету — нам и не нужно, есть checksum) или через `/etc/postgresql` без `emptyDir`.
  - `logging_collector = on` + `log_directory = 'pg_log'` уводит логи из `kubectl logs` — оставить, выключить по умолчанию или писать в stderr.
  - `shared_buffers=256MB`, `max_connections=250`, `wal_buffers`, `min_wal_size=1GB`/`max_wal_size=4GB` против `postgresql.resources` и размера PVC по умолчанию.
  - `md5` против `scram-sha-256` в `pg_hba` чарта; правило `replication` из pod-сетей без бэкапа (бэкап удалён, ADR-016) — нужно ли оно.
  - Поведение exporter-sidecar при новом `pg_hba` (`127.0.0.1` — `trust` раньше `md5`).
