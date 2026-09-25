# Research: PROD-AUTH

Дата: 2026-09-25. Ветка `feature/prod-auth`.

## Вопросы из requirements

1. Как подключать auth: `--web.config.file` (upstream), секция основного конфига или обязательный auth-proxy?
2. Судьба мёртвого `setupMiddleware`.
3. Бесплатен ли TLS вместе с `web.config`?
4. Какие механизмы (basic / bearer) и какие пути исключать.

## Findings

### F1. Серверная обвязка: auth-слоя нет, и «наличие» middleware — иллюзия

- `go-app/cmd/server/main.go`: `http.Server.Handler = application.WithRoutePrefix(mux, prefix)` — больше ничего. Маршруты регистрируют `application.Router.SetupRoutes` (`internal/application/router.go`) и `registerLegacyDashboardRoutes` (`cmd/server/legacy_dashboard.go`: `/`, `/dashboard*`, `/static/`).
- Весь тип `application.Application` (`internal/application/application.go`: `Run` → `setupMiddleware` → `startServer`) — мёртвый: конструктор нигде не вызывается, даже комментарий-пример `application.New(config)` ссылается на несуществующую функцию. Вместе с ним мёртв `MiddlewareStack` (`internal/application/middleware.go`): recovery, debug-логирование, пустой `metricsMiddleware` с TODO, CORS и `// TODO: Authentication`.
- Побочная находка: **`server.cors.*` — мёртвый конфиг.** CORS-middleware реализован и покрыт тестом (`middleware_cors_test.go`), но в прод-пути не участвует. Панический recovery в прод-пути тоже отсутствует (net/http сам ловит панику хендлера и рвёт соединение — не катастрофа).
- Blank-import `net/http/pprof` в `main.go`: сейчас pprof не экспонирован (свой `ServeMux`, не `DefaultServeMux`), но включится анонимно при любой регистрации на `DefaultServeMux` или `Handler: nil`.

### F2. Конфиг, который обещает auth, но не применяется

`internal/config/config.go`: `webhook.authentication.{enabled,type,api_key,jwt_secret}` (дефолты `:941-943`) — парсится, валидируется (`update_validator.go:562`: при `enabled` требует ключ), редактируется в выдаче (`sanitizer.go:47-48`), но **нигде не проверяется в HTTP-пути**. Оператор, выставивший `webhook.authentication.enabled: true`, получает ложное ощущение защиты. Та же картина у `webhook.signature.*` и `webhook.rate_limiting.*` (rate limiting включён по умолчанию «на бумаге»). В Helm-values и публичных доках эти ключи не упоминаются — поверхность поломки для пользователей минимальна.

### F3. Анонимная поверхность (с учётом F1)

| Путь | Риск без auth |
|---|---|
| `POST /api/v1/alerts`, `POST /api/v2/alerts` | инъекция/подделка алертов |
| `POST /api/v2/silences`, `DELETE /api/v2/silence/{id}` | заглушить всё / снять чужие silences |
| `POST /-/reload` | принудительный reload (DoS, гонки с правкой ConfigMap) |
| `/api/v1/alerts/{fp}/investigation` | запуск LLM-расследования (прямые деньги) |
| `GET /api/v2/status` | конфиг (секреты редактированы — `ALERTMANAGER_COMPATIBILITY.md:53` прямо говорит «this endpoint is unauthenticated») |
| `/`, `/dashboard*`, `/static/` | чтение алертов/silences/LLM-выводов |
| `/healthz`, `/readyz`, `/health`, `/ready` | JSON `ReadinessReport` — перечень компонентов и их состояние (информационная утечка) |
| `/-/healthy`, `/-/ready` | plain `OK` / `NOT READY` — утечки нет |
| `/health/reload` | статус последнего reload |
| `/metrics` | метрики (upstream их защищает наравне со всем) |

### F4. Как это делает upstream (`prometheus/exporter-toolkit` v0.15.1, в модульном кэше)

- Флаг `--web.config.file`; файл: `tls_server_config`, `http_server_config` (`http2`, `headers`), `rate_limit`, `basic_auth_users: {user: <bcrypt>}`.
- `web.Serve` оборачивает **весь** `server.Handler` в `webHandler`: auth применяется ко всем путям, включая `/-/healthy` и `/metrics`, исключений нет.
- **Только basic auth**, bearer-серверной аутентификации нет вовсе.
- Файл перечитывается на каждом запросе ⇒ ротация паролей без рестарта; невалидный файл ⇒ `500` на всё (fail-closed).
- Защита от перебора/тайминга: для неизвестного пользователя сравнение с фиксированным bcrypt-хешем; результаты bcrypt кэшируются (ключ — user+hash+password), `bcrypt.CompareHashAndPassword` сериализован мьютексом.
- При старте `validateUsers` проверяет, что все пароли — валидные bcrypt-хеши.
- Зависимости пакета `web`: `go-systemd`, `mdlayher/vsock`, `x/time`, `x/sync`, `golang-jwt` (indirect), `kingpin` (только `kingpinflag`). Релиз `v0.15.1` — 2026-01-01, карантин пройден. `golang.org/x/crypto` (bcrypt) у AMP уже есть как indirect.

