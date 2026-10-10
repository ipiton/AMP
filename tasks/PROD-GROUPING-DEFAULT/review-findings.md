# Deep Review Findings: группировка включена по умолчанию

**Trigger classification:** mandatory (3 сигнала `C R X`, Spec § Deep Review: required)
**Reviewer perspective:** R1 — два независимых агента (general-purpose, read-only), не видевших выводов автора и друг друга: (A) корректность + премисы + контракт конфига + сопровождаемость; (B) runtime/rollout + observability + точность доков + история git. F1 перепроверен автором по коду после получения отчёта.
**Reviewed at:** 2026-10-10
**Reviewed tree:** bugfix/prod-grouping-default @ 177c57b
**Verdict:** R1 — fix_required; R2 — fix_required (see `review-verdict.json`)

Сама правка (дефолт, эффективный флаг, стартовый WARN, чарт) обоими ревьюерами признана корректной. Блокер — не в диффе, а в том, что смена дефолта выводит на всех пользователей `route:` существующий дефект группового пути.

## Round 1 — Findings

### F1 — после первого `group_interval` новые алерты группы ждут `repeat_interval` (до 4h)
- **Severity:** blocker
- **Location:** `go-app/internal/infrastructure/grouping/manager_impl.go` — `StartTimer` вызывается только в `:881` (`group_wait`, при создании группы), `:920` (`group_interval`, из callback'а `group_wait`), `:1824` (`repeat_interval`); `onGroupIntervalExpired` (`:1907-1955`) после публикации всегда ставит `repeat_interval`. Доки: `CHANGELOG.md` (migration note п.1), `docs/ALERTMANAGER_COMPATIBILITY.md` Known Gap #13.
- **Issue:** цепочка таймеров — `group_wait` → один `group_interval` → дальше только `repeat_interval`. Алерт, пришедший в существующую группу, таймер не трогает (закреплено тестом `alert_processor_test.go:404-414`). Новый алерт в группе старше `group_wait + group_interval` уйдёт только на ближайшем `repeat_interval` — до 4h по умолчанию; upstream отправил бы его на следующем `group_interval` (5m). Ревьюер B подтвердил запуском (`go test -overlay`, `group_wait=200ms`, `group_interval=300ms`, `repeat_interval=1h`: алерт B, добавленный через 1s, за 3s не доставлен, висит таймер `repeat_interval`). Resolved идёт тем же путём (по чтению кода, отдельно не прогонялось). Худший случай — минимальный verbatim-конфиг `route: {receiver: default}` без `group_by`: одна группа на receiver, через ~5.5 мин после первого алерта все новые ждут до 4h. До смены дефолта они уходили сразу. Дефект предсуществующий; в compat-доке (`:641-643`) описан только как «retry cadence» упавшего endpoint'а. Формулировки «groups the way upstream does» и «later ones follow `group_interval` (5m)» в новых доках неверны.
- **Recommendation:** до смены дефолта починить цепочку (после flush снова ставить `group_interval`, пока в группе есть недоставленные изменения, либо тикать `group_interval` постоянно с nflog-дедупом, как upstream). Альтернатива — не менять дефолт (вариант B из research: WARN + честные доки).
- **Disposition:** fix-here — решение владельца 2026-10-10 «вариант 2»; исправлено в `c344711`, подтверждено запуском обоими ревьюерами R2 (см. Round 2).
- **Follow-up:** n/a

### F2 — на групповом пути LLM-классификация не попадает в нотификацию
- **Severity:** major
- **Location:** `go-app/internal/core/services/alert_processor.go:800-815`; `go-app/internal/application/publishing_adapter.go:100-106` против `:150-170`
- **Issue:** `PublishGroup` шлёт `[]*core.Alert`, прямой путь — `EnrichedAlert{Classification}`. При группировке теряются блок «AI Classification» в форматтерах, маппинг severity в PagerDuty, приоритет очереди; LLM при этом вызывается и оплачивается. Комментарий в коде называет это «scoped gap… unaffected when grouping is disabled» — теперь это дефолт. В публичных доках и migration note этого нет. По чтению кода, запуском не проверялось.
- **Recommendation:** пункт в migration note и Known Gap; пронос classification в группу — отдельной задачей.
- **Disposition:** fix-here (доки) + defer-backlog (пронос classification)
- **Follow-up:** BACKLOG — при `finalize`.

### F3 — под Helm `grouping.enabled: false` в файле конфига не работает
- **Severity:** major (A: major, B: minor — принят старший)
- **Location:** `helm/amp/templates/configmap.yaml:63`; `CHANGELOG.md` migration note п.2; `docs/CONFIGURATION_GUIDE.md` § Grouping; Spec § Invariants 2, § Rollout / Rollback
- **Issue:** чарт всегда передаёт `GROUPING_ENABLED`, env в viper перекрывает файл (`TestLoadConfig_EnvOverridesFile`). `grouping: {enabled: false}` в `configFile.content` молча перекрывается `"true"` из values, стартового WARN нет. Migration note называет файл конфига равноценным способом отката. Invariant 2 для Helm ложен. Асимметрия была и раньше, но теперь ошибка идёт в сторону «нотификации неожиданно задерживаются».
- **Recommendation:** оговорка в обоих CHANGELOG, README чарта, `CONFIGURATION_GUIDE.md`: в Helm — только values `grouping.enabled`. Исправить Invariant 2. Полезно логировать эффективное значение на старте.
- **Disposition:** fix-here
- **Follow-up:** n/a

### F4 — явный `reconciliation_grace` может уронить старт после апгрейда
- **Severity:** minor
- **Location:** `go-app/internal/application/service_registry.go:2288` (`validateNotifyTimingBudget`, `publishing_runtime.go:483-530`); Spec Premise 5
- **Issue:** Premise 5 верна про `validateGrouping`, но есть второй, фатальный гейт, срабатывающий только при `groupManager != nil`. `standard` + Redis + `route:` с явным `grouping.reconciliation_grace` не больше claim TTL раньше стартовала (группировка выключена), после апгрейда откажется стартовать. Без явного grace значения согласованы — окно узкое.
- **Recommendation:** строка в Premise 5 и в migration note.
- **Disposition:** fix-here (доки)
- **Follow-up:** n/a

### F5 — INFO без `route:` несёт атрибут `error`
- **Severity:** minor (A: minor, B: nit)
- **Location:** `go-app/internal/application/service_registry.go:1629-1632`; `internal/config/grouping_adapter.go:15`
- **Issue:** при дефолте `true` каждая установка без `route:` пишет INFO «Grouping subsystem disabled: no route tree configured» с `error="grouping.enabled requires a route: tree to be configured…"` — читается как ошибка конфигурации, которой оператор не делал.
- **Recommendation:** не логировать `error` в этой ветке, нейтральный текст.
- **Disposition:** fix-here
- **Follow-up:** n/a

### F6 — устаревшие комментарии и тексты, пропущенные правкой
- **Severity:** minor
- **Location:** `alert_processor.go:105-113`, `:159-166`, `:368`, `:380` («mirrors config.Grouping.Enabled»); `service_registry.go:225-226`, `:413-415`; `publish_receiver_scoping_test.go:35`; `CHANGELOG.md:144`, `:267` («false (the default)» в том же `[Unreleased]`); `config.yaml.example` (есть `route:`, секции `grouping:` нет); `README.md:17` (дата заметки).
- **Issue:** описывают поведение, которого больше нет.
- **Recommendation:** обновить.
- **Disposition:** fix-here
- **Follow-up:** n/a

### F7 — `TestLoadConfig_MissingFile_UsesEnv` стал вырожденным
- **Severity:** minor
- **Location:** `go-app/internal/config/config_load_test.go:23,31`
- **Issue:** тест доказывал, что env доходит до конфига, через `GROUPING_ENABLED=true` + `assert.True`. При дефолте `true` пройдёт и без чтения env. Прогон из `evidence/default-flip-test-run.md` этого не показывает.
- **Recommendation:** `GROUPING_ENABLED=false` + `assert.False`.
- **Disposition:** fix-here (в `write-tests`)
- **Follow-up:** n/a

### F8 — открытые дефекты группового пути становятся дефолтом
- **Severity:** minor
- **Location:** `docs/06-planning/BUGS.md` (`GROUPING-CALLBACK-TRANSIENT-LOAD-BREAKS-CHAIN`), `TECH-DEBT.md` (`TIMER-STORAGE-KEY-LOSS-SILENCES-FIRE`), `helm/amp/values.yaml:481-482` (Redis `allkeys-lru`, 384mb), Known Gap #12
- **Issue:** транзиентная ошибка `storage.Load` обрывает цепочку; пропажа ключа таймера глушит срабатывание, а Redis чарта по умолчанию вытесняет ключи; авто-резолва нет, `CleanupExpiredGroups`/`RemoveAlertFromGroup` вне тестов не вызываются — алерт без resolved напоминает каждый `repeat_interval` бессрочно (по чтению кода). По отдельности не блокеры, но migration note о них молчит.
- **Recommendation:** ссылка в Known Gap #13; рассмотреть `noeviction` для Redis чарта.
- **Disposition:** fix-here (ссылка в доке) + defer-backlog (`noeviction`)
- **Follow-up:** BACKLOG — при `finalize`.

### F9 — rollout и rollback в HA не описаны
- **Severity:** minor
- **Location:** Spec § Rollout / Rollback; `redis_group_storage.go:286-298`, `redis_timer_storage.go:48-69`
- **Issue:** при раскатке старые реплики публикуют напрямую, новые группируют — возможны дубли. После отката в Redis остаются группы (TTL 24h+60s) и таймеры; повторное включение в этом окне восстановит их через `RestoreTimers`. Spec утверждает только «истекают по TTL».
- **Recommendation:** 2-3 строки в migration note или `ROLLBACK_RUNBOOK.md`; уточнить Spec.
- **Disposition:** fix-here (доки)
- **Follow-up:** n/a

### F10 — `helm upgrade --reuse-values` сохраняет старый `false`
- **Severity:** nit (A: nit, B: minor; запуском не проверено)
- **Location:** `helm/amp/CHANGELOG.md`
- **Issue:** новый дефолт не придёт, а AMP напишет WARN «Grouping is DISABLED…», хотя оператор ключ не задавал.
- **Recommendation:** фраза в CHANGELOG чарта.
- **Disposition:** fix-here
- **Follow-up:** n/a

### F11 — `/-/reload` с изменённым `grouping.*` не даёт «restart required»
- **Severity:** nit
- **Location:** `go-app/internal/config/reload_coordinator.go:649`, `:706`; Spec Premise 8
- **Issue:** вывод премисы верен, обоснование («grep пуст») неполно: `reload_coordinator.go` в grep не входил, там `grouping` числится компонентом. Предсуществующее.
- **Recommendation:** уточнить Premise 8; W6xx для `grouping.*` — отдельно.
- **Disposition:** fix-here (Spec) + defer-tech-debt
- **Follow-up:** TECH-DEBT — при `finalize`.

## Проверено ревьюерами, замечаний нет

- Конструирование `config.Config` вне тестов идёт через `LoadConfig`; пути JSON-персистенции конфига (`update_service`, `update_storage`, `service.go`) вне тестов не подключены.
- `yaml:"enabled,omitempty"`: `Config` целиком в YAML не маршалится, потери явного `false` нет.
- Стартовый WARN: один раз на процесс, только при `!Enabled && HasRouteTree()`, без значений конфига.
- `AlertProcessor`: `groupingEnabled` читают только `shouldGroup` и `warnGroupingFallback`; без `route:` три пути публикации не меняются; Edge Case 5 — как раньше при явном `true`.
- Сценариев, молча полагавшихся на выключенную группировку, нет: `deploy/smoke`, `deploy/e2e-ha` задают `true` явно.
- Premises 1, 2, 4, 7, 9 — подтверждены независимо. Premise 3 — `measured` заслужен для «lite группирует», но не для дефолтного пути. Premise 6 — верна по падениям; см. F7.
- История: дефолт `false` введён в `a91ec69` (task 2.2) с причиной «ingest pipeline is untouched until task 2.3»; причина отпала, отдельного решения держать `false` нет.

## Не проверялось в ревью

Полный `go test ./...`, `-race`, тег `integration`, `release-gate.sh`, Docker-стеки, живой `helm upgrade`, рендер `values-production.yaml` с placeholders, поведение при потере Redis в рантайме, задержка resolved-нотификации отдельным запуском.

## Решение по F1

Требуется решение владельца — см. итог сессии 2026-10-10. Варианты: (1) починить цепочку `group_interval` отдельной задачей, эту задачу поставить на паузу и довести после; (2) чинить цепочку в рамках этой задачи (scope +1–2d, механика таймеров); (3) не менять дефолт — вариант B из research (WARN + честные доки).

## Round 2 — 2026-10-10

**Reviewed tree:** bugfix/prod-grouping-default @ 7668135 (фиксы `c344711`, `ce06bd1`, артефакты `7668135`).
**Reviewer perspective:** два независимых агента, read-only, прогоны через `go test -overlay`: (A) коммит цепочки таймеров, сравнение с базой `c344711^`; (B) закрытие F2–F11, точность доков, флаг и стартовые логи, чарт, гигиена.

**Статус находок R1:** F1, F2, F4, F5, F10, F11 — закрыты (F1 и F5 — запуском). F3 — частично (нет оговорки в `helm/amp/README.md`, комментарии `values.yaml`, `ROLLBACK_RUNBOOK.md:39`). F6 — частично (остатки про `repeat_interval`-таймер, см. G8). F8, F9 — частично (см. G2). F7 — в `write-tests`, как запланировано.

Блокеров нет. Постоянный тик `group_interval` сам по себе корректен (неизменная группа: 16 тиков — одна нотификация; полный resolve закрывает группу; HA на miniredis — один flush на тик; утечек нет), но он вывел наружу три слабых места, которые раньше прятались за редким `repeat_interval`-таймером.

### G1 — лишняя нотификация через `group_interval`, когда отправленный набор сузился
- **Severity:** major (найдено обоими ревьюерами независимо)
- **Location:** `go-app/internal/infrastructure/grouping/manager_impl.go` Step 4b (`alertSetSignature` + `IsDuplicate`), `RecordSent`, `pruneResolvedAlerts`; сравнение на точное равенство — `dedup.go:123`, `redis_notify_log.go:274`
- **Issue:** dedup сравнивает точную сигнатуру набора. После нотификации с resolved-алертом он удаляется из группы, сигнатура остатка другая — следующий flush повторно шлёт оставшиеся firing. То же при silence/inhibit части группы (сузился — нотификация, вернулся — ещё одна). Upstream (`DedupStage.needsUpdate`) шлёт, только если появился firing/resolved, которого не было в прошлой отправке, либо истёк `repeat_interval`. Запуском: `[A,B] → [A, B resolved] → [A]` — третья лишняя; silence на B и снятие — две лишние; на базе в обоих сценариях одна. Формулировки «as upstream does» в CHANGELOG, compat-доке и комментарии `onGroupIntervalExpired` неверны.
- **Recommendation:** хранить в nflog множества firing/resolved и сравнивать как upstream; минимум — после prune перезаписывать запись сигнатурой остатка (не покрывает silence/inhibit).
- **Disposition:** открыт — решение владельца (см. «Решение по R2»)

### G2 — группа без таймера молчит бессрочно; тик проходит уязвимый участок в ~48 раз чаще
- **Severity:** major (предсуществующий, усилен)
- **Location:** `timer_manager_impl.go:1014-1044`, `:1087-1108`; `manager_impl.go:282`, `:1887-1893`; `redis_timer_storage.go:245` (TTL записи таймера = `ExpiresAt` + 10m)
- **Issue:** таймер ставится только при создании группы; `RestoreTimers` и reconciliation видят только сохранённые таймеры. Разовая ошибка `SaveTimer` при re-arm, окно деградации group storage дольше `group_interval` или разрыв с Redis дольше ~`group_interval`+10m оставляют группу без таймера: новые алерты и resolve не уходят (запуском: R6, R9 ревьюера A; in-memory прогон ревьюера B). Следствия для доков: фраза Known Gap #13 «go quiet until its next alert» неверна (следующий алерт таймер не ставит; та же неточность в записи TECH-DEBT); выключение группировки дольше ~`group_interval`+10m и повторное включение в пределах суток оставляет в Redis группы без таймеров — их алерты не доставляются; `ROLLBACK_RUNBOOK.md:196-199` «no manual cleanup needed» неверно.
- **Recommendation:** самовосстановление — в `AddAlertToGroup` для существующей группы ставить `group_interval`, если таймера нет; не удалять запись таймера, если callback вернул ошибку, а группа жива; отличать not-found от транзиентной ошибки. До фикса — честные доки.
- **Disposition:** открыт — решение владельца

### G3 — fail-open dedup срабатывает на каждом тике
- **Severity:** major (усилен)
- **Location:** `manager_impl.go:1333-1341`, `:1378-1388`
- **Issue:** при ошибке чтения nflog отправка идёт во все target'ы; раньше это случалось раз в `repeat_interval`, теперь — каждый `group_interval` для каждой группы, пока ошибка длится (запуском: +4 нотификации за 3 тика при `repeat_interval=1h`). Реалистичный случай — таймауты отдельных GET к Redis.
- **Recommendation:** при ошибке nflog на тике пропускать target'ы, которым эта реплика уже отправляла; fail-open оставить для первой отправки.
- **Disposition:** открыт — решение владельца

### G4 — шум в логах на каждый тик
- **Severity:** minor
- **Location:** `manager_impl.go:1119`, `:1143` (Info на каждый silenced/inhibited алерт), `:1430`; `publishing/coordinator.go:659` (Info в metrics-only), `:778` (Warn + Error для receiver без целей)
- **Issue:** понижены только логи таймер-менеджера; перечисленные строки теперь пишутся раз в `group_interval` на группу.
- **Disposition:** fix-here вместе с цепочкой

### G5 — legacy-таймер `repeat_interval` после апгрейда живёт до своего срока
- **Severity:** minor (новый)
- **Location:** `timer_manager_impl.go:1232-1267`
- **Issue:** группы в repeat-фазе на момент апгрейда держат старое поведение до `repeat_interval`; при rolling upgrade старая реплика продолжает ставить такие таймеры. Корректность не страдает. В CHANGELOG не описано.
- **Disposition:** fix-here (migration note либо конвертация в `RestoreTimers`)

### G6 — migration note п.5 и Invariant 4 неверны после смены цепочки
- **Severity:** minor
- **Issue:** «Nothing changes… if you already set `grouping.enabled: true`» — у таких установок меняется каденс. П.1: «a single notification» верно только для webhook/alertmanager-целей; Slack, PagerDuty, Telegram, Email получают сообщение на алерт.
- **Disposition:** fix-here

### G7 — предсуществующее, вне диффа
- **Severity:** minor
- **Issue:** gauge активных таймеров уходит в минус при удалении группы из callback'а (двойной `DecActiveTimers`, `timer_manager_impl.go:577`, `:1118`); `CleanupExpiredGroups` в проде не вызывается — группа с недоставляемыми resolved тикает бессрочно (~10–12 round-trip'ов Redis на тик); гонка на `len(group.Alerts)` без `group.mu` (`memory_group_storage.go:264`, `manager_impl.go:1836`, `:1895`, `:1946`).
- **Disposition:** defer-bugs / defer-tech-debt

