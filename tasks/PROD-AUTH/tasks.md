# Implementation Checklist: PROD-AUTH

Ветка `feature/prod-auth`. Источник: `Spec.md` (решения D1–D10, критерии AC1–AC13). Пути — относительно `go-app/`, если не указано иное.

Два среза в одной ветке (Spec «Нарезка»). После каждого шага сборка и существующие тесты остаются зелёными. Срез 1 закрывается отдельным коммитом до начала среза 2.

Уточнение Spec D4 на `/plan`: в кодовой базе действует honesty rule (`internal/config/reloadable_warnings.go`) — изменение, которое нельзя применить на лету, обязано давать W6xx-предупреждение, а не молчаливый no-op. Поэтому `server.web_config_file` и `server.auth.unauthenticated_paths` при `/-/reload` дают новый код **W605** (restart required), по образцу `reloadable_metrics.go`. Оба поля — restart-only: живое изменение списка исключений через reload открывало бы пути без рестарта, а выигрыша нет.

## Research & Spec
- [x] Research — `research.md` (F1 мёртвый middleware, F2 мёртвый `webhook.authentication`, F4 upstream-поведение, F6 probes/sidecar, F7 route prefix)
- [x] Spec — `Spec.md` (D1–D10), согласован 2026-09-25; явный opt-out отклонён (D7)
- [x] Follow-ups в BACKLOG: `DEAD-APPLICATION-MIDDLEWARE`, `DEAD-WEBHOOK-SECURITY-CONFIG`

## Срез 1 — Go (~1d)

- [ ] **S1. Конфиг** — `internal/config/config.go`:
  - `ServerConfig.WebConfigFile string` (`mapstructure:"web_config_file"`);
  - `ServerConfig.Auth ServerAuthConfig` с `UnauthenticatedPaths []string` (`mapstructure:"unauthenticated_paths"`);
  - `viper.SetDefault("server.web_config_file", "")` (без дефолта `AutomaticEnv` не увидит `SERVER_WEB_CONFIG_FILE`) и `server.auth.unauthenticated_paths = ["/-/healthy", "/-/ready"]`;
  - тест: env `SERVER_WEB_CONFIG_FILE` доходит до `cfg.Server.WebConfigFile`; дефолтный список исключений.
- [ ] **S2. Загрузчик файла (D3)** — `internal/application/webauth.go`:
  - тип `webAuthConfig{ users map[string][]byte }` и `loadWebAuthConfig(path) (*webAuthConfig, error)`;
  - строгий YAML (`yaml.v3` `KnownFields(true)` — проверить, какой yaml уже используется в `internal/config`, и взять его);
  - до строгого парсинга — отдельная проверка верхнеуровневых ключей `tls_server_config` / `http_server_config` / `rate_limit` ⇒ ошибка с текстом из Spec D3;
  - пустое имя, не-bcrypt хеш (`bcrypt.Cost`), пустой/отсутствующий `basic_auth_users` ⇒ ошибка с именем пользователя (но не хешем);
  - `go.mod`: `golang.org/x/crypto` из indirect в direct (`go mod tidy`, убедиться, что версия не сдвинулась).
- [ ] **S3. Middleware (D5, D6, D9)** — там же:
  - `NewWebAuth(path string, unauthenticated []string, logger, metrics) (*WebAuth, error)` — грузит файл на старте (ошибка ⇒ наружу);
  - `(*WebAuth).Wrap(next http.Handler) http.Handler`;
  - порядок: exempt (точное совпадение `r.URL.Path`, set) → `r.BasicAuth()` (нет ⇒ `missing`) → пользователь/заглушка-хеш → кэш → bcrypt под мьютексом → 401 `WWW-Authenticate: Basic` / `next`;
  - кэш: `map[[32]byte]bool` под мьютексом, ключ `sha256(user\x00hash\x00pass)`, лимит 100, вытеснение ~10% случайных;
  - компаратор — поле `compare func(hash, pass []byte) error` (по умолчанию `bcrypt.CompareHashAndPassword`) для теста AC5;
  - хеш-заглушку сгенерировать один раз константой (cost 10, как у upstream), в комментарии — зачем.
