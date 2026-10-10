---
id: PROD-GROUPING-DEFAULT
slug: prod-grouping-default
stream: Parity / Grouping
type: bug
status: draft
created_at: 2026-10-10
updated_at: 2026-10-10
based_on:
  - requirements.md
  - research.md
---

# Specification: группировка включена по умолчанию

**Version:** 1.3 (2026-10-10: после deep-review R3 — dedup как этап на получателя, удаление покрытых resolved, память собственных отправок вынесена в обёртку над Redis-nflog; решение владельца «полноценное решение корня»). История: 1.2 (2026-10-10: после deep-review R2 — dedup по подмножеству, самовосстановление таймера, локальная память отправок; решение владельца «вариант 2» повторно). История: 1.1 (2026-10-10: после deep-review R1 — в scope добавлена цепочка `group_interval`, решение владельца «вариант 2»; исправлены Premises 5, 8, Invariants, Rollout / Rollback)
**Status:** Draft

## Summary

`grouping.enabled` по умолчанию становится `true` в коде и в чарте, чтобы скопированный `alertmanager.yml` с `route:` группировал алерты как upstream. Без `route:` поведение не меняется, а явное выключение при наличии `route:` сопровождается предупреждением на старте. Чтобы «как upstream» было правдой, цепочка таймеров группы переводится на постоянный flush каждые `group_interval` (v1.1), а notify-chain — на правило upstream «слать, если есть что-то новое» и на устойчивость к потере таймера и ошибкам nflog (v1.2).

## Requirements Coverage

| Requirement / Criterion | Covered by |
|---|---|
| `route:` без секции `grouping:` включает группировку | Target Design п.1; тест дефолта в `internal/config`; тест `initializeGrouping` на конфиге из `LoadConfig` |
| Без `route:` — старт как раньше, без ошибки, degraded и ложного WARN | Target Design п.2; Invariants 1; тест на эффективный флаг |
| Явный `false` + `route:` выключает группировку и даёт один WARN на старте | Target Design п.3; тест на лог |
| Поведение по профилям зафиксировано | `research.md` Q2–Q3; Design Premises 3–5; `evidence/smoke-lite-grouping.md` |
| Чарт согласован с кодом, render-тест | Target Design п.4; `helm/amp/tests/render-grouping-default.sh` |
| Доки отражают новое поведение | Target Design п.5; Component Architecture |
| CHANGELOG: Changed + migration note | Impact Analysis; Rollout / Rollback |
| Новый алерт существующей группы уходит не позже `group_interval` (v1.1, review F1) | Target Design п.6; Premises 11–13; тесты цепочки в `write-tests` |
| Постоянный flush не даёт лишних нотификаций и не оставляет группу без таймера (v1.2, review G1–G3) | Target Design п.8–10; Premises 14–16; `evidence/group-interval-chain-probes.md` |
| Гейты, `deep-review` | Deep Review; `testing` по `WORKFLOW.md` § Гейты AMP |
| BACKLOG / NEXT | `finalize` |

## Current State

- **Code:**
  - `go-app/internal/config/config.go` (`setDefaults`) — `viper.SetDefault("grouping.enabled", false)`; doc-комментарий `GroupingConfig.Enabled` (`:110-115`) описывает состояние task 2.2 («ingest pipeline does not consult the grouping subsystem yet»).
  - `go-app/internal/application/service_registry.go` — `initializeGrouping`: при `!Enabled` INFO и выход; без `route:` INFO и выход.
  - `service_registry.go` (сборка `AlertProcessorConfig`) — `GroupingEnabled: r.config.Grouping.Enabled` передаётся в `AlertProcessor` без учёта `route:`.
  - `go-app/internal/core/services/alert_processor.go` — `warnGroupingFallback`: WARN, когда флаг включён, а группировать нечем.
  - `helm/amp/values.yaml:661` — `grouping.enabled: false`; `templates/configmap.yaml:63` всегда передаёт `GROUPING_ENABLED`.
