---
id: PROD-CONFIG-FILE-FALLBACK
slug: prod-config-file-fallback
stream: production-readiness
type: bug
status: draft
created_at: 2026-10-07
updated_at: 2026-10-07
based_on:
  - requirements.md
  - research.md
---

# Specification: отсутствие файла конфига не должно выбрасывать env

**Version:** 1.0  
**Status:** Draft

## Summary

Когда файла конфига по умолчанию (`./config.yaml`) нет, `LoadConfig` собирает конфиг из дефолтов viper и env. Любая другая ошибка загрузки, а также отсутствие файла, путь к которому задан явно через `AMP_CONFIG_FILE`, останавливают процесс. Минимальный фолбэк `Config{Server: {Port: 9093}}` удаляется. Дефолты чарта, которые не проходят `Validate`, в эту задачу не входят (вариант A из `research.md`).

## Requirements Coverage

| Requirement / Criterion | Covered by |
|---|---|
| Отсутствующий файл — не ошибка, конфиг из env + дефолтов | Target Design п. 1–2; тест `TestLoadConfig_MissingFile_UsesEnv` |
| Любая другая ошибка — выход, фолбэк удалён | Target Design п. 3; Edge Cases 3–6; тесты битого YAML и невалидного файла |
| Ключи env чарта доходят до `Config` | Premise P4; тест `TestChartEnvKeysKnownToViper` |
| Unit-тесты «нет файла + env», «битый/невалидный файл» | Component Architecture → `config_load_test.go` |
| Render/smoke с `configFile.enabled: false` | `TestChartEnvKeysKnownToViper` проверяет env из рендера дефолтного чарта (`configFile.enabled: false`) на уровне загрузки конфига. Живой старт дефолтного чарта невозможен до `HELM-DEFAULTS-VALIDATE` (`research.md` § Findings), формулировка критерия обновлена в `requirements.md` |
| Тесты/e2e не полагаются на фолбэк; обходы пересмотрены | Premises P5–P7; Component Architecture |
| CHANGELOG + migration note, `BUGS.md` | Rollout; Component Architecture → docs |

## Current State

- **Code:**
  - `go-app/internal/config/config.go:646-680` — `LoadConfig` ждёт `viper.ConfigFileNotFoundError`, но при `SetConfigFile` получает `*fs.PathError` и возвращает ошибку.
  - `go-app/cmd/server/main.go:55-62` — на любую ошибку подставляет минимальный конфиг, `:73-76` — латает `UnauthenticatedPaths` после фолбэка, `:297-303` — `resolveRuntimeConfigPath`.
  - `go-app/cmd/server/futureparity_compat.go:122-127` — свой фолбэк в тестовом harness'е.
- **Data:** not applicable.
- **Tests:**
  - тесты `internal/config` грузят существующие временные файлы, отсутствующий файл не проверяется;
  - `cmd/server/futureparity_compat_test.go` и `route_prefix_integration_test.go` идут через harness;
  - smoke и e2e-ha монтируют `config.yaml`.
- **Docs:** `docs/ALERTMANAGER_COMPATIBILITY.md:869` (Known Gaps п. 15), `docs/06-planning/BUGS.md` § `CONFIG-MISSING-FILE-DROPS-ENV`, `CHANGELOG.md` `[Unreleased]`.

## Design Premises