- [ ] **S4. Hot reload (D4)** — там же:
  - `atomic.Pointer[webAuthConfig]` + запомненные `ModTime`/`Size`; проверка не чаще раза в секунду (`atomic.Int64` с unix-nano последней проверки, чтобы не брать мьютекс на каждый запрос);
  - изменилось ⇒ `loadWebAuthConfig`; ок ⇒ swap + сброс кэша + `INFO users=N`; ошибка/файл пропал ⇒ оставить старое, `ERROR` один раз на пару `(ModTime, Size)`, `amp_http_auth_config_reload_failures_total++`;
  - часы — инъекция `now func() time.Time` для теста.
- [ ] **S5. Метрики (D8)**: `amp_http_auth_failures_total{reason}`, `amp_http_auth_config_reload_failures_total`. Регистрация — тем же способом, что остальные `amp_*` в `internal/application` (посмотреть, есть ли общий registry/`promauto`; в тестах — свой `prometheus.NewRegistry()`, чтобы не ловить duplicate registration).
- [ ] **S6. W605 restart-required** — `internal/config/reloadable_warnings.go` (+ константа `WarnWebAuthRestartRequired = "W605"`) и `internal/config/reloadable_webauth.go` по образцу `reloadable_metrics.go`: при изменении `server.web_config_file` или `server.auth.unauthenticated_paths` — `warnRestartRequired`, состояние не меняется. Зарегистрировать там же, где регистрируется metrics-reloadable. Если окажется, что restart-reloadables регистрируются так, что это тянет >~60 строк проводки — остановиться и зафиксировать в `research.md`/BUGS, а не городить.
- [ ] **S7. `cmd/server/main.go`**:
  - флаг `-web.config.file` (help-текст как у upstream), после `flag.Parse` и загрузки конфига: флаг непустой ⇒ перекрывает `cfg.Server.WebConfigFile`;
  - путь непустой ⇒ `application.NewWebAuth(...)`; ошибка ⇒ `slog.Error` + `os.Exit(1)`; `INFO` (D7) с `users`, `unauthenticated_paths`;
  - путь пуст ⇒ `WARN` (текст из Spec D7);
  - `cfg.Webhook.Authentication.Enabled` ⇒ `WARN` (D8);
  - `rootHandler := application.WithRoutePrefix(authWrapped, prefix)` — auth строго внутри префикса;
  - удалить `_ "net/http/pprof"`.
- [ ] **S8. Проверка среза 1**: `go build ./...`, `go vet ./...`, `go test ./internal/application/... ./internal/config/... ./cmd/...`, `git diff --check`; коммит `feat(auth): ...`.

## Срез 2 — Helm, доки, ADR (~0.5d)

- [ ] **S9. Helm** (`helm/amp/`):
  - `values.yaml`: блок `webConfig` (`existingSecret: ""`, `secretKey: web-config.yml`, `mountPath: /etc/amp/web`) с комментарием про `htpasswd -nBC 10`; `probes.liveness.path`/`startup.path` → `/-/healthy`, `probes.readiness.path` → `/-/ready`; то же в `| default` в `templates/deployment.yaml`;
  - `templates/deployment.yaml`: при `webConfig.existingSecret` — volume (secret), `volumeMount` readOnly, env `SERVER_WEB_CONFIG_FILE`;
  - проверить `values-production.yaml` и прочие `values-*.yaml`: если там переопределены пути probes на `/healthz`/`/readyz` — перевести на `/-/healthy`/`/-/ready` (иначе с auth probe получит 401);
  - `helm template` дефолт / с `--set webConfig.existingSecret=amp-web` — глазами и `grep` (AC11). Внимание: `PROD-HELM-CLEAN-CHECKOUT` — если нет `charts/valkey`, сделать `helm dependency build` локально и не коммитить.
