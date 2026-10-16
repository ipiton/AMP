---
id: HELM-EXTERNAL-DATABASE
slug: helm-external-database
stream: Helm
type: feature
status: draft
created_at: 2026-10-16
updated_at: 2026-10-16
based_on:
  - requirements.md
  - research.md
---

# Specification: внешний PostgreSQL через values чарта

**Version:** 1.0  
**Status:** Draft

## Summary

Чарт получает values `externalDatabase.*`: при `postgresql.enabled: false` и заданном `externalDatabase.host` под получает `DATABASE_*`, пароль — только из существующего Secret, `sslMode` по умолчанию `require`. Старый путь «реквизиты в `configFile.content`» остаётся (решение владельца, Option B), а конфигурация «БД нет нигде» падает на рендере. `values-production.yaml` переходит на внешнюю БД (ADR-016). DSN в Go начинает экранировать пароль, иначе пароль провайдера с `/ # ? %` не доходит до pgx.

## Requirements Coverage

| Requirement / Criterion | Covered by |
|---|---|
| values `externalDatabase.*` с дефолтами | D1 |
| `DATABASE_*` из values, пароль `secretKeyRef`, `required` на `existingSecret` | D2, D3 |
| пустой `host` ⇒ путь `configFile.content` как раньше (Option B) | D2, D3, Invariants |
| insecure-SSL флаг не рендерится для внешней БД | D2, T-ext-4 |
| встроенный PG и lite без изменений | Invariants, T-ext-7/8, существующие тесты |
| `values-production.yaml` на внешней БД, без неё не рендерится | D4, D3, T-ext-9/10 |
| DSN экранирует user/password/db | D6, Go-тест |
| render-тест в гейте | D7 |
| restore во внешний PG 16 прогнан, «not drilled» снято | D9 |
| README / NOTES / CHANGELOG | D5, D8 |

## Current State

- **Code:** `helm/amp/templates/deployment.yaml:94-116` — `DATABASE_*` только для встроенного PG, `else` — `STORAGE_FILESYSTEM_PATH`; `configmap.yaml:12` — `STORAGE_BACKEND=postgres` для `standard`; `NOTES.txt:25,52,90`; `internal/database/postgres/config.go:142-145` `DSN()` — `fmt.Sprintf` без экранирования.
- **Data:** не применимо (схема не меняется).
- **Tests:** `helm/amp/tests/render-*.sh` (9 скриптов, все гоняет `scripts/release-gate.sh` § `step_helm_tests`); `render-postgresql.sh` T1 и `render-defaults-validate.sh` T1 ждут встроенный PG в production; `go-app/internal/database/postgres/pool_test.go:325-338` `TestPostgresConfig_DSN`.
- **Docs:** `helm/amp/README.md:15,107`, `docs/MIGRATION_QUICK_START.md:24`, `helm/amp/docs/POSTGRESQL_RESTORE_GUIDE.md:9,118-123`, `values-production.yaml:48-80`.

## Design Premises

