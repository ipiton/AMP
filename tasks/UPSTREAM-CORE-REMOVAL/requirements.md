---
id: UPSTREAM-CORE-REMOVAL
slug: upstream-core-removal
stream: Architecture / ADR-019
type: refactor
priority: high
status: active
created_at: 2026-11-08
updated_at: 2026-11-08
---

# Requirements: удаление своего ядра, upstream-ядро — основной бинарь (срез 5 ADR-019)

## Problem Framing

- **Symptom:** в репозитории два ядра. `cmd/server` (своё: routing, grouping/dispatcher, notify chain, nflog, таймеры, silences, inhibition, свои интеграции и шаблоны, Redis-HA, god-object `internal/application/service_registry.go`) — это образ `/app/amp` и Deployment чарта. `cmd/amp-upstream` (пакеты upstream Alertmanager + AI-слой, история, investigation, K8s-Secret таргеты) — второй бинарь, в чарте за `upstreamCore.enabled=false`, без версии (`versionInfo.version` и `alertmanager_build_info` пусты), без web auth, без `--amp.config.file`/LLM в чарте, investigation без `llm.agent_mode`. Даже `amp-upstream` транзитивно тянет своё ядро (`internal/config` → routing/grouping/inhibition/configvalidator; `core/services/alert_processor.go`; `business/publishing` → `infrastructure/publishing`, `routing`).
- **Root Cause:** эпик ADR-019 строил upstream-ядро рядом со своим (срезы 1–4); удаление — этот срез.
- **Why Now:** срез 5 эпика, срезы 1–4 закрыты и смержены 2026-11-08. После него — срез 6 (API/UI), 7 (доки), релиз v0.1.0 и выкатка в собственный k3s владельца: значения чарта по умолчанию (single-node) должны работать.
- **How We Measure:** `go list ./...` не содержит своего ядра; `go list -deps ./cmd/amp-upstream` не содержит `infrastructure/{routing,grouping,inhibition,publishing,silencing,lock,cluster}`, `pkg/configvalidator`; образ — один бинарь `amp-upstream` с версией; `helm template` по умолчанию рендерит StatefulSet upstream-ядра с AMP-конфигом; `scripts/release-gate.sh` зелёный целиком, включая lint.

## Risk Profile

- **Signals:** `C S X R`
  - `C` — образ (entrypoint, порт), флаги бинаря, values и объекты чарта (Deployment → StatefulSet), удаление образа `amp-config-reloader`, AMP-конфиг теряет секции своего ядра.
  - `S` — web auth переезжает в новый бинарь; Ingress/NetworkPolicy/ServiceMonitor/RBAC (list secrets) на новом workload.
  - `X` — Go-модуль целиком, Dockerfile, чарт, CI, скрипты гейтов, `deploy/smoke`.
  - `R` — путь доставки уведомлений в проде целиком сменяется на upstream-ядро.
- **Tier:** Full
- **Notes:** `M` нет: схема PG не меняется (история/investigation как в 2b); перенос silences из PG в снапшот upstream и сброс Redis — migration notes среза 7.

## User Stories

1. Как оператор, я ставлю чарт с умолчаниями на одну ноду k3s и получаю upstream-ядро AMP с историей в PostgreSQL, K8s-Secret таргетами и (при `llm.enabled`) классификацией/investigation.
2. Как оператор, я включаю web auth, Ingress, ServiceMonitor, NetworkPolicy, PDB так же, как раньше, и они защищают/обслуживают новый workload.
3. Как пользователь amtool/karma/Grafana, я вижу в `/api/v2/status` и `alertmanager_build_info` версию upstream-контракта, а версию AMP — в `amp_build_info`.
4. Как разработчик, я поддерживаю один бинарь без своего ядра; конфиг alertmanager.yml читает и перечитывает upstream.

## Success Criteria

