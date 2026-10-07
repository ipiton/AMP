# BUGS

Код, нарушающий свой контракт (граница с долгом — `docs/solo-kanban/artifact-contract.md` § Bug Versus Tech Debt). Исправленный баг удаляется отсюда; отчёт — в `DONE.md`.

## Open

<!-- Записи в виде списка (`- [ ] **SLUG** — ...`) — формат до Solo Kanban 1.1. Переводятся в Entry Format при взятии в работу. -->

- [ ] **GROUPING-TIMER-LOCK-RELEASED-BEFORE-LOSER** — `TestDefaultTimerManager_TwoReplicasRaceSameGroupTimer_OnlyLockWinnerFires` (`go-app/internal/infrastructure/grouping/distributed_timer_ownership_test.go:169`) флейкает под `-race`: callback срабатывает **2 раза вместо 1** («two replicas racing the same group's timer must fire exactly once»). Частота: 2/30 на чистом `main`@`8bca192` и 3/30 на ветке `feature/prod-ci-images` (`go test -race -count=30 -run …`, 2026-09-28). Предсуществующий: входит в шаг `race` release-gate, в research-прогоне PROD-CI-IMAGES не проявился случайно. Гипотеза по коду: `onTimerExpired` (`timer_manager_impl.go:871-899`) отпускает lock через `defer release()` сразу после callback'а, поэтому реплика, дошедшая до `AcquireLock` **после** этого, берёт свободный lock и срабатывает повторно. Lock защищает «одновременно», а не «уже сработало». Это TOCTOU, а не тайминг теста. В проде повторную доставку, по комментариям кода, должен отсекать nflog-claim (task 6.1), тест его стабит. Нужно проверить: защищён ли прод на самом деле (e2e-ha шаг 4 проверяет именно это) и что чинить — продукт (не отпускать lock до TTL / помечать fire в storage) или ожидание теста. 🔴 Бьёт по CI: `gate` — required check, а шаг `race` краснеет примерно в 1 прогоне из 15 только из-за этого теста. Найдено на `/implement` PROD-CI-IMAGES. Задача на исправление — `GROUPING-TIMER-LOCK-FIX` в BACKLOG. _(2026-09-29, PROD-DEPS-VULN: снова уронил шаг `race` release-gate; замер `-count=30` — чистый `main`@`9f54424` 2/30, частота прежняя.)_

- [ ] **ALERT-STORE-DEDUP-KEY-STARTSAT** — повторный `POST /api/v2/alerts` без `startsAt` создаёт в memory store **новую копию** алерта, а не обновляет существующую: ключ дедупликации стора — `fingerprint|startsAt` (`go-app/internal/infrastructure/storage/memory/alert_store.go`, `dedupKey`), а пустой `startsAt` на каждом POST превращается в `now`. В итоге `GET /api/v2/alerts` отдаёт один и тот же алерт N раз с разными `startsAt`. Upstream сливает по fingerprint и сохраняет самый ранний `startsAt`. Воспроизведено вживую на `main` и на ветке `PARITY-RESOLVE-TIMEOUT-ENDSAT` (2026-09-24, lite, два POST `{"labels":{"alertname":"X"}}`). Prometheus и `amtool` шлют `startsAt` сами, поэтому затронуты в основном curl и самописные клиенты. Из-за этого для них не работает и продление окна `resolve_timeout`. Не регресс. Найдено на `/testing` PARITY-RESOLVE-TIMEOUT-ENDSAT. Чинить отдельной задачей: для отсутствующего `startsAt` брать `startsAt` уже горящего алерта с тем же fingerprint. Смотреть осторожно: dedup в БД (`deduplication.go`) и resolved-путь стора завязаны на тот же ключ.

- [ ] **CONFIG-MISSING-FILE-DROPS-ENV** — если файла конфига нет (`AMP_CONFIG_FILE` не задан ⇒ `./config.yaml`, или путь неверный), `config.LoadConfig` возвращает ошибку `open ...: no such file or directory`: viper отдаёт `os.PathError`, а не `ConfigFileNotFoundError`, на который рассчитывает проверка в `LoadConfig` (`go-app/internal/config/config.go`). `cmd/server/main.go` на любую ошибку пишет `WARN "Config file not found, using defaults"` и подставляет минимальный `Config{Server: {Port: 9093}}` — **все переменные окружения и дефолты viper при этом теряются** (включая `SERVER_EXTERNAL_URL`, `PROFILE`, `DATABASE_*`, которые Helm передаёт через env при `configFile.enabled: false` — это дефолт чарта). Та же ветка глотает и настоящие ошибки валидации конфига: процесс стартует «на дефолтах» вместо отказа. Воспроизведено 2026-09-25 (`LoadConfig("/nonexistent/config.yaml")` с выставленным env ⇒ ошибка, env не прочитан). Найдено на `/implement` PROD-AUTH; для auth обойдено локально (путь к web config читается из env напрямую, `resolveWebConfigFile` в `main.go`), корень не тронут. 🔴 Масштаб: образ (`Dockerfile`) `config.yaml` не содержит, так что **дефолтный Helm-деплой целиком работает на минимальном фолбэке и игнорирует env из ConfigMap** — блокер Production Readiness. _(Подтверждено 2026-10-07: бинарь из `main` @ `fa682d4` без `config.yaml`, с `PROFILE=standard DATABASE_HOST=127.0.0.1` в env — `database host is required`, exit 1; дефолтный чарт `configFile.enabled: false` ⇒ crash loop в `standard`. Задача — `PROD-CONFIG-FILE-FALLBACK`, P0 в BACKLOG. Ошибки валидации тоже уходят в фолбэк: тот же бинарь с `config.yaml`, где webhook на `http://`, пишет `Config file not found, using defaults` с ошибкой `https_production` и снова теряет env.)_ Чинить отдельной задачей: отсутствие файла — не ошибка (продолжать с env + дефолтами), любая другая ошибка `LoadConfig` — выход с ошибкой, а не фолбэк. Проверить, не полагается ли на фолбэк кто-то в тестах/e2e. _(2026-10-06, PROD-GRACEFUL-SHUTDOWN: из-за этого бага не доходит и `gracefulShutdown.timeoutSeconds` → env `SERVER_GRACEFUL_SHUTDOWN_TIMEOUT`. Приложение подставляет 30s при нулевом таймауте (`effectiveShutdownTimeout`), ограничение описано в `helm/amp/README.md` § Graceful Shutdown. После фикса бага этот фолбэк останется только страховкой.)_

- [ ] **INVESTIGATION-ALERT-TIME-NOT-SET** — `investigation.WithAlertTime` (`go-app/internal/core/investigation/context.go`) нигде в рабочем коде не вызывается: `queue.processJobWithAgent` передаёт в `AgentLoop.Run` голый `q.ctx`. Tools (`prometheus_query_range`, `loki_query_range`) через `AlertTimeFromCtx` падают на фолбэк `time.Now()` и якорят окно ±15 мин на момент расследования, а не срабатывания алерта. Для ретраев (backoff до 60s × `max_retries`) и очереди под нагрузкой окно уезжает от события. Фикс: `ctx = investigation.WithAlertTime(ctx, job.Alert.StartsAt)` перед `Run` (и в 5A-пути, если tools появятся там) + тест, что tool видит `StartsAt`. README пакета уже помечает это как known gap. Не регресс. Найдено на `/research` PHASE-6B-RUNBOOK-ENGINE (2026-09-27).

- [ ] **AGENT-LOOP-UNKNOWN-KIND-NIL-RESULT** — `AgentLoop.Run` (`go-app/internal/core/investigation/agent_loop.go`, ветка `default` в `switch resp.Kind`) возвращает `(nil, err)`, а `InvestigationQueue.processJobWithAgent` (`go-app/internal/infrastructure/investigation/queue.go`) на ошибке сразу читает `agentRun.TerminationKind` ⇒ nil-pointer panic в воркере. Сейчас недостижимо из прод-клиента (`parseOpenAIAgentResponse` отдаёт только два известных kind), но любой новый `AgentLLMClient` или мок с иным kind роняет воркер. Фикс: возвращать `&AgentRunResult{TerminationKind: "error", ...}` и/или nil-guard в queue + тест. Не регресс. Найдено на `/implement` PHASE-6B-RUNBOOK-ENGINE (2026-09-27).

### [medium][Test][~0.25d] GROUPING-ORPHAN-ADOPTION-TEST-FLAKY
- **Title:** `TestInitializeGrouping_StandardReconciliationAdoptsOrphanFromCrashedReplica` флейкает
- **Problem:** `go-app/internal/application/service_registry_grouping_test.go:585` — `continuation timer type = "group_wait", want "group_interval"`. Замер 2026-10-07 под нагрузкой (параллельно с release-gate), `-count=80`: 6 падений на `main` @ `fa682d4`, 7 на ветке с runbook engine — предсуществующий, не от слияния. Без нагрузки на `main` 0/40. Гипотеза: тест ждёт `stats.ReconciledTimers >= 1` и сразу читает storage, а счётчик растёт раньше, чем `onGroupWaitExpired` записывает continuation `group_interval`, — тест читает ещё старую запись `group_wait`.
- **Impact:** шаг `test` required-гейта `gate` краснеет случайно; вместе с `GROUPING-TIMER-LOCK-RELEASED-BEFORE-LOSER` это второй флейк в grouping.
- **Fix:** в тесте ждать опросом, пока тип записи станет `group_interval` (с тем же дедлайном), а не читать один раз; если запись `group_wait` держится дольше дедлайна — это уже продуктовый дефект.
- **Refs:** release-gate 2026-10-07 на `feature/phase-6b-runbook-engine`, `~/amp-audit/gate-runbook.log`.
- **Status:** open

### [low][Test][~0.1d] HELM-TEST-SIGPIPE-PIPEFAIL
- **Title:** render-тесты чарта случайно падают на `printf: Broken pipe`
- **Problem:** `helm/amp/tests/render-networkpolicy.sh` (`assert_render_fails`, стр. 59) под `set -euo pipefail` делает `printf '%s' "${output}" | grep -q -F -e "${expected}"`. `grep -q` выходит на первом совпадении, `printf` получает SIGPIPE, и `pipefail` превращает успешное совпадение в провал: `FAIL ingressController only, Ingress off (wrong error: …)`, хотя текст ошибки ожидаемый. Поймано release-gate 2026-10-07; три повторных прогона скрипта — зелёные.
- **Impact:** шаг `helm-tests` required-гейта `gate` краснеет случайно на длинных выводах `helm template`.
- **Fix:** `grep -q -F -e "${expected}" <<<"${output}"` (here-string вместо пайпа) во всех `tests/*.sh`.
- **Refs:** `~/amp-audit/gate-runbook.log`, 2026-10-07.
- **Status:** open

### [high][Pipeline][~0.5-1d] HARDCODED-FILTER-DROPS-ALERTS
- **Title:** жёстко зашитый фильтр молча выбрасывает алерты, в том числе без LLM
- **Problem:** `SimpleFilterEngine.ShouldBlock` (`go-app/internal/core/services/filter_engine.go`) вызывается в transparent-режиме (`alert_processor.go:724`) и в enriched-режиме, то есть всегда. Правила зашиты в код: имя алерта с префиксом `test` без учёта регистра (`containsTest` проверяет только начало строки, хотя комментарий говорит «contains»), метка `environment=test|testing`, namespace `dev-sandbox` и `tmp` (`isDisabledNamespace`, захардкоженная map), повтор fingerprint+status в окне `DefaultDedupWindow` = 1 мин, resolved старше 24 ч, пустое имя. Заблокированный алерт возвращает `nil` — не ошибка; лог INFO, метрики нет (`// TODO: Record filter metrics`). Ключ `filters:` в `helm/amp/values.yaml` ни одним шаблоном не читается.
- **Impact:** алерт вроде `TestRunnerDown` или любой алерт из namespace `tmp` не доходит до ресивера, и оператор узнаёт об этом только из INFO-лога. Upstream Alertmanager так не делает — это расхождение не описано в `ALERTMANAGER_COMPATIBILITY.md`.
- **Fix:** правила по имени, `environment` и namespace — только за явным конфигом, по умолчанию выключены; дедуп за 1 мин сверить с upstream (повторная отправка Prometheus должна продлевать `endsAt`); счётчик `amp_alerts_filtered_total{reason}`; Known Gap в compat-доке. Задача — `PROD-HARDCODED-FILTER` (P0 в BACKLOG).
- **Refs:** аудит 2026-10-06; `filter_engine.go:85-250`, `alert_processor.go:717-800`.
- **Status:** open

### [low][Helm][~0.2d] SERVICE-METRICS-PORT-MISROUTED
- **Title:** порт `metrics` Service AMP ведёт на экспортёры postgres/redis, а не на AMP
- **Problem:** `templates/service.yaml` открывает `metrics` (`service.metricsPort`, 9090) с `targetPort: metrics` и селектором `amp.selectorLabels`. Под этот селектор попадают и pod'ы postgres/redis, у которых тоже есть порт `metrics` (экспортёры). AMP на 9090 ничего не слушает: метрики отдаются на `http` (`/metrics`), контейнерный порт `metrics` в `deployment.yaml` мёртвый. Запрос на `<release>-amp:9090` уходит экспортёру.
- **Impact:** всё, что скрейпит `metrics` Service AMP (старый ServiceMonitor, ручные конфиги Prometheus), получает метрики БД под видом AMP. Ловушка для оператора, не для приёма алертов.
- **Fix:** убрать порт `metrics` из Service и `containerPort` из Deployment (или ограничить селектор Service компонентом — только отдельным релизом, D3 в Spec PROD-INGRESS-HARDENING).
- **Refs:** ADR-015; `tasks/archive/PROD-INGRESS-HARDENING/Spec.md` D3; подтверждено deep-review (R10), 2026-10-01.
- **Status:** open

### [low][Helm][~0.2d] MONITORING-CRD-DEFAULT
- **Title:** дефолты чарта требуют CRD prometheus-operator
- **Problem:** `monitoring.prometheusEnabled: true` по умолчанию, поэтому `helm install` с дефолтами рендерит `ServiceMonitor` (AMP и redis) и `PrometheusRule` redis. В кластере без CRD `monitoring.coreos.com` install падает на `no matches for kind "ServiceMonitor"`.
- **Impact:** первый install в чистый кластер требует знать про `--set monitoring.prometheusEnabled=false`.
- **Fix:** гейтить на `.Capabilities.APIVersions.Has "monitoring.coreos.com/v1"` или сменить дефолт на `false` (breaking для тех, кто на него полагается).
- **Refs:** ADR-015; `tasks/archive/PROD-INGRESS-HARDENING/Spec.md` D7; 2026-10-01.
- **Status:** open

### [low][Helm][~0.25d] HELM-NAMESPACE-OVERRIDE-SPLIT
- **Title:** `namespace` разносит объекты AMP и redis по разным namespace
- **Problem:** шаблоны приложения ставят `namespace: {{ .Values.namespace | default .Release.Namespace }}`, а `redis-*.yaml` — `.Release.Namespace`. При `namespace` ≠ namespace релиза Redis, его Service и NetworkPolicy оказываются в другом namespace, чем AMP: политика redis выбирает AMP pod'ы через `podSelector` без `namespaceSelector` и их не видит, а `cache.host` указывает на Service не там.
- **Impact:** редкая конфигурация (`namespace` задан явно), но тихо: AMP теряет Redis при `valkey.networkPolicy.enabled`.
- **Fix:** один источник namespace для всех шаблонов (хелпер `amp.namespace`).
- **Refs:** deep-review PROD-INGRESS-HARDENING R16, `tasks/archive/PROD-INGRESS-HARDENING/review-findings.md`; 2026-10-01.
- **Status:** open

## Entry Format

```markdown
### [priority][area][estimate] BUG-SLUG
- **Title:** short title
- **Problem:** what is broken, with reproduction
- **Impact:** who is affected and how
- **Fix:** likely direction
- **Refs:** code, issue, task, or review links
- **Status:** open | in-progress | blocked
```
