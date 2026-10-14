---
id: PROD-HARDCODED-FILTER
slug: prod-hardcoded-filter
stream: Pipeline
type: bug
artifact: research-pack
status: complete
created_at: 2026-10-13
updated_at: 2026-10-13
---

# Research Pack — захардкоженный фильтр не должен молча выбрасывать алерты

Уровень: 2 (light/full). Причины: сверка с upstream, несколько вариантов по каждому правилу, горячий путь приёма алерта. Upstream сверялся по Alertmanager `v0.32.0` из module cache (`~/go/pkg/mod/github.com/prometheus/alertmanager@v0.32.0`); эта же версия — зависимость `go-app/go.mod`.

## 0) TL;DR

- **Задача:** `SimpleFilterEngine` молча дропает принятые алерты по правилам, зашитым в код, в любом режиме, в том числе без LLM.
- **Что нашли:**
  - Правила 1 (`test*`, `environment=test`) и 4 (namespace `dev-sandbox`/`tmp`) в upstream не имеют аналога.
  - Правило 5 (пустой `alertname`) — мёртвый код: оба парсера `POST /api/v2/alerts` отвечают 400 раньше фильтра.
  - Правило 6 (resolved старше 24 ч) по исходу совпадает с upstream: тот тоже не уведомит.
  - Правило 7 (повтор за 1 мин) дедуплицирует повторно то, что уже отсекает `deduplication` (Step 0), и при этом **теряет повторное срабатывание после флапа** firing → resolved → firing в пределах минуты.
  - Метрика дропа уже есть: `alert_history_filter_blocked_total{reason}`.
  - Дропнутый алерт отвечает 2xx, лежит в БД и в `GET /api/v2/alerts`, а до ресивера не доходит.
- **Решение (владелец, 2026-10-13):** правила 1 и 4 удалить без opt-in; это поведение воспроизводится upstream-средствами (route на blackhole-receiver или silence). Правило 7 — ключ по последнему статусу fingerprint'а, чтобы флап не терялся. Правила 5 и 6 остаются. `filters:` из values удалить.
- **Риски:**
  - Кто полагался на дроп `test*` или `tmp`, начнёт получать эти алерты. Закрывается migration note с готовым route.
  - Правило 7 на direct-publish пути — единственный ограничитель повторных уведомлений от HA-пары Prometheus (premise `assumed`, проверить в spec).
- **Дальше:** `spec`.

## 1) Questions

1. Какие из семи правил имеют аналог в upstream Alertmanager, а какие — чистое расхождение?
2. Что upstream делает с повторной отправкой того же алерта и что теряет AMP, когда правило 7 её режет?
3. Принимает ли upstream алерт без `alertname` и старый resolved-алерт; что делает AMP раньше фильтра?
4. Где сохраняется алерт относительно фильтра: виден ли дропнутый алерт в API и БД?
5. Есть ли метрика дропа и под каким именем?
6. Чем заменить удалённые правила для оператора, которому дроп нужен?

## 2) Findings

### Код AMP

- **Порядок `ProcessAlert`** (`go-app/internal/core/services/alert_processor.go:516-687`):
  1. Step 0 — `deduplication.ProcessAlert`: пишет в БД и возвращает `Ignored` для точного дубля.
  2. Inhibition cache и проверка inhibition.
  3. `evaluateRoute`.
  4. Режим (`GetMode` захардкожен на `enriched`).
  5. `processTransparent` / `processEnriched` → `filterEngine.ShouldBlock` (`:727`, `:792`) → группа или direct publish.

  Фильтр стоит **после** записи в БД и **до** группы и ресивера.