| # | Premise | Confirmed by | Class | If wrong |
|---|---|---|---|---|
| P1 | viper v1.21 при `SetConfigFile` + отсутствующем файле возвращает ошибку, для которой `errors.Is(err, fs.ErrNotExist)` истинно | `viper.go:1518-1533` (`afero.ReadFile` → `*fs.PathError`); воспроизведение в `evidence/probe-results.md` | measured | фикс не срабатывает — ловит тест «нет файла» |
| P2 | `LoadConfig` вызывают только `main.go:56`, `futureparity_compat.go:123`, `reload_coordinator.go:365` и тесты | `grep -rn LoadConfig(` по `go-app/` | call-path-traced | неучтённый вызов получит новую семантику без проверки |
| P3 | Hot-reload без файла по-прежнему отказывает: `loadAndParse` сначала делает `os.ReadFile` (`reload_coordinator.go:359`) и возвращает ошибку до `LoadConfig` | чтение кода | code-read | reload без файла начал бы «успешно» перегружать конфиг из env — проверяется тестом reload, если он есть, иначе остаётся `code-read` |
| P4 | Все env-имена контейнера `amp` в дефолтном рендере, кроме `SERVICE_NAME`/`SERVICE_VERSION`, соответствуют ключам viper с `SetDefault` | probe по рендеру чарта (`evidence/probe-results.md`) | measured | часть env молча не дойдёт; тест P4 закрепляет это |
| P5 | Дефолты viper без env проходят `Validate` (профиль `standard`, `development`) | probe `LoadConfigFromEnv()` с пустым окружением → `<nil>` (2026-10-07) | measured | harness `futureparity` и голый бинарь без env упадут |
| P6 | Smoke и e2e-ha не полагаются на фолбэк: задают `AMP_CONFIG_FILE` и монтируют файл | `deploy/smoke/docker-compose.yml:37`, `deploy/e2e-ha/docker-compose.yml:40,57` | code-read | CI-сценарий падает на старте — проверяется прогоном release-gate |
| P7 | `server.web_config_file` и `server.auth.unauthenticated_paths` имеют `SetDefault` (`config.go:815-816`), значит, доходят из env/дефолтов | чтение кода; probe показал `UnauthenticatedPaths: ["/-/healthy","/-/ready"]` | measured | без патча в `main.go:73-76` пути probe окажутся под auth |
| P8 | Чарт задаёт `AMP_CONFIG_FILE` только при `configFile.enabled: true`, и путь указывает на смонтированный файл | `helm/amp/templates/deployment.yaml:162-166` | code-read | явный путь без файла станет фатальным для этой конфигурации — что и задумано |

Premise со статусом `assumed` нет.

## Target Design

1. **`LoadConfig`.** Если `ReadInConfig` вернул ошибку и `errors.Is(err, fs.ErrNotExist)` истинно, загрузка продолжается с дефолтами и env. Мёртвую проверку `ConfigFileNotFoundError` заменить. Остальные ошибки чтения (права доступа, YAML) возвращаются как раньше. Сигнатура не меняется.
2. **`main.go`: явный и дефолтный путь.** `resolveRuntimeConfigPath` начинает сообщать, задан ли путь явно (`AMP_CONFIG_FILE`). Если путь явный и файла нет, это ошибка: `config file <path> from AMP_CONFIG_FILE does not exist`, exit 1. Причина: явно указанный, но отсутствующий файл почти всегда означает ошибку монтирования, и старт на env-дефолтах без маршрутов, ресиверов и auth — это fail-open. Если путь дефолтный (`./config.yaml`) и файла нет — INFO `no config file, using environment and defaults`, с путём.
3. **`main.go`: ошибка загрузки.** Любая ошибка `LoadConfig` → `slog.Error("failed to load configuration", "path", …, "error", err)` и `os.Exit(1)`. Минимальный фолбэк удалить. Патч `UnauthenticatedPaths` (`:73-76`) удалить — дефолт приходит из viper (P7).
4. **Тестовый harness `futureparity`** не меняется: его фолбэк — тестовый код, а после фикса без `config.yaml` в cwd он получит дефолты viper и затем свои переопределения (P5). Достаточно зелёного прогона `cmd/server`.
5. **`resolveWebConfigFile`** не трогать: прямое чтение `SERVER_WEB_CONFIG_FILE` после фикса избыточно (P7), но безвредно, а это путь auth. Упрощение — вне задачи.

Проверка существования явного пути делается в `main` через `os.Stat` до `LoadConfig`. Гонка между `Stat` и чтением файла не важна: если файл исчезнет в этот промежуток, `LoadConfig` молча соберёт конфиг из env, а это то же поведение, что и для дефолтного пути.

Вся задача в пяти предложениях. Viper сообщает об отсутствии файла через `fs.ErrNotExist`, а `LoadConfig` ждал другой тип ошибки, поэтому код считал отсутствие файла ошибкой. `main` на любую ошибку подставлял пустой конфиг и терял env, а заодно и настоящие ошибки валидации. После фикса отсутствие файла по дефолтному пути означает «конфиг из env», явный путь без файла и любая ошибка разбора или валидации — остановку процесса. Обход в `main` для auth-путей удаляется, тестовый harness не меняется. Дефолты чарта, которые после этого начнут падать на валидации, уходят в отдельную P0-задачу.

## API Contracts

