# Deep Review Findings: ядро upstream Alertmanager в бинаре AMP

**Trigger classification:** mandatory (Full tier: diff > ~200 строк) + discretionary (novel pattern)
**Reviewer perspective:** независимый агент (general-purpose), без доступа к рассуждениям автора; повторная проверка фиксов — тот же агент
**Reviewed at:** 2026-10-24
**Reviewed tree:** feature/upstream-core-poc-wiring @ 6a22e4f (первый проход), @ de6f05b (проверка фиксов)
**Verdict:** см. `review-verdict.json`

Ревьюер подтвердил: `reloader.go` совпадает с upstream `app/reloader.go` построчно (кроме комментариев и `peer`), порядок `setup` и стек остановки — как в `app/app.go`; все пять Design Premises верны и их классы заработаны; все новые версии в `go.mod` старше 7 дней; `cmd/server` и своё ядро не тронуты, образ `amp` не меняется (`Dockerfile` собирает только `./cmd/server`).

## Findings

### F1 — errcheck в `amp-upstream` валит release-gate
- **Severity:** major
- **Location:** `go-app/cmd/amp-upstream/main.go` (`defer listener.Close()`)
- **Issue:** `golangci-lint run` (шаг `scripts/release-gate.sh`) — 1 issue errcheck.
- **Recommendation:** закрывать листенер явно на ранних return.
- **Disposition:** fix-here (de6f05b; `golangci-lint` — 0 issues)
- **Follow-up:** n/a

### F2 — reload параллельно со Stop оставляет живой dispatcher
- **Severity:** major
- **Location:** `go-app/internal/upstreamcore/core.go` (`serveReloads`, `Reload`, `Stop`)
- **Issue:** порт потерял барьер upstream (`App.mtx`, `routerDone`): reload, попавший между закрытием `quit` и cleanups, запускал новый dispatcher/inhibitor поверх остановленного провайдера — утечка горутин и гонка.
- **Recommendation:** взаимоисключение reload и Stop; reload после Stop — ошибка.
- **Disposition:** fix-here (de6f05b: `Core.mtx` + `stopped`, `errStopped`)
- **Follow-up:** n/a; тест Reload после Stop — в `write-tests`

### F3 — нет `alertmanager_cluster_enabled` и `alertmanager_build_info`
- **Severity:** minor
- **Location:** `core.go` (`metrics`), `cmd/amp-upstream/main.go`
- **Issue:** порт выбросил gauge кластера и versioncollector — дашборды upstream их используют.
- **Disposition:** fix-here (de6f05b)
- **Follow-up:** n/a

### F4 — `/-/reload` после Stop блокирует handler навсегда
- **Severity:** minor
- **Location:** `core.go` (`Handler`, `Stop`), `main.go` (shutdown)
- **Issue:** upstream-handler ждёт ответа безусловно; после Stop никто не отвечает.
- **Disposition:** fix-here (de6f05b): контракт «сначала перестать обслуживать Handler, потом Stop» в doc-комментарии; `server.Close()` при таймауте `Shutdown`.
- **Follow-up:** n/a

### F5 — тестам нужна изоляция глобалов
- **Severity:** minor
- **Location:** `core.go` (регистрация метрик), глобалы `compat`/otel
- **Issue:** второй `New` с тем же Registerer паникует; `/metrics` отдаёт default gatherer.
- **Disposition:** fix-here в `write-tests` (`prometheus.NewRegistry()` на каждый `New`, `compat.InitFromFlags` в `TestMain`)
- **Follow-up:** n/a

### F6 — BACKLOG `GO-MOD-TIDY-CHECK` не обновлён
- **Severity:** minor (tracking)
- **Location:** `docs/06-planning/BACKLOG.md`
- **Issue:** `go mod tidy` выполнен в этой задаче; пункт надо сузить до шага `go mod tidy -diff` в release-gate.
- **Disposition:** fix-here на `finalize`
- **Follow-up:** BACKLOG `GO-MOD-TIDY-CHECK`

### F7 — порядок Stop в Spec расходится с кодом
- **Severity:** nit
- **Disposition:** fix-here (Spec § Target Design)

### F8 — provenance неполный
- **Severity:** nit
- **Issue:** не указаны `app/lifecycle.go`, `app/url.go`, upstream `NOTICE` (Apache-2.0 §4(d)).
- **Disposition:** fix-here (`doc.go`, заголовок `core.go`)

### F9 — незаписанные отличия от upstream
- **Severity:** nit
- **Issue:** таймаут shutdown 30s против 5s, нет стартового лога, флаги вне PoC.
- **Disposition:** fix-here: таймаут 5s, стартовый лог, список отличий в `doc.go`. SIGHUP без схлопывания — reject (reason: reload синхронный в том же цикле, повторный SIGHUP безвреден).

### F10 — Stop ждёт зависший reload без дедлайна (повторная проверка)
- **Severity:** minor
- **Location:** `core.go` (`reload`, `Stop`)
- **Issue:** `coordinator.Reload()` идёт под `mtx`; зависни он (`WaitForLoading`), Stop зависнет вместе с ним. Upstream бросает горутину по таймауту.
- **Disposition:** fix-here — записано в `doc.go` § Known differences; вероятность низкая, отдельной работы не требует.

### F11 — handler `/-/reload` после `server.Close()` может повиснуть (повторная проверка)
- **Severity:** nit
- **Disposition:** reject (reason: только при таймауте Shutdown, процесс в этот момент завершается; контракт описан у `Handler`).

## Отложено в testing

- Поведение `cmd/server` на сдвинутых общих модулях: futureparity-сьюты и полный `release-gate.sh`.
- `govulncheck`: выполнен на implement, найдена и закрыта GO-2026-6505 (research § Findings).

## Anti-Pattern Check

- [x] Self-audit was not treated as a substitute for independent review.
