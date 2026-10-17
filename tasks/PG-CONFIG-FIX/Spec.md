---
id: PG-CONFIG-FIX
slug: pg-config-fix
stream: Helm
type: bug
status: draft
created_at: 2026-10-17
updated_at: 2026-10-17
based_on:
  - requirements.md
  - research.md
---

# Specification: встроенный PostgreSQL применяет конфиг чарта

**Version:** 1.0
**Status:** Draft

## Summary

Встроенный PostgreSQL получает `postgresql.conf` и `pg_hba.conf` чарта аргументами сервера (`-c config_file`, `-c hba_file`) из ConfigMap, смонтированного в `/etc/postgresql`. Это работает и на уже инициализированном PVC, где init-путь не исполняется. `init.sql` перестаёт падать на первом старте, `pg_hba` закрывает сетевую `replication` суперпользователя и не сужает адреса, логи остаются в stdout, `/dev/shm` перестаёт ронять параллельные запросы.

## Requirements Coverage

| Requirement / Criterion | Covered by |
|---|---|
| Конфиг через аргументы сервера, не `initdb.d`/`POSTGRES_INITDB_ARGS`, не пустой `emptyDir` | D1, T7.1–T7.3 |
| `init.sql` без `ALTER SYSTEM`, `pg_stat_statements.*` в `postgresql.conf`, первый старт `RESTARTS 0` | D2, T7.4, K1 |
| k3d свежая установка: `SHOW …`, AMP 2 реплики → 200, exporter | K1 |
| k3d апгрейд поверх PVC с `main` | K2 |
| Логи в stdout или осознанное решение | D4, T7.6, K1 |
| `shared_buffers`/`max_connections` совместимы с ресурсами | D5, Design Premises P6–P8 |
| Снять `PG-CONFIG-NOT-APPLIED` из NOTES/values/README, T6 переписан | D6, T6 |
| Render-тест в гейте | D7 (`render-postgresql.sh` T7; гейт берёт `tests/*.sh`) |
| CHANGELOG Fixed + migration note | D8 |
| BUGS/BACKLOG закрыты | finalize |
| Гейты | plan-task / testing |

## Current State

- **Code:** `helm/amp/templates/postgresql-statefulset.yaml` — env `POSTGRES_INITDB_ARGS` (`:86-89`), mount `postgres-config` → `/etc/postgresql` (`:93-95`) из `emptyDir` (`:227-228`), `custom-config` `subPath`-mount'ы `postgresql.conf`/`pg_hba.conf`/`init.sql` в `/docker-entrypoint-initdb.d/` (`:96-109`); `command`/`args` не заданы. `templates/postgresql-configmap.yaml` — `postgresql.conf` (`logging_collector` default `"on"`), `pg_hba.conf` (RFC1918 `md5`, `replication`, мёртвое `127.0.0.1 md5`), `init.sql` (`ALTER SYSTEM` секция 4, три view секция 5).
- **Data:** не применимо (схема AMP не трогается; системные настройки PG — см. D1).
- **Tests:** `helm/amp/tests/render-postgresql.sh` T1–T6; T6 ждёт 2 упоминания `PG-CONFIG-NOT-APPLIED` в NOTES. `templates/tests/postgresql-test-connection.yaml` — `helm test`, проверяет `pg_stat_statements`.
- **Docs:** `templates/NOTES.txt:25-28` (предупреждение), `:98-110` (шаг 4 «Expected until … fixed»), `values.yaml:354-357` (NOTE + формула), `helm/amp/README.md:105` (последнее предложение), `CHANGELOG.md` `[Unreleased]`.

## Design Premises

