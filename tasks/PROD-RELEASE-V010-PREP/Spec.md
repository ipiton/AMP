---
id: PROD-RELEASE-V010-PREP
slug: prod-release-v010-prep
stream: Production Readiness / Delivery
type: feature
status: draft
created_at: 2026-09-30
updated_at: 2026-09-30
based_on:
  - requirements.md
  - research.md
---

# Specification: подготовка к первому релизу (версии без тега)

**Version:** 2.0 (1.0 описывала весь `PROD-RELEASE-V010`; 2026-09-30 сужена до среза по решению владельца: релиз не выпускаем)
**Status:** Draft

## Summary

Версия сводится к одной точке правды — `appVersion` чарта (`0.1.0`). Чарт берёт тег образа из неё, бинарь представляется `buildinfo.Version`, а `release.yml` отказывается публиковать тег, который не совпадает с чартом. Тегов, публикации, закрытия CHANGELOG и release notes в срезе нет — они остаются в `PROD-RELEASE-V010`.

## Requirements Coverage

| Requirement / Criterion | Covered by |
|---|---|
| `appVersion: "0.1.0"`, `image.tag: ""`, `values-dev` = `latest` | §1 |
| `render-image-tag.sh` зелёный, release-gate зелёный | §2, Test Plan |
| Guard в `release.yml`, `actionlint`, локальная проверка | §3, Test Plan |
| Go: `appVersion` → `buildinfo.Version` | §4 |
| Доки и CHANGELOG `[Unreleased]` | §5 |
| BACKLOG: пауза `PROD-RELEASE-V010`, `SERVICE-VERSION-ENV-DEAD` | §5 |

## Current State

- **Code:**
  - `helm/amp/templates/deployment.yaml:65,237`: `image.tag | default .Chart.AppVersion` для amp и reloader; `:81` env `SERVICE_VERSION` (в Go не читается);
  - `.github/workflows/release.yml`: публикация по любому `v*`, без сверки с чартом;
  - `go-app/cmd/server/main.go:21-24`: `const ( appName = "Alertmanager++"; appVersion = "0.0.1" )`, использования — `main.go:55` (стартовый лог) и `legacy_dashboard.go:91,104,117,130,143` (`legacyDashboardPageData.Version`);
  - `go-app/internal/buildinfo/buildinfo.go`: `Version = "dev"`, перезаписывается ldflags (`Dockerfile:29-34`, `go-app/Makefile`).
- **Data:** not applicable.
- **Tests:** `helm/amp/tests/render-config-reloader.sh` (образец; в гейт не входит); `release-gate.sh` шаги `helm-lint`/`helm-template`/`helm-rbac`; `actionlint` в CI; тесты `cmd/server`.
- **Docs:** `helm/amp/README.md:59` (дефолт `image.tag` = `latest` — неверно), `docs/CI.md:68` («values-production pins image.tag»), `WORKFLOW.md` § Release Process, `CHANGELOG.md` / `helm/amp/CHANGELOG.md` `[Unreleased]`.

## Design Premises

