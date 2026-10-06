---
id: PROD-GRACEFUL-SHUTDOWN
slug: prod-graceful-shutdown
stream: Production Readiness / Reliability
type: bug
priority: high
status: active
created_at: 2026-10-06
updated_at: 2026-10-06
---

# Requirements: корректный graceful shutdown AMP

## Problem Framing

- **Symptom:** при остановке pod'а (rolling update, drain ноды, scale-down) AMP может отвечать 5xx на запросы, которые уже приняты, и терять работу, которая ещё не завершилась (flush, доставка, финальный snapshot). Трафик продолжает приходить на pod, который уже останавливается.
- **Root Cause:** четыре дефекта в `go-app/cmd/server/main.go:166-183` и в чарте:
  1. Порядок перевёрнут: `registry.Shutdown` вызывается раньше `server.Shutdown`, поэтому in-flight запросы попадают в уже остановленные сервисы (alert processor, хранилища).
  2. `main` завершается сразу, как только `ListenAndServe` вернул `ErrServerClosed`, и не ждёт shutdown-горутину. Drain и flush в `registry.Shutdown` обрываются на середине, включая финальную запись file snapshot в lite (`service_registry.go:2318`).
  3. Таймаут shutdown (30s) равен `terminationGracePeriodSeconds: 30`. Запаса нет, и SIGKILL приходит раньше, чем закончится drain.
  4. Нет переключения readiness по SIGTERM и нет preStop. `gracefulShutdown.preStopDelay: 5` в `values.yaml:249` нигде не используется, а `templates/deployment.yaml:214` явно говорит «No preStop hook needed». Endpoint'ы Service и Ingress продолжают слать трафик на pod, пока kube-proxy/контроллер не узнает об удалении.
- **Why Now:** блокер P0 Production Readiness (`BACKLOG.md` § P0 — Reliability). Без него каждый rolling update — это окно потери алертов и ошибок у отправителей. `PROD-RELEASE-V010` на паузе по решению владельца, поэтому следующий незаблокированный P0 — этот.
- **How We Measure:** тест на порядок shutdown (сначала HTTP drain, потом registry, процесс выходит только после обоих). Рендер чарта: preStop присутствует, а таймаут shutdown в приложении меньше grace period за вычетом preStop. Остановка под нагрузкой не даёт 5xx и потерянных алертов (сценарий — см. Open unknowns).

## Risk Profile

- **Signals:** `R X`
  - `R` — путь остановки процесса в проде и поведение pod'а при rolling update/drain. Ошибка здесь видна как 5xx у Prometheus и потерянные уведомления.
  - `X` — две границы: Go-бинарь (`cmd/server`, `internal/application`) и Helm-чарт (`deployment.yaml`, `values.yaml`, `values-production.yaml`).
- **Tier:** Standard
- **Notes:** `M` рассмотрен и не выставлен: меняется момент записи финального snapshot/flush, а не его содержимое или формат, и необратимых операций нет. `C` не выставлен: HTTP API и формат ответов `/-/ready` не меняются, кроме 503 во время остановки, а для readiness-endpoint это штатное значение. Новые ключи values только добавляются. Если research покажет, что для фикса нужно менять семантику существующих ключей чарта или контракт `Shutdown` у сервисов, тир пересматривается.

## User Stories

1. Как оператор, я хочу, чтобы rolling update AMP проходил без 5xx у Prometheus и без потерянных алертов, чтобы обновлять релиз в рабочее время.
2. Как оператор lite-профиля, я хочу, чтобы финальный snapshot писался после того, как приняты последние запросы, чтобы после рестарта не терять silences и notification log.
3. Как оператор, я хочу задавать задержку перед остановкой и grace period в values так, чтобы они реально работали и были согласованы между собой.

## Success Criteria

