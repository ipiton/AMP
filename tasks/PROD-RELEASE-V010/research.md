# Research: PROD-RELEASE-V010

**Level:** 2 (light). Триггеры: infrastructure (первый запуск `release.yml`), несколько вариантов (rc-тег, guard версии), необратимая операция (тег + публичные образы).
**Mode:** обычный; живые наблюдения — `evidence/github-state-2026-09-30.txt`.

## TL;DR

- Версия задаётся в трёх местах, и все три расходятся: `Chart.yaml` `appVersion: "0.0.1"`, `values.yaml` `image.tag: "0.0.1"`, `values-production.yaml` `image.tag: "1.0.0"`. В шаблонах `default .Chart.AppVersion` уже есть (`deployment.yaml:65,237`), так что достаточно поднять `appVersion` до `0.1.0` и **очистить** явные теги.
- `release.yml` ни разу не запускался, образов в GHCR нет (анонимный pull → `denied`). Сначала — пробный тег **`v0.1.0-rc.1`**: реальный прогон публикации без `latest`/`0.1`, проверка флипа в Public и `docker pull` на обеих архитектурах. Потом финальный `v0.1.0` с того же коммита.
- Guard в `release.yml`: базовая версия тега (без `-rc.N`) должна совпадать с `Chart.yaml` `appVersion`, иначе публикация не начинается. Около 5 строк shell, дешёвый и прямо закрывает класс ошибки, из-за которого эта задача вообще появилась.
- `CHANGELOG`: `[Unreleased]` → `## [0.1.0]`. Тег `v0.0.2` в CHANGELOG не описан — отдельную секцию не придумываем, compare-ссылки строим от `v0.0.2`.
- Черновик release notes (2026-08-20) устарел: нет ни одного P0-закрытия (AUTH, RBAC, CI, DEPS, RESOLVE-TIMEOUT, KARMA), «Previous version: none» — неверно. Пересобрать по `WORKFLOW.md` § Release Process из текущего `[Unreleased]`, а не латать.

## Questions

1. Что нужно изменить, чтобы дефолтный и production-рендер ссылались на опубликованный образ?
2. Как поведёт себя `release.yml` на первом теге и чем можно проверить его до необратимого `v0.1.0`?
3. Нужен ли guard «тег == версия чарта», и где его ставить?
4. Доходит ли версия до бинаря (`/api/v2/status`)?
5. Состояние CI на GitHub — можно ли на него опереться перед тегом?
6. Что делать с `CHANGELOG` (включая `v0.0.2` и нестандартную секцию `### PROD-INFRA`) и черновиком release notes?

## Findings

### F1. Версии в чарте

- `helm/amp/Chart.yaml:8-9`: `version: 0.1.0`, `appVersion: "0.0.1"`.
- `helm/amp/values.yaml:21-23`: `ghcr.io/ipiton/amp:0.0.1`, `IfNotPresent`.
- `helm/amp/values-production.yaml:25-29`: `tag: "1.0.0"`, `Always`, комментарий «No 1.0.0 image exists … aligned in PROD-RELEASE-V010».
- `helm/amp/values-dev.yaml:15-17`: `tag: "latest"`, `Always` — осознанная dev-пара (плавающий тег + всегда тянуть). Появится после первого не-pre-release тега.
- `configReloader.image.tag: ""` в `values.yaml:812` и `values-production.yaml:453` → уже `.Chart.AppVersion`.
- `deployment.yaml:65` / `:237`: `{{ .Values.image.tag | default .Chart.AppVersion }}` — механизм есть, его перекрывают явные значения.
- `deployment.yaml:81`: env `SERVICE_VERSION = .Chart.AppVersion`, но Go-код его **не читает** (grep по `go-app` пуст). Мёртвый env, в этой задаче не трогаем — достаточно заметки в Known Gaps или BACKLOG.
- Helm-чарт нигде не публикуется: все доки ставят его из `./helm/amp`. `Chart.version` = `appVersion` = `0.1.0`, дальше их можно двигать вместе.

### F2. `release.yml` (`.github/workflows/release.yml`)

