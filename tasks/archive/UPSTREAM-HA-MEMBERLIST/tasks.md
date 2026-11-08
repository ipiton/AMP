---
id: UPSTREAM-HA-MEMBERLIST
slug: upstream-ha-memberlist
status: done
created_at: 2026-11-08
updated_at: 2026-11-08
---

# Implementation Plan: HA upstream-ядра на memberlist (срез 4 ADR-019)

Ветка `feature/upstream-ha-memberlist`. Нарезка по правилу «Slice»: подсрез **4a** (фазы 1–2, ~1.5d) — кластер в коде; **4b** (фазы 3–5, ~1.5–2d) — образ, чарт, `e2e-ha`. Подсрезы последовательно на одной ветке, один `deep-review` на оба (после фазы 5), затем тесты.

## Touched Files

- 4a: `go-app/internal/upstreamcore/{core.go,reloader.go,doc.go}`, `go-app/cmd/amp-upstream/main.go`, `scripts/upstream-core-gate.sh`.
- 4b: `Dockerfile`, `helm/amp/{values.yaml,templates/_helpers.tpl,templates/upstream-*.yaml,tests/render-upstream-core.sh,README.md,CHANGELOG.md}`, `deploy/e2e-ha/{docker-compose.yml,alertmanager.yml,run.sh}` (`config.yaml` удаляется), `docs/CI.md`, `.github/workflows/ci.yml`.
- Тесты (после verdict): `go-app/internal/upstreamcore/cluster_test.go`, `go-app/cmd/amp-upstream/main_test.go`.

## Phase 1: 4a — кластер в `upstreamcore`

- [x] 1.1 Поля кластера в `Options` + умолчания `Default*` по `app.DefaultOptions`.
  verify: `cd go-app && go build ./internal/upstreamcore/`
- [x] 1.2 `setup`: `cluster.Create`, `Leave` в `onStop`, `AddState`/`SetBroadcast` для nflog и silences, `Join`, `Settle`, `api.Options.Peer`, `clusterWait`, `clusterEnabled`.
  verify: `cd go-app && go vet ./internal/upstreamcore/ && go test ./internal/upstreamcore/`
  depends: 1.1
- [x] 1.3 `reloader`: поле `peer`, `notify.Peer` в `PipelineBuilder.New`.
  verify: `cd go-app && go test ./internal/upstreamcore/`
  depends: 1.2
- [x] 1.4 `doc.go`: Known differences, комментарий метрики.
  verify: `git diff --check`

## Phase 2: 4a — бинарь и гейт

- [x] 2.1 `cmd/amp-upstream`: флаги `--cluster.*` upstream, повторяемый `--cluster.peer`.
  verify: `cd go-app && go build ./cmd/amp-upstream && go vet ./cmd/amp-upstream/`
  depends: 1.3
- [x] 2.2 `upstream-core-gate.sh`: `--cluster.listen-address=127.0.0.1:0`.
  verify: `scripts/upstream-core-gate.sh`
  depends: 2.1
- [x] 2.3 Ручная проверка: два бинаря локально, `/api/v2/status` — 2 пира, silence виден на втором.
  verify: `curl -s localhost:<port>/api/v2/status | jq '.cluster.peers|length'` = 2

## Phase 3: 4b — образ

- [x] 3.1 `Dockerfile`: второй `go build` → `/app/amp-upstream`.
  verify: `docker build -t amp:ha-test . && docker run --rm --entrypoint /app/amp-upstream amp:ha-test --help`

## Phase 4: 4b — чарт

- [x] 4.1 `values.yaml` § `upstreamCore`, помощники в `_helpers.tpl`.
  verify: `helm lint helm/amp`
- [x] 4.2 `upstream-configmap.yaml`, `upstream-service.yaml`, `upstream-service-headless.yaml`, `upstream-statefulset.yaml`.
  verify: `helm template t helm/amp --set upstreamCore.enabled=true --set upstreamCore.replicas=3 | kubeconform`-эквивалент: `helm lint helm/amp --set upstreamCore.enabled=true`
  depends: 4.1
- 4.3 перенесён в 7.3: тест рендера пишется после verdict (Full).
- [x] 4.4 `helm/amp/README.md` (секция upstream core), `helm/amp/CHANGELOG.md`.
  verify: `git diff --check`

## Phase 5: 4b — e2e-ha

- [x] 5.1 `docker-compose.yml`, `alertmanager.yml`, `run.sh` под memberlist (опрос вместо фиксированных ожиданий).
  verify: `./deploy/e2e-ha/run.sh` → `ALL PASS`
  depends: 3.1
- [x] 5.2 `docs/CI.md`, комментарий в `ci.yml`; висячая ссылка на `e2e-ha/config.yaml` в `deploy/smoke/config.yaml`.
  verify: `git diff --check`

## Phase 6: Review

- [x] 6.1 `deep-review` (independent agent) → `review-findings.md`, `review-verdict.json` `"gate": "pass"`.
  verify: `jq -r .gate tasks/UPSTREAM-HA-MEMBERLIST/review-verdict.json` = `pass`

## Phase 7: Tests (после verdict)

- [x] 7.1 `upstreamcore/cluster_test.go`: две реплики — кластер из 2, silence gossip, одно уведомление в webhook на алерт в обе; без кластера — `cluster.status` = `disabled` без пиров (так отвечает upstream; исходное ожидание «секции нет» было неверным), `alertmanager_cluster_enabled` 0. Мутация `waitFunc` → 0 даёт два уведомления — тест её ловит.
  verify: `cd go-app && go test -race -count=1 ./internal/upstreamcore/`
- [x] 7.2 `cmd/amp-upstream`: повторяемый флаг `--cluster.peer`.
  verify: `cd go-app && go test ./cmd/amp-upstream/`
- [x] 7.3 `helm/amp/tests/render-upstream-core.sh`: умолчания без объектов, 1 реплика без кластера, 3 реплики с пирами, селекторы, префикс external URL, `replicas: 0` падает.
  verify: `bash helm/amp/tests/render-upstream-core.sh`

## Phase 8: Testing и finalize

- [x] 8.1 Гейты: затронутые пакеты, `make -C go-app quality-gates-fast`, `scripts/release-gate.sh`, `git diff --check`.
- [x] 8.2 `finalize`: CHANGELOG `[Unreleased]`, BACKLOG (кандидаты, `UPSTREAM-AI-HA-DEDUP`, `CI-E2E-HA-REQUIRED`), BUGS/TECH-DEBT «срез 4» → «срез 5», DONE, NEXT, архив.

## Definition of Done

- Success criteria из `requirements.md` закрыты или явно перенесены.
- `review-verdict.json` — `pass`; гейты зелёные или красное задокументировано.
- `finalize` на ветке задачи, в `main` не мержить.
