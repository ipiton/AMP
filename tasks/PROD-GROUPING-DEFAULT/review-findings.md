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
- **Disposition:** fix-here — `daab077`: `signatureCovers` в обеих реализациях `IsDuplicate` (решение владельца 2026-10-10: всё чинить в этой задаче)

### G2 — группа без таймера молчит бессрочно; тик проходит уязвимый участок в ~48 раз чаще
- **Severity:** major (предсуществующий, усилен)
- **Location:** `timer_manager_impl.go:1014-1044`, `:1087-1108`; `manager_impl.go:282`, `:1887-1893`; `redis_timer_storage.go:245` (TTL записи таймера = `ExpiresAt` + 10m)
- **Issue:** таймер ставится только при создании группы; `RestoreTimers` и reconciliation видят только сохранённые таймеры. Разовая ошибка `SaveTimer` при re-arm, окно деградации group storage дольше `group_interval` или разрыв с Redis дольше ~`group_interval`+10m оставляют группу без таймера: новые алерты и resolve не уходят (запуском: R6, R9 ревьюера A; in-memory прогон ревьюера B). Следствия для доков: фраза Known Gap #13 «go quiet until its next alert» неверна (следующий алерт таймер не ставит; та же неточность в записи TECH-DEBT); выключение группировки дольше ~`group_interval`+10m и повторное включение в пределах суток оставляет в Redis группы без таймеров — их алерты не доставляются; `ROLLBACK_RUNBOOK.md:196-199` «no manual cleanup needed» неверно.
- **Recommendation:** самовосстановление — в `AddAlertToGroup` для существующей группы ставить `group_interval`, если таймера нет; не удалять запись таймера, если callback вернул ошибку, а группа жива; отличать not-found от транзиентной ошибки. До фикса — честные доки.
- **Disposition:** fix-here — `daab077`: `ensureGroupTimer` + `GroupTimerManager.HasTimer`; корневые фиксы callback'ов остаются в BUGS/TECH-DEBT, доки исправлены

### G3 — fail-open dedup срабатывает на каждом тике
- **Severity:** major (усилен)
- **Location:** `manager_impl.go:1333-1341`, `:1378-1388`
- **Issue:** при ошибке чтения nflog отправка идёт во все target'ы; раньше это случалось раз в `repeat_interval`, теперь — каждый `group_interval` для каждой группы, пока ошибка длится (запуском: +4 нотификации за 3 тика при `repeat_interval=1h`). Реалистичный случай — таймауты отдельных GET к Redis.
- **Recommendation:** при ошибке nflog на тике пропускать target'ы, которым эта реплика уже отправляла; fail-open оставить для первой отправки.
- **Disposition:** fix-here — `daab077`: `localSent`, используется только при ошибке `IsDuplicate`

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

Гейт deep-review не пройден второй раз подряд (правило `CLAUDE.md` § Scope Discipline: остановиться и задокументировать блокер), оценка задачи вышла за ~2 дня (правило: резать на срезы). G1–G3 — не дефекты смены дефолта, а свойства группового пути, которые смена дефолта делает видимыми всем конфигам с `route:`. Владельцу предложены: срез в отдельную задачу, дочинить здесь, выпуск без смены дефолта. **Решение владельца 2026-10-10: дочинить всё в этой задаче.** Исправления — `daab077` и следующий коммит доков; проверка — Round 3.

## Round 3 — 2026-10-10

Предмет: `daab077` + `8418ab6` (HEAD `8418ab6`), регрессии всей ветки. Два независимых read-only ревьюера: (A) корректность кода, с прогонами через `go test -overlay -race`; (B) доки и операторский контракт против кода, `helm lint`/`helm template`. Блокеров нет. G1–G3 закрыты: `signatureCovers`, `ensureGroupTimer`/`HasTimer` (в том числе на двух «репликах» с общим storage: не-владелец таймер не взводит, flush один), формат nflog совместим со старыми записями — подтверждено прогонами ревьюера A.

