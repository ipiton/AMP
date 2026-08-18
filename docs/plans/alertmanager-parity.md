# AMP: путь до полноценной замены Alertmanager (drop-in)

## Context

Репо `~/Documents/Helpfull/AMP`, main после debt-closure sweep (lint 0, 42 пакета тестов зелёные). Текущее состояние — «controlled replacement»: single receiver, фан-аут на все enabled publishing-targets, публикация прямо на ingest. Цель — drop-in замена Alertmanager **включая HA/clustering** (решение пользователя). CI — вне scope (решение пользователя). Всё локальными коммитами, без push без команды.

Разведка показала:
- `internal/business/routing` (~3.5k строк) — dead code с 0 тестов и багами: `FindMatchingRoutes` не обходит дерево (root всегда побеждает), регексы не анкорены (`^(?:...)$` отсутствует — substring match), `!=`/`!~` недостижимы; 3 несвязанных Route-типа без конвертеров.
- `internal/infrastructure/grouping` (managers, timers, Redis/memory storage, restore) — написан на 100%, не подключён; PARITY-A1 закрыл только timer-коллбэки.
- Upstream-семантика: публикация ТОЛЬКО по таймерам dispatcher'а (group_wait → group_interval → repeat_interval), silence/inhibit/mute проверяются в notify-stage, не на ingest.
- `pkg/configvalidator` — 6 no-op заглушек, любой конфиг «валиден»; рабочие референсы есть (routing/parser.go, inhibition/parser.go, update_validator.go).
- API-гэпы: alerts query params не upstream (`active/silenced/inhibited/receiver` отсутствуют), `/api/v2/status` — плоский формат, нет `cluster`, версия захардкожена; `POST /api/v1/alerts` → 404; `mute_time_intervals` — 0 упоминаний в репо.
- Clustering: решение уже принято в доках — Redis-based (nflog в Redis, pub/sub состояния, leader election через существующий distributed lock).

## Фаза 1 — Routing tree end-to-end (~5–6 дней)

Фундамент всего остального; порядок из разведки:

### Task 1.1 Починить семантику матчинга
(`internal/business/routing/matcher.go:293`): рекурсивный `matchRoute` вместо `Walk`+global-stop (родитель обязан сматчиться для спуска; сматчился ребёнок → родитель не возвращается; `continue: true` — в альтернативы). Анкоринг регексов `^(?:...)$` (`matcher.go:233`). `IsNegative` в `parseMatchers` + синтаксис `matchers:` списков. Первые тесты пакета: fixture-тесты «config из доков Alertmanager → ожидаемый receiver».

### Task 1.2 Дедуп типов
Удалить локальные `Route/Receiver/*Config` из `tree_builder.go:388-429`, TreeBuilder работает от `infrastructure/routing.RouteConfig` (`grouping.Route`).

### Task 1.3 Конфиг
Секция `route:` + реальные `receivers:` в `internal/config/config.go` через существующий `infrastructure/routing.Parse()`; `config.yaml.example`.

### Task 1.4 Wiring
`initializeRouting` в `service_registry.go` (образец — `initializeInhibition:638`), `RouteEvaluator` в `AlertProcessor`, `TreeManager` для hot-reload (reload_coordinator уже ставит `affected["routing"]`).

### Task 1.5 Receiver → publishing targets
Механика — label `amp.receiver: "<name1>,<name2>"` на target-Secret + fallback `target.Name == receiver.Name`; фильтрация в `PublishingCoordinator.PublishToTargets` (`coordinator.go:157` — готовый sink). Target без receiver-метки = принадлежит всем (обратная совместимость).

## Фаза 2 — Dispatcher: grouping + notify-pipeline (~4–5 дней)

Перенос публикации с ingest на таймеры (upstream-семантика):

### Task 2.1 Silencing workers wiring
Готовый `DefaultSilenceManager`: `initializeSilenceManager` в registry (skip при lite/nil repo), `Start/Stop` в lifecycle; менеджер как GC/stats-воркер, `memory.SilenceStore` остаётся read-path API.

### Task 2.2 Grouping wiring
`SetGroupManager` (снять цикл GroupManager↔TimerManager), секция `grouping:` в config + адаптер, `initializeGrouping` (storage по профилю: Redis standard / memory lite), `RestoreTimers` на старте, `Shutdown` в teardown.

### Task 2.3 Pipeline-переключение под флагом `grouping.enabled`
`alert_processor.go:274/296` — вместо прямого `PublishToAll` → `keyGen.GenerateKey(RoutingDecision.GroupBy)` + `AddAlertToGroup`; `ResetTimer` при добавлении в существующую группу. Взаимоисключение путей (иначе двойная нотификация).

### Task 2.4 Notify-stage цепочка в `publishGroupAlerts`
Inhibit → Silence (проверка на момент отправки, не на ingest) → Dedup → publish через `PublishToTargets(receiver из RoutingDecision)`. Одна групповая нотификация вместо N одиночных (новый метод Publisher).

## Фаза 3 — mute_time_intervals / time_intervals (PARITY-B1, ~3–4 дня)

С готовой notify-цепочкой из Ф2 встраивается как TimeMute-шаг (upstream-порядок: Inhibit → Silence → **TimeMute** → Dedup):

### Task 3.1 Конфиг time_intervals
`time_intervals:` (times/weekdays/days_of_month/months/years/location) + `mute_time_intervals`/`active_time_intervals` в Route.

