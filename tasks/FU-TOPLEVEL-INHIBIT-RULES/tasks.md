---
id: FU-TOPLEVEL-INHIBIT-RULES
slug: fu-toplevel-inhibit-rules
stream: Parity / Inhibition
type: bug
status: active
created_at: 2026-10-12
updated_at: 2026-10-12
based_on:
  - requirements.md
  - research.md
  - Spec.md
---

# Implementation Plan: верхнеуровневый `inhibit_rules:` молча игнорируется

**Based on:** requirements.md / research.md / Spec.md v1.0  
**Date:** 2026-10-12

Пути ниже — относительно `go-app/`, если не указано иное. `C` = `./internal/config/`.

## Touched Files

- `internal/config/config.go` — поле `Config.InhibitRules`; `LoadConfig` (`:690`) и `LoadConfigFromEnv` (`:798`) → `cfg.InhibitionRules()`.
- `internal/config/inhibition_adapter.go` — метод `(*Config).InhibitionRules()`.
- `internal/config/alertmanager_validation.go` — объединённый список в `configvalidator` (`:98`), комментарий `:18-30`.
- `internal/application/service_registry.go` — `initializeInhibition` (`:1333`, лог по источникам), hot reload (`:2699`).
- `internal/infrastructure/routing/config.go` — `InhibitRule.SourceMatchers/TargetMatchers`, комментарии `:84-99`, `:357-361`.
- `internal/config/inhibition_toplevel_test.go` (новый) — T1–T5.
- `internal/application/handlers/status_config_test.go` — T6.
- Доки (корень репо): `docs/ALERTMANAGER_COMPATIBILITY.md`, `helm/amp/README.md`, `config.yaml.example`, `CHANGELOG.md`.

## Phase 1: Implementation

> **Wave 1** — независимые

- [x] **1.1** `Config.InhibitRules []InhibitionRuleConfig` с `mapstructure:"inhibit_rules" yaml:"inhibit_rules,omitempty"` рядом с `Inhibition`; doc-comment: верхнеуровневый ключ upstream, сливается с `inhibition.inhibit_rules` (ссылка на `FU-TOPLEVEL-INHIBIT-RULES`). Без `json:"-"` — поле должно попадать в diff reload'а (Spec P5). <!-- verify: cd go-app && go build ./... -->
- [x] **1.2** `routing.InhibitRule`: `SourceMatchers`/`TargetMatchers []string` (`yaml:"source_matchers,omitempty"`, `yaml:"target_matchers,omitempty"`); комментарии на `RouteConfig.InhibitRules` и типе — «зеркало для `config.original` и fingerprint; в runtime правила идут через `config.Config.InhibitRules`», убрать «placeholder / TODO(TN-137) / silently inhibits nothing». <!-- verify: cd go-app && go build ./... && go vet ./internal/infrastructure/routing/... -->

> **Wave 2**

- [x] **1.3** `(*Config).InhibitionRules() ([]inhibition.InhibitionRule, error)` в `inhibition_adapter.go`: верхнеуровневые правила → `InhibitionRule{...}` + `rule.Compile()`, ошибка `top-level inhibition rule %q` с именем или `inhibit_rules[i]`; затем `append` результата `c.Inhibition.ToInhibitionRules()`. Логику конвертации одного правила не дублировать: вынести общий кусок из `ToInhibitionRules` в неэкспортируемую функцию, если копия получается дословной. <!-- depends: 1.1 | verify: cd go-app && go build ./... && go vet C -->

> **Wave 3** — переключение потребителей

- [x] **1.4** `LoadConfig`/`LoadConfigFromEnv`: fail-fast через `cfg.InhibitionRules()`; текст ошибки `inhibition config validation failed` сохранить. <!-- depends: 1.3 | verify: grep -n "InhibitionRules()" internal/config/config.go → 2 -->
- [x] **1.5** `validateAlertmanagerSubset`: `toAlertmanagerInhibitRules(append(cfg.InhibitRules, cfg.Inhibition.Rules...))` — без мутации исходных слайсов (копия). Обновить комментарий про `inhibit_rules`. <!-- depends: 1.1 | verify: cd go-app && go vet C -->
- [x] **1.6** `service_registry.go`: `:1333` и `:2699` → `r.config.InhibitionRules()`; INFO `Initializing inhibition subsystem...` получает `top_level_rules` = `len(r.config.InhibitRules)` и `rules` = всего. <!-- depends: 1.3 | verify: grep -rn "Inhibition.ToInhibitionRules()" --include='*.go' internal cmd | grep -v _test → пусто -->

