---
id: HELM-EXTERNAL-DATABASE
slug: helm-external-database
stream: Helm
type: feature
priority: critical
status: active
created_at: 2026-10-16
updated_at: 2026-10-16
---

# Requirements: внешний PostgreSQL через values чарта

## Problem Framing

- **Symptom:** при `postgresql.enabled: false` чарт не передаёт поду ни одной переменной `DATABASE_*`. Внешняя БД настраивается только секцией `database:` в `configFile.content`, и пароль лежит открытым текстом в ConfigMap (пункт 3 бага `HELM-DEFAULTS-FAIL-VALIDATION`). `values-production.yaml` по-прежнему ставит встроенный PG, хотя по ADR-016 он только dev/pilot.
- **Root Cause:** в `helm/amp/templates/deployment.yaml:94-116` блок `DATABASE_*` есть только под `postgresql.enabled && profile == standard`; в ветке `else` — SQLite (`STORAGE_FILESYSTEM_PATH`). Values для внешней БД не заводились.
- **Why Now:** P0 по решению владельца 2026-10-15: без этого поддерживаемый прод-путь ADR-016 (внешний PG ≥ 16) через чарт не собирается.
- **How We Measure:** render-тест: `profile: standard`, `postgresql.enabled: false`, `externalDatabase.*` ⇒ `DATABASE_HOST/PORT/DATABASE/USERNAME/SSL_MODE` из values, `DATABASE_PASSWORD` через `secretKeyRef` на `existingSecret`, без `DATABASE_ALLOW_INSECURE_SSL_IN_PRODUCTION`, без `STORAGE_FILESYSTEM_PATH`; без `existingSecret` — render падает с понятной ошибкой; `values-production.yaml` рендерится с внешней БД; restore-runbook § «External PostgreSQL» пройден на PG 16, пометка «not drilled» снята.

## Risk Profile

- **Signals:** `C S R`
  - `C` — новый публичный контракт values `externalDatabase.*`; `values-production.yaml` меняет `postgresql.enabled` на `false` (breaking для тех, кто ставит прод из этого файла).
  - `S` — путь пароля БД: только `secretKeyRef`, никакого plain-text в ConfigMap/env; дефолт `sslMode: require`, флаг ослабления TLS в этой ветке не рендерится.
  - `R` — меняется поведение установки из `values-production.yaml`; закрывается записью в `CHANGELOG.md` `[Unreleased]` с migration note (в AMP нет `deploy`).
- **Tier:** Full
- **Notes:** `M` рассмотрен и не ставится: схема/данные не трогаются. Но переключение `values-production.yaml` на внешнюю БД при `helm upgrade` существующего релиза удалит StatefulSet встроенного PG (PVC останется) и переведёт AMP на пустую внешнюю БД — migration note обязан описать перенос через runbook (`pg_dump`/`pg_restore`) до апгрейда. `X` нет: Go-код не меняется, только чарт и доки.

## User Stories

1. Как оператор кластера, я хочу указать хост/порт/БД/пользователя внешнего PostgreSQL в values и пароль ссылкой на существующий Secret, чтобы пароль не попадал в ConfigMap и values.
2. Как оператор, ставящий прод из `values-production.yaml`, я хочу, чтобы файл по умолчанию вёл на внешнюю БД (ADR-016) и не рендерился без её реквизитов, чтобы встроенный dev/pilot PG не попал в прод случайно.
3. Как оператор, я хочу проверенный runbook восстановления дампа во внешний PG, чтобы не открывать его впервые во время инцидента.

## Success Criteria