| Premise | Confirmed by | Class | If wrong |
|---|---|---|---|
| Шаблон берёт `.Chart.AppVersion`, когда `image.tag` пустой, для обоих контейнеров | `deployment.yaml:65,237`; grep по `helm/amp/templates` — других потребителей `image.tag` нет | call-path-traced | Рендер даст `amp:` без тега; ловит `render-image-tag.sh` |
| Вне шаблона `image.tag`/`appVersion` читают только доки, которые правим | grep `image\.tag\|\.Values\.image\b\|appVersion` по репо (без `.git`, `tasks`, `archive`, `charts`): `ROLLBACK_RUNBOOK.md:141` (`--set image.tag=<previous-tag>` — остаётся верным), `helm/amp/README.md:59`, `docs/CI.md:68`, Go-константа | call-path-traced | Устаревший док — правим по списку |
| Go-константа `appVersion` используется только в `main.go:55` и `legacy_dashboard.go` (5 мест) | grep `appVersion` по `go-app` | call-path-traced | Компиляция упадёт на пропущенном месте — ловит `go build` |
| Ни один тест/шаблон не проверяет строку `0.0.1` | `grep -rn '0\.0\.1'` по `*.go`/`*.html`/`*.tmpl` в `go-app` — единственное вхождение `main.go:24`; `buildinfo` в `cmd/server` не импортирован (импорт добавляется) | call-path-traced | Упадёт тест — поправить ожидание |
| `buildinfo.Version` в релизном образе = тег без `v` | `release.yml` `VERSION=${{ steps.meta.outputs.version }}` → `Dockerfile` ldflags; metadata-action `{{version}}` (research F2, исходник `procSemver`) | code-read | Лог покажет другое значение; живьём проверяется на первом rc в `PROD-RELEASE-V010` |
| `github.ref_name` для тега — имя тега без `refs/tags/` | документация GitHub Actions contexts; `release.yml` уже использует его как `BRANCH` | code-read | Guard всегда падает ⇒ ложный отказ, ловится на первом rc; фикс — новый rc |

## Target Design

1. `Chart.yaml` `appVersion` поднимается до `0.1.0` (`version` уже `0.1.0`). Явные `image.tag` в `values.yaml` и `values-production.yaml` заменяются на `""`, и шаблон берёт `.Chart.AppVersion` — как уже делает reloader.
2. Helm-тест `render-image-tag.sh` фиксирует этот контракт на default, production и lite, а также для reloader и явного пина.
3. Первый шаг `release.yml` после checkout сравнивает тег (без `v` и `-…`) с `appVersion` и падает до входа в реестр.
4. Бинарь берёт версию из `buildinfo.Version`, как уже делает `/api/v2/status`.
5. Доки и CHANGELOG `[Unreleased]` описывают новое правило. BACKLOG фиксирует, что сам релиз на паузе и что в нём осталось.

## API Contracts

Not applicable — HTTP API не меняется. Меняются контракт Helm values (дефолт `image.tag`) и строка версии в логе/дашборде (Impact Analysis).

## Data Model / Migrations

Not applicable.

## Component Architecture

### §1. Чарт — версии

- `helm/amp/Chart.yaml`: `appVersion: "0.1.0"`.
- `helm/amp/values.yaml:22`: `tag: ""            # defaults to .Chart.AppVersion` — стиль `configReloader.image.tag` (`:812`).
- `helm/amp/values-production.yaml:26-28`: убрать комментарий «No 1.0.0 image exists …», `tag: ""` + тот же комментарий. `pullPolicy: Always` не трогаем.
- `helm/amp/values-dev.yaml`: без изменений (dev = плавающий `latest` + `Always`, осознанно).

### §2. Helm-тест `helm/amp/tests/render-image-tag.sh`

- Образец — `render-config-reloader.sh`: bash 3.2, `set -euo pipefail`, временный каталог + `trap`, here-string вместо `printf | grep -q` (урок PROD-RBAC-SCOPE: SIGPIPE под `pipefail`).
- `appVersion` читается из `Chart.yaml`, не хардкодится.
- Кейсы:
  1. default → в выводе ровно одна строка `image: "ghcr.io/ipiton/amp:<appVersion>"`;
  2. `-f values-production.yaml --set postgresql.password=… --set cache.auth.password=…` (как `render-config-reloader.sh:115-117`) → то же;
  3. `--set profile=lite` → то же;
  4. `--set configReloader.enabled=true --set configFile.enabled=true` (+ что требует шаблон, по образцу `render-config-reloader.sh:125-128`) → `image: "ghcr.io/ipiton/amp-config-reloader:<appVersion>"`;
  5. `--set image.tag=9.9.9` → `amp:9.9.9`, `<appVersion>` в образе amp не встречается.
- Вывод — PASS/FAIL по кейсу, exit ≠ 0 при любом FAIL. В release-gate не включаем (`HELM-RENDER-TEST-IN-GATE`).

### §3. Guard в `release.yml`

Шаг сразу после `actions/checkout`, до QEMU/buildx/login:

