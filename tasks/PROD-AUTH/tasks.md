# Implementation Checklist: PROD-AUTH

Ветка `feature/prod-auth`. Источник: `Spec.md` (решения D1–D10, критерии AC1–AC13). Пути — относительно `go-app/`, если не указано иное.

Два среза в одной ветке (Spec «Нарезка»). После каждого шага сборка и существующие тесты остаются зелёными. Срез 1 закрывается отдельным коммитом до начала среза 2.

Уточнение Spec D4 на `/plan`: в кодовой базе действует honesty rule (`internal/config/reloadable_warnings.go`) — изменение, которое нельзя применить на лету, обязано давать W6xx-предупреждение, а не молчаливый no-op. Поэтому `server.web_config_file` и `server.auth.unauthenticated_paths` при `/-/reload` дают новый код **W605** (restart required), по образцу `reloadable_metrics.go`. Оба поля — restart-only: живое изменение списка исключений через reload открывало бы пути без рестарта, а выигрыша нет.

## Research & Spec
- [x] Research — `research.md` (F1 мёртвый middleware, F2 мёртвый `webhook.authentication`, F4 upstream-поведение, F6 probes/sidecar, F7 route prefix)
- [x] Spec — `Spec.md` (D1–D10), согласован 2026-09-25; явный opt-out отклонён (D7)
- [x] Follow-ups в BACKLOG: `DEAD-APPLICATION-MIDDLEWARE`, `DEAD-WEBHOOK-SECURITY-CONFIG`

## Срез 1 — Go (~1d)

Отклонения от плана, найденные по ходу:
- **Фолбэк конфига теряет env** (BUGS `CONFIG-MISSING-FILE-DROPS-ENV`): без файла конфига `main` работает на минимальном `Config` и не видит `SERVER_WEB_CONFIG_FILE` ⇒ auth молча выключился бы. Обойдено в `main.go`: `resolveWebConfigFile` читает env напрямую (флаг > конфиг > env), а пустой список исключений в фолбэке заполняется `config.DefaultUnauthenticatedPaths()`. Корень не тронут.
- **W605 и флаг**: `ServiceRegistry.SetWebConfigFlag` — при заданном `-web.config.file` правка `server.web_config_file` в конфиге не даёт ложного W605 (флаг всё равно побеждает).
- `yaml.v3` (уже direct) для строгого парсинга; `x/crypto` переведён в direct вручную — `go mod tidy` тронул бы чужие зависимости.
- `TestRegisterReloadables_RegistersAllFiveInReloadOrder` → `..._RegistersAllInReloadOrder`, в списке появился `web_auth`.
- Ручной smoke на бинаре (lite, `:19199`): exempt 200 / `/healthz` 401 / анонимный POST silences 401 / с кредами 200 / метрики; смена пароля на лету; невалидный файл ⇒ старый набор + счётчик 1 + один ERROR; `/-/reload` при флаге без W605; `tls_server_config` ⇒ exit 1; фолбэк-конфиг + env на несуществующий файл ⇒ exit 1; без web-config ⇒ WARN и всё открыто. Пароли в лог не попали.


- [x] **S1. Конфиг** — `internal/config/config.go`:
  - `ServerConfig.WebConfigFile string` (`mapstructure:"web_config_file"`);
  - `ServerConfig.Auth ServerAuthConfig` с `UnauthenticatedPaths []string` (`mapstructure:"unauthenticated_paths"`);
  - `viper.SetDefault("server.web_config_file", "")` (без дефолта `AutomaticEnv` не увидит `SERVER_WEB_CONFIG_FILE`) и `server.auth.unauthenticated_paths = ["/-/healthy", "/-/ready"]`;
  - тест: env `SERVER_WEB_CONFIG_FILE` доходит до `cfg.Server.WebConfigFile`; дефолтный список исключений.
