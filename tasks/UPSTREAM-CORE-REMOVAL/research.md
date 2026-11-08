---
id: UPSTREAM-CORE-REMOVAL
level: full
created_at: 2026-11-08
---

# Research: UPSTREAM-CORE-REMOVAL

## TL;DR

Своё ядро тянется в `amp-upstream` только через три пакета: `internal/config` (модель alertmanager, валидация, hot-reload своего ядра), `core/services/alert_processor.go` и `business/publishing` (config-таргеты, health/refresh/stats). Разрезав их, удаляем всё остальное по графу. alertmanager.yml уже читает и перечитывает upstream (`upstreamcore/core.go:273,413,493-513`), так что «конфиг через `config.Load`/`Coordinator`» сводится к удалению старого пути. Нового кода мало: перенос web auth и agent loop из `internal/application`, `-ldflags` версии, проводка чарта.

## Questions

1. Что из своего кода тянет `amp-upstream` и какие рёбра резать?
2. Как не потерять контрактные сьюты при удалении `cmd/server`?
3. Какую версию ставить в `prometheus/common/version`?
4. Web auth в `amp-upstream`: exporter-toolkit или своя реализация?
5. Чарт: что переносится с Deployment, что удаляется (config-reloader, HPA, lite PVC)?
6. Какие planning-записи закрываются?

## Findings

### Q1. Граф зависимостей (`go list -deps`, code-read)

- Только из `cmd/server`: `cmd/server{,/handlers}`, `internal/application{,/handlers}`, `internal/business/{routing,silencing,template}`, `internal/core/{domain,silencing}`, `internal/infrastructure/{cluster,lock,silencing,snapshot,storage/memory,template,webhook}`, `internal/notification/template`, `internal/realtime`, `internal/storage`, `internal/ui`, `internal/buildinfo`, `pkg/{aiclassifier,middleware,telemetry,templatevalidator/...}`, `internal/infrastructure/investigation/{runbooks,tools}` (нужны для `agent_mode`, остаются).
- Своё ядро в `amp-upstream` через:
  - `internal/config`: `config.go` (`Routing`, `loadRouteConfig`, `validateGrouping`, `InhibitionRules` в `LoadConfig`), `grouping_adapter.go`, `inhibition_adapter.go`, `alertmanager_validation.go` (единственный импортёр `pkg/configvalidator`), `http_config_filepaths.go`, `routing_fingerprint.go`, `templating_validation.go`; hot-reload своего ядра — `reload_coordinator.go`, `reload_*`, `reloadable_*`, `update_*`, `service.go` (потребитель — только `internal/application`).
  - `core/services/alert_processor.go` (+ `route_evaluator.go`, `routing_fallback_metrics.go`): `amp-upstream` берёт только Classification и Deduplication.
  - `business/publishing`: `config_targets.go`, `config_target_templates.go` → `infrastructure/routing`; `stats_collector_{queue,mode,parallel}.go` → `infrastructure/publishing`. `upstreamtargets` использует только `TargetDiscoveryManager`/`NewTargetDiscoveryManager`/`DiscoveryStats`.
- `upstreamcore` своих импортов не имеет.

### Q2. Контрактные сьюты

- `main_phase0_contract_test.go`, `main_upstream_parity_regression_test.go` (`futureparity`), шов `upstreamcore_compat.go` (`futureparity && upstreamcore`) подменяет `registerRoutesImpl` на голый `upstreamcore.Core`; `cmd/amp-upstream` не используется. Нужны из non-test кода: `resolveRuntimeConfigPath`, `AMP_CONFIG_FILE`, `registerFutureParityBootstrapFailure`, `initTemplates` (бесполезен на upstream).
- Baseline `testdata/upstreamcore-divergences.tsv`: 47 записей — a 17 (AMP-only поверхность, срез 6), b 28 (тест закреплял своё ядро), c 2 (`version.Version` пуст), d 0.
- `internal/upstreamcore` уже имеет `TestMain` (`core_test.go`) — второй в том же тест-бинаре нельзя.