- **Дедуп-сервис** (`deduplication.go:204-405`): `Ignored`, только если совпали status и `endsAt`; при изменении `endsAt` или статуса — `Updated` (обновляет `Status`, `EndsAt`, `Annotations`). Повторная отправка Prometheus с новым `endsAt` проходит Step 0 как `Updated`, то есть точные дубли правило 7 уже не видит. Если storage недоступен, сервис не создаётся (`service_registry.go:1229-1234`, degraded reason), и тогда правило 7 — единственная дедупликация.
- **Хендлер** (`internal/application/handlers/alerts.go:404-470`): `ProcessAlert`, вернувший `nil` (в том числе дроп фильтром), считается успешным. Алерт уходит в `store.IngestBatch` (in-memory store для `GET /api/v2/alerts`), клиент получает 200. Дропнутый алерт **виден в API как активный**, но не уведомлён.
- **Метрики уже есть**: `ShouldBlock` пишет `FilterMetrics` (`filter_engine.go:62-80`): `alert_history_filter_blocked_total{reason}`, `alert_history_filter_filtered_total{result}`, `alert_history_filter_duration_seconds` (`pkg/metrics/metrics.go:1522-1553`, `Namespace = "alert_history"`). Описание бага («метрики нет») устарело: `// TODO: Record filter metrics` в `alert_processor.go:733`, `:798` — мёртвые комментарии. Лог дропа — INFO без fingerprint.
- **Парсеры**: Prometheus-путь (`webhook/parser.go:111-112`, `convertSingleAlert`) и legacy-путь (`handlers/alerts.go:537-539`) отвергают алерт без `alertname` ошибкой ⇒ 400 до фильтра. Правило 5 на API-пути недостижимо (остаётся страховкой для внутренних вызовов `ProcessAlert`).
- **Группировка** (`internal/infrastructure/grouping/`) `endsAt` не читает вовсе — ни resolve по таймауту, ни продления (`grep EndsAt` пусто). Согласуется с Known Gap #12 в compat-доке.
- **Правило 7, флап** (`filter_engine.go:126-160`): ключ `fingerprint|status`, при блокировке `seenAt` не обновляется. Сценарий:
  - firing в t0 — проходит;
  - resolved в t0+20s — проходит, другой ключ;
  - снова firing в t0+40s — **блокируется**: ключ `|firing` виден 40 с назад.

  Ресивер получил «resolved», а алерт горит. У Prometheus потеря длится до следующего resend (`resend_delay` 1m). Отправитель, который шлёт событие один раз (webhook-интеграции), теряет его насовсем.
- **Правило 7, direct publish**: без `route:` (или при `grouping.enabled: false`) каждый `Updated` уходит в `PublishToReceiver` без nflog. Два Prometheus в HA-паре шлют один алерт с разными `endsAt` ⇒ без правила 7 — два уведомления (`assumed`: в `publishing/` дедуп уведомлений есть только у группового пути, `coordinator.go`/`queue.go` в spec проверить).
- **Конфиг**: секции `Config` с viper-дефолтами (`internal/config/config.go:45`, `:931`). Чарт отдаёт настройки env'ами из `templates/configmap.yaml` (`GROUPING_ENABLED` и т. п.). `filters: {}` в `helm/amp/values.yaml:801-805` ни одним шаблоном не читается; `values.schema.json` нет ⇒ удаление ключа не ломает `helm upgrade` у тех, кто его задал (лишний ключ игнорируется). BACKLOG P2 `DEAD-VALUES-FILTERS` закрывается этой задачей.
- **Тесты, завязанные на правила**: `filter_engine_test.go` (`test_alert` — строки 131-356, `disabled_namespace` — 542-558) и `filter_engine_dedup_test.go`. Смоуки (`deploy/smoke/run.sh`, `deploy/e2e-ha/run.sh`) имена на `test*` не используют.

### Upstream (Alertmanager v0.32.0)

- **`postAlertsHandler`** (`api/v2/api.go:328-398`): нормализует `StartsAt`/`EndsAt` (`EndsAt = now + resolve_timeout`, если не задан), валидирует, `api.alerts.Put`. **Никакой фильтрации по содержимому** — только валидация.
- **`Alert.Validate`** (`alert/alert.go:71-88`): нужны `StartsAt`, `EndsAt ≥ StartsAt`, хотя бы одна пара label'ов, валидные label'ы и аннотации. **`alertname` не обязателен.**
- **`provider/mem.Put`** (`provider/mem/mem.go:304-364`): повтор с тем же fingerprint и пересекающимся интервалом **сливается** через `Merge` (`alert/alert.go:41-67`: ранний `StartsAt`, поздний не-timeout `EndsAt`, свежие аннотации) и снова отдаётся подписчикам (dispatcher). Повтор не выбрасывается — он продлевает и обновляет алерт.
- **Dispatcher** кладёт в группу любой валидный алерт, в том числе resolved (`dispatch/dispatch.go:445`, `aggrGroup.insert:741`). Нужно ли уведомлять, решает notify pipeline по nflog: уведомление только о resolved без предшествующего firing не отправляется. Старый resolved-алерт в upstream хранится, но не уведомляет.
- Подавление «не слать» в upstream делается только конфигом пользователя: silences, `inhibit_rules`, `route` с `matchers` на receiver без интеграций (blackhole), `mute_time_intervals`.

