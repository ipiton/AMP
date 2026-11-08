---
id: UPSTREAM-CORE-REMOVAL
slug: upstream-core-removal
stream: Architecture / ADR-019
type: refactor
status: active
created_at: 2026-11-08
updated_at: 2026-11-08
based_on:
  - requirements.md
  - research.md
  - Spec.md
---

# Implementation Plan: удаление своего ядра (срез 5 ADR-019)

**Based on:** requirements.md / research.md / Spec.md  
**Date:** 2026-11-08

Ветка `feature/upstream-core-removal`. Подсрезы последовательно, после каждого — `scripts/release-gate.sh` зелёный: **5a** (фазы 1–2), **5b** (фазы 3–4), **5c** (фазы 5–6). Один `deep-review` после 5c, затем `write-tests` (фаза 7) и `testing`.

Тест-файлы в implement только **переносятся или удаляются** вместе со своим кодом; новые/переписанные утверждения — в фазе 7.

## Touched Files

- 5a: `go-app/internal/webauth/` (новый), `go-app/cmd/amp-upstream/{main,amp,auth}.go`, `go-app/internal/buildinfo/metrics.go`, `Dockerfile`, `deploy/smoke/*`, `deploy/e2e-ha/docker-compose.yml`, `go-app/internal/upstreamcore/contract/` (сьюты, baseline), `scripts/{upstream-core-gate,release-gate}.sh`, `docs/UPSTREAM_UPGRADE.md`, `.gitignore`.
- 5b: `go-app/internal/config/*`, `go-app/internal/core/services/*`, `go-app/internal/business/publishing/*`, удаление `go-app/cmd/server`, `internal/application` и пакетов своего ядра, `pkg/*` без потребителей, `go-app/Makefile`, `go.mod`/`go.sum` (tidy), `scripts/release-gate.sh`, `docs/CI.md`.
- 5c: `helm/amp/**`, `cmd/config-reloader`, `Dockerfile.config-reloader`, `.github/workflows/{ci,release}.yml`, `CHANGELOG.md`, `helm/amp/CHANGELOG.md`, `helm/amp/README.md`.

## Phase 1: 5a — `amp-upstream` полный

- [x] **1.1** `internal/webauth`: перенос `internal/application/webauth.go` (+ тест-файл переносится как есть). <!-- verify: cd go-app && go test ./internal/webauth/ -->
- [x] **1.2** `amp-upstream --web.config.file`: загрузка (`webauth.New`), обёртка `core.Handler()`, пути без auth из AMP-конфига или умолчания, с префиксом external URL; ошибка старта при битом файле. <!-- depends: 1.1 | verify: go build ./cmd/amp-upstream && ручной запуск: 401 на /api/v2/status, 200 на /-/healthy -->
- [x] **1.3** `agent_mode`: tools + runbooks в `newInvestigations` (перенос из `service_registry.go`), закрытие DB tools в stop. <!-- verify: go vet ./cmd/amp-upstream/ && go test ./cmd/amp-upstream/ -->
- [x] **1.4** `buildinfo.Register` → только `amp_build_info`; регистрация в `amp-upstream`. <!-- verify: go test ./internal/buildinfo/ ./cmd/amp-upstream/ -->
- [x] **1.5** `Dockerfile`: один бинарь `amp-upstream`, `-ldflags` версии (upstream-модуль из `go.mod` + AMP buildinfo), `EXPOSE 9093`, HEALTHCHECK `/-/healthy`, ENTRYPOINT/CMD. `deploy/e2e-ha` — убрать переопределение entrypoint/комментарий про HEALTHCHECK, если стало лишним. <!-- verify: docker build . && docker run … /api/v2/status → versionInfo.version=0.34.1 -->

## Phase 2: 5a — сьюты и гейты

- [x] **2.1** `internal/upstreamcore/contract`: перенос сьют, harness из `upstreamcore_compat.go` + нужные хелперы, `TestMain`, baseline и `upstreamcore-alertmanager.yml`; тег `upstreamcore`; `doc.go`. <!-- verify: go vet -tags upstreamcore ./internal/upstreamcore/contract/ -->
- [x] **2.2** `upstream-core-gate.sh` на новом пакете и путях; `docs/UPSTREAM_UPGRADE.md`, `core_test.go` комментарий. <!-- depends: 2.1 | verify: scripts/upstream-core-gate.sh -->
- [x] **2.3** `deploy/smoke` на `amp-upstream`: `alertmanager.yml` вместо AMP-конфига, порт, `/-/healthy`; `release-gate.sh` `step_amtool_compat`. <!-- verify: release-gate amtool-compat PASS -->
- [x] **2.4** Удалить сьюты и шов из `cmd/server` (`futureparity`-тег там остаётся только у собственных тестов до 5b). <!-- depends: 2.1 | verify: go vet -tags futureparity ./cmd/server/ -->

