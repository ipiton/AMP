---
id: PROD-HARDCODED-FILTER
slug: prod-hardcoded-filter
stream: Pipeline
type: bug
status: draft
created_at: 2026-10-13
updated_at: 2026-10-13
based_on:
  - requirements.md
  - research.md
---

# Specification: захардкоженный фильтр не должен молча выбрасывать алерты

**Version:** 1.0
**Status:** Draft

## Summary

Из `SimpleFilterEngine` удаляются правила без аналога в upstream: имя на `test*`, `environment=test|testing`, namespace `dev-sandbox`/`tmp`. Правило «повтор за 1 мин» перестаёт терять алерт при флапе firing → resolved → firing. Каждый оставшийся дроп логируется с fingerprint. Мёртвый ключ `filters:` уходит из values, доки и CHANGELOG сообщают о смене поведения.

## Requirements Coverage

| Requirement / Criterion | Covered by |
|---|---|
| Правила `test*`, `environment=test`, namespace удалены; такие алерты доходят до ресивера в transparent и enriched | Target Design п. 1; Component Architecture `filter_engine.go`; тесты T1, T2 |
| Rule 7, 5, 6 сверены с upstream | `research.md` § 2, § 4 (выполнено) |
| Флап в окне проходит, повтор того же статуса в окне блокируется | Target Design п. 2; тесты T3, T4 |
| Дроп виден: метрика с `reason`, лог INFO с `fingerprint`; TODO убраны | Target Design п. 3; Observability; тест T5 |
| `filters:` удалён из values; `helm lint`/render зелёные | Target Design п. 4; гейт release-gate (helm lint/template) |
| CHANGELOG с migration note; compat-дока Known Gaps | Target Design п. 5; Rollout |
| Гейты AMP | `WORKFLOW.md` § Гейты AMP, `testing` |

## Current State

- **Code:**
  - `go-app/internal/core/services/filter_engine.go` (218 строк) — `SimpleFilterEngine`, правила 1–7, `isTestAlert`/`containsTest`/`isDisabledNamespace`, дедуп-map `fingerprint|status → seenAt`. Метрики `FilterMetrics` пишутся в `ShouldBlock`.
  - `alert_processor.go:719-735` (`processTransparent`) и `:784-800` (`processEnriched`) вызывают `ShouldBlock`; при блоке — INFO-лог без fingerprint и `// TODO: Record filter metrics`, `return nil`.
  - Создание — `service_registry.go:1226`, без параметров.
- **Data:** не применимо (in-process map; БД и Redis не затронуты).
- **Tests:**
  - `filter_engine_test.go` (8 тестов, кейсы `test_alert` — строки 131–356, `disabled_namespace` — 542–558);
  - `filter_engine_dedup_test.go` (5 тестов: блок в окне, смена статуса, истечение, отключение, пустой fingerprint);
  - `alert_processor_test.go` — `fakeFilterEngine` (никогда не блокирует), `fakePublisher`, `newTestProcessorConfig`.
- **Docs:**
  - `docs/ALERTMANAGER_COMPATIBILITY.md` — строка 23 (вступление), Known Gap #14 (`:884-890`), чек-лист «Still validate» (`:1016-1017`);
  - `README.md:17` — runtime note;
  - `helm/amp/values.yaml:801-805` — `filters: {}` с примером.

## Design Premises

