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

- [x] **I1. Апгрейд зависимостей (D1)**, в `go-app/`:
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
    golang.org/x/text@v0.41.0
  ```
  - `go mod tidy` **не** запускать.
  - Сверить итог с research §3B/§4: `go.mod` +21/−21, `go`/`toolchain` не изменились, x/text по MVS = v0.41.0. Если выбрана версия, которой нет в таблице §4, проверить её `Time` через `go list -m -json` (карантин ≤ 2026-09-22).
- [x] **I2. Быстрая проверка:** `go build ./...`, `go vet ./...`, `govulncheck@v1.8.0 ./...` → exit 0; `-show verbose`: package-level пусто, module-level — только GO-2026-5932.
- [x] **I3. Коммит 1** (D5): `fix(deps): upgrade grpc, otel, pgx and x/* to clear govulncheck findings`, только `go-app/go.mod` + `go-app/go.sum`. В теле — список GO-ID.
- [x] **I4. `.github/workflows/ci.yml` (D3)** — только комментарии:
  - шапка: `govulncheck` — в списке required, `e2e-ha` остаётся не required;
  - над job'ом `govulncheck`: вместо «Not required until PROD-DEPS-VULN…» — required; red означает достижимую уязвимость, лечится апгрейдом; module-level находки (GO-2026-5932) exit code не портят.
  - `actionlint` (`go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12`) — чисто.
- [x] **I5. `docs/CI.md` (D3, D4):**
  - таблица: `govulncheck` → `yes`, «Why» переписать: достижимая уязвимость = блокер релиза; компромисс — новая запись в vuln DB краснит и PR, который зависимости не трогал, лечится отдельным PR с апгрейдом;
  - абзац про branch protection: добавить `govulncheck` в список имён job'ов;
  - одна строка про GO-2026-5932 (ожидаема, из `x/crypto` используется только `bcrypt`, её покажут и сканеры образов);
  - прочие упоминания «govulncheck красный / PROD-DEPS-VULN» в `docs/CI.md` — найти (`grep -n`) и выровнять.
- [x] **I6. Коммит 2:** `ci: make govulncheck a required check` (`ci.yml` + `docs/CI.md`).

## Testing (`/write-tests`, `/testing`)

Новых тестов нет: поведение кода не меняется, регрессию ловят существующие тесты и сам `govulncheck` в required. На `/write-tests` зафиксировать это решение, тесты не выдумывать.

- [x] **T1.** `GOTOOLCHAIN=go1.26.8 ./scripts/release-gate.sh` → `RESULT: PASS` (все 10 шагов). Флейки перезапустить, записать ниже.
- [x] **T2.** pgx на реальном Postgres: `go test -count=1 -v ./internal/database/...` — `TestRunMigrations_ConcurrentReplicas_*`, `TestPostgresPool_Reload_*` = PASS, не SKIP.
- [x] **T3.** `deploy/e2e-ha/run.sh` локально — PASS. Это единственное покрытие repository-пути silencing: его unit-тесты — заглушки со `t.Skip`.
- [x] **T4.** Образы: `docker buildx build --platform linux/amd64,linux/arm64` для `Dockerfile` и `Dockerfile.config-reloader` (как job `images`, без push). Если локально нет multi-arch builder'а — хотя бы нативная архитектура, остальное — CI на PR.
- [ ] **T5.** _(отложен по решению пользователя: PR не открываем; первый прогон на GitHub — `ci.yml` на push в `main` после мержа)_ CI на PR: зелёные `gate`, `images (amp)`, `images (config-reloader)`, `actionlint`, `govulncheck`, `e2e-ha`.
- [x] **T6.** `git diff main --stat` — затронуты только файлы из Spec «Scope»; `git diff --check` чистый.

## Documentation (`/write-doc`)

- [x] **W1.** `CHANGELOG.md` `[Unreleased]`: новая секция `### Security` (её сейчас нет, поставить первой). `PROD-DEPS-VULN`: 9 reachable GO-ID + module-level fix, таблица «модуль: было → стало» для прямых зависимостей, `govulncheck` required, остаточный GO-2026-5932. Без breaking changes.
- [x] **W2.** Проверить другие упоминания «govulncheck не required / красный» вне `docs/CI.md`: `README.md`, `docs/MIGRATION_QUICK_START.md`, `SECURITY.md` (`grep -rn govulncheck`). Выровнять только фактически неверные.

## Finalization (`/end-task`)

- [x] **F1.** BACKLOG: `PROD-DEPS-VULN` закрыть (ссылка на DONE); завести `GO-MOD-TIDY-CHECK` (research §6, ~0.1d); в `CI-SUPPLY-CHAIN` добавить пункт `schedule` для `govulncheck`; в `PROD-RELEASE-V010` снять оговорку «лучше после PROD-DEPS-VULN».
- [x] **F2.** DONE.md: запись (что сделано, как проверено, осознанные ограничения: GO-2026-5932, `tidy` не делали, required не enforced до branch protection).
- [x] **F3.** NEXT.md: снять из WIP, заметка; следующий прод-блокер — `PROD-RELEASE-V010`.
- [x] **F4.** DECISIONS.md — не нужен (Spec «Scope»). Если на `/implement` понадобилось исключение из карантина, тогда нужен.
- [x] **F5.** Архив `tasks/PROD-DEPS-VULN/` → `tasks/archive/PROD-DEPS-VULN/`; ветка не `main`; `git diff --check`.

## Результаты / отклонения

**/implement (2026-09-29)**

