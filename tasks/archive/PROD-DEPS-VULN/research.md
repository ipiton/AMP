# Research: PROD-DEPS-VULN

Дата: 2026-09-29. Эксперименты — в отдельном detached worktree от `d64eba3` (удалён после research), toolchain принудительно `GOTOOLCHAIN=go1.26.8` (как `toolchain` в `go-app/go.mod` и `go-version-file` в CI; локальный `go` — 1.27.1, без форса результаты могли бы разойтись с CI).

## 1. Исходное состояние (подтверждено)

`govulncheck@v1.8.0 ./...` в `go-app` на `main` — **9 достижимых уязвимостей в 6 модулях**, ровно как в BACKLOG. Уточнение по fix-версиям (отчёт даёт минимальный фикс на каждую находку отдельно):

| Находка | Модуль@сейчас | Fix ≥ |
|---|---|---|
| GO-2026-6348 | grpc@v1.77.0 | v1.83.1 |
| GO-2026-6061 | grpc@v1.77.0 | v1.82.1 |
| GO-2026-5970 | x/text@v0.31.0 | v0.39.0 |
| GO-2026-5506 | otel@v1.39.0 | v1.41.0 |
| GO-2026-5426 | otel/sdk@v1.39.0 | v1.43.0 |
| GO-2026-4394 | otel/sdk@v1.39.0 | v1.40.0 |
| GO-2026-5026 | x/net@v0.47.0 | v0.55.0 |
| GO-2026-4918 | x/net@v0.47.0 | v0.53.0 |
| GO-2026-5004 | pgx/v5@v5.7.6 | v5.9.2 |

Использование в коде: grpc — прямой импорт в `pkg/telemetry/tracer.go` (в `go.mod` помечен `// indirect` — см. §5), otel — `pkg/telemetry`, pgx — `internal/database/postgres`, `internal/infrastructure/*`, `internal/config/reload*`; x/net, x/text — транзитивно.

## 2. Ограничения совместимости, найденные экспериментом

- 🔴 **grpc v1.83.1 требует `go.opentelemetry.io/otel` ≥ v1.44.0** (`go get … otel@v1.43.0` падает: `grpc@v1.83.1 requires otel@v1.44.0`). Поэтому минимальный otel не 1.43, а **1.44.0**, и все otel-модули поднимаются согласованно (`otel`, `sdk`, `trace`, `metric`, `exporters/otlp/otlptrace`, `…/otlptracegrpc`).
- `x/crypto@v0.56.0` требует `x/net` ≥ v0.57.0; `grpc@v1.83.2` требует `x/net` ≥ v0.58.0.
- **`go` directive (`1.26.0`) и `toolchain` (`go1.26.8`) не меняются** ни в одном варианте: максимум среди требований — `go 1.26.0` (`x/crypto@v0.56.0`). Dockerfile'ы и `setup-go` не трогаем.

## 3. Варианты

### A. Минимальный — только достижимые находки

`go get grpc@v1.83.1 otel{,/sdk,/trace,/exporters/otlp/otlptrace{,/otlptracegrpc}}@v1.44.0 x/text@v0.39.0 x/net@v0.55.0 pgx/v5@v5.9.2`

Транзитивно поднимаются: `x/crypto` 0.44→0.51, `x/sys` 0.39→0.45, `x/sync`, `x/term`, `x/oauth2` 0.32→0.36, genproto (→ `20260526…`), `protobuf` 1.36.10→1.36.11, `grpc-gateway/v2` 2.27.3→2.29.0, `otel/proto/otlp` 1.9→1.10, `otelhttp` 0.49→0.61 (indirect, тянется из `docker/docker/client` ← testcontainers).

Результат: build + vet зелёные **без правок Go-кода**, `govulncheck` — 0 достижимых. Но `-show verbose` показывает **19 известных недостижимых**: 1 на уровне пакета (GO-2026-6443, grpc@v1.83.1, fix v1.83.2) и 18 на уровне модуля (17 в `x/crypto@v0.51.0`, fix v0.52–v0.56; GO-2026-5942 в `x/net@v0.55.0`, fix v0.56.0; GO-2026-5932 без фикса).

### B. A + закрыть и недостижимые с доступным фиксом (рекомендуется)

Сверх A: `grpc@v1.83.2 x/crypto@v0.56.0 x/net@v0.58.0` (доп. транзитивка: `x/text`→0.41, `x/sys`→0.47, `x/term`→0.45, `x/sync`→0.22).

Результат: build + vet зелёные без правок кода, `govulncheck` — **0 достижимых, 0 на уровне пакетов**, на уровне модулей остаётся одна находка — **GO-2026-5932** (`x/crypto/openpgp`: пакет заброшен и небезопасен по дизайну, **фикса нет и не будет**). AMP импортирует из `x/crypto` только `bcrypt` ⇒ это шум, не риск. В позиции модуля он будет всегда, пока жив `x/crypto`.

Дифф: `go.mod` +21/−21, `go.sum` +62. Поднимается 21 модуль, из них 8 прямых: pgx, 5× otel, x/crypto, x/text.

### C. «Всё до свежего» (`go get -u ./...`)

Отвергнуто: противоречит ограничению про минимальный дифф в requirements, тащит мажорные апгрейды (k8s 0.29, testcontainers, viper…), ради которых нет причин. Кроме того, часть свежих версий моложе 7 дней (grpc v1.86.0-dev, otel v1.47.0-rc.1).

