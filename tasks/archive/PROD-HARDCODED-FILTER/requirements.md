---
id: PROD-HARDCODED-FILTER
slug: prod-hardcoded-filter
stream: Pipeline
type: bug
priority: critical
status: complete
created_at: 2026-10-13
updated_at: 2026-10-13
---

# Requirements: захардкоженный фильтр не должен молча выбрасывать алерты

## Problem Framing

- **Symptom:** алерт `TestRunnerDown`, алерт с `environment=testing` или из namespace `tmp`/`dev-sandbox` принимается `POST /api/v2/alerts` (2xx), но до ресивера не доходит. Повторная отправка того же firing-алерта в течение минуты тоже теряется. Видно это только по INFO-логу `Alert blocked by filter` и по метрике `alert_history_filter_blocked_total{reason}`, про которую оператор не знает.
- **Root Cause:** `SimpleFilterEngine.shouldBlockInternal` (`go-app/internal/core/services/filter_engine.go:85-124`) — семь правил, зашитых в код, без конфига. Вызывается из `processTransparent` (`alert_processor.go:727`) и `processEnriched` (`:792`), то есть в обоих режимах, через которые идёт обычный алерт (`EnrichmentModeManager.GetMode` захардкожен на `enriched`, без LLM он падает в transparent). Создаётся без параметров в `service_registry.go:1226`; ключ `filters:` в `helm/amp/values.yaml:801` ни одним шаблоном и ни одним полем `Config` не читается.
  - Rule 1 `test_alert`: `containsTest` проверяет префикс `test` без учёта регистра (комментарий врёт про «contains»), плюс `environment=test|testing`.
  - Rule 4 `disabled_namespace`: map `dev-sandbox`, `tmp` в коде.
  - Rule 5 `empty_alert_name`, Rule 6 `old_resolved` (resolved и `endsAt` старше 24 ч / `startsAt` старше 48 ч), Rule 7 `duplicate` (fingerprint+status за `DefaultDedupWindow` = 1 мин, `DEDUP-STATE-STUB` 2026-08-17).
  - Rule 2/3 `noise`/`low_confidence` — решения LLM, работают только в enriched с классификацией.
- **Why Now:** P0 в `BACKLOG.md` § Production Readiness (аудит 2026-10-06): AMP позиционируется как drop-in замена Alertmanager, а upstream ничего подобного не делает. Тихая потеря алерта хуже громкой ошибки.
- **How We Measure:** алерты с именем `Test*`, `environment=test`, namespace `tmp`/`dev-sandbox` и повтор за минуту доходят до webhook-ресивера при дефолтном конфиге (тест на `AlertProcessor` + смоук бинаря); каждое срабатывание правила, если оно включено, видно метрикой с `reason`.

## Risk Profile

- **Signals:** `X R` (пересмотрено после research 2026-10-13: было `C X R`)
  - `C` снят: по решению владельца правила 1 и 4 удаляются, а не уходят под opt-in-конфиг — новой секции конфига нет, HTTP API и имена метрик не меняются. Breaking по поведению (алерты `test*`/`tmp` доставляются) закрывается migration note, это `R`.
  - `X` — `core/services` (движок), `helm/amp` (удаление ключа `filters:`), публичные доки.
  - `R` — горячий путь приёма алерта; меняется, какие алерты доставляются; observability (метрика, лог).
- **Tier:** Standard (`X R`). `deep-review` по усмотрению; обязателен, если дифф больше ~200 строк.
- **Notes:** `S`/`M` нет: данные не мигрируют, правило «не дропать» только уменьшает потери. `R` в AMP закрывается записью в `CHANGELOG.md` `[Unreleased]` с migration note (deploy нет, `WORKFLOW.md`).

## User Stories

1. Как оператор, переносящий `alertmanager.yml` в AMP, я хочу, чтобы любой принятый алерт доходил до ресивера по `route:`, как в upstream, чтобы не терять `TestRunnerDown` и алерты из namespace `tmp`.
2. Как оператор, которому дроп тестовых алертов нужен, я хочу сделать его штатным upstream-конфигом (route на blackhole-receiver или silence) по готовому примеру из migration note, а каждое срабатывание оставшихся правил фильтра видеть в метрике с `reason`.
3. Как оператор, я хочу, чтобы алерт, который снова загорелся вскоре после resolve (флап), доходил до ресивера, а не оставался у ресивера «resolved».

