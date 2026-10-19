---
id: HELM-SINGLE-NODE-DEFAULTS
slug: helm-single-node-defaults
stream: operations
type: feature
status: active
created_at: 2026-10-19
updated_at: 2026-10-19
based_on:
  - requirements.md
  - research.md
  - Spec.md
---

# Implementation Plan: дефолты чарта под одну ноду

**Based on:** requirements.md / research.md / Spec.md
**Date:** 2026-10-19
**Tier:** Full (`R M`) → `implement → deep-review → write-tests → testing → finalize → merge-to-main`

Обозначение в verify: `T` = `helm template t helm/amp`. Все команды запускаются из корня репозитория.

## Touched Files

- `helm/amp/Chart.yaml` — удалить `dependencies`
- `helm/amp/Chart.lock` — удалить
- `helm/amp/values.yaml` — HPA off, requests, PG-бюджет, `valkey.image`, удалить `cache.resources`, комментарии
- `helm/amp/values-small.yaml` — новый пример
- `helm/amp/templates/hpa.yaml` — снять `resource-policy: keep`
- `helm/amp/templates/redis-statefulset.yaml` — init: перенос данных Redis ≥ 7.4
- `helm/amp/templates/pvc.yaml` — только `profile: lite`
- `helm/amp/templates/NOTES.txt` — fallback `maxConnections` 100, строка «Resources: 500m…»
- `scripts/release-gate.sh` — `step_helm_deps` без `Chart.lock` проходит
- `deploy/e2e-ha/docker-compose.yml` — образ Valkey
- `helm/amp/README.md`, `helm/amp/CHANGELOG.md`, `CHANGELOG.md` — изменения и migration notes
- `docs/06-planning/DECISIONS.md` — ADR-017
- `helm/amp/tests/render-single-node.sh` (новый), `helm/amp/tests/render-postgresql.sh` (T6) — Phase 4
- `tasks/HELM-SINGLE-NODE-DEFAULTS/evidence/` — приёмка k3d

## Phase 1: чарт — дефолты и кеш

> **Wave 1** — независимые шаги

- [x] **1.1** `values.yaml`: `autoscaling.enabled: false`; `templates/hpa.yaml`: удалить `helm.sh/resource-policy: keep`. Комментарий у `autoscaling`: дефолт под одну ноду, HA — `values-production.yaml`. <!-- verify: T | grep -c 'kind: HorizontalPodAutoscaler' → 0; T --set autoscaling.enabled=true | grep -c 'resource-policy' → 0; T | yq 'select(.kind=="Deployment" and .metadata.name=="t-amp").spec.replicas' → 1 -->
- [x] **1.2** `values.yaml`: requests по таблице Spec § 2 (amp 100m/128Mi; postgres 100m/256Mi; postgres-exporter 10m/32Mi; valkey 50m/64Mi; valkey exporter 10m/32Mi). Limits не трогать, кроме PG (1.3). <!-- verify: скрипт суммы requests по рендеру (как в research) → 270m / 512Mi -->
- [x] **1.3** `values.yaml` PG: `maxConnections: 100`, `sharedBuffers: 128MB`, `effectiveCacheSize: 512MB`, resources req 100m/256Mi lim 1000m/1Gi; комментарий бюджета (2 пода × 25 + exporter + `superuserReservedConnections`, запас до 3 подов; HPA → поднять). `NOTES.txt`: fallback `| default 250` → `100`; строку «Resources: 500m CPU, 512Mi RAM (per pod)» брать из `.Values.resources.requests` или удалить. <!-- verify: T -s templates/postgresql-configmap.yaml | grep -E 'max_connections = 100|shared_buffers = 128MB'; helm install --dry-run=client t helm/amp | grep -c 'allows max_connections' → 0; то же с --set autoscaling.enabled=true → 1 -->
- [x] **1.4** `Chart.yaml`: удалить блок `dependencies`; `git rm helm/amp/Chart.lock`. `values.yaml`: `valkey.image.repository: valkey/valkey`, `tag: "8.1.10-alpine"`; удалить `cache.resources`; обновить комментарии `cache`/`valkey` (один инстанс, Valkey, лицензия; убрать «Options: redis»). <!-- verify: T | grep -c 'name: t-valkey' → 0; T | grep 'image: valkey/valkey:8.1.10-alpine' → 1 строка; helm lint helm/amp -->
- [x] **1.5** `templates/pvc.yaml`: обернуть в `{{- if eq .Values.profile "lite" }}`. <!-- verify: T | grep -c 't-amp-pvc' → 0; T --set profile=lite | grep -c 'name: t-amp-pvc' → 1 -->
- [x] **1.6** `scripts/release-gate.sh` `step_helm_deps`: если нет `$HELM_CHART_DIR/Chart.lock` — `log "no chart dependencies"` и `return 0`; комментарий над функцией дополнить. <!-- verify: scripts/release-gate.sh — строка helm-deps PASS (полный прогон в Phase 3) -->
- [x] **1.7** `deploy/e2e-ha/docker-compose.yml`: `redis:7-alpine` → `valkey/valkey:8.1.10-alpine`; проверить `command`/healthcheck сервиса (`redis-cli` есть в образе). <!-- verify: docker compose -f deploy/e2e-ha/docker-compose.yml config | grep valkey/valkey -->

