---
id: UPSTREAM-CORE-REMOVAL
slug: upstream-core-removal
stream: Architecture / ADR-019
type: refactor
status: draft
created_at: 2026-11-08
updated_at: 2026-11-08
based_on:
  - requirements.md
  - research.md
---

# Specification: удаление своего ядра, upstream-ядро — основной бинарь (срез 5 ADR-019)

**Version:** 1.0  
**Status:** Draft

## Summary

`cmd/amp-upstream` становится единственным бинарём, образом и workload'ом чарта: получает web auth, `llm.agent_mode` и версию; своё ядро (`cmd/server`, `internal/application` и всё, что достижимо только из них) удаляется, а три пакета, через которые оно тянулось в `amp-upstream`, режутся до нужного AMP-слою.

## Requirements Coverage

| Criterion | Covered by |
|---|---|
| своё ядро удалено, `pkg/*` без потребителей | 5b, Component Architecture § удаление |
| `amp-upstream` без транзитивного ядра | 5b, § резка пакетов; проверка `go list -deps` |
| alertmanager.yml через upstream, reload сериализован | уже в `upstreamcore` (`core.go:273,413,493-513`); 5b удаляет старый путь |
| wiring без god-object | `cmd/amp-upstream/{main,amp,targets,auth}.go` — функции-конструкторы, как `cmd/alertmanager/main.go` |
| web auth, `agent_mode`, версия | 5a |
| образ один бинарь | 5a, `Dockerfile` |
| чарт на StatefulSet | 5c |
| сьюты и гейты без `cmd/server` | 5a (перенос), 5b (`futureparity` из гейта) |
| `LINT-ERRCHECK-CHANNEL-RECEIVE` | 5b (пакеты удаляются), planning — сузить до пина линтера |
| planning-записи | finalize |
| `release-gate.sh` зелёный | testing, после каждого подсреза |

## Current State

- **Code:** см. `research.md` Q1; `cmd/amp-upstream` (`main.go` флаги и HTTP, `amp.go` AMP-слой, `targets.go` K8s-таргеты), `internal/upstreamcore` (порт `app`).
- **Data:** PG-схема истории/investigation (`go-app/migrations`) — без изменений; таблицы своего ядра (silences и пр.) остаются в схеме (`DB-ORPHAN-TABLES`, вне среза).
- **Tests:** юнит-тесты удаляемых пакетов уходят с ними; сьюты `cmd/server` переезжают; `upstreamcore`, `upstreamai`, `upstreamtargets`, `cmd/amp-upstream` тесты остаются.
- **Docs:** `docs/UPSTREAM_UPGRADE.md`, `docs/CI.md`, `helm/amp/README.md`, `CHANGELOG.md`.

## Design Premises

| Premise | Confirmed by | Class | If wrong |
|---|---|---|---|
| Своё ядро попадает в `amp-upstream` только через `internal/config`, `core/services/alert_processor.go`, `business/publishing` | `go list -deps ./cmd/amp-upstream` + импорты каждого пакета | call-path-traced | после удаления не собирается — видно на `go build` |
| Никто вне `cmd/server`/`internal/application` не использует hot-reload `internal/config` (`reload_*`, `reloadable_*`, `update_*`, `service.go`) | `go list -deps`, grep по репозиторию | call-path-traced | ошибка сборки, видно сразу |
| karma и прочие читают `alertmanager_build_info{version}` как semver ≥ 0.22 | `internal/buildinfo/metrics.go` комментарий, `TestAlertmanagerBuildInfo_KarmaVersionProbe`, ADR-009 | code-read | при версии AMP — karma теряет инстанс; выбран вариант с версией upstream |
| upstream `api/v2` берёт `versionInfo` из `prometheus/common/version` | `go-app` vendor-модуль `api/v2/api.go` (`version.Version`) | code-read | `-ldflags` не дойдёт до status — видно в smoke `/api/v2/status` |
| viper подхватывает env (`DATABASE_PASSWORD`, `LLM_API_KEY`) поверх файла для ключей с default | `config.go:683-684,907,934` | code-read | секрет не попадёт — проверяется рендером чарта + ручным запуском бинаря с env |
| kubelet-пробы без credentials: `/-/healthy`, `/-/ready` должны быть вне auth | `DefaultUnauthenticatedPaths`, PROD-AUTH | code-read | поды не станут Ready при `webConfig` |
| Никто, кроме Deployment, не использует `cmd/config-reloader` | `helm/amp/templates/deployment.yaml:362-412`, CI/release matrix | call-path-traced | — |

