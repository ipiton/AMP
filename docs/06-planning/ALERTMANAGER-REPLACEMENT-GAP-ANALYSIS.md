# Анализ: может ли AMP заменить Alertmanager?

**Дата**: 2026-08-18 (предыдущая версия: 2026-03-08 — устарела, ниже полностью переписан статус)
**Статус**: После ветки `feat/alertmanager-parity` AMP реализует основную механику upstream Alertmanager
(routing tree, grouping/dispatch, notify chain, HA-clustering), а не только совместимую по форме API-поверхность.
Честная формулировка теперь: **AMP — сильный parity-кандидат с коротким списком явных, отслеживаемых пробелов**,
а не controlled replacement slice. Финальное подтверждение — вторая половина задачи 7.4 (`amtool` live-audit),
которая идёт отдельно и не входит в этот docs-пасс.

## Что закрыто этой веткой

Всё ниже проверено против кода на branch `feat/alertmanager-parity` (base `ff6accc`), не принято на слово:

- **Routing tree** (`internal/business/routing/`): рекурсивный matcher (`matcher.go` `RouteMatcher.FindMatchingRoutes`
  — не `evaluator.go`, который его лишь оборачивает; дети имеют приоритет
  над родителем, `continue: true` — multi-match), anchored regex (`^(?:re)$`, `anchorRegex`), все 4 оператора
  матчера (`=`, `!=`, `=~`, `!~`), `matchers:` list syntax наравне с legacy `match`/`match_re`. `route:`/`receivers:`
  парсятся через `infrastructure/routing.Parse()` (гейт: top-level `route:` секция). `RouteEvaluator` подключён в
  `AlertProcessor.evaluateRoute` на каждый alert при ingest (nil в lite/legacy режиме без `route:`).
  Receiver-scoped targets через аннотацию/лейбл `amp.receiver` (`internal/business/publishing/discovery_parse.go`)
  — это AMP-нативный механизм, не апстримный, но функционально эквивалентный.
- **Dispatcher/grouping** (`internal/infrastructure/grouping/`): группировка по маршрутному `group_by`,
  тайминги (`group_wait`/`group_interval`/`repeat_interval`) берутся из `RoutingDecision` конкретного маршрута, а
  не только из глобальных default. Notify-chain на send-time (`manager_impl.go` `publishGroupAlerts`), порядок
  Inhibit → Silence → TimeMute → Dedup, задокументирован явно как соответствующий апстриму. Одна логическая
  нотификация на группу на срабатывание (`PublishGroup` вызывается один раз) — но см. пробел #2 ниже про
  wire-level batching.
- **time_intervals / mute_time_intervals / active_time_intervals**: `internal/infrastructure/routing/timeinterval/`
  проверен against upstream'овский fixture corpus (`upstream_fixtures_test.go`), route-level имена резолвятся
  send-time через `routeTreeTimeIntervalLookup` (подавление целой группы, не отдельных алертов).
- **API parity**: `GET /api/v2/alerts` — полный набор апстримных query-параметров
  (`active`/`silenced`/`inhibited`/`unprocessed`/`receiver`/`filter`) + legacy `status=`/`resolved=` как алиасы;
  `POST /api/v1/alerts` алиасится на v2 ingest; `GET /api/v2/status` отдаёт вложенные `versionInfo` (из ldflags) и
  `cluster` (Redis heartbeat); `--web.route-prefix` наследует путь из `external_url`, когда не задан явно
  (`internal/application/route_prefix.go`).
- **Config validation**: `pkg/configvalidator` подключён в `internal/config.LoadConfig`, используется и при старте
  процесса, и на `/-/reload` (тот же код-путь). Работает только для конфигов с top-level `route:` секцией — legacy
  single-receiver конфиги эту проверку пропускают, как и раньше.
- **HA clustering**: Redis-backed nflog + send-claim для межреплик дедупа нотификаций (task 6.1); distributed
  timer liveness reconciliation с targeted overdue-scan и очисткой orphaned timers (task 6.2, коммиты `84df74f`,
  `dec49e7`); silence cache invalidation через Redis pub/sub (task 6.3); leader-elected GC для silence-очистки
  (task 6.4, `internal/infrastructure/lock/election.go`); peer heartbeat + поле `cluster` в `/api/v2/status`
  (task 6.5). 2-реплика e2e (exactly-once delivery + failover при потере реплики) воспроизведена в
  `deploy/e2e-ha/run.sh` (коммит `ff6accc`) — рабочий standalone-скрипт, **не в CI-гейте**.