> **Wave 2** — зависит от Wave 1

- [x] **1.8** `templates/redis-statefulset.yaml` `config-init`: под `{{- if contains "valkey" .Values.valkey.image.repository }}` — для `/data/dump.rdb` и `/data/appendonlydir/*.base.rdb` прочитать 9 байт; если `REDIS` + версия ≥ 12 — `mkdir /data/redis-pre-valkey-$(date +%s)` и `mv` туда `dump.rdb` и `appendonlydir`, лог одной строкой; иначе лог «no incompatible data». Комментарий: почему (RDB v12 — только Redis ≥ 7.4, Valkey 8.1 читает ≤ 11), перепроверить при Valkey 9.x. `set -e` сохраняется, работа под uid 999. <!-- depends: 1.4 | verify: T -s templates/redis-statefulset.yaml | grep -c redis-pre-valkey ≥ 1; T --set valkey.image.repository=redis -s templates/redis-statefulset.yaml | grep -c redis-pre-valkey → 0; ручной прогон скрипта в busybox:1.36 на копиях /data из evidence (v12 → перенос, v11 → нет, пусто → нет, повтор → нет) -->
- [x] **1.9** `helm/amp/values-small.yaml` по Spec § 5 (`autoscaling.enabled: false` явно; PVC PG 5Gi, Valkey 1Gi; Valkey limit 256Mi, `maxmemory: 192mb`; AMP limit 512Mi; PG limit 512Mi, `sharedBuffers: 64MB`, `effectiveCacheSize: 256MB`, `maxConnections: 60`), комментарий к каждому ключу. <!-- depends: 1.2, 1.3 | verify: helm lint helm/amp -f helm/amp/values-small.yaml; helm template t helm/amp -f helm/amp/values-small.yaml >/dev/null -->

**Phase verification:** `helm lint helm/amp` для дефолтов, `values-dev.yaml`, `values-production.yaml` (с `tests/values-production-placeholders.yaml`), `values-small.yaml`. Существующие `helm/amp/tests/render-*.sh` проходят, кроме ожидаемо падающего T6 в `render-postgresql.sh` (250 → 100, правится в Phase 4). Диф рендера `values-production.yaml` против `main`: только удалённые объекты сабчарта, `amp-pvc` и аннотация HPA.

## Phase 2: документация и решения