- [x] **S2. Загрузчик файла (D3)** — `internal/application/webauth.go`:
  - тип `webAuthConfig{ users map[string][]byte }` и `loadWebAuthConfig(path) (*webAuthConfig, error)`;
  - строгий YAML (`yaml.v3` `KnownFields(true)` — проверить, какой yaml уже используется в `internal/config`, и взять его);
  - до строгого парсинга — отдельная проверка верхнеуровневых ключей `tls_server_config` / `http_server_config` / `rate_limit` ⇒ ошибка с текстом из Spec D3;
  - пустое имя, не-bcrypt хеш (`bcrypt.Cost`), пустой/отсутствующий `basic_auth_users` ⇒ ошибка с именем пользователя (но не хешем);
  - `go.mod`: `golang.org/x/crypto` из indirect в direct (`go mod tidy`, убедиться, что версия не сдвинулась).
- [x] **S3. Middleware (D5, D6, D9)** — там же:
  - `NewWebAuth(path string, unauthenticated []string, logger, metrics) (*WebAuth, error)` — грузит файл на старте (ошибка ⇒ наружу);
  - `(*WebAuth).Wrap(next http.Handler) http.Handler`;
  - порядок: exempt (точное совпадение `r.URL.Path`, set) → `r.BasicAuth()` (нет ⇒ `missing`) → пользователь/заглушка-хеш → кэш → bcrypt под мьютексом → 401 `WWW-Authenticate: Basic` / `next`;
  - кэш: `map[[32]byte]bool` под мьютексом, ключ `sha256(user\x00hash\x00pass)`, лимит 100, вытеснение ~10% случайных;
  - компаратор — поле `compare func(hash, pass []byte) error` (по умолчанию `bcrypt.CompareHashAndPassword`) для теста AC5;
  - хеш-заглушку сгенерировать один раз константой (cost 10, как у upstream), в комментарии — зачем.
- [x] **S4. Hot reload (D4)** — там же:
  - `atomic.Pointer[webAuthConfig]` + запомненные `ModTime`/`Size`; проверка не чаще раза в секунду (`atomic.Int64` с unix-nano последней проверки, чтобы не брать мьютекс на каждый запрос);
  - изменилось ⇒ `loadWebAuthConfig`; ок ⇒ swap + сброс кэша + `INFO users=N`; ошибка/файл пропал ⇒ оставить старое, `ERROR` один раз на пару `(ModTime, Size)`, `amp_http_auth_config_reload_failures_total++`;
  - часы — инъекция `now func() time.Time` для теста.
- [x] **S5. Метрики (D8)**: `amp_http_auth_failures_total{reason}`, `amp_http_auth_config_reload_failures_total`. Регистрация — тем же способом, что остальные `amp_*` в `internal/application` (посмотреть, есть ли общий registry/`promauto`; в тестах — свой `prometheus.NewRegistry()`, чтобы не ловить duplicate registration).
- [x] **S6. W605 restart-required** — `internal/config/reloadable_warnings.go` (+ константа `WarnWebAuthRestartRequired = "W605"`) и `internal/config/reloadable_webauth.go` по образцу `reloadable_metrics.go`: при изменении `server.web_config_file` или `server.auth.unauthenticated_paths` — `warnRestartRequired`, состояние не меняется. Зарегистрировать там же, где регистрируется metrics-reloadable. Если окажется, что restart-reloadables регистрируются так, что это тянет >~60 строк проводки — остановиться и зафиксировать в `research.md`/BUGS, а не городить.
- [x] **S7. `cmd/server/main.go`**:
  - флаг `-web.config.file` (help-текст как у upstream), после `flag.Parse` и загрузки конфига: флаг непустой ⇒ перекрывает `cfg.Server.WebConfigFile`;
  - путь непустой ⇒ `application.NewWebAuth(...)`; ошибка ⇒ `slog.Error` + `os.Exit(1)`; `INFO` (D7) с `users`, `unauthenticated_paths`;
  - путь пуст ⇒ `WARN` (текст из Spec D7);
  - `cfg.Webhook.Authentication.Enabled` ⇒ `WARN` (D8);
  - `rootHandler := application.WithRoutePrefix(authWrapped, prefix)` — auth строго внутри префикса;
  - удалить `_ "net/http/pprof"`.
