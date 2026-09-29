# Spec: PROD-DEPS-VULN

Дата: 2026-09-29. Ветка `bugfix/prod-deps-vuln`. Источники: `requirements.md`, `research.md`. Пути — от корня репозитория.

## Проблема

`govulncheck@v1.8.0 ./...` в `go-app` находит 9 достижимых уязвимостей в 6 модулях: grpc, otel, otel/sdk, x/net, x/text, pgx/v5 (research §1). Поэтому job `govulncheck` в CI красный и не входит в required checks (ADR-013). Первый релиз (`PROD-RELEASE-V010`) вышел бы с известными уязвимостями. Кроме того, в модулях есть ещё 19 недостижимых уязвимостей с доступным фиксом, и сканеры образов их тоже покажут (research §3).

## Цели

1. 0 достижимых и 0 package-level находок `govulncheck` при неизменных `go`/`toolchain`.
2. На уровне модулей остаётся только GO-2026-5932 (фикса нет), причина задокументирована.
3. `govulncheck` — required check.
4. Без правок Go-кода; release-gate, образы и `e2e-ha` зелёные.

## Не-цели

- `go mod tidy` и чистка неиспользуемых модулей (`go-sqlite3`, `ulid`, `cobra`): предсуществующий дрейф, research §6. Уходит в BACKLOG `GO-MOD-TIDY-CHECK`.
- Запуск `govulncheck` по расписанию, Dependabot/Renovate. Это `CI-SUPPLY-CHAIN`, туда добавить пункт про `schedule`.
- Апгрейды, не нужные для фиксов (`go get -u ./...`, k8s, testcontainers, viper и т.д.).
- Смена `go` directive, `toolchain`, базовых образов и `setup-go`.
- Включение branch protection. Его делает владелец репозитория.

## Ключевые решения

### D1. Вариант B: закрыть и достижимые, и недостижимые находки с доступным фиксом

Целевые версии (явный `go get`):

| Модуль | Было | Стало | Зачем |
|---|---|---|---|
| `google.golang.org/grpc` | v1.77.0 | **v1.83.2** | GO-2026-6348, -6061 (reachable), GO-2026-6443 (package) |
| `go.opentelemetry.io/otel` + `sdk`, `trace`, `exporters/otlp/otlptrace`, `…/otlptracegrpc` | v1.39.0 | **v1.44.0** | GO-2026-5506, -5426, -4394. v1.44.0, а не v1.43, потому что его требует grpc ≥ 1.83.1 |
| `github.com/jackc/pgx/v5` | v5.7.6 | **v5.9.2** | GO-2026-5004 |
| `golang.org/x/crypto` | v0.44.0 | **v0.56.0** | 17 module-level находок |
| `golang.org/x/net` | v0.47.0 | **v0.58.0** | GO-2026-5026, -4918, -5942; минимум для grpc v1.83.2 |
| `golang.org/x/text` | v0.31.0 | ≥ v0.39.0 (по MVS выйдет v0.41.0) | GO-2026-5970 |

Остальное поднимает MVS транзитивно (`x/sys`, `x/sync`, `x/term`, `x/oauth2`, genproto, `protobuf`, `grpc-gateway/v2`, `otel/metric`, `otel/proto/otlp`, `otelhttp`). Итог — 21 модуль (research §3B). После `go get` **не запускать `go mod tidy`**: иначе в дифф попадёт посторонний дрейф.

### D2. Карантин 7 дней

Все целевые и транзитивные версии опубликованы не позже 2026-09-02 (research §4). Исключения не нужны. На `/implement` проверить заново только в одном случае: если MVS выберет версию, которой нет в таблице research §4.

### D3. `govulncheck` required без `schedule`

Job без изменений, меняется только статус:
- `.github/workflows/ci.yml`: комментарий в шапке (список required) и над job'ом. Убрать фразу «Not required until PROD-DEPS-VULN…».
- `docs/CI.md`: в таблице `Required = yes`, обоснование переписать; job добавить в список имён для branch protection.

Осознанный компромисс: новая запись в vuln DB про достижимый код покраснит любой PR, даже если он не трогает зависимости. Так и задумано: достижимая уязвимость блокирует релиз. Лечится отдельным PR с апгрейдом. Написать это в `docs/CI.md`.

### D4. Остаточный GO-2026-5932

`govulncheck` без `-show verbose` падает только на symbol-level находках, модульные он лишь перечисляет. GO-2026-5932 (`x/crypto/openpgp` заброшен, фикса нет) exit code не портит. Подавлять нечем и незачем. В `docs/CI.md` одна строка: находка ожидаема, из `x/crypto` используется только `bcrypt`. Сканеры образов её тоже покажут.

### D5. Коммиты

1. `fix(deps): upgrade grpc, otel, pgx and x/* to clear govulncheck findings`: только `go-app/go.mod` + `go-app/go.sum`.
2. `ci: make govulncheck a required check`: `ci.yml` (только комментарии) + `docs/CI.md`.
3. Доки/планирование — на `/write-doc` и `/end-task`: CHANGELOG `[Unreleased]` → `### Security`, BACKLOG, DONE.

## Scope (файлы)

- `go-app/go.mod`, `go-app/go.sum`
- `.github/workflows/ci.yml` (комментарии)
- `docs/CI.md`
- `CHANGELOG.md`
- `docs/06-planning/{BACKLOG,NEXT,DONE}.md`; `DECISIONS.md` — не нужен (ADR-013 предусматривал этот переход, исключений из карантина нет)

## Критерии приёмки

- [ ] Версии в `go.mod` ≥ таблицы D1; `go`/`toolchain` не изменились; `git diff` не трогает строки, которые поменял бы только `tidy`.
- [ ] `GOTOOLCHAIN=go1.26.8 go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...` → exit 0, «affected by 0 vulnerabilities».
- [ ] `-show verbose`: package-level — пусто, module-level — только GO-2026-5932.
- [ ] `scripts/release-gate.sh` — PASS локально.
- [ ] На PR ветки зелёные `gate`, `images (amp)`, `images (config-reloader)`, `actionlint`, `govulncheck`, `e2e-ha`.
- [ ] `docs/CI.md` и шапка `ci.yml` согласованы: пять required checks.
- [ ] CHANGELOG `### Security` с перечнем GO-ID; BACKLOG: `PROD-DEPS-VULN` закрыт, заведён `GO-MOD-TIDY-CHECK`, в `CI-SUPPLY-CHAIN` добавлен `schedule` для govulncheck.

## Риски

- **Поведенческий регресс в pgx 5.7→5.9 / otel 1.39→1.44 без ошибок компиляции.** Митигация: тесты на реальном Postgres (testcontainers) уже зелёные (research §5), плюс `e2e-ha` на PR. Остаток риска: repository-тесты silencing — заглушки со `t.Skip`, их путь покрыт только e2e.
- **otelhttp 0.49→0.61** — большой скачок, но он indirect и живёт только в тестовой ветке (`docker/docker/client` ← testcontainers). В прод-бинарник не попадает.
- **Новые advisory до мержа.** Если между research и PR выйдет запись про поднятые версии, `govulncheck` на PR покраснеет. Тогда поднять минимально и перепроверить карантин.
- **Флейки** `GROUPING-TIMER-LOCK-RELEASED-BEFORE-LOSER` (-race) и `PUBLISHING-WARMUP-TEST-FLAKY` могут покраснить `gate`. Перезапустить и записать в testing-отчёт, в этой задаче не чинить.