### H1 — получатель с `send_resolved: false` получает повтор оставшихся firing при каждом частичном resolve
- **Severity:** major
- **Location:** `manager_impl.go:1434-1438` (`targetAlerts`), `publishing/coordinator.go:694-707` (`filterAlertsForTarget`)
- **Issue:** сигнатура для dedup считается по всей группе, а не по набору, который причитается target'у после фильтра. Отправлено `{a,b firing}`; `a` resolved → групповая сигнатура содержит новый `a:resolved` → target без resolved получает `b:firing` повторно. Upstream `needsUpdate` смотрит resolved только при `SendResolved()`. Было и до ветки (при точном сравнении), но срабатывало раз в `repeat_interval`; с постоянным тиком — на ближайшем `group_interval` после каждого resolve. Формулировка «upstream's rule» (`CHANGELOG.md:304`, doc-комментарий `signatureCovers`) для таких target'ов неверна (D1).
- **Evidence:** прогон ревьюера A на эмуляции порядка вызовов coordinator'а: 2 нотификации (upstream: 1); ревьюер B пришёл к тому же чтением кода независимо. На реальном `PublishingCoordinator` не прогонялось.
- **Disposition:** fix-here — сигнатура по набору получателя (`targetAlerts`); доки исправлены (D1)

### H2 — `localSent` может подавить нотификацию, которую общий nflog отправил бы
- **Severity:** minor (внесено `daab077`)
- **Issue:** HA: реплика A отправила `{a,b firing}`; B отправила `a:resolved`; `a` снова firing; у A чтение nflog падает → `localSent` A покрывает `a:firing` → пропуск до восстановления чтения (максимум `repeat_interval`). Противоречит принципу «дубликат лучше потери». Нужны смена владельца + flap + сбой чтения nflog. Подтверждено прогоном.
- **Disposition:** fix-here (ограничено) — память отправок вынесена в `resilientNotifyLog`, вытеснение по TTL; остаточное ограничение описано в CHANGELOG и doc-комментарии, TECH-DEBT

### H3 — `localSent` не вытесняет записи групп, удалённых не этим процессом
- **Severity:** minor (внесено `daab077`)
- **Issue:** `Forget` вызывается только в двух путях удаления группы этим процессом; группа, удалённая другой репликой или по TTL Redis, оставляет запись до рестарта. Прогон: 100 групп → `localSent` 100 записей при 0 в общем nflog. Медленная утечка при churn ключей в HA.
- **Disposition:** fix-here — вытеснение локальных записей по TTL

### H4 — в metrics-only режиме понижение логов расходится с заявленным
- **Severity:** minor
- **Issue:** ревьюер A: через `coordinator.go:661` (пустые outcomes без ошибки) не остаётся повторяющегося сигнала выше Debug. Ревьюер B (D2): через `MetricsOnlyPublisher` (`publishing_metrics_only.go:69`, `manager_impl.go:1503`) Info + Warn остаются на каждый flush каждой группы бессрочно — и на них опирается `deploy/e2e-ha/run.sh:127`, `:203`. Оба пути существуют; `CHANGELOG.md:306` не описывает ни один.
- **Disposition:** fix-here — «Group publishing skipped (metrics-only mode)» возвращён на Info; CHANGELOG описывает оба пути

### H5 — resolved-only нотификация для группы, о которой получатель не знал
- **Severity:** minor (не регрессия)
- **Issue:** алерт сработал и разрешился до первого flush → уходит «resolved». Upstream при отсутствии записи шлёт только при непустом firing. Прогон: 1 (upstream: 0).
- **Disposition:** defer-bugs — исправление требует отличать «записи нет» от «запись не покрывает» и теряет resolve после простоя получателя дольше TTL записи; расхождение названо в compat-доке #13

### H6 — resolved, признанный дубликатом, не удаляется из группы
- **Severity:** minor (не регрессия; станет частым при исправлении H1 через сигнатуру по candidates)
- **Issue:** при пустых outcomes prune не вызывается; алерт остаётся до следующей реальной отправки и объявляется resolved повторно.
- **Disposition:** fix-here — flush, на котором все опрошенные target'ы покрыты, удаляет resolved