- [x] **2.1** `helm/amp/README.md`: § «Read before installing» (дефолт — одна нода, HPA выкл.), список компонентов (один кеш, Valkey, сабчарта нет), таблица `cache.enabled` (дефолт `true`), бюджет соединений (100, когда поднимать), раздел про `values-small.yaml`. <!-- verify: grep -n 'valkey\|values-small\|HPA' helm/amp/README.md; нет упоминания «subchart is also deployed» -->
- [x] **2.2** `CHANGELOG.md` `[Unreleased]`: Changed + «Breaking changes / migration notes» по Spec § Rollout (HPA и одноразовое `kubectl delete hpa`, `--force-conflicts`, Redis → Valkey и повтор уведомлений, `redis-pre-valkey-*` и его удаление, PG-параметры и рестарт, удалённые сабчарт/`amp-pvc`, `cache.resources`); `helm/amp/CHANGELOG.md` — то же кратко. <!-- verify: чтение; каждый пункт Spec § Rollout 1–5 есть -->
- [x] **2.3** `docs/06-planning/DECISIONS.md`: ADR-017 «Кеш AMP — Valkey; Redis ≥ 7.4 не используется (лицензия)» — контекст, решение, отклонённое (Redis 7.2 BSD — EOL; сабчарт groundhog2k), следствия (пин образа, init-перенос, внешний кеш — выбор оператора). <!-- verify: grep -n 'ADR-017' docs/06-planning/DECISIONS.md -->

**Phase verification:** `git diff --check`; ссылки на файлы в доках существуют.

## Phase 3: deep-review

- [x] **3.1** `/deep-review` (обязателен, сигнал `M`) → `tasks/HELM-SINGLE-NODE-DEFAULTS/review-verdict.json` с `"gate": "pass"`; находки — фиксами и повторным ревью. <!-- depends: Phase 1, Phase 2 | verify: jq -r .gate review-verdict.json → pass -->

## Phase 4: тесты (после verdict `pass`)

- [ ] **4.1** `helm/amp/tests/render-single-node.sh`: дефолты — нет HPA, `replicas: 1`, сумма requests = 270m/512Mi, нет `t-valkey`/`amp-pvc`, образ `valkey/valkey`, init с переносом; `autoscaling.enabled=true` — HPA без `keep`; `profile=lite` — `amp-pvc` есть; `valkey.image.repository=redis` — init без переноса; `values-small.yaml` рендерится; `values-production.yaml` — HPA 3–20. Мутации: вернуть `keep` / `enabled: true` / убрать условие init — тест краснеет. <!-- depends: 3.1 | verify: bash helm/amp/tests/render-single-node.sh -->
- [ ] **4.2** `render-postgresql.sh` T6: ожидания 250 → 100; кейс «HPA on, maxReplicas 10 → предупреждение», «maxReplicas 3 → нет» (3 × 25 < 100). <!-- depends: 3.1 | verify: bash helm/amp/tests/render-postgresql.sh -->
- [ ] **4.3** Тест init-скрипта: вынести тело проверки так, чтобы его можно было прогнать в `busybox:1.36` на фикстурах (RDB v12, v11, пусто, повторный запуск) — либо обоснованно ограничиться k3d A2 + ручным прогоном 1.8, записав это в evidence. <!-- depends: 3.1 | verify: прогон фикстур или evidence -->

- [ ] **4.4** Из ревью: тест init на фикстуре «после отката» (dump v12 + base v11 → переносится только `dump.rdb`); `valkey.image.tag` пустой → render fails; NOTES — предупреждение о бюджете при `maxReplicas × 25 + 5 > maxConnections`, строка кеша для `cache.enabled=false` и `valkey.replicas: 0`; `redis-prometheusrule` берёт `maxmemory` из values. Убрать из `tests/*.sh` блок `helm dependency build` (зависимостей нет). <!-- depends: 3.1 | verify: bash helm/amp/tests/render-single-node.sh -->

**Phase verification:** все `helm/amp/tests/*.sh` PASS.

## Phase 5: testing и приёмка

