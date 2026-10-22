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

- [ ] **1.1** VCT `redis-data`: `amp.labels` → `amp.selectorLabels` + `component: redis-storage` <!-- verify: helm template t helm/amp | yq 'select(.kind=="StatefulSet" and .metadata.name=="t-amp-redis") | .spec.volumeClaimTemplates[0].metadata.labels' — нет helm.sh/chart и version; helm lint helm/amp -->
- [ ] **1.2** Migration note: корневой `CHANGELOG.md` (Fixed + Breaking changes / migration notes), `helm/amp/CHANGELOG.md` (Fixed), README § Upgrading <!-- depends: 1.1 | verify: git diff --check; ручная вычитка -->

**Phase verification:** `helm lint helm/amp`, `helm/amp/tests/render-*.sh` зелёные.

## Phase 2: Tests (write-tests)

- [ ] **2.1** `helm/amp/tests/render-vct-labels.sh`: в VCT нет `helm.sh/chart`/`app.kubernetes.io/version`; рендер копии чарта с другим `version`/`appVersion` даёт идентичный VCT; на `main` тест красный <!-- verify: bash helm/amp/tests/render-vct-labels.sh -->
- [ ] **2.2** Подключить тест туда, где release-gate гоняет render-тесты (если список явный) <!-- verify: grep render-vct-labels scripts/release-gate.sh -->

## Phase 3: Testing

- [ ] **3.1** k3d: install `main` (standard) → запись ключа в Valkey → orphan-удаление `<fullname>-redis` → upgrade веткой → upgrade веткой с бампом `version`/`appVersion` → UID PVC и ключ на месте <!-- verify: evidence/k3d-acceptance.md; нужно подтверждение владельца на kubectl delete -->
- [ ] **3.2** Гейты AMP: `quality-gates-fast` не требуется (Go не менялся), `scripts/release-gate.sh` (чарт изменён), `git diff --check`

## Phase 4: Finalize

- [ ] **4.1** `/finalize`: DONE.md, NEXT.md (WIP), BUGS (закрыть `HELM-VCT-CHART-LABELS`), архив workspace

## Deep Review

Не применимо (Spec § Deep Review): только `R`, дифф мал, без `C+X` и нового паттерна. Порядок: implement → write-tests → testing.

## Definition of Done

- [ ] All steps are complete or explicitly marked blocked/skipped
- [ ] Success criteria from `requirements.md` are covered
- [ ] Contracts from `Spec.md` are implemented or deviations are recorded
- [ ] Deep review verdict is `pass`, or deep review was not required
- [ ] Tests for changed behavior are added or updated
- [ ] Phase checks pass
- [ ] Docs/planning are updated if behavior, contracts, or process changed
