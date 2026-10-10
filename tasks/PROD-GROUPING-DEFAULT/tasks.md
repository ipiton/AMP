---
id: PROD-GROUPING-DEFAULT
slug: prod-grouping-default
stream: Parity / Grouping
type: bug
status: active
created_at: 2026-10-10
updated_at: 2026-10-10
based_on:
  - requirements.md
  - research.md
  - Spec.md
---

# Implementation Plan: группировка включена по умолчанию

**Based on:** requirements.md / research.md / Spec.md v1.0
**Date:** 2026-10-10

Пути — от корня репозитория. Go-команды выполняются из `go-app/`. Оценка: ~0.5–1d, резать не нужно.

## Touched Files

- `go-app/internal/config/config.go` — дефолт `grouping.enabled`, doc-комментарий `GroupingConfig.Enabled`.
- `go-app/internal/application/service_registry.go` — WARN в `initializeGrouping`, эффективный флаг для `AlertProcessor`, комментарии (`:199`, `:1612`).
- `go-app/internal/core/services/alert_processor.go` — комментарий `:29` («the default»).
- `go-app/internal/config/grouping_adapter_test.go` — тесты дефолта и явного выключения.
- `go-app/internal/application/service_registry_grouping_test.go` — тесты WARN, дефолта с `route:`, эффективного флага.
- `helm/amp/values.yaml` — `grouping.enabled: true`, комментарий.
- `helm/amp/values-production.yaml` — комментарий.
- `helm/amp/tests/render-grouping-default.sh` (новый) — render-тест.
- `deploy/smoke/config.yaml`, `deploy/e2e-ha/config.yaml` — комментарии.
- `docs/ALERTMANAGER_COMPATIBILITY.md`, `docs/MIGRATION_QUICK_START.md`, `docs/CONFIGURATION_GUIDE.md`, `README.md`, `helm/amp/README.md` — тексты.
- `CHANGELOG.md`, `helm/amp/CHANGELOG.md` — `[Unreleased]`.

## Phase 1: Implementation (код)

> **Wave 1** — независимые шаги

- [ ] **1.1** `config.go`: `viper.SetDefault("grouping.enabled", true)`; doc-комментарий `GroupingConfig.Enabled` — включено по умолчанию, действует только при наличии `route:`, читается на старте (не hot-reload). <!-- verify: cd go-app && go build ./... && grep -n 'SetDefault("grouping.enabled", true)' internal/config/config.go -->
- [ ] **1.2** `service_registry.go`, `initializeGrouping`, ветка `!Enabled`: при `r.config.HasRouteTree()` — `Warn` из Spec § Target Design п.3, иначе прежний INFO. Комментарии `:199` и `:1612` — убрать «default(s) to false». <!-- verify: cd go-app && go vet ./internal/application/... -->
- [ ] **1.3** `service_registry.go`, сборка `AlertProcessorConfig` (`:2320`): `GroupingEnabled: r.config.Grouping.Enabled && r.config.HasRouteTree()` с комментарием, зачем (без `route:` нет ложного fallback-WARN). <!-- verify: cd go-app && go vet ./internal/application/... -->
- [ ] **1.4** `alert_processor.go:29`: комментарий `Publisher` — убрать «(the default)». Логика не трогается. <!-- verify: git diff go-app/internal/core/services/alert_processor.go — только комментарий -->

> **Wave 2** — смоук до deep-review (новые тесты не пишем)

- [ ] **1.5** Минимальная правка существующего теста, чтобы пакет собирался зелёным: `TestLoadConfig_GroupingDefaults` ждёт `true` (комментарий теста обновить). <!-- depends: 1.1 | verify: cd go-app && go test ./internal/config/... -->
- [ ] **1.6** Существующие тесты затронутых деревьев зелёные. <!-- depends: 1.1–1.5 | verify: cd go-app && go vet ./internal/config/... ./internal/application/... ./internal/core/... && go test ./internal/config/... ./internal/application/... ./internal/core/... ./cmd/... -->

**Phase verification:** `make -C go-app quality-gates-fast` + `git status` (fmt ничего не переписал) + `git diff --check`.

## Phase 2: Implementation (чарт, deploy, доки)

> **Wave 1** — независимые шаги

