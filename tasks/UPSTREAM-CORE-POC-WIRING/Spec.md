---
id: UPSTREAM-CORE-POC-WIRING
slug: upstream-core-poc-wiring
stream: Architecture / ADR-019
type: feature
status: approved
created_at: 2026-10-24
updated_at: 2026-10-24
based_on:
  - requirements.md
  - research.md
---

# Specification: ядро upstream Alertmanager в бинаре AMP

**Version:** 1.0  
**Status:** Approved (self, PoC)

## Summary

Новый пакет `internal/upstreamcore` собирает ядро upstream Alertmanager v0.34.1 из пакетов (порт `app.setup` + `reloader` без `ui` и кластера) и отдаёт `http.Handler`; новый бинарь `cmd/amp-upstream` поднимает его по `alertmanager.yml`. Своё ядро и `cmd/server` не меняются.

## Requirements Coverage

| Requirement / Criterion | Covered by |
|---|---|
| Версия закреплена, карантин, модуль собирается | `go.mod`; research § Findings; Phase 1 |
| `internal/upstreamcore` собирает ядро и отдаёт handler | Target Design, Component Architecture |
| `cmd/amp-upstream`: SIGTERM, SIGHUP, `/-/reload`, невалидный конфиг | Component Architecture, Edge Cases |
| Сквозной тест ingest → groups → webhook → silence → reload 500 | Phase 4 (write-tests) |
| Инвентаризация: `govulncheck`, размер бинаря | Phase 5 (testing), research |
| Гейты AMP | Phase 5 |

## Current State

- **Code:** импортов upstream Alertmanager нет. Своё ядро — `internal/business/*`, `internal/infrastructure/*`, wiring — `internal/application/service_registry.go`.
- **Data:** не применимо.
- **Tests:** `cmd/server` futureparity-сьюты зелёные на своём ядре.
- **Docs:** ADR-019 в `DECISIONS.md`.

## Design Premises

| Premise | Confirmed by | Class | If wrong |
|---|---|---|---|
| Пакет `app` upstream не собирается из модуля | `go build` с импортом `app`: `ui/web.go:31: pattern app/dist: no matching files found` | measured | можно было бы взять `app` — порт лишний |
| Только `app` импортирует `ui` | grep `alertmanager/ui"` по модулю v0.34.1 → `app/app.go` | call-path-traced | порт тоже не собрался бы |
| Граф upstream совместим с AMP, тесты AMP зелёные | `go build ./...`, `go test ./...` (54 ok), futureparity (235 ok) с v0.34.1 в `go.mod` | measured | сдвиг общих модулей сломал бы AMP |
| `mem.NewAlerts` принимает `AlertStoreCallback`, upstream передаёт туда `silencer` | `provider/mem/mem.go:118-130`, `app/app.go:362` | code-read | срез 2 встраивался бы иначе; на этот срез не влияет |
| Без `compat.InitFromFlags` парсер матчеров — classic, а не дефолт upstream | `matcher/compat/parse.go:31-71` | code-read | расхождение с upstream по UTF-8 матчерам |

## Target Design

`upstreamcore.New(Options)` повторяет `app.setup` upstream за вычетом кластера, UI и сетевых слушателей: создаёт `nflog`, `silence` (со снапшотами в `DataDir` и maintenance-горутинами), `Silencer`, `mem.Alerts` (callback — `silencer`, как в upstream), `api.API`, `tracing.Manager`, `config.Coordinator` и `reloader` (порт `app/reloader.go` один в один без `peer`), применяет начальный конфиг. HTTP: `route.Router` с `httpserver.Register` (`/metrics`, `/-/healthy|ready|reload`, `/debug/*`) и `apih.Register` (`/api/v2/*`), обёрнутый `tracing.Middleware`; запросы `/-/reload` обслуживает горутина ядра через `coordinator.Reload()`. `Core.Reload()` — для SIGHUP; `Core.Stop()` — останавливает dispatcher/inhibitor, maintenance (снапшоты), alerts, tracing в порядке, обратном созданию (как стек `onStop` upstream). `ExternalURL` вычисляет `cmd` (порт `app/url.go`), ядро получает готовый `*url.URL`. `cmd/amp-upstream` — флаги подмножества upstream (`--config.file`, `--storage.path`, `--web.listen-address`, `--web.external-url`, `--data.retention`), `compat.InitFromFlags` с `NoopFlags`, `http.Server`, SIGINT/SIGTERM → `Shutdown` + `Stop`, SIGHUP → `Reload`.

