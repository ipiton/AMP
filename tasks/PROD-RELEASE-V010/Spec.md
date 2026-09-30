---
id: PROD-RELEASE-V010
slug: prod-release-v010
stream: Production Readiness / Delivery
type: feature
status: draft
created_at: 2026-09-30
updated_at: 2026-09-30
based_on:
  - requirements.md
  - research.md
---

# Specification: первый релиз AMP v0.1.0

**Version:** 1.0  
**Status:** Draft

## Summary

Версия образа сводится к одной точке правды — `appVersion` чарта (`0.1.0`), а `release.yml` отказывается публиковать тег, который с ней не совпадает. `CHANGELOG` закрывается в `[0.1.0]`, release notes пересобираются из него. Публикация идёт в два шага: пробный `v0.1.0-rc.1`, затем `v0.1.0` — оба тега только с подтверждения владельца. `v0.1.0` позиционируется как **pilot-релиз**: открытые P0 перечислены в Known Gaps (см. Open Questions, OQ1).

## Requirements Coverage

| Requirement / Criterion | Covered by |
|---|---|
| `appVersion: "0.1.0"`, `image.tag` не задан в `values.yaml`/`values-production.yaml`, решение по `values-dev` | Component Architecture §1; решение: `values-dev` остаётся `latest` |
| `helm template` default/dev/production/lite → ожидаемые образы | Component Architecture §2 (`render-image-tag.sh`), Test Plan |
| Guard «тег == `appVersion`» | Component Architecture §3, Invariants I2 |
| `CHANGELOG` закрыт в `[0.1.0]`, `### PROD-INFRA` решена | Component Architecture §4 |
| `docs/RELEASE_NOTES_v0.1.0.md` по процессу, GO-2026-5932 | Component Architecture §5 |
| Pre-release `deep-review` → pass | Deep Review |
| Тег, `release.yml` зелёный, пакеты Public, `docker pull` amd64/arm64 | Rollout |

## Current State

- **Code:** `.github/workflows/release.yml` (публикация по `v*`, `docker/metadata-action` `dc80280…` v6.2.0, build-arg `VERSION`); `Dockerfile:19-34` (ldflags → `internal/buildinfo.Version`); `helm/amp/templates/deployment.yaml:65,237` (`image.tag | default .Chart.AppVersion`), `:81` (`SERVICE_VERSION`, в Go не читается).
- **Data:** not applicable.
- **Tests:** `helm/amp/tests/render-config-reloader.sh` (образец helm-теста, в гейт не входит — `HELM-RENDER-TEST-IN-GATE`); `scripts/release-gate.sh` (`helm-lint`, `helm-template`, `helm-rbac`); `actionlint` в CI.
- **Docs:** `CHANGELOG.md`, `helm/amp/CHANGELOG.md`, `docs/RELEASE_NOTES_v0.1.0-draft.md` (устарел), `docs/RELEASE_NOTES_TEMPLATE.md:7`, `docs/CI.md` § Releasing Images (`:53-68`), `WORKFLOW.md` § Release Process.

## Design Premises