- [ ] По SIGTERM/SIGINT `/-/ready` отвечает 503 сразу, а `/-/healthy` продолжает отвечать 200, чтобы liveness не убил pod во время drain.
- [ ] Порядок остановки: readiness 503 → задержка (preStop или внутри процесса, решение — на `/spec`) → `server.Shutdown` (новые соединения не принимаются, in-flight дорабатывают) → `registry.Shutdown` → выход из `main`.
- [ ] `main` возвращает управление только после завершения shutdown-последовательности. Ошибки shutdown видны в логе и в коде выхода.
- [ ] Таймаут shutdown в приложении настраивается и по умолчанию строго меньше `terminationGracePeriodSeconds` с учётом задержки. Чарт не даёт отрендерить несогласованную комбинацию (или явно её документирует, решение — на `/spec`).
- [ ] `gracefulShutdown.preStopDelay` используется в шаблоне. Комментарий «No preStop hook needed» убран или заменён правдой.
- [ ] Unit-тест на порядок shutdown и на то, что `main` ждёт завершения. Render-тест чарта в `helm/amp/tests/` (входит в шаг `helm-tests` release-gate).
- [ ] Проверка остановки под нагрузкой: без 5xx и потерянных алертов (форма проверки — см. Open unknowns).
- [ ] `CHANGELOG.md` `[Unreleased]`: запись о новом поведении. Если меняются дефолты чарта, то с migration notes. `helm/amp/CHANGELOG.md` — при изменении чарта.
- [ ] Гейты AMP из `WORKFLOW.md` § Гейты AMP, включая `scripts/release-gate.sh`, зелёные, или известные красные задокументированы (флейк `GROUPING-TIMER-LOCK-RELEASED-BEFORE-LOSER`).

## Non-Goals

- Перестройка порядка остановки внутри `ServiceRegistry.Shutdown`, кроме того, что прямо требуется для корректного drain. Аудит каждого сервиса — отдельная задача, если research найдёт проблемы.
- Postgres HA, PDB, бэкапы (`PROD-POSTGRES-HA-DECISION`).
- Удаление мёртвого `internal/application/application.go` (`DEAD-APPLICATION-MIDDLEWARE`), даже если там есть своя shutdown-логика.
- Shutdown sidecar `config-reloader`.
- Перевод `e2e-ha` в required (`CI-E2E-HA-REQUIRED`).

## Constraints

- **Scope:** `go-app/cmd/server/main.go`, readiness-обработчик (`internal/application/handlers`, `router.go:45`), точечно `ServiceRegistry` (флаг «останавливаемся»), чарт: `templates/deployment.yaml`, `values.yaml`, `values-production.yaml`, тесты чарта.
- **Security:** неприменимо. Auth (`webAuth.Wrap`) остаётся как есть. `/-/ready` уже может быть в `unauthenticated_paths`, и это не меняется.
- **Compatibility:** поведение upstream Alertmanager по SIGTERM сверить на research (он тоже переключает readiness?). Новые ключи values добавляются с безопасными дефолтами. Если меняется дефолт `terminationGracePeriodSeconds`, это строка в CHANGELOG. Runtime-образ — `alpine:3.24` (`Dockerfile:39`), так что `sleep` в preStop `exec` доступен. Нативное действие `lifecycle.preStop.sleep` требует свежего Kubernetes, а `kubeVersion` в `Chart.yaml` не задан.

## Discovery Notes

- Similar tasks: не найдено (`tasks/archive/`, `DONE.md`, `archive/DONE-*.md`). Упоминания — только в `tasks/archive/PROD-AUTH/research.md`, `Spec.md` и `tasks/archive/PROD-RELEASE-V010-PREP/research.md`.
- Relevant patterns:
  - `watchReloadSignal` (`main.go`) — отдельная горутина на сигнал с тестом. Образец для выноса shutdown-логики в тестируемую функцию.
  - `ServiceRegistry.Shutdown` (`internal/application/service_registry.go:2318`) — финальный snapshot «deliberately FIRST», обратный порядок инициализации.
  - `AlertmanagerReadyHandler` (`internal/application/handlers`) — точка переключения readiness.
  - render-тесты чарта — `helm/amp/tests/*.sh`, шаг `helm-tests` в `scripts/release-gate.sh`.
- Open unknowns:
  - Где держать задержку: preStop `exec sleep` в чарте, задержка внутри процесса после SIGTERM, или и то и другое. Задержка в процессе работает и вне Kubernetes (docker-compose, `e2e-ha`), а preStop — стандарт для k8s. Решить на `/research` и `/spec`.
  - Как проверить «rolling update под нагрузкой без 5xx», если `deploy/e2e-ha` — docker-compose, а не k8s, и живого кластера нет (kind не установлен, см. PROD-INGRESS-HARDENING). Кандидат — `compose stop` реплики под непрерывным POST в `e2e-ha`, либо отдельный локальный сценарий. Сам k8s rolling update тогда остаётся проверенным только рендером.
  - Нет ли других мест, которые слушают SIGTERM или вызывают `os.Exit` в обход shutdown (горутины, `log.Fatal`).
  - Что делает `ServiceRegistry.Shutdown` с фоновыми воркерами (публикация, timers grouping): дожидается ли он их или только отменяет контекст.
  - Поведение upstream Alertmanager при SIGTERM — для parity и документации.
