---
id: PROD-CONFIG-FILE-FALLBACK
slug: prod-config-file-fallback
stream: production-readiness
type: bug
status: active
created_at: 2026-10-07
updated_at: 2026-10-07
based_on:
  - requirements.md
  - research.md
  - Spec.md
---

# Implementation Plan: отсутствие файла конфига не должно выбрасывать env

**Based on:** requirements.md / research.md / Spec.md v1.0
**Date:** 2026-10-07
**Tier:** Full → `implement → deep-review → write-tests → testing → finalize → merge-to-main`

## Touched Files

- `go-app/internal/config/config.go` — `LoadConfig`: `errors.Is(err, fs.ErrNotExist)` вместо проверки `ConfigFileNotFoundError`.
- `go-app/cmd/server/main.go` — `resolveRuntimeConfigPath` → `(path, explicit)`; проверка явного пути; exit 1 при ошибке; удаление фолбэка и патча `UnauthenticatedPaths`.
- `go-app/cmd/server/futureparity_compat.go` — адаптация к новой сигнатуре `resolveRuntimeConfigPath`, логика не меняется.
- `go-app/internal/config/config_load_test.go` (новый) — тесты загрузки: нет файла, битый, невалидный, нечитаемый.
- `go-app/internal/config/chart_env_keys_test.go` (новый) — `TestChartEnvKeysKnownToViper`.
- `go-app/cmd/server/` — тест проверки явного пути (в существующем или новом `*_test.go`).
- `docs/ALERTMANAGER_COMPATIBILITY.md` — Known Gaps п. 15.
- `helm/amp/README.md` — предупреждение «Read before installing» и § Graceful Shutdown (стр. 132).
- `CHANGELOG.md` — `[Unreleased]` → `### Fixed` и migration notes.
- `docs/06-planning/BUGS.md`, `BACKLOG.md` — закрытие бага, новая P0 `HELM-DEFAULTS-VALIDATE` (в `finalize`).

## Phase 1: Код

> **Wave 1** — независимые шаги

- [x] **1.1** `LoadConfig`: при ошибке `ReadInConfig`, если `errors.Is(err, fs.ErrNotExist)`, продолжить без файла; иначе `failed to read config file: %w`. Комментарий — почему не `ConfigFileNotFoundError` (`SetConfigFile` не ищет файл). <!-- verify: cd go-app && go build ./... && go vet ./internal/config/ && go test ./internal/config/ -count=1 -->
- [x] **1.2** `main.go`: `resolveRuntimeConfigPath() (string, bool)` — `explicit=true`, когда `AMP_CONFIG_FILE` непуст после `TrimSpace`. Маленькая функция `checkConfigPath(path string, explicit bool) error`:
  - явный путь + `os.Stat` → `fs.ErrNotExist` ⇒ ошибка `config file %q from AMP_CONFIG_FILE does not exist`;
  - дефолтный путь без файла ⇒ `nil` и INFO `no config file, using environment and defaults` (`path`).
  
  Адаптировать вызов в `futureparity_compat.go:123` (`path, _ :=` — значение используется, глушится только флаг; это не проглоченная ошибка). <!-- verify: cd go-app && go build ./cmd/server/ && go vet ./cmd/server/ -->

> **Wave 2** — зависит от Wave 1

- [x] **1.3** `main.go`: `checkConfigPath`, затем `LoadConfig`; любая ошибка → `slog.Error("failed to load configuration", "path", path, "error", err)` + `os.Exit(1)`. Удалить минимальный фолбэк (`:57-62`) и патч `UnauthenticatedPaths` (`:73-76`). <!-- depends: 1.1, 1.2 | verify: cd go-app && go build ./cmd/server/ && grep -n "Config file not found\|DefaultUnauthenticatedPaths" cmd/server/main.go; test $? -eq 1 -->
- [x] **1.4** Ручная проверка бинаря (`go build -o $SCRATCH/amp ./cmd/server`) в пустом каталоге:
  - (a) без файла, `PROFILE=lite STORAGE_BACKEND=filesystem STORAGE_FILESYSTEM_PATH=$SCRATCH/db SERVER_PORT=18093` → старт, слушает `:18093` (не 9093), `curl /-/healthy` = 200;
  - (b) `AMP_CONFIG_FILE=/nonexistent.yaml` → exit 1 с путём;
  - (c) файл с битым YAML → exit 1;
  - (d) env дефолтного чарта (`evidence/chart-default-env.txt`, пароль подставить ≥12 символов) → exit 1 с `database SSL mode 'disable' is not allowed in production`.
  
  Результаты — в `evidence/binary-check.md`. <!-- depends: 1.3 | verify: каждый из 4 сценариев дал ожидаемый код выхода/лог; записано в evidence -->
- [x] **1.5** `go test ./cmd/server/... -count=1` — harness `futureparity` и `route_prefix` зелёные без `config.yaml` в cwd (Spec P5). Если красный — выяснить причину, а не подгонять тест; при расхождении с P5 обновить Spec. <!-- depends: 1.3 | verify: cd go-app && go test ./cmd/server/... -count=1 -->