## Success Criteria

- [x] Правила по имени (`test*`), `environment=test|testing` и namespace (`dev-sandbox`, `tmp`) удалены из кода: такие алерты доходят до ресивера — тест на `AlertProcessor` в transparent и enriched.
- [x] Rule 7, 5, 6 сверены с upstream (`research.md` § 2, решение § 4).
- [x] Rule 7: флап firing → resolved → firing внутри окна проходит целиком; повтор того же статуса в окне по-прежнему блокируется.
- [x] Каждый дроп виден: `alert_history_filter_blocked_total{reason}` (существующая метрика, имя не меняется), лог дропа — INFO с `fingerprint` и `reason`. `// TODO: Record filter metrics` в `alert_processor.go` убраны.
- [x] Ключ `filters:` удалён из `helm/amp/values.yaml` (закрывает BACKLOG `DEAD-VALUES-FILTERS`); `helm lint`/render-тесты чарта зелёные.
- [x] `CHANGELOG.md` `[Unreleased]` — Fixed + breaking/migration note с готовым route на blackhole-receiver, возвращающим старый дроп; `docs/ALERTMANAGER_COMPATIBILITY.md` Known Gaps — какие правила фильтра остались (5, 6, 7, LLM 2/3) и что дропнутый алерт виден в `GET /api/v2/alerts`.
- [x] Гейты AMP (`WORKFLOW.md` § Гейты AMP): затронутые пакеты, `quality-gates-fast`, `scripts/release-gate.sh`, `git diff --check`.

## Non-Goals

- LLM-правила Rule 2/3 (`noise`, `low_confidence`) — `PROD-LLM-ALERT-PATH-ISOLATION` (P1): там же LLM-дроп за флагом, асинхронная классификация и настоящий переключатель enrichment mode. Здесь их не трогаем, кроме общей метрики/лога дропа.
- `EnrichmentModeManager.GetMode`, захардкоженный на `enriched`, — тоже `PROD-LLM-ALERT-PATH-ISOLATION`.
- Конфигурируемые фильтры (opt-in секция `filters:`, regex по label'ам) — отвергнуто владельцем 2026-10-13: для «не слать» есть upstream-средства (silences, inhibition, `route` на receiver без интеграций).
- Удаление правил 5 (`empty_alert_name`) и 6 (`old_resolved`): первое недостижимо с API, второе по исходу совпадает с upstream (`research.md` § 3).
- UI/API управления фильтрами, hot reload фильтров сверх того, что даст общий reload конфига.

## Constraints

- **Scope:** `go-app/internal/core/services/filter_engine.go` и его тесты, лог и TODO в `alert_processor.go`, `helm/amp/values.yaml` (удаление `filters:`), CHANGELOG и публичные доки. Сверх этого — только по решению в `Spec.md`.
- **Security:** не применимо (auth не трогаем). В лог дропа не писать значения label'ов целиком сверх того, что уже логируется (`alertname`, fingerprint).
- **Compatibility:** поведение меняется: раньше дропалось, теперь доставляется. Кто полагался на дроп `test*`/`tmp`, получает migration note с route, возвращающим старое поведение. Имена метрик `alert_history_filter_*` не меняются; значения `reason` `test_alert` и `disabled_namespace` исчезают.

## Discovery Notes

- Similar tasks: `tasks/archive/PROD-GROUPING-DEFAULT/` (смена дефолта с migration note, render-тест чарта, e2e с webhook), `tasks/archive/FU-TOPLEVEL-INHIBIT-RULES/` (сверка с upstream, конфиг + reload), `DEDUP-STATE-STUB` (`archive/DONE-2026-08.md`, ввёл Rule 7, тесты `filter_engine_dedup_test.go`).
- Relevant patterns: `pkg/metrics` `FilterMetrics` (`alert_history_filter_blocked_total{reason}`, `filtered_total{result}`, `duration_seconds`) — уже пишется из `ShouldBlock`, расходится с описанием бага («метрики нет»); дефолты конфига через viper в `internal/config/config.go`; `configvalidator` для W/E-кодов.
- Open unknowns: закрыты в `research.md`, кроме одной `assumed`-премисы для spec — на direct-publish пути нет другого дедупа уведомлений, кроме Rule 7.
