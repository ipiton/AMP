---
id: HELM-DEFAULTS-VALIDATE
slug: helm-defaults-validate
stream: Helm
type: bug
status: draft
created_at: 2026-10-14
updated_at: 2026-10-14
based_on:
  - requirements.md
  - research.md
---

# Specification: дефолтные values чарта проходят валидацию AMP

**Version:** 1.0  
**Status:** Draft

## Summary

Дефолтная установка чарта перестаёт падать: LLM по умолчанию выключен, а при включённом LLM без ключа чарт падает на рендере с понятной ошибкой. Встроенный PostgreSQL без TLS проходит production-валидацию благодаря новому флагу AMP `database.allow_insecure_ssl_in_production`. Чарт выставляет его только для встроенной БД, а AMP при включённом флаге пишет предупреждение в лог.

## Requirements Coverage

| Requirement / Criterion | Covered by |
|---|---|
| Дефолтный рендер без `secretKeyRef` на отсутствующий ключ; `llm.enabled: false` | Component Architecture: `values.yaml`, `values-production.yaml`; render-тест T1 |
| Бинарь на env дефолтного рендера проходит валидацию | Target Design (флаг); бинарная проверка B1 на `testing` |
| Без флага `production` + `disable` отклоняется | `config.go` условие; Go-тест G1 |
| Флаг только при встроенном PostgreSQL | `deployment.yaml`; render-тесты T2, T3, T4 |
| Явное предупреждение в логе при включённом флаге | `main.go` `logDatabaseTLSState`; Go-тест G4, бинарная проверка B1 |
| `llm.enabled` без ключа и без ESO — ошибка на рендере | `secret.yaml` `fail`; render-тесты T5, T6 |
| README, `values.yaml`, CHANGELOG; баг закрыт | Docs (Component Architecture); `finalize` |
| Гейты AMP зелёные | `testing` |

## Current State

- **Code:** `go-app/internal/config/config.go` — `DatabaseConfig` (`:329-344`), `setDefaults` (`:891` `database.ssl_mode=require`), `validateDatabaseCredentials` (`:1439-1478`, запрет `disable` в production на `:1474`). `cmd/server/main.go` — `logWebAuthState` (`:94`, `:234-249`).
- **Chart:** `helm/amp/templates/deployment.yaml:94-113` — env БД только при `postgresql.enabled && profile == standard`, `DATABASE_SSL_MODE` = `require` при `postgresql.config.ssl == "on"`, иначе `disable`; `:131-139` — `LLM_API_KEY` из `amp-secrets/llm-api-key` при `llm.enabled`. `templates/secret.yaml:44-51` — ключ `llm-api-key` только при непустом `llm.apiKey`. `values.yaml:528`, `values-production.yaml:178` — `llm.enabled: true`.
- **Data:** not applicable.
- **Tests:** тестов на `validateDatabaseCredentials` нет (`grep` по `internal/config/*_test.go`). Render-тесты `helm/amp/tests/*.sh` автоматически идут в release-gate (`scripts/release-gate.sh:228-236`, шаг `helm-tests`).
- **Docs:** `helm/amp/README.md` § Quick Start (обходы бага), `config.yaml.example` § `database`, `CHANGELOG.md [Unreleased]`, `docs/06-planning/BUGS.md` `HELM-DEFAULTS-FAIL-VALIDATION`.

## Design Premises