- [x] **S8. Проверка среза 1**: `go build ./...`, `go vet ./...`, `go test ./internal/application/... ./internal/config/... ./cmd/...`, `git diff --check`; коммит `feat(auth): ...`.

## Срез 2 — Helm, доки, ADR (~0.5d)

Отклонения от плана:
- **config-reloader sidecar уже существует** (план считал, что кода нет): он ходит в `/-/reload` и `/health/reload` без кредов и получил бы 401 даже при `method: signal`. Чарт теперь `fail`-ит комбинацию `configReloader` + `webConfig` с понятным сообщением (рядом с существующими `fail`-проверками sidecar); поддержка auth в sidecar — BACKLOG `CONFIG-RELOADER-AUTH`. Пункт `CONFIG-RELOADER-SIDECAR` в NEXT.md помечен как устаревший.
- Переопределений путей probes в `values-*.yaml` нет; smoke/e2e-скрипты ходят в `/healthz` без auth — не тронуты.
- Проверено: `helm lint` PASS; `helm template` дефолт (probes `/-/healthy`/`/-/ready`, web-config нет), `--set webConfig.existingSecret=amp-web` (env, volumeMount, secret volume), `profile=lite` + webConfig (оба volume), reloader + webConfig ⇒ `fail`, reloader без webConfig ⇒ рендерится. `charts/valkey` собран локально `helm dependency build`, не коммитится.


- [x] **S9. Helm** (`helm/amp/`):
  - `values.yaml`: блок `webConfig` (`existingSecret: ""`, `secretKey: web-config.yml`, `mountPath: /etc/amp/web`) с комментарием про `htpasswd -nBC 10`; `probes.liveness.path`/`startup.path` → `/-/healthy`, `probes.readiness.path` → `/-/ready`; то же в `| default` в `templates/deployment.yaml`;
  - `templates/deployment.yaml`: при `webConfig.existingSecret` — volume (secret), `volumeMount` readOnly, env `SERVER_WEB_CONFIG_FILE`;
  - проверить `values-production.yaml` и прочие `values-*.yaml`: если там переопределены пути probes на `/healthz`/`/readyz` — перевести на `/-/healthy`/`/-/ready` (иначе с auth probe получит 401);
  - `helm template` дефолт / с `--set webConfig.existingSecret=amp-web` — глазами и `grep` (AC11). Внимание: `PROD-HELM-CLEAN-CHECKOUT` — если нет `charts/valkey`, сделать `helm dependency build` локально и не коммитить.
- [x] **S10. Доки**:
  - `docs/CONFIGURATION_GUIDE.md` — раздел «Authentication»: флаг/ключ/env, формат файла, генерация хеша, `unauthenticated_paths`, hot reload и его отклонение от upstream, что даёт ошибку старта, Prometheus `basic_auth` пример, amtool `--http.config.file`, Helm `webConfig`;
  - `docs/ALERTMANAGER_COMPATIBILITY.md` — строка `--web.config.file`: поддержано `basic_auth_users`; `tls_server_config`/`http_server_config`/`rate_limit` ⇒ ошибка старта; отклонения (пустые users, reload без 500, исключения путей). Поправить `:53` «this endpoint is unauthenticated» → «unauthenticated unless web config is set»;
  - `CHANGELOG.md` `[Unreleased]` — Added (auth), Changed (Helm probe paths), Removed (pprof import); дефолт не меняется;
  - `docs/06-planning/DECISIONS.md` — ADR-011 (дефолт off + WARN, свой middleware вместо exporter-toolkit, отклонения D3/D4/D5, отклонённый opt-out);
  - BACKLOG: `PROD-AUTH-BEARER`; в `CONFIG-RELOADER-SIDECAR` (NEXT.md) — заметка «нужны basic-креды или SIGHUP, loopback-исключение запрещено»; в `PROD-INGRESS-HARDENING` — «auth теперь есть, решить обязательность в values-production».
