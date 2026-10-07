---
id: PROD-CONFIG-FILE-FALLBACK
slug: prod-config-file-fallback
stream: production-readiness
type: bug
artifact: research-pack
status: active
created_at: 2026-10-07
updated_at: 2026-10-07
---

# Research Pack — отсутствие файла конфига не должно выбрасывать env

## 0) TL;DR

- **Задача:** без файла конфига AMP должен собираться из env и дефолтов viper, а любая другая ошибка загрузки — останавливать процесс.
- **Найдено:**
  - корень подтверждён: viper v1.21 при `SetConfigFile` возвращает `*fs.PathError`, а не `ConfigFileNotFoundError`;
  - env из чарта до viper доходит: 58 из 60 имён совпадают с ключами, у которых есть дефолт;
  - **но после фикса дефолтный чарт всё равно не стартует** — его же значения не проходят `Validate` (`environment: production` + встроенный Postgres без TLS);
  - ещё раньше контейнер упирается в отсутствующий ключ `llm-api-key` в Secret.
- **Выбор:** вариант A — Go-фикс плюс тест, который подтверждает, что env из чарта доходит до `Config`. Дефолты чарта (TLS к Postgres, LLM по умолчанию) выносятся в отдельную P0-задачу и требуют решения владельца.
- **Риски:**
  - дефолтный `helm install` останется нерабочим до следующей задачи — но теперь с понятной ошибкой вместо тихого фолбэка;
  - тестовый harness `futureparity` перестанет получать «пустой» фолбэк-конфиг.
- **Следующий шаг:** → spec.

## 1) Questions

1. Какую ошибку viper возвращает для отсутствующего файла и как её надёжно распознать?
2. Доходят ли env-переменные чарта до `Config` без файла? `AutomaticEnv` при `Unmarshal` видит только ключи, известные viper.
3. Проходит ли конфиг из дефолтных values `Validate` после фикса, в `standard` и в `lite`?
4. Кто ещё полагается на фолбэк: тесты, harness'ы, smoke/e2e, документация?
5. Какие обходы бага теперь можно упростить: `resolveWebConfigFile`, `DefaultUnauthenticatedPaths`, `effectiveShutdownTimeout`?

## 2) Findings

- **Codebase — viper.**
  - `go-app/go.mod`: `github.com/spf13/viper v1.21.0`.
  - Когда путь задан через `SetConfigFile`, `ReadInConfig` берёт `v.configFile` без поиска и вызывает `afero.ReadFile` (`viper.go:1518-1533`). Отсутствующий файл даёт `*fs.PathError`, обёрнутую ошибку `fs.ErrNotExist`.
  - `ConfigFileNotFoundError` возникает только в `findConfigFile`, то есть при `AddConfigPath`/`SetConfigName`, а AMP их не использует. Значит, проверка `config.go:660` никогда не срабатывает. Правильная проверка — `errors.Is(err, fs.ErrNotExist)`.
  - Воспроизведено: `LoadConfig("/nonexistent/config.yaml")` → `failed to read config file: open …: no such file or directory` (`evidence/probe-results.md`).
- **Codebase — env → Config.** Весь env контейнера `amp` из дефолтного рендера чарта прогнан через `LoadConfigFromEnv` (`evidence/chart-default-env.txt`, `evidence/probe-results.md`):
  - viper не знает только `SERVICE_NAME` и `SERVICE_VERSION`, и в Go-коде их никто не читает;
  - остальные ключи, включая `GROUPING_*`, `SILENCING_*`, `PUBLISHING_*`, `SERVER_GRACEFUL_SHUTDOWN_TIMEOUT`, `LLM_*`, `DATABASE_*`, `REDIS_ADDR`, `SERVER_PORT`, имеют `SetDefault` и попадают в `Config`.
  - Вывод: ловушка «AutomaticEnv без дефолта» к чарту сейчас не относится. В спеке нужен страхующий тест на это.
