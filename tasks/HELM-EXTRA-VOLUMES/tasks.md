---
id: HELM-EXTRA-VOLUMES
slug: helm-extra-volumes
stream: operations
type: feature
status: active
created_at: 2026-10-20
updated_at: 2026-10-20
based_on:
  - requirements.md
  - research.md
  - Spec.md
---

# Implementation Plan: extraVolumes / extraVolumeMounts

**Based on:** requirements.md / research.md / Spec.md  
**Date:** 2026-10-20

## Touched Files

- `helm/amp/values.yaml` — ключи + пример
- `helm/amp/templates/deployment.yaml` — guard + рендер
- `helm/amp/tests/render-extra-volumes.sh` — render-тест (write-tests)
- `helm/amp/README.md`, `docs/CONFIGURATION_GUIDE.md` — пример Telegram + Slack
- `helm/amp/CHANGELOG.md`?, `CHANGELOG.md` — [Unreleased] Added (finalize)

## Phase 1: Chart

- [ ] **1.1** values.yaml: `extraVolumes: []`, `extraVolumeMounts: []` с комментарием и примером <!-- verify: helm lint helm/amp -->
- [ ] **1.2** deployment.yaml: рендер в `amp` и в `volumes`, условия блоков <!-- verify: helm template с примером -->
- [ ] **1.3** deployment.yaml: guard на конфликт имён/mountPath <!-- verify: helm template с конфликтом падает -->
- [ ] **1.4** Байт-в-байт сравнение рендера с main для пустых значений (default, lite, configFile+webConfig, production) <!-- verify: evidence/render-invariance.txt -->

## Phase 2: Docs

- [ ] **2.1** README: раздел Receiver Credentials From A Secret + параметры
- [ ] **2.2** CONFIGURATION_GUIDE: пример Telegram + Slack через `*_file` в Kubernetes

**Phase verification:** чтение, `git diff --check`

## Phase 3: Review → Tests → Testing (после deep-review pass)

- [ ] **3.1** deep-review (независимый агент) → `review-verdict.json` pass
- [ ] **3.2** `helm/amp/tests/render-extra-volumes.sh`: T1 пусто = без изменений, T2 Secret смонтирован в `amp`, не в reloader, `configFile.content` ссылается на путь, T3 конфликты падают
- [ ] **3.3** k3d: AMP стартует с `bot_token_file`/`api_url_file` из Secret (+ `defaultMode: 0400`) → `evidence/k3d-extra-volumes.md`
- [ ] **3.4** `scripts/release-gate.sh` → `evidence/release-gate.txt`, `git diff --check`

## Definition of Done

- [ ] All steps are complete or explicitly marked blocked/skipped
- [ ] Success criteria from `requirements.md` are covered
- [ ] Contracts from `Spec.md` are implemented or deviations are recorded
- [ ] Deep review verdict is `pass`
- [ ] Tests for changed behavior are added or updated
- [ ] Phase checks pass
- [ ] Docs/planning are updated