### Q3. Версия

- `cmd/amp-upstream` регистрирует `versioncollector.NewCollector("alertmanager")`, `api/v2` upstream отдаёт `versionInfo` из `prometheus/common/version`.
- `internal/buildinfo/metrics.go`: `alertmanager_build_info{version}` обязан быть semver ≥ 0.22.0 — karma выбирает API-маппер по этому значению и рвёт соединение, если не совпало (`TestAlertmanagerBuildInfo_KarmaVersionProbe`, ADR-009). Версия AMP `0.1.0` это правило ломает.
- `amp_build_info` — версия самого AMP, уже есть в `buildinfo`.

### Q4. Web auth

- `cmd/server` — своя `internal/application/webauth.go` (PROD-AUTH): формат `--web.config.file` exporter-toolkit, только `basic_auth_users`, остальные ключи — ошибка загрузки, пробы (`DefaultUnauthenticatedPaths`: `/-/healthy`, `/-/ready`) без auth, файл перечитывается при изменении. Зависимости: stdlib, `prometheus`, `bcrypt`, `yaml.v3` — пакет самодостаточный.
- exporter-toolkit (`v0.17.1 // indirect`) даёт TLS, но не умеет исключать пробы: kubelet-пробы чарта упали бы на 401.

### Q5. Чарт

- Deployment (`templates/deployment.yaml`) проводит: env БД/кеша/LLM/publishing namespace, `AMP_CONFIG_FILE`, `SERVER_WEB_CONFIG_FILE`, пробы, preStop, extraVolumes, config-reloader, scheduling. Satellites: `service.yaml` (8080/9090), `ingress.yaml` (guard auth), `servicemonitor.yaml` (basicAuth), `networkpolicy.yaml`, `poddisruptionbudget.yaml` (selector задевает PG/Valkey), `hpa.yaml` (кастомные метрики своего ядра), `rbac.yaml`+`serviceaccount.yaml` (list secrets), `pvc.yaml` (lite), `configmap.yaml` (env своего ядра), `configfile.yaml`.
- StatefulSet `upstream-*.yaml` (срез 4): аргументы ядра, кластер при N>1, PVC, `automountServiceAccountToken: false`, нет AMP-конфига, auth, SA/RBAC, scheduling.
- NetworkPolicy PG (`postgresql-networkpolicy.yaml:20-23`) и Valkey (`redis-networkpolicy.yaml:32-35`) пускают только поды `amp.selectorLabels`(+`component: application`).
- `cmd/config-reloader` требует `--health-url` (`/health/reload`) — эндпоинт `cmd/server`; у upstream-ядра его нет. Upstream-подход — rollout по checksum ConfigMap (уже в StatefulSet).
- `viper.AutomaticEnv` с заменой `.`→`_` (`config.go:683`): секреты (`DATABASE_PASSWORD`, `LLM_API_KEY`) можно передать env поверх файла AMP-конфига.

### Q6. Planning

- BUGS «срез 5» — 39, TECH-DEBT «срез 5» — 27, REVIEW-PIPELINE `[-]` на срез 5 — 24; все про удаляемый код. Правило секций: закрываются срезом — удаляются, строка в `DONE.md`.
- `LINT-ERRCHECK-CHANNEL-RECEIVE`: 7 сайтов в `infrastructure/publishing/delivery_confirmation_test.go` и `infrastructure/silencing/redis_event_bus_test.go` — оба пакета удаляются; вторая половина записи (пин линтера, `Makefile:158` `@latest`) к срезу не относится.
- 5 записей BUGS «срез 6» — про `internal/application/handlers`, код исчезает здесь, решение по API — срез 6.

## Options

`Generation: single pass (author); варианты по ключевым развилкам, fan-out не делался — каждая развилка обратима и проверяется гейтами.`

