---
id: PG-CONFIG-FIX
slug: pg-config-fix
stream: Helm
type: bug
status: active
created_at: 2026-10-17
updated_at: 2026-10-17
based_on:
  - requirements.md
  - research.md
  - Spec.md
---

# Implementation Plan: встроенный PostgreSQL применяет конфиг чарта

**Based on:** requirements.md / research.md / Spec.md v1.0
**Date:** 2026-10-17

`$SCRATCH` = `/private/tmp/claude-501/-Users-vit-Documents-Projects-AMP/15317cf1-cc49-4001-8dd7-d88e02f11f28/scratchpad`. Номера строк — на `7d5d248`.

## Touched Files

- `helm/amp/templates/postgresql-statefulset.yaml` — `args`, mount ConfigMap в `/etc/postgresql`, `init.sql` по `subPath`, `/dev/shm`; убрать `POSTGRES_INITDB_ARGS` (`:87`), `postgres-config` (`:93-94`, volume `~:227`), mount'ы `*.conf` в `initdb.d` (`:97-104`) (D1, D5).
- `helm/amp/templates/postgresql-configmap.yaml` — `pg_stat_statements.*` после `:81`; `logging_collector` default `"off"` (`:65`); `pg_hba` (`:128-140`); удалить `ALTER SYSTEM` (`:205-207`) и секцию 5 views (`:210-~245`); комментарий бюджета (`:31`) (D2–D5).
- `helm/amp/values.yaml` — NOTE `:355-356` убрать; формула `:357`/`:360`; `loggingCollector: "off"` + комментарий (`:386`) (D4, D5, D6).
- `helm/amp/templates/postgresql-exporter-configmap.yaml` — `relname`/`indexrelname`/`relid` в трёх запросах (отклонение, K1.3).
- `helm/amp/templates/NOTES.txt` — `:25-28`, шаг 4 `:101-110`, строка бюджета (D6).
- `helm/amp/README.md` — `:105` (D6).
- `CHANGELOG.md` — `[Unreleased]` Fixed + migration note (D8).
- `helm/amp/CHANGELOG.md` — `[Unreleased]` Fixed (чартовый changelog; Spec его не называл).
- `tasks/PG-CONFIG-FIX/evidence/` — `render-invariants.txt`, `k3d-acceptance.md`.
- Тесты (Phase 6, после вердикта): `helm/amp/tests/render-postgresql.sh` — T6 переписать, T7.1–T7.8 (D7).
- Не трогаются: `values-production.yaml` (`postgresql.enabled: false`), `values-dev.yaml` (только `enabled: true` — берёт дефолты), `templates/tests/postgresql-test-connection.yaml` (отклонение: в Phase 6 получил Test 5 `SHOW config_file`/`hba_file` — deep review R7(1)).

## Phase 1: Chart — запуск сервера с конфигом (D1, D5)

> Перед правками: `helm template amp helm/amp > $SCRATCH/before-default.yaml`; то же для `--set profile=lite` (`before-lite.yaml`), `--set postgresql.enabled=false` + `externalDatabase.*` (`before-ext.yaml`), `-f values-dev.yaml` (`before-dev.yaml`) — база для инвариантов.

- [x] **1.1** _(готово; `config=null` не рендерится и на `main` — см. Implementation Notes)_ StatefulSet, контейнер `postgres`: под `{{- if .Values.postgresql.config }}` — `args: ["-c", "config_file=/etc/postgresql/postgresql.conf", "-c", "hba_file=/etc/postgresql/pg_hba.conf"]`; mount `custom-config` → `/etc/postgresql` (каталог, `readOnly: true`); `init.sql` по `subPath` в `/docker-entrypoint-initdb.d/init.sql` остаётся. Удалить env `POSTGRES_INITDB_ARGS`, mount и volume `postgres-config`, два `subPath`-mount'а `*.conf`. `command` не задавать. <!-- verify: helm template amp helm/amp | yq 'select(.kind=="StatefulSet") | .spec.template.spec.containers[0] | [.args, .volumeMounts]' — args из 4 элементов, mount /etc/postgresql без subPath, нет INITDB -->
- [x] **1.2** Volume `dshm: emptyDir {medium: Memory, sizeLimit: 256Mi}` → `mountPath: /dev/shm` в контейнере `postgres`, вне условия `config`. <!-- depends: 1.1 | verify: helm template amp helm/amp | grep -c 'medium: Memory' = 1 -->
- [x] **1.3** `helm lint` default/dev; `checksum/config` (`:36`) по-прежнему считается от ConfigMap. <!-- depends: 1.2 | verify: helm lint helm/amp && helm lint helm/amp -f helm/amp/values-dev.yaml -->

