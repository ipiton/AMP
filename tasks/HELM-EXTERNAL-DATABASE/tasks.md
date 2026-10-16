---
id: HELM-EXTERNAL-DATABASE
slug: helm-external-database
stream: Helm
type: feature
status: active
created_at: 2026-10-16
updated_at: 2026-10-16
based_on:
  - requirements.md
  - research.md
  - Spec.md
---

# Implementation Plan: внешний PostgreSQL через values чарта

**Based on:** requirements.md / research.md / Spec.md v1.0
**Date:** 2026-10-16

Spec Open Question «`sslMode` в `values-production.yaml`» закрыт дефолтом из Spec — `require` (владелец не возражал, 2026-10-16). Ветка синхронизирована с `main` (`eb8e4ca`, `go1.26.9`).

## Touched Files

- `go-app/internal/database/postgres/config.go` — `DSN()` через `net/url` (D6).
- `helm/amp/values.yaml` — блок `externalDatabase` (D1).
- `helm/amp/templates/deployment.yaml` — guard'ы и ветка внешней БД (D2, D3); удалена захардкоженная аннотация `deployment.kubernetes.io/revision` (отклонение, см. Implementation Notes)
- `helm/amp/templates/NOTES.txt` — строка о внешней БД / `configFile`-пути (D5).
- `helm/amp/values-production.yaml` — `postgresql.enabled: false`, `externalDatabase` (D4).
- `helm/amp/values-dev.yaml` — `postgresql.enabled: true` (отклонение, см. Implementation Notes).
- `helm/amp/tests/values-production-placeholders.yaml` — заглушки `externalDatabase.*` (D4).
- `helm/amp/README.md`, `docs/MIGRATION_QUICK_START.md`, `helm/amp/docs/POSTGRESQL_RESTORE_GUIDE.md`, `CHANGELOG.md`, `docs/06-planning/TECH-DEBT.md` (D8, D9, D6 follow-up).
- Тесты (Phase 7, после вердикта): `helm/amp/tests/render-external-database.sh` (новый), `helm/amp/tests/render-postgresql.sh`, `helm/amp/tests/render-defaults-validate.sh`, `helm/amp/tests/render-ingress-auth.sh`, `go-app/internal/database/postgres/pool_test.go`.

## Phase 1: Go — экранирование DSN (D6)

- [x] **1.1** `DSN()` собирать через `url.URL{Scheme: "postgres", User: url.UserPassword(User, Password), Host: net.JoinHostPort(Host, strconv.Itoa(Port)), Path: "/" + Database, RawQuery: "sslmode=" + url.QueryEscape(SSLMode)}`; комментарий — почему (пароль провайдера с `/ # ? %`). `ConnectionString()` не трогать. <!-- verify: cd go-app && GOTOOLCHAIN=go1.26.9 go vet ./internal/database/... && go test ./internal/database/postgres/ -run 'TestPostgresConfig_DSN' -count=1 — существующий тест зелёный (строка для обычных значений не изменилась) -->
- [x] **1.2** Проба на тех же паролях, что `evidence/dsn-password-escaping.txt`, но через `PostgresConfig.DSN()` → `pgxpool.ParseConfig`: все `pw_match=true`; вывод — `evidence/dsn-password-escaping-after.txt` (скрипт — в scratchpad, не в репо). <!-- depends: 1.1 | verify: grep -c 'pw_match=true' evidence/dsn-password-escaping-after.txt = 8, grep -c ERR = 0 -->
- [x] **1.3** TECH-DEBT: `ConnectionString()` и `config.GetDatabaseURL()` — тот же дефект, в рантайме не вызываются (`internal/config/example.go:94` — единственный вызов). <!-- verify: grep -n 'GetDatabaseURL' docs/06-planning/TECH-DEBT.md -->
- [x] **1.4** Коммит `fix(db): escape credentials in the PostgreSQL DSN`. <!-- depends: 1.1-1.3 | verify: git show --stat HEAD — config.go, TECH-DEBT.md, evidence -->

**Phase verification:** `GOTOOLCHAIN=go1.26.9 make -C go-app quality-gates-fast`, `git status` после `go fmt`.

## Phase 2: Chart — values и Deployment (D1–D3)