- **Codebase — валидация дефолтных values.**
  - **`standard`:** `config validation failed: … database SSL mode 'disable' is not allowed in production`.
    - Чарт выставляет `APP_ENVIRONMENT=production` (`values.yaml:5`) и `DATABASE_SSL_MODE=disable`, когда `postgresql.config.ssl: "off"` (`values.yaml:413`, `templates/deployment.yaml:104-105`).
    - Правило — `config.go:1407`.
    - В `values-production.yaml` та же пара: `environment: production` (стр. 17), `ssl: "off"` (стр. 136).
    - Сейчас эту ошибку прячет фолбэк, и наружу видна только `database host is required`.
  - С `DATABASE_SSL_MODE=require` валидация проходит. Тогда в конфиге `LLM.Enabled=true` и `BaseURL=https://llm-proxy.example.com` (`values.yaml:529-531`): после фикса каждый алерт синхронно классифицировался бы через несуществующий прокси (`PROD-LLM-ALERT-PATH-ISOLATION`).
  - **`lite` (`--set profile=lite`):** `LoadConfigFromEnv` → `<nil>`.
  - Тот же класс ошибок: в production действует и правило `https_production` для webhook на `http://` (`BUGS.md` § `CONFIG-MISSING-FILE-DROPS-ENV`, проверено 2026-10-07).
- **Codebase — чарт ещё до приложения.**
  - При `llm.enabled: true` и пустом `llm.apiKey` (дефолт) в Secret `amp-secrets` нет ключа `llm-api-key` (`templates/secret.yaml`, блок LLM).
  - Deployment ссылается на него через `secretKeyRef` без `optional` (`templates/deployment.yaml:127-133`).
  - По семантике kubelet это `CreateContainerConfigError`: контейнер не создаётся. Вывод сделан по рендеру, на кластере не проверялся.
  - Порты: чарт слушает и пробует `service.port: 8080`, фолбэк-конфиг слушает 9093. Даже без падения пробы бы не прошли.
- **Codebase — кто полагается на фолбэк.**
  - Есть два вызова с фолбэком:
    - `cmd/server/main.go:56-62` — продовый путь;
    - `cmd/server/futureparity_compat.go:122-127` — `loadFutureParityCompatibilityConfig`, используется только тестовым harness'ом (`futureparity_compat_test.go`, `route_prefix_integration_test.go`).
  - Harness после загрузки сам переопределяет профиль, storage, Redis и publishing. После фикса без `config.yaml` в cwd он получит дефолты viper вместо `futureParityDefaultConfig()`, поэтому нужен прогон `cmd/server` тестов.
  - Тесты `internal/config` вызывают `LoadConfig` с существующими временными файлами, отсутствующий файл нигде не проверяется.
  - `cmd/config-reloader/e2e_contract_test.go:62` тоже работает с реальным файлом.
  - Smoke и e2e-ha монтируют `config.yaml` и задают `AMP_CONFIG_FILE` (`deploy/smoke/docker-compose.yml:37`, `deploy/e2e-ha/docker-compose.yml:40,57`), так что на фолбэк не полагаются.
- **Codebase — обходы бага.**
  - `resolveWebConfigFile` (`main.go:207-219`) читает `SERVER_WEB_CONFIG_FILE` напрямую. Если у `server.web_config_file` есть `SetDefault`, после фикса это дублирование; если нет — env-чтение всё ещё нужно. Проверить на spec.
  - `DefaultUnauthenticatedPaths` после загрузки (`main.go:73-76`) нужен только из-за фолбэка: viper уже отдаёт `["/-/healthy","/-/ready"]` (probe).
  - `effectiveShutdownTimeout` остаётся страховкой на случай нулевого значения, так решено в PROD-GRACEFUL-SHUTDOWN.