- **Data:** не применимо.
- **Tests:** `internal/config/grouping_adapter_test.go` (`TestLoadConfig_GroupingDefaults` ждёт `false`), `internal/application/service_registry_grouping_test.go` (`TestInitializeGrouping_DisabledByDefault` и соседи строят `Config` руками), `internal/core/services/alert_processor_test.go` (флаг задаётся явно). Render-тесты чарта — `helm/amp/tests/render-*.sh`, запускаются шагом `helm-tests` release-gate.
- **Docs:** `docs/ALERTMANAGER_COMPATIBILITY.md` Known Gap #13 (`:855`) и шаг миграции (`:973`), `docs/MIGRATION_QUICK_START.md:24`, `README.md:17`, `helm/amp/README.md` (`:23`, `:53`, `:68`), комментарии в `helm/amp/values.yaml:656-659`, `values-production.yaml:251`, `deploy/smoke/config.yaml:44`, `deploy/e2e-ha/config.yaml:80`.

## Design Premises

| Premise | Confirmed by | Class | If wrong |
|---|---|---|---|
| Флаг читают только два места: `initializeGrouping` и проводка `AlertProcessor` | grep `Grouping\.Enabled\|grouping\.enabled\|GROUPING_ENABLED` по всему репозиторию (go, sh, yaml, tpl): вне тестов и комментариев — `service_registry.go` (`initializeGrouping`, сборка `AlertProcessorConfig`), `config.go` (`setDefaults`), `configmap.yaml:63` | call-path-traced | третий потребитель увидит `true` без `route:` и поведёт себя иначе |
| Без `route:` подсистема не поднимается и ничего не деградирует | `service_registry.go`, `initializeGrouping` (`ErrGroupingRequiresRouteTree` → INFO, `return nil`) | code-read | установки без `route:` получат degraded или ошибку старта |
| В `lite` группировка работает целиком: алерт уходит после `group_wait` | `./deploy/smoke/run.sh` 2026-10-10: `profile: lite`, ALL PASS — `evidence/smoke-lite-grouping.md` (снято на `21dde05` с явным `grouping.enabled: true`, до смены цепочки и dedup; повтор на HEAD с дефолтом — `/testing` 5.3) | measured at `21dde05`, не повторено на HEAD | `lite`-установки с `route:` перестанут доставлять |
| В `standard` без Redis включённая группировка не добавляет degraded-причину сверх той, что уже ставит кэш | `service_registry.go`: `initializeCache` (degraded-причина кэша), `newGroupingStorage` и `newNotifyLog` (только WARN) | code-read | здоровая по `/health` установка станет degraded после апгрейда |
| `validateGrouping` не зависит от `Enabled`; второй гейт — `validateNotifyTimingBudget` — зависит: проверка `reconciliation_grace > claim TTL` выполняется только при поднятом timer manager | `config.go:1194-1205`; `publishing_runtime.go:498-526` (review F4) | code-read | `standard` + Redis + `route:` с явным `grouping.reconciliation_grace` ≤ claim TTL после апгрейда не стартует — учтено в migration note п.7; без явного grace значения согласованы |
| От дефолта `false` зависит один тест в `config`, `application`, `core`, `cmd` | прогон с перевёрнутым дефолтом — `evidence/default-flip-test-run.md` | measured | неожиданные падения на `/testing`; остальной модуль закрывает release-gate |
| Чарт перекрывает дефолт кода: `GROUPING_ENABLED` передаётся всегда | `templates/configmap.yaml:63`; `tasks/archive/PROD-CONFIG-FILE-FALLBACK/evidence/chart-default-env.txt:44` | code-read | правка только кода не дойдёт до Helm-установок |
| Флаг читается только на старте | grep `Grouping` по `reload_sections.go`, `reloadable_*.go`, `service_registry_reload.go` — пусто. `reload_coordinator.go:649`, `:706` называет `grouping` компонентом, но подсистему не пересоздаёт и «restart required» для `grouping.*` не выдаёт (review F11, defer-tech-debt) | call-path-traced | WARN на старте не покроет включение/выключение через reload |
| Без таймингов в `route:` действуют upstream-дефолты 30s / 5m / 4h | `internal/infrastructure/grouping/config.go:158-164` | code-read | migration note назовёт неверную задержку |
| Dedup в `publishGroupAlerts` уже решает «слать или нет» на каждом flush: набор, покрытый последней отправкой получателю в пределах `repeat_interval`, пропускается; набор с новым алертом или новым статусом — отправляется | `manager_impl.go` Step 4b (`alertSetSignature` по набору получателя, `IsDuplicate` с `ttl = now − repeat_interval`); `evidence/group-interval-chain-probes.md` — одноразовые прогоны на in-memory реализациях, без Redis и без реального `PublishingCoordinator` | measured (in-memory, прогоны вне дерева) | постоянный тик `group_interval` дал бы нотификацию каждые 5m по неизменной группе |
| Таймеры группы ставятся только в `manager_impl.go` (до правки — три вызова `StartTimer`, после — два: `group_wait` и `group_interval`); `AddAlertToGroup` для существующей группы таймер не перезапускает (ставит `group_wait` только при его отсутствии — п.9) | grep `StartTimer\|ResetTimer` по `internal/` вне тестов | call-path-traced | остался бы путь, возвращающий группу на `repeat_interval` |
| Таймеры `repeat_interval`, сохранённые в Redis версией до апгрейда, должны сработать и перейти на `group_interval` | `onRepeatIntervalExpired` оставлен, после flush ставит `group_interval`; `TimerType.IsValid` принимает тип | code-read | группы, созданные до апгрейда, останутся на старой цепочке или не загрузятся |
| Сигнатура в nflog хранится открытым списком `fingerprint:status` через `\|` в обеих реализациях, поэтому сравнение по подмножеству не требует смены формата записи и совместимо с записями прежней версии | `dedup.go` (`alertSetSignature`, `dedupEntry.signature`), `redis_notify_log.go` (`notifyLogEntry.Signature`, JSON), `nflog_snapshot.go` | code-read | старые записи в Redis после апгрейда читались бы неверно — дубли или пропуски на `repeat_interval` |
| У `GroupTimerManager` одна реализация; новый метод интерфейса `HasTimer` не ломает сторонних реализаций | grep `) GetTimer(` по `go-app` — только `DefaultTimerManager`; `go vet ./...` зелёный | call-path-traced | не соберутся моки/альтернативные реализации |
| Запись таймера в storage существует всё время, пока callback выполняется (удаляется только в хвосте `onTimerExpired`, если callback не перевзвёл), поэтому `HasTimer` на другой реплике во время fire не даёт ложного «таймера нет» | `timer_manager_impl.go` — хвост `onTimerExpired` (`continuationTookOver`) | code-read | приём алерта на не-владельце во время fire перезапускал бы `group_wait`; `StartTimer` из callback'а затем заменил бы его — один таймер, но лишний flush |
| Операторы существующих установок с `route:` без явного ключа прочитают migration note до апгрейда | не проверяемо | assumed | нотификации неожиданно задержатся на `group_wait` — см. Rollout / Rollback |

