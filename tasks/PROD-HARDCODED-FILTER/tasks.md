---
id: PROD-HARDCODED-FILTER
slug: prod-hardcoded-filter
stream: Pipeline
type: bug
status: active
created_at: 2026-10-13
updated_at: 2026-10-13
based_on:
  - requirements.md
  - research.md
  - Spec.md
---

# Implementation Plan: захардкоженный фильтр не должен молча выбрасывать алерты

**Based on:** requirements.md / research.md / Spec.md v1.0
**Date:** 2026-10-13
**Tier:** Standard (`X R`). Конвейер: `implement → write-tests → testing → finalize → merge-to-main`; deep-review пропускается (Spec § Deep Review), пересмотреть, если дифф кода больше ~200 строк.

## Touched Files

- `go-app/internal/core/services/filter_engine.go` — удаление правил 1 и 4 и их хелперов; Rule 7 с ключом по fingerprint и значением `{status, seenAt}`; doc-комментарии.
- `go-app/internal/core/services/alert_processor.go` — лог дропа с `fingerprint`, удаление двух `// TODO: Record filter metrics`.
- `go-app/internal/core/services/filter_engine_test.go` — минимальная правка в implement (бывшие кейсы `test_alert`/`disabled_namespace`), новые кейсы — в фазе 3.
- `go-app/internal/core/services/filter_engine_dedup_test.go` — флап, повтор после смены статуса, метрика (фаза 3).
- `go-app/internal/core/services/alert_processor_filter_test.go` (новый) — `AlertProcessor` с настоящим `SimpleFilterEngine` (фаза 3).
- `helm/amp/values.yaml` — удаление секции `Filter Rules Configuration`.
- `CHANGELOG.md` — `### Fixed` + `### Breaking changes / migration notes`.
- `docs/ALERTMANAGER_COMPATIBILITY.md` — `:23`, Known Gap #14 (`:884-890`), чек-лист `:1016-1017`.
- `README.md` — runtime note `:17`.
- `tasks/PROD-HARDCODED-FILTER/evidence/` — вывод смоука (фаза 4).

## Phase 1: Implementation (код)

> **Wave 1** — независимые шаги, разные файлы

- [x] **1.1** `filter_engine.go`:
  - удалить из `shouldBlockInternal` Rule 1 (`isTestAlert`) и Rule 4 (`isDisabledNamespace`) вместе с функциями `isTestAlert`, `containsTest`, `isDisabledNamespace`;
  - нумерацию оставшихся правил в комментариях не менять;
  - doc-комментарий `ShouldBlock` перечисляет фактические правила (2, 3, 5, 6, 7) и говорит, что метрику пишет сам движок.

  <!-- verify: cd go-app && go build ./internal/core/services/ && ! grep -nE "isTestAlert|containsTest|isDisabledNamespace|test_alert|disabled_namespace" internal/core/services/filter_engine.go -->
- [x] **1.2** `filter_engine.go`, Rule 7:
  - `dedupSeen map[string]dedupEntry`, `type dedupEntry struct { status core.AlertStatus; seenAt time.Time }`, ключ — `alert.Fingerprint`;
  - блок только при `entry.status == alert.Status && now.Sub(entry.seenAt) < window`, иначе запись перезаписывается `{alert.Status, now}`;
  - при блоке запись не трогается;
  - lazy sweep по `entry.seenAt`;
  - doc-комментарий `isDuplicate` и поля структуры — про флап (Spec § Target Design п. 2).

  <!-- depends: 1.1 (тот же файл) | verify: cd go-app && go test -race ./internal/core/services/ -run 'Dedup' -count=1 -->
- [x] **1.3** `alert_processor.go` (`processTransparent`, `processEnriched`): в INFO `Alert blocked by filter` добавить `"fingerprint", alert.Fingerprint`; удалить обе строки `// TODO: Record filter metrics`. Логику не трогать.

  <!-- verify: cd go-app && ! grep -n "TODO: Record filter metrics" internal/core/services/alert_processor.go && git diff --stat internal/core/services/alert_processor.go -->