### G8 — остатки формулировок и артефактов
- **Severity:** nit
- **Issue:** про `repeat_interval`-таймер: `docs/ALERTMANAGER_COMPATIBILITY.md:330`, `:348`; `timer_manager.go:171`, `:257-258`; `timer_manager_impl.go:832`, `:850`, `:1144`; `manager.go:988`; `manager_impl.go:1442-1443`, `:1755`, `:1827`; `redis_notify_log.go:35`; `memory_group_storage.go:216`; `redis_group_storage.go:292`; `publishing/coordinator.go:730`; `alert_processor.go:806-809`. Тесты, которые проходят и на старом коде (`TestTimerChain_GroupWaitToRepeatInterval`, `TestTimerContinuation_FullChainFiresRepeatIntervalTwice` — `repeat == group_interval`). Spec: п.2 называет переменную `groupingActive`, которой нет; Premise «Dedup решает» класса `measured` без файла в `evidence/`; Premise 5 — строки `config.go:1194-1205`.
- **Disposition:** fix-here (тесты — в `write-tests`)

### Проверено в R2, замечаний нет
Эффективный флаг и три стартовых состояния (запуском); `BuildGroupingConfig` возвращает только `ErrGroupingRequiresRouteTree`; env против файла — пять комбинаций (запуском); `helm template` / `helm lint`; `fireStillDue` при неизменном типе таймера; напоминание на первом тике после `repeat_interval`; `go test -race` по `grouping` и `publishing`; `git diff --check`, нет AI-атрибуции и секретов.

### Не проверялось в R2
Реальный Redis и rolling upgrade со смешанными версиями; реальный `PublishingCoordinator` с очередью под постоянным тиком; нагрузка на 10k групп; `helm upgrade --reuse-values`; рендер `values-production.yaml`; полный `go test ./...`, `release-gate.sh`, smoke/e2e-ha.

## Решение по R2

Гейт deep-review не пройден второй раз подряд (правило `CLAUDE.md` § Scope Discipline: остановиться и задокументировать блокер), оценка задачи вышла за ~2 дня (правило: резать на срезы). G1–G3 — не дефекты смены дефолта, а свойства группового пути, которые смена дефолта делает видимыми всем конфигам с `route:`. Требуется решение владельца — варианты в итоге сессии 2026-10-10.

## Anti-Pattern Check

- [x] Self-audit was not treated as a substitute for independent review.