- **Receivers**: telegram теперь нативно поддержан (`internal/infrastructure/publishing/telegram_*.go`, глобальный
  rate-limiter 30 msg/s + retry/backoff). Остальная матрица приёмников — см. `docs/ALERTMANAGER_COMPATIBILITY.md`.
- **Helm/Docker**: `Dockerfile` — `HEALTHCHECK` на `:8080/healthz`, `-ldflags` прокидывает версию/build info,
  `EXPOSE 8080`; `helm/amp/values.yaml` — canonical image `ghcr.io/ipiton/amp`.

## Что осталось (известные, отслеживаемые пробелы)

Ничего из списка ниже не скрыто — каждый пункт имеет код-адрес и, где применимо, backlog-ссылку.

1. **`receivers[].*_configs` не создают delivery-эндпоинты — control plane != data plane.** Главное расхождение
   с апстримом, и это дизайн, а не баг: ни один код-путь не строит `PublishingTarget` из `routing.Receiver`
   (проверено grep'ом по всему дереву). Таргеты обнаруживаются исключительно из Kubernetes Secrets, скоупленных
   аннотацией/лейблом `amp.receiver` (`internal/business/publishing/discovery_parse.go`). Блоки `*_configs`
   парсятся и валидируются, но **не провижнятся**. Перенос апстримного `alertmanager.yml` даёт корректный routing
   и **ноль доставок** до создания соответствующих Secret'ов — без ошибки, без warning'а, со чистым стартом.
   Оператор обязан создать по одному Secret на эндпоинт; это отдельный шаг миграции, задокументированный в
   `docs/MIGRATION_COMPARISON.md` (шаг 3 rollout) и `docs/ALERTMANAGER_COMPATIBILITY.md`
   («control plane vs data plane»). Построение таргетов напрямую из `receivers:` — будущий эпик, не отгруженная
   фича.
   *(Раньше пунктом #1 здесь был «groups-эндпоинт присваивает hardcoded receiver» — этот пробел закрыт: labels
   берутся из `group_by` сматчившегося маршрута, receiver резолвится на группу из живого route tree
   (`alertGroupingResolver`). Остаётся только AMP-специфичный `?group_by=` override.)*
2. **Wire-level webhook batching**: `PublishingCoordinator.PublishGroupToTargets` фанит одну группу в один HTTP
   запрос на пару `(target × alert)`, а не в один POST с JSON-массивом `alerts` на target, как у апстрима.
   Функционально каждый алерт доставляется, но количество и форма запросов отличаются — вопрос для интеграций,
   которые парсят конкретную форму payload.
3. **OpsGenie / VictorOps / WeChat** — конфиг валидируется (E126-E141), но нет runtime publisher — ноль
   нотификаций. Осознанно отложено «по потребности» (задача 7.2).
4. **Pushover / AWS SNS / Webex** — нет поддержки ни на одном уровне (config/template/publisher). Discord/Teams
   закрыты через generic webhook-шаблоны, это не блокер.
5. **Config write API (`POST/PUT /api/v2/config*`)** и **`/history*`** — не реализованы, явно вне scope задачи 7.4.
6. **Per-chat Telegram rate-limit** — сейчас только глобальный лимитер (30 msg/s), без учёта отдельных chat_id.
   Отложено.
7. **Cross-replica DB migration advisory lock** — при одновременном старте нескольких реплик миграции не
   координируются advisory-локом. Отложено.
8. **Reloadable-sidecar** (`CONFIG-RELOADER-SIDECAR`) — K8s sidecar для ConfigMap-driven SIGHUP. После Ф5 ценность
   ниже (`/-/reload` уже покрывает основной кейс); backlog.
9. **`inhibited` query-параметр на `/api/v2/alerts`** структурно реализован, но пока no-op — inhibition state ещё
   не протянут в `alertconv.ToGettableAlert`'s `InhibitedBy`. Сам notify-chain Inhibit-step на это не влияет — там
   инхибиция реально работает.
10. **Repeat/group-interval continuation под restart реплики** — `PARITY-A1-NOTIFICATION-TRIGGERING` закрыт,
    task 6.2 добавил reconciliation loop специально для восстановления таймеров после падения/рестарта. В
    git log этой ветки не найден отдельный коммит, оформленный как фикс «timer stops after first fire»
    (context-cancellation-shaped P0) — если такой баг репортился отдельно, идентифицировать его в истории не
    удалось на момент написания. Считать continuation реализованным и укреплённым, но требующим эмпирической
    проверки (`amtool` + ожидание за `group_interval`, наблюдение повторной нотификации) во время live-audit
    половины задачи 7.4 — не как окончательно закрытый цикл с long-duration регрессионным тестом.

## Как это было проверено

Ничего из раздела «закрыто» не принято на слово из брифа задачи — каждый пункт сверен с кодом на этой ветке:
`grep`/`find` по соответствующим пакетам, чтение ключевых файлов (`evaluator.go`, `manager_impl.go`,
`route_evaluator.go`, `alerts.go`, `coordinator.go`, `status_api.go`, `route_prefix.go`, `heartbeat.go`,
`election.go`, `Dockerfile`, `values.yaml`), проверка `git log` на заявленные фикс-коммиты.

Дополнено после final fix wave (whole-branch review): исправлены и покрыты тестами silent-loss баги, которых этот
документ раньше не знал — routing-only reload молча отбрасывался (`/-/reload` отвечал 200 OK), SIGHUP-хендлера не
существовало вопреки docs, окно adoption у reconciliation было ~0s, три early-return в `onTimerExpired` навсегда
заклинивали группу, metrics-only publisher писал в общий nflog и глушил здоровые реплики, resolved-группа
нотифицировала бесконечно, `EnhancedTelegramPublisher` был недостижим в рантайме, а `config.original` на
`/api/v2/status` отдавал сырой конфиг с паролями. Детали — `.superpowers/sdd/alertmanager-parity/`.

## Решение по позиционированию

- **Alertmanager parity core is real**: routing/grouping/silences/inhibition/time-windows/HA — не заглушки, а
  рабочая механика с честными, локализованными пробелами.
- **НЕ config-level drop-in — и это постоянное свойство, а не временный пробел.** Control plane
  (routing/grouping/timing/inhibition) — parity-level; data plane принципиально другой: доставка идёт через
  `amp.receiver`-скоупленные Kubernetes Secrets, а не через `receivers[].*_configs` (пробел #1). Любая внешняя
  формулировка обязана называть это явно: миграция конфига без создания Secret'ов даёт тихий ноль доставок.
- Также не пройден финальный `amtool`/Grafana live-audit (вторая половина задачи 7.4), и остаётся пробел #2
  (webhook wire shape).
- **Формулировка для внешних docs**: "AMP реализует ключевую механику Alertmanager (routing, grouping, dispatch,
  silences, inhibition, time-based muting, HA) с коротким списком задокументированных пробелов; delivery-таргеты
  провижнятся отдельно, через `amp.receiver`-скоупленные Kubernetes Secrets, а не из `receivers[].*_configs`" —
  никогда не `drop-in replacement` без этой оговорки.

## Что считать закрытием темы

Тезис "AMP может заменить Alertmanager" будет подтверждён, когда:

- пройдёт финальный `amtool`/Grafana smoke (вторая половина задачи 7.4): `alert add`/`silence add`/`config show`
  против AMP, Grafana Alertmanager datasource smoke
- пробел #1 (`receivers:` не провижнит доставку) явно принят как постоянное архитектурное ограничение во всех
  внешних docs — либо закрыт отдельным эпиком, строящим `PublishingTarget` из `routing.Receiver`
- пробел #2 (webhook batching) либо закрыт, либо явно принят как постоянное ограничение
- 2-реплика e2e (`deploy/e2e-ha/`) либо остаётся подтверждённым manual runbook, либо переведён в CI-гейт

До этого — позиционирование ограничено формулировкой выше, без безоговорочного `drop-in replacement` claim.