**Phase verification:** `helm template` default проходит; `diff` с `before-ext.yaml` пуст.

## Phase 2: Chart — содержимое конфига (D2, D3, D4)

- [x] **2.1** `init.sql`: удалить секцию 4 (`ALTER SYSTEM`, `:204-207`) и секцию 5 (три `CREATE OR REPLACE VIEW`, `:210-248`); секцию 6 `VACUUM ANALYZE` (`:249`) перенумеровать в 4. `postgresql.conf`: после `shared_preload_libraries` — `pg_stat_statements.max = 10000`, `pg_stat_statements.track = all`, `pg_stat_statements.track_utility = on`. <!-- verify: helm template amp helm/amp | grep -cE 'ALTER SYSTEM|CREATE OR REPLACE VIEW' = 0; grep -c 'pg_stat_statements.max = 10000' = 1 -->
- [x] **2.2** `pg_hba.conf`: оставить `local all all trust`, `host all all 127.0.0.1/32 trust`, `host all all ::1/128 trust`; одно `host <database> <username> all scram-sha-256`; `host all all all reject`. Удалить три RFC1918 `md5`, `127.0.0.1/32 md5`, три `replication`. Комментарий: сужение по сети — `postgresql.networkPolicy.enabled`. <!-- verify: helm template ... ConfigMap amp-postgres-config, блок pg_hba.conf: grep -c replication = 0, grep -c md5 = 0, scram-sha-256 = 1, reject = 1 -->
- [x] **2.3** `logging_collector`: шаблон `default "off"`, `values.yaml` `loggingCollector: "off"` + комментарий «`on` пишет в `$PGDATA/pg_log` на PVC без ротации-удаления, логи пропадают из `kubectl logs`». <!-- verify: helm template amp helm/amp | grep -c 'logging_collector = off' = 1 -->
- [x] **2.4** Комментарии бюджета: `configmap :31` и `values.yaml :357/:360` — «25 per AMP pod × replicas + exporter»; значение `250` не меняется. <!-- verify: grep -n 'supports 10 replicas\|10 × 20' helm/amp/values.yaml helm/amp/templates/postgresql-configmap.yaml — пусто -->
- [x] **2.5** _(готово: ext — diff пуст; lite и dev рендерят встроенный PG, их diff байт-в-байт = diff default; вне `amp-postgres`/`amp-postgres-config`/Secret документы идентичны)_ Инварианты: lite, `postgresql.enabled=false` + external — байт-в-байт как до задачи; diff default/dev — только ожидаемые места (D1–D5). Вывод — `evidence/render-invariants.txt`. <!-- depends: 1.1-2.4 | verify: diff $SCRATCH/before-lite.yaml и before-ext.yaml — пусто; diff before-default.yaml — только StatefulSet amp-postgres и ConfigMap amp-postgres-config -->
- [x] **2.6** Коммит `fix(helm): apply the chart PostgreSQL config to the bundled server`. <!-- depends: 2.5 | verify: git show --stat HEAD — statefulset, configmap, values.yaml, evidence -->

**Phase verification:** `helm lint helm/amp`; `bash helm/amp/tests/render-postgresql.sh` — T1–T5 зелёные, T6 падает (ожидаемо до Phase 3/6).

## Phase 3: Docs (D6, D8)

- [x] **3.1** _(готово: существующий блок «CRITICAL: Database Connection Pool» переписан под условие `maxReplicas × 25 ≥ maxConnections` — на дефолтах (10 × 25 = 250) предупреждение остаётся, при `maxReplicas=8` его нет)_ `NOTES.txt`: убрать предупреждение `:25-28`; шаг 4 — «Expected: {{ maxConnections }}»; при `autoscaling.enabled` и `maxReplicas × 25 ≥ maxConnections` — строка про бюджет соединений. <!-- verify: helm install --dry-run=client amp helm/amp | grep -c PG-CONFIG-NOT-APPLIED = 0, grep -c 'Expected: 250' = 1; с --set autoscaling.maxReplicas=10 — строка бюджета есть, с дефолтом — по факту формулы -->
- [x] **3.2** `values.yaml`: убрать NOTE `:355-356`. `README.md:105`: убрать последнее предложение; абзац — бюджет соединений (25 × реплики против `maxConnections`), `pg_hba` пускает из сети только `<database>`/`<username>` по SCRAM, периметр — `postgresql.networkPolicy.enabled`, `/dev/shm` 256Mi входит в `resources.limits.memory`. <!-- verify: grep -rn PG-CONFIG-NOT-APPLIED helm/ — только tests/render-postgresql.sh (T6 до Phase 6) -->
- [x] **3.3** `CHANGELOG.md` `[Unreleased]`: `### Fixed` (`PG-CONFIG-NOT-APPLIED`, падение `init.sql`, `/dev/shm`) + migration note по Spec § Rollout / Rollback: рестарт PG при upgrade; что меняется (`max_connections` 20→250, `shared_buffers` 400kB→256MB, preload, WAL 1/4GB, shm 256Mi, `pg_hba`); `reject` для других ролей/БД; `--reuse-values` и `loggingCollector`; rollback + ручное удаление pod'а при зависшем `OrderedReady`. <!-- verify: grep -n 'PG-CONFIG-NOT-APPLIED\|reuse-values\|helm rollback' CHANGELOG.md -->
- [x] **3.4** Коммит `docs: bundled PostgreSQL config, pg_hba and migration note`. <!-- depends: 3.1-3.3 | verify: git diff --check HEAD~1 -->