- [ ] Своё ядро удалено: `cmd/server`, `internal/application`, routing, grouping/dispatcher, notify chain/publishing, nflog, таймеры, silences, inhibition, свои интеграции и шаблоны, Redis-HA (lock, heartbeat, group/timer storage, silence event bus); пакеты `pkg/*` без потребителей.
- [ ] `amp-upstream` не тянет своё ядро транзитивно: `internal/config` без модели alertmanager и hot-reload своего ядра, `core/services` без `alert_processor`, discovery K8s-Secret таргетов без `infrastructure/publishing`/`routing` (`UPSTREAM-TARGETS-DISCOVERY-DEPENDENCY`).
- [ ] alertmanager.yml — только upstream `config.LoadFile`/`config.Coordinator` (уже в `upstreamcore`), reload сериализован и не на контексте HTTP-клиента.
- [ ] wiring AMP-слоя — функции в `cmd/amp-upstream` по образцу `cmd/alertmanager`, без god-object.
- [ ] `amp-upstream`: `--web.config.file` (basic auth как у `cmd/server`, пробы без auth); `llm.agent_mode` — tools и runbooks в investigation; версия через `-ldflags` (`prometheus/common/version`) и `amp_build_info`.
- [ ] Образ: один бинарь `/app/amp-upstream`, порт 9093, HEALTHCHECK на `/-/healthy`.
- [ ] Чарт: StatefulSet upstream-ядра — единственный workload AMP; Service/Ingress/ServiceMonitor/NetworkPolicy/PDB/web auth/RBAC на нём; `podAntiAffinity`, `nodeSelector`/`tolerations`; AMP-конфиг (`--amp.config.file`) и секреты LLM/PG; NetworkPolicy PG/Valkey пускают новые поды; умолчания single-node рендерятся.
- [ ] Контрактные сьюты и baseline переехали с `cmd/server` на upstream-ядро; `upstream-core-gate.sh`, `release-gate.sh`, Makefile, `deploy/smoke` — без `cmd/server`.
- [ ] `LINT-ERRCHECK-CHANNEL-RECEIVE`: errcheck-сайты уходят с удалёнными пакетами; запись сужена до пина линтера или закрыта.
- [ ] Записи «срез 5» в `BUGS.md`/`TECH-DEBT.md` и `[-]` в `REVIEW-PIPELINE-2026-10-21.md` закрыты или переадресованы с причиной; кандидаты BACKLOG разобраны.
- [ ] `scripts/release-gate.sh` зелёный целиком.

## Non-Goals

- HTTP API и UI AMP сверх нужного для сборки (история, investigation, `/api/v2/inhibitions`, дашборд) — срез 6 `UPSTREAM-API-UI`. До него у продукта нет UI и AMP-маршрутов, только upstream `api/v2`.
- README, `ALERTMANAGER_COMPATIBILITY.md`, migration notes — срез 7.
- Общий dedup LLM/investigation между репликами через Valkey — `UPSTREAM-AI-HA-DEDUP`.
- Изменение схемы PG и перенос silences из PG.

## Constraints

- **Scope:** удаление по графу зависимостей; новый код — только перенос (web auth, agent loop) и проводка. Порт upstream (`upstreamcore/core.go`, `reloader.go`) не трогать.
- **Security:** web auth не ослабляется: те же правила, что в `cmd/server` (PROD-AUTH, ADR-015): пробы `/-/healthy`, `/-/ready` без auth, остальное — basic auth; Ingress рендерится только с auth.
- **Compatibility:** breaking для чарта и образа допустим (релизов не было), фиксируется в `CHANGELOG.md` `[Unreleased]`.

## Discovery Notes

- Similar tasks: `tasks/archive/UPSTREAM-HA-MEMBERLIST/`, `tasks/archive/UPSTREAM-NON-UPSTREAM-RECEIVERS/`, `tasks/archive/UPSTREAM-CORE-POC-GATE/`.
- Нарезка (правило «Slice», оценка 5–6d), последовательно на ветке `feature/upstream-core-removal`, гейты зелёные после каждого:
  - **5a** (~1.5–2d) — `amp-upstream` становится полным: web auth, `agent_mode`, версия, образ на одном бинаре, `deploy/smoke` на нём, контрактные сьюты переезжают с `cmd/server`.
  - **5b** (~2d) — удаление своего ядра и разрезание оставшихся пакетов (`internal/config`, `core/services`, `business/publishing`); гейты без `futureparity`.
  - **5c** (~1.5–2d) — чарт: StatefulSet вместо Deployment, удаление `config-reloader`; planning.
- Open unknowns: нет блокирующих; решения — `research.md`.