| Premise | Confirmed by | Class | If wrong |
|---|---|---|---|
| `ShouldBlock` вызывается только из `processTransparent` и `processEnriched`; других потребителей `SimpleFilterEngine` нет | `grep -rn "ShouldBlock\|SimpleFilterEngine\|filterEngine"` по всему `go-app` (без `_test`): `alert_processor.go:727,792`, `service_registry.go:1226,2323,2566` | call-path-traced | Удалённые правила продолжили бы действовать где-то ещё — тесты T1/T2 этого не поймали бы |
| Точный повтор (тот же status и `endsAt`) отсекается раньше фильтра дедуп-сервисом (`Ignored`) | `deduplication.go:313-405` (`alertNeedsUpdate`), `alert_processor.go:519-546` | code-read | Rule 7 оказался бы единственной защитой от дублей на всех путях; выбранный вариант B её сохраняет, так что дизайн выживает |
| На direct-publish пути (без `route:` или `grouping.enabled: false`) нет дедупа уведомлений, кроме Rule 7 | Две реализации `PublishToReceiver` во всём репозитории (`publishing_adapter.go:62` → `coordinator.PublishToTargets` без nflog; `publishing_metrics_only.go:36`); nflog — только у группового пути (`coordinator.go:619+`, `targetAlerts`) | call-path-traced | Если бы дедуп был, Rule 7 можно было бы удалить целиком (вариант A из research) — дизайн остался бы корректным, но не минимальным |
| Группировка не читает `endsAt`: пропущенный Rule 7 повтор не ломает резолв группы | `grep -rn EndsAt internal/infrastructure/grouping` (без `_test`) — пусто; compat Known Gap #12 | code-read | Повтор, задержанный Rule 7, приводил бы к ложному резолву в группе; это вне задачи, зафиксировать follow-up |
| Алерт без `alertname` отвергается парсерами до фильтра (Rule 5 недостижим с HTTP API) | `webhook/parser.go:111-112`, `handlers/alerts.go:537-539`, `handleAlertsPost` → `parseAlertsForProcessing` | code-read | Rule 5 остаётся как есть — от этого дизайн не зависит |
| В `helm/amp` нет `values.schema.json` и шаблона, читающего `.Values.filters` | `ls helm/amp`, `grep -rn filters helm/amp/templates` — только `rootly.filterConfig` и `$target.filterConfig` (другие ключи) | call-path-traced | Удаление ключа сломало бы `helm upgrade` с пользовательским `filters:` |
| Prometheus HA-пара шлёт один алерт с разными `endsAt`, поэтому дедуп-сервис видит `Updated`, и Rule 7 — единственное, что гасит второе уведомление на direct-publish пути | Внешнее знание о Prometheus (`EndsAt = ValidUntil` от времени оценки каждого replica) | assumed | Только обосновывает, что Rule 7 оставляем (вариант B). Если неверно, вариант B всё равно корректен; см. Open Questions |

## Target Design

1. **Удалить правила 1 и 4.** Из `shouldBlockInternal` уходят проверки `isTestAlert` и `isDisabledNamespace`, вместе с функциями `isTestAlert`, `containsTest`, `isDisabledNamespace`. Значения `reason` `test_alert` и `disabled_namespace` больше не порождаются. Нумерация правил в комментариях не перенумеровывается: на «Rule 7» ссылаются `DEDUP-STATE-STUB` и тесты.
2. **Rule 7 — ключ по fingerprint, значение `{status, seenAt}`.**
   - Блок — только если сохранённый статус равен текущему и `now - seenAt < window`.
   - Иначе запись перезаписывается текущими `{status, now}` и алерт проходит. При блоке запись не трогается — окно не продлевается, как сейчас.
   - Флап firing → resolved → firing внутри окна проходит целиком: каждый переход меняет статус. Повтор того же статуса в окне блокируется, как раньше.
   - Lazy sweep и `SetDedupWindow` без изменений.
3. **Лог дропа.** В `processTransparent`/`processEnriched` INFO `Alert blocked by filter` получает поле `fingerprint`, `// TODO: Record filter metrics` удаляются: метрику уже пишет `ShouldBlock`. Уровень остаётся INFO: штатный повтор Prometheus в окне на direct-publish пути сделал бы WARN шумным. Метрика — основной сигнал.
4. **Чарт.** Из `helm/amp/values.yaml` удаляется секция `Filter Rules Configuration` целиком (`:798-805`: заголовок, `filters: {}`, пример).
5. **Доки и CHANGELOG.**
   - `[Unreleased]` → `### Fixed` (PROD-HARDCODED-FILTER) и запись в breaking/migration notes: что теперь доставляется, исчезнувшие `reason`, удалённый ключ values, готовый фрагмент `route:` для старого поведения (см. Rollout).
   - Compat-дока: Known Gap #14 переписывается под оставшиеся правила — resolved старше 24 ч, пустое имя (недостижимо с API), повтор того же статуса за 1 мин; LLM `noise`/`low_confidence` только в enriched (`PROD-LLM-ALERT-PATH-ISOLATION`). Метрика есть, дропнутый алерт отвечает 2xx и виден в `GET /api/v2/alerts`.
   - Вступление (`:23`), чек-лист (`:1016-1017`) и `README.md:17` перестают упоминать `test*`/`tmp`.

Почему так: upstream не фильтрует по содержимому; нужное поведение даёт его же route. Rule 7 сохраняет единственную защиту direct-publish пути от двойной отправки, но перестаёт подменять текущее состояние алерта прошлым.

## API Contracts

Не применимо: HTTP API, формат ответа и коды не меняются. Поведенческие изменения перечислены в Impact Analysis.

## Data Model / Migrations

Не применимо: внутренняя структура `dedupSeen` живёт в памяти процесса; миграций нет.

