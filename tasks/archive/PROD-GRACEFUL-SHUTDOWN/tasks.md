---
id: PROD-GRACEFUL-SHUTDOWN
slug: prod-graceful-shutdown
stream: Production Readiness / Reliability
type: bug
status: complete
created_at: 2026-10-06
updated_at: 2026-10-06
based_on:
  - requirements.md
  - research.md
  - Spec.md
---

# Implementation Plan: корректный graceful shutdown AMP

**Based on:** requirements.md / research.md / Spec.md v1.0
**Date:** 2026-10-06

## Touched Files

- `go-app/internal/application/service_registry.go` — поле `shuttingDown atomic.Bool`
- `go-app/internal/application/service_registry_health.go` — `BeginShutdown`, флаг в `Readiness` и `buildHealthReport`
- `go-app/cmd/server/shutdown.go` (новый) — `runShutdown`, интерфейсы, `defaultGracefulShutdownTimeout`
- `go-app/cmd/server/main.go` — проводка `runShutdown`, канал завершения, ожидание после `ErrServerClosed`
- `helm/amp/values.yaml` — `gracefulShutdown.timeoutSeconds: 30`, `terminationGracePeriodSeconds: 40`
- `helm/amp/templates/deployment.yaml` — env `SERVER_GRACEFUL_SHUTDOWN_TIMEOUT`, `lifecycle.preStop`, комментарий
- `helm/amp/templates/NOTES.txt` — WARNING при несогласованном бюджете
- `docs/CONFIGURATION_GUIDE.md`, `helm/amp/README.md`, `CHANGELOG.md`, `helm/amp/CHANGELOG.md`
- тесты (Phase 4): `go-app/cmd/server/shutdown_test.go`, `go-app/internal/application/service_registry_health_test.go`, `helm/amp/tests/render-graceful-shutdown.sh`

## Phase 1: приложение

> **Wave 1** - независимые шаги

- [x] **1.1** `ServiceRegistry`: поле `shuttingDown atomic.Bool`, метод `BeginShutdown()`. `Readiness` возвращает `service shutting down` первым условием. `buildHealthReport` при `readiness && shuttingDown` добавляет проверку `shutdown` (`status: unhealthy`, `required: true`), так что `ready: false`. Liveness-отчёт без изменений. <!-- verify: cd go-app && go build ./... && go vet ./internal/application/ -->
- [x] **1.2** `cmd/server/shutdown.go`:
  - интерфейсы `readinessGate` (`BeginShutdown()`) и `shutdowner` (`Shutdown(ctx) error`);
  - `const defaultGracefulShutdownTimeout = 30 * time.Second`;
  - `effectiveShutdownTimeout(d)` — `d ≤ 0` → дефолт;
  - `runShutdown(sigCh <-chan os.Signal, gate, server, registry, timeout, logger) error`: ждёт сигнал → лог (signal, timeout) → `gate.BeginShutdown()` → `ctx` с таймаутом от `context.Background()` → `server.Shutdown` (лог duration/ошибки) → `registry.Shutdown` всегда (лог duration/ошибки) → `errors.Join` ошибок;
  - логи — по Spec § Observability.

  <!-- verify: cd go-app && go build ./cmd/server/ && go vet ./cmd/server/ -->

> **Wave 2** - depends on Wave 1

- [x] **1.3** `main.go`: удалить старую горутину и захардкоженные 30s. `sigCh` + `signal.Notify(SIGINT, SIGTERM)`, `shutdownDone := make(chan error, 1)`, горутина `shutdownDone <- runShutdown(...)` с таймаутом `effectiveShutdownTimeout(cfg.Server.GracefulShutdownTimeout)`. После `ListenAndServe`: не `ErrServerClosed` → `os.Exit(1)` как сейчас, иначе `<-shutdownDone`, ошибка → `slog.Error` + `os.Exit(1)`, иначе `Server stopped gracefully`. <!-- depends: 1.1, 1.2 | verify: cd go-app && go build ./cmd/server/ && go vet ./cmd/server/ && grep -n "30 \* time.Second" cmd/server/main.go | wc -l → 0 -->
  - _Отклонение verify:_ `grep "30 \* time.Second"` даёт не 0, а 3 совпадения, все чужие (`ReadTimeout`/`WriteTimeout` сервера и таймаут SIGHUP-reload). Захардкоженный таймаут shutdown удалён.
- [x] **1.4** Ручной smoke lite: собрать бинарь, запустить с `PROFILE=lite`, во время `curl /-/ready` послать SIGTERM. В логе — последовательность Spec § Observability, код выхода 0, финальный snapshot записан. <!-- depends: 1.3 | verify: ручная проверка лога и `echo $?`; результат — строкой под шагом, в evidence не кладём (повторяемо) -->
  - _Результат 2026-10-06:_ lite, `graceful_shutdown_timeout: 7s` из файла. В логе по порядку: `Shutdown signal received` (timeout=7s) → `Readiness set to not ready` → `HTTP server drained` → `Writing final file snapshot` → `Services stopped` → `Server stopped gracefully`, exit 0. Фолбэк при отсутствии файла конфига smoke'ом не проверить: процесс уходит в standard и падает на Postgres до установки обработчика. Фолбэк покрывается unit-тестом 4.1.