| Premise | Confirmed by | Class | If wrong |
|---|---|---|---|
| Шаблон берёт `.Chart.AppVersion`, когда `image.tag` пустой, для обоих контейнеров | `deployment.yaml:65,237`; grep `image.tag` по `helm/amp/templates` — других потребителей нет | call-path-traced | Рендер даст `amp:` без тега; ловит `render-image-tag.sh` |
| Для pre-release тега metadata-action генерирует только `{{version}}` (+ `sha-`), `latest` не ставится | исходник `src/meta.ts` `procSemver` на пиненом SHA `dc80280…`: при `semver.prerelease` → `{{version}}`, `latest=false`; README там же (строка 523) | code-read | rc перезапишет `latest`/`0.1` ⇒ пользователи `latest` получат rc. Проверка — лог шага `meta` в rc-прогоне |
| `steps.meta.outputs.version` = `0.1.0-rc.1` / `0.1.0` (без `v`) ⇒ это же уходит в `VERSION` | README metadata-action: `{{version}}` = `major.minor.patch[-pre]`; priority semver 900 > sha | code-read | `/api/v2/status` покажет другое значение; проверяется на rc-образе |
| `GITHUB_TOKEN` с `packages: write` в публичном репо пользователя может создать новые пакеты `ghcr.io/ipiton/*` | `docs/CI.md:66` (так задумано в PROD-CI-IMAGES), не запускалось | **assumed** | rc-прогон падает на push ⇒ `v0.1.0` не ставим; фикс в `release.yml`, `rc.2`. См. Rollout R-risk-1 |
| Первая публикация создаёт пакеты **private** | `docs/CI.md:66`; анонимный pull сейчас `denied` (`evidence/github-state-2026-09-30.txt`) | **assumed** (для новых пакетов не наблюдалось) | Если public сразу — шаг флипа просто не нужен |
| Никто, кроме `deployment.yaml`, не читает `image.tag` из values | grep `image\.tag\|\.Values\.image\b\|appVersion` по всему репо (кроме `.git`, `tasks`, `archive`, `charts`): шаблон `deployment.yaml:65`; доки — `ROLLBACK_RUNBOOK.md:141` (`--set image.tag=<previous-tag>`, остаётся верным), `helm/amp/README.md:59` (дефолт `latest` — неверно, правим §6), `docs/CI.md:68` (правим §6); Go-константа `appVersion` — §7 | call-path-traced | Док со старым тегом вводит в заблуждение — правим по списку |
| Последний `ci` на `main` зелёный, красный прогон — известный флейк | `evidence/github-state-2026-09-30.txt` | measured (2026-09-30) | Релизный коммит может покраснеть флейком ⇒ перезапуск job'а, фиксируется в итоге |

## Target Design

1. В чарте версия задаётся один раз: `Chart.yaml` `version` и `appVersion` = `0.1.0`. `image.tag` в `values.yaml` и `values-production.yaml` становится пустым (`""`), поэтому шаблон берёт `.Chart.AppVersion`. `values-dev.yaml` остаётся `latest` + `Always`.
2. Новый helm-тест `render-image-tag.sh` проверяет, что default, production и lite рендерят `ghcr.io/ipiton/amp:<appVersion>`, а reloader при `configReloader.enabled=true` — `…/amp-config-reloader:<appVersion>`.
3. `release.yml` получает первый шаг после checkout: версия тега без `v` и pre-release суффикса должна равняться `appVersion`, иначе job падает до входа в реестр.
4. `CHANGELOG.md` и `helm/amp/CHANGELOG.md` закрываются в `[0.1.0]`. Release notes пересобираются из закрытого блока по процессу `WORKFLOW.md`, в том же коммите, что и тег.
5. После мержа владелец ставит `v0.1.0-rc.1`, мы проверяем публикацию целиком, затем владелец ставит `v0.1.0` на тот же коммит.

## API Contracts

Not applicable — HTTP API не меняется. Меняется контракт Helm values (дефолт `image.tag`), см. Impact Analysis.

## Data Model / Migrations

Not applicable.

## Component Architecture

### §1. Чарт — версии

- `helm/amp/Chart.yaml`: `appVersion: "0.1.0"` (`version: 0.1.0` уже такой).
- `helm/amp/values.yaml:22`: `tag: ""` + комментарий `# defaults to .Chart.AppVersion` (тот же стиль, что у `configReloader.image.tag`, `:812`).
- `helm/amp/values-production.yaml:26-28`: убрать комментарий про «No 1.0.0 image exists», `tag: ""` + тот же комментарий. `pullPolicy: Always` не трогаем.
- `helm/amp/values-dev.yaml`: без изменений (осознанно: dev = плавающий `latest`).

### §2. Helm-тест