### F5. Клиенты экосистемы — все умеют basic auth

Prometheus (`alertmanagers[].basic_auth`), Grafana (Alertmanager datasource), `amtool` (`--http.config.file`), karma (креды в URI/headers) — basic поддерживают все. Bearer (`authorization`) тоже поддерживают Prometheus и amtool, но **upstream его не принимает**, так что ни один мигрирующий пользователь на bearer не рассчитывает. Дашборд AMP: браузер покажет basic-диалог на `WWW-Authenticate: Basic` и сам приложит креды к same-origin `fetch` — дашборд продолжит работать без правок JS.

### F6. Kubernetes-сторона

- Probes в `helm/amp/templates/deployment.yaml:163-187`: `/healthz` (liveness, startup), `/readyz` (readiness) на основном порту. kubelet умеет basic только через статический `httpHeaders` в spec пода (креды в plain-виде в Deployment) — неприемлемо ⇒ probe-пути нужно исключать из auth. Upstream-совместимые `/-/healthy` и `/-/ready` для этого подходят лучше: они не отдают JSON-отчёт (F3).
- Будущий config-reloader sidecar (`CONFIG-RELOADER-SIDECAR`, только values-шейп, `values.yaml:601-800`) дёргает `POST /-/reload` и `GET /health/reload`. С включённым auth ему понадобятся креды (или SIGHUP через `shareProcessNamespace` — SIGHUP-обработчик в `main.go` есть). **Исключение по loopback-адресу недопустимо**: `kubectl port-forward` тоже приходит с `127.0.0.1` внутри netns пода.
- Прометей скрейпит `/metrics` (ServiceMonitor) — либо basic auth в ServiceMonitor, либо исключение `/metrics`.

### F7. Взаимодействие с `route_prefix`

`WithRoutePrefix` (`internal/application/route_prefix.go`) — внешний mux со `StripPrefix`. Если auth ставить **внутрь** (`WithRoutePrefix(auth(mux), prefix)`), список исключений матчится по путям без префикса и не зависит от `route_prefix`. Матчинг исключений — только точное совпадение `r.URL.Path` (никаких префиксов/glob): `ServeMux` чистит `..` через 301-редирект, повторный запрос снова проходит auth, так что точное сравнение безопасно.

## Options

### A. Использовать `exporter-toolkit/web.Serve` как есть
+ Буквальная совместимость с upstream, TLS и rate limit «бесплатно», hot reload файла.
− Нет исключений путей ⇒ probes ломаются (F6) или креды уходят в pod spec. Нет bearer. +5–7 новых модулей (systemd, vsock, …). Serve-цикл уходит в библиотеку — конфликтует с предстоящей переделкой shutdown (`PROD-GRACEFUL-SHUTDOWN`).

### B. Собственный middleware, формат файла совместим с upstream (рекомендуется)
- Флаг `--web.config.file` (как у upstream; в духе `-web.route-prefix`) + ключ `server.web_config_file` в основном конфиге; флаг побеждает.
- Файл: `basic_auth_users` (bcrypt), строгий парсинг. Upstream-ключи, которые AMP не реализует (`tls_server_config`, `http_server_config`, `rate_limit`), ⇒ **ошибка старта** с понятным текстом, а не молчаливое игнорирование (молча проигнорированный TLS — ложная безопасность).
- AMP-специфичное (список исключённых путей) — в основном конфиге (`server.auth.*` или рядом), чтобы файл `web.config` оставался переносимым в upstream.
- Поведение переносим с upstream: фиктивный хеш для неизвестного пользователя, кэш результатов bcrypt, мьютекс, `WWW-Authenticate: Basic`, `401`.
- Новых модулей ноль (`x/crypto/bcrypt` уже в графе, станет direct).
+ Исключения probe-путей, контроль над Serve-циклом, минимальный граф зависимостей.
− TLS не входит (терминация на Ingress/mesh — документируем); свой код безопасности, нужны хорошие тесты.

### C. Только документированный auth-proxy (oauth2-proxy / Ingress basic-auth)
+ Кода почти нет.
− Не drop-in: upstream-пользователь с `--web.config.file` мигрирует и теряет защиту. Pod остаётся открытым внутри кластера (обход Ingress, NetworkPolicy пока нет — `PROD-INGRESS-HARDENING`). Не закрывает критерий «анонимный POST → 401».

## Recommendation