- _Отклонение I1:_ `golang.org/x/text@v0.39.0` в явном `go get` конфликтует: `x/crypto@v0.56.0`, `x/net@v0.58.0` и `grpc@v1.83.2` требуют `x/text@v0.41.0`. Если версия указана явно, `go get` не даёт MVS поднять её выше. Взята v0.41.0: research предсказывал её как итог MVS, карантин она проходит (2026-08-11). В команде I1 версия исправлена.
- `go.mod` после `go get` **совпал с экспериментом research §3B построчно** (+21/−21, `go`/`toolchain` не изменились). В `go.sum` +46, в research было +62: там оставались записи промежуточных версий варианта A.
- I2: `go build`, `go vet` — OK; `govulncheck@v1.8.0 ./...` → exit 0, «affected by 0 vulnerabilities»; `-show verbose`: Symbol и Package пусто, Module — только GO-2026-5932; `go mod verify` — all modules verified.
- I4: `actionlint@v1.7.12` — чисто.
- I5: прочих упоминаний «govulncheck красный» в `docs/CI.md` не осталось. Вне `docs/CI.md`: запись PROD-CI-IMAGES в `CHANGELOG.md` `[Unreleased]` (строка 19) говорит «govulncheck is red… (`PROD-DEPS-VULN`)», правится на W1. `SECURITY.md:131` упоминает govulncheck нейтрально, править не нужно.
- Коммиты: `4def6ad` (`fix(deps)`), `90adada` (`ci:`).
- Замечено, вне скоупа: `docs/CI.md` «Go Version» утверждает, что локальный Go с `GOTOOLCHAIN=auto` скачает `go1.26.8`. Это верно, только если локальный Go старше. Более новый локальный (1.27.1 на этой машине) используется как есть, и тогда `govulncheck` проверяет stdlib другой версии, не той, что в CI. Решить на `/write-doc`: одна фраза или follow-up.

**/write-tests (2026-09-29)** — отдельно не запускался. Решение из плана в силе: новых тестов нет, поведение кода не менялось (0 строк Go). Регрессию ловят существующие тесты (ниже) и `govulncheck` в required.

**/testing (2026-09-29)**, HEAD `bd11794`, `GOTOOLCHAIN=go1.26.8`, Docker (OrbStack) поднят.