| Premise | Confirmed by | Class | If wrong |
|---|---|---|---|
| Запрет `sslmode=disable` в production есть только в `config.go:1474`; других проверок `SSLMode` в рантайме нет | `grep -rn 'SSLMode\|ssl_mode\|SSL_MODE\|sslmode'` по `go-app` без `.cache` и тестов: `config.go`, `reloadable_database.go:38` (копирование), `pool.go:169` (лог), `internal/database/postgres/config.go` (проверяет только допустимость значения) | call-path-traced | AMP падает на другой проверке; бинарная проверка B1 это поймает |
| Hot-reload валидирует тем же `Validate()` | `reload_coordinator.go:365` `loadAndParse` → `LoadConfig` → `Validate()` (`config.go:696`, `:846`); `update_validator.go:259` `ssl_mode` не проверяет | call-path-traced | reload с флагом отклонялся бы — неприменимо, путь один |
| Новый ключ доходит из env, только если viper о нём знает | `config.go:972-976` (комментарий и прецедент `BindEnv`), `AutomaticEnv` + replacer `:833-834` | code-read | флаг из env игнорируется; Go-тест G3 (env → поле) |
| В дефолтном рендере единственная битая ссылка на Secret — `amp-secrets/llm-api-key` | `helm template amp helm/amp` 2026-10-14 + сверка `secretKeyRef` с ключами Secret (research § Findings) | measured | останется ещё одна причина `CreateContainerConfigError`; render-тест T1 проверяет все ссылки |
| Дефолтный `postgresql.password` проходит проверки слабого пароля и длины | `values.yaml:308` `secure_password_123` (19 символов, не в списке `config.go:1460`) | code-read | B1 упадёт на пароле — тогда вне scope, запись в BUGS |
| Шаблоны чарта больше нигде не зависят от `llm.enabled: true` по умолчанию; render-тесты LLM не проверяют | `grep '\.Values\.llm\.'` по `templates/` (configmap, deployment, secret, externalsecret, llm-secret — все за `if llm.enabled`); `grep -il llm helm/amp/tests scripts/release-gate.sh` — пусто | call-path-traced | падает существующий render-тест — увидим на `helm-tests` |
| `values-production.yaml` не включает глобальный `externalSecrets.enabled` | `grep -A3 '^externalSecrets:' values-production.yaml` — пусто, наследуется `false` из `values.yaml:423` | code-read | guard LLM не влияет на prod-рендер при `llm.enabled: false` в любом случае |

`assumed`-предпосылок нет.

## Target Design

1. **Go-флаг.** `DatabaseConfig.AllowInsecureSSLInProduction bool` (`mapstructure:"allow_insecure_ssl_in_production"`), `SetDefault(false)` рядом с `database.ssl_mode`. В `config.go:1474` проверка становится `production && ssl_mode == "disable" && !AllowInsecureSSLInProduction`. Флаг снимает только этот запрет: обязательность username/password, слабые пароли и длина проверяются как раньше.
2. **Предупреждение.** `logDatabaseTLSState(logger, cfg)` в `main.go` рядом с `logWebAuthState`. При `profile == standard && environment == production && ssl_mode == "disable" && flag` пишет `Warn` «database connection is NOT encrypted (database.allow_insecure_ssl_in_production=true): only acceptable for a database inside the cluster» с полем `host`. Если флаг включён, но не применяется (`ssl_mode != disable` или environment не production), ничего не пишет.
3. **Чарт, флаг.** В `deployment.yaml` внутри ветки встроенной БД, после `DATABASE_SSL_MODE`, под тем же условием, что даёт `disable` (`ne (default "off" .Values.postgresql.config.ssl) "on"`), рендерится `DATABASE_ALLOW_INSECURE_SSL_IN_PRODUCTION: "true"`. Ключа в values нет.
4. **Чарт, LLM.** `llm.enabled: false` в `values.yaml` и `values-production.yaml`, с комментарием, как включить. В `secret.yaml` в блоке LLM: при `llm.enabled && !llm.apiKey && !externalSecrets.enabled` срабатывает `fail` с текстом «llm.enabled=true requires llm.apiKey (or externalSecrets.enabled with llm_api_key in the store)». Прецедент — `required` для `cache.auth.password` (`secret.yaml:39`).
5. **Тесты и доки.** Go-тесты валидатора и env-маппинга, новый `helm/amp/tests/render-defaults-validate.sh`, README/`config.yaml.example`/CHANGELOG.

## API Contracts

