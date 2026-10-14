---
id: HELM-DEFAULTS-VALIDATE
slug: helm-defaults-validate
stream: Helm
type: bug
status: active
created_at: 2026-10-14
updated_at: 2026-10-14
based_on:
  - requirements.md
  - research.md
  - Spec.md
---

# Implementation Plan: дефолтные values чарта проходят валидацию AMP

**Based on:** requirements.md / research.md / Spec.md v1.0  
**Date:** 2026-10-14

Tier Full: `implement` (фазы 1–3) → `deep-review` → `write-tests` (фаза 4) → `testing` (фаза 5) → `finalize`.

## Touched Files

- `go-app/internal/config/config.go` — поле `AllowInsecureSSLInProduction`, `SetDefault`, условие в `validateDatabaseCredentials`
- `go-app/cmd/server/main.go` — `logDatabaseTLSState` и вызов рядом с `logWebAuthState`
- `helm/amp/templates/deployment.yaml` — env `DATABASE_ALLOW_INSECURE_SSL_IN_PRODUCTION` в ветке встроенной БД
- `helm/amp/templates/secret.yaml` — `fail` при `llm.enabled` без ключа и без ESO
- `helm/amp/values.yaml`, `helm/amp/values-production.yaml`, `helm/amp/values-dev.yaml` — `llm.enabled: false`, комментарии
- `helm/amp/README.md`, `config.yaml.example`, `CHANGELOG.md` — доки
- `go-app/internal/config/config_database_validation_test.go` (новый) — G1–G3
- `go-app/cmd/server/database_tls_state_test.go` (новый) — G4
- `helm/amp/tests/render-defaults-validate.sh` (новый) — T1–T6
- `tasks/HELM-DEFAULTS-VALIDATE/evidence/binary-check.md` (новый, на `testing`) — B1

## Phase 1: Go — флаг и предупреждение

- [x] **1.1** `DatabaseConfig.AllowInsecureSSLInProduction bool` с тегом `mapstructure:"allow_insecure_ssl_in_production"`; `viper.SetDefault("database.allow_insecure_ssl_in_production", false)` сразу после `database.ssl_mode` в `setDefaults`. <!-- verify: cd go-app && go build ./internal/config/... -->
- [x] **1.2** В `validateDatabaseCredentials` условие запрета: `production && SSLMode == "disable" && !AllowInsecureSSLInProduction`; текст ошибки не менять. <!-- depends: 1.1 | verify: cd go-app && go vet ./internal/config/... && go test ./internal/config/... -count=1 -->
- [x] **1.3** `logDatabaseTLSState(logger, cfg)` в `cmd/server/main.go` рядом с `logWebAuthState`, вызов рядом с `:94`. `Warn` с полем `host` только при `profile == standard && environment == production && ssl_mode == disable && flag`; пароль/DSN не логировать. <!-- depends: 1.1 | verify: cd go-app && go vet ./cmd/server/... && go build ./cmd/server -->

**Phase verification:** `cd go-app && go vet ./internal/config/... ./cmd/server/... && go test ./internal/config/... ./cmd/server/... -count=1`

## Phase 2: Helm — флаг и LLM

> **Wave 1** - independent steps (разные файлы)

- [x] **2.1** `deployment.yaml`: после `DATABASE_SSL_MODE` в ветке `postgresql.enabled && profile == standard` — `DATABASE_ALLOW_INSECURE_SSL_IN_PRODUCTION: "true"` под `if ne (default "off" .Values.postgresql.config.ssl) "on"`, с коротким комментарием (почему и ссылка на флаг). <!-- verify: helm template amp helm/amp | grep -c DATABASE_ALLOW_INSECURE_SSL_IN_PRODUCTION  # 1; и helm template amp helm/amp --set postgresql.config.ssl=on | grep -c ...  # 0 -->
- [x] **2.2** `secret.yaml`: в блоке LLM — `fail` при `llm.enabled && !llm.apiKey && !externalSecrets.enabled` с текстом из Spec Target Design п. 4. <!-- verify: helm template amp helm/amp --set llm.enabled=true 2>&1 | grep -q 'llm.apiKey' -->
- [x] **2.3** `values.yaml` и `values-production.yaml`: `llm.enabled: false` + комментарий «включить: `llm.enabled=true` и `llm.apiKey` или ESO»; в `values.yaml:411` комментарий у `postgresql.config.ssl` — про флаг вместо `see HELM-DEFAULTS-VALIDATE`. <!-- verify: helm template amp helm/amp | grep 'LLM_ENABLED: "false"' -->