> Перед правками: `helm template amp helm/amp > $SCRATCH/before-default.yaml`, то же для `--set profile=lite` и для `postgresql.enabled=false` + `configFile` с `database:` — база для инвариантов.

- [x] **2.1** `values.yaml`: блок `externalDatabase` сразу после `postgresql` — 7 ключей и комментарии из Spec § API Contracts (`sslMode: disable` в production отклоняется AMP; `verify-*` — системные корни образа; пароль ≥ 12 в production; Secret чарт не создаёт; `externalDatabase` перекрывает `database:` в конфиге). <!-- verify: helm template amp helm/amp | diff - $SCRATCH/before-default.yaml — пусто -->
- [x] **2.2** `deployment.yaml`: в блоке «Database Configuration» — `$ext := .Values.externalDatabase | default dict`; guard'ы D3 (обе БД; `existingSecret`/`database`/`username` при `host`; enum `sslMode`; «БД нет нигде» с `regexMatch "(?m)^database:"` по `configFile.content` только при `configFile.enabled`); ветки: встроенный (без изменений) / внешний (6 env, `secretKeyRef`, без insecure-флага и `STORAGE_FILESYSTEM_PATH`) / `else` как до задачи. <!-- depends: 2.1 | verify: ручные рендеры из Spec Edge Cases 1, 2, 9, 10 и T-ext-1…8 — ожидаемые env/ошибки; insecure-флаг: helm template ... --set postgresql.enabled=false --set externalDatabase.host=db --set externalDatabase.database=amp --set externalDatabase.username=amp --set externalDatabase.existingSecret=s --set externalDatabase.sslMode=disable | grep -c ALLOW_INSECURE = 0 -->
- [x] **2.3** Инварианты: дефолт, lite и `configFile`-путь рендерятся байт-в-байт как до задачи. <!-- depends: 2.2 | verify: три diff против $SCRATCH/before-*.yaml — пусто; вывод — evidence/render-invariants.txt -->
- [x] **2.4** Коммит `feat(helm): external PostgreSQL via externalDatabase values`. <!-- depends: 2.3 | verify: git show --stat HEAD -->

## Phase 3: Chart — production values и NOTES (D4, D5)

- [x] **3.1** `values-production.yaml`: `postgresql.enabled: false`, остальные ключи `postgresql.*` убрать, переписать комментарий (ADR-016, migration note, ссылка на README); блок `externalDatabase` с пустыми `host`/`database`/`username`/`existingSecret`, `sslMode: require`. <!-- verify: helm template amp helm/amp -f helm/amp/values-production.yaml -f helm/amp/tests/values-production-placeholders.yaml 2>&1 | grep -q 'externalDatabase' — падает на guard'е; grep -c '^  [a-z]' в секции postgresql — только enabled -->
- [x] **3.2** `tests/values-production-placeholders.yaml`: `externalDatabase.{host,database,username,existingSecret}` заглушками `release-gate-placeholder` + строка в шапке. <!-- depends: 3.1 | verify: helm lint и helm template с placeholders и --set из release-gate — OK; в рендере нет StatefulSet amp-postgres, есть DATABASE_HOST: "release-gate-placeholder" -->
- [x] **3.3** `NOTES.txt`: внешняя БД — `host:port/db (sslmode=…)`, Secret `имя/ключ`; `configFile`-путь — предупреждение про пароль в ConfigMap и ссылка на `externalDatabase`; блок про пул соединений — без изменений по смыслу. <!-- depends: 2.2 | verify: helm install --dry-run=client amp helm/amp ... (обе ветки) — NOTES без значения пароля -->
- [x] **3.4** Коммит `feat(helm): point values-production at an external PostgreSQL`. <!-- depends: 3.1-3.3 | verify: git show --stat HEAD -->

**Phase verification:** `helm lint` default / dev / production+placeholders; `scripts/release-gate.sh` в этой фазе не гонять — существующие render-тесты ждут встроенный PG в production и чинятся в Phase 6.

## Phase 4: Прогон на k3d (D9)

> Kubectl-контекст владельца не менять: кластер создаётся с `--kubeconfig-switch-context=false`, все команды — с `--context k3d-amp-extdb`; в конце — `kubectl config current-context` = значение до начала (`k3d-sema-dev` на 2026-10-16). Raw-вывод — `evidence/k3d-external-drill.md`. Пароли в evidence — `<redacted>`.