| Premise | Confirmed by | Class | If wrong |
|---|---|---|---|
| P1. `-c config_file` в аргументах применяет конфиг на свежем и на уже инициализированном PVC; entrypoint передаёт аргументы и временному init-серверу | `evidence/docker-probe.txt` B/C/D; `evidence/k3d-shm-probe.txt` (pod с теми же args в k3d) | measured | конфиг снова не применяется на одной из веток |
| P2. Без `-c hba_file` сервер берёт `$PGDATA/pg_hba.conf` | docker-probe B vs C | measured | `pg_hba` чарта не действует |
| P3. Откат на старую раскладку на том же томе стартует, данные на месте; в PVC фикс ничего не пишет | docker-probe D (rollback) | measured | `helm rollback` не восстанавливает сервис |
| P4. Пароли хранятся как SCRAM ⇒ `scram-sha-256` в `pg_hba` не ломает вход существующих установок | docker-probe C (`rolpassword=SCRAM-SHA-256$…`, `password_encryption=scram-sha-256`); все установки инициализированы `postgres:16*` (пин с ADR-016, раньше `postgres:16`) | measured + code-read | AMP на старом PVC с MD5-хешем получает `password authentication failed` |
| P5. Views из `init.sql`, правило `replication`, `127.0.0.1 md5` никому не нужны | grep по `helm/`, `go-app/`, `grafana/` — нет потребителей views; backup CronJob удалён (ADR-016); exporter ходит на `localhost` (`trust` раньше) | call-path-traced | что-то читает `v_*` или реплицируется |
| P6. `/dev/shm` в pod'е 64M, параллельные hash join'ы падают уже на дефолтах (и на `per_gather=2` образа); `emptyDir {medium: Memory, sizeLimit: 256Mi}` снимает | `evidence/k3d-shm-probe.txt` | measured | — |
| P7. `shared_buffers=256MB` + shm ≤256Mi + backend'ы укладываются в `limits.memory: 2Gi` | `values.yaml:317-323`; shm считается в memory cgroup контейнера | code-read | OOMKill под нагрузкой — проверка K1 |
| P8. Pod CIDR k3d `10.42/16`, вход по паролю из pod-сети проходит | k3d-shm-probe (последний блок; с текущим `md5`, с новым `all scram-sha-256` — в K1) | measured (частично) | — |
| P9. Кластеры с pod CIDR вне RFC1918 существуют у пользователей чарта | не проверено; EKS custom networking (`100.64/10`), dual-stack | assumed | ничего: выбранный `pg_hba` не фильтрует по адресу (см. Open Questions) |

## Target Design

1. **D1 — механизм.** В контейнер `postgres` под `{{- if .Values.postgresql.config }}`: `args: ["-c", "config_file=/etc/postgresql/postgresql.conf", "-c", "hba_file=/etc/postgresql/pg_hba.conf"]`, `command` не задаём. Volume `custom-config` (ConfigMap) монтируется каталогом в `/etc/postgresql` (readOnly) вместо `emptyDir` `postgres-config`; `init.sql` — по `subPath` в `/docker-entrypoint-initdb.d/init.sql`. Удаляются env `POSTGRES_INITDB_ARGS`, mount/volume `postgres-config`, mount'ы `*.conf` в `initdb.d`. При `postgresql.config` пустом/`null` — ни `args`, ни mount'ов: сервер на дефолтах образа (аварийный выход).
2. **D2 — `init.sql`.** Удалить секцию 4 (`ALTER SYSTEM`) и секцию 5 (три view). В `postgresql.conf` после `shared_preload_libraries` — `pg_stat_statements.max = 10000`, `pg_stat_statements.track = all`, `pg_stat_statements.track_utility = on` (те же значения, что пытался ставить `init.sql`; новых values-ключей нет).
3. **D3 — `pg_hba.conf`.** Остаются: `local all all trust`, `host all all 127.0.0.1/32 trust`, `host all all ::1/128 trust`; одно правило `host <database> <username> all scram-sha-256`; `host all all all reject`. Удаляются: три RFC1918 `md5`, `host all <user> 127.0.0.1/32 md5`, три `host replication …`. Комментарий в файле: сужение по сети — `postgresql.networkPolicy.enabled`.
4. **D4 — логи.** `logging_collector` по умолчанию `off`: `values.yaml` `loggingCollector: "off"` и `default "off"` в шаблоне. `log_destination = 'stderr'` уже стоит. Комментарий в values: `on` уводит логи в `$PGDATA/pg_log` на PVC без удаления.
5. **D5 — `/dev/shm`.** Volume `dshm: emptyDir {medium: Memory, sizeLimit: 256Mi}` → `/dev/shm` в контейнере `postgres`, всегда (не под `config`): дефект есть и на дефолтах образа (P6). Без нового values-ключа. `max_connections=250` не меняется; комментарий «10 × 20 + 50» → «25 per AMP pod × replicas + exporter».
6. **D6 — docs.** NOTES: убрать две строки предупреждения (`:27-28`) и заменить текст шага 4 на «Expected: {{ maxConnections }}»; добавить строку про бюджет, если `autoscaling.maxReplicas × 25 ≥ maxConnections`. values: убрать NOTE `:355-356`. README `:105`: убрать последнее предложение, добавить абзац о бюджете соединений и `pg_hba`/NetworkPolicy.
7. **D7 — тесты.** `render-postgresql.sh`: T6 переписать (0 × `PG-CONFIG-NOT-APPLIED`, `Expected: 250` — 1 раз в notes-default); новый T7 (см. Test Plan).
8. **D8 — CHANGELOG** `[Unreleased]`: `### Fixed` (`PG-CONFIG-NOT-APPLIED`) + migration note (Rollout / Rollback ниже, verbatim по смыслу).