Публичного API нет. Меняется контракт старта процесса:

| Ситуация | Было | Стало |
|---|---|---|
| `AMP_CONFIG_FILE` не задан, `./config.yaml` нет | WARN, минимальный конфиг, env потерян | INFO, конфиг из env + дефолтов |
| `AMP_CONFIG_FILE=<path>`, файла нет | WARN, минимальный конфиг | ERROR, exit 1 |
| файл не читается (права) | WARN, минимальный конфиг | ERROR, exit 1 |
| битый YAML / unmarshal | WARN, минимальный конфиг | ERROR, exit 1 |
| `Validate` / inhibition не прошли | WARN, минимальный конфиг (auth, маршруты потеряны) | ERROR, exit 1 |
| hot-reload без файла | ошибка reload, старый конфиг остаётся | без изменений (P3) |

## Data Model / Migrations

Not applicable.

## Component Architecture

- `go-app/internal/config/config.go` — `LoadConfig`: `errors.Is(err, fs.ErrNotExist)` вместо проверки `ConfigFileNotFoundError`.
- `go-app/cmd/server/main.go` — `resolveRuntimeConfigPath` возвращает `(path string, explicit bool)`; проверка явного пути; exit 1 при ошибке; удаление фолбэка и патча `UnauthenticatedPaths`. Второй вызов `resolveRuntimeConfigPath` в `futureparity_compat.go:123` адаптировать к новой сигнатуре.
- `go-app/internal/config/config_load_test.go` (новый) — тесты:
  - `TestLoadConfig_MissingFile_UsesEnv`: нет файла + `PROFILE`, `SERVER_PORT`, `GROUPING_ENABLED` → значения применены;
  - `TestLoadConfig_MalformedYAML_ReturnsError`;
  - `TestLoadConfig_InvalidConfig_ReturnsError`, например `server.external_url` не URL;
  - `TestLoadConfig_UnreadableFile_ReturnsError` — `chmod 000`, `t.Skip` под root.
- `go-app/cmd/server/main_test.go` (или текущий файл тестов `main`) — тест на `resolveRuntimeConfigPath` и проверку явного пути. Если проверка вынесена в маленькую функцию (`checkExplicitConfigPath`) — тест на неё; `os.Exit` не тестируется.
- `TestChartEnvKeysKnownToViper` — тест P4. Вариант реализации выбирает `plan-task`:
  - (a) Go-тест в `internal/config` со списком env-имён, извлечённых из `helm/amp/templates/{deployment,configmap}.yaml` regex'ом по `- name: X` и `^X:`;
  - (b) скрипт `helm/amp/tests/render-env-keys.sh` + Go-хелпер.
  
  Предпочтительно (a): без `helm` в Go-тестах, и тест ловит новые env-имена, добавленные в шаблоны.
- Docs:
  - `docs/ALERTMANAGER_COMPATIBILITY.md` п. 15 — переписать: файл больше не обязателен, невалидный конфиг фатален;
  - `CHANGELOG.md` `[Unreleased]` — `### Fixed` и migration note;
  - `BUGS.md` — закрыть `CONFIG-MISSING-FILE-DROPS-ENV`, завести `HELM-DEFAULTS-FAIL-VALIDATION`;
  - `BACKLOG.md` — P0 `HELM-DEFAULTS-VALIDATE` сразу после этой задачи;
  - `helm/amp/README.md` — убрать требование `configFile.enabled: true`, если оно есть, и добавить предупреждение про `HELM-DEFAULTS-VALIDATE`.

## Security Design

- [x] Ownership validation — not applicable.
- [x] Input validation — `Validate` остаётся как есть, но её ошибка теперь фатальна (fail-closed). Ни одна ошибка конфигурации не запускает процесс без `server.auth`/web config.
- [x] Sensitive data not logged — ошибки `Validate` логируются целиком. Проверено: сообщения с `%s/%q/%v` по полям-секретам печатают только элемент списка слабых паролей (`config.go:1396`) и `server.external_url` (`config.go:1069`), реальных секретов нет. Env-значения не логируются.
- [x] Rate limiting — not applicable.
- [x] Auth/RBAC path — `resolveWebConfigFile` не меняется. Удаление патча `UnauthenticatedPaths` безопасно по P7 — проверяется тестом, что после `LoadConfig` без файла список не пуст.

## Invariants

