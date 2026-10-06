---
id: PROD-GRACEFUL-SHUTDOWN
slug: prod-graceful-shutdown
stream: Production Readiness / Reliability
type: bug
status: draft
created_at: 2026-10-06
updated_at: 2026-10-06
based_on:
  - requirements.md
  - research.md
---

# Specification: корректный graceful shutdown AMP

**Version:** 1.0  
**Status:** Draft

## Summary

AMP по SIGTERM сначала снимает себя с readiness, потом дожидается in-flight HTTP-запросов, затем останавливает сервисы и выходит только после этого. Чарт получает preStop-задержку и согласованный бюджет времени: grace period ≥ preStop + таймаут приложения.

## Requirements Coverage

| Requirement / Criterion | Covered by |
|---|---|
| `/-/ready` 503 по сигналу, `/-/healthy` 200 | Target Design п.1; `ServiceRegistry.BeginShutdown`; unit-тест registry |
| Порядок readiness → задержка → `server.Shutdown` → `registry.Shutdown` → выход | Target Design п.1–3; `runShutdown`; тест порядка на фейках |
| `main` ждёт завершения, ошибки в логе и коде выхода | Target Design п.3; тест на канал завершения |
| Таймаут настраивается, < grace period | Target Design п.4–5; дефолты 40 ≥ 5 + 30; WARNING в `NOTES.txt`; render-тест |
| `preStopDelay` используется | Target Design п.5; render-тест |
| Unit-тест + render-тест | Component Architecture, Edge Cases |
| Проверка под нагрузкой | Go-тест с реальным `http.Server` (in-flight запрос завершается 200). k8s rolling update — Rollout / Rollback, риск |
| CHANGELOG | Impact Analysis |
| Гейты | `testing` по `WORKFLOW.md` § Гейты AMP |

## Current State

- **Code:**
  - `go-app/cmd/server/main.go:166-202`: горутина по SIGINT/SIGTERM вызывает `registry.Shutdown`, затем `server.Shutdown` с захардкоженным таймаутом 30s. `main` выходит сразу, как только `ListenAndServe` вернул `ErrServerClosed`.
  - Readiness: `ServiceRegistry.Readiness` (`service_registry_health.go:38`) обслуживает `/-/ready` и `/readyz`.
- **Data:** не применимо. Финальный file snapshot lite пишется в `ServiceRegistry.Shutdown` (`service_registry.go:2318`), формат не меняется.
- **Tests:** образец — `reload_signal_test.go` для `watchReloadSignal`. Тестов на shutdown нет. Render-тесты чарта — `helm/amp/tests/*.sh`.
- **Docs:**
  - `docs/CONFIGURATION_GUIDE.md:94`: `server.graceful_shutdown_timeout: 30s`, ключ не используется.
  - `helm/amp/values.yaml:247-249`: `gracefulShutdown.*`, `preStopDelay` не используется.
  - `deployment.yaml:213-214`: комментарий «No preStop hook needed».

## Design Premises

