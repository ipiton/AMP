# Requirements: PROD-DEPS-VULN

## Context

Блокер P0 из BACKLOG («Production Readiness — блокеры»), заведён по итогам PROD-CI-IMAGES (2026-09-28). Идёт перед `PROD-RELEASE-V010`: первый опубликованный образ не должен нести известных уязвимостей.

Состояние на 2026-09-29:

- Job `govulncheck` в `.github/workflows/ci.yml` (`govulncheck@v1.8.0 ./...` в `go-app`) красный и сознательно не required (`docs/CI.md`, ADR-013).
- 9 достижимых уязвимостей в 6 модулях (`go-app/go.mod`):

| Модуль | Сейчас | Находки | Fix ≥ | В go.mod |
|---|---|---|---|---|
| `google.golang.org/grpc` | v1.77.0 | GO-2026-6348, GO-2026-6061 | v1.83.1 | indirect |
| `golang.org/x/text` | v0.31.0 | GO-2026-5970 | v0.39.0 | direct |
| `go.opentelemetry.io/otel` | v1.39.0 | GO-2026-5506 | v1.41.0 | direct |
| `go.opentelemetry.io/otel/sdk` | v1.39.0 | GO-2026-5426, GO-2026-4394 | v1.43.0 | direct |
| `golang.org/x/net` | v0.47.0 | GO-2026-5026, GO-2026-4918 | v0.55.0 | indirect |
| `github.com/jackc/pgx/v5` | v5.7.6 | GO-2026-5004 | v5.9.2 | direct |

- otel-модули (`otel`, `otel/sdk`, `otlptracegrpc` и др.) версионируются вместе — поднимать согласованно; grpc/otel тянут транзитивку (`x/net`, `x/sys`, genproto и т.д.).
- Toolchain `go1.26.8` — уязвимости stdlib в список не входят; если после апгрейда всплывут, решить отдельно.

## Goals

- [ ] Поднять перечисленные модули до версий с фиксом (и согласованные с ними otel/grpc-модули), `go mod tidy`.
- [ ] `govulncheck ./...` в `go-app` — 0 достижимых уязвимостей.
- [ ] `govulncheck` → required check: `docs/CI.md` (таблица), комментарий в `.github/workflows/ci.yml`.
- [ ] Release-gate (`scripts/release-gate.sh`), `go vet`, `go test -race` в объёме CI — зелёные.

## Constraints

- Supply chain: карантин 7 дней — брать версии, опубликованные не позже 2026-09-22; для Go проверять возраст вручную (`go list -m -json <mod>@<ver>` → `Time`). Если fix-версия моложе 7 дней — зафиксировать решение явно (ждать или исключение с обоснованием).
- Минимальный дифф: поднимать только то, что нужно для фиксов, плюс неизбежную транзитивку; без «заодно обновить всё» (`go get -u ./...` — нет).
- Breaking changes в API модулей (pgx 5.7→5.9, otel 1.39→1.43, grpc 1.77→1.83) — правки Go-кода только необходимые для сборки/тестов, отдельным коммитом.
- `go` directive / toolchain не менять, если апгрейд этого не требует; если требует — отдельное решение (Dockerfile'ы, `setup-go` в CI).
- Известный флейк `GROUPING-TIMER-LOCK-RELEASED-BEFORE-LOSER` (`-race`, ~1/15) и `PUBLISHING-WARMUP-TEST-FLAKY` — не чинить здесь, при падении перепрогнать и зафиксировать.
- Branch protection включает владелец репозитория; задача только документирует новый required check.

## Success Criteria (Definition of Done)

- [ ] `go-app/go.mod`/`go.sum` обновлены, версии удовлетворяют карантину (или исключение задокументировано).
- [ ] `govulncheck@v1.8.0 ./...` — чисто (локально и в CI на PR).
- [ ] `go build ./...`, `go vet ./...`, `go test -race` (как в CI), `scripts/release-gate.sh` — зелёные (флейки — задокументированы).
- [ ] Образы `amp` и `amp-config-reloader` собираются (job `images` в CI).
- [ ] `docs/CI.md`: `govulncheck` required; `CHANGELOG.md` `[Unreleased]` (Security); BACKLOG-запись закрыта; DECISIONS — если были исключения из карантина.
