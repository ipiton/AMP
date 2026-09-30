---
id: PROD-RELEASE-V010-PREP
slug: prod-release-v010-prep
stream: Production Readiness / Delivery
type: feature
status: active
created_at: 2026-09-30
updated_at: 2026-09-30
based_on:
  - requirements.md
  - research.md
  - Spec.md
---

# Implementation Plan: подготовка к первому релизу (версии без тега)

**Based on:** requirements.md / research.md / Spec.md (v2.0)  
**Date:** 2026-09-30  
**Tier:** Standard (`C R`), deep-review — recommended, skipped (Spec § Deep Review)

## Touched Files

- `helm/amp/Chart.yaml` — `appVersion: "0.1.0"`
- `helm/amp/values.yaml` — `image.tag: ""`
- `helm/amp/values-production.yaml` — `image.tag: ""`, убрать устаревший комментарий
- `.github/workflows/release.yml` — guard-шаг + строка в шапке
- `go-app/cmd/server/main.go` — убрать константу `appVersion`, лог → `buildinfo.Version`
- `go-app/cmd/server/legacy_dashboard.go` — 5 × `Version: buildinfo.Version`
- `helm/amp/README.md` — дефолт `image.tag`
- `docs/CI.md` — § Releasing Images, `:68`
- `WORKFLOW.md` — § Release Process, шаг 0
- `CHANGELOG.md`, `helm/amp/CHANGELOG.md` — `[Unreleased]`
- `docs/06-planning/BACKLOG.md` — пауза `PROD-RELEASE-V010`, `SERVICE-VERSION-ENV-DEAD`
- `helm/amp/tests/render-image-tag.sh` — новый helm-тест (Phase 3)

## Phase 1: Код и чарт

> **Wave 1** — независимые шаги

- [x] **1.1** `Chart.yaml` `appVersion: "0.1.0"`; `values.yaml:22` → `tag: ""            # defaults to .Chart.AppVersion`; `values-production.yaml:26-28` → убрать комментарий «No 1.0.0 image exists…», `tag: ""` + тот же комментарий. `values-dev.yaml` не трогать. <!-- verify: for f in "" "-f helm/amp/values-production.yaml --set postgresql.password=x --set cache.auth.password=x" "--set profile=lite"; do helm template amp helm/amp $f | grep -E '^\s+image: "ghcr.io/ipiton/amp'; done  # три строки amp:0.1.0 -->
- [x] **1.2** `release.yml`: шаг `check tag matches Chart appVersion` сразу после `actions/checkout`, код — Spec §3 дословно; в шапочный комментарий добавить строку «the tag must match helm/amp/Chart.yaml appVersion». <!-- verify: go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12  # из корня репо, как ci.yml:92 -->
- [x] **1.3** Go: удалить `appVersion` из `const`-блока `cmd/server/main.go`, `main.go:55` → `buildinfo.Version`, импорт `github.com/ipiton/AMP/internal/buildinfo`; `legacy_dashboard.go:91,104,117,130,143` → `buildinfo.Version`. <!-- verify: cd go-app && ! grep -rn 'appVersion' --include='*.go' . && go vet ./cmd/server/... && go build ./cmd/server -->

> **Wave 2** — локальная проверка guard'а (без файлов в репозитории)

- [x] **1.4** Скопировать тело `run:` шага из `release.yml` в скрипт в scratchpad и прогнать: `TAG=v0.1.0`, `v0.1.0-rc.1` → exit 0; `v0.1.1`, `v1.0.0`, `v0.1.0+build.1` → exit 1 с `::error::`; копия `Chart.yaml` без кавычек у `appVersion` → pass на `v0.1.0`; без ключа `appVersion` → fail. Вывод — в `evidence/guard-local-2026-09-30.txt`. <!-- depends: 1.1, 1.2 | verify: cat tasks/PROD-RELEASE-V010-PREP/evidence/guard-local-2026-09-30.txt  # 3 pass / 4 fail как ожидалось -->

**Phase verification:** `cd go-app && go vet ./cmd/server/... && go test ./cmd/server/... -count=1`; `helm lint helm/amp`; `actionlint` по `.github/workflows/`.

## Phase 2: Доки и planning

- [x] **2.1** `helm/amp/README.md:59` → `| \`image.tag\` | Image tag | \`""\` (defaults to \`.Chart.AppVersion\`) |`. <!-- verify: grep -n 'image.tag' helm/amp/README.md -->
- [x] **2.2** `docs/CI.md:68` — переписать: тег образа по умолчанию = `appVersion`; `release.yml` не публикует тег, не совпадающий с ним ⇒ bump `appVersion` в релизном коммите. Шаги `:55-66` не трогать. <!-- verify: grep -n 'appVersion' docs/CI.md -->
- [x] **2.3** `WORKFLOW.md` § Release Process — шаг 0 перед «Collect»: поднять `helm/amp/Chart.yaml` `version` и `appVersion` до версии релиза; `release.yml` сверяет тег с `appVersion`. <!-- verify: grep -n 'appVersion' WORKFLOW.md -->
- [x] **2.4** `CHANGELOG.md` `[Unreleased]`: в `### Changed` — дефолт `image.tag` → `.Chart.AppVersion` (было `0.0.1` / production `1.0.0`, таких образов нет, явный пин выигрывает) и версия сборки в стартовом логе и legacy-дашборде; в `### Added` — guard `release.yml`. Формат — как у соседних записей (`**PROD-RELEASE-V010-PREP** (2026-09-30): …`). `helm/amp/CHANGELOG.md` `[Unreleased]` / `### Changed` — `appVersion` `0.1.0`, `image.tag` пустой по умолчанию. <!-- verify: grep -n 'PROD-RELEASE-V010-PREP' CHANGELOG.md helm/amp/CHANGELOG.md -->
- [x] **2.5** `BACKLOG.md`: у `PROD-RELEASE-V010` — пометка о паузе и остатке (текст — Spec §5); новая строка `SERVICE-VERSION-ENV-DEAD` (env `SERVICE_VERSION`, `deployment.yaml:81`, Go не читает; удалить или начать читать; ~0.1d) в «Находки PROD-CI-IMAGES» или новой секции «Находки PROD-RELEASE-V010-PREP». <!-- verify: grep -n 'PROD-RELEASE-V010-PREP\|SERVICE-VERSION-ENV-DEAD' docs/06-planning/BACKLOG.md -->