> **Wave 2** — зависит от Wave 1

- [x] **1.4** Минимальная правка существующих тестов, чтобы пакет был зелёным. Кейсы `filter_engine_test.go`, ожидавшие `test_alert`/`disabled_namespace`, теперь ждут «не блокируется» (`false, ""`); имена и комментарии кейсов — под новое ожидание. Тесты и бенчмарки удалённых хелперов удалить вместе с ними, иначе пакет не компилируется: `TestIsTestAlert` (`:430`), `TestContainsTest` (`:666`), `BenchmarkIsTestAlert` (`:725`), `BenchmarkContainsTest` (`:740`). `TestSimpleFilterEngine_ShouldBlock_TestAlerts` (`:113`) и `_CombinedRules` (`:331`) не удалять — инвертировать ожидания. Новых тестов здесь не писать.

  <!-- depends: 1.1, 1.2 | verify: cd go-app && go test ./internal/core/services/ -run 'SimpleFilterEngine|FilterEngine' -count=1 -->
- [x] **1.5** Затронутое дерево зелёное, `go fmt` не меняет файлы.

  <!-- depends: 1.1–1.4 | verify: cd go-app && gofmt -l internal/core/services/ && go vet ./internal/core/... && go test ./internal/core/... -count=1 -->
- [x] **1.6** Проверка размера диффа кода для решения по deep-review (Spec § Deep Review): больше ~200 строк — остановиться и запустить `deep-review`.

  <!-- depends: 1.5 | verify: git diff --stat main -- go-app/ | tail -1 -->

**Phase verification:** `cd go-app && go vet ./internal/core/... && go test -race ./internal/core/services/ -count=1`

## Phase 2: Implementation (чарт и доки)

> **Wave 1** — независимые шаги, разные файлы

- [x] **2.1** `helm/amp/values.yaml`: удалить секцию `Filter Rules Configuration` (`:798-805`: заголовок-рамку, `filters: {}`, пример).

  <!-- verify: ! grep -nE "^filters:|Filter Rules Configuration" helm/amp/values.yaml && helm lint helm/amp && helm template amp helm/amp >/dev/null -->
- [x] **2.2** `CHANGELOG.md` `[Unreleased]`:
  - `### Fixed` — PROD-HARDCODED-FILTER: удалены правила по имени, `environment` и namespace; Rule 7 не теряет повторное срабатывание после resolve; лог дропа с fingerprint.
  - `### Breaking changes / migration notes`:
    - что теперь доставляется;
    - `reason` `test_alert`/`disabled_namespace` исчезли из `alert_history_filter_blocked_total`;
    - ключ values `filters:` удалён (был no-op);
    - фрагмент `route:` + receiver `blackhole` из Spec § Rollout (с W024) и альтернатива — silence.

  <!-- verify: awk '/^## \[Unreleased\]/{f=1} f&&/^## \[[0-9]/{exit} f' CHANGELOG.md | grep -c "PROD-HARDCODED-FILTER" (≥2) -->
- [x] **2.3** `docs/ALERTMANAGER_COMPATIBILITY.md`:
  - Known Gap #14 переписать под оставшиеся правила (Spec § Target Design п. 5): resolved старше 24 ч, пустое имя (недостижимо с HTTP API), повтор того же статуса за 1 мин, LLM `noise`/`low_confidence` только в enriched;
  - метрика `alert_history_filter_blocked_total{reason}`; дропнутый алерт отвечает 2xx и виден в `GET /api/v2/alerts`;
  - вступление `:23` и чек-лист `:1016-1017` — без `test*`/`tmp`.

  <!-- verify: ! grep -nE "dev-sandbox|names starting with .test" docs/ALERTMANAGER_COMPATIBILITY.md -->
- [x] **2.4** `README.md:17`: убрать ловушку про встроенный фильтр (`test*`/`tmp`); оставшиеся P0-ловушки и ссылка на Known Gaps — без изменения смысла.

  <!-- verify: ! grep -nE "dev-sandbox|environment=test" README.md -->