- [ ] **5.1** Гейты AMP: `make -C go-app quality-gates-fast` (Go не меняется — контрольный), `scripts/release-gate.sh` → `RESULT: PASS`, вывод в `evidence/release-gate.txt`; `git diff --check`. <!-- depends: Phase 4 | verify: grep 'RESULT: PASS' evidence/release-gate.txt -->
- [ ] **5.2** k3d **A1**: кластер с `--kubelet-arg=kube-reserved=cpu=7` (allocatable ≈ 1 vCPU); чарт `main` → поды `Pending`; ветка → все Ready, `POST /api/v2/alerts` → 200, `redis_up 1`, `pg_up 1`. <!-- verify: evidence/k3d-acceptance.md § A1 -->
- [ ] **5.3** k3d **A2**: `main` (HPA on) + поток алертов на тестовый webhook → upgrade веткой: лог init с переносом, `ls /data/redis-pre-valkey-*`, Valkey Ready, ingest 200; HPA-сирота → `kubectl delete hpa` → 1 реплика; подсчёт повторных уведомлений (Open Question Spec); лог AMP — кеш подключён или fallback; предупреждение NOTES о HPA-сироте. <!-- verify: evidence/k3d-acceptance.md § A2 -->
- [ ] **5.4** k3d **A3**: `helm rollback` → Redis 7.4 стартует на данных Valkey. **A3** также: процедура восстановления исходных данных (CHANGELOG 6) на кластере. **A4**: установка `-f values-small.yaml`, всё Ready; `kubectl top` PG при открытом пуле AMP (25 соединений) — в evidence, иначе TECH-DEBT. <!-- verify: evidence/k3d-acceptance.md § A3, A4 -->
- [ ] **5.5** Удалить k3d-кластеры задачи (`single-node` и приёмочный) — с подтверждения владельца. <!-- verify: k3d cluster list -->

## Phase 6: finalize

- [ ] **6.1** `/finalize`: DONE.md, NEXT.md (WIP → пусто; Queue пополнить — сейчас 1 задача), BUGS (закрыть `VALKEY-SUBCHART-INIT-ERROR`, `AMP-PVC-PENDING-STANDARD`), TECH-DEBT `HELM-CHART-GAPS` (снять «два Redis»), новые BUGS `AMP-CACHE-NOAUTH`, `AMP-CACHE-NO-RECONNECT`, `SILENCE-SYNC-LIMIT`, `HELM-VCT-CHART-LABELS`, TECH-DEBT (Go-тест `redis:7-alpine`, «2-10 replicas» в `config.go`), BACKLOG `values-dev.yaml` под новые дефолты, BACKLOG (закрыть P1 `HELM-SINGLE-NODE-DEFAULTS`), архив workspace. <!-- depends: Phase 5 | verify: git status чистый, workspace в tasks/archive/ -->

## Definition of Done

- [ ] All steps are complete or explicitly marked blocked/skipped
- [ ] Success criteria from `requirements.md` are covered
- [ ] Contracts from `Spec.md` are implemented or deviations are recorded
- [ ] Deep review verdict is `pass`, or deep review was not required
- [ ] Tests for changed behavior are added or updated
- [ ] Phase checks pass
- [ ] Docs/planning are updated if behavior, contracts, or process changed

## Deviations from Spec (implement, 2026-10-19)

- `templates/postgresql-configmap.yaml`: заголовок и комментарии рендерящегося `postgresql.conf` («HPA Cluster Mode», «Increased from 100 to 250») переписаны, fallback-значения шаблона (`| default 250`, `256MB`, `1GB`) приведены к новым дефолтам (100, 128MB, 512MB) — иначе конфиг противоречил values.
- `NOTES.txt`: строка ресурсов берётся из `.Values.resources` через `dig` (переживает `resources: null`); строка «HPA disabled» с ⚠️ стала информационной — это теперь дефолт.
- `values-production.yaml`: из шапки убрана инструкция `helm dependency build` (зависимостей больше нет). Значения файла не менялись.
- `helm/amp/docs/POSTGRESQL_RESTORE_GUIDE.md`: «default `autoscaling.enabled: true`» → off by default.
- Удалён локальный gitignored `helm/amp/charts/valkey-2.1.3.tgz`: Helm подхватывает содержимое `charts/` и без `dependencies`.
- Вне scope, на `finalize` в TECH-DEBT: `go-app/internal/infrastructure/inhibition/integration_test.go:24` поднимает `redis:7-alpine` через testcontainers (Go-код не меняем по Non-Goals); `go-app/internal/config/config.go:236,1575` описывают standard как «2-10 replicas».