- [ ] Конфиг из существующего валидного файла загружается так же, как до фикса: тесты `internal/config` зелёные без изменений.
- [ ] Env по-прежнему перекрывает значения из файла.
- [ ] Hot-reload без файла по-прежнему отказывает и не подменяет конфиг (P3).
- [ ] Ни одна ошибка загрузки не приводит к старту с частичным или минимальным конфигом.
- [ ] Smoke и e2e-ha стартуют как раньше (P6).

## Edge Cases

1. `AMP_CONFIG_FILE` не задан, `./config.yaml` нет, env пуст → старт на дефолтах viper (`standard`, `development`, P5). Без БД процесс упадёт дальше на подключении, а не на конфиге, как и при любом standard-старте без Postgres.
2. `AMP_CONFIG_FILE="  "` (пробелы) → считается незаданным (текущий `TrimSpace`), путь `./config.yaml` — дефолтный.
3. `AMP_CONFIG_FILE=/etc/amp/config.yaml`, файла нет → exit 1 с путём.
4. Файл есть, но это каталог → `ReadFile` даёт ошибку, отличную от `ErrNotExist` → exit 1.
5. Файл пустой → viper даёт пустую map, конфиг из дефолтов и env. Поведение не меняется; чарт отдельно запрещает пустой `configFile.content`.
6. Дефолтный чарт (`APP_ENVIRONMENT=production`, `DATABASE_SSL_MODE=disable`) → exit 1 с `database SSL mode 'disable' is not allowed in production`. Это ожидаемо и описано в CHANGELOG и `HELM-DEFAULTS-VALIDATE`.
7. Явный путь к symlink на отсутствующий файл (ConfigMap mount) → `os.Stat` по symlink даёт `ErrNotExist` → exit 1.

## Impact Analysis

- **Affected modules:** `internal/config`, `cmd/server`; docs/planning.
- **Breaking changes:**
  1. Невалидный или нечитаемый конфиг теперь фатален.
  2. Явный `AMP_CONFIG_FILE` без файла фатален.
  3. Env из ConfigMap `amp-config` начинает действовать. На дефолтном чарте это проявится как ошибка валидации SSL; с переопределёнными values применятся `LLM_*`, `APP_ENVIRONMENT`, `GROUPING_*` и т. д.
- **New dependencies:** none.
- **Risks:**
  - Операторы, чей кластер «работал» на фолбэке, после обновления получат crash. Митигация: migration note; на практике дефолтный standard-деплой и так не стартовал (`database host is required`).
  - Harness `futureparity` поменяет исходный конфиг. Митигация: прогон `go test ./cmd/server/...`.

## Rollout / Rollback

- **Rollout:** нет `deploy` (WORKFLOW.md). Изменение попадает в `CHANGELOG.md` `[Unreleased]` → `### Fixed` и migration notes:
  - «AMP no longer starts on a minimal built-in config when loading fails; fix the reported error»;
  - «a missing explicit `AMP_CONFIG_FILE` is fatal»;
  - «without a config file, environment variables (Helm `amp-config`) now apply»;
  - «the chart's default values currently fail validation in `standard` — see `HELM-DEFAULTS-VALIDATE`».
- **Rollback:** откат образа на предыдущий.
- **Feature flag:** not applicable — флаг вернул бы fail-open.

## Observability

- **Logs:**
  - INFO `no config file, using environment and defaults` (`path`);
  - INFO с путём загруженного файла (если такой лог уже есть — оставить);
  - ERROR `failed to load configuration` (`path`, `error`) перед exit 1;
  - сообщение `Config file not found, using defaults` уходит.
- **Metrics:** not applicable.
- **Alerts:** not applicable — crash loop пода видят стандартные алерты kube-prometheus-stack.

## Deep Review

- **Mandatory triggers present:** 3+ signals (`C X R`).
- **Discretionary triggers present:** `C+X`; security-смежный fail-closed.
- **Decision:** required.

## Open Questions

- [ ] Решение владельца для `HELM-DEFAULTS-VALIDATE`: TLS встроенного Postgres в `production` или другой `environment` по умолчанию; `llm.enabled` по умолчанию. На эту задачу не влияет.
- [x] Явный отсутствующий `AMP_CONFIG_FILE` — фатален. Это отклонение от формулировки критерия 1 в requirements, критерий обновлён.