| Premise | Confirmed by | Class | If wrong |
|---|---|---|---|
| env `DATABASE_*` перекрывает `database.*` из config-файла | `config.go:671-672,838-839` `AutomaticEnv` + `SetEnvKeyReplacer`; viper ставит env выше config | code-read | `externalDatabase` молча проигрывает старому `database:` в `configFile` — ловится прогоном D9 (там `configFile` из production values) |
| Других каналов передать БД поду, кроме `DATABASE_*` в `deployment.yaml` и `database:` в config-файле, у чарта нет | grep `extraEnv\|envFrom` по `values.yaml`/`deployment.yaml`: только `envFrom` на `<fullname>-config`, в `configmap.yaml` нет `DATABASE` | call-path-traced | guard D3 ломает установку, которая подаёт БД иначе (post-renderer/kustomize-патч env) — описано в Edge Cases |
| Пул и миграции строят подключение из одного `DSN()` | `internal/database/postgres/pool.go:174`, `internal/database/migrations.go:185`; `PostgresConfigFrom` `reloadable_database.go:31-38`; grep `ConnectionString()`/`GetDatabaseURL()` вне тестов — только `internal/config/example.go:94` | call-path-traced | фикс D6 не покрывает какой-то путь — Go-тест и прогон с паролем `/` `%` |
| Пароль с `/ # ? пробел` не парсится, с `%` портится | `evidence/dsn-password-escaping.txt` (pgx v5.9.2) | measured | — |
| `postgres:16` поднимает TLS со snakeoil-сертификатом; владелец БД без superuser создаёт `uuid-ossp` и `btree_gin` | `evidence/pg16-tls-nonsuperuser.txt` (16.15) | measured | — |
| Helm `regexMatch "(?m)^database:"` различает ключ верхнего уровня и вложенный | проба `helm template` (Helm v4.3.0): верхний — `true`, вложенный — `false` | measured | guard D3 ошибается на легальной конфигурации |
| `values-production.yaml` содержит `configFile.enabled: true` без `database:` | `values-production.yaml:451-` | code-read | production рендерится без БД — T-ext-10 |

## Target Design

В `values.yaml` появляется блок `externalDatabase`. Шаблон выбирает источник БД в `standard`-профиле в таком порядке: встроенный PG (`postgresql.enabled`) → внешний (`externalDatabase.host` задан) → `database:` в `configFile.content` → иначе `fail`. Для внешней БД шаблон рендерит `DATABASE_HOST/PORT/DATABASE/USERNAME/SSL_MODE` из values и `DATABASE_PASSWORD` через `secretKeyRef` на `externalDatabase.existingSecret`; insecure-SSL флаг и `STORAGE_FILESYSTEM_PATH` в этой ветке не рендерятся. Ошибки конфигурации (нет Secret, неверный `sslMode`, обе БД сразу, БД нет нигде) падают на рендере с текстом, называющим ключ. `values-production.yaml` выключает встроенный PG и ставит пустой `externalDatabase`: без реквизитов файл не рендерится, как и без `webConfig.existingSecret`. В Go `DSN()` собирается через `net/url`, чтобы любые символы пароля доходили до pgx. Restore-runbook и путь переноса «встроенный → внешний» прогоняются на k3d с внешним `postgres:16`.

## API Contracts

Публичный контракт — values чарта и env пода.

### values: `externalDatabase`

```yaml
externalDatabase:
  host: ""                        # пусто = внешняя БД через values выключена
  port: 5432
  database: ""                    # required, если host задан
  username: ""                    # required, если host задан
  sslMode: require                # disable | allow | prefer | require | verify-ca | verify-full
  existingSecret: ""              # required, если host задан; чарт этот Secret не создаёт
  existingSecretPasswordKey: password
```

- Комментарии в `values.yaml`: `sslMode: disable` под `environment: production` AMP отклонит на старте (чарт флаг-обход для внешней БД не ставит); `verify-ca`/`verify-full` проверяют сертификат по системным корням образа, CA провайдера (RDS и т. п.) туда не входит, клиентских TLS-материалов чарт не монтирует; под `environment: production` пароль ≥ 12 символов. Порт — число; в env уходит строкой.
- `profile: lite` блок игнорирует.

### env пода (ветка внешней БД)

| Env | Источник |
|---|---|
| `DATABASE_HOST` | `externalDatabase.host` (`quote`) |
| `DATABASE_PORT` | `externalDatabase.port` (`quote`) |
| `DATABASE_DATABASE` | `externalDatabase.database` |
| `DATABASE_USERNAME` | `externalDatabase.username` |
| `DATABASE_PASSWORD` | `secretKeyRef: {name: existingSecret, key: existingSecretPasswordKey}` |
| `DATABASE_SSL_MODE` | `externalDatabase.sslMode` |

Не рендерятся: `DATABASE_ALLOW_INSECURE_SSL_IN_PRODUCTION`, `STORAGE_FILESYSTEM_PATH`.

### Render-ошибки (`fail`/`required`)

