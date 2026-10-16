# TECH-DEBT

Работающее по контракту, но дорогое в сопровождении: плохая структура, хрупкий дизайн, нет тестов на рабочий код, слабая наблюдаемость. Если код нарушает свой контракт — это баг, место ему в `BUGS.md` (граница — `docs/solo-kanban/artifact-contract.md` § Bug Versus Tech Debt).

Погашенный долг удаляется; отчёт — в `DONE.md`.

## Bundles

<!-- Мелкие записи одной подсистемы (одних и тех же файлов) — в одного claimable-родителя.
     В Queue `NEXT.md` идёт только slug бандла. См. docs/solo-kanban/artifact-contract.md § Bundles. -->

## Critical

## High

## Medium

### [medium][Helm][~2d] HELM-CHART-GAPS
- **Title:** расхождения шаблонов чарта с values
- **Problem:** найдено при аудите `values-production.yaml` (INF-B, 2026-08-20), не исправлено (слишком большой темплейт-скоуп для values-аудита):
  - `valkey.enabled`/`cache.enabled` НЕ гейтят деплой `templates/redis-statefulset.yaml` — единственный гейт — `profile: standard`. Рабочий воркараунд — `valkey.replicas: 0` (заведён в values-production.yaml), но сами флаги мёртвые. _(Уточнено аудитом 2026-10-06, `helm template`: `valkey.replicas: 0` обнуляет именно `amp-redis` — тот Redis, в который ходит AMP по умолчанию (`REDIS_ADDR=amp-redis:6379`), — а Deployment сабчарта `amp-valkey` продолжает деплоиться. Обход работает только с внешним Redis (`cache.host`, как Dragonfly в values-production). `cache.enabled: false` ставит `REDIS_ADDR=localhost:6379`. Без внешнего Redis штатно убрать лишний инстанс нельзя — два Redis на каждый `standard`-деплой. Связано с `HELM-SINGLE-NODE-DEFAULTS` в BACKLOG.)_
  - `postgresql.existingSecret` читается `templates/secret.yaml` (другой Secret, `<fullname>-secrets`), но НЕ читается `templates/postgresql-secret.yaml`/хелпером `amp.postgresql.secretName` — реальный DB-пароль (`DATABASE_PASSWORD`, `POSTGRES_PASSWORD` в StatefulSet) всегда берётся из `postgresql.password`, existingSecret туда не долетает.
  - _(2026-10-15, PROD-POSTGRES-HA-DECISION)_ HA встроенного PostgreSQL решён ADR-016: встроенный — dev/pilot, single instance, `postgresql.replicas != 1` падает на рендере; прод — внешний PostgreSQL. Реализации HA в чарте не будет.
  - _(2026-10-15, PROD-POSTGRES-HA-DECISION R16)_ `migration.backupBeforeMigration` в `values.yaml` ни один шаблон не читает — мёртвое обещание бэкапа перед миграцией; удалить ключ (или реализовать, если понадобится).
  - _(2026-10-15, PROD-POSTGRES-HA-DECISION R24)_ `NOTES.txt` (шаг 5) советует `kubectl port-forward svc/<fullname>`, но селектор Service захватывает и postgres/redis (BUGS `SERVICE-METRICS-PORT-MISROUTED`) — адресовать под AMP по меткам `component=application`, как runbook `POSTGRESQL_RESTORE_GUIDE.md` § 1.
  - Найдено ревью fix-round (2026-08-20): базовый `values.yaml` до сих пор хардкодит слабый dev-дефолт для `postgresql.password` (dev/test zero-config install path). Не трогали — задача была про `values-production.yaml`, и требование "без дефолта" сломало бы dev-путь без правки `secret.yaml`'s fallback-логики. `values-production.yaml` явно перекрывает его на `""` (см. её комментарий) — инконсистентность зафиксирована здесь, не исправлена.
  - _(2026-10-01, PROD-INGRESS-HARDENING R17)_ сабчарт valkey рендерит свою NetworkPolicy `amp-valkey` (deny-all по умолчанию) параллельно чартовой `redis-networkpolicy.yaml`; политики postgres/redis хардкодят `namespaceSelector` `name: monitoring` / `name: kube-system` вместо автоматической метки `kubernetes.io/metadata.name`; `ingress.yaml` безусловно добавляет nginx sticky-аннотации (`affinity: cookie` и др.) к любым `ingress.annotations`.
  - _(2026-10-14, HELM-DEFAULTS-VALIDATE review F7)_ `postgresql.config.ssl` не bool-safe: `ssl: on` без кавычек или `--set postgresql.config.ssl=true` → `eq: incompatible types` на рендере; `ssl: "true"` даёт PostgreSQL `ssl = true`, а AMP — `DATABASE_SSL_MODE=disable` и флаг `DATABASE_ALLOW_INSECURE_SSL_IN_PRODUCTION` (`templates/deployment.yaml`). Фикс: сравнивать через `toString`, `fail` на всё, кроме `"on"`/`"off"`.