**Вариант B.** По вопросам из requirements:

1. **Подключение:** `--web.config.file` + `server.web_config_file`, upstream-совместимый `basic_auth_users`. Auth-proxy остаётся допустимым дополнением, но не заменой.
2. **`setupMiddleware` / `Application` / `MiddlewareStack`:** не переиспользовать для auth — это отдельный мёртвый граф объектов с TODO. Auth — отдельный `http.Handler`-обёртка в `internal/application` (рядом с `route_prefix.go`), подключается в `main.go`. Удаление мёртвого `Application`/`MiddlewareStack` и «оживление» `server.cors` — **вне скоупа**, завести в BACKLOG (см. «Изменения скоупа»).
3. **TLS:** не бесплатен при варианте B (см. A) ⇒ вне скоупа; ключ `tls_server_config` в файле — ошибка старта; TLS терминируется на Ingress/mesh, это документируется.
4. **Механизмы:** только **basic** в этом срезе. Bearer upstream не поддерживает, все клиенты экосистемы работают на basic (F5). Bearer — в BACKLOG, если появится use case (машинные клиенты без bcrypt-пользователя).
5. **Исключения:** настраиваемый список точных путей. Дефолт при включённом auth — предлагаю `/-/healthy`, `/-/ready` (plain-text, без утечки). Как быть с `/healthz`/`/readyz` (их используют Helm-probes, но они отдают JSON-отчёт) и `/metrics` — решение в `/spec` (варианты: перевести Helm-probes на `/-/healthy`/`/-/ready`; или исключить `/healthz`/`/readyz` по умолчанию; `/metrics` по умолчанию защищён, как у upstream).

## Решения для `/spec`

- **Дефолт без `web.config`:** auth выключен (как у upstream) + громкий `WARN` на старте. Включение по умолчанию сломает все пилотные деплои без выигрыша: генерировать пароль некому. Прод-профиль Helm (`values-production.yaml`) — требовать ли web-config там, решить вместе с `PROD-INGRESS-HARDENING`; в этой задаче — минимум «способ передать Secret с web-config и флаг».
- **Hot reload файла:** upstream перечитывает на каждый запрос. Предлагаю: загрузка и валидация на старте (fail-fast), подхват изменений по SHA/mtime-проверке с дешёвым кэшем; невалидный новый файл ⇒ оставить предыдущий валидный + `ERROR` + метрика (fail-closed в смысле «никогда не открываем», но не `500` на всё, как upstream). Точную семантику зафиксировать в Spec.
- **`webhook.authentication.*` (F2):** не реализовывать. Минимум в этой задаче — `WARN` на старте, если `enabled: true` («не применяется, используйте `--web.config.file`»); удаление ключей — в BACKLOG вместе с прочим мёртвым `webhook.*`.
- **Метрики:** `amp_http_auth_failures_total` (без имени пользователя в лейблах — кардинальность и утечка).
- **Логи:** никогда не логировать заголовок `Authorization`/пароль; имя пользователя при отказе — допустимо на `DEBUG`.
- **Helm:** `webConfig.existingSecret` → volume + `--web.config.file`; probes/ServiceMonitor согласовать с дефолтным списком исключений.

## Risks

- **bcrypt на горячем пути ingest.** Prometheus шлёт алерты каждый eval-цикл; bcrypt cost 10 ≈ 50–100 мс, сериализованный мьютексом ⇒ без кэша ingest деградирует. Кэш обязателен (как у upstream); тест на повторный запрос без повторного bcrypt.
- **Timing / enumeration** — фиктивный хеш для неизвестного пользователя (перенять у upstream).
- **Сломать пилоты** — снимается дефолтом «выключено + WARN».
- **Config-reloader sidecar** (будущая задача) должен уметь basic auth или SIGHUP — зафиксировать в её описании.
- **CORS preflight** не несёт кредов — при оживлении CORS `OPTIONS` придётся пропускать до auth. Сейчас CORS мёртв (F1), риск отложенный — отметить в BACKLOG-пункте про CORS.

## Изменения скоупа

Скоуп **сузился**:
- bearer → BACKLOG (в requirements был «плюс bearer»);
- TLS → явно вне скоупа (ошибка старта на `tls_server_config`);
- `setupMiddleware` не переиспользуется.

Новые находки для BACKLOG (не расширяют задачу):
- `DEAD-APPLICATION-MIDDLEWARE` — мёртвые `Application`/`MiddlewareStack`; `server.cors.*` и recovery не работают в прод-пути.
- `DEAD-WEBHOOK-SECURITY-CONFIG` — `webhook.authentication|signature|rate_limiting.*` парсятся и валидируются, но не применяются.

Оценка остаётся ~2d: middleware + конфиг + тесты ~1d, Helm + доки ~0.5–1d. При превышении — резать по этой границе.
