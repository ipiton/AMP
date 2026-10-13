---
id: PROD-HARDCODED-FILTER
slug: prod-hardcoded-filter
stream: Pipeline
type: bug
priority: critical
status: active
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

- **Signals:** `C X R`
  - `C` — новая секция конфига (фильтры за явным opt-in) и, возможно, переименование/добавление метрики; поведение по умолчанию меняется (breaking для тех, кто полагался на дроп).
  - `X` — `core/services` (движок), `config` (схема и валидация), `application` (проводка), `helm/amp` (ключ `filters:`), публичные доки.
  - `R` — горячий путь приёма алерта; меняется, какие алерты доставляются; observability (метрика, лог).
- **Tier:** Full (3 сигнала).
- **Notes:** `S`/`M` нет: данные не мигрируют, правило «не дропать» только уменьшает потери. `R` в AMP закрывается записью в `CHANGELOG.md` `[Unreleased]` с migration note (deploy нет, `WORKFLOW.md`).

## User Stories

1. Как оператор, переносящий `alertmanager.yml` в AMP, я хочу, чтобы любой принятый алерт доходил до ресивера по `route:`, как в upstream, чтобы не терять `TestRunnerDown` и алерты из namespace `tmp`.
2. Как оператор, которому фильтр нужен, я хочу включить конкретное правило явно в конфиге и видеть каждое срабатывание в метрике с `reason`, чтобы дроп был осознанным и наблюдаемым.
3. Как оператор, я хочу, чтобы повторная отправка firing-алерта Prometheus'ом (каждые `resend_delay`) не терялась, чтобы `endsAt` продлевался и алерт не резолвился сам по таймауту.

## Success Criteria

- [ ] При дефолтном конфиге правила по имени (`test*`), `environment=test|testing` и namespace не блокируют ни одного алерта — тест на `AlertProcessor` в transparent и enriched.
- [ ] Эти правила включаются только явным конфигом; namespace-список и шаблон имени задаются конфигом, а не кодом. Если ни одно правило не включено, фильтр не вызывается или пропускает всё.
- [ ] Rule 7 (повтор за 1 мин) сверен с upstream по коду Alertmanager (`api/v2`, `provider/mem`): решение (убрать / оставить с merge-семантикой / оставить за opt-in) записано в `research.md`; повторная отправка firing-алерта продлевает `endsAt` в хранилище и в группе, а не теряется.
- [ ] Rule 5 (`empty_alert_name`) и Rule 6 (`old_resolved`) сверены с upstream-валидацией; решение и обоснование — в `research.md`.
- [ ] Каждый дроп виден: счётчик с `reason` (`amp_alerts_filtered_total{reason}` из BACKLOG или существующий `alert_history_filter_blocked_total{reason}` — выбор в `Spec.md`), лог — не тише INFO, с fingerprint. `// TODO: Record filter metrics` в `alert_processor.go` убраны.
- [ ] Ключ `filters:` в values либо реально проводится в конфиг AMP, либо удалён; render-тест чарта.
- [ ] `CHANGELOG.md` `[Unreleased]` — Fixed + breaking/migration note; строка в `docs/ALERTMANAGER_COMPATIBILITY.md` Known Gaps (или закрытие расхождения); `CONFIGURATION_GUIDE.md` описывает opt-in-фильтры.
- [ ] Гейты AMP (`WORKFLOW.md` § Гейты AMP): затронутые пакеты, `quality-gates-fast`, `scripts/release-gate.sh`, `git diff --check`.

## Non-Goals

- LLM-правила Rule 2/3 (`noise`, `low_confidence`) — `PROD-LLM-ALERT-PATH-ISOLATION` (P1): там же LLM-дроп за флагом, асинхронная классификация и настоящий переключатель enrichment mode. Здесь их не трогаем, кроме общей метрики/лога дропа.
- `EnrichmentModeManager.GetMode`, захардкоженный на `enriched`, — тоже `PROD-LLM-ALERT-PATH-ISOLATION`.
- Полноценный язык фильтров (regex по любым label'ам, как пример в `values.yaml`) — только минимальный opt-in для существующих правил. Для «не слать» у upstream есть silences/inhibition/`route` с пустым ресивером.
- UI/API управления фильтрами, hot reload фильтров сверх того, что даст общий reload конфига.

## Constraints

- **Scope:** `go-app/internal/core/services/filter_engine.go`, вызовы в `alert_processor.go`, проводка в `internal/application/service_registry.go`, `internal/config` (новые ключи + валидация), `helm/amp/values.yaml` и шаблон ConfigMap/env, доки. Сверх этого — только по решению в `Spec.md`.
- **Security:** не применимо (auth не трогаем). В лог дропа не писать значения label'ов целиком сверх того, что уже логируется (`alertname`, fingerprint).
- **Compatibility:** поведение по умолчанию меняется: раньше дропалось, теперь доставляется. Кто-то мог полагаться на дроп `test*` → migration note с конфигом, который возвращает старое поведение. Существующие имена метрик `alert_history_filter_*` не ломать без отдельной записи в CHANGELOG.

## Discovery Notes

- Similar tasks: `tasks/archive/PROD-GROUPING-DEFAULT/` (смена дефолта с migration note, render-тест чарта, e2e с webhook), `tasks/archive/FU-TOPLEVEL-INHIBIT-RULES/` (сверка с upstream, конфиг + reload), `DEDUP-STATE-STUB` (`archive/DONE-2026-08.md`, ввёл Rule 7, тесты `filter_engine_dedup_test.go`).
- Relevant patterns: `pkg/metrics` `FilterMetrics` (`alert_history_filter_blocked_total{reason}`, `filtered_total{result}`, `duration_seconds`) — уже пишется из `ShouldBlock`, расходится с описанием бага («метрики нет»); дефолты конфига через viper в `internal/config/config.go`; `configvalidator` для W/E-кодов.
- Open unknowns:
  1. Upstream: что делает Alertmanager с повторной отправкой того же алерта (merge `endsAt` в `provider/mem`) и как AMP хранит алерт до/после фильтра — теряется ли продление `endsAt` при Rule 7.
  2. Upstream-валидация: принимается ли алерт без `alertname` (upstream требует только непустые label'ы) и старый resolved-алерт.
  3. Где сохраняется алерт (история/storage) относительно фильтра — дропнутый алерт есть в истории/`GET /api/v2/alerts` или нет.
  4. Имя метрики: новая `amp_alerts_filtered_total` против существующей `alert_history_filter_blocked_total`.
  5. Нужна ли форма конфига, совместимая с упомянутым `filters:` в values, или ключ удаляется.