| Условие (`profile: standard`) | Сообщение называет |
|---|---|
| `postgresql.enabled` и `externalDatabase.host` одновременно | «выбери одно: `postgresql.enabled=false` для внешней БД» |
| `host` задан, пуст `existingSecret` / `database` / `username` | соответствующий ключ; для Secret — что чарт его не создаёт и какой ключ нужен |
| `sslMode` вне шести значений | ключ и список |
| `postgresql.enabled: false`, `host` пуст, и нет `configFile.enabled` с `database:` верхнего уровня в `configFile.content` | `externalDatabase.host` (рекомендуемый путь) и `database:` в `configFile.content` (пароль в ConfigMap) |

## Data Model / Migrations

- **Migration:** no (схема и данные не трогаются).
- **Перенос установок со встроенного PG на внешний** (migration note в `CHANGELOG.md` и runbook): `pg_dump -Fc` встроенного → пустая БД и владелец на внешнем → AMP в 0 реплик → `helm upgrade` на `externalDatabase` с 0 репликами → `pg_restore` без `--clean` → AMP обратно. PVC встроенного PG `helm upgrade` не удаляет (volumeClaimTemplate) — удалить вручную после проверки. Точный порядок шагов фиксируется прогоном D9.
- **Rollback / forward-fix:** `helm rollback` на ревизию со встроенным PG поднимает StatefulSet на старом PVC, если его не удалили; данные, записанные во внешнюю БД после переключения, туда не возвращаются.

## Component Architecture

- **D1** `helm/amp/values.yaml` — блок `externalDatabase` после `postgresql`, с комментариями из § API Contracts.
- **D2** `helm/amp/templates/deployment.yaml` — блок «Database Configuration»: три ветки `standard` (встроенный / внешний / ничего — для `configFile`-пути, как до задачи: `STORAGE_FILESYSTEM_PATH` в ней остаётся, чтобы рендер Option B не менялся) + lite.
- **D3** guard'ы — в начале того же блока `deployment.yaml` (рядом с использованием; отдельный helper не заводим).
- **D4** `helm/amp/values-production.yaml` — `postgresql.enabled: false`; удалить из файла остальные ключи `postgresql.*` (мёртвая конфигурация, вводит в заблуждение) и переписать комментарий; блок `externalDatabase` с пустыми `host`/`database`/`username`/`existingSecret` и `sslMode: require` (см. Open Questions); `helm/amp/tests/values-production-placeholders.yaml` += `externalDatabase.{host,database,username,existingSecret}`. `--set postgresql.password=…` в release-gate и тестах оставить: при выключенном встроенном PG он безвреден, правка гейта не нужна.
- **D5** `helm/amp/templates/NOTES.txt` — для внешней БД `External PostgreSQL: <host>:<port>/<db> (sslmode=<mode>), password from Secret <name>/<key>`; для `configFile`-пути — предупреждение «пароль в ConfigMap, переходи на `externalDatabase`».
- **D6** `go-app/internal/database/postgres/config.go` `DSN()` — `url.URL{Scheme: "postgres", User: url.UserPassword(User, Password), Host: net.JoinHostPort(Host, strconv.Itoa(Port)), Path: "/" + Database, RawQuery: "sslmode=" + url.QueryEscape(SSLMode)}.String()`. Для обычных значений строка совпадает с прежней (`TestPostgresConfig_DSN` остаётся зелёным). `ConnectionString()` и `config.GetDatabaseURL()` в рантайме не вызываются — не трогаем, запись в TECH-DEBT (тот же дефект).
- **D7** тесты: новый `helm/amp/tests/render-external-database.sh` (стиль `render-defaults-validate.sh`: `render`/`render_fails`/`assert_count`); правки `render-postgresql.sh` T1 (production: нет StatefulSet `amp-postgres`, нет `postgres:16.15`), `render-defaults-validate.sh` T1 (production: флага нет, `DATABASE_SSL_MODE=require`) и `check_secret_refs` (ссылка на Secret, которого нет в рендере, допустима только если это `externalDatabase.existingSecret` из args), T3 `external-db` — рендер с `configFile`-путём или ожидание `fail` (выбрать при реализации, смысл теста — «флага нет без встроенного PG»); Go: `TestPostgresConfig_DSN_EscapesCredentials` — round-trip через `pgxpool.ParseConfig` для `/ # ? % пробел @ :` в пароле, `@ :` в пользователе, `/` и пробел в имени БД.
- **D8** docs: `helm/amp/README.md` (баннер `:15`, § PostgreSQL `:107` — пример `externalDatabase` + `kubectl create secret`, `configFile` как запасной путь с предупреждением), `docs/MIGRATION_QUICK_START.md:24`, комментарий в `values-production.yaml`, `CHANGELOG.md` `[Unreleased]` (Added + Breaking/migration note + Fixed для DSN).
- **D9** прогон на k3d (см. Rollout) → `POSTGRESQL_RESTORE_GUIDE.md` § «External PostgreSQL» (команды ровно из прогона, без «not drilled»), строка 9; raw-вывод — `evidence/k3d-external-drill.txt`.