- [x] **S11. Проверка среза 2**: `helm lint`, `helm template` (AC11), `git diff --check`; коммит `docs(auth): ...` / `feat(helm): ...`.

## Testing (на `/write-tests` и `/testing`)

- [x] **T1** загрузчик, табличный (AC7): валидный; нет файла; битый YAML; неизвестный ключ; `tls_server_config`; `http_server_config`; `rate_limit`; пустые users; нет `basic_auth_users`; пустое имя; не-bcrypt хеш.
- [x] **T2** middleware (AC1–3): без заголовка ⇒ 401 + `WWW-Authenticate: Basic` + `missing`; неверный пароль ⇒ 401 + `invalid`; неизвестный пользователь ⇒ 401 + `invalid`; валидные ⇒ `next` вызван; exempt `/-/healthy`, `/-/ready` ⇒ 200 без кредов; `/healthz`, `/metrics` ⇒ 401; кастомный exempt; `/-/healthy/` (слеш) **не** exempt.
- [x] **T3** route prefix (AC4): `WithRoutePrefix(auth.Wrap(mux), "/am")` — `/am/-/healthy` 200, `/am/api/v2/silences` 401, с кредами — 200.
- [x] **T4** кэш (AC5): два запроса с теми же кредами ⇒ компаратор вызван один раз; другой пароль ⇒ второй вызов; переполнение кэша не паникует и держит размер ≤ лимита.
- [x] **T5** hot reload (AC6): смена пароля в файле + сдвиг часов ⇒ старый 401, новый 200; невалидный файл ⇒ старый набор работает, счётчик +1, повторные запросы не увеличивают счётчик/ERROR повторно; удаление файла ⇒ старый набор; проверка не чаще раза в секунду (без сдвига часов изменение не видно).
- [x] **T6** секреты в логах (AC10): захват `slog` в буфер, прогон отказов и reload — в выводе нет ни пароля, ни хеша, ни `Authorization`.
- [x] **T7** W605 (S6): изменение `server.web_config_file` через reload ⇒ предупреждение W605 в `RestartWarnings`, состояние не меняется.
- [x] **T8** интеграционный сквозной на реальном роутере (AC1 целиком): `NewRouter(registry).SetupRoutes(mux)` в существующем тестовом харнессе `internal/application` (если он поднимает registry без БД) — анонимный `POST /api/v2/silences` ⇒ 401, с кредами ⇒ 200. Если харнесс тяжёлый — достаточно T2 + ручной проверки на `/testing`.
- [x] **T9** ручная проверка на `/testing` (AC1, AC8): lite-инстанс без web-config (WARN в логе, всё открыто) и с web-config (`curl` без/с кредами, `amtool --http.config.file`, смена пароля на лету).
- Где лежат тесты: `go-app/internal/application/webauth_test.go` (T1–T6, T8), `go-app/internal/config/reloadable_webauth_test.go` (T7 + откат снимает W605, nil-конфиг), `go-app/cmd/server/webauth_wiring_test.go` (флаг > конфиг > env для `SERVER_WEB_CONFIG_FILE`, без пути middleware не строится).
- Сверх плана: length-prefix ключа кэша, отказ `NewWebAuth` на пустом пути и на отсутствующем файле, «ошибка загрузки не содержит хеш».
- Отложено осознанно: T9 — ручная проверка на `/testing`. Helm (`webConfig`, `fail` при сочетании с `configReloader`, probes) юнит-тестами не покрыт: в репо нет helm-unittest, проверяется `helm lint` + `helm template` на `/testing`. Постоянство времени ответа (заглушка-хеш для неизвестного пользователя) тестом не измеряется — проверено только, что bcrypt вызывается и для неизвестного пользователя (T4).
- [x] `go vet ./...`, `go test ./...` (с учётом `PUBLISHING-WARMUP-TEST-FLAKY`), `go build ./...`, `git diff --check` (AC13).

