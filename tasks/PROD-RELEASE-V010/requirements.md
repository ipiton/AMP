---
id: PROD-RELEASE-V010
slug: prod-release-v010
stream: Production Readiness / Delivery
type: feature
priority: critical
status: active
created_at: 2026-09-30
updated_at: 2026-09-30
---

# Requirements: первый релиз AMP v0.1.0

## Problem Framing

- **Symptom:** у проекта нет пригодного релиза. Теги — только `v0.0.1`/`v0.0.2` (2025-12), после них 500+ коммитов. `CHANGELOG.md` — один большой `[Unreleased]`; `docs/RELEASE_NOTES_v0.1.0-draft.md` — черновик с «TBD». Образов в GHCR нет ни одного: `release.yml` (PROD-CI-IMAGES) ни разу не запускался ⇒ любой `helm install` = ImagePullBackOff.
- **Root Cause:** версии не согласованы и никто не резал релиз:
  - `helm/amp/Chart.yaml`: `version: 0.1.0`, `appVersion: "0.0.1"`;
  - `helm/amp/values.yaml:22`: `image.tag: "0.0.1"` (перекрывает `default .Chart.AppVersion` в `deployment.yaml:65`);
  - `helm/amp/values-production.yaml:28`: `image.tag: "1.0.0"` — образа с таким тегом не будет никогда;
  - `helm/amp/values-dev.yaml:16`: `tag: "latest"`;
  - config-reloader уже берёт `.Chart.AppVersion` (`tag: ""`).
  `release.yml` публикует тег образа без `v` (`0.1.0`, `0.1`, `sha-<short>`, `latest`).
- **Why Now:** последний P0-блокер Delivery; от него зависят `CONFIG-RELOADER-SIDECAR` (Queue) и проверка «`docker pull` работает» из PROD-CI-IMAGES. `PROD-DEPS-VULN` закрыт — первый образ не несёт известных исправимых уязвимостей.
- **How We Measure:** после тега `v0.1.0` — `release.yml` зелёный; `docker pull ghcr.io/ipiton/amp:0.1.0` и `…/amp-config-reloader:0.1.0` без логина на amd64 и arm64; `helm template` с дефолтными и production values рендерит `ghcr.io/ipiton/amp:0.1.0`.

## Risk Profile

- **Signals:** `C S M R`
  - `C` — Helm values: дефолт `image.tag` меняется (явный тег → `.Chart.AppVersion`); кто пинил тег через values, не затронут, кто полагался на дефолт — получает другой образ.
  - `S` — supply chain: первая публикация образов в публичный реестр, пакеты GHCR переводятся в Public.
  - `M` — необратимая операция: опубликованный тег и образы нельзя «отозвать» бесследно (кэши, зеркала, pull-и). Перевыпуск того же тега запрещён.
  - `R` — это и есть релиз: первое, что увидят операторы.
- **Tier:** Full
- **Notes:** независимо от tier `WORKFLOW.md` требует pre-release `deep-review` перед каждым тегом `v*`. Push тега и перевод пакетов в Public — действия владельца / только с явного подтверждения в сессии.

## User Stories

1. Как оператор, я хочу `helm install` чарта AMP с дефолтными или production values и получить работающий под из публичного образа, без правки тегов руками.
2. Как оператор, мигрирующий с Alertmanager, я хочу release notes с breaking changes, upgrade-шагами и списком того, что НЕ меняется, чтобы не читать `CHANGELOG` целиком и код.
3. Как мейнтейнер, я хочу одну точку правды для версии (Chart `appVersion` = тег образа), чтобы следующие релизы не требовали сверки трёх файлов.

## Success Criteria