| Premise | Confirmed by | Class | If wrong |
|---|---|---|---|
| `/-/ready` и `/readyz` идут через `registry.Readiness`, а `/-/healthy` и `/healthz` — нет | `router.go:40-45`, `handlers/status.go:31,74`; других вызовов `Readiness(` в пути сервера нет (grep `go-app`) | call-path-traced | флаг в `Readiness` уронит liveness, и kubelet убьёт pod во время drain |
| Единственный обработчик SIGTERM/SIGINT в сервере — `main.go:169`, `log.Fatal`/`os.Exit` в runtime-пути нет | grep `signal.Notify`, `os.Exit`, `log.Fatal` по `go-app` (остальное — `config-reloader`, примеры, комментарии) | call-path-traced | второй обработчик обойдёт последовательность |
| Финальный snapshot в `registry.Shutdown` не зависит от `ctx` | `writeSnapshot(path string)`, `service_registry.go:2108,2338` | code-read | при исчерпанном бюджете lite потеряет финальное состояние |
| Время in-flight запроса ограничено `WriteTimeout: 30s` | `main.go` (`http.Server{WriteTimeout: 30s}`) | code-read | drain может съесть весь бюджет — см. Edge Cases 3 |
| `server.graceful_shutdown_timeout` читается из файла конфига и из env `SERVER_GRACEFUL_SHUTDOWN_TIMEOUT` как `time.Duration` | `config.go:260,649-650,810` (`AutomaticEnv`, replacer `.`→`_`, дефолт `"30s"`) | code-read | значение из чарта не дойдёт до приложения |
| В дефолтном Helm-деплое env не читается, таймаут будет `0` | `BUGS.md` `CONFIG-MISSING-FILE-DROPS-ENV`; фолбэк `Config{Server:{Port:9093}}` в `main.go` | code-read | без фолбэка на дефолт `server.Shutdown` отменится мгновенно |
| kubelet выполняет preStop до SIGTERM, а endpoint'ы pod'а снимаются параллельно с preStop. Grace period отсчитывается от начала preStop | документация Kubernetes (Pod Lifecycle, termination of Pods); в репозитории не проверялось | assumed | preStop не закрывает гонку с kube-proxy/Ingress, и 5xx остаются — см. Rollout / Rollback |
| В runtime-образе есть `/bin/sleep` | `Dockerfile:39` `FROM alpine:3.24` (busybox) | code-read | preStop падает, kubelet логирует `FailedPreStopHook` и шлёт SIGTERM сразу: задержки нет, pod не ломается |

## Target Design

1. **Readiness-флаг.** `ServiceRegistry` получает `shuttingDown atomic.Bool` и метод `BeginShutdown()`. После вызова `Readiness` возвращает `service shutting down`, а `ReadinessReport` добавляет проверку `shutdown: unhealthy` и `ready: false`. Liveness не трогается.
2. **Последовательность.** Shutdown-горутина из `main` выносится в функцию `runShutdown(sigCh, deps, timeout, logger) error` по образцу `watchReloadSignal`. Зависимости — узкие интерфейсы: `BeginShutdown()`, `server.Shutdown(ctx)`, `registry.Shutdown(ctx)`. Порядок: лог сигнала → `BeginShutdown()` → `ctx` с таймаутом → `server.Shutdown(ctx)` → `registry.Shutdown(ctx)` → возврат первой ошибки. `registry.Shutdown` вызывается и при ошибке `server.Shutdown`: сервисы всё равно надо остановить. Задержки внутри процесса нет: её даёт preStop.
3. **Ожидание в `main`.** Горутина пишет результат в `shutdownDone chan error`. После `ErrServerClosed` `main` читает канал. Ошибка → `slog.Error` и `os.Exit(1)`, иначе — «Server stopped gracefully». Любая другая ошибка `ListenAndServe` по-прежнему ведёт к `os.Exit(1)` без ожидания.
4. **Таймаут.** Берётся из `cfg.Server.GracefulShutdownTimeout`, при `≤ 0` подставляется `30s` (константа рядом с `runShutdown`). Один бюджет на всю последовательность: `registry.Shutdown` получает тот же `ctx`, а финальный snapshot от него не зависит.
5. **Чарт.**
   - `values.yaml`: `gracefulShutdown.timeoutSeconds: 30` (новый ключ), `preStopDelay: 5` (есть), `terminationGracePeriodSeconds: 40` (было 30).
   - `deployment.yaml`: env `SERVER_GRACEFUL_SHUTDOWN_TIMEOUT: "<timeoutSeconds>s"` и `lifecycle.preStop.exec: ["sleep", "<preStopDelay>"]` при `preStopDelay > 0`.
   - Проверка бюджета без `fail` (решение владельца 2026-10-06: рендер не ломаем, тир остаётся Standard): если `terminationGracePeriodSeconds < preStopDelay + timeoutSeconds`, `NOTES.txt` печатает `WARNING` с тремя числами и последствием (SIGKILL посреди drain).
   - Ложный комментарий в `deployment.yaml` заменяется правдой.