```yaml
      # The chart pulls .Chart.AppVersion by default, so the tag must match
      # it: v0.1.0 and v0.1.0-rc.1 both require appVersion "0.1.0".
      - name: check tag matches Chart appVersion
        env:
          TAG: ${{ github.ref_name }}
        run: |
          app="$(sed -n 's/^appVersion: *"\{0,1\}\([^"]*\)"\{0,1\} *$/\1/p' helm/amp/Chart.yaml)"
          want="${TAG#v}"
          want="${want%%-*}"
          if [ -z "$app" ] || [ "$want" != "$app" ]; then
            echo "::error::tag $TAG does not match helm/amp/Chart.yaml appVersion '$app'"
            exit 1
          fi
```

- Тег — через `env`, а не `${{ }}` внутри `run`.
- Выполняется в обоих job'ах матрицы. Это дешевле и проще, чем отдельный job с `needs`.
- `Chart.yaml` `version` не проверяется: соглашение «`version` = `appVersion`» записывается в `WORKFLOW.md`, а чарт не публикуется.
- Комментарий в шапке `release.yml` («A tag is the one way to publish») дополнить одной строкой: тег должен совпадать с `appVersion`.

### §4. Версия в бинаре

- `go-app/cmd/server/main.go`: `appVersion` убрать из `const`-блока, `appName` остаётся. `main.go:55` → `"version", buildinfo.Version`; добавить импорт `github.com/ipiton/AMP/internal/buildinfo` (в пакете `main` его сейчас нет).
- `go-app/cmd/server/legacy_dashboard.go:91,104,117,130,143` → `Version: buildinfo.Version`.
- Вне Docker/Makefile-сборки значение будет `dev`. Это верно: такая сборка и не является релизной.

### §5. Доки и planning

- `helm/amp/README.md:59`: `| image.tag | Image tag | "" (defaults to .Chart.AppVersion) |`.
- `docs/CI.md` § Releasing Images: `:68` переписать — тег образа по умолчанию берётся из `appVersion`, `release.yml` отказывается публиковать тег, не совпадающий с ним, так что перед тегом `appVersion` поднимается в релизном коммите. Шаги `:55-66` не трогаем (rc-процедура — `PROD-RELEASE-V010`).
- `WORKFLOW.md` § Release Process: новый шаг 0 — «поднять `helm/amp/Chart.yaml` `version` и `appVersion` до версии релиза; `release.yml` сверяет тег с `appVersion` и без этого ничего не публикует».
- `CHANGELOG.md` `[Unreleased]`:
  - `### Changed`: дефолт `image.tag` → `.Chart.AppVersion` (было `0.0.1` / production `1.0.0`, образов с такими тегами нет; явный пин по-прежнему выигрывает);
  - `### Changed`: стартовый лог и legacy-дашборд показывают версию сборки (`buildinfo.Version`, как `/api/v2/status`), а не константу `0.0.1`;
  - `### Added`: guard `release.yml`.
- `helm/amp/CHANGELOG.md` `[Unreleased]` / `### Changed`: `appVersion` `0.1.0`, `image.tag` по умолчанию пустой.
- `docs/06-planning/BACKLOG.md`:
  - `PROD-RELEASE-V010` — пометка: «_(2026-09-30: на паузе по решению владельца. Подготовка — `PROD-RELEASE-V010-PREP`: версии, guard, версия в бинаре. Осталось: закрыть `CHANGELOG` в `[0.1.0]`, release notes из актуального `[Unreleased]` (draft устарел, `Previous version: v0.0.2`), rc-тег → проверка → Public → финальный тег; план — `tasks/archive/PROD-RELEASE-V010-PREP/Spec.md` v1.0 в git-истории и `research.md`.)_»;
  - новая строка `SERVICE-VERSION-ENV-DEAD` (~0.1d) в секции находок.

## Security Design

- [x] Ownership validation — not applicable.
- [x] Input validation: имя тега попадает в shell только через `env` (`TAG`).
- [x] Sensitive data не логируется: guard печатает тег и `appVersion` (публичные), лог — версию сборки.
- [x] Rate limiting — not applicable.
- [x] Auth/RBAC: `permissions` workflow не меняются, новых actions нет.