## Target Design

1. **5a — `amp-upstream` полный.** `internal/webauth` — перенос `internal/application/webauth.go` (тот же формат, кеш bcrypt, перечитывание файла); флаг `--web.config.file` оборачивает `core.Handler()`; пути без auth — `server.auth.unauthenticated_paths` AMP-конфига или `DefaultUnauthenticatedPaths()`, с префиксом `--web.external-url`. `agent_mode`: перенос регистрации tools (Prometheus, Loki, Kubernetes, Database) и runbooks в `newInvestigations`; DB-хэндл tools закрывается в `stop`. Версия: `Dockerfile` ставит `-X prometheus/common/version.{Version,Revision,Branch,BuildUser,BuildDate}` (Version — версия модуля upstream из `go.mod`) и `-X internal/buildinfo.*` (AMP); `amp-upstream` регистрирует только `amp_build_info` (`alertmanager_build_info` даёт `versioncollector`). Образ — один бинарь `/app/amp-upstream`, `EXPOSE 9093`, HEALTHCHECK `/-/healthy`, `ENTRYPOINT`+`CMD` с `--config.file=/etc/amp/alertmanager.yml --storage.path=/data`. `deploy/smoke` на `amp-upstream` и upstream-конфиге. Сьюты — в `internal/upstreamcore/contract` (тег `upstreamcore`), с baseline и `upstream-core-gate.sh`.
2. **5b — удаление.** Режем рёбра: в `internal/config` удаляются модель alertmanager (`Routing`, `Receivers`, `InhibitRules`, секции `inhibition`, `grouping`, `silencing`, `lock`, `webhook`, `storage.snapshot*`, `publishing.queue/templates/health`), валидация alertmanager-подмножества, hot-reload (`reload_*`, `reloadable_*`, `update_*`, `service.go`); `core/services` — `alert_processor.go`, `route_evaluator.go`, `routing_fallback_metrics.go`; `business/publishing` — config-таргеты, health, refresh, stats/trends/timeseries (остаётся discovery). Затем удаляются `cmd/server`, `internal/application` и все пакеты, недостижимые из `cmd/amp-upstream`, `cmd/...` и тест-пакета сьютов. Гейты: `step_futureparity` удалён, `step_race` — на пакетах с конкурентностью, что остались (`upstreamcore`, `upstreamai`, `upstreamtargets`, `infrastructure/investigation`), Makefile на `amp-upstream`.
3. **5c — чарт.** `templates/statefulset.yaml` (из `upstream-statefulset.yaml`) — единственный workload: общие ключи values, `serviceAccountName` с `automountServiceAccountToken` только при RBAC, AMP-конфиг `<fullname>-amp-config` (генерируется из values `profile`, `database`, `llm`, `investigation`, `publishing`), секреты env, web auth (`--web.config.file`), пробы, preStop, scheduling, extraVolumes. `service.yaml` → порт `web` 9093; Ingress/ServiceMonitor/NetworkPolicy/PDB на селекторе StatefulSet; PDB-селектор без PG/Valkey; NetworkPolicy PG/Valkey пускают новые поды; удаляются `deployment.yaml`, `configmap.yaml`, `configfile.yaml`, `hpa.yaml`, `pvc.yaml`, `upstream-*.yaml` (сливаются), `cmd/config-reloader`, `Dockerfile.config-reloader`, matrix CI/release.

## API Contracts

- Флаги `amp-upstream`: новый `--web.config.file` (имя upstream). Остальные без изменений.
- HTTP: только upstream (`api/v2`, `/-/*`, `/metrics`, `/debug/*`) — AMP-маршруты своего ядра исчезают до среза 6 (breaking, `CHANGELOG`).
- `GET /api/v2/status` `versionInfo.version` = версия upstream-модуля (`0.34.1`), `revision`/`branch`/`buildUser`/`buildDate` — сборки AMP.

## Data Model / Migrations

Не применимо: схема и миграции не меняются.

## Component Architecture