## API Contracts

Не применимо: HTTP API AMP не меняется. Values-контракт: новых ключей нет; меняется дефолт `postgresql.config.loggingCollector` (`"on"` → `"off"`), который до фикса не действовал.

## Data Model / Migrations

- **Migration:** no. Схема AMP и данные не трогаются; `volumeClaimTemplates` неизменен. Меняются настройки сервера и `pg_hba` — на каждом старте из ConfigMap, в PVC не пишутся (P3).
- **Rollback / forward-fix:** `helm rollback` (P3). Расширения, созданные старым `init.sql` (`pg_stat_statements`, `pg_trgm`, `btree_gin`), на существующих установках остаются как есть.

## Component Architecture

- `helm/amp/templates/postgresql-statefulset.yaml` — D1, D5.
- `helm/amp/templates/postgresql-configmap.yaml` — D2, D3, D4, комментарий бюджета.
- `helm/amp/values.yaml` — D4, D6 (NOTE), комментарий формулы `maxConnections`.
- `helm/amp/templates/NOTES.txt`, `helm/amp/README.md` — D6.
- `helm/amp/tests/render-postgresql.sh` — D7.
- `CHANGELOG.md` — D8.

## Security Design

- [x] Ownership validation — не применимо.
- [x] Input validation — не применимо (values подставляются как раньше; `database`/`username` уже идут в `pg_hba`/`init.sql` без экранирования — не меняется, см. Edge Case 6).
- [x] Sensitive data not logged — пароль только из Secret (`POSTGRES_PASSWORD` `secretKeyRef`), в ConfigMap/args не попадает.
- [x] Rate limiting — не применимо.
- [x] Auth path: из сети — только `<database>`/`<username>` по SCRAM; сетевая `replication` и другие БД/роли — `reject`; `trust` — только socket и loopback внутри pod'а (как в дефолте образа). Относительно сегодняшнего поведения (дефолт образа `host all all all scram-sha-256`) доступ сужается, не расширяется. Сетевой периметр — NetworkPolicy (`postgresql.networkPolicy.enabled`, по умолчанию выключена — не меняем).

## Invariants

- [ ] Данные существующего PVC доступны после upgrade и после rollback.
- [ ] Свежая установка стартует без рестарта контейнера.
- [ ] AMP, exporter, probes и `helm test` подключаются с дефолтными values.
- [ ] Lite-профиль и `postgresql.enabled: false` рендерят то же, что до задачи (T5 зелёный, diff рендера пуст для этих вариантов).
- [ ] T1–T5 `render-postgresql.sh` и остальные `tests/*.sh` зелёные без правок, кроме T6.

## Edge Cases

1. `postgresql.config: null` / `{}` → нет `args`, нет mount'ов конфига; `init.sql` тоже не монтируется (как сейчас); сервер на дефолтах образа; `/dev/shm` остаётся. T7.7.
2. `helm upgrade --reuse-values` со старыми values → `loggingCollector: "on"` из старых values перенесётся (ключ существовал) ⇒ логи уйдут в `pg_log`. Migration note: `--reset-then-reuse-values` или `--set postgresql.config.loggingCollector=off`.
3. Оператор задал `sharedBuffers` больше лимита памяти → PG стартует и падает OOM под нагрузкой; поведение values-ключа, не этой задачи. README упоминает связку с `resources`.
4. Битое значение в values (`maxConnections: abc`) → `FATAL` на старте, CrashLoop, текст в `kubectl logs` (логи в stderr до чтения конфига). Откат — `helm rollback`.
5. Pod CIDR вне RFC1918 / IPv6 → правило `all` пропускает (раньше дефолт образа тоже пропускал).
6. `database`/`username` с пробелами или спецсимволами → невалидный `pg_hba`/SQL, как и сейчас; не расширяем.
7. `sharedPreloadLibraries` без `pg_stat_statements` → строки `pg_stat_statements.*` остаются placeholder'ами: сервер стартует без FATAL/WARNING (`measured`, `evidence/docker-probe.txt` E). Условный шаблон не нужен.

## Impact Analysis