- [ ] **S10. Доки**:
  - `docs/CONFIGURATION_GUIDE.md` — раздел «Authentication»: флаг/ключ/env, формат файла, генерация хеша, `unauthenticated_paths`, hot reload и его отклонение от upstream, что даёт ошибку старта, Prometheus `basic_auth` пример, amtool `--http.config.file`, Helm `webConfig`;
  - `docs/ALERTMANAGER_COMPATIBILITY.md` — строка `--web.config.file`: поддержано `basic_auth_users`; `tls_server_config`/`http_server_config`/`rate_limit` ⇒ ошибка старта; отклонения (пустые users, reload без 500, исключения путей). Поправить `:53` «this endpoint is unauthenticated» → «unauthenticated unless web config is set»;
  - `CHANGELOG.md` `[Unreleased]` — Added (auth), Changed (Helm probe paths), Removed (pprof import); дефолт не меняется;
  - `docs/06-planning/DECISIONS.md` — ADR-011 (дефолт off + WARN, свой middleware вместо exporter-toolkit, отклонения D3/D4/D5, отклонённый opt-out);
  - BACKLOG: `PROD-AUTH-BEARER`; в `CONFIG-RELOADER-SIDECAR` (NEXT.md) — заметка «нужны basic-креды или SIGHUP, loopback-исключение запрещено»; в `PROD-INGRESS-HARDENING` — «auth теперь есть, решить обязательность в values-production».
- [ ] **S11. Проверка среза 2**: `helm lint`, `helm template` (AC11), `git diff --check`; коммит `docs(auth): ...` / `feat(helm): ...`.

## Testing (на `/write-tests` и `/testing`)

- [ ] **T1** загрузчик, табличный (AC7): валидный; нет файла; битый YAML; неизвестный ключ; `tls_server_config`; `http_server_config`; `rate_limit`; пустые users; нет `basic_auth_users`; пустое имя; не-bcrypt хеш.
- [ ] **T2** middleware (AC1–3): без заголовка ⇒ 401 + `WWW-Authenticate: Basic` + `missing`; неверный пароль ⇒ 401 + `invalid`; неизвестный пользователь ⇒ 401 + `invalid`; валидные ⇒ `next` вызван; exempt `/-/healthy`, `/-/ready` ⇒ 200 без кредов; `/healthz`, `/metrics` ⇒ 401; кастомный exempt; `/-/healthy/` (слеш) **не** exempt.
- [ ] **T3** route prefix (AC4): `WithRoutePrefix(auth.Wrap(mux), "/am")` — `/am/-/healthy` 200, `/am/api/v2/silences` 401, с кредами — 200.
- [ ] **T4** кэш (AC5): два запроса с теми же кредами ⇒ компаратор вызван один раз; другой пароль ⇒ второй вызов; переполнение кэша не паникует и держит размер ≤ лимита.
- [ ] **T5** hot reload (AC6): смена пароля в файле + сдвиг часов ⇒ старый 401, новый 200; невалидный файл ⇒ старый набор работает, счётчик +1, повторные запросы не увеличивают счётчик/ERROR повторно; удаление файла ⇒ старый набор; проверка не чаще раза в секунду (без сдвига часов изменение не видно).
- [ ] **T6** секреты в логах (AC10): захват `slog` в буфер, прогон отказов и reload — в выводе нет ни пароля, ни хеша, ни `Authorization`.
- [ ] **T7** W605 (S6): изменение `server.web_config_file` через reload ⇒ предупреждение W605 в `RestartWarnings`, состояние не меняется.
- [ ] **T8** интеграционный сквозной на реальном роутере (AC1 целиком): `NewRouter(registry).SetupRoutes(mux)` в существующем тестовом харнессе `internal/application` (если он поднимает registry без БД) — анонимный `POST /api/v2/silences` ⇒ 401, с кредами ⇒ 200. Если харнесс тяжёлый — достаточно T2 + ручной проверки на `/testing`.
- [ ] **T9** ручная проверка на `/testing` (AC1, AC8): lite-инстанс без web-config (WARN в логе, всё открыто) и с web-config (`curl` без/с кредами, `amtool --http.config.file`, смена пароля на лету).
- [ ] `go vet ./...`, `go test ./...` (с учётом `PUBLISHING-WARMUP-TEST-FLAKY`), `go build ./...`, `git diff --check` (AC13).

## Finalization (`/end-task`)
- [ ] AC1–AC13 сверены с фактом
- [ ] `grep -rn 'net/http/pprof' cmd internal` пусто (AC9)
- [ ] NEXT.md → WIP снят, DONE.md, BACKLOG `PROD-AUTH` закрыт, архив `tasks/archive/PROD-AUTH/`