- **Impact:** операторы, включающие `existingSecret` или выключающие valkey флагом, получают не то, что заявлено в values.
- **Fix:** гейтить Redis по `valkey.enabled`, провести `existingSecret` через `amp.postgresql.secretName`; убрать `migration.backupBeforeMigration`; NOTES — port-forward на под по меткам. HA Postgres — не делаем (ADR-016).
- **Refs:** `helm/amp/templates/{redis-statefulset,secret,postgresql-secret,postgresql-statefulset}.yaml`
- **Status:** open

### [medium][Config][~0.5d] CONFIG-GLOBAL-VIPER-STATE
- **Title:** `LoadConfig` на глобальном viper держит значения прошлого файла
- **Problem:** `config.LoadConfig` работает на глобальном `viper`. Отсутствующий файл не ошибка (PROD-CONFIG-FILE-FALLBACK), но `ReadInConfig` при этом не сбрасывает прочитанное ранее: повторный `LoadConfig` после исчезновения файла вернёт старые значения и `Routing == nil` без ошибки. Старт не затронут; reload проверяет файл до вызова (`ReloadCoordinator.loadAndParse`), остаётся окно TOCTOU между `os.ReadFile` и `LoadConfig` — при атомарной смене ConfigMap на практике недостижимо. Инвариант записан комментарием в `LoadConfig`.
- **Impact:** любой новый вызов `LoadConfig` вне старта может молча получить смесь старого и нового конфига; тесты вынуждены звать `resetViper`.
- **Fix:** `viper.New()` на каждый вызов (или reload передаёт уже прочитанные байты).
- _(FU-TOPLEVEL-INHIBIT-RULES S1, 2026-10-12)_ тот же корень: один reload читает файл 3–4 раза (`reload_coordinator.loadAndParse`, viper, `loadInhibitRulesYAML`, `loadRouteConfig`); подмена symlink ConfigMap между чтениями может склеить две версии. Фикс «прочитать байты один раз и передать дальше» закрывает и это.
- **Refs:** `go-app/internal/config/config.go` (`LoadConfig`, `loadRouteConfig`, `loadInhibitRulesYAML`), `reload_coordinator.go`; `tasks/archive/PROD-CONFIG-FILE-FALLBACK/review-findings.md` F5; `tasks/archive/FU-TOPLEVEL-INHIBIT-RULES/review-findings.md` S1.
- **Status:** open

### [medium][Security][~0.5d] CONFIG-VALIDATION-ERROR-REDACTION
- **Title:** ошибки валидации конфига печатают URL с секретами
- **Problem:** сообщения валидаторов (E114/E116, проверка `external_url`) эхом печатают значение: Slack webhook URL (это credential), userinfo в `external_url`. С PROD-CONFIG-FILE-FALLBACK невалидный конфиг — ERROR `failed to load configuration` и exit 1, т. е. строка повторяется на каждом рестарте crash loop. Не регресс: раньше то же уходило в WARN. Ошибки YAML и mapstructure значений не печатают.
- **Impact:** секреты ресиверов попадают в логи пода и в агрегатор логов.
- **Fix:** редактировать userinfo, query и path URL в сообщениях валидаторов (показывать схему и хост).
- **Refs:** `go-app/internal/config`, `cmd/server/main.go`; review-findings F6.
- **Status:** open

### [medium][Grouping][~1d] TIMER-STORAGE-KEY-LOSS-SILENCES-FIRE
- **Title:** пропажа ключа таймера в Redis глушит живой локальный таймер
- **Problem:** после `GROUPING-TIMER-LOCK-FIX` срабатывание сверяется с записью в timer storage, и отсутствие записи значит «уже обработано» (`fireStillDue`, `not_found`). Если ключ пропал не из-за обработчика — eviction (`allkeys-lru`), `FLUSHDB`, failover на отстающую реплику Redis, — локальный таймер тоже пропускает срабатывание, а reconciliation ключа не видит. Различить «удалён обработчиком» и «потерян» без отдельного маркера нельзя. У group storage есть failback (`StorageManager`), у timer storage — нет (`service_registry.go:1897-1911`).
- **Impact:** группа молчит до следующего алерта в ней; только HA-режим с Redis. _(PROD-GROUPING-DEFAULT, 2026-10-10: следующий алерт перевзводит таймер через `ensureGroupTimer`; группировка теперь включена по умолчанию, а Redis чарта по умолчанию `allkeys-lru` — см. `HELM-REDIS-NOEVICTION` в BACKLOG.)_
- **Fix:** маркер обработанного срабатывания (например, `fired:{groupKey}:{expiresAt}` с TTL) вместо отсутствия записи, либо требование `noeviction` для Redis AMP в чарте и доках.
- **Refs:** `go-app/internal/infrastructure/grouping/timer_manager_impl.go` (`fireStillDue`, `skipHandledFire`); `tasks/archive/GROUPING-TIMER-LOCK-FIX/review-findings.md` F6.
- **Status:** open

