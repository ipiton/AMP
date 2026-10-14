---
id: HELM-DEFAULTS-VALIDATE
slug: helm-defaults-validate
stream: Helm
type: bug
priority: critical
status: active
created_at: 2026-10-14
updated_at: 2026-10-14
---

# Requirements: дефолтные values чарта проходят валидацию AMP

## Problem Framing

- **Symptom:** `helm install` без своих values не поднимает AMP (баг `HELM-DEFAULTS-FAIL-VALIDATION` в `BUGS.md`):
  1. `llm.enabled: true` при пустом `llm.apiKey` — Secret `<fullname>-secrets` не получает ключ `llm-api-key`, а Deployment ссылается на него через `secretKeyRef` (`helm/amp/templates/deployment.yaml`, блок `LLM_API_KEY`) ⇒ pod в `CreateContainerConfigError`.
  2. При `llm.enabled: false` профиль `standard` со встроенным PostgreSQL падает на старте: `environment: production` (дефолт `values.yaml`) + `DATABASE_SSL_MODE=disable` (встроенный PostgreSQL TLS не умеет) ⇒ `database SSL mode 'disable' is not allowed in production` (`go-app/internal/config/config.go:1475`), exit 1.
  3. При `postgresql.enabled: false` чарт не передаёт `DATABASE_*`: единственный путь — `database:` в `configFile.content`, пароль лежит в ConfigMap открытым текстом.
- **Root Cause:** дефолты чарта не согласованы с production-валидацией конфига. Это маскировал фолбэк, который игнорировал env целиком; после `PROD-CONFIG-FILE-FALLBACK` env доходит до AMP, и валидация срабатывает.
- **Why Now:** P0 №1 в `BACKLOG.md` § Production Readiness. Дефолтная установка — crash loop, первое впечатление пользователя и любой pilot-install ломаются.
- **How We Measure:** бинарь AMP, запущенный на env из `helm template` с дефолтными values, проходит валидацию конфига и стартует. Pod дефолтного рендера не ссылается на отсутствующие ключи Secret.

## Решение владельца (2026-10-14)

- `llm.enabled` по умолчанию `false`.
- TLS для встроенного PostgreSQL не делаем. Вместо этого — явный opt-in флаг конфига AMP, который разрешает `sslmode=disable` при `environment: production`. Чарт выставляет его **только** для встроенного PostgreSQL (in-cluster). Для внешней БД production-проверка не меняется. Без флага поведение валидатора прежнее.

## Risk Profile

- **Signals:** `C S X R`
  - `C` — новый ключ конфига AMP (и env), изменение дефолта `llm.enabled` и новые values внешней БД.
  - `S` — точечное ослабление проверки TLS к БД в production; пароль внешней БД переезжает из ConfigMap в Secret.
  - `X` — Go-конфиг (`internal/config`) и Helm-чарт.
  - `R` — меняется поведение дефолтной установки; закрывается записью в `CHANGELOG.md` `[Unreleased]` (в AMP нет `deploy`).
- **Tier:** Full
- **Notes:** `S` эскалирует до Full без вариантов; `deep-review` обязателен до `write-tests`.

## User Stories

1. Как оператор, я хочу поставить AMP через `helm install` без своих values и получить работающий pod, чтобы попробовать продукт без чтения обходов в README.
2. Как оператор production-установки с внешним PostgreSQL, я хочу, чтобы проверка `sslmode` в production оставалась строгой, а ослабить её можно было только явно.

## Success Criteria