> **Wave 2** - depends on Wave 1

- [x] **2.4** Дефолтный рендер: все `secretKeyRef` сходятся с ключами Secret (скрипт сверки из research). <!-- depends: 2.1, 2.2, 2.3 | verify: python-сверка из research § Findings → 0 MISSING -->
- [x] **2.5** `helm lint` для дефолтных values, `values-dev.yaml`, `values-production.yaml` с placeholders. <!-- depends: 2.1, 2.2, 2.3 | verify: helm lint helm/amp && helm lint helm/amp -f helm/amp/values-dev.yaml && helm lint helm/amp -f helm/amp/values-production.yaml -f helm/amp/tests/values-production-placeholders.yaml --set postgresql.password=x123456789012 --set cache.auth.password=x -->

**Phase verification:** `for t in helm/amp/tests/*.sh; do bash "$t" || echo "FAIL $t"; done` (существующие render-тесты зелёные)

## Phase 3: Docs

- [x] **3.1** `helm/amp/README.md` § Quick Start: дефолтный install стартует; встроенный PostgreSQL в production работает без TLS по флагу (почему, что видно в логе); как включить LLM; убрать обход, который больше не нужен; внешняя БД — пока через `configFile.content` (`HELM-EXTERNAL-DATABASE`). <!-- verify: grep -n 'allow_insecure_ssl_in_production\|llm.enabled' helm/amp/README.md -->
- [x] **3.2** `config.yaml.example` § `database`: ключ `allow_insecure_ssl_in_production: false` с комментарием. <!-- verify: grep -n allow_insecure_ssl_in_production config.yaml.example -->
- [x] **3.3** `CHANGELOG.md [Unreleased]`: Fixed (дефолтный install), Added (ключ конфига), migration notes (дефолт `llm.enabled` `true`→`false`; `fail` на рендере без ключа). <!-- verify: grep -n 'HELM-DEFAULTS-VALIDATE' CHANGELOG.md -->

**Phase verification:** `git diff --check`

→ **deep-review** (обязателен). `review-verdict.json` с `"gate": "pass"` до фазы 4.

### Fix round 1 (deep-review @ 867e169, `fix_required`)