## Target Design

1. **Дефолт кода.** `viper.SetDefault("grouping.enabled", true)`. Doc-комментарий `GroupingConfig.Enabled` переписывается: включено по умолчанию, действует только при наличии `route:`, читается на старте.
2. **Эффективный флаг.** В `ServiceRegistry` — в месте сборки `AlertProcessorConfig` (`:2320`): `GroupingEnabled: r.config.Grouping.Enabled && r.config.HasRouteTree()` (выражение записано прямо в поле). Отдельный метод или поле не вводятся. Смысл `warnGroupingFallback` сохраняется для случая «`route:` есть, включено, подсистема не поднялась или у алерта нет routing decision».
3. **WARN при явном выключении.** В `initializeGrouping`, ветка `!Enabled`: если `HasRouteTree()` — `Warn` «Grouping is DISABLED (grouping.enabled=false) but a route: tree is configured: group_by/group_wait/group_interval/repeat_interval are ignored and every alert is published immediately»; иначе прежний INFO. Один раз на старт.
4. **Чарт.** `helm/amp/values.yaml`: `grouping.enabled: true`, комментарий переписан (включено по умолчанию; `lite` — in-memory, `standard` — Redis). `values-production.yaml`, `values-dev.yaml` не меняются по значениям; комментарий «turns on» в production выравнивается. Новый `helm/amp/tests/render-grouping-default.sh`: дефолт → `GROUPING_ENABLED: "true"`; `--set grouping.enabled=false` → `"false"`; `values-production.yaml` (с placeholders) → `"true"`.
5. **Доки и комментарии.** Known Gap #13 переписывается в «закрыт, как отключить»; из шагов миграции убирается ручное включение; `README.md`, `MIGRATION_QUICK_START.md`, `helm/amp/README.md` — убрать «off by default» и «lite игнорирует»; `deploy/*/config.yaml` — комментарий «must be explicit true» заменить на «default; kept explicit»; стейл-комментарии в `service_registry.go` (`:199`, `:1612`) и `alert_processor.go:29`.