**Phase verification:** `helm lint helm/amp`; `git diff --check`.

## Phase 4: Приёмка на k3d (K1–K3)

> Кластер `pg-config-fix` уже есть (research). Все команды — с `--context k3d-pg-config-fix`. До начала: убрать pod'ы `pgshm`, `pgshm2` и ConfigMap `pgconf` в `default` — **с подтверждения владельца** (хук `destructive-command-guard`). Образ AMP — `amp:pg-config-fix` с HEAD ветки, `k3d image import`. Raw-вывод — `evidence/k3d-acceptance.md`, пароли `<redacted>`.

- [x] **4.1** _(отклонение: тестовые pod'ы research не удалял — K1 в namespace `k1`, K2/K3 в `k2`; удаление ждёт владельца в 4.6)_ Подготовка: тестовые pod'ы research убраны, образ импортирован. <!-- depends: 3.4 | verify: kubectl --context k3d-pg-config-fix get pods -A | grep pgshm — пусто; docker exec k3d-pg-config-fix-server-0 crictl images | grep pg-config-fix -->
- [x] **4.2** _(готово: все `SHOW`/`pg_hba_file_rules` = чарт, `RESTARTS 0`, `/dev/shm` 256M, ingest 200, `helm test` OK; exporter был сломан — починен, см. Implementation Notes; port-forward на этом кластере не работает — API через curl-pod)_ K1 свежая установка (дефолты, `profile: standard`, 2 реплики): `SHOW config_file, hba_file, max_connections, shared_buffers, shared_preload_libraries, logging_collector`; `pg_hba_file_rules` = 5 правил чарта; `RESTARTS 0`; `df -h /dev/shm` 256M; AMP `/-/ready`; `POST /api/v2/alerts` → 200; exporter `pg_up 1`; `kubectl logs` PG после «ready to accept connections»; `helm test`. <!-- depends: 4.1 | verify: все пункты записаны в evidence, значения = values -->
- [x] **4.3** _(готово: 4 × 3000000, restarts 0; `memory.peak` 1880 MiB — page cache, `anon` 27 MiB)_ K1 нагрузка: 4 параллельных hash join (сценарий `evidence/k3d-shm-probe.txt`) + ingest — нет `could not resize shared memory`, нет OOMKill (`kubectl get pod -o jsonpath` lastState). <!-- depends: 4.2 | verify: 4 × 3000000; restartCount 0 -->
- [x] **4.4** _(готово: без uninstall/удаления PVC — отдельный namespace `k2`; `main` воспроизвёл баг (`init.sql:56`, restarts 1); после upgrade — конфиг чарта, данные на месте, AMP переподключился без рестарта)_ K2 upgrade: `helm uninstall` + удалить PVC K1 (подтверждение владельца) → `helm install` чартом с `main` (`git worktree` в `$SCRATCH`) → алерт + silence, счётчики → `helm upgrade` на ветку → проверки 4.2 + счётчики совпадают; AMP не получает `pg_hba.conf rejects`. <!-- depends: 4.3 | verify: SHOW config_file — /etc/postgresql/postgresql.conf на старом PVC; счётчики = до upgrade -->
- [x] **4.5** _(готово: pod пересоздан StatefulSet'ом сам, `max_connections=20`, 6 алертов / 2 silence; edge case 2 `--reuse-values` подтверждён dry-run'ом)_ K3 rollback: `helm rollback` на ревизию с `main` → PG стартует на дефолтах образа, данные на месте; если pod не пересоздан — удалить вручную (подтверждение) и записать это в evidence. <!-- depends: 4.4 | verify: SHOW max_connections = 20; счётчики = 4.4 -->
- [ ] **4.6** _(blocked: ждёт подтверждения владельца на удаление кластера)_ Убрать: кластер `pg-config-fix` (подтверждение владельца), вернуть kubectl-контекст `k3d-sema-dev` (сейчас текущий — `k3d-pg-config-fix`, переключён в research). <!-- depends: 4.5 | verify: k3d cluster list — нет pg-config-fix; kubectl config current-context = k3d-sema-dev -->
- [x] **4.7** Коммит `docs: k3d acceptance PG-CONFIG-FIX`. <!-- depends: 4.6 | verify: git show --stat HEAD — evidence/k3d-acceptance.md -->

**Phase verification:** все K1–K3 зелёные в `evidence/k3d-acceptance.md`; при провале — фикс в Phase 1–3, отклонение в Implementation Notes.

## Phase 5: Deep review

- [x] **5.1** _(готово: два ревьюера @ `3271a45` → fix_required, 12 находок; фиксы `42e8828`; повторное ревью → R13–R16, текстовые закрыты, R13 → BUGS; gate pass)_ `/deep-review` (обязателен: `S`) — независимый агент; линзы: security (`pg_hba`, `trust` только loopback/socket, пароль не в ConfigMap/args), correctness (`args` vs entrypoint, upgrade на старом PVC, rollback), contract/migration (дефолт `loggingCollector`, CHANGELOG), premises P1–P9. <!-- depends: 4.7 | verify: jq -r .gate tasks/PG-CONFIG-FIX/review-verdict.json → pass -->

## Phase 6: Tests

- [x] **6.1** _(готово: + бюджет-предупреждение: notes-default есть, `maxReplicas=8` нет)_ `render-postgresql.sh` T6: 0 × `PG-CONFIG-NOT-APPLIED`; notes-default — `Expected: 250` 1 раз. <!-- depends: 5.1 | verify: bash helm/amp/tests/render-postgresql.sh; мутация: вернуть NOTES.txt из main → T6 FAIL -->
- [x] **6.2** _(готово: T7.5 — точный набор 5 правил pg_hba, `trust` только socket (R6); + T7.10 rules: нет `now()`, есть `> 0` (R2); + T7.11 `helm test` проверяет `config_file`/`hba_file` (R7(1)), на k3d `k2` rev 5 — Succeeded. Мутации: каждый из NOTES, statefulset, configmap, exporter-configmap, prometheus-rules, test-connection, values.yaml из `main` → FAIL)_ T7.1–T7.8 по Spec § Test Plan (args; нет INITDB и mount'ов `*.conf` в `initdb.d`, есть `init.sql`; один `/etc/postgresql` из ConfigMap; нет `ALTER SYSTEM`/`VIEW`, есть `pg_stat_statements.max`; `pg_hba` без `replication`/`md5`, `scram-sha-256` и `reject`; `logging_collector = off`; `/dev/shm` Memory; T7.7 снят — см. Implementation Notes) + T7.9 exporter: `DATA_SOURCE_URI`/`USER`/`PASS`, нет `DATA_SOURCE_NAME` и `$(POSTGRES_`, `PG_EXPORTER_AUTO_DISCOVER_DATABASES` `"false"`, в exporter ConfigMap 0 × `tablename`/`indexname as`. <!-- depends: 5.1 | verify: bash helm/amp/tests/render-postgresql.sh — все ok; мутация: templates/postgresql-*.yaml из main → T7 FAIL по каждому пункту -->
- [x] **6.3** Коммит `test(helm): cover the bundled PostgreSQL config wiring`. <!-- depends: 6.1, 6.2 | verify: git show --stat HEAD -->

## Phase 7: Testing

- [ ] **7.1** `scripts/release-gate.sh` — RESULT: PASS (helm-tests берёт `tests/*.sh`). <!-- depends: 6.3 | verify: вывод — evidence/release-gate.txt -->
- [ ] **7.2** `git diff --check main...HEAD`; `git status` чист. <!-- depends: 7.1 | verify: пусто -->

## Phase 8: Finalize

- [ ] **8.1** `/finalize`: BUGS `PG-CONFIG-NOT-APPLIED` закрыт; follow-ups из Phase 4 (Implementation Notes); BACKLOG `PG-CONFIG-FIX` → закрытые; DONE, NEXT (WIP снять); follow-ups: бюджет `max_connections` при `maxReplicas: 10` → `HELM-SINGLE-NODE-DEFAULTS`; preStop `su - postgres` под uid 999 не работает → BUGS/BACKLOG P2; `alert_history` суперпользователь → TECH-DEBT `HELM-CHART-GAPS`; находки ревью; архив workspace; sync с `main`. <!-- depends: 7.1, 7.2 | verify: ls tasks/archive/PG-CONFIG-FIX -->
- [ ] **8.2** `/merge-to-main`; push — по решению владельца. <!-- depends: 8.1 | verify: git log main -1 -->

## Definition of Done

- [ ] All steps are complete or explicitly marked blocked/skipped
- [ ] Success criteria from `requirements.md` are covered
- [ ] Contracts from `Spec.md` are implemented or deviations are recorded
- [ ] Deep review verdict is `pass`
- [ ] Tests for changed behavior are added or updated
- [ ] Phase checks pass
- [ ] Docs/planning are updated (NOTES, values, README, CHANGELOG, BUGS, BACKLOG)

## Implementation Notes

- **Отклонение от Spec (2026-10-17, Phase 1): `postgresql.config: null` — не аварийный выход.** Spec (D1, Edge Case 1, Rollout/Rollback, T7.7) считал его существующим поведением шаблона — premise не проверялся. Замер: и на `main`, и на ветке рендер падает `nil pointer evaluating interface {}.maxConnections` (`postgresql-configmap.yaml:25`; то же в `NOTES.txt:101` и `deployment.yaml:140`) — `evidence/render-invariants.txt`. Сделать его рабочим — guard'ы в четырёх шаблонах, вне scope. Решение: единственный откат — `helm rollback`; из migration note и README `config=null` убран, T7.7 снят. Условия `{{- if .Values.postgresql.config }}` в StatefulSet оставлены как были (существующий паттерн). Follow-up в finalize: TECH-DEBT `HELM-CHART-GAPS` — `postgresql.config: null` не рендерится.
- **Отклонение от Spec Invariants:** «lite рендерит то же, что до задачи» неверно — lite тоже рендерит встроенный PG (`postgresql.enabled: true` по умолчанию), его diff совпадает с default. Инвариант заменён: вне документов PG рендер не меняется (2.5).
- **Отклонение от Spec (2026-10-17, Phase 4): фикс `postgres-exporter`.** Критерий requirements «`postgres-exporter` подключается» на ветке не выполнялся: `pg_up 0`, exporter входил ролью `$(POSTGRES_USER)` — `DATA_SOURCE_NAME` ссылался на переменные, объявленные ниже в списке env (с `05c9ee0`, 2025-12-04; на `main` exporter не подключался никогда). Три слоя, каждый вскрылся после предыдущего (`evidence/k3d-acceptance.md` K1.3): (1) `DATA_SOURCE_URI`/`DATA_SOURCE_USER`/`DATA_SOURCE_PASS` — пароль не в URL, порядок env не важен (вариант «переставить `DATA_SOURCE_NAME` ниже» отклонён: пароль со спецсимволами ломает URL, а хук `secret-leak-guard` не даёт записать DSN с паролем); (2) `queries.yaml` — `tablename`/`indexname` в `pg_stat_user_*` не существуют → `relname`/`indexrelname`, размер через `relid`; (3) `PG_EXPORTER_AUTO_DISCOVER_DATABASES` `"false"` — discovery гонял кластерные запросы в каждой БД, дубли серий роняли весь scrape с 500. Итог: `pg_up 1`, 1231 серия, 0 ошибок запросов. Spec D1–D8 не покрывали — добавлен в Spec § Deviations, CHANGELOG (корневой и чартовый), T7.9.
- **Follow-ups из Phase 4 (в finalize):** (a) ConfigMap exporter'а не входит в `checksum/config` pod'а PG — правка `queries.yaml` без смены конфига PG не рестартует exporter (`HELM-CHART-GAPS`); (b) Service `amp` выбирает и pod `amp-redis-0` (`kubectl port-forward svc/amp` → `Pod 'amp-redis-0' does not have a named port 'http'`) — селектор шире нужного, трафик не задет (endpoint'ы без порта `http` не попадают) (`HELM-CHART-GAPS`); (c) AMP падает с exit 1, пока PG стартует (нет ожидания БД) — 3–8 рестартов на install (BUGS, P3); (d) правила `postgresql-prometheus-rules.yaml` ссылаются на метрики, наличие которых не проверено (`pg_settings_max_connections`, `pg_stat_statements_mean_exec_time_seconds`, `pg_replication_lag`) — теперь exporter отдаёт данные, правила стоит сверить (BACKLOG).
- Phase 4 идёт до deep-review: ревьюер получает измеренный upgrade-путь, а не только рендер. Если ревью потребует правок механизма (D1/D3) — повторить 4.2/4.4.