> **Wave 4** — смоук до deep-review (новые тесты не пишем)

- [x] **1.7** Существующие тесты затронутых пакетов зелёные. <!-- depends: 1.2–1.6 | verify: cd go-app && go test ./internal/config/... ./internal/infrastructure/routing/... ./internal/application/... ./pkg/configvalidator/... -->
- [ ] **1.8** Ручной смоук на бинаре: конфиг с `route:` + верхнеуровневым правилом (`source_matchers`) — в логе старта `top_level_rules=1`; тот же конфиг с битым matcher'ом — exit 1. Вывод — в Notes. <!-- depends: 1.6 | verify: go run ./cmd/server с AMP_CONFIG_FILE=<tmp> profile=lite -->

**Phase verification:** `cd go-app && go build ./... && go vet ./internal/config/... ./internal/application/... ./internal/infrastructure/routing/... && go test ./internal/config/... ./internal/application/...`

## Phase 2: Deep Review

- [ ] **2.1** `/deep-review` (Spec § Deep Review: recommended, will run). Фокус: потребитель, оставшийся на старом пути; частичная загрузка при ошибке; reload, отброшенный как no-change; неизменность поведения для конфигов только с `inhibition:`. <!-- depends: 1.1–1.8 | verify: tasks/FU-TOPLEVEL-INHIBIT-RULES/review-verdict.json → "gate": "pass" -->

**Phase verification:** `review-verdict.json` `gate: pass`.

## Phase 3: Tests (write-tests, после verdict pass)

- [ ] **3.1** T1 `LoadConfig` с `route:` и верхнеуровневыми правилами в обеих формах (legacy `source_match`/`target_match` и `source_matchers`/`target_matchers`, `equal`) → `cfg.InhibitionRules()` даёт 2 правила, matcher реально подавляет target (паттерн `TestLoadConfig_MatchersFormOnly_WithRouteSection_LoadsAndInhibits`). Плюс вариант без `route:`. Мутация: убрать верхнеуровневый источник из `InhibitionRules()` → FAIL. <!-- depends: 2.1 | verify: go test -run TestTopLevelInhibitRules_ C -->
- [ ] **3.2** T2 оба источника + `config_file` → объединение в порядке top-level, inline, file; конфиг только с `inhibition:` → тот же результат, что `Inhibition.ToInhibitionRules()` (инвариант). <!-- depends: 2.1 | verify: go test -run TestTopLevelInhibitRules_ C -->
- [ ] **3.3** T3 битый matcher/regex в верхнеуровневом правиле → `LoadConfig` ошибка, сообщение содержит `inhibit_rules[0]` (без имени) и имя (с именем); `inhibit_rules: []` → без ошибок. <!-- depends: 2.1 | verify: go test -run TestTopLevelInhibitRules_ C -->
- [ ] **3.4** T4 `configvalidator` видит верхнеуровневые правила: правило, которое валидатор отвергает (E-code из `pkg/configvalidator/validators/inhibition.go`), в верхнеуровневой форме с `route:` → ошибка загрузки. <!-- depends: 2.1 | verify: go test -run TestTopLevelInhibitRules_ C -->
- [ ] **3.5** T5 reload: файл v1 без правил → v2 только с верхнеуровневым правилом → `ReloadFromFile` не no-change, `GetCurrentConfig().InhibitionRules()` содержит правило (паттерн `reload_coordinator_test.go:336`). Плюс правка только `source_matchers` в верхнеуровневом правиле → тоже не no-change. <!-- depends: 2.1 | verify: go test -run TestTopLevelInhibitRules_Reload C -->
- [ ] **3.6** T6 `AlertmanagerConfigYAML` сохраняет `source_matchers`/`target_matchers` верхнеуровневого правила (`status_config_test.go`). <!-- depends: 2.1 | verify: go test -run TestAlertmanagerConfigYAML ./internal/application/handlers/ -->