**Phase verification (5a):** `scripts/release-gate.sh` PASS.

## Phase 3: 5b — резка пакетов

- [x] **3.1** `internal/config`: удалить модель alertmanager, валидацию подмножества, секции своего ядра, hot-reload; `LoadConfig` без inhibition/route. <!-- verify: go build ./internal/config/ ./cmd/amp-upstream/ -->
- [x] **3.2** `core/services`: удалить `alert_processor.go`, `route_evaluator.go`, `routing_fallback_metrics.go` и их тесты. <!-- verify: go build ./internal/core/services/ -->
- [x] **3.3** `business/publishing`: оставить discovery (+ то, что ему нужно), удалить config-таргеты, health, refresh, stats/trends/timeseries; убрать дубль namespace в `targets.go`, если появился общий хелпер. <!-- verify: go list -deps ./cmd/amp-upstream | grep -E 'infrastructure/(routing|grouping|inhibition|publishing)|configvalidator' пусто -->

## Phase 4: 5b — удаление

- [x] **4.1** Удалить `cmd/server`, `internal/application`, пакеты, недостижимые из `./cmd/...` и `./internal/upstreamcore/contract` (с тегом). <!-- depends: 3.* | verify: go build ./... && go vet ./... -->
- [x] **4.2** `pkg/*` без потребителей — удалить; `go mod tidy`. <!-- verify: go build ./... && git diff go.mod -->
- [x] **4.3** `release-gate.sh`: удалить `step_futureparity`, пересобрать список `step_race`; `go-app/Makefile` на `amp-upstream`; `docs/CI.md`. <!-- verify: scripts/release-gate.sh -->

**Отклонения 5b:**
- `config`: из `config_test.go`/`config_load_test.go` удалены тесты и отдельные утверждения про удалённые секции (server.port/host, redis, grouping, route, storage, publishing.queue, `TestChartEnvKeysKnownToViper` — чарт на env уходит в 5c). Тесты `PostgresConfigFrom` перенесены в `database_test.go` вместе с функцией (`database.go`). Новых утверждений нет — фаза 7.
- `cmd/config-reloader/e2e_contract_test.go` удалён в 5b: контракт с hot-reload `cmd/server`, которого больше нет. Сам бинарь — 5.4.
- Сверх плана удалены целиком мёртвые файлы своего ядра: `core/services/{filter_engine,enrichment_types,classification_adapter}.go`, `core/publishing_templates.go`, `infrastructure/sqlite_adapter.go` (lite storage), `infrastructure/cache/redis.go` (Redis-HA), `pkg/metrics/inhibition_cache_collision_test.go`; `pkg/logger`, `notification/url`, `infrastructure/{webhook,silencing,storage/memory}`, `business/templating`, `core/silencing`, `alertmanager/config`.
- Остаток недостижимых функций в общей инфраструктуре (`database/postgres`, `pkg/httperror`, `pkg/retry`, `pkg/metrics`, `llm`) не тронут — TECH-DEBT (deadcode, ~300 функций).
- `release-gate.sh` `step_race`: явный список `RACE_PKGS` (Spec + `business/publishing`), пропавший пакет — fail.

**Phase verification (5b):** `scripts/release-gate.sh` PASS; `go list ./...` без своего ядра.

## Phase 5: 5c — чарт

- [x] **5.1** `values.yaml`: `upstreamCore` → `alertmanager`, общие ключи для StatefulSet; удалить ключи своего ядра (`configFile`, `configReloader`, `autoscaling`, `persistence` lite, `grouping`, `silencing`, `storage`, publishing queue/templates/health, `liteProfile`, `alerts`, `migration`, `strategy`). `values-production.yaml` — то же. <!-- verify: helm lint helm/amp -->
- [x] **5.2** `statefulset.yaml`, `service.yaml`, `service-headless.yaml`, `configmap.yaml` (alertmanager.yml), `amp-config.yaml` (AMP-конфиг); SA/RBAC; web auth; ingress/servicemonitor/networkpolicy/pdb на селекторе; NetworkPolicy PG/Valkey; удалить `deployment.yaml`, `hpa.yaml`, `pvc.yaml`, `configfile.yaml`, старые `upstream-*.yaml`; `NOTES.txt`. <!-- verify: helm template по dev/prod/small -->
- [x] **5.3** `tests/*.sh`: удалить тесты своего ядра (config-reloader, grouping-default, cache-startup-wait), перенести остальные на StatefulSet (переписывание утверждений — фаза 7). <!-- verify: step_helm_tests -->
- [x] **5.4** Удалить `cmd/config-reloader`, `Dockerfile.config-reloader`, matrix CI/release; `docs/CI.md`. <!-- verify: go build ./... ; actionlint если есть -->
- [x] **5.5** `CHANGELOG.md` `[Unreleased]` (breaking), `helm/amp/CHANGELOG.md`, `helm/amp/README.md`. <!-- verify: git diff --check -->