### [medium][Grouping][~1d] NOTIFY-LOG-FALLBACK-LIMITS
- **Title:** ограничения `resilientNotifyLog` при недоступном журнале нотификаций
- **Problem:** обёртка над `RedisNotifyLog` при ошибке чтения отвечает из локальной памяти реплики. (1) Локальная запись может быть устаревшей: другая реплика успела отправить более новый набор — ответ «дубликат» придерживает target, покрытым он не считается, prune не выполняется (это намеренно, Spec п.8, 10). (2) При сбое дольше `repeat_interval` группа с несколькими target'ами не оседает: локальные записи истекают в разное время, каждый раз один target получает fail-open отправку, другой придержан — resolved-алерт уходит каждому target'у раз в `repeat_interval`, пока журнал не читается. (3) Нет метрики состояния fallback — видно только по Warn-логу.
- **Impact:** при длительной недоступности Redis — повторы resolved раз в `repeat_interval` на target; после восстановления первый flush удаляет алерт. Оператор не видит по метрикам, что дедуп работает из локальной памяти.
- **Fix:** gauge/counter «nflog отвечает локально»; для (2) — общий срок годности локальных записей группы либо отказ от fail-open для resolved-only набора.
- **Refs:** `go-app/internal/infrastructure/grouping/notify_log_fallback.go`; `tasks/archive/PROD-GROUPING-DEFAULT/review-findings.md` K-серия, M1, N1; `Spec.md` Edge Cases 19–20.
- **Status:** open

### [medium][Grouping][~0.5d] GROUP-CLEANUP-NOT-WIRED
- **Title:** `CleanupExpiredGroups` не вызывается в проде
- **Problem:** `CleanupExpiredGroups`/`RemoveAlertFromGroup` вне тестов не вызываются; авто-резолва нет. Группа, чьи resolved-алерты не удаётся доставить (или алерт без resolved), тикает каждый `group_interval` бессрочно — в HA ~10–12 round-trip'ов Redis на тик; алерт без resolved напоминает каждый `repeat_interval`. По чтению кода.
- **Impact:** рост числа групп и фоновой нагрузки на Redis со временем; с группировкой по умолчанию касается всех установок с `route:`.
- **Fix:** периодический вызов `CleanupExpiredGroups` из timer manager/registry с порогом по `UpdatedAt`; тест на снятие таймера вместе с группой.
- **Refs:** `tasks/archive/PROD-GROUPING-DEFAULT/review-findings.md` F8, G7.
- **Status:** open

## Low

### [low][Config][~0.25d] INHIBIT-CONFIG-LOUDNESS
- **Title:** мелкие шероховатости загрузки inhibit-правил
- **Problem:**
  - Индексы ошибок валидатора для вложенных правил сдвигаются, если есть верхнеуровневые: правило `inhibition.inhibit_rules[j]` репортится как `inhibit_rules[len(top)+j]` (`go-app/internal/config/alertmanager_validation.go:101-105`) (F6).
  - Ключи секции в другом регистре (`Inhibition:`, `Inhibit_Rules:`, `Source_Match:`) yaml.v3 не узнаёт, и правила под ними пропадают молча (`config.go`, `loadInhibitRulesYAML`). Upstream такие ключи отвергает как неизвестные; в CHANGELOG — migration note (R2 N2).
  - `reload_coordinator.go:645-646` маппит `inhibit_rules`/`inhibition` в компонент, а diff отдаёт имена Go-полей (`InhibitRules`/`Inhibition`) ⇒ `affected_components` для ингибиции всегда пуст. На применение не влияет: правила пересобираются на каждом reload (F7).