- `go-app/internal/webauth/` — новый пакет (перенос).
- `go-app/cmd/amp-upstream/main.go` — флаг `--web.config.file`, обёртка handler, `buildinfo` (`amp_build_info`).
- `go-app/cmd/amp-upstream/amp.go` — agent loop + runbooks.
- `go-app/internal/buildinfo/` — `Register` регистрирует только `amp_build_info` (`alertmanager_build_info` даёт upstream).
- `go-app/internal/upstreamcore/contract/` — сьюты + baseline + `doc.go`.
- `Dockerfile`, `deploy/smoke/*`, `scripts/{release-gate,upstream-core-gate}.sh`, `go-app/Makefile`, `docs/{UPSTREAM_UPGRADE,CI}.md`.
- удаление: `go-app/cmd/{server,config-reloader}`, `internal/application`, своё ядро (`research.md` Q1), `Dockerfile.config-reloader`.
- `helm/amp/templates/*`, `values*.yaml`, `tests/*.sh`, `README.md`, `CHANGELOG.md`.

## Security Design

- [x] Ownership validation — не применимо (нет пользовательских данных в новом коде).
- [x] Input validation — web config валидируется как в PROD-AUTH (неизвестные ключи — ошибка, пустой список пользователей — ошибка).
- [x] Sensitive data not logged — секреты LLM/PG только в Secret → env; `cmdline` виден на `/debug/vars` (ADR-019), поэтому секреты не во флагах.
- [x] Rate limiting — не меняется.
- [x] Auth/RBAC: basic auth на всём, кроме проб; Ingress только с auth (guard сохраняется); Role `list secrets` на SA StatefulSet, токен монтируется только при RBAC.

## Invariants

- [ ] Порт upstream (`upstreamcore/core.go`, `reloader.go`) не меняется.
- [ ] Без `--amp.config.file` и `--web.config.file` `amp-upstream` ведёт себя как сейчас (гейт `upstream-core-gate.sh`).
- [ ] Чарт по умолчанию (single-node): 1 реплика, кластер выключен, PG+Valkey, история включена; `helm template` проходит.
- [ ] Ingress не рендерится без auth.

## Edge Cases

1. `--web.config.file` указан, файла нет или он невалиден → старт с ошибкой (как `cmd/server`).
2. Файл web config изменился на невалидный → остаются прежние пользователи (поведение `webauth`).
3. `--web.external-url` с префиксом → пути без auth сравниваются с префиксом.
4. `agent_mode` без PG/investigation → Warn, как сейчас; tools DB DSN пуст → Warn, tool не регистрируется.
5. AMP-конфиг из старого чарта с `route:`/`receivers:` → ключи игнорируются viper; конфиг alertmanager берётся из `--config.file`.
6. `replicaCount > 1` → кластер memberlist (как `upstreamCore.replicas` среза 4).

## Impact Analysis

- **Affected modules:** весь `go-app`, образ, чарт, CI.
- **Breaking changes:** образ (один бинарь, порт 9093, нет `/app/amp`), удалён образ `amp-config-reloader`; чарт (StatefulSet вместо Deployment, `upstreamCore.*` → `alertmanager.*`, нет HPA/lite-PVC/`configReloader`/`configFile`, Service на 9093); AMP-конфиг без секций своего ядра; HTTP без AMP-маршрутов и UI до среза 6. Всё — в `CHANGELOG.md` `[Unreleased]`, migration notes — срез 7.
- **New dependencies:** нет.
- **Risks:** (1) N реплик + LLM → N вызовов LLM на алерт (`UPSTREAM-AI-HA-DEDUP`) — по умолчанию 1 реплика и `llm.enabled=false`; риск в CHANGELOG и values-production. (2) объём удаления — компилятор и гейты; deep-review.

## Rollout / Rollback

- **Rollout:** новый образ + чарт; `helm upgrade` удаляет Deployment и создаёт StatefulSet; silences своего ядра не переносятся (срез 7).
- **Rollback:** откат ветки/тега; на кластере — `helm rollback` к ревизии с Deployment.
- **Feature flag:** нет.

## Observability

- **Logs:** web auth вкл/выкл, agent loop и tools — Info при старте, как в `cmd/server`.
- **Metrics:** `amp_build_info`; метрики web auth (`webauth`); метрики своего ядра исчезают (dashboards/alerts на них — срез 7 доки, `grafana/` проверить на ссылки).
- **Alerts:** правила чарта на метрики своего ядра (если есть) — удалить/переписать в 5c.

## Deep Review

- **Mandatory triggers present:** `S`; 4 сигнала; pre-release (следом v0.1.0).
- **Discretionary triggers present:** large diff.
- **Decision:** required.

## Open Questions

- [ ] нет блокирующих. Решения владельца не требуются: все развилки — в пределах ADR-019 и брифа среза.