- Триггер: только push тега `v*`, `workflow_dispatch` нет сознательно (ADR-013). Матрица `amp` / `config-reloader`, `fail-fast: false`.
- `docker/metadata-action` v6.2.0: `type=semver,pattern={{version}}`, `{{major}}.{{minor}}`, `type=sha,format=short`, `flavor: latest=auto`. У metadata-action `{{major}}.{{minor}}` и `latest=auto` для pre-release semver **не генерируются** ⇒ тег `v0.1.0-rc.1` даст только `0.1.0-rc.1` и `sha-<short>`. Это поведение action, в репозитории не проверено; в спеке закрепить проверкой вывода `steps.meta` в логе rc-прогона.
- `VERSION=${{ steps.meta.outputs.version }}` → `-X github.com/ipiton/AMP/internal/buildinfo.Version` (`Dockerfile:21-34`, модуль `github.com/ipiton/AMP`, пакет существует) ⇒ `/api/v2/status` `versionInfo.version` = `0.1.0` (или `0.1.0-rc.1`). Ответ на Q4 — да, статически. Живьём проверить на rc-образе.
- `karma`/`alertmanager_build_info{version}` от этого не зависят: там константа (ADR-009, CHANGELOG KARMA-COMPAT). Версия `0.1.0` на karma не влияет.
- Риски первого прогона: права `packages: write` с `GITHUB_TOKEN` в публичном репозитории пользователя; первая публикация создаёт **private** пакеты (`docs/CI.md`), анонимный pull падает до ручного флипа; `fail-fast: false` ⇒ возможна «половина релиза» (`amp` опубликован, reloader нет).
- `release.yml` **не прогоняет** гейт — полагается на CI коммита. Тегировать можно только коммит `main` с зелёным `ci`.

### F3. Состояние GitHub (measured, `evidence/github-state-2026-09-30.txt`)

- Последний `ci` на `main` (`16a75f3`, merge solo-kanban-upgrade) — все 6 job'ов success.
- Предыдущий красный прогон (`36556853447`, merge PROD-DEPS-VULN) — `gate` → `race` FAIL на `internal/infrastructure/grouping` (`TestTimerContinuation_GroupWaitFireCreatesGroupIntervalTimer`) = известный `GROUPING-TIMER-LOCK-RELEASED-BEFORE-LOSER`. Флейк может покраснить и коммит, который пойдёт в релиз; перезапуск job'а допустим, но фиксируется в итоге.
- Remote-теги: `v0.0.2`, `v0.0.1`. Защиты `main` нет (404) — owner action `MAIN-BRANCH-PROTECTION`, для релиза не блокер.
- `docker manifest inspect ghcr.io/ipiton/amp:latest` без логина → `denied` (образов нет).
- У `gh` нет scope `read:packages` — видимость пакетов через API агент не проверит; флип в Public и проверка — вручную у владельца или анонимным `docker pull`.

### F4. `CHANGELOG.md` и история тегов