- [ ] **2.1** `helm/amp/values.yaml`: `grouping.enabled: true`; комментарий — включено по умолчанию, действует при наличии `route:`, `lite` — in-memory, `standard` — Redis, выключение возвращает прямую публикацию. `values-production.yaml:251` — формулировка «turns on» → «kept explicit». <!-- verify: helm template t helm/amp | grep 'GROUPING_ENABLED: "true"' -->
- [ ] **2.2** `helm/amp/tests/render-grouping-default.sh` по образцу `render-image-tag.sh`: дефолт → `"true"`; `--set grouping.enabled=false` → `"false"`; `values-production.yaml` + `tests/values-production-placeholders.yaml` → `"true"`. Исполняемый бит. <!-- depends: 2.1 | verify: helm/amp/tests/render-grouping-default.sh → 0 FAIL -->
- [ ] **2.3** `deploy/smoke/config.yaml:44`, `deploy/e2e-ha/config.yaml:80`: «must be explicit true (defaults to false)» → «default since PROD-GROUPING-DEFAULT; kept explicit». Значения не менять. <!-- verify: git diff deploy/ — только комментарии -->
- [ ] **2.4** `docs/ALERTMANAGER_COMPATIBILITY.md`: Known Gap #13 — группировка включена по умолчанию при наличии `route:`, как отключить, флаг читается на старте, `lite` группирует in-memory; шаг миграции (`:973`) — убрать ручное включение. Проверить таблицу статусов выше по файлу на «off by default». <!-- verify: grep -n -i 'off by default\|defaults to .false.\|ignores the key' docs/ALERTMANAGER_COMPATIBILITY.md → пусто -->
- [ ] **2.5** `README.md:17` (три ловушки → две), `docs/MIGRATION_QUICK_START.md:24`, `docs/CONFIGURATION_GUIDE.md` (ключ `grouping.enabled`: дефолт, не hot-reload), `helm/amp/README.md` (`:23` пример `values-small.yaml`, `:53`, `:68` «No grouping» в lite). <!-- verify: grep -rn -i 'grouping stays off\|grouping is off\|grouping.enabled. is ignored\|default false: every alert' README.md docs/*.md helm/amp/README.md → пусто -->
- [ ] **2.6** `CHANGELOG.md` `[Unreleased]`: `### Changed` + запись в breaking changes / migration notes (задержка на `group_wait` 30s по умолчанию, сгруппированный payload, откат `grouping.enabled: false` + рестарт). `helm/amp/CHANGELOG.md` — дефолт values. <!-- verify: grep -n 'PROD-GROUPING-DEFAULT' CHANGELOG.md helm/amp/CHANGELOG.md -->

**Phase verification:** `helm lint helm/amp` + все `helm/amp/tests/*.sh` зелёные + `git diff --check`.

## Phase 3: Deep Review

- [ ] **3.1** `/deep-review` (Spec § Deep Review: required). Фокус: установки без `route:` (нет WARN, нет degraded); Edge Case 5 (дерево не собралось); полнота поиска потребителей флага; честность migration note; доки про `lite`. <!-- depends: Phase 1, Phase 2 | verify: tasks/PROD-GROUPING-DEFAULT/review-verdict.json -->

**Phase verification:** `review-verdict.json` — `"gate": "pass"`.

## Phase 4: Tests (write-tests, после verdict pass)

- [ ] **4.1** `grouping_adapter_test.go`: явный `grouping.enabled: false` из YAML → `false`; `GROUPING_ENABLED=false` из env без файла → `false`. <!-- depends: 3.1 | verify: cd go-app && go test -run 'TestLoadConfig_Grouping' ./internal/config/... -->
- [ ] **4.2** `service_registry_grouping_test.go`: переименовать `TestInitializeGrouping_DisabledByDefault` → `…DisabledExplicitly`, добавить проверку WARN через захват логов; случай «выключено, `route:` нет» → WARN нет. <!-- depends: 3.1 | verify: cd go-app && go test -run 'TestInitializeGrouping' ./internal/application/... -->
- [ ] **4.3** Тест «конфиг из `LoadConfig` с `route:` и без `grouping:` → `initializeGrouping` поднимает groupManager (lite)». <!-- depends: 3.1 | verify: cd go-app && go test -run 'GroupingDefault' ./internal/application/... -->
- [ ] **4.4** Тест эффективного флага: включено + нет `route:` → `AlertProcessor` собран с `GroupingEnabled == false`, алерт публикуется напрямую, fallback-WARN не пишется; включено + `route:` → `true`. Если сборка процессора в registry не тестируется изолированно — вынести выражение флага в неэкспортируемый метод registry и тестировать его (отклонение записать в Spec). <!-- depends: 3.1 | verify: cd go-app && go test -run 'GroupingActive|EffectiveGrouping' ./internal/application/... -->
- [ ] **4.5** Мутационная проверка: вернуть `GroupingEnabled: r.config.Grouping.Enabled` → 4.4 падает; вернуть дефолт `false` → 1.5 и 4.3 падают; убрать WARN → 4.2 падает. <!-- depends: 4.1–4.4 | verify: вручную, результат в tasks.md -->

**Phase verification:** `cd go-app && go test -race ./internal/config/... ./internal/application/... ./internal/core/...`

## Phase 5: Testing и Finalize

- [ ] **5.1** `/testing`: гейты `WORKFLOW.md` § Гейты AMP — `quality-gates-fast`, `scripts/release-gate.sh`, `git diff --check`. <!-- depends: Phase 4 | verify: release-gate RESULT: PASS -->
- [ ] **5.2** Smoke на дефолте (Spec Open Question): временная копия `deploy/smoke/config.yaml` без секции `grouping:` → `./deploy/smoke/run.sh` ALL PASS; вывод в `evidence/`. Если `run.sh` не принимает путь к конфигу — временно убрать секцию в рабочем дереве и откатить. <!-- depends: 5.1 | verify: evidence/smoke-lite-default.md -->
- [ ] **5.3** Стартовый лог бинаря: (а) `route:` + дефолт → «Initializing grouping subsystem...»; (б) `route:` + `grouping.enabled: false` → WARN один раз; (в) без `route:` → INFO, WARN нет. <!-- depends: 5.1 | verify: вывод в evidence/startup-logs.md -->
- [ ] **5.4** `/finalize`: `DONE.md`, BACKLOG P0 → «Закрыто», `NEXT.md` (WIP очистить, строку Queue обновить), архив workspace. <!-- depends: 5.1–5.3 -->

**Phase verification:** release-gate PASS, evidence приложены.

## Definition of Done

- [ ] All steps are complete or explicitly marked blocked/skipped
- [ ] Success criteria from `requirements.md` are covered
- [ ] Contracts from `Spec.md` are implemented or deviations are recorded
- [ ] Deep review verdict is `pass`
- [ ] Tests for changed behavior are added or updated
- [ ] Phase checks pass
- [ ] Docs/planning are updated if behavior, contracts, or process changed