Not applicable: HTTP API не меняется.

### Config contract (не HTTP)

- **Новый ключ:** `database.allow_insecure_ssl_in_production` (bool, default `false`), env `DATABASE_ALLOW_INSECURE_SSL_IN_PRODUCTION`.
- **Семантика:** при `true` AMP не отклоняет `database.ssl_mode: disable` в `app.environment: production`; на остальные значения `ssl_mode` и на остальные окружения не влияет.
- **Chart values:** `llm.enabled` по умолчанию `true` → `false` (`values.yaml`, `values-production.yaml`). Новых ключей values нет.

## Data Model / Migrations

Not applicable.

## Component Architecture

- `go-app/internal/config/config.go` — поле, `SetDefault`, условие в `validateDatabaseCredentials`.
- `go-app/internal/config/config_test.go` (или новый `config_database_validation_test.go`) — G1–G3.
- `go-app/cmd/server/main.go` — `logDatabaseTLSState` и её вызов рядом с `:94`; тест G4 рядом с тестами `main` (если их нет — в новом `cmd/server/database_tls_state_test.go`).
- `helm/amp/templates/deployment.yaml` — env флага в ветке встроенной БД.
- `helm/amp/templates/secret.yaml` — `fail` в блоке LLM.
- `helm/amp/values.yaml`, `helm/amp/values-production.yaml` — `llm.enabled: false`, комментарий; комментарий у `postgresql.config.ssl` (`values.yaml:411`) сослаться на флаг вместо `see HELM-DEFAULTS-VALIDATE`.
- `helm/amp/tests/render-defaults-validate.sh` — T1–T6.
- `helm/amp/README.md` — Quick Start: дефолтный install работает; как включить LLM; что встроенный PG в production работает без TLS и почему.
- `config.yaml.example` — ключ в `database:` с комментарием.
- `CHANGELOG.md [Unreleased]` — Fixed + migration note про `llm.enabled`.

## Security Design

- [x] Ownership validation — not applicable (нет пользовательских данных).
- [x] Input validation: bool из viper; невалидное значение env → ошибка unmarshal, как у остальных bool-ключей.
- [x] Sensitive data not logged: `Warn` пишет только `host`, не пароль и не DSN. `fail` LLM не печатает значения.
- [x] Rate limiting — not applicable.
- [x] Auth/RBAC — not applicable.
- Ослабление ограничено: (а) только `ssl_mode=disable` в production; (б) в чарте — только встроенная ветка и только при `ssl != on`; (в) обход «флаг на внешней БД» через `configFile.content`/ручной env не предотвращается, но виден в логе по `host` (принятый риск, research § «Самое сильное возражение»).

## Invariants

- [ ] Без флага `production` + `disable` отклоняется с прежним текстом ошибки.
- [ ] Флаг не влияет на проверки username/password, слабых паролей и длины.
- [ ] При `postgresql.config.ssl: "on"` флаг не рендерится.
- [ ] При `postgresql.enabled: false` или `profile: lite` флаг не рендерится.
- [ ] `llm.enabled: true` с `llm.apiKey` рендерится как раньше (Secret с ключом, env `LLM_API_KEY`).
- [ ] `llm.enabled: true` + `externalSecrets.enabled: true` без `apiKey` рендерится (ключ из стора).
- [ ] `values-dev.yaml` и `values-production.yaml` (с placeholders) проходят release-gate.

## Edge Cases