- `helm/amp/tests/render-image-tag.sh` — по образцу `render-config-reloader.sh` (bash 3.2, `set -euo pipefail`, here-string вместо `printf | grep -q` — урок PROD-RBAC-SCOPE). `appVersion` читается из `Chart.yaml`, а не хардкодится. Проверки:
  - default, `-f values-production.yaml` (с обязательными паролями через `--set`, как в `render-config-reloader.sh`), `--set profile=lite` → ровно `image: "ghcr.io/ipiton/amp:<appVersion>"`;
  - `--set configReloader.enabled=true` → `ghcr.io/ipiton/amp-config-reloader:<appVersion>`;
  - `--set image.tag=9.9.9` → `amp:9.9.9` (явный пин по-прежнему выигрывает).
- В `release-gate.sh` **не** добавляем: это scope `HELM-RENDER-TEST-IN-GATE` (запускает все `helm/amp/tests/*.sh` разом). Тест гоняется на `testing` вручную.

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

- Тег передаётся через `env`, а не подстановкой в `run` (без shell-инъекции из имени ref).
- Выполняется в обоих job'ах матрицы — это дёшево, и так проще, чем заводить отдельный job с `needs`.
- `Chart.yaml` `version` guard не проверяет: соглашение «`version` = `appVersion`» фиксируется в `WORKFLOW.md`, а чарт не публикуется.

### §4. CHANGELOG

- `CHANGELOG.md`:
  - новый пустой `## [Unreleased]`, затем `## [0.1.0] - <дата>` со всем текущим содержимым;
  - `### PROD-INFRA` → `### Added (release tooling)`: меняется один заголовок, текст не переписываем;
  - первой строкой `[0.1.0]` — заметка: `v0.0.2` (2025-12-09) отдельно не документировался, его изменения («Code Quality Refactoring») входят сюда. Коротким bullet'ом в `### Changed` — дефолт `image.tag` → `.Chart.AppVersion` (migration: явный пин по-прежнему работает; `values-production.yaml` больше не пинит `1.0.0`);
  - ссылки: `[Unreleased]: …/compare/v0.1.0...HEAD`, `[0.1.0]: …/compare/v0.0.2...v0.1.0`.
- `helm/amp/CHANGELOG.md`: `[Unreleased]` → `## [0.1.0] - <дата>`, пустой `[Unreleased]`; bullet про `appVersion` / `image.tag` в `### Changed`.
- Дата — день мержа. Если финальный тег уходит на другой день, дата правится отдельным docs-коммитом до `v0.1.0` (см. Rollout).

### §5. Release notes

- `git mv docs/RELEASE_NOTES_v0.1.0-draft.md docs/RELEASE_NOTES_v0.1.0.md`, затем содержимое пересобирается из закрытого `[0.1.0]` по `WORKFLOW.md` § Release Process (шаги 1–5). Отклонение от шага 6 (переименование «после finalize»): итоговые notes должны лежать в коммите тега. Порядок шагов в `WORKFLOW.md` правится соответственно (§6).
- Шапка: `Release date: <дата>`, `Previous version: v0.0.2`, «Generated from `CHANGELOG.md` `[0.1.0]`».
- Summary: pilot-релиз — функциональная замена Alertmanager с basic auth, узким RBAC и публичными multi-arch образами; для прода см. Known Gaps.
- Обязательно:
  - Breaking changes — verbatim из CHANGELOG, включая PROD-RBAC-SCOPE и смену дефолтного `image.tag`;
  - Backward Compatibility — что НЕ меняется: без web config API открыт, как раньше, + WARN; K8s-Secret targets; webhook payload;
  - Upgrade Steps;
  - Security — GO-2026-5932 (`x/crypto/openpgp`, фикса нет, AMP импортирует только `x/crypto/bcrypt`, сканеры образов будут показывать);
  - Known Gaps — открытые P0/P1 из BACKLOG: `PROD-GRACEFUL-SHUTDOWN`, `PROD-INGRESS-HARDENING`, `PROD-SECURITY-MD`, `PROD-POSTGRES-HA-DECISION`, `PROD-GROUPING-DEFAULT`, `FU-TOPLEVEL-INHIBIT-RULES`, `PROD-LLM-ALERT-PATH-ISOLATION`, `RESOLVE-TIMEOUT-AUTO-RESOLVE`, `CONFIG-MISSING-FILE-DROPS-ENV`, `CONFIG-RELOADER-AUTH` (reloader выключен и несовместим с `webConfig`), `CI-SUPPLY-CHAIN` (нет SBOM/подписи), `PROD-AUTH-BEARER`.
