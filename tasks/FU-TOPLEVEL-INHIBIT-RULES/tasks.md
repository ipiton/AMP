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
- `pkg/metrics/metrics.go` — удалены мёртвые `BusinessMetrics.InhibitionCache*` (дубли имён `GetCacheMetrics`, panic на старте; шаг 1.9).
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
- [x] **1.8** Ручной смоук на бинаре: конфиг с `route:` + верхнеуровневым правилом (`source_matchers`) — в логе старта `top_level_rules=1`; тот же конфиг с битым matcher'ом — exit 1. Вывод — в Notes. <!-- depends: 1.6 | verify: go run ./cmd/server с AMP_CONFIG_FILE=<tmp> profile=lite -->

> **Wave 5** — добавлено после смоука 1.8 (решение владельца 2026-10-12: чинить в этой задаче)

- [x] **1.9** Удалить мёртвые `BusinessMetrics.InhibitionCache{Hits,Misses,Evictions,Size,Operations,Duration}` в `pkg/metrics/metrics.go` (0 использований в репозитории, в `grafana/`/`helm/` не упоминаются) — они регистрировали те же имена `alert_history_inhibition_cache_*`, что и `internal/infrastructure/inhibition.GetCacheMetrics`, но без label'ов ⇒ panic default registry при инициализации ингибиции. Источник имён — `GetCacheMetrics` (реально пишется кэшем). <!-- verify: grep -rn "InhibitionCache[A-Z]" --include='*.go' go-app/pkg → пусто; смоук 1.8 без panic -->
- [x] **1.10** Повторный смоук 1.8. <!-- depends: 1.9 | verify: см. Notes -->

> **Wave 6** — review round 1 (`review-findings.md`), решение владельца 2026-10-12: F1/F2 fix-here