### Constraints

- Ответ клиенту не меняется (2xx), формат API не меняется.
- Имена метрик `alert_history_filter_*` не трогать (правило Compatibility в requirements).
- Правила LLM (2, 3) и `GetMode` — вне задачи (`PROD-LLM-ALERT-PATH-ISOLATION`).

### Unknowns

- `assumed`: на direct-publish пути нет другого дедупа уведомлений, кроме правила 7. Проверить в spec по `publishing/coordinator.go`/`queue.go`. От этого зависит, нельзя ли удалить правило 7 целиком.

## 3) Options

`Generation:` single-pass — ошибка дешёвая и обратимая (код-only, без схемы и данных); решение по правилам 1 и 4 принял владелец.

### По правилам 1 (test) и 4 (namespace)

#### Option A — удалить (выбран владельцем)

- **Pros:** паритет с upstream; ноль нового конфига и контракта; меньше кода. Оператор воспроизводит старое поведение upstream-средствами — route с `matchers: [alertname=~"(?i)test.*"]` или `namespace=~"dev-sandbox|tmp"` на receiver без интеграций.
- **Cons:** кто полагался на дроп, увидит эти алерты после апгрейда (breaking, нужна migration note).
- **Cost/Risk:** low.

#### Option B — opt-in секция `filters:` (default off)

- **Pros:** старое поведение возвращается одной строкой конфига.
- **Cons:** новый AMP-специфичный контракт, который дублирует upstream-route; проводка в чарт, валидация, доки, reload-семантика; держит в живых `SimpleFilterEngine` как вторую систему подавления рядом с silences и inhibition.
- **Cost/Risk:** medium.

### По правилу 7 (повтор за 1 мин)

#### Option A — удалить целиком

- **Pros:** паритет с upstream, флап не теряется, точные дубли и так отсекает Step 0, групповой путь закрыт nflog.
- **Cons:** на direct-publish пути (без `route:`) HA-пара Prometheus даст двойные уведомления (`assumed`); без storage дедупа нет вовсе.
- **Cost/Risk:** medium (поведение ухудшается в legacy-режиме).

#### Option B — ключ по последнему статусу fingerprint'а (выбран)

- `fingerprint → (status, seenAt)`; блок, только если статус тот же и `seenAt` моложе окна; при смене статуса запись перезаписывается. Последовательность firing → resolved → firing в окне проходит целиком.
- **Pros:** закрывает реальную потерю (флап); сохраняет защиту direct-publish пути от двойной отправки; минимальный дифф, существующие тесты дедупа почти без изменений.
- **Cons:** повтор с новыми аннотациями в окне по-прежнему не обновит копию в группе (в upstream обновил бы); на групповом пути правило избыточно.
- **Cost/Risk:** low.

#### Option C — правило 7 только на direct-publish пути

- **Pros:** на групповом пути повтор доходит до группы, как в upstream.
- **Cons:** движок фильтра расщепляется на две точки вызова, растёт дифф; выигрыш — только свежесть аннотаций в группе в пределах минуты.
- **Cost/Risk:** medium.

### По правилам 5 (пустой `alertname`) и 6 (старый resolved)

- **Оставить как есть.** Правило 5 недостижимо с API (400 раньше), правило 6 по исходу совпадает с upstream (resolved без firing не уведомляет). Удаление ничего не даёт пользователю, но расширяет дифф. Требование «каждый дроп виден» выполняется общей метрикой и логом.

## 4) Decision