**Почему B, а не A:** первый публичный образ (`PROD-RELEASE-V010`) проверят сканерами образов (Trivy/Grype), а они считают уязвимости по модулям, а не по достижимости. На варианте A сканер нашёл бы ~19 CVE в только что выпущенном релизе. Разница с A — три `go get` и ноль правок кода.

## 4. Карантин 7 дней (граница — опубликовано ≤ 2026-09-22)

Возраст — по `go list -m -json <mod>@<ver>` → `Time` (прокси). Все версии варианта B проходят:

| Модуль@версия | Опубликовано |
|---|---|
| grpc@v1.83.2 | 2026-08-25 |
| otel*@v1.44.0 | 2026-05-27 |
| pgx/v5@v5.9.2 | 2026-04-19 |
| x/crypto@v0.56.0 | 2026-09-02 (самый молодой, 27 дней) |
| x/net@v0.58.0 | 2026-08-12 |
| x/text@v0.41.0 | 2026-08-11 |
| x/sys@v0.47.0 / x/sync@v0.22.0 / x/term@v0.45.0 | 2026-06-30 / 07-01 / 07-08 |
| grpc-gateway/v2@v2.29.0 | 2026-04-15 |
| otel/proto/otlp@v1.10.0 | 2026-03-09 |
| x/oauth2@v0.36.0 | 2026-02-11 |
| protobuf@v1.36.11 | 2025-12-12 |
| otelhttp@v0.61.0 | 2025-05-22 |

genproto — pseudo-version `20260526…` (май). Исключений из карантина не нужно ⇒ записи в DECISIONS по карантину не будет.

## 5. Проверки варианта B (в worktree, `GOTOOLCHAIN=go1.26.8`)

- `go build ./...`, `go vet ./...` — OK.
- `scripts/release-gate.sh` — **RESULT: PASS**. Все 10 шагов: build, lint, test, futureparity, race (278 с, без флейков), helm-deps/dev/production/rbac, amtool-compat.
- pgx 5.7→5.9 на реальном Postgres (testcontainers, Docker поднят): `TestRunMigrations_ConcurrentReplicas_FreshDB`, `TestPostgresPool_Reload_*` — PASS. Оговорка: тесты `internal/infrastructure/silencing/postgres_silence_repository_test.go` — заглушки с безусловным `t.Skip("Requires database connection…")` (предсуществующее, от апгрейда не зависит). Покрытие pgx-пути repository-слоя поэтому тонкое; на `/testing` стоит прогнать `deploy/e2e-ha` (у него настоящая HA-топология).
- Не проверено в research: сборка образов (`docker buildx`, job `images`) и `e2e-ha` — это на `/testing`.

## 6. Попутные находки (вне скоупа)

- **`go.mod` на `main` не tidy.** `go mod tidy` без каких-либо апгрейдов меняет файл: убирает неиспользуемые `mattn/go-sqlite3`, `oklog/ulid/v2`, `spf13/cobra` (+ `inconshreveable/mousetrap`), переводит в direct `google.golang.org/grpc`, `docker/docker`, `prometheus/client_model`. CI это не ловит. В этой задаче tidy **не делаем**: дифф смешался бы с security-апгрейдом. Предлагаю завести в BACKLOG `GO-MOD-TIDY-CHECK` (tidy + шаг `go mod tidy -diff` в release-gate, ~0.1d). Следствие для реализации: использовать `go get` без последующего `go mod tidy`.
- **govulncheck запускается только на PR и push в `main`** (`ci.yml`: `on: pull_request, push`, без `schedule`). Новая уязвимость в уже влитых зависимостях обнаружится только на следующем PR, и если check required, этот PR станет красным по чужой причине. См. §7.

## 7. Что решить на `/spec`

1. **Вариант апгрейда:** B (рекомендация) или A.
2. **Как сделать govulncheck required.** Проблема: required check с внешним, меняющимся во времени источником (vuln DB) может покраснеть на PR, который зависимости не трогал. Варианты:
   - (a) просто required, как в BACKLOG. Честно: новая достижимая уязвимость и есть блокер релиза; снимается отдельным PR с апгрейдом.
   - (b) (a) + `schedule:` (например, раз в сутки) для `govulncheck`, чтобы уязвимость находилась до того, как сломает чей-то PR. +3 строки в `ci.yml`, но это расширение скоупа (`CI-SUPPLY-CHAIN` про Dependabot рядом).
   - Рекомендация: (a) в этой задаче, `schedule` — в `CI-SUPPLY-CHAIN` или однострочным follow-up.
3. **Остаточный GO-2026-5932** — описать в `docs/CI.md`: `govulncheck` по умолчанию падает только на достижимых (symbol-level) находках, модульные отчёты информативны. Подавлять не нужно и нечем (у govulncheck v1.8.0 нет allowlist).
4. Коммиты: апгрейд одним коммитом `fix(deps): …` (правок кода нет), доки (`docs/CI.md`, комментарий в `ci.yml`, CHANGELOG `Security`) — отдельным.

## Вывод: изменился ли скоуп

Скоуп **сузился по коду и немного расширился по версиям**:

- правок Go-кода не требуется (риск «pgx/otel/grpc ломают API» не подтвердился);
- otel поднимается до 1.44.0, а не 1.43 (требование grpc); плюс рекомендуются grpc 1.83.2, x/crypto 0.56, x/net 0.58 для чистого модульного отчёта;
- исключения из карантина не нужны;
- смена go/toolchain не нужна.

Оценка падает с ~0.5–1d до **~0.25–0.5d**. Основное время уйдёт на `/testing`: CI на PR, `images`, `e2e-ha`.