### H7 — legacy-таймер `repeat_interval` после апгрейда держит задержку F1 до своего срока
- **Severity:** minor
- **Issue:** `HasTimer` = true, re-arm не срабатывает; новый алерт в такой группе ждёт остаток `repeat_interval` (до 4h), один раз на группу, пережившую апгрейд. Migration note п.10 этого последствия не называет (D8).
- **Disposition:** defer-tech-debt — timer manager не знает `group_interval` группы; последствие названо в migration note п.10

### H8 — гонка на `len(group.Alerts)` без `group.mu`
- **Severity:** minor (предсуществующее, G7; экспозиция выросла)
- **Issue:** `manager_impl.go:1901`, `:1913`, `:1961`, `:1972`, `memory_group_storage.go:264`. Прогон ingest + flush под `-race`: 4 отчёта DATA RACE. Тест «ingest во время flush» под `-race` упадёт.
- **Disposition:** fix-here — `alertCount(group)`

### H9 — `HasTimer` добавляет Redis GET на каждый алерт у реплики без локального handle
- **Severity:** minor (perf)
- **Issue:** в HA это (N−1)/N алертов; при ошибке GET — Warn + Error на каждый алерт без rate-limit. Не под мьютексом менеджера, deadlock'а нет.
- **Disposition:** defer-tech-debt

### H10 — мелочи в коде
- **Severity:** nit
- **Issue:** комментарии «exact alert set» (`dedup.go:110-112`, `manager_impl.go:1292`, `:1427`); метрика `timer_rearm/success` пишется до результата `StartTimer` (`manager_impl.go:948`); fingerprint из API с символом `|` ломает разбор сигнатуры (`handlers/alerts.go:543`); в lite `localSent` дублирует основной nflog.
- **Disposition:** fix-here (комментарии, порядок метрики); символ `|` в fingerprint — defer-bugs

### D3 — `deploy/e2e-ha` шаг 4 стал чувствителен ко времени
- **Severity:** minor
- **Issue:** `run.sh:299-307`: проверка ровно 2 публикаций приходится на ≈50–61s после POST, третий flush — на ~68s; запас 7–18s вместо 1h. CI гоняет e2e-ha на каждом PR (не required). Комментарии `run.sh:313-314`, `deploy/e2e-ha/config.yaml:67-73` («exactly once») устарели. Шаги 3, 5, 6 и smoke не затронуты. Выведено расчётом, стек не запускался.
- **Disposition:** fix-here — опрос вместо `sleep`; прогон `evidence/e2e-ha.md` ALL PASS

### D4 — комментарий и CHANGELOG о `localSent` сильнее кода
- **Severity:** minor
- **Issue:** `manager_impl.go:1625-1627`, `CHANGELOG.md:305`: при неудачной записи в nflog и успешном чтении `localSent` не спрашивается — реплика шлёт повтор на каждом `group_interval`.
- **Disposition:** fix-here — формулировки; уточнение: неудачная запись при успешном чтении даёт один повтор (следующий `RecordSent` его закрывает), а не повтор на каждом flush

### D5–D7 — Spec v1.2: противоречия, уехавшие строки, завышенные классы evidence
- **Severity:** minor
- **Issue:** Premise 11 и п.6 против п.9; Invariant «nflog не тронуты»; «Metrics: не меняются» против `timer_rearm`; Premise 10 против правила подмножества; Edge Case 14 занижает число дублей. Номера строк `service_registry.go`, `config.go`, `alert_processor.go`. Premise 3 `measured` снят на `21dde05` с явным `true`; Premise 10 `measured` — пробы вне дерева, без Redis.
- **Disposition:** fix-here — Spec v1.3