## Component Architecture

- `go-app/internal/core/services/filter_engine.go`:
  - удалить правила 1 и 4 и их хелперы;
  - `dedupSeen map[string]dedupEntry` (`dedupEntry{status core.AlertStatus; seenAt time.Time}`), ключ — fingerprint;
  - обновить doc-комментарий `ShouldBlock` (сейчас перечисляет «test alerts»).
- `go-app/internal/core/services/alert_processor.go` — два места лога дропа: `fingerprint`, удалить TODO.
- `go-app/internal/core/services/filter_engine_test.go` — кейсы `test_alert`/`disabled_namespace` переделать в «не блокируется».
- `go-app/internal/core/services/filter_engine_dedup_test.go` — флап и повтор того же статуса после смены.
- `go-app/internal/core/services/alert_processor_test.go` (или новый `alert_processor_filter_test.go`) — e2e через `AlertProcessor` с настоящим `SimpleFilterEngine`.
- `helm/amp/values.yaml` — удалить `filters:`.
- `CHANGELOG.md`, `docs/ALERTMANAGER_COMPATIBILITY.md`, `README.md` — тексты.
- Planning при `finalize`: BUGS `HARDCODED-FILTER-DROPS-ALERTS` → closed, BACKLOG `PROD-HARDCODED-FILTER` и `DEAD-VALUES-FILTERS` → закрыты.

## Security Design