- [x] **4.1** Кластер `amp-extdb`, образ `amp:extdb-drill` с HEAD ветки, `k3d image import`. <!-- depends: 1.4, 3.4 | verify: docker exec k3d-amp-extdb-server-0 crictl images | grep extdb-drill; kubectl config current-context — не изменился -->
- [x] **4.2** Установка со встроенным PG (1 реплика, HPA выкл.), алерты + silence, счётчики `alerts`/`silences`/`alert_investigations`. <!-- depends: 4.1 | verify: /-/ready OK; счётчики записаны -->
- [x] **4.3** Внешний `postgres:16` в docker-сети `k3d-amp-extdb` (`-c ssl=on` + snakeoil), роль `amp` без superuser с паролем, содержащим `/` и `%`, пустая БД с владельцем `amp`; Secret в namespace; адрес — IP контейнера в сети k3d. <!-- depends: 4.1 | verify: под в кластере: psql "sslmode=require" → select 1 -->
- [x] **4.4** Перенос по migration note: `pg_dump -Fc` встроенного → AMP в 0 → `helm upgrade` на `postgresql.enabled=false` + `externalDatabase` (0 реплик) → `pg_restore --no-owner` без `--clean` → AMP в 1. Записать: что стало с PVC встроенного PG. <!-- depends: 4.2, 4.3 | verify: pg_restore exit 0; kubectl get sts — нет amp-postgres; kubectl get pvc — PVC встроенного (ожидание: остался) -->
- [x] **4.5** Проверки runbook § 4: `/-/ready`, алерты и silences на месте, счётчики совпадают, миграции не переприменены; `pg_stat_ssl.ssl = t` у подключений AMP; в логе нет `NOT encrypted`; env-приоритет — при `configFile` из production values с `database:` → побеждает `externalDatabase` (premise 1). <!-- depends: 4.4 | verify: все пункты в evidence -->
- [x] **4.6** Rollback: `helm rollback` на ревизию со встроенным PG поднимает его на старом PVC с исходными данными. <!-- depends: 4.5 | verify: счётчики = значения из 4.2 -->
- [x] **4.7** Убрать кластер и контейнер. <!-- depends: 4.6 | verify: k3d cluster list — нет amp-extdb; docker ps -a | grep extdb — пусто; kubectl config current-context — как до начала -->

## Phase 5: Docs (D8, D9)

- [x] **5.1** `POSTGRESQL_RESTORE_GUIDE.md`: § «External PostgreSQL» — команды ровно из 4.3–4.5, без «not drilled»; строка 9; новый подраздел «Move from the bundled to an external PostgreSQL» по 4.4/4.6. <!-- depends: 4.7 | verify: grep -c 'not drilled' helm/amp/docs/POSTGRESQL_RESTORE_GUIDE.md = 0 -->
- [x] **5.2** `helm/amp/README.md` (баннер `:15`, § PostgreSQL `:107` — пример `externalDatabase` + `kubectl create secret generic`, edge cases 3, 5, 6, 7 одной фразой каждый, `configFile` как запасной путь), `docs/MIGRATION_QUICK_START.md:24`. <!-- depends: 3.4 | verify: grep -n 'planned (`HELM-EXTERNAL-DATABASE`)' — пусто; примеры values из README рендерятся (helm template с ними — OK) -->
- [x] **5.3** `CHANGELOG.md` `[Unreleased]`: Added (`externalDatabase.*`), Breaking/migration note (`values-production.yaml`, «БД нет нигде» → fail), Fixed (DSN). <!-- depends: 5.1 | verify: grep -n HELM-EXTERNAL-DATABASE CHANGELOG.md -->
- [x] **5.4** Коммит `docs: external PostgreSQL values, runbook drill and migration note`. <!-- depends: 5.1-5.3 | verify: git diff --check -->

## Phase 6: Deep review

