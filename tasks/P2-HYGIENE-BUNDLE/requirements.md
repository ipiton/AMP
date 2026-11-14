---
id: P2-HYGIENE-BUNDLE
slug: p2-hygiene-bundle
stream: Production Readiness
type: tech-debt
priority: medium
status: active
created_at: 2026-11-14
updated_at: 2026-11-14
---

# Requirements: P2-гигиена (бандл)

Участники — записи `BACKLOG.md` § «Production Readiness — блокеры» / P2, порядок фаз — по `tasks.md`:
`HELM-MONITORING-CRD-GATE`, `RELEASE-TAG-ON-MAIN-GUARD`, `GO-MOD-TIDY-CHECK`, `VALKEY-PROBE-ARGV-PASSWORD`, `HELM-VALUES-DEV-ALIGN`, `LLM-ERROR-BODY-CAP`.

## Problem Framing

- **Symptom:**
  - `helm install` с дефолтными values на кластере без CRD prometheus-operator падает: `no matches for kind "ServiceMonitor"` (pre-release review `PROD-RELEASE-V010` F2; в v0.1.0 — только предупреждение в Known Gaps).
  - `release.yml` публикует образ с любого коммита, на который повесили тег `v*`, даже не из `main` (F9).
  - CI не ловит не-tidy `go-app/go.mod`.
  - Пробы Valkey передают пароль в argv (`redis-cli -a $REDIS_PASSWORD`): он виден в `ps` и `/proc/<pid>/cmdline`, `redis-cli` пишет warning в каждую пробу.
  - `values-dev.yaml` расходится с дефолтами (requests 500m/512Mi против 100m/128Mi) и дублирует дефолтные ключи.
  - `LLM-ERROR-BODY-CAP`: запись говорит, что тело ошибки провайдера до 1 MiB идёт в лог.
- **Root Cause:** гейты и шаблоны написаны до появления этих сценариев; часть записей BACKLOG могла устареть.
- **Why Now:** до итогов пилота (2026-11-16) нет P0/P1-работы, которая не зависела бы от решения владельца; P2 дешёвые и снимают Known Gaps v0.1.0.
- **How We Measure:** render-тесты на каждый шаблонный участник, `scripts/release-gate.sh` PASS (включая новый шаг tidy), `actionlint`/ручная проверка shell-шага release.

## Risk Profile

- **Signals:** `S X R`
  - `S` — supply chain (кто и с какого коммита публикует образ), пароль Valkey в argv.
  - `X` — чарт, CI workflow, release-gate скрипт.
  - `R` — меняется рендер чарта (ServiceMonitor/PrometheusRule при отсутствии CRD).
- **Tier:** Full
- **Notes:** `S` ⇒ Full и обязательный deep-review. Публичный API AMP не меняется; values чарта получают один новый ключ (`monitoring.assumeCRDs`).

## User Stories

1. Как оператор без prometheus-operator, я хочу, чтобы `helm install` с дефолтами проходил, а мониторинг-объекты просто не создавались.
2. Как оператор с GitOps (`helm template` без доступа к кластеру), я хочу явно сказать чарту «CRD есть», чтобы ServiceMonitor не пропал молча.
3. Как владелец релизов, я хочу, чтобы образ публиковался только с коммита из `main`.
4. Как владелец секретов, я хочу, чтобы пароль Valkey не попадал в argv процессов.

## Success Criteria

- [ ] `ServiceMonitor` и `PrometheusRule` (AMP, PostgreSQL, Valkey) рендерятся, только если кластер отдаёт `monitoring.coreos.com/v1` или `monitoring.assumeCRDs: true`; render-тесты на оба случая; `NOTES.txt` сообщает, что мониторинг пропущен.
- [ ] Guard ServiceMonitor на `webConfig` без кредов продолжает срабатывать, когда SM рендерится.
- [ ] `release.yml` до логина в реестр проверяет `git merge-base --is-ancestor "$GITHUB_SHA" origin/main` (с `fetch-depth: 0`) и падает с `::error::` иначе.
- [ ] `scripts/release-gate.sh` содержит шаг `go mod tidy -diff` (go-app) и он зелёный на текущем `main`.
- [ ] Пробы Valkey: `REDISCLI_AUTH="$REDIS_PASSWORD" redis-cli ping`, в рендере нет `-a $`; render-тест.
- [ ] `values-dev.yaml` не задаёт `resources` и не дублирует дефолтные `monitoring`/`ingress`; `helm-dev` в release-gate зелёный.
- [ ] `LLM-ERROR-BODY-CAP` сверён с кодом: закрыт как сделанный (с указанием коммита) или доделан.
- [ ] README чарта, Known Gaps и `CHANGELOG.md` `[Unreleased]` отражают изменения.

## Non-Goals

- Проверка «CI на теге зелёный» в `release.yml` (нужны `actions: read` и опрос статусов — отдельная задача, если понадобится).
- Пересмотр security context в `values-dev.yaml` (root, privilege escalation — сознательно «relaxed for development»).
- Остальной `CI-SUPPLY-CHAIN`.

## Constraints

- **Scope:** `helm/amp/templates/{servicemonitor,postgresql-prometheus-rules,redis-servicemonitor,redis-prometheusrule,redis-statefulset,NOTES.txt,_helpers.tpl}`, `helm/amp/values*.yaml`, `helm/amp/tests/`, `helm/amp/README.md`, `.github/workflows/release.yml`, `scripts/release-gate.sh`, `CHANGELOG.md`, planning.
- **Security:** пароль Valkey не в argv; release-guard не получает новых прав (`contents: read` достаточно для fetch).
- **Compatibility:** на кластере с CRD (и в Argo CD / Flux, которые передают capabilities кластера) рендер не меняется. `helm template` без `--api-versions monitoring.coreos.com/v1` и без `monitoring.assumeCRDs: true` больше не выдаёт ServiceMonitor/PrometheusRule — миграционная заметка в CHANGELOG.

## Discovery Notes

- Similar tasks: `tasks/archive/PUBLISH-QUICK-FIX-BUNDLE/` (формат бандла), `tasks/archive/HELM-PROMRULE-LABELS/` (render-тесты мониторинга).
- Relevant patterns: `helm/amp/tests/render-servicemonitor.sh` (assert_contains/assert_absent), `scripts/release-gate.sh` `run_step`.

## Research (mini)

- `HELM-MONITORING-CRD-GATE`: `.Capabilities.APIVersions.Has "monitoring.coreos.com/v1"` истинно при `helm install/upgrade` против кластера с CRD; `helm template`/`helm lint` без кластера видят только встроенные API ⇒ нужен явный override. Argo CD передаёт API-версии кластера в `helm template`, Flux рендерит через SDK с кластером — для них override не нужен. Решение: helper `amp.monitoringCRDs` = Has OR `monitoring.assumeCRDs`.
- `LLM-ERROR-BODY-CAP`: в `go-app/internal/infrastructure/llm/client.go` уже есть `maxErrorBodyBytes = 512` и `truncateBody` (тест `retry_classify_test.go`), путь `chat.go`/`doChat` и `client.go` используют его; проверить оставшиеся места из записи (`investigate_with_tools.go`).
- `RELEASE-TAG-ON-MAIN-GUARD`: `actions/checkout` с `fetch-depth: 0` тянет все ветки, `origin/main` доступен без `persist-credentials`.
- `GO-MOD-TIDY-CHECK`: `go mod tidy -diff` есть с Go 1.23, toolchain модуля — 1.26.