6. **Цепочка таймеров (v1.1).** `onGroupIntervalExpired` после flush снова ставит `group_interval` (раньше — `repeat_interval`). `repeat_interval` перестаёт быть таймером: это TTL Dedup-шага, поэтому напоминание по неизменной группе уходит на первом flush после `repeat_interval` (как upstream; на практике — `repeat_interval`, округлённый вверх до тика `group_interval`). `onRepeatIntervalExpired` остаётся только для таймеров, сохранённых прежней версией, и тоже переводит группу на `group_interval`; `startRepeatIntervalTimer` удаляется. `AddAlertToGroup` таймер по-прежнему не трогает (тест `alert_processor_test.go:404-414` остаётся в силе). Логи таймера (`Started timer`, `Timer expired`, `Timer expiration processed`, «group_interval timer expired») понижаются до Debug: теперь они возникают раз в `group_interval` на каждую живую группу.
7. **INFO без `route:` (v1.1, review F5).** «Grouping subsystem not started: no route: tree configured, alerts are published directly», без атрибута `error`.

8. **Dedup — этап на получателя (v1.2 G1, v1.3 H1/H6).** Решение принимается для каждого target'а по набору, который ему причитается после его собственного фильтра `send_resolved` (`targetAlerts` получает этот набор от coordinator'а и считает сигнатуру по нему; под той же сигнатурой делается `RecordSent`). Дубликат — набор, каждый элемент которого (`fingerprint:status`) был в последней отправке этому target'у в пределах `repeat_interval` (`signatureCovers`). Набор, который лишь сузился, ждёт `repeat_interval`. Если publisher опросил хотя бы один target и все опрошенные покрыты, flush считается успешным без отправки и resolved-алерты удаляются из группы (`pruneResolvedAlerts`), как после подтверждённой доставки. Если publisher никого не опрашивал (metrics-only) — ничего не удаляется. Формат записи nflog не меняется.
9. **Самовосстановление таймера (v1.2, review G2).** `AddAlertToGroup` для существующей группы вызывает `ensureGroupTimer`: если у группы нет таймера ни локально, ни в storage (`GroupTimerManager.HasTimer`), ставится `group_wait`. Если таймер есть или проверка не удалась — ничего не делается (инвариант «алерт не перезапускает таймер» сохраняется). Событие — WARN и метрика операции `timer_rearm`.
10. **Память собственных отправок (v1.2 G3, v1.3 H2/H3).** `resilientNotifyLog` (`notify_log_fallback.go`) — обёртка над `GroupNotifyLog`, подключается в `ServiceRegistry.newNotifyLog` только для `RedisNotifyLog`. Зеркалирует каждый `RecordSent` в локальный `notifyDedupLog` и отвечает из него только когда `IsDuplicate` основного журнала вернул ошибку: своя отправка → «дубликат», иначе ошибка уходит наверх и менеджер действует fail-open. Локальные записи вытесняются по собственному TTL (`repeat_interval` + grace), проход не чаще раза в минуту. Менеджер о сбоях журнала ничего не знает; в `lite` обёртки нет. Известное ограничение: если между отправкой этой реплики и сбоем чтения ту же группу нотифицировала другая реплика, локальная запись устарела и может придержать одну нотификацию до восстановления чтения (не дольше `repeat_interval`).
11. **Логи на каждый flush (v1.2 G4, v1.3 H4).** В Debug переведены: «alert dropped… silenced/inhibited at send time», «No publishing targets matched receiver…» (ошибка по-прежнему логируется вызывающим на Error), логи старта и срабатывания таймеров. «Group publishing skipped (metrics-only mode)» остаётся на Info: кроме gauge режима это единственный повторяющийся признак, что групповые нотификации не доставляются.