- [ ] **6.1** _(прогон 1 @ 6eb1774: fix_required, 2 major + 7 minor + 7 nit, см. review-findings.md)_ `/deep-review` (обязателен: `S`, 4 сигнала) — независимый агент; линзы: security (путь пароля, TLS-флаг, NOTES), contract (values, breaking), correctness guard'ов и веток, premises, Go-DSN. <!-- depends: 5.4 | verify: jq -r .gate tasks/HELM-EXTERNAL-DATABASE/review-verdict.json → pass -->

## Phase 7: Tests

- [ ] **7.1** `helm/amp/tests/render-external-database.sh` — T-ext-1…10 из Spec § Component Architecture D7 (env + `secretKeyRef`; нет insecure-флага при любом `sslMode`; `required` на Secret/БД/пользователя; конфликт двух БД; enum `sslMode`; «БД нет нигде» — fail и `configFile` с `database:` — проходит; `--reuse-values`-случай без ключа; lite не тронут; production рендерится с placeholders и без StatefulSet; без placeholders — fail). <!-- depends: 6.1 | verify: bash helm/amp/tests/render-external-database.sh — все ok; мутация: вернуть deployment.yaml из main → тест FAIL -->
- [ ] **7.2** Правки `render-postgresql.sh` T1 (production — нет StatefulSet/образа PG) и `render-defaults-validate.sh` T1/T3 + `check_secret_refs` (разрешена ссылка на `externalDatabase.existingSecret`). <!-- depends: 6.1 | verify: bash helm/amp/tests/render-postgresql.sh && bash helm/amp/tests/render-defaults-validate.sh -->
- [ ] **7.3** Go: `TestPostgresConfig_DSN_EscapesCredentials` в `pool_test.go` — round-trip через `pgxpool.ParseConfig` для `/ # ? % пробел @ :` в пароле, `@ :` в пользователе, `/`/пробел в имени БД. <!-- depends: 6.1 | verify: cd go-app && go test ./internal/database/postgres/ -run DSN -count=1; мутация: старый Sprintf → тест FAIL -->

## Phase 8: Testing

- [ ] **8.1** `GOTOOLCHAIN=go1.26.9 scripts/release-gate.sh` — все шаги PASS (helm-tests подхватывает новый скрипт). <!-- depends: 7.1-7.3 | verify: RESULT: PASS; вывод — evidence/release-gate.txt (без хвостовых пробелов) -->
- [ ] **8.2** `git diff --check main...HEAD`, нет `_, _ :=` в диффе. <!-- depends: 8.1 | verify: пусто -->

## Phase 9: Finalize

- [ ] **9.1** `/finalize`: DONE, NEXT (WIP снять), BACKLOG P0 → закрытые; follow-ups (TECH-DEBT из 1.3, находки ревью); архив workspace; sync с `main`. <!-- depends: 8.1, 8.2 | verify: ls tasks/archive/HELM-EXTERNAL-DATABASE -->
- [ ] **9.2** `/merge-to-main`; push — по решению владельца. <!-- depends: 9.1 | verify: git log main -1 -->

## Definition of Done

- [ ] All steps are complete or explicitly marked blocked/skipped
- [ ] Success criteria from `requirements.md` are covered
- [ ] Contracts from `Spec.md` are implemented or deviations are recorded
- [ ] Deep review verdict is `pass`
- [ ] Tests for changed behavior are added or updated
- [ ] Phase checks pass
- [ ] Docs/planning are updated (README, runbook, MIGRATION_QUICK_START, CHANGELOG, TECH-DEBT)

## Implementation Notes