- `docs/RELEASE_NOTES_TEMPLATE.md:7`: ссылка на пример → `RELEASE_NOTES_v0.1.0.md`.
- grep по репозиторию на `RELEASE_NOTES_v0.1.0-draft` — все ссылки обновить, кроме `tasks/archive/` и `docs/06-planning/archive/`: это история, её не переписываем.

### §6. Процессные доки

- `WORKFLOW.md` § Release Process — шаг 0: bump `Chart.yaml` `version` + `appVersion` (guard в `release.yml` упадёт иначе). Шаг 6 переписывается: закрыть `[Unreleased]` и финализировать notes **до** тега, в релизном коммите. Новый шаг 7: rc-тег → проверка → финальный тег на том же коммите; оба тега ставятся только с зелёного `ci` на `main` и с явного решения владельца.
- `docs/CI.md` § Releasing Images: `:55` пример с rc; `:68` («values-production pins image.tag») переписать: тег образа берётся из `appVersion`, `release.yml` проверяет совпадение; pre-release не двигает `latest`/`X.Y`.
- `helm/amp/README.md:59`: таблица values документирует дефолт `image.tag` как `latest` (неверно и сейчас) → `""` (defaults to `.Chart.AppVersion`).
- `docs/06-planning/BACKLOG.md`: строка `SERVICE-VERSION-ENV-DEAD` (env `SERVICE_VERSION` в `deployment.yaml:81`, Go его не читает, ~0.1d). Закрытие `PROD-RELEASE-V010` — на `finalize`.

### §7. Версия в бинаре (расширение scope, OQ3)

- `go-app/cmd/server/main.go:24`: константа `appVersion = "0.0.1"` попадает в стартовый лог (`main.go:55`) и в 5 страниц legacy-дашборда (`cmd/server/legacy_dashboard.go:91-143`). Образ `0.1.0` писал бы `version 0.0.1`. Правильное значение уже инжектится ldflags в `internal/buildinfo.Version` и отдаётся `/api/v2/status`.
- Сделать: удалить `appVersion` из `const`-блока, 6 использований → `buildinfo.Version`. `appName` не трогать. Вне Docker (`go run`, тесты) значение будет `dev` вместо `0.0.1`. grep `appVersion` по `go-app` — других потребителей нет, тестов на строку `0.0.1` в дашборде нет (проверить на `implement`: `grep -rn '0\.0\.1' go-app`).
- Тест: существующие тесты `cmd/server` + проверка стартового лога на rc-образе (`"version":"0.1.0-rc.1"`).

## Security Design

- [x] Ownership validation — not applicable.
- [x] Input validation: имя тега попадает в shell только через `env` (`TAG`), без `${{ }}` внутри `run`.
- [x] Sensitive data не логируется: guard печатает тег и `appVersion` — публичные значения.
- [x] Rate limiting — not applicable.
- [x] Auth/RBAC: права workflow не меняются (`permissions: {}` на верхнем уровне, `packages: write` только у `publish`). Новых actions нет — пинов по SHA не добавляется.
- [x] Supply chain: публичные образы без SBOM и подписи — осознанный gap (`CI-SUPPLY-CHAIN`), в Known Gaps release notes.