**Phase verification:** `cd go-app && go vet ./internal/config/... ./cmd/server/... && go test ./internal/config/... ./cmd/server/... -count=1`; `evidence/binary-check.md` записан.

## Phase 2: Docs

- [x] **2.1** `docs/ALERTMANAGER_COMPATIBILITY.md` п. 15: файл конфига необязателен (env + дефолты); невалидный или нечитаемый конфиг и отсутствующий явный `AMP_CONFIG_FILE` — exit 1; дефолтные values чарта пока не проходят валидацию в `standard` → `HELM-DEFAULTS-VALIDATE`. <!-- verify: grep -n "Config file not found" docs/ALERTMANAGER_COMPATIBILITY.md; test $? -eq 1 -->
- [x] **2.2** `helm/amp/README.md`:
  - в предупреждении «Read before installing» заменить причину: теперь `configFile` не обязателен, а `standard` падает на `database SSL mode 'disable' is not allowed in production` (`environment: production` + встроенный Postgres без TLS). Обход до `HELM-DEFAULTS-VALIDATE` описать фактом, без рекомендации ослаблять безопасность: TLS для Postgres (`postgresql.config.ssl: "on"` с сертификатами) или внешний PG с TLS;
  - § Graceful Shutdown (стр. 132): убрать оговорку про игнорируемый env.
  
  Сниппет `values-small.yaml` проверить так же, как 1.4(d): рендер → env + файл → загрузка; результат — в `evidence/binary-check.md`. <!-- depends: 1.4 | verify: grep -n "CONFIG-MISSING-FILE-DROPS-ENV" helm/amp/README.md; test $? -eq 1 -->
- [x] **2.3** `CHANGELOG.md` `[Unreleased]`: `### Fixed` — `PROD-CONFIG-FILE-FALLBACK` плюс migration notes из Spec § Rollout (четыре пункта). <!-- verify: grep -n "PROD-CONFIG-FILE-FALLBACK" CHANGELOG.md -->

**Phase verification:** `git diff --check`; ссылки на `HELM-DEFAULTS-VALIDATE` в доках совпадают с именем задачи, которое заведёт `finalize`.

## Implementation notes (2026-10-07)

- Отклонений от Spec нет. `checkConfigPath` при ошибке `os.Stat`, отличной от `ErrNotExist`, возвращает `nil` — ошибка придёт из `LoadConfig` с полным контекстом (каталог, права).
- Удаление патча `UnauthenticatedPaths` в `main.go` делает старт и hot-reload согласованными: reload патч никогда не применял, и `ReloadableWebAuth` сравнивал «пропатченный» стартовый список с непропатченным из reload. Поведение для явного `unauthenticated_paths: null` в файле — вопрос к `deep-review`.
- Утверждение в compat-доке «upstream likewise refuses to start» сверено: `alertmanager@v0.32.0` `cmd/alertmanager/main.go:567` (`configCoordinator.Reload()` → `return 1`).
- 1.4/2.2 — `evidence/binary-check.md` (сценарии a–f). README-сниппет `values-small.yaml` падает на той же проверке SSL, с `sslmode=require` конфиг загружается.

## Gate: deep-review

- [x] **R.1** `/deep-review` (обязателен: `C X R`). Фокус: fail-closed — нет пути, при котором ошибка конфига ведёт к старту; отсутствие утечки секретов в ERROR-логе; семантика явного пути; harness. `write-tests` — только после `review-verdict.json` `"gate": "pass"`. <!-- depends: Phase 1, Phase 2 | verify: jq -r .gate tasks/PROD-CONFIG-FILE-FALLBACK/review-verdict.json == pass -->

## Phase 3: Tests (`write-tests`, после verdict `pass`)

- [x] **3.1** `config_load_test.go`:
  - `TestLoadConfig_MissingFile_UsesEnv` — `t.Setenv` для `PROFILE=lite`, `SERVER_PORT=18080`, `GROUPING_ENABLED=true`; `viper.Reset()` в начале; проверить поля и непустой `Server.Auth.UnauthenticatedPaths`;
  - `TestLoadConfig_MalformedYAML_ReturnsError`;
  - `TestLoadConfig_InvalidConfig_ReturnsError` (`server.external_url: "::bad"`);
  - `TestLoadConfig_UnreadableFile_ReturnsError` (`chmod 000`, `t.Skip` под root);
  - `TestLoadConfig_DirectoryPath_ReturnsError`.
  
  Изоляция глобального viper — по образцу соседних тестов. <!-- depends: R.1 | verify: cd go-app && go test ./internal/config/ -run 'TestLoadConfig_' -count=1 -v -->
- [x] **3.2** `chart_env_keys_test.go` — `TestChartEnvKeysKnownToViper`:
  - извлечь env-имена из `../../../helm/amp/templates/deployment.yaml` (`- name: ([A-Z][A-Z0-9_]+)` в контейнере `amp`) и `configmap.yaml` (`^([A-Z][A-Z0-9_]+):`);
  - после `setDefaults()` каждое имя, кроме явного allowlist (`SERVICE_NAME`, `SERVICE_VERSION`, `AMP_CONFIG_FILE`, `SERVER_WEB_CONFIG_FILE` — если не ключи, и прочих, читаемых через `os.Getenv`; каждое с комментарием-причиной), должно соответствовать ключу из `viper.AllKeys()`.
  
  Мутационная проверка: временно добавить `FOO_BAR:` в `configmap.yaml` → тест красный. <!-- depends: R.1 | verify: cd go-app && go test ./internal/config/ -run TestChartEnvKeysKnownToViper -count=1 -v -->