- [x] **2.5** Прочие упоминания: поиск по репо, кроме `docs/06-planning/`, `tasks/archive/` и `CHANGELOG.md`, не находит старых правил как действующих.

  <!-- verify: grep -rnE "dev-sandbox|test_alert|disabled_namespace|containsTest|block_test_alerts" --exclude-dir=archive --exclude-dir=06-planning --exclude-dir=tasks --exclude=CHANGELOG.md . | grep -v "^./go-app/.cache" — только тесты фазы 3 (инверсия) -->

**Phase verification:** `git diff --check && helm lint helm/amp`

### Итог фаз 1–2 (implement, 2026-10-13)

- 1.5: `go vet ./internal/core/...`, `go test ./internal/core/...` (487 passed, 8 пакетов), `go test -race ./internal/core/services/` (122 passed) — зелёные; `gofmt -l` пусто.
- 1.6: дифф кода без тестов — 112 строк (`filter_engine.go` +40/−68, `alert_processor.go` +2/−2) < ~200 ⇒ deep-review остаётся пропущенным (Spec § Deep Review).
- 2.1: `helm lint helm/amp` — 0 failed, `helm template` — ok.
- **Отклонения от плана:**
  - 2.2 — дополнительно `helm/amp/CHANGELOG.md` `### Changed` (удаление ключа `filters:`), по образцу PROD-GROUPING-DEFAULT.
  - 2.3/2.5 — `verify`-grep не пустой намеренно: Known Gap #14 и инвертированные тест-кейсы называют `dev-sandbox`/`test_alert` как **доставляемые**.
  - 2.3 — вступление compat-доки переписано целиком: после задачи из гэпов аудита на verbatim-конфиге открытым остаётся только #15 (дефолты чарта).

## Phase 3: Tests (выполняется в `write-tests`)

Тест-план — Spec § Test Plan. Тестовые файлы пишутся здесь, после `implement`.

- [ ] **3.1** T1 — `filter_engine_test.go`: `ShouldBlock` → `false` для `TestRunnerDown`, `TEST_X`, `testing`, `environment=test|testing`, namespace `tmp`/`dev-sandbox` (табличный тест).

  <!-- verify: cd go-app && go test ./internal/core/services/ -run 'TestSimpleFilterEngine' -count=1 -v | grep -c PASS -->
- [ ] **3.2** T3 — `filter_engine_dedup_test.go`: флап firing → resolved → firing в окне — три прохода; следом повтор firing в окне → `true, "duplicate"`. Плюс повтор после смены статуса и возврата: блок считается от последней записи, а не от первой.

  <!-- verify: cd go-app && go test -race ./internal/core/services/ -run 'Dedup' -count=1 -->
- [ ] **3.3** T4 — существующие 5 тестов дедупа зелёные без изменения ожиданий; конкурентный `ShouldBlock` одного fingerprint'а с разными статусами под `-race`.

  <!-- depends: 3.2 | verify: cd go-app && go test -race ./internal/core/services/ -run 'Dedup' -count=3 -->
- [ ] **3.4** T5 — дроп `duplicate` инкрементирует `alert_history_filter_blocked_total{reason="duplicate"}` (`NewSimpleFilterEngineWithMetrics` + `testutil.ToFloat64`). Если `promauto` в глобальный registry мешает повторному созданию — по образцу существующих тестов `pkg/metrics`.

  <!-- verify: cd go-app && go test ./internal/core/services/ -run 'Metric' -count=1 -->
- [ ] **3.5** T2 — `alert_processor_filter_test.go`: `AlertProcessor` с настоящим `SimpleFilterEngine` и `fakePublisher`:
  - алерт `TestRunnerDown` с `namespace=tmp`, `environment=testing` публикуется в transparent (без `LLMClient`) и в enriched (fake `LLMClient`: severity не `noise`, confidence ≥ 0.3);
  - повтор того же алерта в окне не публикуется второй раз (защита direct-publish пути);
  - флап доходит до publisher трижды.

  <!-- verify: cd go-app && go test -race ./internal/core/services/ -run 'AlertProcessor.*Filter' -count=1 -->