- **Impact:** вводящие в заблуждение путь в ошибке, `affected_components` и молча пропущенная секция.
- **Fix:** валидировать списки раздельно (или перенумеровывать в `formatConfigValidatorErrors`); ошибка на ключ, совпадающий с известным без учёта регистра; маппинг компонентов по mapstructure/yaml-имени.
- **Refs:** `tasks/archive/FU-TOPLEVEL-INHIBIT-RULES/review-findings.md` F6, F7, N2.
- **Status:** open

### [low][Grouping][~0.25d] TIMER-ACTIVE-GAUGE-DRIFT
- **Title:** `alert_history_timer_active_total` расходится с числом таймеров
- **Problem:** `Inc`/`Dec` гейджа не сбалансированы на нескольких путях `DefaultTimerManager`: `StartTimer` заменяет существующий handle без `Dec` (`timer_manager_impl.go:441-445`), и если сработавший handle заменён внешним `StartTimer` во время проверки записи, `Inc` старого не компенсируется; ветки `gm == nil` и `GroupNotFound` в `onTimerExpired` удаляют handle без `Dec`; срабатывание с `nil`-handle (restore/reconcile) делает `Dec` без `Inc`. Ранний выход через `dropLocalHandle` сбалансирован в `GROUPING-TIMER-LOCK-FIX`.
- **Impact:** гейдж годится только для тренда; на доставку не влияет. _(PROD-GROUPING-DEFAULT G7: ещё один путь — удаление группы из callback'а даёт двойной `DecActiveTimers`, гейдж уходит в минус.)_
- **Fix:** считать гейдж от `len(tm.timers)` (GaugeFunc) вместо `Inc`/`Dec` по путям.
- **Refs:** `tasks/archive/GROUPING-TIMER-LOCK-FIX/review-findings.md` N3, F2.
- **Status:** open

### [low][Config][~0.1d] CONFIG-PATH-RESOLUTION-DUP
- **Title:** путь конфига разрешается в двух местах по-разному
- **Problem:** `cmd/server` берёт `AMP_CONFIG_FILE` с `TrimSpace` (`resolveRuntimeConfigPath`), reload в `service_registry.go:327-330`, `:2642-2645` — без. Значение из пробелов или с пробелом в начале старт и reload разрешают по-разному. Существовало до PROD-CONFIG-FILE-FALLBACK.
- **Impact:** редкий: reload читает другой файл, чем старт.
- **Fix:** передавать в `ServiceRegistry` путь, уже разрешённый в `main`.
- **Refs:** review-findings F8.
- **Status:** open

### [low][Gate][~0.1d] RELEASE-GATE-UNQUOTED-ARGS
- **Title:** `scripts/release-gate.sh` собирает аргументы helm строкой без кавычек
- **Problem:** `args="-f $HELM_CHART_DIR/values-dev.yaml"` и `-f $HELM_CHART_DIR/tests/values-production-placeholders.yaml` раскрываются word-split'ом: путь с пробелом ломает шаг, `[`/`*` в значениях подвержены glob-раскрытию (поэтому placeholder'ы PROD-INGRESS-HARDENING ушли в файл, а не в `--set-json`).
- **Impact:** чекаут в каталоге с пробелом роняет helm-шаги гейта; следующий `--set` со спецсимволами — тихая порча аргументов.
- **Fix:** bash-массивы аргументов в `step_helm_values`/`step_helm_rbac`.
- **Refs:** deep-review PROD-INGRESS-HARDENING R14; 2026-10-01.
- **Status:** open

### [low][Grouping][~0.5d] GROUP-HASTIMER-REDIS-GET-PER-ALERT
- **Title:** `HasTimer` делает Redis GET на каждый алерт у реплики без локального handle
- **Problem:** `ensureGroupTimer` спрашивает `HasTimer`; у реплики, не владеющей таймером группы, это поход в Redis — в HA (N−1)/N алертов. При ошибке GET пишутся Warn и Error на каждый алерт, без rate-limit.
- **Impact:** лишний round-trip на ingest-пути и шум в логах при сбое Redis; на корректность не влияет.
- **Fix:** короткий локальный кэш «таймер есть» на группу; rate-limit логов ошибки.
- **Refs:** `tasks/archive/PROD-GROUPING-DEFAULT/review-findings.md` H9; `go-app/internal/infrastructure/grouping/manager_impl.go` (`ensureGroupTimer`), `timer_manager_impl.go` (`HasTimer`).
- **Status:** open