Не делается в этой задаче: различение not-found и транзиентной ошибки в callback'ах (`GROUPING-CALLBACK-TRANSIENT-LOAD-BREAKS-CHAIN`) и маркер обработанного срабатывания (`TIMER-STORAGE-KEY-LOSS-SILENCES-FIRE`) — п.9 ограничивает их последствие «до следующего алерта в группе», корневые фиксы остаются в BUGS/TECH-DEBT. Нотификация «resolved» для алерта, который сработал и разрешился до первой отправки группы (review H5; upstream не шлёт): исправление требует отличать «записи нет» от «запись не покрывает» и теряет resolve после простоя получателя дольше TTL записи — в BUGS. Лишний Redis GET на алерт у реплики без локального handle (H9) — в TECH-DEBT. Конвертация legacy-таймера `repeat_interval` при `RestoreTimers` (review G5) — описано в migration note п.10.

Отвергнутая альтернатива для п.6: сбрасывать таймер на `group_interval` при добавлении алерта в существующую группу. Требует различать типы таймеров в `AddAlertToGroup`, взаимодействует с `fireStillDue` в HA и не покрывает resolved и ретрай недоставленного; постоянный тик покрывает всё одним изменением и совпадает с upstream.

Итог: дефолт меняется в двух местах, которые его задают — viper и values чарта. Подсистема и раньше чисто пропускала конфиг без `route:`, поэтому условная логика в загрузке конфига не нужна. Единственный побочный эффект — ложное предупреждение `AlertProcessor` на установках без `route:` — снимается тем, что процессору передаётся «включено и есть дерево». Оператор, сознательно выключивший группировку при наличии `route:`, видит одно предупреждение на старте. Откат для любой установки — один ключ.

## API Contracts

Не применимо: HTTP API не меняется. Контракт конфигурации: ключ `grouping.enabled` (YAML), env `GROUPING_ENABLED`, values `grouping.enabled` — имя и тип прежние, дефолт `false` → `true`.

## Data Model / Migrations

Не применимо. Новых ключей Redis и форматов нет; при первом старте с включённой группировкой таймеры и группы создаются штатно.

## Component Architecture

- `go-app/internal/config/config.go` — дефолт, doc-комментарий `GroupingConfig.Enabled`.
- `go-app/internal/application/service_registry.go` — WARN в `initializeGrouping`, эффективный флаг в сборке `AlertProcessorConfig`, комментарии.
- `go-app/internal/core/services/alert_processor.go` — только комментарии (`(the default)`).
- `go-app/internal/config/grouping_adapter_test.go` — `TestLoadConfig_GroupingDefaults` ждёт `true`; кейс явного `false` из YAML и из env.
- `go-app/internal/application/service_registry_grouping_test.go` — переименовать `…DisabledByDefault` в `…DisabledExplicitly` + проверка WARN; тест «дефолт из `LoadConfig` + `route:` → groupManager поднят»; тест «включено, нет `route:` → `AlertProcessor` без `groupingEnabled`, WARN не пишется».
- `helm/amp/values.yaml`, `helm/amp/values-production.yaml` (комментарий), `helm/amp/tests/render-grouping-default.sh` (новый).
- `deploy/smoke/config.yaml`, `deploy/e2e-ha/config.yaml` — комментарии.
- `docs/ALERTMANAGER_COMPATIBILITY.md`, `docs/MIGRATION_QUICK_START.md`, `docs/CONFIGURATION_GUIDE.md`, `README.md`, `helm/amp/README.md`, `CHANGELOG.md`, `helm/amp/CHANGELOG.md`.

## Security Design

- [x] Ownership validation — не применимо
- [x] Input validation — не меняется (bool, существующая валидация)
- [x] Sensitive data not logged — новый WARN не содержит значений конфига
- [x] Rate limiting — не применимо
- [x] Auth/RBAC — не применимо

## Invariants