Итог в пяти предложениях: по сигналу процесс сразу отвечает «не готов» и даёт балансировщикам время (preStop) убрать его из ротации. Затем HTTP-сервер перестаёт принимать соединения и дожидается начатых запросов. После этого останавливаются сервисы: финальный snapshot, flush, закрытие хранилищ. `main` выходит только по завершении всей цепочки, а на всё отведён один таймаут. Дефолты чарта укладывают таймаут и preStop в grace period, а несогласованные values получают громкое предупреждение при install/upgrade.

## API Contracts

Не применимо: публичные эндпоинты не меняются. `/-/ready` и `/readyz` во время остановки отвечают 503 — это штатное значение readiness, тело в прежнем формате (`NOT READY` / JSON-отчёт с дополнительной проверкой `shutdown`).

## Data Model / Migrations

Не применимо.

## Component Architecture

- `go-app/internal/application/service_registry_health.go` — `BeginShutdown`, проверка флага в `Readiness` и `buildHealthReport`.
- `go-app/internal/application/service_registry.go` — поле `shuttingDown atomic.Bool`.
- `go-app/cmd/server/shutdown.go` (новый) — `runShutdown`, интерфейсы, `defaultGracefulShutdownTimeout`.
- `go-app/cmd/server/main.go` — проводка `runShutdown`, канал завершения, ожидание после `ErrServerClosed`.
- `go-app/cmd/server/shutdown_test.go` (новый):
  - порядок вызовов на фейках;
  - `registry.Shutdown` после ошибки HTTP;
  - фолбэк таймаута при `0`;
  - реальный `http.Server`: медленный in-flight запрос завершается 200 после сигнала, новые соединения отклоняются.
- `go-app/internal/application/service_registry_health_test.go` (или существующий тест health) — после `BeginShutdown` `Readiness` возвращает ошибку, liveness-отчёт без изменений.
- `helm/amp/values.yaml`, `templates/deployment.yaml`, `templates/NOTES.txt` — п.5 Target Design.
- `helm/amp/tests/render-graceful-shutdown.sh` (новый): дефолты дают preStop `sleep 5`, env `30s`, grace 40; `preStopDelay=0` — без preStop; несогласованный бюджет рендерится, а `NOTES.txt` содержит `WARNING` (проверка через `helm install --dry-run` или `helm template --show-only templates/NOTES.txt`, решить на `plan-task`).
- Документы:
  - `docs/CONFIGURATION_GUIDE.md` — фактическое поведение ключа и последовательность;
  - `helm/amp/README.md` — `gracefulShutdown.*`;
  - `CHANGELOG.md`, `helm/amp/CHANGELOG.md`.

## Security Design

- [x] Ownership validation — не применимо.
- [x] Input validation: несогласованные `timeoutSeconds`/`preStopDelay`/grace — WARNING в `NOTES.txt`, таймаут `≤ 0` в приложении — фолбэк на дефолт.
- [x] Sensitive data not logged: логируются только сигнал, этапы и ошибки.
- [x] Rate limiting — не применимо.
- [x] Auth/RBAC path: без изменений. `/-/ready` проходит через auth так же, как сейчас (в `unauthenticated_paths` по дефолту).

## Invariants

- [ ] Liveness (`/-/healthy`, `/healthz`) во время остановки не меняется.
- [ ] Финальный snapshot lite пишется и при исчерпанном бюджете.
- [ ] Без сигнала поведение сервера не меняется (readiness, обработка запросов, SIGHUP-reload).
- [ ] Чарт с дефолтами рендерится и проходит `helm-tests`, включая prod-рендер с `values-production-placeholders.yaml`.
- [ ] Новых зависимостей нет.

## Edge Cases