### D8 — пробелы migration notes и runbook
- **Severity:** minor
- **Issue:** lite: рестарт в окне `group_wait` теряет ожидающую нотификацию (snapshot хранит только silences и nflog); WARN «alert group has no timer scheduled…» и операция `timer_rearm` не описаны; п.7 — `validateNotifyTimingBudget` не работает в metrics-only; п.1 — в списке non-batch целей нет Rootly; откат только образа при новых values оставляет группировку на старой цепочке (задержка F1); `ROLLBACK_RUNBOOK.md:39` — алерты, уже сидящие в группах, при выключении не отправляются.
- **Disposition:** fix-here — migration notes п.1, 4, 7, 10, runbook; уточнение: в lite ожидающая нотификация не теряется, а уходит после повторной отправки алерта Prometheus

### D9–D10 — формулировки и нетронутые устаревшие места
- **Severity:** nit
- **Issue:** «alerts changed» без оговорки про подмножество (`CONFIGURATION_GUIDE.md:255`, `timer_models.go:37`, `manager_impl.go:1949`); `tasks.md:66`; комментарии `config.go:101`, `service_registry.go:1594`, `memory_group_storage.go:216`, `redis_notify_log.go:14`, `timer_manager_impl.go:868`.
- **Disposition:** fix-here частично — формулировки про набор; исторические нарративы в комментариях оставлены

### Проверено в R3, замечаний нет
`go vet` + `go test -race -count=1` по `grouping`, `publishing`, `application`, `core`, `config`; `signatureCovers` на таблице случаев (сужение, новый firing, новый resolved, re-fire после resolved, firing→resolved→firing между отправками, пустые строки) — как upstream при `send_resolved: true`; совместимость записей nflog; отсутствие окна «таймера нет ни локально, ни в storage» у живой цепочки; `fireStillDue` и lock при одновременном re-arm; `helm lint`, `helm template` (дефолт, `false`, lite, production); env против файла; стартовые логи дословно; в `docs/`, `deploy/`, `helm/`, `scripts/`, `.github/` нет зависимостей от пониженных строк.

### Не проверялось в R3
Реальный Redis (`RedisNotifyLog`, `RedisTimerStorage`, reconciliation), rolling upgrade, docker-стеки smoke и e2e-ha, реальный `PublishingCoordinator` для H1, `helm upgrade --reuse-values`, полный `go test ./...`.

## Решение по R3

Гейт не пройден третий раз подряд. Раунд закрыл то, ради чего запускался (G1–G3), но каждое исправление в notify-chain открывает следующий слой: H2 и H3 внесены самим `daab077`, H1 — предсуществующее расхождение с upstream, которое постоянный тик делает заметным. Работа остановлена по правилу § Scope Discipline; исправления по R3 не начаты. Требуется решение владельца — варианты в итоге сессии 2026-10-10.

**Решение владельца 2026-10-10: «полноценное решение корня».** Общая причина R2–R3: решение «слать или нет» принималось на уровне группы, а знание о том, что причитается получателю, жило в coordinator'е; память собственных отправок была вторым источником правды внутри менеджера. Fix-раунд: dedup считается по набору получателя, flush без отправки при покрытых получателях считается успешным (prune), память отправок вынесена в обёртку над Redis-nflog. Проверка — Round 4.

## Round 4 — 2026-10-10

Коммиты под ревью: `245c95d`, `5df0a32` (fix-раунд R3, Spec v1.3). Два независимых агента, только чтение: A — корректность notify-chain и HA (вердикт fix_required), B — контракты, доки, артефакты (вердикт pass с замечаниями).

### K1 — ответ обёртки из локальной памяти считался покрытием и запускал prune
- **Severity:** major
- **Issue:** при нечитаемом nflog `resilientNotifyLog.IsDuplicate` отвечал «дубликат» по своей записи; менеджер засчитывал target как покрытый и на flush без отправки удалял resolved-алерты. Если запись устарела (набор изменился, resolve ещё не отправлен), resolve терялся навсегда: алерта в группе больше нет.
- **Disposition:** fix-here — sentinel `ErrNotifyLogAnsweredLocally`: target'у не шлём, покрытым не считаем, prune не выполняется. Проба `TestR5_K1_LocalAnswerDoesNotPrune`.