- `v0.0.1` = `617fc59` (2025-12-04), `v0.0.2` = `ce4ce72` (2025-12-09, merge PR #10 `refactor/code-quality-improvements`); от `v0.0.2` до `main` — 597 коммитов.
- В `CHANGELOG.md` есть `## [0.0.1] - 2024-12-04`, **секции `[0.0.2]` нет**; «Code Quality Refactoring (2024-12-05)» лежит в `[Unreleased]` / `### Improved`, хотя вошёл в `v0.0.2`. Даты `2024-12` в CHANGELOG расходятся с датами тегов (`2025-12`) — исторический дрейф, не чиним.
- Ссылка внизу: `[Unreleased]: …/compare/v0.0.1...HEAD`.
- Секции `[Unreleased]`: `Security`, `Fixed`, `Added`, `Breaking changes / migration notes`, `Changed`, `Improved`, **`PROD-INFRA`** (не Keep a Changelog: это содержимое `Added` — release-gate, smoke e2e, runbook, rollout plan).
- `helm/amp/CHANGELOG.md` — свой `[Unreleased]`, тоже закрывается в `[0.1.0]`.

### F5. Release notes

- `docs/RELEASE_NOTES_v0.1.0-draft.md` (332 строки) последний раз правился 2026-08-20 (`404b913`); ссылок на PROD-AUTH / PROD-RBAC-SCOPE / PROD-CI-IMAGES / PROD-DEPS-VULN / PARITY-RESOLVE-TIMEOUT / KARMA-COMPAT / GO-2026-5932 — 0 (в CHANGELOG они есть).
- «Previous version: none — first tagged release» — неверно (есть `v0.0.1`, `v0.0.2`).
- Структура совпадает с `docs/RELEASE_NOTES_TEMPLATE.md` — брать как основу, но сверять с текущим `[Unreleased]` целиком (правило «Collect» процесса).
- Процесс требует после финализации переименовать `*-draft.md` → `RELEASE_NOTES_v0.1.0.md`. Шаблон ссылается на draft как на «filled-in example» (`RELEASE_NOTES_TEMPLATE.md:7`) — ссылку обновить.

### F6. Доки с версиями

- `README.md:28`, `docs/MIGRATION_QUICK_START.md:31`: `ghcr.io/ipiton/amp:latest` — заработает после `v0.1.0` (`latest=auto`). Оставить.
- `docs/CONFIGURATION_GUIDE.md:880,904`: `image: amp:latest` без реестра — пример docker-compose/k8s, не ломается версией. Вне scope.
- Установка везде из `./helm/amp` ⇒ смена дефолтного тега прозрачна для доков.

## Options

**Generation:** single-author (level 2; варианты немногочисленны и проверяемы, fan-out по линзам не делали).

### Q-A. Выравнивание тега образа

| Вариант | Суть | За | Против |
|---|---|---|---|
| **A1** | `appVersion: "0.1.0"`, `image.tag: ""` в `values.yaml` и `values-production.yaml` (`values-dev` — `latest` как есть) | Одна точка правды; шаблон уже умеет; reloader уже так работает | Смена дефолта для тех, кто ставил без override (им и так ставился несуществующий образ) |
| A2 | Прописать `"0.1.0"` явно во всех values | Видно в values | Три места снова разъедутся к `0.1.1` — ровно исходная проблема |

→ **A1.**

### Q-B. Как проверить `release.yml` до необратимого тега

| Вариант | Суть | За | Против |
|---|---|---|---|
| **B1** | `v0.1.0-rc.1` → проверка → `v0.1.0` с того же коммита | Реальный прогон с правами и реестром; без `latest`/`0.1`; флип Public и `docker pull` проверяются заранее; при сбое следующий — `rc.2`, без переписывания тегов | Лишняя версия пакетов в GHCR; два push'а тега |
| B2 | Сразу `v0.1.0` | Быстрее | Первый в истории прогон на финальном теге; при частичном сбое — «битый» `v0.1.0` в публичном репо, исправление только `v0.1.1` или перенос тега |
| B3 | Временный `workflow_dispatch` / публикация из ветки | Без тега | Противоречит ADR-013 (публикация только тегом) и расширяет поверхность `packages: write` |

→ **B1.** Оба push'а тега — только с явного подтверждения владельца.

### Q-C. Guard рассинхрона версий

| Вариант | Суть | За | Против |
|---|---|---|---|
| **C1** | Шаг в `release.yml` перед login: `${GITHUB_REF_NAME#v}` без pre-release суффикса == `appVersion` из `Chart.yaml`, иначе `exit 1` | Ловит ошибку до публикации, в единственной точке, где она стоит денег; ~5 строк | Проверяется только на теге (в PR — нет); actionlint + rc-прогон покрывают |
| C2 | Шаг в `release-gate.sh`: values не пинят `image.tag` | Ловится в каждом PR | Не ловит «тег не совпал с чартом»; жёстко запрещает легитимный пин в values |
| C3 | Только чек-лист в `WORKFLOW.md` | Ноль кода | Ровно тот класс ошибки, что уже случился |

→ **C1.** Плюс строка в `WORKFLOW.md` § Release Process (bump `appVersion` + `version` чарта → CHANGELOG → тег). C2 не делаем.

### Q-D. CHANGELOG

- **D1 (выбрано):** `## [Unreleased]` (пустой) + `## [0.1.0] - <дата тега>` со всем текущим содержимым; `### PROD-INFRA` переименовать в `### Added (release tooling)` или влить в `### Added` — решить в spec (минимальный дифф — переименование заголовка). Ссылки: `[Unreleased]: …/compare/v0.1.0...HEAD`, `[0.1.0]: …/compare/v0.0.2...v0.1.0`. Секцию `[0.0.2]` задним числом не писать: одна строка-заметка в `[0.1.0]`, что `v0.0.2` отдельно не документировался и его изменения (Code Quality Refactoring) включены сюда.
- Дата: ставится в день rc-проверки, коммит релиза = коммит тега. Если rc растянется — дату обновить отдельным коммитом **до** финального тега (тогда финальный тег ставится на этот коммит, и `ci` должен быть зелёным на нём).

## Decision

1. A1 + C1 + B1 + D1.
2. Release notes: пересобрать `docs/RELEASE_NOTES_v0.1.0.md` из текущего `[Unreleased]` по процессу (не дописывать draft), `*-draft.md` удалить (`git mv`), ссылку в шаблоне обновить. Previous version — `v0.0.2`.
3. Порядок: ветка (версии + guard + CHANGELOG + notes + `helm/amp/CHANGELOG.md`) → deep-review (pre-release) → merge в `main` → зелёный `ci` на merge-коммите → **[владелец]** `v0.1.0-rc.1` → проверка → **[владелец]** флип пакетов в Public → анонимный `docker pull` amd64/arm64 + `/api/v2/status` → **[владелец]** `v0.1.0` → проверка `latest`/`0.1`.
4. Пост-тег шаги (флип, pull, финальный тег) выходят за рамки обычного `merge-to-main`. Фиксировать их в `tasks.md` отдельной фазой «Release» и закрывать `finalize` только после неё, либо закрыть задачу на merge и завести `PROD-RELEASE-V010-PUBLISH` — выбрать в spec. Рекомендация: одна задача, `finalize` после публикации: критерий «`docker pull` работает» и есть смысл задачи.

## Spec inputs

- Файлы: `helm/amp/Chart.yaml`, `helm/amp/values.yaml`, `helm/amp/values-production.yaml` (убрать устаревший комментарий), `.github/workflows/release.yml` (guard-шаг), `CHANGELOG.md`, `helm/amp/CHANGELOG.md`, `docs/RELEASE_NOTES_v0.1.0.md` (из draft), `docs/RELEASE_NOTES_TEMPLATE.md` (ссылка), `WORKFLOW.md` § Release Process (bump + rc + guard), `docs/CI.md` (rc-процедура рядом с флипом Public — по месту), BACKLOG (закрытие PROD-RELEASE-V010 при finalize).
- Проверки: `helm template` default/dev/production/lite → grep образов (`amp:0.1.0`, reloader при `configReloader.enabled=true` → `amp-config-reloader:0.1.0`); `scripts/release-gate.sh`; `actionlint`; локальная проверка guard'а (скрипт шага на `v0.1.0`, `v0.1.0-rc.1` → pass, `v0.2.0`, `v1.0.0` → fail); rc: лог `steps.meta` (ожидаемые теги), `docker pull` анонимно `--platform linux/amd64|arm64`, `docker run` + `GET /api/v2/status` → `versionInfo.version == "0.1.0-rc.1"`.
- Release notes обязаны содержать: GO-2026-5932; смену дефолтного `image.tag` (→ `.Chart.AppVersion`); Breaking PROD-RBAC-SCOPE verbatim; PROD-AUTH (открыт по умолчанию + WARN; откат на старый образ открывает API — `ROLLBACK_RUNBOOK.md`); `configReloader` выключен и несовместим с `webConfig`; `grouping.enabled: false` по умолчанию (`PROD-GROUPING-DEFAULT`) в Known Gaps; флейк `race` не нужен (внутреннее).
- Known Gaps кандидаты: `PROD-GROUPING-DEFAULT`, `FU-TOPLEVEL-INHIBIT-RULES`, `PROD-GRACEFUL-SHUTDOWN`, `PROD-POSTGRES-HA-DECISION`, `PROD-INGRESS-HARDENING`, `PROD-SECURITY-MD`, `RESOLVE-TIMEOUT-AUTO-RESOLVE`, `CONFIG-MISSING-FILE-DROPS-ENV`, `CI-SUPPLY-CHAIN` (нет SBOM/подписи). Открыто: релизить с незакрытыми P0 (`PROD-GRACEFUL-SHUTDOWN`, `PROD-INGRESS-HARDENING`, `PROD-SECURITY-MD`, `PROD-POSTGRES-HA-DECISION`)? BACKLOG ставит RELEASE-V010 раньше «остального», аудит 2026-09-21 — «pilot-ready». Предложение для spec: `v0.1.0` = pilot-релиз, это явно сказать в Summary и Known Gaps. **Подтвердить у владельца.**
- `SERVICE_VERSION` env — мёртвый; завести строку в BACKLOG, не чинить.

## References

- `helm/amp/Chart.yaml`, `helm/amp/values{,-dev,-production}.yaml`, `helm/amp/templates/deployment.yaml:65,81,237`
- `.github/workflows/release.yml`, `Dockerfile:19-34`, `go-app/internal/buildinfo/buildinfo.go`
- `CHANGELOG.md` (`[Unreleased]`, `[0.0.1]`, ссылки), `helm/amp/CHANGELOG.md`
- `docs/RELEASE_NOTES_v0.1.0-draft.md`, `docs/RELEASE_NOTES_TEMPLATE.md`, `WORKFLOW.md` § Release Process, `docs/CI.md`
- `tasks/archive/PROD-CI-IMAGES/` (ADR-013), `docs/06-planning/BACKLOG.md` § Production Readiness
- `evidence/github-state-2026-09-30.txt`
- docker/metadata-action: поведение `latest=auto` и `{{major}}.{{minor}}` для pre-release — README action (v6), подтвердить на rc-прогоне