**Phase verification:** `git diff --check`; `grep -rn '1\.0\.0' helm/amp/values-production.yaml` пуст по `image`.

## Phase 3: Тесты (write-tests)

- [ ] **3.1** `helm/amp/tests/render-image-tag.sh` по Spec §2: bash 3.2, `set -euo pipefail`, `trap` на временный каталог, here-string вместо `printf | grep -q`, `appVersion` из `Chart.yaml`; кейсы: default, production, lite → `amp:<appVersion>`; reloader enabled → `amp-config-reloader:<appVersion>`; `--set image.tag=9.9.9` → `amp:9.9.9`. PASS/FAIL по кейсу, exit ≠ 0 при любом FAIL. `chmod +x`. <!-- depends: 1.1 | verify: helm/amp/tests/render-image-tag.sh -->
- [ ] **3.2** Мутационная проверка теста: временно вернуть `values.yaml` `tag: "0.0.1"` → тест FAIL; вернуть `appVersion: "0.0.1"` → FAIL; откатить правки. <!-- depends: 3.1 | verify: git status --short helm/amp  # только ожидаемые файлы -->
- [ ] **3.3** Go: новых тестов не требуется (источник строки, поведение не меняется); подтвердить, что существующие тесты `cmd/server` зелёные. <!-- depends: 1.3 | verify: cd go-app && go test ./cmd/server/... -count=1 -->

**Phase verification:** `helm/amp/tests/render-image-tag.sh && helm/amp/tests/render-config-reloader.sh`.

## Phase 4: Testing и finalize

- [ ] **4.1** Гейты AMP: `make -C go-app quality-gates-fast` (+ `git status` — `go fmt` не должен ничего переписать), `scripts/release-gate.sh` (флейк `GROUPING-TIMER-LOCK-RELEASED-BEFORE-LOSER` на `race` — один перезапуск с пометкой; дважды красный — стоп), `git diff --check`, нет `_, _ :=` в диффе. <!-- depends: Phase 1-3 | verify: scripts/release-gate.sh  # RESULT: PASS -->
- [ ] **4.2** Локальная сборка образа с `--build-arg VERSION=0.1.0-local` и проверка стартового лога `"version":"0.1.0-local"` (опционально, если Docker доступен; иначе — пометка). <!-- depends: 1.3 | verify: docker run --rm <img> 2>&1 | head -5 | grep '"version":"0.1.0-local"' -->
- [ ] **4.3** `finalize`: `DONE.md` (запись 2026-09), удалить WIP-строку из `NEXT.md`, архив `tasks/PROD-RELEASE-V010-PREP/` → `tasks/archive/`; `PROD-RELEASE-V010` остаётся открытым в BACKLOG. <!-- depends: 4.1 | verify: test -d tasks/archive/PROD-RELEASE-V010-PREP && ! grep -n 'PROD-RELEASE-V010-PREP' docs/06-planning/NEXT.md -->
- [ ] **4.4** `merge-to-main`, зелёный `ci` на merge-коммите. Тегов не ставить (Spec I4). <!-- depends: 4.3 | verify: gh run list -L 1 --branch main -->

## Implementation Notes (2026-09-30)

- 1.1: `verify` из плана под zsh не разбивает `$f` на слова — прогнано через `bash -c`; три строки `ghcr.io/ipiton/amp:0.1.0`.
- 1.3: константа `appName` осталась одна в `const` и по-прежнему нигде не используется (так было и до правки) — не трогали, вне scope.
- 1.4: тело шага извлекается из `release.yml` через YAML-парсер, не копией руками; 8/8 кейсов как ожидалось (`evidence/guard-local-2026-09-30.txt`). Шелл CI — `bash -e`, локально `bash`; для этого скрипта разница не влияет (нет команд, чей ненулевой код до `exit 1` что-то меняет).
- 2.4: старая запись PROD-CI-IMAGES в `CHANGELOG.md` («`values-production.yaml` pins `1.0.0`») не переписывалась — это история того изменения; новая запись PREP её отменяет.
- Отклонений от Spec нет.

## Definition of Done

- [ ] All steps are complete or explicitly marked blocked/skipped
- [ ] Success criteria from `requirements.md` are covered
- [ ] Contracts from `Spec.md` are implemented or deviations are recorded
- [ ] Deep review verdict is `pass`, or deep review was not required — recommended, skipped (Spec § Deep Review)
- [ ] Tests for changed behavior are added or updated (`render-image-tag.sh`)
- [ ] Phase checks pass
- [ ] Docs/planning are updated if behavior, contracts, or process changed
- [ ] Ни одного git-тега и push в реестр (Spec I4)