- **Docs.** Нужно переписать: `docs/ALERTMANAGER_COMPATIBILITY.md:869` (Known Gaps п. 15), `helm/amp/README.md` (требование `configFile.enabled: true`, если оно там есть) и `BUGS.md`.
- **Constraints.**
  - Fail-closed: сейчас ошибка валидации молча отбрасывает `server.auth` и web config. После фикса процесс остановится — это и есть желаемое поведение безопасности.
  - Breaking для операторов, чей невалидный конфиг «работал» на фолбэке, поэтому нужна migration note.
  - Значения ошибок валидации содержат значения полей. Пример: `weak database password detected: '%s'` (`config.go:1396`) — в этом случае печатается только слово из списка слабых паролей, реальные секреты в сообщения не попадают. На spec проверить остальные сообщения.
- **Unknowns.**
  - Поведение чарта на живом кластере (`CreateContainerConfigError`) выведено из рендера, не наблюдалось.
  - Политика TLS для встроенного Postgres в `production` — решение владельца, не исследование.

## 3) Options

`Generation:` single-pass

### Option A — Go-фикс и тест env-пути; дефолты чарта — отдельной задачей

- `LoadConfig`: `errors.Is(err, fs.ErrNotExist)` → продолжить с дефолтами и env. Остальные ошибки возвращать как есть.
- `main.go`: при ошибке — `slog.Error` и `os.Exit(1)`. Минимальный фолбэк удалить. Сообщение о том, что файла нет, писать с путём.
- Обход `DefaultUnauthenticatedPaths` убрать. `futureparity` harness — решение на spec: выход при ошибке, либо оставить его фолбэк, ведь это тестовый код.
- Тесты:
  - нет файла + env → env применён;
  - битый YAML → ошибка;
  - невалидный файл → ошибка;
  - env из дефолтного рендера чарта доходит до `Config` — render-сценарий в `helm/amp/tests/` по образцу `render-*.sh`, либо Go-тест с golden-списком env-имён.
- Доки: Known Gaps п. 15 переписать. Новый баг — дефолтные values не проходят валидацию и `llm-api-key` отсутствует — завести P0 в `BACKLOG.md`/`BUGS.md`.
- **Pros:** укладывается в оценку ~0.5d и в формулировку P0 из BACKLOG. Не принимает за владельца решение о безопасности (TLS в production). Ошибка становится громкой и указывает настоящую причину.
- **Cons:** дефолтный `helm install` после задачи всё ещё не стартует, хотя причина теперь видна. Сценарий с `configFile.enabled: false` можно проверить только рендером и загрузкой конфига, а не живым стартом.
- **Cost/Risk:** low.

### Option B — Go-фикс плюс выравнивание дефолтов чарта в этой же задаче

- Всё из A, плюс:
  - `llm.enabled: false` по умолчанию — совпадает с дефолтом приложения `config.go:852`;
  - политика TLS для встроенного Postgres: (B1) `environment` по умолчанию не `production`, (B2) ослабить правило `config.go:1407` для встроенного Postgres, (B3) включить TLS во встроенном Postgres;
  - smoke-старт дефолтного чарта.
- **Pros:** P0 «дефолтный `helm install` работает» закрывается целиком.
- **Cons:**
  - B1 и B2 меняют поведение безопасности, и это решение владельца. B3 — отдельная работа с сертификатами, больше 1d.
  - Задача расползается на чарт и на смежную P1 (`PROD-LLM-ALERT-PATH-ISOLATION`). По Risk Profile добавляется `S`.
  - Оценка 1.5–2d.
- **Cost/Risk:** medium–high.

## 4) Decision

- **Chosen:** Option A. Дефолты чарта оформить новой P0-задачей `HELM-DEFAULTS-VALIDATE`, первой в очереди после этой. Нужно решение владельца по TLS-политике и дефолту LLM.
- **Why:**
  - Формулировка P0 в `BACKLOG.md` ограничивает задачу Go-фиксом и тестом. Найденная поломка дефолтов чарта — отдельная причина с отдельным решением (CLAUDE.md § Scope Discipline).
  - Вариант B1/B2 касается безопасности: ослабить проверку TLS в `production` агент без владельца не решает.
  - Вариант A сам по себе убирает fail-open: невалидный конфиг больше не запускает процесс без auth и маршрутов. Это главный риск бага.
  - Дефолтный чарт после A падает с первой настоящей ошибкой, а не с вводящей в заблуждение `database host is required`.

