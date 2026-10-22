---
id: HELM-VCT-CHART-LABELS
slug: helm-vct-chart-labels
stream: Helm
type: bug
status: active
created_at: 2026-10-22
updated_at: 2026-10-22
based_on:
  - requirements.md
  - Spec.md
---

# Implementation Plan: метки volumeClaimTemplates без версии чарта

**Based on:** requirements.md / Spec.md  
**Date:** 2026-10-22

## Touched Files

- `helm/amp/templates/redis-statefulset.yaml` - метки VCT
- `CHANGELOG.md`, `helm/amp/CHANGELOG.md`, `helm/amp/README.md` - migration note
- `helm/amp/tests/render-vct-labels.sh` - render-тест (Phase 2)

## Phase 1: Implement

- [x] **1.1** VCT `redis-data`: `amp.labels` → `amp.selectorLabels` + `component: redis-storage` <!-- verify: helm template t helm/amp | yq 'select(.kind=="StatefulSet" and .metadata.name=="t-amp-redis") | .spec.volumeClaimTemplates[0].metadata.labels' — нет helm.sh/chart и version; helm lint helm/amp -->
- [x] **1.2** Migration note: корневой `CHANGELOG.md` (Fixed + Breaking changes / migration notes), `helm/amp/CHANGELOG.md` (Fixed), README § Upgrading <!-- depends: 1.1 | verify: git diff --check; ручная вычитка -->

**Phase verification:** `helm lint helm/amp`, `helm/amp/tests/render-*.sh` зелёные.

## Phase 2: Tests (write-tests)

- [x] **2.1** `helm/amp/tests/render-vct-labels.sh`: в VCT нет `helm.sh/chart`/`app.kubernetes.io/version`; рендер копии чарта с другим `version`/`appVersion` даёт идентичный VCT; на `main` тест красный <!-- verify: bash helm/amp/tests/render-vct-labels.sh -->
- [x] **2.2** Подключить тест туда, где release-gate гоняет render-тесты (если список явный) <!-- verify: release-gate шаг `helm-tests` берёт `helm/amp/tests/*.sh` глобом — подключено без правок -->

## Phase 3: Testing

- [x] **3.1** k3d: install `main` (standard) → запись ключа в Valkey → orphan-удаление `<fullname>-redis` → upgrade веткой → upgrade веткой с бампом `version`/`appVersion` → UID PVC и ключ на месте <!-- verify: evidence/k3d-acceptance.md; нужно подтверждение владельца на kubectl delete -->
- [x] **3.2** Гейты AMP: `quality-gates-fast` не требуется (Go не менялся), `scripts/release-gate.sh` (чарт изменён), `git diff --check`

## Phase 4: Finalize

- [x] **4.1** `/finalize`: DONE.md, NEXT.md (WIP), BUGS (закрыть `HELM-VCT-CHART-LABELS`), архив workspace

## Deep Review

Не применимо (Spec § Deep Review): только `R`, дифф мал, без `C+X` и нового паттерна. Порядок: implement → write-tests → testing.

## Итог testing

- 3.1: шаги 1–3 полностью (`evidence/k3d-acceptance.md`): `Forbidden` без миграции, после orphan-удаления (выполнил владелец — хук блокирует удаление k8s-ресурсов) upgrade прошёл, под/PVC/ключ те же. Шаг 4 — upgrade с бампом 0.1.1 принят API; состояние после rollout не снято: кластер удалён владельцем во время ожидания.
- 3.2: `quality-gates-fast` PASS; release-gate @ `efa4316` 11/12 PASS, `lint` FAIL предсуществующий (golangci-lint 2.15.0, 7 errcheck в тестах вне диффа) → BUGS `LINT-ERRCHECK-CHANNEL-RECEIVE`; `git diff --check` чист.

## Definition of Done

- [x] All steps are complete or explicitly marked blocked/skipped
- [x] Success criteria from `requirements.md` are covered
- [x] Contracts from `Spec.md` are implemented or deviations are recorded
- [x] Deep review verdict is `pass`, or deep review was not required
- [x] Tests for changed behavior are added or updated
- [x] Phase checks pass
- [x] Docs/planning are updated if behavior, contracts, or process changed
