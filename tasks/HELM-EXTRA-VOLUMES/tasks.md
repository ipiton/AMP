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
- `go-app/internal/alertmanager/config/config.go` — поля `*_file` (Scope Change)
- `go-app/pkg/configvalidator/validators/receiver.go`, `ERROR_CODES.md`, `validators/security.go` (подсказка W300) — E113/E115/E122 учитывают `*_file`
- `go-app/pkg/configvalidator/validators/*_test.go`, `go-app/internal/config/*_test.go` — тесты (write-tests)
- `docs/ALERTMANAGER_COMPATIBILITY.md` — пути `*_file` и валидатор
- `CHANGELOG.md` — [Unreleased] Added (finalize)

## Phase 1: Chart

- [x] **1.1** values.yaml: `extraVolumes: []`, `extraVolumeMounts: []` с комментарием и примером <!-- verify: helm lint helm/amp -->
- [x] **1.2** deployment.yaml: рендер в `amp` и в `volumes`, условия блоков <!-- verify: helm template с примером -->
- [x] **1.3** deployment.yaml: guard на конфликт имён/mountPath <!-- verify: helm template с конфликтом падает -->
- [x] **1.4** Байт-в-байт сравнение рендера с main для пустых значений (default, lite, configFile+webConfig, production) <!-- verify: evidence/render-invariance.txt -->

## Phase 2: Docs

- [x] **2.1** README: раздел Receiver Credentials From A Secret + параметры
- [x] **2.2** CONFIGURATION_GUIDE: пример Telegram + Slack через `*_file` в Kubernetes

**Phase verification:** чтение, `git diff --check`

## Phase 2b: Validator `*_file` (Scope Change 2026-10-20, review F1)

- [x] **2b.1** `internal/alertmanager/config`: поля `*_file` у global (slack_api_url_file, smtp_auth_password_file), slack, webhook, pagerduty, telegram <!-- verify: go build ./... -->
- [x] **2b.2** `validators/receiver.go`: E113/E115/E122 считают `*_file` заданным значением; `ERROR_CODES.md` <!-- verify: go test ./pkg/configvalidator/... -->
- [x] **2b.3** Подсказка W300 в `validators/security.go` упоминает `*_file` (review R2-F4)

## Phase 3: Review → Tests → Testing (после deep-review pass)

- [x] **3.1** deep-review (независимый агент, 2 раунда) → `review-verdict.json` pass
- [x] **3.2** `helm/amp/tests/render-extra-volumes.sh`: T1 пусто = без изменений, T2 Secret смонтирован в `amp`, не в reloader, `configFile.content` ссылается на путь, T3 конфликты падают
- [x] **3.2b** Go-тесты: валидатор принимает каждый `*_file` и даёт E113/E115/E122 без значения и файла; строгий парсер принимает ключи; `config.LoadConfig` с реальными файлами и ошибка без обоих
- [x] **3.3** k3d на образе `1674cdb`: Telegram `bot_token_file` и Slack `api_url_file` из Secret (+ `defaultMode: 0400`) → `evidence/k3d-extra-volumes.md` §2–§3
- [ ] **3.3b** Перерендер `evidence/render-invariance.txt` на финальном коммите (R2-F7)
- [ ] **3.4** `scripts/release-gate.sh` → `evidence/release-gate.txt`, `git diff --check`

## Definition of Done

- [ ] All steps are complete or explicitly marked blocked/skipped
- [ ] Success criteria from `requirements.md` are covered
- [ ] Contracts from `Spec.md` are implemented or deviations are recorded
- [ ] Deep review verdict is `pass`
- [ ] Tests for changed behavior are added or updated
- [ ] Phase checks pass
- [ ] Docs/planning are updated
