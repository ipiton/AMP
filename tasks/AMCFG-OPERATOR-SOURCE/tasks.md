---
id: AMCFG-OPERATOR-SOURCE
slug: amcfg-operator-source
stream: pilot-replacement
type: feature
status: active
created_at: 2026-11-18
updated_at: 2026-11-18
based_on:
  - requirements.md
  - research.md
  - Spec.md
---

# Implementation Plan: AlertmanagerConfig через prometheus-operator

**Based on:** requirements.md / research.md / Spec.md
**Date:** 2026-11-18

## Touched Files

- `helm/amp/templates/operator-alertmanager.yaml` - Secret базового конфига и CR
- `helm/amp/templates/{statefulset.yaml,configmap.yaml,_validate.tpl,_helpers.tpl,NOTES.txt}` - режим и проверки
- `helm/amp/values.yaml`, `helm/amp/README.md`, `helm/amp/CHANGELOG.md` - контракт и документация
- `helm/amp/tests/render-operator-source.sh` - render-тесты
- `docs/06-planning/DECISIONS.md`, `docs/06-planning/AMCFG-SPIKE-2026-11-18.md` - ADR-020 и вердикт спайка

## Phase 1: Чарт

- [x] **1.1** Values, валидация, CR и монтирования <!-- verify: helm template в обоих режимах -->
- [x] **1.2** Сквозная проверка на k3s <!-- verify: новый AMC появляется в /api/v2/status без рестарта -->

## Phase 2: Ревью и тесты

- [ ] **2.1** deep-review, `review-verdict.json` с `gate: pass`
- [ ] **2.2** `render-operator-source.sh` <!-- depends: 2.1 | verify: скрипт зелёный -->

## Phase 3: Документация и гейты

- [ ] **3.1** ADR-020, README, CHANGELOG, отчёт спайка
- [ ] **3.2** `scripts/release-gate.sh`, `git diff --check`

## Definition of Done

Критерии `requirements.md` выполнены, гейты зелёные или расхождения записаны.