- [ ] `Chart.yaml` `appVersion: "0.1.0"`; `image.tag` в `values.yaml` и `values-production.yaml` не задан (пусто) ⇒ используется `.Chart.AppVersion`; `values-dev.yaml` — решение зафиксировано (оставить `latest` или тоже `AppVersion`).
- [ ] `helm template` (default / dev / production / lite) рендерит ожидаемые образы `ghcr.io/ipiton/amp:0.1.0` и `ghcr.io/ipiton/amp-config-reloader:0.1.0`; release-gate зелёный (с оговоркой про известный флейк `GROUPING-TIMER-LOCK-RELEASED-BEFORE-LOSER`, если проявится).
- [ ] Решено, нужен ли guard «тег git == `appVersion`» (в `release.yml` или гейте), чтобы рассинхрон не повторился; если да — реализован.
- [ ] `CHANGELOG.md`: `[Unreleased]` закрыт в `## [0.1.0] - YYYY-MM-DD`, новый пустой `[Unreleased]`; нестандартная секция `### PROD-INFRA` разнесена или обоснованно оставлена.
- [ ] `docs/RELEASE_NOTES_v0.1.0.md` по процессу `WORKFLOW.md` § Release Process (Collect → Split → Verify breaking → Backward compatibility → Upgrade steps), без «TBD»; упомянут GO-2026-5932 (`x/crypto/openpgp`, фикса нет, не импортируется).
- [ ] Pre-release `deep-review` → `review-verdict.json` `"gate": "pass"`.
- [ ] После мержа: тег `v0.1.0` на `main` (с подтверждения владельца), `release.yml` зелёный, пакеты `amp` и `amp-config-reloader` переведены в Public, `docker pull` без логина на amd64 и arm64 проверен.

## Non-Goals

- Включение `configReloader.enabled` в production values — `CONFIG-RELOADER-SIDECAR` / `CONFIG-RELOADER-AUTH`.
- SBOM, подпись cosign, Trivy, digest-пины — `CI-SUPPLY-CHAIN`.
- Публикация Helm-чарта в OCI/Helm-репозиторий (не заявлено в BACKLOG; при необходимости — отдельная задача).
- Правки кода ради «красивого» релиза; найденные дефекты — в `BUGS.md` / BACKLOG, не в этот дифф.
- Branch protection — owner action `MAIN-BRANCH-PROTECTION`.

## Constraints

- **Scope:** `helm/amp/{Chart.yaml,values*.yaml}`, возможно `helm/amp/README.md`/`CHANGELOG.md` чарта, `CHANGELOG.md`, `docs/RELEASE_NOTES_*`, `.github/workflows/release.yml` (guard). Go-код — только источник строки версии (`cmd/server/main.go` константа `appVersion = "0.0.1"` → `buildinfo.Version`, найдено на `/spec`, OQ3); поведение не меняется.
- **Security:** публичные образы; `release.yml` — единственный workflow с `packages: write`, не расширять права.
- **Compatibility:** смена дефолтного `image.tag` — в release notes / migration notes. Тег образа без `v` (`0.1.0`), git-тег с `v` (`v0.1.0`). Тег необратим — перед push всё проверено, повторного `v0.1.0` не бывает (исправление = `v0.1.1`).

## Discovery Notes

- Similar tasks: `tasks/archive/PROD-CI-IMAGES/` (release.yml, ADR-013, `docs/CI.md`), `tasks/archive/PROD-DEPS-VULN/`.
- Relevant patterns: `WORKFLOW.md` § Release Process; `docs/RELEASE_NOTES_TEMPLATE.md`; `docs/RELEASE_NOTES_v0.1.0-draft.md` (332 строки, «TBD»); `helm/amp/templates/deployment.yaml:65,237` (`default .Chart.AppVersion`); `.github/workflows/release.yml` (`docker/metadata-action`, VERSION build-arg → buildinfo).
- Open unknowns:
  - `release.yml` не запускался ни разу — первый прогон и есть проверка; нужен ли пробный pre-release тег (`v0.1.0-rc.1` — `latest` не ставится) до финального?
  - Состояние CI на GitHub пользователь считает непроверенным (итог PROD-DEPS-VULN) — прогнать PR перед тегом.
  - Версия бинаря: подхватывает ли `/api/v2/status` `versionInfo.version` = `0.1.0` из build-arg (`STATUS-VERSIONINFO-CONTRACT` не трогаем, только проверяем).
  - Дата релиза в `CHANGELOG` = дата тега, а не мержа.