### K2 — комментарий обёртки не называл её ограничение
- **Severity:** minor
- **Issue:** doc-комментарий `resilientNotifyLog` обещал «не повторяет свои отправки» без оговорки, что устаревшая запись может придержать нотификацию, включая первую у вновь сработавшей группы.
- **Disposition:** fix-here — комментарий; CHANGELOG (верхняя граница `repeat_interval`, память не переживает рестарт); TECH-DEBT при finalize (2d.8).

### K3 / L5 — шаг 4 `deploy/e2e-ha/run.sh` мог пройти вхолостую
- **Severity:** minor
- **Issue:** опрос «до появления второй публикации» проходил и тогда, когда вторая публикация случилась до рестарта реплики B, то есть без гонки двух таймеров.
- **Disposition:** fix-here — перед опросом публикаций должно быть ровно одна, иначе сценарий падает как холостой. Одновременность срабатывания по-прежнему не доказывается выводом — записано в `evidence/e2e-ha.md`.

### K4 — doc-комментарий `DeliveredAlerts` оторван вставленной функцией
- **Severity:** nit
- **Disposition:** fix-here — `evictExpired` перенесён выше.

### K5 — prune удалял алерт без проверки текущего статуса
- **Severity:** minor
- **Issue:** между отправкой и `pruneResolvedAlerts` алерт мог снова стать firing (ingest идёт параллельно с flush); удаление по fingerprint выбрасывало уже активный алерт из группы до следующей отправки Prometheus.
- **Disposition:** fix-here — `removeAlertFromGroup` с условием «всё ещё resolved» под блокировкой группы. Проба `TestR5_K5_PruneKeepsRefiredAlert`.

### L1–L4, L15 — комментарии и контракты
- **Severity:** nit
- **Issue:** комментарий prune называл одно место вызова из двух; контракт `PublishGroup`/`targetAlerts` не говорил, что dedup до вызова не выполнен и что ноль outcomes при опрошенных target'ах означает «покрыто»; комментарии coordinator'а про ноль outcomes; `dedup.go` — «upstream needsUpdate rule» без оговорки про H5.
- **Disposition:** fix-here

### L6, L13 — evidence утверждал больше, чем показывал вывод
- **Severity:** minor (L6), nit (L13)
- **Issue:** `evidence/e2e-ha.md` — «`RedisNotifyLog` под обёрткой работает», хотя metrics-only publisher не вызывает `targetAlerts`, и из методов обёртки выполнялся только `TryClaim`; привязка к рабочему дереву, а не к коммиту. Пробы: два утверждения без строки в выводе, файл проб вне дерева.
- **Disposition:** fix-here — текст сужен, прогон повторён на коде R4; файл проб сохранён в `evidence/r4-probes.go.txt` с командой запуска.

### L7 — `ROLLBACK_RUNBOOK.md`
- **Severity:** minor
- **Issue:** строка про `grouping.enabled: false` не говорила, что алерты, уже сидящие в группах, прямым путём не уходят; не описан `helm rollback` на чарт с прежним дефолтом; при откате только образа версии по-разному сравнивают набор с nflog.
- **Disposition:** fix-here

### L8–L10 — CHANGELOG
- **Severity:** minor
- **Issue:** не сказано, что при нечитаемом nflog предупреждение пишется на каждом flush и метрики этого состояния нет; что в metrics-only / без target'ов resolved не удаляются и группы живут до рестарта или TTL; что локальная память ограничена `repeat_interval` и не переживает рестарт.
- **Disposition:** fix-here — текст; метрика состояния fallback — defer-tech-debt (вместе с ограничением обёртки, 2d.8).

### L11, L12, L14 — Spec, доки про профили, tasks
- **Severity:** nit
- **Issue:** Component Architecture и Impact Analysis без файлов `grouping`/`publishing` и логики `run.sh`; неверные номера Premises для G1–G3; устаревшие номера строк; Summary без v1.3; синтетические target'ы; «kept in Redis» без оговорки про fallback; правило dedup без «среди причитающихся target'у»; двойной `verify` у 5.5; Spec писал про запись в BUGS как о существующей.
- **Disposition:** fix-here — Spec v1.4, `CONFIGURATION_GUIDE.md`, `values.yaml`, compat-док, `tasks.md`. Записи в BUGS / TECH-DEBT создаются на finalize (2d.8) — это порядок конвейера, не дефект.