## Invariants

- [ ] I1. Default, production и lite рендер ссылаются на `ghcr.io/ipiton/amp:<appVersion>`; явный `image.tag` в values/`--set` по-прежнему выигрывает.
- [ ] I2. `release.yml` ничего не публикует, если `${TAG#v}` без `-*` ≠ `appVersion` (включая пустой `appVersion`).
- [ ] I3. Pre-release тег не двигает `latest` и `X.Y`.
- [ ] I4. Однажды опубликованный тег (`v0.1.0-rc.1`, `v0.1.0`) не переписывается и не удаляется. Ошибка исправляется следующей версией (`rc.2`, `v0.1.1`).
- [ ] I5. `v0.1.0` ставится только на коммит `main` с зелёным `ci`, ранее проверенный rc. Исключение — коммит, отличающийся от rc-коммита только датой в CHANGELOG/notes (docs-only).
- [ ] I6. HTTP API и конфиг приложения не меняются. Go-код — только §7: источник строки версии, без изменения поведения.

## Edge Cases

1. Тег `v0.1.0` при `appVersion: "0.1.1"` (забыли откатить или закоммитили bump раньше) → оба job'а падают на guard, в GHCR ничего не пишется.
2. Тег `v0.1.0+build.1` (build metadata): `%%-*` не срезает `+` → mismatch → fail. Build metadata не используем — приемлемо.
3. `appVersion` без кавычек (`appVersion: 0.1.0`) → sed понимает оба варианта; пустая строка → fail (явная ветка `-z`).
4. rc: один job матрицы упал, второй опубликовал → `v0.1.0` не ставим. Причину чиним и выпускаем `v0.1.0-rc.2` (`fail-fast: false` оставляем: логи обоих полезнее).
5. `gate` на merge-коммите красный из-за grouping-флейка → перезапуск job'а. Если красный дважды — стоп, по правилу «gate fails twice».
6. Финальный тег на следующий день после мержа → docs-коммит с датой, ждём зелёный `ci`, тег ставим на него (I5, исключение).
7. После флипа Public `docker pull` анонимно всё равно `unauthorized` → проверить привязку пакета к репо (label `org.opencontainers.image.source` ставит metadata-action) и видимость. Без решения финальный тег не ставим.

## Impact Analysis

- **Affected modules:** `go-app/cmd/server/{main,legacy_dashboard}.go` (§7), `helm/amp/` (Chart, 2 values, новый тест, CHANGELOG, README), `.github/workflows/release.yml`, `CHANGELOG.md`, `docs/RELEASE_NOTES_*`, `docs/CI.md`, `WORKFLOW.md`, BACKLOG.
- **Breaking changes:** дефолт `image.tag`: `0.0.1` (values.yaml) / `1.0.0` (production) → `.Chart.AppVersion` = `0.1.0`. Ни `0.0.1`, ни `1.0.0` в GHCR никогда не существовали, так что практически никто не ломается. Кто пинил тег явно, не затронут.
- **New dependencies:** none.
- **Risks:** см. Rollout / Rollback; главный — первый в истории прогон `release.yml` (снижается rc-тегом).

## Rollout / Rollback