**Отклонения 5c:**
- Один StatefulSet `<fullname>` вместо Deployment + `<fullname>-upstream`: поды несут `component: application` (и в селекторе StatefulSet), поэтому NetworkPolicy PG/Valkey пускают их без правок; headless — `component: cluster`, чтобы ServiceMonitor не скрейпил дважды.
- `amp-config.yaml`: AMP-конфиг генерируется из values и сливается с `alertmanager.ampConfig` (`mustMergeOverwrite`) — `ampConfig` перекрывает `externalDatabase` (раньше env `externalDatabase` перекрывал `configFile`). Fallback без БД — `alertmanager.ampConfig.database` вместо `configFile.content`.
- Удалены `logLevel` и `gracefulShutdown.timeoutSeconds`: у `amp-upstream` нет таких флагов (HTTP-drain фиксированные 5s); порог NOTES — `preStopDelay + 10`.
- `cache.*` удалён целиком; Valkey остаётся (дефолт PG+Valkey по Spec), но не используется — NOTES/values/CHANGELOG, TECH-DEBT (UPSTREAM-AI-HA-DEDUP). `values-production.yaml`: `valkey.replicas: 0`.
- Тесты чарта: удалены также `render-cache-auth.sh` (cache.*); `render-upstream-core.sh` → `render-statefulset.sh`; утверждения про env (`DATABASE_*`, `LLM_ENABLED`) переведены на ключи `amp.yaml` (`cfg_value`), HPA/lite-PVC/cache-строки NOTES — на реплики StatefulSet и строку Valkey. Новых сценариев сверх замены — фаза 7.
- CI: matrix `images` оставлен с одной записью `amp`, чтобы имя required-check `images (amp)` не менялось.
- `grafana/dashboards/alert-history-service.json` ссылается на метрики своего ядра — срез 7 (доки/дашборды), не тронут. `docs/CONFIGURATION_GUIDE.md` (configReloader, configFile) — тоже срез 7.

## Phase 6: deep-review

- [x] **6.1** `deep-review` → `review-verdict.json` `"gate": "pass"`.

## Phase 7: write-tests

- [x] **7.1** `internal/webauth` в `amp-upstream`: тест обёртки (401/200, пробы, префикс).
- [x] **7.2** `agent_mode`: тест сборки agent loop (tools по конфигу).
- [x] **7.3** Сьюты: записи b — на ожидание upstream или удалить; c — `TestMain` ставит `version.Version`; baseline только a.
- [x] **7.4** `buildinfo`: `Register` без `alertmanager_build_info`.
- [x] **7.5** Рендер-тесты чарта на StatefulSet (web auth, AMP-конфиг, RBAC, NetworkPolicy PG/Valkey, single-node).

**Отклонения 6–7:**
- deep-review: два независимых ревьюера (Go; чарт и миграция), 25 находок, 5 major — все fix-here и внесены (`review-findings.md`, коммит `fix: address deep-review findings`).
- 7.2: сборка tools вынесена из `newAgentLoop` в `newToolRegistry` ради теста; поведение то же.
- 7.3: записи b удалены вместе с тестами (24 теста/подтеста) или переписаны на upstream (3 строки probes); c закрыты `setBuildInfo` в `TestMain`. Baseline — 17 записей a.
- Сверх плана: `TestSetContractVersion` (F4), `TestIgnoredKeys` (F2), тесты `resolveWebConfigFile` (F1), render-тесты guard'ов ревью.

## Definition of Done

- [ ] All steps are complete or explicitly marked blocked/skipped
- [ ] Success criteria from `requirements.md` are covered
- [ ] Contracts from `Spec.md` are implemented or deviations are recorded
- [ ] Deep review verdict is `pass`
- [ ] Tests for changed behavior are added or updated
- [ ] `scripts/release-gate.sh` PASS целиком
- [ ] Planning: BUGS/TECH-DEBT/REVIEW-PIPELINE/BACKLOG/DONE/NEXT, CHANGELOG