- [x] **3.3** Тест `checkConfigPath` в `cmd/server`: явный отсутствующий путь → ошибка с путём; дефолтный отсутствующий → `nil`; явный существующий → `nil`; `resolveRuntimeConfigPath` с пробелами в `AMP_CONFIG_FILE` → `explicit=false`. <!-- depends: R.1 | verify: cd go-app && go test ./cmd/server/ -run 'ConfigPath' -count=1 -v -->

**Phase verification:** `cd go-app && go test ./internal/config/... ./cmd/server/... -count=1 -race`.

## Write-tests notes (2026-10-07)

- **Отклонение после вердикта (код, 1 строка):** `TestChartEnvKeysKnownToViper` нашёл, что `GROUPING_RECONCILIATION_GRACE` из `templates/configmap.yaml` не доходит до `Config`. У `grouping.reconciliation_grace` намеренно нет `SetDefault`, а `AutomaticEnv` видит только известные ключи. Probe в research этого не поймал: в дефолтном рендере ключ не выводится (`{{- if }}`). Фикс — `viper.BindEnv("grouping.reconciliation_grace")` в `setDefaults` (`config.go`), семантика «unset → деривация» сохранена и закреплена тестом. Покрывает критерий 3 requirements. Изменение после `review-verdict.json` (`216fa19`) — на проверку `qa-check`/`testing`.
- Мутационные проверки (откат и восстановление, дерево чистое):
  - `FOO_BAR:` в `configmap.yaml` → `TestChartEnvKeysKnownToViper` красный;
  - удалить `BindEnv` → `TestLoadConfig_ReconciliationGraceFromEnv` и `TestChartEnvKeysKnownToViper` красные;
  - заменить `fs.ErrNotExist` → `MissingFile_UsesEnv`, `ReconciliationGraceFromEnv`, `DanglingSymlink` красные.
- `TestLoadConfig_EnvOverridesFile` уже был в `config_test.go` — инвариант «env перекрывает файл» покрыт им, дубль не добавлялся.
- Устаревшие комментарии в `shutdown_test.go` (F2) и `webauth_wiring_test.go` обновлены.
- F5 (stale viper) тестом не закреплён: это зафиксированное поведение долга `CONFIG-GLOBAL-VIPER-STATE`, а не контракт.

## Phase 4: Gates и закрытие (`testing` → `finalize`)

- [ ] **4.1** Гейты AMP (WORKFLOW.md § Гейты): `go vet` + `go test` затронутых пакетов, `make -C go-app quality-gates-fast` (после — `git status`), `scripts/release-gate.sh`, `git diff --check`, нет `_, _ :=` в диффе. <!-- verify: все команды exit 0; вывод — в testing-отчёт -->
- [ ] **4.2** `finalize`:
  - doc-nit R3-1..R3-3 из `review-findings.md` (README чарта, compat п. 15);
  - TECH-DEBT: `CONFIG-GLOBAL-VIPER-STATE` (F5), `CONFIG-VALIDATION-ERROR-REDACTION` (F6), `CONFIG-PATH-RESOLUTION-DUP` (F8); `HELM-DEFAULTS-VALIDATE` — включить values внешней БД со ссылкой на Secret (N1);
  - `BUGS.md` — закрыть `CONFIG-MISSING-FILE-DROPS-ENV`;
  - `BUGS.md` — новый баг `HELM-DEFAULTS-FAIL-VALIDATION`: production + `sslmode=disable` в `values.yaml`/`values-production.yaml`; `llm.enabled: true` без `apiKey` → нет ключа `llm-api-key` → `CreateContainerConfigError`; LLM по умолчанию на example-прокси;
  - `BACKLOG.md` — P0 `HELM-DEFAULTS-VALIDATE` первой после закрытых, с `Waiting-on:` решения владельца по TLS/`environment`/LLM-дефолту; убрать `PROD-CONFIG-FILE-FALLBACK` из P0;
  - `NEXT.md` — WIP и порядок P0 в Queue;
  - `DONE.md`, архив workspace. <!-- depends: 4.1 | verify: grep -n "HELM-DEFAULTS-VALIDATE" docs/06-planning/BACKLOG.md docs/06-planning/NEXT.md -->

## Definition of Done

- [ ] All steps are complete or explicitly marked blocked/skipped
- [ ] Success criteria from `requirements.md` are covered
- [ ] Contracts from `Spec.md` are implemented or deviations are recorded
- [ ] Deep review verdict is `pass`
- [ ] Tests for changed behavior are added or updated
- [ ] Phase checks pass
- [ ] Docs/planning are updated (CHANGELOG, compat doc, chart README, BUGS, BACKLOG, NEXT, DONE)