- **Правила 1 и 4 — удалить** (Option A, решение владельца 2026-10-13). Причины:
  - upstream не фильтрует по содержимому, а нужное поведение даёт штатный route на blackhole-receiver;
  - новый контракт `filters:` дублировал бы routing и держал вторую систему подавления;
  - сигнал `C` уходит из Risk Profile.
- **Правило 7 — ключ по последнему статусу** (Option B). Закрывает потерю алерта при флапе, не открывает двойную отправку на direct-publish пути. Удаление целиком (A) — только после проверки `assumed`-премисы; если она не подтвердится в spec, выбор пересмотреть там же.
- **Правила 5, 6 и LLM-правила 2, 3 не трогать.**
- **Наблюдаемость:**
  - метрика остаётся `alert_history_filter_blocked_total{reason}` — уже пишется, имя не ломаем, `amp_alerts_filtered_total` из BACKLOG не вводим;
  - лог дропа — INFO с `fingerprint` и `reason`;
  - мёртвые `// TODO: Record filter metrics` убрать.
- **`filters:` в `helm/amp/values.yaml` удалить** — закрывает BACKLOG P2 `DEAD-VALUES-FILTERS`.
- **Risk Profile** пересматривается: `X R` → tier **Standard** (было `C X R`, Full). `deep-review` — по усмотрению; если дифф перевалит за ~200 строк, обязателен (`docs/solo-kanban/workflow.md` § Tiers).

## 5) Spec Inputs

- **API/contracts:** HTTP API не меняется. Breaking по поведению: алерты `test*`, `environment=test|testing`, namespace `dev-sandbox`/`tmp` теперь доставляются. Значения `reason` `test_alert` и `disabled_namespace` исчезают из `alert_history_filter_blocked_total`. Ключ values `filters:` удаляется (без schema — не ломает установку).
- **Data model/migrations:** не применимо.
- **Rollout/rollback:**
  - `CHANGELOG.md` `[Unreleased]`: Fixed + breaking/migration note с готовым фрагментом `route:` + `receivers: [{name: blackhole}]`, который возвращает старый дроп.
  - Откат — предыдущий образ, без данных.
- **Observability:**
  - существующая метрика; лог дропа с fingerprint;
  - в compat-доке — строка про оставшиеся правила фильтра (5, 6, 7, LLM 2/3) в Known Gaps: дропнутый алерт виден в `GET /api/v2/alerts`, но не уведомляется.
- **Security/ownership/RBAC:** не применимо.
- **Для spec проверить:**
  1. `assumed` — дедуп уведомлений на direct-publish пути (`publishing/coordinator.go`, `queue.go`).
  2. Тест-план:
     - флап firing → resolved → firing в окне проходит;
     - `TestRunnerDown`, `environment=testing`, namespace `tmp` доходят до ресивера через `AlertProcessor` в transparent и enriched (с fake LLM);
     - повтор того же статуса в окне по-прежнему блокируется;
     - render-тест чарта без `filters`.
  3. Доки: `CONFIGURATION_GUIDE.md`, `MIGRATION_QUICK_START.md` — упоминания тестовых алертов и namespace'ов, если есть.

## 6) References

- Files:
  - `go-app/internal/core/services/filter_engine.go`
  - `go-app/internal/core/services/alert_processor.go:516-800`
  - `go-app/internal/core/services/deduplication.go:204-405`
  - `go-app/internal/application/handlers/alerts.go:404-540`
  - `go-app/internal/infrastructure/webhook/parser.go:109-115`
  - `go-app/internal/application/service_registry.go:1222-1272`
  - `go-app/pkg/metrics/metrics.go:1522-1565`
  - `helm/amp/values.yaml:801-805`
- Upstream (v0.32.0): `api/v2/api.go:328-398`, `alert/alert.go:41-88`, `provider/mem/mem.go:304-364`, `dispatch/dispatch.go:445`, `:741`.
- Docs: `docs/ALERTMANAGER_COMPATIBILITY.md` § Known Gaps (#12 — resolve_timeout), `docs/06-planning/BUGS.md` `HARDCODED-FILTER-DROPS-ALERTS`, `BACKLOG.md` `DEAD-VALUES-FILTERS`, `archive/DONE-2026-08.md` `DEDUP-STATE-STUB`.