- [x] Ownership validation — не применимо (нет пользовательских данных и прав).
- [x] Input validation — не меняется (парсеры раньше фильтра).
- [x] Sensitive data not logged — в лог добавляется только fingerprint (хеш label'ов), label'ы и аннотации не логируются.
- [x] Rate limiting — не применимо.
- [x] Auth/RBAC — не применимо.

## Invariants

- [ ] Алерт, не попадающий ни под одно оставшееся правило, проходит фильтр — ни одного нового условия блокировки.
- [ ] Повтор того же fingerprint и того же статуса в окне блокируется, как до изменения (защита direct-publish пути).
- [ ] Смена статуса никогда не блокируется Rule 7.
- [ ] Имена метрик `alert_history_filter_blocked_total`, `_filtered_total`, `_duration_seconds` и их label'ы не меняются.
- [ ] LLM-правила 2/3, правила 5/6 ведут себя как раньше.
- [ ] Ответ `POST /api/v2/alerts` на дропнутый алерт — по-прежнему 2xx.

## Edge Cases

1. `alertname=TestRunnerDown`, `TEST_X`, `testing` → доставляется (раньше `test_alert`).
2. `environment=test` / `testing` → доставляется.
3. Namespace `tmp`, `dev-sandbox` → доставляется.
4. Флап firing(t0) → resolved(t0+20s) → firing(t0+40s) → три прохода. Повтор firing(t0+50s) → блок, потому что тот же статус, что в t0+40s, моложе окна.
5. Повтор firing в окне после firing → блок, `reason=duplicate` (как раньше).
6. Повтор firing ровно на границе окна (`now - seenAt == window`) → проходит (сравнение `<`, как сейчас).
7. Пустой fingerprint → Rule 7 не применяется (как сейчас).
8. Окно ≤ 0 → Rule 7 выключен (как сейчас).
9. Алерт в enriched-режиме с LLM-классификацией `noise` → блок `noise` (не меняется, вне задачи).
10. Пользователь задал `filters:` в своих values → `helm upgrade` проходит, ключ игнорируется, как и раньше.
11. Конкурентные `ShouldBlock` одного fingerprint'а — под `dedupMu`, как сейчас; тест под `-race`.

## Impact Analysis

- **Affected modules:** `internal/core/services` (фильтр, лог процессора), `helm/amp` (values), доки.
- **Breaking changes:**
  - алерты `test*` (имя), `environment=test|testing`, namespace `dev-sandbox`/`tmp` теперь доставляются;
  - `reason` `test_alert` и `disabled_namespace` исчезают из `alert_history_filter_blocked_total`;
  - ключ values `filters:` удалён (был no-op).
- **New dependencies:** none.
- **Risks:**
  - Рост объёма уведомлений у тех, кто отправлял в AMP тестовые алерты. Митигируется migration note с готовым route.
  - Отдельно, вне задачи: на direct-publish пути каждая повторная отправка Prometheus с новым `endsAt` старше окна уходит ресиверу заново — дедупа уведомлений там нет (premise 3). Это существовало до задачи и ею не меняется → follow-up в BUGS при `finalize` (`DIRECT-PUBLISH-RENOTIFIES-ON-RESEND`).

## Rollout / Rollback

- **Rollout:** нет `deploy` (`WORKFLOW.md`). Сигнал `R` закрывается записью в `CHANGELOG.md` `[Unreleased]` с migration note. Фрагмент, который возвращает старый дроп:

  ```yaml
  route:
    receiver: default
    routes:
      - receiver: blackhole
        matchers:
          - alertname=~"(?i)test.*"
      - receiver: blackhole
        matchers:
          - environment=~"test|testing"
      - receiver: blackhole
        matchers:
          - namespace=~"dev-sandbox|tmp"
  receivers:
    - name: default
      # ...
    - name: blackhole
  ```

  Маршруты ставятся первыми дочерними, без `continue`. Альтернатива — долгий silence с теми же matchers. Receiver без интеграций AMP принимает как upstream-blackhole с предупреждением W024 (`pkg/configvalidator/validators/receiver.go:71-77`); загрузку фрагмента проверить смоуком в `testing`.
- **Rollback:** предыдущий образ; состояние — только in-memory map, потерь нет.
- **Feature flag:** не применимо (решение владельца — без opt-in).
- **Risk из `assumed`-премисы (HA-пара Prometheus):** вариант B сохраняет защиту, поэтому ошибочность премисы ничего не ломает.

## Observability

- **Logs:** INFO `Alert blocked by filter` + `fingerprint` (было: `alert`, `reason`, в enriched — `severity`).
- **Metrics:** без изменений: `alert_history_filter_blocked_total{reason}` (`duplicate`, `noise`, `low_confidence`, `empty_alert_name`, `old_resolved`), `alert_history_filter_filtered_total{result}`, `alert_history_filter_duration_seconds`. Тест T5 проверяет, что дроп инкрементирует счётчик с `reason`.
- **Alerts:** не применимо.

## Test Plan (вход для `write-tests`)

- **T1** — `SimpleFilterEngine.ShouldBlock` на кейсах edge 1–3 → `false`. Бывшие кейсы `test_alert`/`disabled_namespace` в `filter_engine_test.go` инвертируются, не удаляются.
- **T2** — `AlertProcessor` с настоящим `SimpleFilterEngine` и `fakePublisher`. `TestRunnerDown` + `namespace=tmp` + `environment=testing` доходит до publisher в transparent (без LLM) и в enriched (fake `LLMClient`, классификация не `noise` и `confidence ≥ 0.3`).
- **T3** — флап firing → resolved → firing в окне: три `false`. Затем повтор firing в окне → `true, "duplicate"`.
- **T4** — существующие тесты дедупа (`filter_engine_dedup_test.go`) зелёные без изменения ожиданий. Конкурентный тест под `-race`.
- **T5** — дроп пишет `alert_history_filter_blocked_total{reason="duplicate"}` (через `NewSimpleFilterEngineWithMetrics` и отдельный registry или `testutil.ToFloat64`).
- **Мутации:**
  - вернуть `isTestAlert` → T1/T2 красные;
  - вернуть ключ `fingerprint|status` → T3 красный;
  - убрать сравнение статуса → T4 (`StatusChangePasses`) или T3 красный.
- **Chart:** `helm lint`/`helm template` в release-gate; ключ удалён — отдельный render-тест не нужен (шаблонов, читающих его, не было).
- **Смоук бинаря:** `POST /api/v2/alerts` с `alertname=TestRunnerDown` на webhook-приёмник (по образцу `deploy/smoke/run.sh`) → доставлен; конфиг с route на `blackhole` из migration note → загружается и не доставляет.

## Deep Review

- **Mandatory triggers present:** none (сигналы `X R`, нет `S`/`M`, не pre-release, < 3 сигналов).
- **Discretionary triggers present:** none. Ожидаемый дифф кода ~60–100 строк без доков, паттерн не новый.
- **Decision:** recommended, skipped (reason: Standard tier, малый дифф, поведение закрыто тестами T1–T5 с мутациями). Если дифф кода превысит ~200 строк — пересмотреть на `implement`.

## Open Questions

- [ ] `assumed`: HA-пара Prometheus даёт `Updated` в дедуп-сервисе (разные `endsAt`) — только обоснование сохранения Rule 7; на дизайн не влияет. Проверка — вне задачи.
- [ ] Follow-up (не блокирует): `DIRECT-PUBLISH-RENOTIFIES-ON-RESEND` — direct-publish путь без nflog шлёт уведомление на каждый resend старше окна Rule 7. Завести в BUGS на `finalize`.