**Phase verification:** `cd go-app && go vet ./cmd/server/... ./internal/application/... && go test ./cmd/server/... ./internal/application/...`

## Phase 2: чарт

- [x] **2.1** `values.yaml`: `gracefulShutdown.terminationGracePeriodSeconds: 40`, новый `timeoutSeconds: 30` (комментарий: передаётся в `server.graceful_shutdown_timeout`, пока открыт `CONFIG-MISSING-FILE-DROPS-ENV` — в дефолтном деплое приложение берёт 30s), `preStopDelay: 5` с правдивым комментарием. <!-- verify: helm lint helm/amp -->
- [x] **2.2** `deployment.yaml`:
  - `terminationGracePeriodSeconds` default 40;
  - env `SERVER_GRACEFUL_SHUTDOWN_TIMEOUT: "{{ timeoutSeconds }}s"`;
  - `lifecycle.preStop.exec.command: ["sleep", "<preStopDelay>"]` под `if gt (int preStopDelay) 0`;
  - комментарий «No preStop hook needed» заменить описанием последовательности.

  <!-- depends: 2.1 | verify: helm template amp helm/amp | grep -A4 preStop -->
- [x] **2.3** `NOTES.txt`: если `terminationGracePeriodSeconds < preStopDelay + timeoutSeconds` — `WARNING` с тремя числами и последствием (SIGKILL посреди drain). <!-- depends: 2.1 | verify: helm install amp helm/amp --dry-run=client --set gracefulShutdown.terminationGracePeriodSeconds=30 | grep WARNING -->
  - _Результат:_ WARNING при grace 30 печатается, при дефолтах — нет; `preStopDelay=0` убирает `lifecycle` из deployment AMP; prod-рендер (placeholders + пароли) содержит preStop `sleep 5`, env `30s`, grace 40; 5 существующих `helm/amp/tests/*.sh` — ok.

**Phase verification:** `helm lint helm/amp && helm template amp helm/amp -f helm/amp/values-production.yaml -f helm/amp/tests/values-production-placeholders.yaml >/dev/null && for t in helm/amp/tests/*.sh; do bash "$t" || echo "FAIL $t"; done`

## Phase 3: документация

- [x] **3.1** `docs/CONFIGURATION_GUIDE.md`: `server.graceful_shutdown_timeout` — фактическое поведение (бюджет на drain HTTP + остановку сервисов, `0` → 30s), последовательность остановки, связь с `gracefulShutdown.*` чарта. <!-- verify: git diff --check -->
- [x] **3.2** `helm/amp/README.md`: ключи `gracefulShutdown.*`, правило бюджета, зависимость preStop от `sleep` в образе, ограничение `CONFIG-MISSING-FILE-DROPS-ENV`. <!-- verify: git diff --check -->
- [x] **3.3** `CHANGELOG.md` `[Unreleased]` (Fixed: порядок shutdown, readiness 503, ожидание; Changed: grace 30 → 40, preStop, рекомендация поднять явно заданный grace) и `helm/amp/CHANGELOG.md`. <!-- verify: git diff --check -->

**Phase verification:** `git diff --check`

## Phase 4: тесты (write-tests)

- [x] **4.1** `cmd/server/shutdown_test.go`, фейки с журналом вызовов:
  - порядок `BeginShutdown` → `server.Shutdown` → `registry.Shutdown`;
  - `registry.Shutdown` вызывается при ошибке `server.Shutdown`, обе ошибки в результате;
  - `ctx` переданный в Shutdown имеет дедлайн ≈ timeout;
  - `effectiveShutdownTimeout(0)` и отрицательный → 30s.

  <!-- verify: cd go-app && go test -race -run 'Shutdown' ./cmd/server/ -->
- [x] **4.2** `cmd/server/shutdown_test.go`, реальный `http.Server` на `127.0.0.1:0`: медленный handler (держит запрос до сигнала из теста) → сигнал → in-flight запрос получает 200, новое соединение после завершения `server.Shutdown` отклоняется, `runShutdown` вернул `nil`. <!-- verify: cd go-app && go test -race -count=20 -run 'Shutdown' ./cmd/server/ -->
- [x] **4.3** `internal/application`: после `BeginShutdown` `Readiness` → ошибка, `ReadinessReport["ready"] == false` с проверкой `shutdown`, `LivenessReport` не содержит `shutdown`. Fixture registry — по образцу `service_registry_storage_test.go`. <!-- verify: cd go-app && go test -race -run 'Shutdown|Readiness' ./internal/application/ -->
- [x] **4.4** `helm/amp/tests/render-graceful-shutdown.sh` по образцу `render-image-tag.sh`:
  - дефолты: preStop `sleep 5`, env `30s`, grace 40, без WARNING;
  - `preStopDelay=0`: нет `lifecycle`;
  - grace 30: рендер проходит, WARNING в `helm install --dry-run=client`;
  - prod-рендер с placeholders — с preStop.

  <!-- verify: bash helm/amp/tests/render-graceful-shutdown.sh -->