- [x] **1.11** F1: `loadInhibitRulesYAML` (`config.go`) — правила обоих размещений перечитываются из файла через yaml.v3 после `viper.Unmarshal` (viper приводил имена label'ов в `*_match` к нижнему регистру); `Config.InhibitRules` → `mapstructure:"-"` (заодно env `INHIBIT_RULES` больше не ломает загрузку). <!-- verify: смоук r2 в Notes -->
- [x] **1.12** F2: `pkg/configvalidator/validators/inhibition.go` — E150/E151 → W150/W151 (upstream принимает правило без source/target; runtime трактует пустую сторону как «всё»); `doc.go`. <!-- verify: смоук r2 в Notes -->
- [x] **1.14** R2 N4: текст W151 уточнён для правила с обеими пустыми сторонами (глушит ничего — двусторонний матч исключается, как в upstream). <!-- verify: go build ./pkg/... -->
- [x] **1.13** F11 `RouteConfig.Clone()` копирует `InhibitRules`; F12 комментарий `InhibitionRules()` смягчён; комментарий-предусловие в `alertmanager_validation.go`. <!-- verify: go build ./... -->

**Phase verification:** `cd go-app && go build ./... && go vet ./internal/config/... ./internal/application/... ./internal/infrastructure/routing/... ./pkg/metrics/... ./pkg/configvalidator/... && go test ./internal/config/... ./internal/application/... ./pkg/metrics/... ./pkg/configvalidator/...` — красные только `TestInhibitionValidator/missing_{source,target}_matchers` (ждут E150/E151; старый контракт, обновляются в 3.4).

## Phase 2: Deep Review

- [ ] **2.1** `/deep-review` (Spec § Deep Review: recommended, will run). Фокус: потребитель, оставшийся на старом пути; частичная загрузка при ошибке; reload, отброшенный как no-change; неизменность поведения для конфигов только с `inhibition:`. <!-- depends: 1.1–1.8 | verify: tasks/FU-TOPLEVEL-INHIBIT-RULES/review-verdict.json → "gate": "pass" -->

**Phase verification:** `review-verdict.json` `gate: pass`.

## Phase 3: Tests (write-tests, после verdict pass)

- [x] **3.1** T1 `LoadConfig` с `route:` и верхнеуровневыми правилами в обеих формах (legacy `source_match`/`target_match` и `source_matchers`/`target_matchers`, `equal`) → `cfg.InhibitionRules()` даёт 2 правила, matcher реально подавляет target (паттерн `TestLoadConfig_MatchersFormOnly_WithRouteSection_LoadsAndInhibits`). Плюс вариант без `route:`. Мутация: убрать верхнеуровневый источник из `InhibitionRules()` → FAIL. <!-- depends: 2.1 | verify: go test -run TestTopLevelInhibitRules_ C --> _Done: `internal/config/toplevel_inhibit_rules_test.go` `TestTopLevelInhibitRules_BothFormsInhibit` (с `route:` и без); мутация M1 (выкинуть top-level из `InhibitionRules()`) → FAIL._
- [x] **3.2** T2 оба источника + `config_file` → объединение в порядке top-level, inline, file; конфиг только с `inhibition:` → тот же результат, что `Inhibition.ToInhibitionRules()` (инвариант). <!-- depends: 2.1 | verify: go test -run TestTopLevelInhibitRules_ C --> _Done: `TestTopLevelInhibitRules_MergeOrder` — порядок `from-top-level, from-inline, from-file`; инвариант «только обёртка ≡ `ToInhibitionRules`»._
- [x] **3.3** T3 битый matcher/regex в верхнеуровневом правиле → `LoadConfig` ошибка, сообщение содержит `inhibit_rules[0]` (без имени) и имя (с именем); `inhibit_rules: []` → без ошибок. <!-- depends: 2.1 | verify: go test -run TestTopLevelInhibitRules_ C --> _Done: `TestTopLevelInhibitRules_BrokenRuleFailsLoad` — префиксы `top-level … "inhibit_rules[0]"`, `top-level … "bad-regex"`, inline-текст не изменился; `inhibit_rules: []` ок._
- [x] **3.4** T4 `configvalidator` видит верхнеуровневые правила: с `route:` верхнеуровневое правило с E-кодом (E152 битый label в `equal`, E153/E154) → ошибка загрузки; правило без source / без target → загружается, W150/W151 (обновить `pkg/configvalidator/validators/inhibition_test.go:82,91` — сейчас ждут E150/E151), и такое правило реально подавляет (target-only `alertname=InfoInhibitor`). _Done: `TestTopLevelInhibitRules_ConfigValidator` (E152 валит загрузку; target-only InfoInhibitor грузится и подавляет); `validators/inhibition_test.go` — кейсы E150/E151 → W150/W151 без ошибок, новый `TestInhibitionValidator_EmptySidesMessages` закрепляет текст W150/W151 (R2 N4)._
- [x] **3.8** T8 (F1) регистр: `source_match: {clusterName: Prod}` верхнего уровня и вложенный — после `LoadConfig` ключ `clusterName` сохранён, правило подавляет алерт с `clusterName`; мутация — убрать вызов `loadInhibitRulesYAML` → FAIL. <!-- depends: 2.1 | verify: go test -run TestTopLevelInhibitRules_ C --> _Done: `TestTopLevelInhibitRules_LabelNameCasePreserved` (оба размещения); мутация M2 (`loadInhibitRulesYAML` → no-op) → FAIL._
- [x] **3.5** T5 reload: файл v1 без правил → v2 только с верхнеуровневым правилом → `ReloadFromFile` не no-change, `GetCurrentConfig().InhibitionRules()` содержит правило (паттерн `reload_coordinator_test.go:336`). Плюс правка только `source_matchers` в верхнеуровневом правиле → тоже не no-change. <!-- depends: 2.1 | verify: go test -run TestTopLevelInhibitRules_Reload C --> _Done: `TestTopLevelInhibitRules_Reload` — реальный `NewConfigComparator`, «правило добавлено» и «изменены только `target_matchers`» → Version 2; мутация M3 (`json:"-"` на `InhibitRules`) → FAIL «treated as no-change»._
- [x] **3.7** T7 регрессия panic'а: в одном процессе `metrics.NewBusinessMetrics()` (или как его зовёт `ServiceRegistry`) + `inhibition.GetCacheMetrics()` на default registry — без panic; проверить, что тест падает на `main`-версии `metrics.go`. Предпочтительно — тест старта `initializeInhibition` с реальными зависимостями, если его можно собрать без Redis. <!-- depends: 2.1 | verify: go test -run Inhibition ./pkg/metrics/ ./internal/application/ --> _Done: `pkg/metrics/inhibition_cache_collision_test.go` (внешний пакет `metrics_test` — `inhibition` импортирует `pkg/metrics`); свежий registry как Default, guard от маскировки `sync.Once` через `Gather`. На `metrics.go` из `60b1584^` → FAIL с исходным «previously registered descriptor … alert_history_inhibition_cache_hits_total». Тест старта `ServiceRegistry` не выбран: `sync.Once` в общем тест-бинаре `internal/application` делает его зависимым от порядка тестов._
- [x] **3.6** T6 `AlertmanagerConfigYAML` сохраняет `source_matchers`/`target_matchers` верхнеуровневого правила (`status_config_test.go`). <!-- depends: 2.1 | verify: go test -run TestAlertmanagerConfigYAML ./internal/application/handlers/ --> _Done: `handlers/status_config_test.go` `TestAlertmanagerConfigYAML_KeepsInhibitMatchers` + `routing_fingerprint_test.go` `TestRoutingFingerprint_SeesInhibitMatchersChange`._

**Phase verification:** `cd go-app && go test -race ./internal/config/... ./internal/application/...` — _2026-10-12: зелёное, плюс `./pkg/metrics/... ./pkg/configvalidator/... ./internal/infrastructure/{routing,inhibition}/...`; gofmt/vet чисто._

## Phase 4: Docs

- [x] **4.1** `docs/ALERTMANAGER_COMPATIBILITY.md`: строки 14 и 73 → 🟢 (обе формы, объединение), строка 23 — убрать «a top-level `inhibit_rules:` is ignored (#9)», Known Gap #9 — пометить закрытым (`FU-TOPLEVEL-INHIBIT-RULES`, 2026-10-12) без перенумерации, миграция ~994 — верхнеуровневая форма работает как есть; reload без правил на старте требует рестарта (Spec Edge Case 4). <!-- verify: grep -n "top-level \`inhibit_rules:\`" docs/ALERTMANAGER_COMPATIBILITY.md — нет утверждений «ignored» --> _Done: Executive Summary, строка Inhibition → 🟢 (слияние, формы, W150/W151, ограничения `rule=""` и рестарт при пустом старте), Known Gap #9 помечен Closed с историей, Replacement Guidance — копировать как есть + рестарт, если правил на старте не было (F4)._
- [x] **4.2** Остальные места «top-level `inhibit_rules` ignored» (review F8): `helm/amp/README.md:38,249,262`, `README.md:17`, `docs/CONFIGURATION_GUIDE.md:419,715`, `docs/RELEASE_NOTES_v0.1.0-draft.md:322`, `config.yaml.example:438` (верхнеуровневая форма тоже принимается и сливается); `go-app/internal/infrastructure/inhibition/README.md:511` — устаревшее имя метрики. <!-- verify: grep -rniE "inhibit_rules.{0,40}(ignored|silently)|(ignored|silently).{0,40}inhibit_rules" README.md docs helm config.yaml.example go-app/internal --include='*.md' --include='*.yaml' --include='*.example' → только исторические записи (CHANGELOG/планирование) --> _Done: `README.md`, `helm/amp/README.md` (пример values — верхнеуровневый `inhibit_rules: []`), `docs/CONFIGURATION_GUIDE.md` ×2, `config.yaml.example`, `RELEASE_NOTES_v0.1.0-draft.md` (follow-up снят); `inhibition/README.md` — PromQL на реальные имена (`alert_history_inhibition_{check,match}_total`, `cache_hits_total by (tier)`); verify-grep пуст._
- [x] **4.4** Доки валидатора (review R2 N5): `go-app/pkg/configvalidator/ERROR_CODES.md:317-326` — E150/E151 → W150/W151 (Warning, текст про правило без стороны и про «обе пустые ⇒ ничего не глушит»), счётчик `:515`; `go-app/pkg/configvalidator/README.md:256-258`. <!-- verify: grep -rn "E15[01]" go-app/pkg/configvalidator --include='*.md' → только историческое упоминание «formerly» --> _Done: W150/W151 в ERROR_CODES.md (с пояснением «обе пустые ⇒ ничего»), счётчик E150-E159 → 3, ссылка E027/E028; README валидатора._
- [x] **4.3** `CHANGELOG.md` `[Unreleased]`: Fixed — panic на старте при любом inhibit-правиле с PARITY-A2 (`a822faf`, 2026-04-16; дубли `alert_history_inhibition_cache_*`); Fixed — имена label'ов в `*_match` больше не приводятся к нижнему регистру (касается и `inhibition.inhibit_rules`); Changed — E150/E151 → W150/W151; правила, добавленные reload'ом при выключенном на старте движке, требуют рестарта (review F4); migration note: правила ингибиции (оба размещения) читаются строго, как в upstream — `equal: cluster` (скаляр вместо списка) и `inhibit_rules:` как одна map вместо списка теперь ошибка загрузки (R2 N1), ключи секции в другом регистре (`Inhibition:`, `Source_Match:`) больше не распознаются (R2 N2); Fixed + migration note — ранее молча игнорируемые верхнеуровневые правила начнут подавлять алерты; неверный тип под `inhibit_rules:` теперь ошибка загрузки; `config.original` отдаёт matchers-форму. <!-- verify: review diff --> _Done: `[Unreleased]` → Fixed `FU-TOPLEVEL-INHIBIT-RULES` (Before/Now, panic, регистр, E→W, Known limits F4/F5, migration notes 1–2 с N1/N2). Уточнено: без inhibit-правил всегда нулевые серии `alert_history_inhibition_cache_*` без label'ов с `/metrics` пропали._

**Phase verification:** `git diff --check`

## Phase 5: Testing (гейты AMP)

- [x] **5.1** `make -C go-app quality-gates-fast` + `git status` после `go fmt`. _2026-10-12: passed, `go fmt` ничего не переписал._ <!-- verify: exit 0, нет непредвиденных изменений -->
- [x] **5.2** `scripts/release-gate.sh` (код Go изменён). _2026-10-12 @ `294e412`: `RESULT: PASS` — build, golangci-lint, test, futureparity, race, helm-deps/dev/production/rbac/tests, amtool-compat._ <!-- verify: RESULT: PASS -->
- [x] **5.3** `git diff --check main...`, в диффе нет `_, _ :=`. _2026-10-12: найдены хвостовые пробелы в шапках Spec.md:16 и tasks.md:17 (markdown hard break из шаблона) — убраны; `_, _ :=` нет._ <!-- verify: пусто -->

## Notes

- **1.7:** `go test ./internal/config/... ./internal/infrastructure/routing/... ./internal/application/... ./pkg/configvalidator/...` — 1103 passed.
- **1.8 (blocked):** смоук на бинаре (lite, `route:` + верхнеуровневое правило с `source_matchers`):
  - битый matcher → exit 1, `inhibition config validation failed: top-level inhibition rule "inhibit_rules[0]": …` — ок;
  - валидный конфиг → в логе `rules=1 top_level_rules=1`, затем **panic** `a previously registered descriptor with the same fully-qualified name as Desc{fqName: "alert_history_inhibition_cache_hits_total" …}`.
  - **Предсуществующий баг:** тот же panic на сборке `main` (`f90d853`) с правилом под `inhibition:` — любой конфиг хотя бы с одним inhibit-правилом роняет AMP при старте. Корень: одни и те же имена регистрируются дважды в default registry — `pkg/metrics/metrics.go:1221-1260` (`BusinessMetrics.InhibitionCache*`, `alert_history` + `inhibition_cache`, 0 использований) и `internal/infrastructure/inhibition/cache.go:45` (`GetCacheMetrics`, с label'ами, реально используется). Существует с `d096644` (2025-12-02), в planning не учтён.
  - Следствие для этой задачи: без фикса panic'а она превращает «правила молча игнорируются» в «verbatim upstream-конфиг не стартует». Отпускать отдельно нельзя. **Решение владельца (2026-10-12): чинить здесь** → шаги 1.9–1.10, T7, CHANGELOG; запись в `BUGS.md` как закрытого — на `finalize`.
- **Review round 1 смоук (r2, после 1.11–1.13):** верхнеуровневые правила `source_match: {clusterName: Prod, alertname: ClusterDown}` / `target_match: {severity: warning}` / `equal: [clusterName]` и `- target_matchers: ['alertname = InfoInhibitor']` без source: старт `rules=2 top_level_rules=2`, `W150` на `inhibit_rules[1]`, panic нет; `HighLatency{clusterName=Prod}` и `InfoInhibitor` подавлены (`inhibition_check_total{result="inhibited"} 2`).
- **1.10:** после 1.9 тот же конфиг: `rules=1 top_level_rules=1`, panic нет; `ClusterDown{cluster=a}` → `HighLatency{cluster=a}` подавлен, `HighLatency{cluster=b}` нет (`/api/v2/inhibitions` — 1 запись; `alert_history_inhibition_check_total{result="inhibited"} 1`, `{result="allowed"} 2`); `config.original` содержит `source_matchers`/`target_matchers`/`equal`; на `/metrics` 33 серии `alert_history_inhibition_cache*`. `inhibitedBy` в `/api/v2/alerts` пуст — известный gap (compat-дока, раздел karma), вне scope.

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