- **Отклонение от Spec (2026-10-16, Phase 3): `helm/amp/values-dev.yaml`.** Research/Spec его не учли: там `profile: standard` (по умолчанию) + `postgresql.enabled: false` без БД, с комментарием «SQLite for development». Guard «БД нет нигде» его ловит, и `helm-dev`/`render-defaults-validate.sh` T1 падают. В standard-профиле SQLite нет (`config.go` `validateProfile`: `storage.backend=postgres`; дефолт `database.host=localhost`), так что под из dev-values и до задачи не мог подключиться к БД — guard поймал реальный дефект. Фикс: `postgresql.enabled: true` (встроенный PG — dev/pilot по ADR-016), комментарий исправлен. Не `profile: lite`: тогда `helm-rbac[dev]` в release-gate ждёт 1 Role, а lite даёт 0 — пришлось бы править гейт. В CHANGELOG — строка в Fixed.
- Комментарий в ветке внешней БД `deployment.yaml` не содержит имени insecure-флага: иначе `grep -c ALLOW_INSECURE` в проверках считал бы комментарий.
- **Не учтено в Spec D7:** `helm/amp/tests/render-ingress-auth.sh` `prod-external` (`:106`) рендерит production без `values-production-placeholders.yaml` и теперь падает на guard'е «БД нет нигде». Phase 7: добавить ему `externalDatabase.*` (или `-f` placeholders). Прочие render-тесты зелёные; падают ровно `render-postgresql.sh` T1, `render-defaults-validate.sh` T1/T3 и этот кейс (прогон 2026-10-16).
- **Дополнение к списку падающих тестов:** `render-postgresql.sh` `notes-external` (`:176`, `--set postgresql.enabled=false` без БД) — тот же guard. Phase 7: дать ему `externalDatabase.*` и проверять новую строку NOTES. (Ранний прогон «все rc=0» был ложным: `$?` читался после `$(basename …)`.)
- **Отклонение от Spec (2026-10-16, Phase 4): фикс `deployment.kubernetes.io/revision` в `deployment.yaml`.** Прогон D9 упёрся в дефект чарта, существовавший до задачи (с `e1d520e`, 2025-12-04): шаблон хардкодил `deployment.kubernetes.io/revision: "1"`. Под Helm 4 (server-side apply) после первого rollout с изменённым pod template любой следующий `helm upgrade`/`rollback` падает на конфликте с менеджером `k3s` (evidence `k3d-external-drill.md` F4). Migration note задачи требует двух upgrade подряд (0 реплик → обратно), так что без фикса прод-путь не проходит. Фикс в корень: строка удалена (аннотацию ведёт контроллер Deployment). Следствия: инвариант Spec «дефолтный рендер не меняется» нарушен ровно на эти 2 строки (default и lite); rollback на ревизии, отрендеренные старым чартом, под Helm 4 требует `--force-conflicts` (проверено, rev 8) — в migration note и runbook. Helm 3 (client-side 3-way merge) дефект не задевал: значение в старом и новом манифесте одинаково. Тест — Phase 7 (render: аннотации нет). CHANGELOG — Fixed.
- **Находки прогона для runbook (D9):** (1) дамп встроенного PG содержит `pg_stat_statements` (создаёт init-скрипт `postgresql-configmap.yaml:159`, не trusted) → без superuser restore падает; (2) GRANT'ы на роль `alert_history` → нужен `--no-privileges`; (3) если расширения заранее создал superuser провайдера, restore без `--clean` падает на `COMMENT ON EXTENSION` → `-L`-список без `pg_stat_statements` и без `COMMENT - EXTENSION`; (4) `--clean` работает, только если восстанавливающая роль владеет расширениями — текущая формулировка runbook («`--clean` fails on DROP EXTENSION without superuser») неточна. Единый рецепт проверен на 4 случаях (§ 7 evidence). (5) Dump снимать после остановки AMP, иначе записи между dump и стопом теряются — второй проход сделан в таком порядке.
- **Фиксы deep-review @ `6eb1774` (2026-10-16), `review-findings.md` R1–R14.** Код: enum `sslMode` сужен до 4 значений, которые принимает `PostgresConfig.Validate()` (R1); guard «БД нет нигде» — `fromYaml` вместо `regexMatch` (R4, Spec premise и Edge Case 7 обновлены); `DSN()` снимает скобки с IPv6-хоста перед `JoinHostPort` (R3). Docs: совет «остаться на встроенном» в CHANGELOG и `values-production.yaml` (R2), README/runbook — Secret через `printf '%s'` + `--from-file` (R6), пул × HPA (R7), ESO (R8), `pg_trgm` (R10), `rollout restart` после смены Secret и оговорка к `--force-conflicts` (R13), мониторинг (R14). R5/R9: третий проход § 3c с HPA и финальным фильтром — evidence § 9; Deployment возвращается к 1 реплике сам, в runbook добавлена проверка `.spec.replicas`. Deferred: guard на слабый пароль встроенного PG (R2) → `HELM-CHART-GAPS`, `helm test` для внешней БД (R13) → BACKLOG — оба в finalize.