## Invariants

- [ ] I1. Default, production и lite рендер ссылаются на `ghcr.io/ipiton/amp:<appVersion>`, reloader — на `…/amp-config-reloader:<appVersion>`. Явный `image.tag` выигрывает.
- [ ] I2. `release.yml` ничего не публикует, если `${TAG#v}` без `-*` ≠ `appVersion`, включая пустой `appVersion`. Guard стоит до `docker/login-action`.
- [ ] I3. HTTP API и конфиг приложения не меняются. `/api/v2/status` `versionInfo` — без изменений (уже `buildinfo`).
- [ ] I4. Никаких тегов и push в реестр в рамках среза.

## Edge Cases

1. Тег `v0.1.1` при `appVersion: "0.1.0"` → guard падает в обоих job'ах, login не выполняется.
2. `v0.1.0+build.1` → `%%-*` не срезает `+` → fail. Build metadata не используем — приемлемо.
3. `appVersion: 0.1.0` без кавычек → sed понимает оба варианта. Пустая строка или отсутствие ключа → fail (`-z`).
4. `--set image.tag=""` явно → то же, что дефолт (`default` считает пустую строку отсутствием).
5. `go run ./cmd/server` → лог `"version":"dev"`. Это ожидаемо.

## Impact Analysis

- **Affected modules:** `helm/amp/` (Chart, 2 values, README, CHANGELOG, новый тест), `.github/workflows/release.yml`, `go-app/cmd/server/{main,legacy_dashboard}.go`, `CHANGELOG.md`, `docs/CI.md`, `WORKFLOW.md`, BACKLOG.
- **Breaking changes:** дефолт `image.tag` `0.0.1`/`1.0.0` → `0.1.0` (через `appVersion`). Ни один из этих образов не существует ни до, ни после — пока не выпущен `v0.1.0`, дефолтный install по-прежнему `ImagePullBackOff`. Кто пинит тег явно, не затронут. Строка версии в логе: `0.0.1` → версия сборки. Если кто-то парсит стартовый лог по `version`, он увидит реальное значение.
- **New dependencies:** none.
- **Risks:** ложный отказ guard'а обнаружится только на первом реальном теге (`PROD-RELEASE-V010`). Митигация — локальный прогон скрипта шага на pass/fail-кейсах, `actionlint`; при сбое — фикс и новый rc.

## Rollout / Rollback

- **Rollout:** обычный merge в `main`, зелёный `ci`. Тегов нет (I4).
- **Rollback:** `git revert` мерж-коммита. Данных и публикаций нет.
- **Feature flag:** not applicable.

## Observability

- **Logs:** стартовый `Starting Alertmanager++` — `version` = `buildinfo.Version`; `::error::` guard'а в логе `release.yml`.
- **Metrics:** not applicable (`amp_build_info` уже из `buildinfo`).
- **Alerts:** not applicable.

## Deep Review

- **Mandatory triggers present:** none. `S`/`M` отпали вместе с тегом (requirements § Risk Profile), 2 сигнала, pre-release не применим: тега нет.
- **Discretionary triggers present:** author doubt (незначительный) — guard проверяется реальным запуском только в будущем релизе.
- **Decision:** recommended, skipped (reason: дифф маленький — ~40 строк логики + тест + доки; guard покрывается локальным прогоном pass/fail-кейсов и `actionlint`; обязательный pre-release `deep-review` всё равно пройдёт по этому коду перед тегом в `PROD-RELEASE-V010`).

## Open Questions

- [x] **OQ1 (владелец), решено 2026-09-30:** релиз пока не выпускаем.
- [x] **OQ4 (владелец), решено 2026-09-30:** вариант 1 — подготовительный срез (этот workspace), остаток — в `PROD-RELEASE-V010` на паузе.
- [x] **OQ3:** Go-правка источника версии — входит в срез (§4); выбор владельца «1» включал её в перечень.
- none open.