- [ ] `values.yaml`: `externalDatabase.{host,port,database,username,sslMode,existingSecret,existingSecretPasswordKey}` с комментариями; `port: 5432`, `sslMode: require`, `existingSecretPasswordKey: password`.
- [ ] `deployment.yaml`: при `profile: standard` и `postgresql.enabled: false` рендерятся `DATABASE_*` из `externalDatabase`, пароль — `secretKeyRef`; `required` на `externalDatabase.host` и `externalDatabase.existingSecret` с текстом, называющим ключ.
- [ ] `DATABASE_ALLOW_INSECURE_SSL_IN_PRODUCTION` в ветке внешней БД не рендерится ни при каком `sslMode`.
- [ ] Поведение встроенного PG и lite-профиля не меняется (существующие render-тесты зелёные).
- [ ] `values-production.yaml`: `postgresql.enabled: false` + блок `externalDatabase` (без реальных реквизитов — render требует их явно); `helm/amp/tests/values-production-placeholders.yaml` дополнен, тесты на production values зелёные.
- [ ] Новый render-тест в `helm/amp/tests/` (в стиле существующих `render-*.sh`), подключён туда же, где запускаются остальные (release-gate).
- [ ] Runbook `helm/amp/docs/POSTGRESQL_RESTORE_GUIDE.md` § «External PostgreSQL» прогнан на PG 16 (restore в пустую БД без `--clean`, без superuser), команды в тексте — ровно те, что прошли; пометка «not drilled» снята.
- [ ] README чарта (`helm/amp/README.md`) и корневой `README.md` (если там есть) — раздел про внешнюю БД вместо «planned»/`configFile.content`; `NOTES.txt` — если упоминает.
- [ ] `CHANGELOG.md` `[Unreleased]`: Added + Breaking/migration note для `values-production.yaml`.
- [ ] Гейты: затронутые render-тесты, `helm lint`, `scripts/release-gate.sh`, `git diff --check`.

## Non-Goals

- Режим «Secret создаёт чарт» (`externalDatabase.password`) — не делаем: добавить позже проще, чем убрать (решение в BACKLOG).
- ESO-путь для пароля внешней БД (`externalsecret.yaml` сейчас ведёт только `postgres_*` встроенного PG) — вне scope, если research не покажет, что без него прод-путь ломается; баг `ESO-SECRET-OWNERSHIP-CONFLICT` — отдельно.
- `DATABASE_URL`, пул (`max_connections` и т. п.) через values — не добавляем; пул по-прежнему настраивается конфигом.
- TLS-материалы клиента (`sslrootcert`, client cert) — вне scope; только `sslMode`.
- `PG-CONFIG-FIX` и любые изменения встроенного PG.
- Автоматическая миграция данных со встроенного PG на внешний.

## Constraints

- **Scope:** `helm/amp/` (values, values-production, deployment, тесты, docs, NOTES), `README.md`, `CHANGELOG.md`. Go-код не меняется: `DATABASE_*` уже читаются (`go-app/internal/config/config.go:329-348`, дефолт `ssl_mode=require` на `:896`).
- **Security:** пароль только через `secretKeyRef`; никакого значения пароля в values/ConfigMap; в production AMP сам отклоняет `sslmode=disable` без флага (`config.go:1480`) — чарт флаг для внешней БД не ставит, поэтому `sslMode: disable` + `environment: production` упадёт на старте пода: это ожидаемо и должно быть написано в комментарии values.
- **Compatibility:** `values.yaml` — аддитивно (по умолчанию `postgresql.enabled: true`, ничего не меняется). `values-production.yaml` — breaking, migration note обязателен. Стиль guard'ов — fail-loud на рендере (ADR-015, ADR-016).

## Discovery Notes

- Similar tasks: `tasks/archive/HELM-DEFAULTS-VALIDATE/` (флаг insecure SSL, render-тест `render-defaults-validate.sh`), `tasks/archive/PROD-POSTGRES-HA-DECISION/` (ADR-016, runbook, `render-postgresql.sh`). Дубликатов в `tasks/`, `tasks/archive/`, `DONE.md`, `archive/DONE-*.md` нет.
- Relevant patterns: блок `DATABASE_*` встроенного PG в `deployment.yaml:94-116`; `required`-guard'ы ADR-015; bitnami `externalDatabase.*` как образец имён; `helm/amp/tests/values-production-placeholders.yaml`.
- Open unknowns (в research):
  - условие веток в `deployment.yaml`: сейчас `else` = SQLite — как выбирать «встроенный / внешний / lite» и что делать при `postgresql.enabled: false` + `profile: standard` без `externalDatabase.host` (fail-loud vs SQLite);
  - какие ещё шаблоны завязаны на `postgresql.enabled` (`externalsecret.yaml`, NetworkPolicy egress на 5432, init-контейнеры ожидания БД / миграции, `NOTES.txt`) и должны учитывать внешнюю БД;
  - где запускаются `render-*.sh` (release-gate/CI) — чтобы новый тест реально гонялся;
  - как прогнать restore на внешнем PG 16 локально (docker `postgres:16` вне кластера + не-superuser владелец БД) и что именно сейчас написано в § «External PostgreSQL».
- Research level: 2 — новый контракт values и путь секрета, но паттерны известны; внешних исследований почти не нужно.