1. Второй SIGTERM/SIGINT во время остановки → игнорируется. Последовательность уже идёт, а kubelet пришлёт SIGKILL по grace period. Upstream тоже не форсирует выход.
2. `ListenAndServe` падает с ошибкой, отличной от `ErrServerClosed` (порт занят) → `os.Exit(1)` сразу, как сейчас, shutdown-горутина не ждётся.
3. Drain HTTP съедает весь бюджет → `server.Shutdown` возвращает `context.DeadlineExceeded`, `registry.Shutdown` вызывается с истёкшим `ctx`. Снимок пишется, остальные `Stop(ctx)` возвращаются быстро. Exit code 1, в логе обе ошибки.
4. `graceful_shutdown_timeout` = 0 или env потерян (баг `CONFIG-MISSING-FILE-DROPS-ENV`) → 30s.
5. `preStopDelay: 0` → preStop не рендерится, предупреждение проверяет `grace ≥ timeoutSeconds`.
6. Оператор явно задал `terminationGracePeriodSeconds: 30` → рендер проходит, `NOTES.txt` печатает WARNING (30 < 5 + 30). Pod работает, но долгий drain может прерваться SIGKILL. Строка в CHANGELOG рекомендует поднять grace.
7. Ctrl+C локально или `docker compose stop` → preStop нет, остановка сразу после drain, без лишней задержки.
8. Сигнал до завершения `registry.Initialize` → обработчик ещё не установлен, процесс завершается по дефолтному поведению Go, как сейчас. Не меняется.

## Impact Analysis

- **Affected modules:** `cmd/server`, `internal/application` (health), Helm-чарт `deployment.yaml`/`values.yaml`, документация.
- **Breaking changes:** none. Чарт: дефолт `terminationGracePeriodSeconds` 30 → 40 (rollout в худшем случае на 10s дольше), появляется preStop. Рендер не ломается ни при каких values — это осознанный выбор вместо `fail`, чтобы не вносить сигнал `C`. Приложение: нет.
- **New dependencies:** none.
- **Risks:**
  - Ограничение `CONFIG-MISSING-FILE-DROPS-ENV`: в дефолтном деплое `timeoutSeconds` из values до приложения не доходит, и оно использует 30s. Предупреждение в NOTES считает по values. Если оператор уменьшит `timeoutSeconds` и grace вместе, приложение всё равно возьмёт 30s и может получить SIGKILL посреди drain. Митигация: строка в README и CHANGELOG, связь с багом в `BUGS.md`. Полностью снимется починкой бага.
  - preStop зависит от `sleep` в образе — см. Design Premises.

## Rollout / Rollback

- **Rollout:** обычный релиз чарта и образа, нового флага нет. Новые pod'ы получают preStop и grace 40 при первом upgrade. Старые pod'ы завершаются по старым правилам.
- **Rollback:** `helm rollback` возвращает grace 30 и убирает preStop. Старый образ — прежний порядок shutdown. Состояние не меняется, откат без потерь.
- **Feature flag:** нет. Задержку выключает `preStopDelay: 0`.
- **Риск (`assumed`):** порядок «preStop ∥ снятие endpoint'ов → SIGTERM» взят из документации Kubernetes и в этом репозитории не проверен. Живого кластера нет (kind не установлен), поэтому rolling update без 5xx вживую не проверяется. В итоге задачи это будет отмечено как непроверенное. Go-тест покрывает только drain внутри процесса.

## Observability

- **Logs:**
  - `Received <signal>, shutting down` (signal, timeout);
  - `Readiness set to not ready`;
  - `HTTP server drained` (duration) или ошибка;
  - `Services stopped` (duration) или ошибка;
  - итог `Server stopped gracefully` или `Shutdown finished with errors`.
- **Metrics:** не применимо. Процесс завершается, скрейпить нечего.
- **Alerts:** не применимо.

## Deep Review

- **Mandatory triggers present:** none (сигналы `R X`, нет `S`/`M`, не pre-release, < 3 сигналов).
- **Discretionary triggers present:** none. Ожидаемый дифф ~150 LOC без тестов. `C+X` нет. Паттерн — копия `watchReloadSignal`.
- **Decision:** not applicable.

## Open Questions

- [ ] Порядок preStop и снятия endpoint'ов в Kubernetes — `assumed`, проверка возможна только на живом кластере. Принято как известное ограничение (Rollout / Rollback).