## /write-doc (2026-09-25)
- Проверены против smoke `/testing`: `CONFIGURATION_GUIDE` §4 и `ALERTMANAGER_COMPATIBILITY` (строка `--web.config.file`) — правок не потребовали.
- Добавлено: `helm/amp/README.md` (раздел HTTP Authentication, таблица `webConfig.*`), `helm/amp/DEPLOYMENT.md` (§4 HTTP Authentication — Secret из `htpasswd`, `curl -u`), `helm/amp/CHANGELOG.md` (`webConfig.*`, смена probes).
- `docs/ROLLBACK_RUNBOOK.md`: откат на образ до PROD-AUTH молча открывает API (старый бинарь игнорирует `SERVER_WEB_CONFIG_FILE`) — предупреждение и `curl -u`.
- `docs/ALERTMANAGER_COMPATIBILITY.md`: «unauthenticated endpoint» у редакции `/api/v2/status` уточнено до «unauthenticated unless `--web.config.file` is set».
- Spec/requirements: допущения не изменились; отклонения реализации уже записаны в срезах выше.

## Finalization (`/end-task`)
- [ ] AC1–AC13 сверены с фактом
- [ ] `grep -rn 'net/http/pprof' cmd internal` пусто (AC9)
- [ ] NEXT.md → WIP снят, DONE.md, BACKLOG `PROD-AUTH` закрыт, архив `tasks/archive/PROD-AUTH/`

## Результат /testing (2026-09-25)

### Зелёное
- `make quality-gates-all` (gofmt + vet + полный тестовый набор): 53 пакета `ok`, `Full quality gates passed`.
- `golangci-lint run` по `internal/application`, `internal/config`, `cmd/server`: `No issues found`.
- `gofmt -l` по Go-файлам ветки — пусто; `git diff --check main...HEAD` — чисто.
- Helm: `helm lint` — 0 failed. `helm template` по умолчанию: probes на `/-/healthy` и `/-/ready`, web-config не монтируется. С `webConfig.existingSecret=amp-web`: env `SERVER_WEB_CONFIG_FILE=/etc/amp/web/web-config.yml`, volume из секрета `amp-web`. `webConfig` + `configReloader.enabled` ⇒ `fail` с отсылкой к CONFIG-RELOADER-AUTH.
- **T9, ручной smoke** (lite, `:19199`, собранный бинарь):
  - без web-config: анонимный `POST /api/v2/silences` ⇒ 200, WARN «authentication is DISABLED» в логе;
  - с `-web.config.file`: `/-/healthy`, `/-/ready` ⇒ 200 без кредов; `POST /api/v2/silences` анонимно ⇒ 401, с кредами ⇒ 200; `/metrics`, `/healthz` анонимно ⇒ 401;
  - `amtool silence query` без `--http.config.file` ⇒ 401, с ним ⇒ список silences (upstream-клиент совместим);
  - хеш из `htpasswd -nbBC 10` (`$2y$`) принимается;
  - ротация пароля в файле без рестарта: старый ⇒ 401, новый ⇒ 200;
  - невалидный файл: прежний пароль продолжает работать, `amp_http_auth_config_reload_failures_total 1`, один ERROR «Web config change rejected»;
  - `POST /-/reload` анонимно ⇒ 401, с кредами ⇒ 200, W605 не выдаётся (путь закреплён флагом);
  - пустой хеш в файле ⇒ процесс не стартует (ERROR «refusing to start…»);
  - в логе нет паролей и bcrypt-хешей (`grep` по паролям и `$2[aby]$` — 0 совпадений).

### Красное / вне скоупа
- Новых падений нет.
- Предсуществующее: `make quality-gates*` запускает `gofmt -w` и переформатирует 6 файлов, не относящихся к PROD-AUTH (`cmd/server/futureparity_compat.go`, `internal/application/handlers/alerts_test.go`, `internal/core/investigation/{message,tool}.go`, `internal/infrastructure/inhibition/{matcher_impl,matchers_list_test}.go`). Это дрейф форматирования в `main`; в ветку PROD-AUTH не включается.
