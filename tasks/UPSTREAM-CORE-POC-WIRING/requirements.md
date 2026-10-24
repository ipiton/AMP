---
id: UPSTREAM-CORE-POC-WIRING
slug: upstream-core-poc-wiring
stream: Architecture / ADR-019
type: feature
priority: high
status: active
created_at: 2026-10-24
updated_at: 2026-10-24
---

# Requirements: ядро upstream Alertmanager в бинаре AMP (срез 1a ADR-019)

## Problem Framing

- **Symptom:** ADR-019 решил строить AMP на пакетах upstream Alertmanager, но ни одного импорта upstream в `go-app` нет. Неизвестно, собирается ли граф зависимостей upstream вместе с AMP, во что это обходится (размер бинаря, уязвимости, сдвиг версий общих модулей) и можно ли собрать ядро из пакетов так, чтобы в него встраивались AI-хуки (срез 2), свои receivers (срез 3) и свои маршруты (срез 6).
- **Root Cause:** эпик стартует с PoC; срез `UPSTREAM-CORE-POC` (4–5d) больше двух дней и нарезан на `start-task` 2026-10-24: 1a — этот срез (сборка ядра), 1b — `UPSTREAM-CORE-POC-GATE` (сьюты и amtool-гейт на upstream-ядре, вердикт по ADR-019).
- **Why Now:** выход PoC — стоп-кран всего эпика ADR-019 (~20–28d); срезы 2–4 ждут его.
- **How We Measure:** отдельный бинарь AMP на upstream-ядре стартует на немодифицированном `alertmanager.yml`, принимает алерт по `POST /api/v2/alerts`, отдаёт его в `GET /api/v2/alerts` и `/api/v2/alerts/groups`, доставляет группу в `webhook_configs`, держит silence (`POST/GET/DELETE`), перечитывает конфиг по `POST /-/reload`; своё ядро и `cmd/server` не изменены.

## Risk Profile

- **Signals:** `X`
  - `X` — новый граф зависимостей (upstream Alertmanager и его транзитивка, сдвиг общих модулей `prometheus/common`, `client_golang`), новый пакет и новый бинарь.
- **Tier:** Full
- **Notes:** по сигналам — Standard (как в BACKLOG), но дифф заведомо больше ~200 строк (порт wiring upstream `app/app.go` + `app/reloader.go`, бинарь, `go.mod`/`go.sum` не в счёт) — по `docs/solo-kanban/workflow.md` § Tiers это Full: `deep-review` обязателен. `C` нет: публичные контракты AMP не меняются, новый бинарь отдаёт контракт upstream. `R` нет: бинарь не выкатывается, чарт и образы не трогаются.

## User Stories

1. Как владелец AMP, я хочу видеть upstream-ядро, собранное из пакетов в бинаре AMP, чтобы принять решение по ADR-019 на коде, а не на оценке.
2. Как разработчик срезов 2, 3 и 6, я хочу wiring, в котором конструкторы ядра вызываются из кода AMP, чтобы встроить `AlertStoreCallback`, свои notify-стадии и маршруты без форка upstream.

## Success Criteria

- [ ] `github.com/prometheus/alertmanager` закреплён точной версией в `go-app/go.mod`; версия старше 7 дней (карантин); сдвиги общих модулей перечислены; весь модуль AMP собирается (`go build ./...`, `-tags futureparity`), существующие тесты зелёные.
- [ ] Пакет `go-app/internal/upstreamcore` собирает ядро из пакетов upstream (`config`, `provider/mem`, `silence`, `nflog`, `inhibit`, `dispatch`, `notify` + `config/receiver`, `api`) по `alertmanager.yml` и отдаёт `http.Handler` с `api/v2`, `/-/healthy`, `/-/ready`, `/-/reload`, `/metrics`.
- [ ] Бинарь `go-app/cmd/amp-upstream` поднимает это ядро; SIGTERM — graceful stop, SIGHUP и `POST /-/reload` — перечитывание конфига; невалидный конфиг на reload — 500 и прежний конфиг в силе.
- [ ] Тест сквозного пути: `POST /api/v2/alerts` → алерт в `GET /api/v2/alerts` и в группе → доставка в `webhook_configs` (локальный `httptest`-сервер) → silence скрывает алерт (`status.state=suppressed`) → `DELETE` silence; reload с невалидным конфигом — 500.
- [ ] Инвентаризация в `research.md`: версия, транзитивные модули, `govulncheck` по модулю с upstream-ядром, размер бинаря `amp-upstream` против `cmd/server`.
- [ ] Гейты AMP (`WORKFLOW.md` § Гейты AMP), включая `scripts/release-gate.sh`.

## Non-Goals

- Своё ядро AMP (`internal/business/*`, `internal/infrastructure/*` routing/grouping/silencing/inhibition/publishing) и `cmd/server` не трогаются.
- `main_phase0_contract_test.go`, `main_upstream_parity_regression_test.go`, amtool-гейт на upstream-ядре и вердикт по ADR-019 — срез 1b `UPSTREAM-CORE-POC-GATE`.
- HA (`cluster.Peer`, memberlist) — срез 4; AI-хуки — срез 2; Rootly и K8s-Secret таргеты — срез 3; UI и AMP-маршруты — срез 6.
- Образ, Dockerfile, чарт, CI-job для нового бинаря.

## Constraints

- **Scope:** `go-app/go.mod`, `go-app/go.sum`, новый `go-app/internal/upstreamcore/`, новый `go-app/cmd/amp-upstream/`, task-артефакты.
- **Security:** новый граф зависимостей — карантин 7 дней (глобальный `CLAUDE.md`, Go — вручную), `govulncheck`. Порт кода upstream (Apache-2.0) — с уведомлением в каждом файле по образцу `internal/business/templating/template.go`.
- **Compatibility:** поведение `cmd/server` не меняется; сдвиг общих модулей — только в пределах MVS, весь набор тестов AMP зелёный.

## Discovery Notes

- Similar tasks: порт upstream-шаблонов — `internal/business/templating/` (формат атрибуции, `doc.go`).
- Relevant patterns: wiring upstream — `alertmanager@v0.34.1/app/app.go` (`setup`), `app/reloader.go`, `cmd/alertmanager/main.go`.
- Тестовый шов для 1b: `registerRoutes(mux)` в `cmd/server/futureparity_compat.go` (build tag `futureparity`) — 1b подставит туда `upstreamcore`.
- Базовая линия: `go test -tags futureparity ./cmd/server/...` — зелёный (235 тестов) на 2026-10-24.
- Open unknowns: закрыты в `research.md`.