## 5) Spec Inputs

- **API/contracts.** Контракт старта меняется:
  - нет файла конфига → INFO/WARN с путём, работа на env + дефолтах;
  - ошибка чтения, YAML, unmarshal, `Validate` или inhibition → exit 1 с текстом ошибки.
  - Решить на spec: отличать ли явный `AMP_CONFIG_FILE`, указывающий на несуществующий путь, от дефолтного `./config.yaml`. Довод «за»: явный путь, которого нет, — почти всегда ошибка оператора. Довод «против»: так ведёт себя `--config.file` в upstream, его стоит сверить.
- **Data model/migrations:** not applicable.
- **Rollout/rollback.**
  - `CHANGELOG.md` `[Unreleased]` → `### Fixed` плюс migration note: невалидный конфиг теперь фатален, а env из ConfigMap `amp-config` начинает действовать.
  - Перечислить, что меняется на дефолтном чарте: `GROUPING_ENABLED=false` уже совпадает с дефолтом, а `LLM_ENABLED=true` и `APP_ENVIRONMENT=production` начинают применяться.
  - Rollback — откат образа.
- **Observability:** лог старта с путём конфига и признаком «файл не найден». Ошибка конфига уходит в лог уровня ERROR до выхода.
- **Security/ownership/RBAC:**
  - fail-closed при ошибке валидации — проверить на `deep-review`;
  - ошибки конфига не должны печатать секреты — пройтись по сообщениям `Validate`;
  - `resolveWebConfigFile`: проверить, есть ли `SetDefault("server.web_config_file")`, и только после этого упрощать.
- **Tests:**
  - unit в `internal/config`: нет файла + env, битый YAML, невалидный файл, ошибка прав доступа (если реально воспроизводится на CI);
  - `cmd/server`: тесты `futureparity` и `route_prefix` после изменения;
  - тест или render-сценарий: каждое env-имя из `templates/deployment.yaml` и `templates/configmap.yaml` соответствует ключу viper с дефолтом, исключения перечислены явно (`SERVICE_NAME`, `SERVICE_VERSION`).
- **Follow-up (вне задачи):**
  - P0 `HELM-DEFAULTS-VALIDATE`:
    - дефолтные `values.yaml` и `values-production.yaml` не проходят `Validate` (production + `sslmode=disable`);
    - `llm.enabled: true` с пустым `apiKey` → нет ключа `llm-api-key` → `CreateContainerConfigError`;
    - LLM включён по умолчанию с example-прокси.
  - Решение владельца: TLS для встроенного Postgres или `environment` по умолчанию.

## 6) References

- Files:
  - `go-app/internal/config/config.go:645-680` (`LoadConfig`), `:771-795` (`LoadConfigFromEnv`), `:852`, `:952`, `:1376-1412` (production-правила);
  - `go-app/cmd/server/main.go:55-76`, `:207-219`, `:297-303`;
  - `go-app/cmd/server/futureparity_compat.go:96-145`, `go-app/cmd/server/shutdown.go:31`;
  - `helm/amp/values.yaml:5,413,529-537,835-837`, `helm/amp/values-production.yaml:17,136,177-178,458-459`;
  - `helm/amp/templates/deployment.yaml:100-134,162-166`, `helm/amp/templates/secret.yaml`, `helm/amp/templates/configmap.yaml`;
  - `deploy/smoke/docker-compose.yml`, `deploy/e2e-ha/docker-compose.yml`;
  - `/Users/vit/go/pkg/mod/github.com/spf13/viper@v1.21.0/viper.go:1518-1544,2051-2060`.
- Docs: `docs/06-planning/BUGS.md` § `CONFIG-MISSING-FILE-DROPS-ENV`, `docs/06-planning/BACKLOG.md` § P0, `docs/ALERTMANAGER_COMPATIBILITY.md:869`.
- Evidence: `evidence/chart-default-env.txt`, `evidence/probe-results.md`.
