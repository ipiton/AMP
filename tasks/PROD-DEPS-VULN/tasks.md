# Implementation Checklist: PROD-DEPS-VULN

Ветка `bugfix/prod-deps-vuln`. Источник: `Spec.md` (решения D1–D5, критерии приёмки). Пути — от корня репозитория.

Один срез (~0.25–0.5d), мержится целиком. Резать не на что: апгрейд и перевод `govulncheck` в required друг без друга смысла не имеют. Required без апгрейда = красный PR; апгрейд без required = регрессия пройдёт незамеченной.

## Допущения и блокеры

- **Toolchain.** Локальный `go` — 1.27.1. Все команды гнать с `GOTOOLCHAIN=go1.26.8`: как `toolchain` в `go.mod` и `go-version-file` в CI. Иначе `go get` может записать в `go.mod` другой `toolchain`, а результаты `govulncheck` разойдутся с CI.
- **Сеть** нужна для `go get` (proxy.golang.org), `govulncheck` (vuln.go.dev), `helm dependency build` в release-gate.
- **Docker** нужен для testcontainers-тестов pgx, шага `amtool-compat` и локального `deploy/e2e-ha`. В research был поднят.
- **CI на PR.** Push ветки и открытие PR — действия наружу, делать только с подтверждения пользователя (на `/testing`). Без PR критерий «зелёные jobs на PR» проверяется только локальными эквивалентами. Если так и выйдет, записать это явно.
- **Vuln DB меняется.** Если к `/implement` появятся новые advisory на целевые версии, поднять минимально и заново проверить карантин (Spec D2). Это отклонение, записать ниже.

## Research & Spec
- [x] Research — `research.md` (вариант B, otel ≥ 1.44 из-за grpc, карантин без исключений, код не трогаем)
- [x] Spec — `Spec.md` (D1–D5), 2026-09-29, вариант B выбран пользователем

## Implementation

- [ ] **I1. Апгрейд зависимостей (D1)**, в `go-app/`:
  ```
  GOTOOLCHAIN=go1.26.8 go get \
    google.golang.org/grpc@v1.83.2 \
    go.opentelemetry.io/otel@v1.44.0 \
    go.opentelemetry.io/otel/sdk@v1.44.0 \
    go.opentelemetry.io/otel/trace@v1.44.0 \
    go.opentelemetry.io/otel/exporters/otlp/otlptrace@v1.44.0 \
    go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc@v1.44.0 \
    github.com/jackc/pgx/v5@v5.9.2 \
    golang.org/x/crypto@v0.56.0 \
    golang.org/x/net@v0.58.0 \
    golang.org/x/text@v0.39.0
  ```
  - `go mod tidy` **не** запускать.
  - Сверить итог с research §3B/§4: `go.mod` +21/−21, `go`/`toolchain` не изменились, x/text по MVS = v0.41.0. Если выбрана версия, которой нет в таблице §4, проверить её `Time` через `go list -m -json` (карантин ≤ 2026-09-22).
- [ ] **I2. Быстрая проверка:** `go build ./...`, `go vet ./...`, `govulncheck@v1.8.0 ./...` → exit 0; `-show verbose`: package-level пусто, module-level — только GO-2026-5932.
- [ ] **I3. Коммит 1** (D5): `fix(deps): upgrade grpc, otel, pgx and x/* to clear govulncheck findings`, только `go-app/go.mod` + `go-app/go.sum`. В теле — список GO-ID.
- [ ] **I4. `.github/workflows/ci.yml` (D3)** — только комментарии:
  - шапка: `govulncheck` — в списке required, `e2e-ha` остаётся не required;
  - над job'ом `govulncheck`: вместо «Not required until PROD-DEPS-VULN…» — required; red означает достижимую уязвимость, лечится апгрейдом; module-level находки (GO-2026-5932) exit code не портят.
  - `actionlint` (`go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12`) — чисто.