**Phase verification:** `cd go-app && go test -race ./internal/config/... ./internal/application/...`

## Phase 4: Docs

- [ ] **4.1** `docs/ALERTMANAGER_COMPATIBILITY.md`: строки 14 и 73 → 🟢 (обе формы, объединение), строка 23 — убрать «a top-level `inhibit_rules:` is ignored (#9)», Known Gap #9 — пометить закрытым (`FU-TOPLEVEL-INHIBIT-RULES`, 2026-10-12) без перенумерации, миграция ~994 — верхнеуровневая форма работает как есть; reload без правил на старте требует рестарта (Spec Edge Case 4). <!-- verify: grep -n "top-level \`inhibit_rules:\`" docs/ALERTMANAGER_COMPATIBILITY.md — нет утверждений «ignored» -->
- [ ] **4.2** `helm/amp/README.md:38` — убрать «that key is ignored», пример можно оставить верхнеуровневым; `config.yaml.example:438` — упомянуть, что верхнеуровневый `inhibit_rules:` тоже принимается и сливается. <!-- verify: grep -rn "key is ignored" helm/ config.yaml.example → пусто -->
- [ ] **4.3** `CHANGELOG.md` `[Unreleased]`: Fixed + migration note — ранее молча игнорируемые верхнеуровневые правила начнут подавлять алерты; неверный тип под `inhibit_rules:` теперь ошибка загрузки; `config.original` отдаёт matchers-форму. <!-- verify: review diff -->

**Phase verification:** `git diff --check`

## Phase 5: Testing (гейты AMP)

- [ ] **5.1** `make -C go-app quality-gates-fast` + `git status` после `go fmt`. <!-- verify: exit 0, нет непредвиденных изменений -->
- [ ] **5.2** `scripts/release-gate.sh` (код Go изменён). <!-- verify: RESULT: PASS -->
- [ ] **5.3** `git diff --check main...`, в диффе нет `_, _ :=`. <!-- verify: пусто -->

## Notes

- **1.7:** `go test ./internal/config/... ./internal/infrastructure/routing/... ./internal/application/... ./pkg/configvalidator/...` — 1103 passed.
- **1.8 (blocked):** смоук на бинаре (lite, `route:` + верхнеуровневое правило с `source_matchers`):
  - битый matcher → exit 1, `inhibition config validation failed: top-level inhibition rule "inhibit_rules[0]": …` — ок;
  - валидный конфиг → в логе `rules=1 top_level_rules=1`, затем **panic** `a previously registered descriptor with the same fully-qualified name as Desc{fqName: "alert_history_inhibition_cache_hits_total" …}`.
  - **Предсуществующий баг:** тот же panic на сборке `main` (`f90d853`) с правилом под `inhibition:` — любой конфиг хотя бы с одним inhibit-правилом роняет AMP при старте. Корень: одни и те же имена регистрируются дважды в default registry — `pkg/metrics/metrics.go:1221-1260` (`BusinessMetrics.InhibitionCache*`, `alert_history` + `inhibition_cache`, 0 использований) и `internal/infrastructure/inhibition/cache.go:45` (`GetCacheMetrics`, с label'ами, реально используется). Существует с `d096644` (2025-12-02), в planning не учтён.
  - Следствие для этой задачи: без фикса panic'а она превращает «правила молча игнорируются» в «verbatim upstream-конфиг не стартует». Отпускать отдельно нельзя. Решение — за владельцем.

- Оценка: ~0.5–1d. Нарезка не нужна.
- Если дифф кода (без тестов и доков) превысит ~200 строк или потребуется трогать `internal/infrastructure/inhibition` — эскалация до Full (requirements § Risk Profile), согласовать с владельцем.

## Definition of Done

- [ ] All steps are complete or explicitly marked blocked/skipped
- [ ] Success criteria from `requirements.md` are covered
- [ ] Contracts from `Spec.md` are implemented or deviations are recorded
- [ ] Deep review verdict is `pass`, or deep review was not required
- [ ] Tests for changed behavior are added or updated
- [ ] Phase checks pass
- [ ] Docs/planning are updated if behavior, contracts, or process changed