- [ ] Конфиг без `route:`: прямая публикация, нет WARN про группировку, нет новой degraded-причины.
- [ ] Явное выключение выключает группировку по приоритету источников viper: env перекрывает файл. Под Helm действует только values `grouping.enabled` (чарт всегда передаёт `GROUPING_ENABLED`); ключ в `configFile.content` там не работает (review F3).
- [ ] `route:` есть, включено, подсистема не поднялась — `warnGroupingFallback` по-прежнему пишет WARN (громкий fallback не теряется).
- [ ] `values-production.yaml`, `deploy/smoke`, `deploy/e2e-ha`: значение флага прежнее; меняется только каденс flush (v1.1) — review G6.
- [ ] Набор, который только сузился относительно последней отправки, не нотифицируется до `repeat_interval`; набор с новым алертом, новым resolved или повторным firing — нотифицируется на ближайшем flush.
- [ ] Алерт, пришедший в группу с существующим таймером, таймер не перезапускает; в группу без таймера — ставит `group_wait`.
- [ ] Distributed lock, `fireStillDue`, reconciliation и формат записей nflog не тронуты; в цепочке таймеров меняется только тип следующего таймера после fire. В nflog меняется правило сравнения (подмножество) и набор, по которому считается сигнатура (набор получателя).
- [ ] Получатель с `send_resolved: false` не получает повтор оставшихся firing, когда другой алерт группы разрешился.
- [ ] Resolved-алерт удаляется из группы, только когда каждый опрошенный target либо подтвердил доставку, либо уже покрыт; если publisher никого не опрашивал — не удаляется.
- [ ] Неизменная группа не нотифицируется чаще, чем раз в `repeat_interval`.
- [ ] Алерт или resolve, пришедший в уже нотифицированную группу, уходит не позже чем через `group_interval`.
- [ ] Группа, удалённая после fire (все алерты resolved и доставлены), следующий таймер не получает.

## Edge Cases

1. `route:` есть, ключа `grouping:` нет, `lite` → группировка на in-memory storage, доставка после `group_wait`.
2. `route:` есть, ключа нет, `standard`, Redis недоступен → in-memory группировка, WARN от storage/nflog, degraded-причина только от кэша.
3. `route:` нет, ключа нет → INFO «no route tree configured», прямая публикация, `AlertProcessor.groupingEnabled == false`.
4. `route:` есть, `grouping.enabled: false` → WARN на старте, прямая публикация в receiver из routing decision.
5. `route:` есть, включено, дерево не собралось (`initializeRouting` non-fatal) → эффективный флаг `true` (берётся из конфига, не из результата сборки), `groupManager` может быть поднят, у алерта нет decision → `warnGroupingFallback`, публикация в `DefaultReceiver` — как сейчас при явном `true`.
6. Чарт с `--set grouping.enabled=false` → `GROUPING_ENABLED: "false"` перекрывает дефолт кода.
7. Апгрейд `standard` HA с `route:` без явного ключа → реплики начинают делить таймеры через Redis; двойное срабатывание закрыто `GROUPING-TIMER-LOCK-FIX`.
8. Включение/выключение через `/-/reload` → не действует до рестарта (как и раньше); отмечается в доке.
9. Группа с недоступным получателем → ретрай каждый `group_interval` (раньше — один быстрый ретрай, затем `repeat_interval`).
10. Группа, полностью подавленная silence/inhibit/time-mute → no-op flush каждый `group_interval`, отправка на первом flush после снятия подавления.
11. Таймер `repeat_interval` в Redis от прежней версии → срабатывает в свой срок (до 4h), затем группа идёт по `group_interval`.
12. `helm upgrade --reuse-values` → остаётся прежний `false`, на старте WARN о выключенной группировке.
13. Resolved-алерт, совпадающий с уже отправленным (алерт сработал и разрешился между двумя flush после того, как его resolve уже был в последней отправке) → считается дубликатом и удаляется из группы на том же flush, если покрыты все опрошенные target'ы (v1.3).
14. nflog недоступен на чтение → реплика не повторяет свои отправки; первая отправка и новые алерты идут (fail-open); реплика без локальной записи (другая реплика, либо эта же после рестарта) шлёт повторно на каждом изменении набора, пока чтение не восстановится: локальная память — in-memory и своя у каждой реплики.
15. Группировку выключили и включили, пока запись группы жива, а таймер истёк → группа получает `group_wait` при следующем алерте в ней.
16. `deploy/e2e-ha`: шаг 4 ждёт ровно 2 публикации за окно «рестарт B + `group_interval`»; при постоянном тике третья придёт через ещё один `group_interval` (30s). В v1.3 фиксированный `sleep` заменён опросом до появления второй публикации — проверка не зависит от длительности рестарта; прогон — `evidence/e2e-ha.md`.
17. Получатель с `send_resolved: false`: алерт разрешился и сработал снова → повторной нотификации нет (получатель о resolve не знал, для него набор не менялся) — как upstream.
18. Алерт сработал и разрешился до первой отправки группы → уходит нотификация «resolved» (upstream не шлёт) — известное расхождение, review H5, BUGS.

