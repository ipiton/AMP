---
id: PROD-RELEASE-V010-PREP
slug: prod-release-v010-prep
stream: Production Readiness / Delivery
type: feature
priority: high
status: complete
created_at: 2026-09-30
updated_at: 2026-09-30
---

# Requirements: подготовка к первому релизу (версии без тега)

> Срез `PROD-RELEASE-V010`. 2026-09-30 владелец решил релиз пока не выпускать (Spec OQ1) и взять подготовительную часть отдельно (OQ4, вариант 1). CHANGELOG `[0.1.0]`, release notes, rc/финальный тег и флип GHCR в Public остаются в `PROD-RELEASE-V010` (BACKLOG, на паузе). Research делался под родительскую задачу — `research.md` в этом workspace, выводы в силе.

## Problem Framing

- **Symptom:** версия AMP задаётся в четырёх местах, и все они расходятся. Чарт по умолчанию тянет образы, которых никогда не будет: `ghcr.io/ipiton/amp:0.0.1` (default) и `:1.0.0` (production). Бинарь представляется как `0.0.1` в стартовом логе и legacy-дашборде при любой сборке. Когда релиз всё-таки будут резать, рассинхрон тега и чарта ничем не ловится.
- **Root Cause:**
  - `helm/amp/Chart.yaml`: `version: 0.1.0`, `appVersion: "0.0.1"`;
  - `helm/amp/values.yaml:22`: `image.tag: "0.0.1"` перекрывает `default .Chart.AppVersion` (`deployment.yaml:65`);
  - `helm/amp/values-production.yaml:28`: `image.tag: "1.0.0"`;
  - `go-app/cmd/server/main.go:24`: константа `appVersion = "0.0.1"`, хотя ldflags уже пишут настоящую версию в `internal/buildinfo.Version`;
  - `release.yml` публикует любой `v*` тег, не сверяясь с чартом.
- **Why Now:** это дешёвая часть `PROD-RELEASE-V010`, полезная при любом сроке релиза: после неё релиз сводится к CHANGELOG, notes и тегу. Заодно убирается класс ошибки, из-за которого появился блокер.
- **How We Measure:**
  - `helm/amp/tests/render-image-tag.sh` зелёный: default/production/lite → `amp:<appVersion>`, reloader → `amp-config-reloader:<appVersion>`, явный `image.tag` выигрывает;
  - guard-скрипт шага `release.yml`, прогнанный локально: `v0.1.0`, `v0.1.0-rc.1` → pass; `v0.1.1`, `v1.0.0`, пустой `appVersion` → fail;
  - `grep -rn appVersion go-app` пуст;
  - release-gate зелёный.

## Risk Profile

- **Signals:** `C R`
  - `C` — Helm values: дефолт `image.tag` → `.Chart.AppVersion`. Явный пин не затронут. Ни `0.0.1`, ни `1.0.0` в GHCR не существовали, так что рабочих установок, которые бы сломались, нет.
  - `R` — наблюдаемая версия бинаря: стартовый лог и legacy-дашборд показывают `buildinfo.Version` (`dev` вне Docker, тег — в релизном образе) вместо `0.0.1`.
- **Tier:** Standard
- **Notes:** `S` рассмотрен и отклонён: шаг в `release.yml` только добавляет проверку перед login, права (`packages: write`), actions и пины не меняются, публикации в этом срезе нет. `M` отпал вместе с тегом. Pre-release `deep-review` не применим — тега нет; он остаётся обязательным в `PROD-RELEASE-V010`.

## User Stories

1. Как мейнтейнер, я хочу одну точку правды для версии (`Chart.yaml` `appVersion`), чтобы релиз не требовал сверки нескольких файлов.
2. Как мейнтейнер, я хочу, чтобы `release.yml` отказался публиковать тег, не совпадающий с чартом, — до входа в реестр.
3. Как оператор, я хочу, чтобы бинарь называл свою настоящую версию в логе и дашборде, как уже делает `/api/v2/status`.

