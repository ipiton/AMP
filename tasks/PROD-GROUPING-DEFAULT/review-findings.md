# Deep Review Findings: группировка включена по умолчанию

**Trigger classification:** mandatory (3 сигнала `C R X`, Spec § Deep Review: required)
**Reviewer perspective:** R1 — два независимых агента (general-purpose, read-only), не видевших выводов автора и друг друга: (A) корректность + премисы + контракт конфига + сопровождаемость; (B) runtime/rollout + observability + точность доков + история git. F1 перепроверен автором по коду после получения отчёта.
**Reviewed at:** 2026-10-10
**Reviewed tree:** bugfix/prod-grouping-default @ 177c57b
**Verdict:** R1 — fix_required (see `review-verdict.json`)

Сама правка (дефолт, эффективный флаг, стартовый WARN, чарт) обоими ревьюерами признана корректной. Блокер — не в диффе, а в том, что смена дефолта выводит на всех пользователей `route:` существующий дефект группового пути.

## Round 1 — Findings

### F1 — после первого `group_interval` новые алерты группы ждут `repeat_interval` (до 4h)
- **Severity:** blocker
- **Location:** `go-app/internal/infrastructure/grouping/manager_impl.go` — `StartTimer` вызывается только в `:881` (`group_wait`, при создании группы), `:920` (`group_interval`, из callback'а `group_wait`), `:1824` (`repeat_interval`); `onGroupIntervalExpired` (`:1907-1955`) после публикации всегда ставит `repeat_interval`. Доки: `CHANGELOG.md` (migration note п.1), `docs/ALERTMANAGER_COMPATIBILITY.md` Known Gap #13.
- **Issue:** цепочка таймеров — `group_wait` → один `group_interval` → дальше только `repeat_interval`. Алерт, пришедший в существующую группу, таймер не трогает (закреплено тестом `alert_processor_test.go:404-414`). Новый алерт в группе старше `group_wait + group_interval` уйдёт только на ближайшем `repeat_interval` — до 4h по умолчанию; upstream отправил бы его на следующем `group_interval` (5m). Ревьюер B подтвердил запуском (`go test -overlay`, `group_wait=200ms`, `group_interval=300ms`, `repeat_interval=1h`: алерт B, добавленный через 1s, за 3s не доставлен, висит таймер `repeat_interval`). Resolved идёт тем же путём (по чтению кода, отдельно не прогонялось). Худший случай — минимальный verbatim-конфиг `route: {receiver: default}` без `group_by`: одна группа на receiver, через ~5.5 мин после первого алерта все новые ждут до 4h. До смены дефолта они уходили сразу. Дефект предсуществующий; в compat-доке (`:641-643`) описан только как «retry cadence» упавшего endpoint'а. Формулировки «groups the way upstream does» и «later ones follow `group_interval` (5m)» в новых доках неверны.
- **Recommendation:** до смены дефолта починить цепочку (после flush снова ставить `group_interval`, пока в группе есть недоставленные изменения, либо тикать `group_interval` постоянно с nflog-дедупом, как upstream). Альтернатива — не менять дефолт (вариант B из research: WARN + честные доки).
- **Disposition:** открыт — решение владельца (см. «Решение по F1» ниже)
- **Follow-up:** запись в `BUGS.md` — после решения.

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

## Anti-Pattern Check

- [x] Self-audit was not treated as a substitute for independent review.