## Impact Analysis

- **Affected modules:** `internal/infrastructure/grouping` (цепочка таймеров, уровни логов — v1.1), `internal/config`, `internal/application`, `internal/core/services` (комментарии), Helm-чарт, `deploy/` (комментарии), публичные доки.
- **Breaking changes:** установки с `route:` без явного `grouping.enabled` (в чарте — без `grouping.enabled` в своих values) начинают группировать: первая нотификация группы задерживается на `group_wait` (30s, если не задано), повторы подчиняются `group_interval` / `repeat_interval`, несколько алертов группы приходят одной нотификацией. Запись в `CHANGELOG.md` `[Unreleased]`: `### Changed` + breaking/migration note; то же в `helm/amp/CHANGELOG.md`.
- **New dependencies:** нет.
- **Risks:**
  - Неожиданная задержка нотификаций после апгрейда → migration note с откатом одним ключом; стартовый лог «Initializing grouping subsystem...» уже есть.
  - Получатели, рассчитанные на «один алерт — один вызов», получат сгруппированный payload → в migration note.
  - Diff может превысить ~200 строк за счёт доков — тир уже Full, эскалации нет.
  - Нагрузка (v1.1): каждая живая группа раз в `group_interval` делает `storage.Load`, фильтры, claim и dedup-проверку на target (раньше — раз в `repeat_interval`). Upstream работает так же; под нагрузкой не измерялось.
  - На групповом пути нотификация не несёт LLM-классификацию (review F2) — в migration note; пронос — отдельная задача в BACKLOG.
  - Открытые дефекты группового пути (`GROUPING-CALLBACK-TRANSIENT-LOAD-BREAKS-CHAIN`, `TIMER-STORAGE-KEY-LOSS-SILENCES-FIRE`) теперь касаются всех конфигов с `route:` (review F8) — ссылка в Known Gap #13.

## Rollout / Rollback

- **Rollout:** обычный релиз; изменение вступает в силу при рестарте с новой версией. При rolling upgrade в HA старые реплики публикуют напрямую, новые группируют — на время раскатки возможны дубли (review F9, в migration note).
- **Rollback:** под Helm — `--set grouping.enabled=false`; без чарта — `grouping.enabled: false` в файле или `GROUPING_ENABLED=false`; затем рестарт. Группы в Redis после выключения не читаются и истекают по TTL (24h + 60s), таймеры — вскоре после своего срока; алерты, ждавшие в группе в момент выключения, прямым путём не отправляются.
- **Feature flag:** сам `grouping.enabled`.
- **Риск (assumed):** оператор не прочитал migration note — смягчается тем, что новое поведение совпадает с upstream и с тем, что `route:` декларирует; WARN для обратного случая (явный `false`) есть.

## Observability

- **Logs:** новый WARN на старте при `grouping.enabled=false` + `route:`; INFO без `route:` — нейтральный текст без `error`; ложный fallback-WARN на установках без `route:` не появляется; покадровые логи таймеров — Debug (v1.1).
- **Metrics:** новых метрик нет; у существующей метрики операций группы появляется операция `timer_rearm` (`success` / `error`). Метрика исхода срабатывания таймера — отдельная задача `TIMER-FIRE-OUTCOME-METRIC`.
- **Alerts:** не применимо.

## Deep Review

- **Mandatory triggers present:** 3+ signals (`C R X`).
- **Discretionary triggers present:** `C+X`.
- **Decision:** required.

## Open Questions

- [ ] Нет блокирующих. На `/testing`: smoke с конфигом без ключа `grouping:` (дефолт) в `lite` — подтвердить Edge Case 1 на собранном бинаре.