- **Rollout:**
  1. Ветка → `deep-review` (pre-release) → `write-tests` → `testing` → merge в `main` (`finalize` откладывается, см. ниже).
  2. Зелёный `ci` на merge-коммите.
  3. **[владелец, подтверждение в сессии]** `git tag v0.1.0-rc.1 <sha> && git push origin v0.1.0-rc.1`.
  4. Проверка rc:
     - `release.yml` зелёный, оба job'а;
     - в логе `meta` только `0.1.0-rc.1` и `sha-<short>`;
     - в GHCR два пакета.
  5. **[владелец]** оба пакета → Public (`docs/CI.md:66`).
  6. Анонимно (`docker logout ghcr.io`):
     - `docker pull --platform linux/amd64|linux/arm64` обоих образов `:0.1.0-rc.1`;
     - `docker run` amp + `GET /api/v2/status` → `versionInfo.version == "0.1.0-rc.1"`;
     - `docker manifest inspect` → два платформенных манифеста.
     Сырые выводы — в `evidence/`.
  7. **[владелец, подтверждение]** `v0.1.0` на тот же коммит (или docs-коммит с датой, I5).
  8. Проверка: теги `0.1.0`, `0.1`, `latest`, `sha-`; анонимный pull `:0.1.0` на обеих архитектурах; `helm template` с дефолтами → `amp:0.1.0` (уже есть в тесте).
  9. `finalize` на ветке-хвосте (DONE, BACKLOG `[x]`, archive) → `merge-to-main`.
  - Зависимость пайплайна: `finalize` и `merge-to-main` фреймворка предполагают один мерж. Здесь мержей два: релизный коммит до тега, `finalize` после публикации. Вариант — `finalize` на короткой ветке `docs/prod-release-v010-finalize` после шага 8. Фиксируется в `tasks.md`.
- **Rollback:**
  - До тега — обычный revert в ветке.
  - Упал rc (push или частичная публикация) → фикс, `v0.1.0-rc.2`; тег rc.1 и частичные образы остаются, в notes не упоминаются.
  - Дефект, найденный после `v0.1.0`, → `v0.1.1`. Тег `v0.1.0` и образы не удаляются (I4).
  - Guard ложно блокирует → правка `release.yml` в `main` и новый rc: тег со старым workflow уже не перезапустить (workflow берётся из коммита тега).
- **Feature flag:** not applicable.
- **R-risk-1 (assumed premise):** `GITHUB_TOKEN` не может создать пакет → rc падает на push. Митигация — rc-тег, дальше по Rollback.
- **R-risk-2 (assumed premise):** пакеты создаются не private или флип недоступен/не срабатывает → Edge Case 7.

## Observability

- **Logs:** `::error::` guard'а в логе `release.yml`.
- **Metrics:** not applicable. `amp_build_info` / `/api/v2/status` `versionInfo` уже есть (PARITY-4.2) — проверяются на rc.
- **Alerts:** not applicable.

## Deep Review

- **Mandatory triggers present:** `S`, `M`, pre-release (обязателен перед тегом `v*` по `WORKFLOW.md`), 4 сигнала (`C S M R`).
- **Discretionary triggers present:** none (дифф в основном docs: release notes до ~350 строк текста, логики мало).
- **Decision:** required. Фокус ревью: guard (обход/ложный отказ), правильность verbatim-переноса breaking changes и полноту Known Gaps, соответствие Rollout инвариантам I3–I5.

## Open Questions

- [x] **OQ1 (владелец), решено 2026-09-30:** релиз пока **не выпускаем** (ни `v0.1.0-rc.1`, ни `v0.1.0`). Rollout шаги 3–9 не выполняются до отдельного решения владельца. Объём задачи до релиза — открыт (см. OQ4).
- [ ] **OQ4 (владелец):** делать ли сейчас подготовительный срез без тега (§1 версии чарта, §2 helm-тест, §3 guard, §6 README/CI.md, §7 версия в бинаре) и закрыть его, а CHANGELOG/release notes/теги (§4, §5) оставить на момент релиза — или поставить задачу на паузу целиком.
- [ ] **OQ2:** `finalize` на отдельной короткой ветке после публикации (рекомендация) — подтвердить на `plan-task`.
- [ ] **OQ3 (владелец):** §7 — Go-правка источника версии (константа `0.0.1` → `buildinfo.Version`) в этой задаче? Spec исходит из «да»: без неё релизный образ сам себя называет `0.0.1` в логе и дашборде. Альтернатива — BACKLOG и Known Gap.
- [x] ~~metadata-action и pre-release~~ — снято: исходник `procSemver` на пиненом SHA.