### Task 3.2 Evaluator + TimeMute-шаг
Интервалы в `RoutingDecision`; TimeMute-шаг в notify-цепочке.

### Task 3.3 Тесты по upstream-фикстурам
Таймзоны, границы интервалов.

## Фаза 4 — API parity (~3 дня)

### Task 4.1 GET /api/v2/alerts + /groups query params
Upstream query params `active`, `silenced`, `inhibited`, `unprocessed`, `receiver` (сейчас свои `status`/`resolved`) — с сохранением старых как алиасов. `handlers/alerts.go:55-61`.

### Task 4.2 /api/v2/status
Вложенный `config:{original}`, `versionInfo` через ldflags (`-X main.version=...` в Makefile/Dockerfile), `cluster`-поле — заглушка `status:"disabled"` до Ф6. `handlers/status_api.go:13-64`.

### Task 4.3 POST /api/v1/alerts
Реальный alias на v2-ingest вместо `NotFoundHandler` (`router.go:36`).

### Task 4.4 web.route-prefix
PARITY-B6.

## Фаза 5 — Config validation по-настоящему (~3 дня)

`pkg/configvalidator` из no-op в рабочий, приоритет structural+route+receiver:

### Task 5.1 structural.go/route.go
Секции, receiver-refs, matcher-синтаксис, длительности, циклы — переиспользовать `infrastructure/routing/parser.go` валидацию.

### Task 5.2 receiver.go
Уникальность имён, ≥1 integration, обязательные поля — референс `publishing/webhook_validator.go`.

### Task 5.3 inhibition.go/global.go/security.go
Референсы `inhibition/parser.go`, `update_validator.go`, `sanitizer.go`.

### Task 5.4 Wiring в /-/reload и стартовую загрузку
Невалидный конфиг → отказ reload с деталями (сейчас любой конфиг проходит).

(`pkg/templatevalidator` Phase 2–5 — вне scope, backlog.)

## Фаза 6 — Clustering / HA (PARITY-C1, ~10 дней)

Redis-based (решение из доков; gossip не тащим):

### Task 6.1 nflog в Redis
Дедуп нотификаций между репликами; ключ = groupKey+receiver, TTL = repeat_interval.

### Task 6.2 Распределённые таймеры
`RedisTimerStorage.AcquireLock` уже есть — exactly-once срабатывание на реплику-лидера группы.

### Task 6.3 Silence/alert-state sync
Уже DB-first (Postgres — общий), кэши по репликам — инвалидация через Redis pub/sub.

### Task 6.4 Leader election
Для GC/sync-воркеров через существующий `internal/infrastructure/lock`.

### Task 6.5 cluster в /api/v2/status + e2e
Peers из Redis heartbeat, e2e-тест на 2 инстансах (docker compose).

## Фаза 7 — Receivers + финализация (~3–4 дня)

### Task 7.1 Telegram publisher (PARITY-B3)
Конфиг + publisher + тесты по образцу slack.

### Task 7.2 VictorOps/WeChat + compat-matrix
Конфиги уже есть в `internal/alertmanager/config` — publishers по потребности; Pushover/SNS/Webex — зафиксировать как unsupported в compat-matrix (не блокер: Discord/Teams работают через webhook).

### Task 7.3 Helm/Docker
Реальный image-name/tag в `values.yaml:20`, `HEALTHCHECK` в Dockerfile, version ldflags.

### Task 7.4 Финальный parity-аудит
amtool против AMP (status/alerts/silences/config-check), обновить `ALERTMANAGER_COMPATIBILITY.md`/`MIGRATION_COMPARISON.md`, gap-analysis (устарел с 2026-03).

## Вне scope (зафиксировано)

CI (решение пользователя), `pkg/templatevalidator` Phase 2–5, config API (`/api/v2/config*` write), `/history*` API, PARITY-B2 OpsGenie (EOL), reloadable-sidecar (PARITY-B4 — после Ф5 ценность падает, backlog).

## Порядок и оценка

Ф1 → Ф2 → Ф3 (зависят последовательно); Ф4, Ф5 — параллелятся с Ф2/Ф3; Ф6 после Ф2; Ф7 последняя. Итого ~31–35 оценочных дней; с параллельными агентами ожидаемо сильно быстрее (debt-sweep: 2.5 нед → 6 ч).

## Верификация (Global Constraints)

- Каждая фаза: `go build ./... && golangci-lint run ./... && go test ./... -count=1` + `go test ./cmd/server -tags=futureparity` (в каталоге go-app).
- Ф1–Ф3: fixture-тесты против upstream-семантики (конфиги из документации Alertmanager → ожидаемый receiver/тайминги).
- Ф2: интеграционный тест «ingest → group_wait → одна нотификация», «повтор в группе → reset group_interval», «silence после ingest подавляет notify».
- Ф6: docker compose с 2 репликами + Redis + Postgres: нотификация ровно одна, failover лидера.
- Финал: amtool `alert add`/`silence add`/`config show` против AMP; Grafana Alertmanager datasource smoke.
- Workflow: ветка `feat/alertmanager-parity`, коммит на слайс, статусы в NEXT/BACKLOG/DONE, без push. Никаких `Co-Authored-By: Claude` / "Generated with Claude Code" в коммитах.