### [low][Grouping][~0.5d] LEGACY-REPEAT-TIMER-AFTER-UPGRADE
- **Title:** legacy-таймер `repeat_interval` после апгрейда держит задержку до своего срока
- **Problem:** группа, пережившая апгрейд с таймером типа `repeat_interval`, считается «с таймером» (`HasTimer` = true), перевзвод не срабатывает; `RestoreTimers` такой таймер в `group_interval` не конвертирует — timer manager не знает `group_interval` группы. Новый алерт в такой группе ждёт остаток `repeat_interval` (до 4h), один раз на группу.
- **Impact:** разовая задержка нотификации после апгрейда HA-установки с живыми группами; названо в migration note п.10.
- **Fix:** при `RestoreTimers` сжимать legacy-таймер до `min(остаток, group_interval)` — нужен доступ к параметрам маршрута группы.
- **Refs:** `tasks/archive/PROD-GROUPING-DEFAULT/review-findings.md` H7.
- **Status:** open

### [low][Grouping][~0.5d] REDIS-GROUP-STORAGE-UNLOCKED-WRITES
- **Title:** `RedisGroupStorage.Store`/`Delete` без блокировки группы и проверки версии
- **Problem:** `RedisGroupStorage.Store` делает `json.Marshal` и `Version++` без `group.mu` (в `MemoryGroupStorage.Store` это исправлено в PROD-GROUPING-DEFAULT); `storage.Delete` не проверяет версию, поэтому проверка «алерт всё ещё resolved» перед prune атомарна только в пределах объекта группы одной реплики. Пробой не воспроизведён: в Redis-режиме объект группы обычно свой у каждого вызова.
- **Impact:** теоретическая гонка между репликами: prune может удалить группу, в которую другая реплика только что добавила алерт.
- **Fix:** `Store` под `group.mu.RLock`; `Delete` с проверкой версии (Lua/WATCH).
- **Refs:** `tasks/archive/PROD-GROUPING-DEFAULT/review-findings.md` M3, M4; `Spec.md` п.10 (оговорка про Redis).
- **Status:** open

### [low][Config][~0.25d] GROUPING-RELOAD-NO-RESTART-HINT
- **Title:** `/-/reload` с изменённым `grouping.*` не сообщает «restart required»
- **Problem:** `grouping.enabled` и остальные ключи `grouping.*` читаются один раз при старте; `reload_coordinator.go` числит `grouping` компонентом, но изменение этих ключей на reload молча не применяется и предупреждения W6xx нет. Предсуществующее.
- **Impact:** оператор меняет `grouping.enabled` в файле, делает reload и считает, что режим сменился.
- **Fix:** предупреждение W6xx «restart required» при изменении `grouping.*` на reload.
- **Refs:** `tasks/archive/PROD-GROUPING-DEFAULT/review-findings.md` F11; `go-app/internal/config/reload_coordinator.go`.
- **Status:** open

### [low][Config][~0.25d] DB-CONN-STRING-UNESCAPED
- **Title:** `ConnectionString()` и `config.GetDatabaseURL()` собирают строку подключения без экранирования
- **Problem:** `PostgresConfig.ConnectionString()` (`internal/database/postgres/config.go`, key=value через `fmt.Sprintf`) и `Config.GetDatabaseURL()` (`internal/config/config.go`, URL через `fmt.Sprintf`) подставляют пользователя и пароль как есть — тот же дефект, что был в `DSN()` до HELM-EXTERNAL-DATABASE: пароль с пробелом, `'`, `/ # ? %` ломает разбор или молча меняется. Дополнительно `GetDatabaseURL()` подставляет `sslmode=disable` при пустом значении, тогда как рантайм по умолчанию берёт `require`. В рантайме не вызываются: единственные вызовы — `internal/config/example.go:94` и `pool_test.go:322`.
- **Impact:** сейчас нет; ловушка для следующего, кто возьмёт эти функции вместо `DSN()`.
- **Fix:** удалить обе (и пример/тест) либо собрать через `url.URL`, как `DSN()`.
- **Refs:** `tasks/HELM-EXTERNAL-DATABASE/research.md`, `evidence/dsn-password-escaping.txt`; 2026-10-16.
- **Status:** open

## Entry Format

```markdown
### [priority][area][estimate] DEBT-SLUG
- **Title:** short title
- **Problem:** what makes maintenance risky
- **Impact:** why it matters
- **Fix:** likely direction
- **Refs:** code, issue, task, or review links
- **Status:** open | in-progress | blocked
```

## Bundle Format

```markdown
### AREA-CLEANUP-BUNDLE
- [priority][area][combined estimate] AREA-CLEANUP-BUNDLE
- **Combined verify:** <одна команда; exit 0 — вся пачка готова>
- **Members (ordered):**
  - [ ] DEBT-SLUG-ONE
  - [ ] DEBT-SLUG-TWO
```

Полные записи остаются у участников; приоритет родителя — максимум по участникам.