- [ ] **3.6** Мутации (Spec § Test Plan), каждая — временная правка, прогон, откат; результат — в `tasks.md` или `evidence/mutations.md`:
  1. вернуть Rule 1 → 3.1/3.5 красные;
  2. ключ `fingerprint|status` → 3.2 красный;
  3. убрать сравнение статуса → 3.2/3.3 красный.

  <!-- depends: 3.1–3.5 | verify: evidence/mutations.md, `git diff --stat` после отката — только тесты и код задачи -->

**Phase verification:** `cd go-app && go vet ./internal/core/... && go test -race ./internal/core/services/ -count=1`

## Phase 4: Testing (гейты AMP, `WORKFLOW.md` § Гейты AMP)

- [ ] **4.1** Затронутые пакеты.

  <!-- verify: cd go-app && go vet ./internal/core/... && go test ./internal/core/... -->
- [ ] **4.2** Быстрый гейт, после него `git status` без новых изменений от `go fmt`.

  <!-- verify: make -C go-app quality-gates-fast && git status --short -->
- [ ] **4.3** Полный гейт (build, golangci-lint, тесты, `-race`, helm lint/template, RBAC-scope, amtool-smoke).

  <!-- depends: 4.2 | verify: scripts/release-gate.sh — RESULT: PASS; флейки — по правилу BUGS, не скрывать -->
- [ ] **4.4** Смоук бинаря, вывод — в `evidence/smoke.md`:
  - `POST /api/v2/alerts` с `alertname=TestRunnerDown` и `namespace=tmp` → доставлен на webhook-приёмник;
  - конфиг с route на `blackhole` из migration note загружается (W024 в логе) и такой алерт не доставляет.

  <!-- depends: 4.3 | verify: evidence/smoke.md -->
- [ ] **4.5** `git diff --check`; в диффе нет проглоченных ошибок `_, _ :=`.

  <!-- verify: git diff --check main && ! git diff main -- go-app | grep -nE '^\+.*_, _ :=' -->

**Phase verification:** release-gate `RESULT: PASS` + `evidence/smoke.md`.

## Phase 5: Finalize

- [ ] **5.1** Planning на ветке задачи:
  - BUGS `HARDCODED-FILTER-DROPS-ALERTS` → `Status: closed` со ссылкой на задачу;
  - BACKLOG `PROD-HARDCODED-FILTER` и `DEAD-VALUES-FILTERS` → в «Закрыто»;
  - NEXT — убрать из WIP и из порядка P0;
  - DONE — запись.

  <!-- verify: grep -n "PROD-HARDCODED-FILTER" docs/06-planning/NEXT.md — пусто; DONE.md — запись -->
- [ ] **5.2** Follow-up в BUGS: `DIRECT-PUBLISH-RENOTIFIES-ON-RESEND` — direct-publish путь без nflog шлёт уведомление на каждую повторную отправку старше окна Rule 7 (Spec § Impact Analysis).

  <!-- verify: grep -n "DIRECT-PUBLISH-RENOTIFIES-ON-RESEND" docs/06-planning/BUGS.md -->
- [ ] **5.3** Workspace → `tasks/archive/PROD-HARDCODED-FILTER/`; статус артефактов `complete`.

  <!-- verify: ls tasks/archive/PROD-HARDCODED-FILTER/ -->

## Definition of Done

- [ ] All steps are complete or explicitly marked blocked/skipped
- [ ] Success criteria from `requirements.md` are covered
- [ ] Contracts from `Spec.md` are implemented or deviations are recorded
- [ ] Deep review verdict is `pass`, or deep review was not required (Spec § Deep Review: skipped, если дифф кода ≤ ~200 строк — шаг 1.6)
- [ ] Tests for changed behavior are added or updated
- [ ] Phase checks pass
- [ ] Docs/planning are updated if behavior, contracts, or process changed