## Success Criteria

- [x] `Chart.yaml` `appVersion: "0.1.0"`; `image.tag: ""` в `values.yaml` и `values-production.yaml`; `values-dev.yaml` остаётся `latest` (осознанно).
- [x] `helm/amp/tests/render-image-tag.sh` зелёный; `scripts/release-gate.sh` зелёный (известный флейк `GROUPING-TIMER-LOCK-RELEASED-BEFORE-LOSER` — перезапуск с пометкой).
- [x] В `release.yml` есть guard: тег без `v` и pre-release суффикса == `appVersion`, иначе fail до login; `actionlint` чист; guard проверен локально на pass- и fail-кейсах.
- [x] Go: константы `appVersion` нет, 6 использований → `buildinfo.Version`; `go vet` + `go test ./cmd/server/...` зелёные.
- [x] Доки: `helm/amp/README.md:59` (дефолт `image.tag`), `docs/CI.md:68` (тег из `appVersion` + guard), `WORKFLOW.md` § Release Process (шаг: bump `version`/`appVersion` чарта; guard), записи в `CHANGELOG.md` `[Unreleased]` и `helm/amp/CHANGELOG.md` `[Unreleased]`.
- [x] BACKLOG: `PROD-RELEASE-V010` — пометка «на паузе по решению владельца 2026-09-30, подготовка сделана в `-PREP`, остаток: …»; новая строка `SERVICE-VERSION-ENV-DEAD`.

## Non-Goals

- Тег (`v0.1.0-rc.1`, `v0.1.0`), публикация образов, флип пакетов GHCR в Public — `PROD-RELEASE-V010`, на паузе по решению владельца.
- Закрытие `CHANGELOG` в `[0.1.0]`, release notes (`docs/RELEASE_NOTES_v0.1.0-draft.md` не трогаем), rc-процедура в `WORKFLOW.md` / `docs/CI.md` — там же.
- Включение `configReloader.enabled` — `CONFIG-RELOADER-SIDECAR` / `CONFIG-RELOADER-AUTH`.
- Шаг `helm-tests` в release-gate — `HELM-RENDER-TEST-IN-GATE`.
- Env `SERVICE_VERSION` (`deployment.yaml:81`, Go не читает) — только строка в BACKLOG.
- Branch protection — owner action `MAIN-BRANCH-PROTECTION`.

## Constraints

- **Scope:**
  - `helm/amp/{Chart.yaml,values.yaml,values-production.yaml,README.md,CHANGELOG.md}`, `helm/amp/tests/render-image-tag.sh` (новый);
  - `.github/workflows/release.yml` (один шаг);
  - `go-app/cmd/server/{main.go,legacy_dashboard.go}`;
  - `CHANGELOG.md`, `docs/CI.md`, `WORKFLOW.md`, `docs/06-planning/BACKLOG.md`.
- **Security:** права workflow не меняются; имя тега в shell — только через `env`.
- **Compatibility:**
  - `appVersion: 0.1.0` до релиза означает, что дефолтный рендер ссылается на `amp:0.1.0`, которого ещё нет. Это не регрессия: `0.0.1`/`1.0.0` не существуют тоже. Состояние «чарт указывает на следующую версию» — нормальное между релизами.
  - Guard с этого момента пропускает только теги `v0.1.0[-*]`.

## Discovery Notes

- Similar tasks: `tasks/archive/PROD-CI-IMAGES/` (release.yml, ADR-013), `helm/amp/tests/render-config-reloader.sh` (образец helm-теста).
- Relevant patterns: `deployment.yaml:65,237` (`default .Chart.AppVersion`), `values.yaml:812` (`tag: ""  # defaults to .Chart.AppVersion`), `Dockerfile:19-34` (ldflags → buildinfo).
- Open unknowns: none (metadata-action и pre-release сняты в spec; первый реальный прогон `release.yml` — в родительской задаче).