**Нарезка.** A: удаление первым, потом достройка `amp-upstream` — между подсрезами образ без рабочего бинаря с web auth. B: сначала `amp-upstream` полный (5a), потом удаление (5b), потом чарт (5c) — каждый подсрез оставляет рабочий продукт. **Выбрано B.**

**Сьюты.** A: в `cmd/amp-upstream` — шов не использует его wiring. B: тест-пакет `internal/upstreamcore/contract` (только `_test.go` + `doc.go`), тег `upstreamcore`. C: удалить. **Выбрано B** — гейт upstream-ядра и бамп-процедура (`docs/UPSTREAM_UPGRADE.md`) на нём держатся. Записи класса b переписываются на ожидание upstream в `write-tests` (b — «эталон upstream»), c — `TestMain` ставит `version.Version`, как `-ldflags`; a остаются до среза 6.

**Версия.** A: `version.Version` = версия AMP — ломает karma (< 0.22). B: `version.Version` = версия модуля upstream из `go.mod` (контракт API), `Revision`/`Branch`/`BuildUser`/`BuildDate` — сборки AMP; версия AMP — `amp_build_info`. **Выбрано B** — та же семантика, что ADR-009 у `cmd/server`; окончательное содержимое `versionInfo` — `STATUS-VERSIONINFO-CONTRACT` (срез 6).

**Web auth.** A: exporter-toolkit `web.ListenAndServe` — пробы под auth, ломает чарт. B: перенести `webauth.go` в `internal/webauth` без изменений поведения, флаг `--web.config.file`. **Выбрано B.**

**config-reloader.** A: сохранить, научить работать без `--health-url`. B: удалить бинарь, образ, sidecar — rollout по checksum, как upstream-чарты. **Выбрано B**: потребителя нет, upstream-ядро переживает рестарт (снапшоты на PVC).

**Values чарта.** A: оставить `upstreamCore.*` рядом с верхнеуровневыми ключами. B: StatefulSet читает общие ключи (`replicaCount`, `resources`, `service`, `probes`, `nodeSelector`, `tolerations`, `affinity`, `webConfig`, `ingress`, `networkPolicy`, `podDisruptionBudget`), секция `upstreamCore` → `alertmanager` (`config`, `peerTimeout`, `persistence`, `externalURL`, `extraArgs`), без `enabled`. **Выбрано B** — один workload, один набор ключей.

## Decision

Нарезка 5a → 5b → 5c на ветке `feature/upstream-core-removal`, по варианту B каждой развилки; один `deep-review` после 5c, затем `write-tests` и `testing`.

## Spec Inputs

- Рёбра резать: Q1. Пакеты удалять по `go list -deps` после резки.
- Web auth: `internal/webauth` = `internal/application/webauth.go` 1:1 + `reloadable_webauth.go`-логика не нужна (путь не перечитывается); unauthenticated paths — `DefaultUnauthenticatedPaths()` либо `server.auth.unauthenticated_paths` AMP-конфига.
- Agent loop: перенос `service_registry.go:2266-2340` и `configureRunbooks` в `cmd/amp-upstream/amp.go`.
- Чарт: см. Q5; HPA и lite-PVC удаляются (кастомные метрики своего ядра, файловое хранилище своего ядра).

## References

- `go-app/internal/upstreamcore/{core.go,doc.go}`, `cmd/amp-upstream/{main.go,amp.go,targets.go}`.
- `go-app/internal/application/{service_registry.go,webauth.go}`, `go-app/internal/buildinfo/metrics.go`.
- `go-app/cmd/server/{futureparity_compat.go,upstreamcore_compat.go,testdata/upstreamcore-divergences.tsv}`.
- `helm/amp/templates/*`, `scripts/{release-gate.sh,upstream-core-gate.sh}`, `Dockerfile`, `.github/workflows/{ci,release}.yml`.
- `docs/06-planning/{BUGS,TECH-DEBT,BACKLOG,REVIEW-PIPELINE-2026-10-21}.md`, `DECISIONS.md` § ADR-019, ADR-009, ADR-015.