## Security Design

- [x] Ownership validation — не применимо (нет пользовательских объектов).
- [x] Input validation: `required` на Secret/БД/пользователя при заданном `host`, enum `sslMode`, конфликт двух БД — на рендере; пароль валидирует AMP (`config.go:1447-1482`).
- [x] Sensitive data not logged: пароль только в `secretKeyRef`, в values его нет; `NOTES.txt` печатает имя Secret и ключ, не значение; pgx маскирует пароль в ошибке парсинга (`xxxxxx`, evidence) — после D6 парсинг не падает вовсе.
- [x] Rate limiting — не применимо.
- [x] Auth/RBAC: чарт Secret не читает и не создаёт — RBAC не меняется (`list secrets` только для publishing, ADR-012). TLS: дефолт `require`, флаг ослабления в ветке внешней БД не рендерится ни при каком `sslMode` (T-ext-4).

## Invariants

- [ ] Дефолтный рендер (`values.yaml`) не меняется ни в одном объекте (проверка: `helm template` до/после, diff пустой).
- [ ] Рендер `postgresql.enabled: false` + `configFile.content` с `database:` — тот же, что до задачи (Option B).
- [ ] lite-профиль не меняется; `externalDatabase.*` в lite ни на что не влияет.
- [ ] Ни в одной ветке внешней БД нет `DATABASE_ALLOW_INSECURE_SSL_IN_PRODUCTION`.
- [ ] `DSN()` для значений без спецсимволов возвращает прежнюю строку.

## Edge Cases

1. `host` задан, `postgresql.enabled: true` (забыли выключить) → `fail` с подсказкой.
2. `host` задан, `existingSecret` пуст → `fail`: чарт Secret не создаёт, назвать ключ `existingSecretPasswordKey`.
3. Secret или ключ в нём не существует в кластере → рендер проходит, под `CreateContainerConfigError` (чарт кластер не читает; `lookup` не используем — ломает `helm template`). Описать в README.
4. `sslMode: disable` + `environment: production` → под падает на валидации AMP с её сообщением; ожидаемо, комментарий в values.
5. `sslMode: verify-full` к managed PG без CA в образе → ошибка TLS при подключении; README: non-goal, используйте `require` или собственный образ с CA.
6. `host` и `database:` в `configFile.content` одновременно → env побеждает (premise 1); README: «`externalDatabase` перекрывает `database:` в конфиге».
7. `postgresql.enabled: false`, `host` пуст, `configFile.content` с `database:` не на верхнем уровне (flow-style, кавычки `"database":`) → guard ложно падает; обход — `externalDatabase.host`. Записать в README одной фразой.
8. БД подаётся post-renderer'ом/kustomize-патчем env → guard D3 падает; обход — `externalDatabase.host` (тот же env). Принимаем.
9. `helm upgrade --reuse-values` со старого релиза без `externalDatabase` → в values ключа нет; шаблон обращается через `| default dict` (как `networkPolicy`), ветка внешней БД выключена.
10. Порт строкой (`--set-string externalDatabase.port=5433`) → `quote` даёт `"5433"`, AMP парсит.
11. Пароль с `/ # ? % пробел` → после D6 подключается (Go-тест + прогон D9).