Зелёные:
- T1 `scripts/release-gate.sh`: 9 из 10 шагов PASS с первого прогона (build 12s, lint 52s, test 90s, futureparity 20s, helm-deps/dev/production/rbac, amtool-compat 105s). Шаг `race` — см. «Красные». Повторный прогон шага `race` (тот же `go test -race -count=1` по тем же 7 пакетам) — все `ok`, exit 0.
- T2 pgx v5.9.2 на реальном Postgres (testcontainers): `TestRunMigrations_ConcurrentReplicas_FreshDB`, `TestPostgresPool_Reload_{NoInFlightQueryLoss,FailedVerificationKeepsOldPool,RefusesWhenHandleShared,RejectsInvalidConfig}` — PASS, без SKIP.
- T3 `deploy/e2e-ha/run.sh` (образ AMP собирается compose'ом из текущего дерева): `ALL PASS`, 7 проверок, 1:33. Кластер `ready` с 2 peers, ровно одна публикация на группу, failover и adoption таймера.
- T4 `docker buildx build --platform linux/amd64,linux/arm64`: `Dockerfile` и `Dockerfile.config-reloader` — exit 0. У `Dockerfile` arm64-слой `go build` в первом прогоне был `CACHED` (кэш от образа smoke-стенда из шага `amtool-compat`), поэтому `Dockerfile` пересобран с `--no-cache`: `go mod download` и оба `go build` выполнены заново, exit 0.
- T6 `git diff main --stat` — только файлы из Spec «Scope» (+ task workspace и `NEXT.md`); `git diff main --check` — чисто.
- `govulncheck` / `actionlint` — см. /implement выше.

Красные — только предсуществующее:
- T1, шаг `race`: `TestDefaultTimerManager_TwoReplicasRaceSameGroupTimer_OnlyLockWinnerFires` (`distributed_timer_ownership_test.go:169`, «expected 1, actual 2») — это `GROUPING-TIMER-LOCK-RELEASED-BEFORE-LOSER` из BUGS.md, симптом тот же. Отделено от апгрейда замером `go test -race -count=30 -run '^…OnlyLockWinnerFires$' ./internal/infrastructure/grouping/`: ветка `bd11794` — **0/30**, чистый `main` `9f54424` — **2/30** (как в BUGS.md: 2/30 на `8bca192`). Апгрейд пакет `grouping` не затрагивает (redis/miniredis не поднимались). Не регресс, не чинили (скоуп — `GROUPING-TIMER-LOCK-FIX`).

Не проверено:
- T5 CI на PR — **отложен по решению пользователя (2026-09-29): PR не открываем.** Jobs `gate`/`images`/`actionlint`/`govulncheck`/`e2e-ha` проверены только локальными эквивалентами (macOS, OrbStack). Первый прогон на GitHub-раннере будет на push в `main` после `/merge-to-main`: `ci.yml` срабатывает на `push: branches: [main]`, если `main` запушат. Остаточный риск: `govulncheck` в CI ещё ни разу не был зелёным, а Linux-раннер и актуальная на момент прогона vuln DB могут дать другой результат. Проверить после мержа и push.

**/write-doc (2026-09-29)**

- W1 `CHANGELOG.md` `[Unreleased]`: новая `### Security` (первой) — `PROD-DEPS-VULN`: 9 reachable GO-ID по модулям, отдельно unreachable (GO-2026-6443, GO-2026-5942, 16 в `x/crypto`), версии было → стало, остаточный GO-2026-5932, `govulncheck` required. Запись PROD-CI-IMAGES в `### Added` выровнена: govulncheck больше не «red», стал required.
- В research/Spec было «17 module-level находок в `x/crypto`». Из них фикс есть у 16, 17-я — сам GO-2026-5932. В CHANGELOG — 16, в Spec D1 уточнено.
- `docs/CI.md` «Go Version»: замечание с /implement исправлено одной фразой. `GOTOOLCHAIN=auto` скачивает `go1.26.8`, только если локальный Go старше. Более новый используется как есть, поэтому проверки гнать с `GOTOOLCHAIN=go1.26.8` (stdlib-находки `govulncheck` зависят от версии Go).
- W2: вне `docs/CI.md`/`CHANGELOG.md` govulncheck упоминается только в `SECURITY.md:130` («Static analysis with gosec, govulncheck»). После задачи это правда для govulncheck (каждый PR/push в `main`). Соседние пункты раздела («security scans on every commit», gosec) — декларативные, их переписывает отдельная `PROD-SECURITY-MD` (BACKLOG), не трогали. `README.md`, `docs/MIGRATION_QUICK_START.md` govulncheck не упоминают.
- CI по решению пользователя в задаче не проверяется совсем (2026-09-29: «обойдемся без CI»). Состояние CI-пайплайна пользователь считает непроверенным, T5 остаётся открытым.

## Итоговый статус (/end-task, 2026-09-29)

**DONE.** Критерии приёмки Spec:
- [x] версии ≥ D1, `go`/`toolchain` не изменились, tidy-дрейф не тронут;
- [x] `govulncheck` exit 0, 0 reachable; verbose — package пусто, module — только GO-2026-5932;
- [~] release-gate — PASS после повторного прогона шага `race` (первый прогон красный на предсуществующем флейке, см. /testing);
- [ ] ~~jobs на PR~~ — не проверялось: CI в задаче исключён решением пользователя;
- [x] `docs/CI.md` и шапка `ci.yml` согласованы (5 required);
- [x] CHANGELOG `### Security`; BACKLOG: `PROD-DEPS-VULN` закрыт, `GO-MOD-TIDY-CHECK` заведён, `schedule` добавлен в `CI-SUPPLY-CHAIN`.

Ограничения: CI на GitHub не прогонялся; required не enforced до branch protection; GO-2026-5932 остаётся (фикса нет); `go.mod` не tidy (`GO-MOD-TIDY-CHECK`); DECISIONS не менялся (исключений из карантина нет).
