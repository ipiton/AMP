# Requirements: PROD-CI-IMAGES

## Context

Блокер P0 (Delivery) из аудита production readiness (2026-09-21, BACKLOG «Production Readiness — блокеры»). По рекомендуемому порядку идёт третьим, после закрытых PROD-AUTH и PROD-RBAC-SCOPE; от него зависит `PROD-RELEASE-V010`.

Состояние на 2026-09-28:

- CI нет вообще: `.github/` отсутствует и никогда не существовал. Все гейты (`go vet`, тесты, `scripts/release-gate.sh`, `deploy/e2e-ha`) гоняются только руками на машине разработчика — PR/мерж ничем не защищён.
- Опубликованных образов нет. Ссылки на образы в чарте расходятся и никуда не ведут:
  - `helm/amp/values.yaml:21-22` — `ghcr.io/ipiton/amp:0.0.1`;
  - `helm/amp/values-production.yaml:25-26` — `ipiton/amp-llm:1.0.0` (Docker Hub, образа нет ⇒ прод-инсталл = `ImagePullBackOff`);
  - `helm/amp/values-production.yaml:449-450` — config-reloader из `registry.example.com/amp/config-reloader`, тег пустой.
- Сборочные артефакты есть: `Dockerfile` (сервер) и `Dockerfile.config-reloader` в корне, `go-app/Makefile`, `scripts/release-gate.sh`.
- Remote — `github.com/ipiton/AMP` ⇒ естественный реестр — GHCR (`ghcr.io/ipiton/...`).

## Goals

- [ ] GitHub Actions workflow на PR и push в `main`: build, `go vet`, `go test -race`, `scripts/release-gate.sh`, govulncheck.
- [ ] HA e2e (`deploy/e2e-ha`) включён в CI (отдельный job; решить на `/spec` — на каждый PR или по расписанию/лейблу, с учётом времени прогона).
- [ ] Release workflow на push тега `v*`: публикация `amp` и `amp-config-reloader` в GHCR, multi-arch (`linux/amd64`, `linux/arm64`), теги по semver + sha.
- [ ] Ссылки на образы в `values.yaml` / `values-production.yaml` указывают на публикуемые образы (единый реестр, без `amp-llm` и чужого `registry.example.com`).
- [ ] Защита `main`: PR без зелёного CI не мержится (branch protection — настройка репозитория, делает владелец; в задаче — документировать required checks).

## Constraints

- Supply chain: сторонние actions пинить по commit SHA, а не по тегу; минимальные `permissions:` на workflow/job (`contents: read`, `packages: write` — только в release). Новые зависимости/actions — с учётом 7-дневного карантина.
- Секреты не коммитить; публикация в GHCR — через `GITHUB_TOKEN`, без личных PAT.
- Зависит от `PROD-HELM-CLEAN-CHECKOUT` (`helm/amp/charts` в `.gitignore` ⇒ `helm template` на чистом checkout падает): release-gate в CI без `helm dependency build` не пройдёт. На `/research`/`/spec` решить — втянуть в срез или сделать шагом CI.
- Известные предсуществующие дефекты гейтов, которые всплывут в CI: `PUBLISHING-WARMUP-TEST-FLAKY` (BUGS.md), `PARITY-GATE-DOES-NOT-GATE`, `QUALITY-GATES-DIRTIES-TREE` (BACKLOG). Не чинить молча в рамках задачи — зафиксировать решение (quarantine/skip с ссылкой или отдельный срез).
- Выравнивание версий (`Chart.yaml` `appVersion`, тег `v0.1.0`, CHANGELOG) — не здесь, это `PROD-RELEASE-V010`.
- Go-код не меняем, кроме необходимого для прохождения CI (и тогда — отдельным коммитом с обоснованием).
- Оценка ~1.5d; при росте — нарезать (например: срез 1 — CI-гейты на PR; срез 2 — публикация образов).
- Внешняя интеграция + риск «сломать прод» ⇒ `/research` перед `/spec`.

## Success Criteria (Definition of Done)

- [ ] Workflow-файлы в `.github/workflows/`, проходят `actionlint` (или эквивалентную проверку).
- [ ] CI-прогон на PR этой ветки зелёный (или красный только на задокументированных предсуществующих дефектах).
- [ ] Release workflow проверен (dry-run / тестовый тег / `workflow_dispatch` без push) — образы собираются под обе архитектуры.
- [ ] `docker pull` образов, на которые ссылаются `values.yaml` и `values-production.yaml`, работает после первого релиза (или явно зафиксировано, что проверка откладывается до `PROD-RELEASE-V010`).
- [ ] Документация: как устроен CI, required checks для branch protection, как выпустить образ (README / `docs/`); `CHANGELOG.md` `[Unreleased]` обновлён; BACKLOG-запись закрыта.