1. `llm.enabled: true`, `apiKey` пустой, ESO выключен → `helm template` падает с текстом про `llm.apiKey`.
2. Флаг `true`, `ssl_mode: require` → валидация проходит, `Warn` нет (флаг ни на что не влияет).
3. Флаг `true`, `environment: development` → без изменений, `Warn` нет.
4. Флаг задан через `configFile.content` для внешней БД с `ssl_mode: disable` → AMP стартует, `Warn` с внешним `host` (наблюдаемый обход).
5. Новый чарт со старым образом → старый бинарь env не знает и падает на `ssl_mode`, как сейчас; регрессии нет. Старый чарт с новым образом → env флага нет, поведение прежнее.
6. `helm upgrade` установки, где задан только `llm.apiKey` и дефолт `enabled` → LLM выключается. Migration note в CHANGELOG.
7. `DATABASE_ALLOW_INSECURE_SSL_IN_PRODUCTION=yes` (не bool) → ошибка загрузки конфига при старте (стандартное поведение viper/mapstructure). Проверить на G3; если viper молча даёт `false`, это не ослабляет защиту.

## Impact Analysis

- **Affected modules:** `internal/config`, `cmd/server`, Helm-чарт (`deployment`, `secret`, оба values), docs.
- **Breaking changes:** дефолт `llm.enabled` в чарте `true` → `false`; `llm.enabled: true` без ключа и без ESO теперь падает на рендере, а не на старте pod'а. Оба — в CHANGELOG migration notes.
- **New dependencies:** none.
- **Risks:**
  - дефолтная production-установка работает с БД без шифрования — принято владельцем 2026-10-14, трафик внутри кластера; `Warn` на старте;
  - флаг на внешней БД через ручную настройку — наблюдаем по `host` в `Warn`.

## Rollout / Rollback

- **Rollout:** обычный `helm upgrade` на образ с флагом. Порядок образ/чарт не важен (Edge Case 5). AMP не деплоится агентом (нет `deploy`); сигнал `R` закрывается записью в CHANGELOG.
- **Rollback:** revert коммитов; данных и миграций нет.
- **Feature flag:** `database.allow_insecure_ssl_in_production` — это и есть флаг, default `false`.

## Observability

- **Logs:** `Warn` на старте (Target Design п. 2) с полем `host`. `ssl_mode` уже логируется при создании пула (`pool.go:169`).
- **Metrics:** not applicable (метрика отвергнута в research, Option C).
- **Alerts:** not applicable.

## Test Plan

- **G1:** `production` + `disable`, флаг `false` → ошибка `SSL mode 'disable' is not allowed in production`.
- **G2:** `production` + `disable`, флаг `true` → `nil`; флаг `true` + слабый пароль → по-прежнему ошибка.
- **G3:** env `DATABASE_ALLOW_INSECURE_SSL_IN_PRODUCTION=true` → `cfg.Database.AllowInsecureSSLInProduction == true` через `LoadConfig`; без env → `false`.
- **G4:** `logDatabaseTLSState` — `Warn` с `host` только в комбинации из Target Design п. 2; без `Warn` в Edge Cases 2, 3.
- **T1:** дефолтный рендер — каждый `secretKeyRef` указывает на ключ, который есть в рендеренном Secret того же имени; `LLM_ENABLED: "false"`; env флага есть ровно один раз.
- **T2:** `postgresql.config.ssl=on` → флага нет.
- **T3:** `postgresql.enabled=false` → флага нет. **T4:** `profile=lite` → флага нет.
- **T5:** `llm.enabled=true` без ключа → рендер падает, текст про `llm.apiKey`. **T6:** `llm.enabled=true` + `apiKey` → рендерится, `llm-api-key` в Secret; `llm.enabled=true` + `externalSecrets.enabled=true` без ключа → рендерится.
- **B1 (testing):** бинарь на env дефолтного рендера, как в `tasks/archive/PROD-CONFIG-FILE-FALLBACK/evidence/binary-check.md` (d): валидация проходит (до ошибки подключения к БД), в логе `Warn` с `host`, пароль в логе не встречается.

## Deep Review

- **Mandatory triggers present:** `S`; 3+ signals (`C S X R`).
- **Discretionary triggers present:** `C+X`.
- **Decision:** required.

## Open Questions

- [ ] none — решения владельца (флаг, `llm.enabled: false`, внешняя БД отдельно) зафиксированы 2026-10-14.