- **Affected modules:** только Helm-чарт (`helm/amp/`), CHANGELOG.
- **Breaking changes:** поведенческое для установок со встроенным PG: при upgrade рестарт PG, затем действуют `postgresql.config` и `pg_hba` чарта; сетевой доступ под другими ролями/в другие БД — `reject`; сетевая `replication` закрыта.
- **New dependencies:** none.
- **Risks:** (a) установка, где кто-то ходил в PG под другой ролью → `reject` после upgrade; mitigation — migration note + `postgresql.config: null`. (b) `/dev/shm` 256Mi в лимите памяти → чуть меньше запаса до OOM; mitigation — K1 под нагрузкой, README. (c) `--reuse-values` тащит `loggingCollector: on` — Edge Case 2.

## Rollout / Rollback

- **Rollout:** `helm upgrade` → `checksum/config` меняется → pod PG рестартует (короткий простой ingest; AMP ретраит/отдаёт 5xx на время рестарта). После рестарта: `max_connections` 20→250, `shared_buffers` 400kB→256MB, preload `pg_stat_statements`, `min/max_wal_size` 1GB/4GB (до ~40% PVC 10Gi), `/dev/shm` 256Mi, новый `pg_hba`. Проверка: `SHOW config_file; SHOW hba_file; SHOW max_connections;`, `SELECT * FROM pg_hba_file_rules;`, в логах AMP нет `pg_hba.conf rejects`.
- **Rollback:** `helm rollback <release> <rev>` → PG на дефолтах образа (исходный баг возвращается). Если pod PG не стал Ready и StatefulSet (`OrderedReady`) его не пересоздаёт — удалить pod `<release>-postgres-0` вручную (forced rollback). Аварийно без отката чарта — `--set postgresql.config=null`.
- **Feature flag:** не применимо; аварийный выход — `postgresql.config: null` (существующее поведение шаблона).

## Observability

- **Logs:** PG пишет в stderr контейнера (`kubectl logs <release>-postgres-0 -c postgres`) и после старта.
- **Metrics:** exporter без изменений; `pg_stat_statements_*` начинают появляться (алерт `postgresql-prometheus-rules.yaml:73` получает данные — раньше молчал).
- **Alerts:** без изменений.

## Test Plan

**Render (T7 в `render-postgresql.sh`, дефолтные values, документ `StatefulSet amp-postgres` / `ConfigMap amp-postgres-config`):**

- T7.1 `args` содержит `config_file=/etc/postgresql/postgresql.conf` и `hba_file=/etc/postgresql/pg_hba.conf`.
- T7.2 нет `POSTGRES_INITDB_ARGS`; нет `mountPath: /docker-entrypoint-initdb.d/postgresql.conf` и `…/pg_hba.conf`; есть `…/init.sql`.
- T7.3 `mountPath: /etc/postgresql` ровно один, volume — ConfigMap (нет `emptyDir` с именем `postgres-config`).
- T7.4 в ConfigMap нет `ALTER SYSTEM` и `CREATE OR REPLACE VIEW`; есть `pg_stat_statements.max = 10000`.
- T7.5 `pg_hba.conf`: 0 × `replication`, 0 × `md5`, 1 × `all                     scram-sha-256` (точная строка по шаблону), есть `reject`.
- T7.6 `logging_collector = off`.
- T7.7 `--set postgresql.config=null` → 0 × `config_file=`, 0 × `/docker-entrypoint-initdb.d/`, рендер успешен.
- T7.8 `mountPath: /dev/shm` и `medium: Memory` — 1 раз.
- T6 (переписан): 0 × `PG-CONFIG-NOT-APPLIED`; notes-default — `Expected: 250` 1 раз.

**k3d (`evidence/`):**

- K1 свежая установка (`profile: standard`, дефолты, локально собранный образ AMP): `SHOW config_file, hba_file, max_connections, shared_buffers, shared_preload_libraries, logging_collector`; `pg_hba_file_rules`; `RESTARTS 0`; `df -h /dev/shm` = 256M; AMP 2 реплики `/-/ready`; `POST /api/v2/alerts` → 200; exporter `pg_up 1`; `kubectl logs` PG содержит строки после «ready to accept connections»; `helm test`.
- K2 upgrade: `helm install` с `main` → алерт + silence → `helm upgrade` на ветку → K1-проверки + алерт/silence на месте.
- K3 rollback: `helm rollback` → PG стартует, данные на месте.

## Deep Review

- **Mandatory triggers present:** `S` (`pg_hba` — доступ к БД).
- **Discretionary triggers present:** none (дифф ожидается < 200 строк).
- **Decision:** required.

## Open Questions

- [ ] P9 (`assumed`): реальные пользователи с pod CIDR вне RFC1918 — не влияет на дизайн (правило `all`), остаётся как обоснование отказа от CIDR-списка.
- [x] Edge Case 7 — закрыт замером (docker-probe E).