- [ ] **I5. `docs/CI.md` (D3, D4):**
  - таблица: `govulncheck` → `yes`, «Why» переписать: достижимая уязвимость = блокер релиза; компромисс — новая запись в vuln DB краснит и PR, который зависимости не трогал, лечится отдельным PR с апгрейдом;
  - абзац про branch protection: добавить `govulncheck` в список имён job'ов;
  - одна строка про GO-2026-5932 (ожидаема, из `x/crypto` используется только `bcrypt`, её покажут и сканеры образов);
  - прочие упоминания «govulncheck красный / PROD-DEPS-VULN» в `docs/CI.md` — найти (`grep -n`) и выровнять.
- [ ] **I6. Коммит 2:** `ci: make govulncheck a required check` (`ci.yml` + `docs/CI.md`).

## Testing (`/write-tests`, `/testing`)

Новых тестов нет: поведение кода не меняется, регрессию ловят существующие тесты и сам `govulncheck` в required. На `/write-tests` зафиксировать это решение, тесты не выдумывать.

- [ ] **T1.** `GOTOOLCHAIN=go1.26.8 ./scripts/release-gate.sh` → `RESULT: PASS` (все 10 шагов). Флейки перезапустить, записать ниже.
- [ ] **T2.** pgx на реальном Postgres: `go test -count=1 -v ./internal/database/...` — `TestRunMigrations_ConcurrentReplicas_*`, `TestPostgresPool_Reload_*` = PASS, не SKIP.
- [ ] **T3.** `deploy/e2e-ha/run.sh` локально — PASS. Это единственное покрытие repository-пути silencing: его unit-тесты — заглушки со `t.Skip`.
- [ ] **T4.** Образы: `docker buildx build --platform linux/amd64,linux/arm64` для `Dockerfile` и `Dockerfile.config-reloader` (как job `images`, без push). Если локально нет multi-arch builder'а — хотя бы нативная архитектура, остальное — CI на PR.
- [ ] **T5.** CI на PR (с подтверждения пользователя): зелёные `gate`, `images (amp)`, `images (config-reloader)`, `actionlint`, `govulncheck`, `e2e-ha`.
- [ ] **T6.** `git diff main --stat` — затронуты только файлы из Spec «Scope»; `git diff --check` чистый.

## Documentation (`/write-doc`)

- [ ] **W1.** `CHANGELOG.md` `[Unreleased]`: новая секция `### Security` (её сейчас нет, поставить первой). `PROD-DEPS-VULN`: 9 reachable GO-ID + module-level fix, таблица «модуль: было → стало» для прямых зависимостей, `govulncheck` required, остаточный GO-2026-5932. Без breaking changes.
- [ ] **W2.** Проверить другие упоминания «govulncheck не required / красный» вне `docs/CI.md`: `README.md`, `docs/MIGRATION_QUICK_START.md`, `SECURITY.md` (`grep -rn govulncheck`). Выровнять только фактически неверные.

## Finalization (`/end-task`)

- [ ] **F1.** BACKLOG: `PROD-DEPS-VULN` закрыть (ссылка на DONE); завести `GO-MOD-TIDY-CHECK` (research §6, ~0.1d); в `CI-SUPPLY-CHAIN` добавить пункт `schedule` для `govulncheck`; в `PROD-RELEASE-V010` снять оговорку «лучше после PROD-DEPS-VULN».
- [ ] **F2.** DONE.md: запись (что сделано, как проверено, осознанные ограничения: GO-2026-5932, `tidy` не делали, required не enforced до branch protection).
- [ ] **F3.** NEXT.md: снять из WIP, заметка; следующий прод-блокер — `PROD-RELEASE-V010`.
- [ ] **F4.** DECISIONS.md — не нужен (Spec «Scope»). Если на `/implement` понадобилось исключение из карантина, тогда нужен.
- [ ] **F5.** Архив `tasks/PROD-DEPS-VULN/` → `tasks/archive/PROD-DEPS-VULN/`; ветка не `main`; `git diff --check`.

## Результаты / отклонения

_(заполняется по ходу)_