## Impact Analysis

- **Affected modules:** `helm/amp` (values, values-production, deployment, NOTES, tests, README, docs), `go-app/internal/database/postgres` (`DSN()` + тест), `docs/MIGRATION_QUICK_START.md`, `CHANGELOG.md`.
- **Breaking changes:**
  - `values-production.yaml`: встроенный PG выключен, файл не рендерится без `externalDatabase.*`; `helm upgrade` существующего релиза из этого файла удалит StatefulSet встроенного PG — migration note.
  - `postgresql.enabled: false` без какой-либо БД теперь падает на рендере (раньше — `CrashLoopBackOff` на `localhost:5432`).
- **New dependencies:** none (`net/url`, `net`, `strconv` — stdlib).
- **Risks:**
  - забытый перенос данных при апгрейде прод-релиза → пустая внешняя БД; mitigation — migration note с командами из прогона, `NOTES.txt` показывает, куда подключён AMP;
  - guard D3 ложно падает на нестандартном YAML (edge 7/8) → обход через `externalDatabase.host`;
  - premise 1 (env > config) — code-read; проверяется в D9.

## Rollout / Rollback

- **Rollout (прогон D9 на k3d, он же проверка migration note):**
  1. k3d-кластер, локальный образ AMP (`linux/<arch>` + `k3d image import`).
  2. Установка с встроенным PG (`values.yaml` + 1 реплика), отправить алерты и создать silence, записать счётчики.
  3. Внешний `postgres:16` в docker-сети k3d (`-c ssl=on` + snakeoil), роль `amp` без superuser с паролем, содержащим `/` и `%`, пустая БД `amp` с владельцем `amp`; Secret с паролем в namespace.
  4. `pg_dump -Fc` встроенного → AMP в 0 → `helm upgrade` на `postgresql.enabled=false` + `externalDatabase` (`sslMode: require`) с 0 репликами → `pg_restore --no-owner` без `--clean` во внешнюю → AMP обратно.
  5. Проверки runbook § 4: `/-/ready`, алерты и silences на месте, счётчики совпадают, миграции не переприменены, `pg_stat_ssl.ssl = t` для подключений AMP, в логе нет `NOT encrypted`.
  6. Отдельно: `helm template` production values с placeholders рендерится; без них — падает на `externalDatabase`.
- **Rollback:** `helm rollback` (см. § Data Model). Для Go-фикса — revert коммита; формат DSN для обычных значений не меняется.
- **Feature flag:** не применимо — переключатель сам `externalDatabase.host`.

## Observability

- **Logs:** без изменений в Go. Существующий `Database connection is NOT encrypted in production` для внешней БД не появляется (флаг не ставится).
- **Metrics:** не применимо.
- **Alerts:** `postgresql-prometheus-rules.yaml` рендерится только для встроенного PG — для внешней БД мониторинг на стороне оператора; README одной фразой.

## Deep Review

- **Mandatory triggers present:** `S`; 4 сигнала (`C S X R`).
- **Discretionary triggers present:** `C+X`; диф, вероятно, >200 строк вместе с тестами и доками.
- **Decision:** required.

## Open Questions

- [ ] `sslMode` в `values-production.yaml`: `require` (шифрование без проверки сертификата, работает с любым managed PG) или `verify-full` (без CA провайдера в образе не подключится). Предложение — `require` + комментарий; `verify-full` станет разумным дефолтом, когда появятся клиентские TLS-материалы (non-goal). Решается до `plan-task`, по умолчанию `require`.
- [ ] premise «env > config-файл» — code-read, подтверждается шагом 5 прогона D9 (в production values есть `configFile`).