### Найдено при исправлении R4
После правки K1 проба HA под `-race` показала чтение `len(group.Alerts)` без блокировки в трёх проверках callback'ов таймера и в логе `AddAlertToGroup` (остаток H8). Исправлено там же (`alertCount`); 10 прогонов подряд без отчётов о гонке.

### Проверено в R4, замечаний нет
`helm lint`, `helm template` (дефолт, `--set grouping.enabled=false`, lite); `go vet` по `grouping`, `publishing`, `application`; тексты логов и имя операции `timer_rearm` дословно совпадают с доками; классы evidence в Design Premises не завышены; per-target сигнатура и `RecordSent` под ней; поведение `send_resolved: false` соответствует upstream.

### Не проверялось в R4
Реальная доставка получателю и `RecordSent`/`IsDuplicate` на реальном Redis (e2e-ha — metrics-only); rolling upgrade; `helm upgrade --reuse-values`; поведение при недоступном Redis на живом стенде.

## Round 5 — 2026-10-10 (проверка дельты `51a949a`)

Один независимый агент, только чтение, со своими пробами. Вердикт: fix_required.

### M1 — K1 не закрыт на пути с отправкой
- **Severity:** major
- **Issue:** target, придержанный локальным ответом, не попадал в outcomes; если другому target'у отправка удалась, `allSucceeded` оставался истинным и prune удалял resolved. Придержанный target resolve не получал никогда. Подтверждено пробой ревьюера.
- **Disposition:** fix-here — счётчик придержанных target'ов; при ненулевом prune не выполняется и после успешной отправки. Проба ревьюера проходит: после восстановления чтения target получает resolve.

### M2 — CHANGELOG расходился с кодом
- **Severity:** minor
- **Issue:** «resolved alerts stay… every flush logs a warning» — верно только для flush с придержанным target'ом; для target'а без локальной записи пишется Error и идёт отправка.
- **Disposition:** fix-here

### M3 — `MemoryGroupStorage.Store` читает группу без её блокировки
- **Severity:** minor (предсуществующее, вне дельты)
- **Issue:** два параллельных ingest в одну группу: итерация по `group.Alerts` в `Store` против записи под блокировкой — гонка, потенциально `concurrent map iteration and map write` в lite и в memory-fallback. С группировкой по умолчанию путь становится общим.
- **Disposition:** fix-here — `Store` читает группу под `group.mu.RLock`; неблокированное чтение `Metadata.State` в debug-логе убрано. `RedisGroupStorage.Store` (`json.Marshal` и `Version++` без блокировки) — defer-tech-debt: объект там обычно свой у каждого вызова, пробой не воспроизведено.

### M4 — проверка K5 атомарна только в пределах объекта группы
- **Severity:** nit
- **Disposition:** fix-here в Spec (оговорка про Redis); `storage.Delete` без проверки версии — defer-tech-debt.

### M5 — узкое окно ложного падения шага 4 e2e-ha
- **Severity:** nit
- **Disposition:** accept — падение громкое, холостого прохода нет; записано в `evidence/e2e-ha.md`.

### Проверено в R5, замечаний нет
K1 в ветке без outcomes (смешанные target'ы, синтетические `suppressed:`/`blackhole:`, локальный ответ + неуспех другого); `errors.Is` через `%w: %w`; других потребителей `IsDuplicate` нет; K5 — блокировки, ранний выход, публичный `RemoveAlertFromGroup`; `alertCount` в callback'ах; комментарии и Spec п.8, Edge Case 20.

### Не проверялось в R5
`deploy/e2e-ha/run.sh` на стенде (только чтение); реальный Redis при сбое; доки `693a099` вне указанных абзацев.

## Anti-Pattern Check

- [x] Self-audit was not treated as a substitute for independent review.