## API Contracts

Контракт upstream `api/v2` и `/-/*` v0.34.1 без изменений — его определяет upstream; AMP своих маршрутов на этом бинаре не вешает.

## Data Model / Migrations

Не применимо. Снапшоты `silences` и `nflog` — файлы в `--storage.path` (формат upstream).

## Component Architecture

- `go-app/go.mod`, `go-app/go.sum` — `require github.com/prometheus/alertmanager v0.34.1`, `go mod tidy`.
- `go-app/internal/upstreamcore/doc.go` — назначение пакета, ссылка на ADR-019, атрибуция Apache-2.0 (по образцу `internal/business/templating/doc.go`).
- `go-app/internal/upstreamcore/core.go` — `Options`, `Core`, `New`, `Handler`, `Reload`, `Stop`; порт `app.setup`, `app/metrics.go`.
- `go-app/internal/upstreamcore/reloader.go` — порт `app/reloader.go` без `peer`.
- `go-app/cmd/amp-upstream/main.go` — бинарь; порт `app/url.go` (`extURL`).

## Security Design

- [x] Ownership — не применимо (нет пользовательских данных AMP).
- [x] Input validation — валидацию делает upstream (`config.LoadFile`, `api/v2`).
- [x] Sensitive data not logged — логирование upstream; секреты receivers upstream маскирует (`config.Secret`).
- [x] Rate limiting — `api.Options.Concurrency`/`Timeout` — дефолты upstream (0 → GOMAXPROCS/8, без таймаута).
- [x] Auth/RBAC — нет; `--web.config.file` вне PoC, бинарь не выкатывается (Non-Goals).

## Invariants

- [ ] `cmd/server` и своё ядро не меняются (дифф вне `internal/upstreamcore`, `cmd/amp-upstream`, `go.mod`/`go.sum` пуст).
- [ ] Весь набор тестов AMP зелёный на новых версиях общих модулей.
- [ ] Неудачный reload не трогает живой конфиг (гарантия `reloader` upstream: всё fallible — до остановки старого dispatcher).
- [ ] `Stop` идемпотентен и не виснет; снапшоты пишутся при остановке maintenance.

## Edge Cases

1. Невалидный `alertmanager.yml` на старте → `New` возвращает ошибку, бинарь выходит с кодом 1.
2. Невалидный конфиг на `POST /-/reload` → 500 с текстом ошибки, прежний конфиг в силе.
3. Receiver без маршрута → не строится (лог, как upstream).
4. `--storage.path` не существует → создаётся (`MkdirAll`).
5. Ошибка `New` после частичной сборки → уже запущенные горутины останавливаются (rollback через тот же стек остановки).

## Impact Analysis

- **Affected modules:** `go.mod`/`go.sum` (общие модули сдвинуты, см. research), новые пакет и бинарь.
- **Breaking changes:** none.
- **New dependencies:** `github.com/prometheus/alertmanager v0.34.1` и транзитивка (research § Findings).
- **Risks:** сдвиг общих модулей ломает AMP — снят прогоном тестов; новые уязвимости в транзитивке — `govulncheck` на `testing`; порт расходится с upstream на следующем миноре — комментарий-указатель на исходный файл/версию в каждом портированном файле.

## Rollout / Rollback

- **Rollout:** не применимо — бинарь не публикуется, образ/чарт не трогаются.
- **Rollback:** удалить `internal/upstreamcore`, `cmd/amp-upstream`, `require` и `go mod tidy`.
- **Feature flag:** не применимо (отдельный бинарь и есть переключатель).

## Observability

- **Logs:** slog upstream с `component`.
- **Metrics:** `alertmanager_*` upstream на `/metrics` нового бинаря.
- **Alerts:** не применимо.

## Deep Review

- **Mandatory triggers present:** diff > ~200 строк (Full tier по `workflow.md` § Tiers).
- **Discretionary triggers present:** novel pattern (первое встраивание upstream-ядра).
- **Decision:** required.

## Open Questions

- [ ] Upstream-PR с точками расширения в `app.Options` и отделением `ui` от `app` (Option C research) — кандидат в BACKLOG; решение владельца после 1b.