- [x] **3.4** F1: `docs/MIGRATION_QUICK_START.md:24`, `docs/ALERTMANAGER_COMPATIBILITY.md` (вступление, Known Gap #15) — под новые дефолты; F8: `llm.model` в таблице README. <!-- verify: ! grep -rn 'defaults do not start\|Do not install with chart defaults' docs helm/amp/README.md -->
- [x] **3.5** F6: migration note в CHANGELOG про `-f values-production.yaml`/`values-dev.yaml`; Spec v1.1 (три values-файла, Test Plan G2–G4, T1 по F4/F5). <!-- verify: grep -n 'values-dev.yaml' CHANGELOG.md tasks/HELM-DEFAULTS-VALIDATE/Spec.md -->
- [ ] **3.6** Повторный deep-review по дельте fix round 1. <!-- verify: review-verdict.json gate=pass -->

## Phase 4: Tests (write-tests, после verdict `pass`)

- [ ] **4.1** G1–G3 в `go-app/internal/config/config_database_validation_test.go`: production+disable без флага → прежняя ошибка; с флагом → `nil`; флаг + слабый пароль → ошибка; env `DATABASE_ALLOW_INSECURE_SSL_IN_PRODUCTION=true` через `LoadConfig` → поле `true`, без env → `false`; невалидное значение env — зафиксировать фактическое поведение (Spec Edge Case 7). <!-- verify: cd go-app && go test ./internal/config/... -run 'InsecureSSL' -count=1 -v -->
- [ ] **4.2** G4 в `go-app/cmd/server/database_tls_state_test.go`: `Warn` с `host` только в целевой комбинации; нет `Warn` при `ssl_mode=require`, при `development`, при `lite`; пароль не в выводе. <!-- verify: cd go-app && go test ./cmd/server/... -run 'DatabaseTLSState' -count=1 -v -->
- [ ] **4.3** `helm/amp/tests/render-defaults-validate.sh` (по образцу `render-grouping-default.sh`): T1 (все `secretKeyRef` сходятся, `LLM_ENABLED: "false"`, флаг ровно один раз), T2 (`ssl=on` → нет флага), T3 (`postgresql.enabled=false` → нет), T4 (`profile=lite` → нет), T5 (`llm.enabled=true` без ключа → fail с `llm.apiKey`), T6 (`apiKey` задан → `llm-api-key` в Secret; ESO без ключа → рендерится). <!-- verify: bash helm/amp/tests/render-defaults-validate.sh -->

**Phase verification:** `cd go-app && go test ./internal/config/... ./cmd/server/... -count=1 && bash ../helm/amp/tests/render-defaults-validate.sh`

## Phase 5: Testing (гейты AMP)

- [ ] **5.1** Затронутые пакеты: `cd go-app && go vet ./internal/config/... ./cmd/server/... && go test ./internal/config/... ./cmd/server/...`. <!-- verify: exit 0 -->
- [ ] **5.2** `make -C go-app quality-gates-fast`, затем `git status` (go fmt мог переписать файлы). <!-- verify: exit 0, git status без неожиданных изменений -->
- [ ] **5.3** B1: `go build ./cmd/server`, запуск под `env -i` на env дефолтного рендера (как `tasks/archive/PROD-CONFIG-FILE-FALLBACK/evidence/binary-check.md` сценарий d, тестовый пароль 19 символов): нет `config validation failed`, есть `Warn` с `host`, пароль в логе 0 раз; контроль — без флага прежняя ошибка. Результат — `evidence/binary-check.md`. <!-- depends: 1.3, 2.1 | verify: evidence/binary-check.md заполнен -->
- [ ] **5.4** `scripts/release-gate.sh` (полный). <!-- verify: все шаги PASS или задокументированный предсуществующий FAIL -->
- [ ] **5.5** `git diff --check`; в диффе нет `_, _ :=`. <!-- verify: git diff main --check && ! git diff main | grep -E '^\+.*_, _ :=' -->

## Deviations from Spec

- **values-dev.yaml** (2.3): Spec называл только `values.yaml` и `values-production.yaml`, но в `values-dev.yaml` тоже `llm.enabled: true` без ключа. С новым `fail` его рендер падал бы, и шаг `helm-dev` в release-gate покраснел бы; до задачи такой dev-install всё равно падал с `CreateContainerConfigError`. Выключен по тому же решению владельца.
- **Поле `AllowInsecureSSLInProduction`** стоит в конце `DatabaseConfig` отдельным блоком: в середине структуры gofmt перевыравнивал все поля.

## Definition of Done

- [ ] All steps are complete or explicitly marked blocked/skipped
- [ ] Success criteria from `requirements.md` are covered
- [ ] Contracts from `Spec.md` are implemented or deviations are recorded
- [ ] Deep review verdict is `pass`, or deep review was not required
- [ ] Tests for changed behavior are added or updated
- [ ] Phase checks pass
- [ ] Docs/planning are updated if behavior, contracts, or process changed (`BUGS.md` `HELM-DEFAULTS-FAIL-VALIDATION` закрыт на `finalize`)