- [ ] `helm template` с дефолтными values: нет `secretKeyRef` на ключ, которого нет в рендеренном Secret; `llm.enabled` по умолчанию `false`.
- [ ] Бинарь AMP на env дефолтного рендера (профиль `standard`, встроенный PostgreSQL, `environment: production`) проходит валидацию конфига. Проверка — тем же способом, что в `tasks/archive/PROD-CONFIG-FILE-FALLBACK/evidence/binary-check.md`.
- [ ] Без opt-in флага `environment: production` + `sslmode=disable` по-прежнему отклоняется (регрессионный тест в `internal/config`).
- [ ] Чарт выставляет флаг только при встроенном PostgreSQL; при `postgresql.enabled: false` флаг не рендерится (render-тест).
- [ ] `llm.enabled: true` без `llm.apiKey` и без `externalSecrets.enabled` падает на рендере с понятной ошибкой, а не `CreateContainerConfigError`; render-тест.
- [ ] При включённом флаге AMP пишет в лог явное предупреждение о незашифрованном соединении с БД в production.
- [ ] `helm/amp/README.md` (Quick Start, обходы), `values.yaml` и `CHANGELOG.md` `[Unreleased]` описывают новые дефолты; баг `HELM-DEFAULTS-FAIL-VALIDATION` закрыт.
- [ ] Гейты AMP (`WORKFLOW.md` § Гейты AMP), включая `scripts/release-gate.sh`, зелёные.

## Non-Goals

- TLS для встроенного PostgreSQL.
- HA/репликация встроенного PostgreSQL, PDB, бэкапы — это `PROD-POSTGRES-HA-DECISION`.
- Дефолты под одну ноду (HPA, requests) — `HELM-SINGLE-NODE-DEFAULTS`.
- Изменение остальной production-валидации (слабые пароли, длина пароля).
- Values внешней БД с паролем из Secret (пункт 3 симптома) — отдельный срез `HELM-EXTERNAL-DATABASE`, решение владельца 2026-10-14; до него путь — `configFile.content`.
- Проводка `postgresql.existingSecret` в `postgresql-secret.yaml` (TECH-DEBT `HELM-CHART-GAPS`).

## Constraints

- **Scope:** `go-app/internal/config` и `cmd/server/main.go` (флаг, валидация, предупреждение), `helm/amp/` (values, `deployment.yaml`, `secret.yaml`, render-тесты, README), planning и `CHANGELOG.md`.
- **Security:** ослабление строго opt-in и видно в логе; для внешней БД production-проверка не меняется; пароли — только из Secret.
- **Compatibility:** установки с `llm.enabled: true` и заданным ключом не ломаются, если ключ передан явно. Смена дефолта `llm.enabled` — заметка в CHANGELOG (кто полагался на дефолт `true`, должен включить явно). Путь `configFile.content` для внешней БД не меняется.

## Discovery Notes

- Similar tasks: `tasks/archive/PROD-CONFIG-FILE-FALLBACK/` (research, `evidence/binary-check.md` сценарии d, e, `evidence/chart-default-env.txt`) — там найден баг и описан способ проверки бинарём.
- Relevant patterns:
  - production-валидация БД — `go-app/internal/config/config.go:1430-1478` (`username/password`, слабые пароли, длина ≥12, `ssl_mode`);
  - env БД в чарте — `helm/amp/templates/deployment.yaml:94-113`; `DATABASE_SSL_MODE` выводится из `postgresql.config.ssl`;
  - `LLM_API_KEY` — `deployment.yaml:131-139`, ключ в `templates/secret.yaml:46`, ESO-вариант в `templates/externalsecret.yaml:40`;
  - render-тесты чарта — `helm/amp/tests/` (гейт в `scripts/release-gate.sh`).
- Дефолтный `postgresql.password` (`secure_password_123`, 19 символов) проходит проверки слабого пароля и длины — ломается только `ssl_mode`. Слабый dev-дефолт пароля — отдельный пункт TECH-DEBT `HELM-CHART-GAPS`.
- Research: `research.md` (level 3, три линзы). Open unknowns ниже закрыты там.
- Open unknowns (для research):
  - имя и форма флага (ключ YAML `database.*`, env `DATABASE_*`), как он вписывается в загрузку конфига (viper/env-binding);
  - как именно чарт отличает «встроенный PostgreSQL» (условие `postgresql.enabled && profile == standard`) и не может ли пользователь включить флаг для внешней БД случайно;
  - форма values внешней БД (`externalDatabase.*` по образцу bitnami или иное) и как она сочетается с `configFile.content`;
  - ссылается ли ESO-путь (`externalsecret.yaml`) на `llm-api-key` безусловно;
  - нет ли других дефолтных включённых интеграций, требующих ключи Secret.