**Phase verification:** `cd go-app && go test -race ./cmd/server/... ./internal/application/...` и `bash helm/amp/tests/render-graceful-shutdown.sh`

_Результат 2026-10-06:_
- `go test -race -count=20 -run Shutdown ./cmd/server/` — 140/140 (7 тестов × 20).
- `go test -race -run 'BeginShutdown|Readiness' ./internal/application/` — 10/10.
- `render-graceful-shutdown.sh` — 17/17.
- Мутация «registry до HTTP» (старый порядок) роняет все три теста последовательности: `OrderAndBudget`, `StopsServicesWhenDrainFails`, `DrainsInFlightRequestBeforeServices`.
- 4.3 сделан на фейке `contractStorageRuntime` (`router_contract_test.go`), без полного `Initialize`, плюс проверка проб через `AlertmanagerReadyHandler`/`AlertmanagerHealthyHandler`.
- Пробел: `main` (ожидание `shutdownDone`, код выхода 1) unit-тестом не покрыт — `main()` не вызывается из тестов. Покрыто smoke 1.4 (exit 0) и чтением кода.

## Phase 5: testing и finalize

- [x] **5.1** Гейты `WORKFLOW.md` § Гейты AMP: `go vet`/`go test` затронутых пакетов, `make -C go-app quality-gates-fast` + `git status`, `scripts/release-gate.sh`. Флейк `GROUPING-TIMER-LOCK-RELEASED-BEFORE-LOSER` — известный, не скрывать. <!-- verify: scripts/release-gate.sh -->
- [x] **5.2** `git diff --check`, нет `_, _ :=` в диффе. <!-- verify: git diff main...HEAD --check && git diff main...HEAD | grep -n '^+.*_, _ :=' ; test $? -eq 1 -->
  - _Результат 2026-10-06 (`/testing`):_
    - Затронутые пакеты: `go vet` + `go test ./cmd/server/... ./internal/application/...` — 514/514. `-race`: `cmd/server` `-count=20` 140/140, `internal/application` 10/10 (см. Phase 4).
    - `make -C go-app quality-gates-fast` — PASS, `go fmt` файлов не переписал (`git status` чистый).
    - `scripts/release-gate.sh` — **RESULT: PASS**: build 22s, lint 151s, test 256s, futureparity 40s, race 625s, helm-deps 10s, helm-dev, helm-production, helm-rbac 2s, helm-tests 6s (в том числе новый `render-graceful-shutdown.sh`), amtool-compat 586s. Флейк `GROUPING-TIMER-LOCK-RELEASED-BEFORE-LOSER` в этом прогоне не проявился, и это не значит, что он исправлен.
    - Шаг `race` release-gate покрывает 7 пакетов, `cmd/server` и `internal/application` в него не входят. Для них `-race` прогнан отдельно (выше).
    - `git diff main --check`: найдены 2 хвостовых пробела (markdown-переносы из шаблонов в `Spec.md`/`tasks.md`), убраны. `_, _ :=` в коде диффа нет. Единственное совпадение — текст самого шага 5.2.
    - `deploy/e2e-ha/run.sh` (вне гейта) — **ALL PASS**, все 5 сценариев, включая `compose restart amp-b` (SIGTERM → новый путь остановки) и `kill amp-a`. `run.sh` не выводит логи контейнеров, поэтому порядок шагов остановки там не наблюдался: подтверждено только, что HA-сценарии не сломаны.
    - Не запускалось: rolling update на живом k8s (кластера нет, premise `assumed` в Spec). Deep review не требовался (Spec § Deep Review), `review-findings.md` нет.
- [x] **5.3** Итог: rolling update в k8s вживую не проверен (premise `assumed`) — записать в `DONE.md` и итог задачи. `BUGS.md` `CONFIG-MISSING-FILE-DROPS-ENV` — дописать, что из-за него не доходит `gracefulShutdown.timeoutSeconds`. `BACKLOG.md` — отметить PROD-GRACEFUL-SHUTDOWN закрытым. <!-- verify: grep -n "PROD-GRACEFUL-SHUTDOWN" docs/06-planning/DONE.md docs/06-planning/BACKLOG.md -->

## Definition of Done

- [x] All steps are complete or explicitly marked blocked/skipped
- [x] Success criteria from `requirements.md` are covered
- [x] Contracts from `Spec.md` are implemented or deviations are recorded
- [x] Deep review verdict is `pass`, or deep review was not required (не требуется — Spec § Deep Review)
- [x] Tests for changed behavior are added or updated
- [x] Phase checks pass
- [x] Docs/planning are updated if behavior, contracts, or process changed
