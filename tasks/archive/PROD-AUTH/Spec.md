# Spec: PROD-AUTH

Дата: 2026-09-25. Ветка `feature/prod-auth`. Источники: `requirements.md`, `research.md`. Пути ниже — относительно `go-app/`, если не указано иное.

## Проблема

HTTP API AMP полностью открыт. `cmd/server/main.go` отдаёт в `http.Server` голый mux, обёрнутый только в `WithRoutePrefix`. Кто угодно, у кого есть сетевой доступ к порту, может заглушить все алерты (`POST /api/v2/silences`), подделать алерты, дёрнуть `/-/reload` или запустить платное LLM-расследование (research F3). Auth-middleware в коде нет даже в мёртвом виде (F1), а ключ `webhook.authentication.*` обещает защиту, которую никто не применяет (F2).

Upstream Alertmanager закрывает это через `--web.config.file` с `basic_auth_users` (bcrypt). Пользователь, мигрирующий с upstream, ожидает, что этот же файл сработает и здесь.

## Цели

1. Включаемая basic-аутентификация всего HTTP API, формат файла совместим с upstream `web.config` (`basic_auth_users`).
2. Настраиваемый список путей без аутентификации (probes), по умолчанию — только не раскрывающие внутреннее состояние.
3. Смена паролей без рестарта (ротация Secret в Kubernetes), без возможности «открыть» сервер правкой файла.
4. Без `web.config` поведение не меняется (пилоты не ломаются), но это громко видно в логе.
5. Helm умеет подключить Secret с web-config.
6. Удалён blank-import `net/http/pprof`.

## Не-цели

- **Bearer-токены.** Upstream их на сервере не принимает, все клиенты экосистемы работают на basic (research F5). → BACKLOG `PROD-AUTH-BEARER`, если появится use case.
- **TLS на уровне процесса** (`tls_server_config`). Терминация на Ingress/mesh. Ключ в файле ⇒ ошибка старта (D3).
- `http_server_config`, `rate_limit` из upstream-формата ⇒ ошибка старта (D3).
- Авторизация (роли, read-only пользователи). Любой аутентифицированный пользователь может всё — как у upstream.
- Реанимация или удаление `Application`/`MiddlewareStack`, CORS, `webhook.*` security-ключей → BACKLOG `DEAD-APPLICATION-MIDDLEWARE`, `DEAD-WEBHOOK-SECURITY-CONFIG`.
- Ingress-аннотации, NetworkPolicy, обязательность auth в `values-production.yaml` → `PROD-INGRESS-HARDENING`.
- Доработка config-reloader sidecar под auth → в описание `CONFIG-RELOADER-SIDECAR` (кода sidecar ещё нет).
- `PROD-GRACEFUL-SHUTDOWN` — в `main.go` рядом, но не трогаем порядок shutdown.

## Ключевые решения

### D1. Собственный middleware, не `exporter-toolkit/web.Serve`

`exporter-toolkit` оборачивает весь сервер без исключений путей (probes ломаются), тянет 5–7 новых модулей и забирает Serve-цикл, который `PROD-GRACEFUL-SHUTDOWN` будет переделывать (research F4, вариант A). Пишем свою обёртку `http.Handler` в `internal/application/webauth.go` (рядом с `route_prefix.go`, тот же стиль: чистая функция-обёртка + тесты). Поведение переносим из upstream `webHandler` (D6). Новых модулей нет: `golang.org/x/crypto/bcrypt` уже в графе как indirect и станет direct.

`Application.setupMiddleware`/`MiddlewareStack` не используем: это отдельный мёртвый граф объектов (F1).

### D2. Откуда берётся путь к файлу

Приоритет, по образцу `-web.route-prefix` / `server.route_prefix`:

1. флаг `-web.config.file` (имя upstream; Go `flag` принимает и `--web.config.file`);
2. ключ `server.web_config_file` в основном конфиге; через `AutomaticEnv` — переменная `SERVER_WEB_CONFIG_FILE` (для Helm, где контейнер запускается без args).

Пустой путь ⇒ auth выключен (D7).

### D3. Формат файла и валидация

```yaml
basic_auth_users:
  alice: $2y$10$...   # bcrypt
  prometheus: $2y$10$...
```

- Строгий YAML-парсинг (неизвестные ключи ⇒ ошибка).
- Ключи upstream, которые AMP не реализует — `tls_server_config`, `http_server_config`, `rate_limit` — ⇒ отдельная понятная ошибка: `web config: "tls_server_config" is not supported by AMP; terminate TLS at the ingress or service mesh`. Молча проигнорированный TLS — ложная безопасность.
- Каждый пароль — валидный bcrypt-хеш (`bcrypt.Cost` без ошибки), имя пользователя непустое.
- `basic_auth_users` пуст или отсутствует ⇒ **ошибка** (отклонение от upstream, где пустой список = auth выключен). Указанный файл без пользователей — почти наверняка ошибка оператора, а не намерение открыть API.
- **На старте:** файл не читается или невалиден ⇒ `os.Exit(1)` с ошибкой (fail-fast, как upstream).

### D4. Hot reload файла

- Перед обработкой запроса middleware проверяет файл не чаще раза в секунду: `os.Stat` (следует симлинкам, так что подмена Secret через `..data`-симлинк kubelet видна) и сравнение `ModTime` + `Size` с загруженными. При изменении — перечитать и провалидировать по D3.
- Новый файл валиден ⇒ атомарно подменить набор пользователей, `INFO` с числом пользователей (без имён), кэш bcrypt сбросить.
- Новый файл невалиден, пуст или пропал ⇒ **оставить предыдущий валидный набор**, `ERROR` (не чаще раза за изменение файла), метрика (D8). Отклонение от upstream (там — `500` на всё): мы никогда не открываемся и не роняем ingest из-за опечатки в Secret.
- Выключить auth в рантайме нельзя — только рестартом без флага/ключа.
- `server.web_config_file` меняется только рестартом: middleware получает путь один раз при сборке сервера. Если путь поменяли в конфиге и сделали `/-/reload` — `WARN` «requires restart» (через существующий механизм «restart required» полей, если он легко расширяется; иначе — просто документируем).

### D5. Пути без аутентификации

- Ключ `server.auth.unauthenticated_paths` (список строк), по умолчанию `["/-/healthy", "/-/ready"]`.
- Сравнение — **точное** по `r.URL.Path` после снятия route prefix (D9). Без префиксов и glob: `ServeMux` чистит `..` через 301, повторный запрос снова проходит auth, так что точное сравнение безопасно (research F7).
- `/healthz`, `/readyz`, `/health`, `/ready` по умолчанию **защищены**: они отдают JSON-отчёт о компонентах. `/-/healthy` и `/-/ready` проверяют ту же `Liveness`/`Readiness`, но отвечают plain-text (`internal/application/handlers/status.go`).
- `/metrics` по умолчанию защищён, как у upstream. Prometheus скрейпит с `basic_auth`, или оператор добавляет `/metrics` в список.
- Список применяется только при включённом auth.

### D6. Алгоритм проверки (перенос из upstream `webHandler`)

1. Путь в `unauthenticated_paths` ⇒ пропустить.
2. `r.BasicAuth()`; нет заголовка ⇒ 401.
3. Пользователь не найден ⇒ сравнивать с фиксированным bcrypt-хешем-заглушкой (защита от enumeration по времени), результат всегда `false`.
4. Кэш результатов: ключ — `sha256(user \x00 hash \x00 password)` (в отличие от upstream, пароль в открытом виде в памяти как ключ не держим), значение — `bool`. Размер ≤ 100, при переполнении случайное вытеснение ~10% (как upstream `cache.go`). Сбрасывается при reload (D4).
5. `bcrypt.CompareHashAndPassword` под мьютексом (CPU-тяжёлая, как upstream).
6. Отказ ⇒ `401`, заголовок `WWW-Authenticate: Basic`, тело `Unauthorized` (plain-text, как `http.Error` у upstream).

Сравнение пароля — только через bcrypt (он constant-time по построению); строковых сравнений секретов нет.

### D7. Дефолт: выключено + громкий WARN

Без `web.config` auth выключен, как у upstream. На старте — `WARN`: `HTTP API authentication is DISABLED: anyone with network access can create silences, post alerts and trigger reloads. Set -web.config.file or server.web_config_file.` Включено ⇒ `INFO` `HTTP API basic authentication enabled` с `users=<N>`, `unauthenticated_paths=[...]`.

Включать по умолчанию нельзя: пароль некому сгенерировать, все пилоты упадут. Фиксируется в `DECISIONS.md` (ADR-011).

Явный отказ от auth (`server.auth.disabled: true`, глушащий WARN) рассмотрен и отклонён на согласовании Spec (2026-09-25): сервис без auth — это просто отсутствие web-config, WARN остаётся всегда. Случаи «auth снаружи» (Ingress/oauth2-proxy, mesh, NetworkPolicy) живут с одной WARN-строкой на старте.

### D8. Наблюдаемость

- `amp_http_auth_failures_total{reason}`, `reason ∈ {missing, invalid}`. Имя пользователя в лейблы не попадает (кардинальность, утечка).
- `amp_http_auth_config_reload_failures_total` — невалидный новый файл при hot reload (D4).
- Логи: отказ — `DEBUG` с `path`, `remote_addr`, `user` (имя допустимо, пароль/заголовок — никогда).
- `webhook.authentication.enabled: true` ⇒ `WARN` на старте: `webhook.authentication.* is not enforced; use -web.config.file`.

### D9. Место в цепочке

`main.go`: `WithRoutePrefix(webAuth(mux), prefix)` — auth внутри префикса, `unauthenticated_paths` пишутся без префикса и не зависят от `route_prefix`. Запросы вне префикса (редирект `/` → `prefix/`, 404) обрабатывает внешний mux без auth — они ничего не раскрывают.

### D10. Helm

- Values:
  ```yaml
  webConfig:
    existingSecret: ""        # Secret с ключом secretKey; пусто ⇒ auth выключен
    secretKey: web-config.yml
    mountPath: /etc/amp/web
  ```
  Чарт Secret не создаёт (bcrypt-хеш должен сгенерировать оператор: `htpasswd -nBC 10 user`).
- При заданном `existingSecret`: volume из Secret, `volumeMount` (readOnly), env `SERVER_WEB_CONFIG_FILE=<mountPath>/<secretKey>`.
- Дефолтные пути probes меняются на `/-/healthy` (liveness, startup) и `/-/ready` (readiness): семантика та же (`Liveness`/`Readiness`), зато они в дефолтном `unauthenticated_paths`. Явно заданные `probes.*.path` не трогаем.
- `helm template` с дефолтами и с `webConfig.existingSecret` — проверяется на `/testing`.

## Scope (файлы)

| Файл | Изменение |
|---|---|
| `internal/application/webauth.go` (+ `_test.go`) | загрузка/валидация файла (D3), hot reload (D4), middleware (D5, D6), метрики (D8) |
| `internal/config/config.go` | `ServerConfig.WebConfigFile`, `ServerConfig.Auth.UnauthenticatedPaths` + defaults |
| `cmd/server/main.go` | флаг `-web.config.file`, fail-fast загрузка, подключение по D9, WARN/INFO (D7), WARN про `webhook.authentication` (D8), удалить `_ "net/http/pprof"` |
| `go.mod` | `golang.org/x/crypto` indirect → direct |
| `helm/amp/values.yaml`, `templates/deployment.yaml` | D10 |
| `docs/CONFIGURATION_GUIDE.md` | раздел «Authentication» |
| `docs/ALERTMANAGER_COMPATIBILITY.md` | строка про `--web.config.file` (что поддержано, что ошибка старта, отклонения D3/D4/D5); исправить формулировку `:53` «this endpoint is unauthenticated» |
| `CHANGELOG.md` `[Unreleased]` | Added + заметка, что дефолт не меняется; изменение путей probes в Helm |
| `docs/06-planning/DECISIONS.md` | ADR-011 |

## Acceptance Criteria

1. Auth включён: анонимный `POST /api/v2/silences` ⇒ `401` + `WWW-Authenticate: Basic`; с валидными кредами ⇒ как без auth.
2. Неверный пароль и неизвестный пользователь ⇒ `401`; `amp_http_auth_failures_total{reason="invalid"}` растёт; без заголовка — `reason="missing"`.
3. `/-/healthy`, `/-/ready` без кредов ⇒ `200`; `/healthz`, `/metrics` без кредов ⇒ `401`; путь, добавленный в `unauthenticated_paths`, ⇒ пропускается.
4. С `route_prefix: /am`: `/am/-/healthy` без кредов ⇒ `200`, `/am/api/v2/silences` без кредов ⇒ `401`.
5. Повторный запрос с теми же кредами не вызывает bcrypt повторно (тест через счётчик/инъекцию компаратора).
6. Hot reload: смена пароля в файле подхватывается без рестарта (старый ⇒ 401, новый ⇒ OK); запись невалидного файла ⇒ продолжает работать предыдущий набор, растёт `amp_http_auth_config_reload_failures_total`.
7. Старт с невалидным файлом (не bcrypt, пустые пользователи, `tls_server_config`, неизвестный ключ, нет файла) ⇒ процесс не стартует, ошибка называет причину. Покрыто табличным тестом загрузчика.
8. Без web-config: поведение как сейчас, в логе `WARN` про выключенный auth.
9. Blank-import `net/http/pprof` удалён; `grep -rn 'net/http/pprof' cmd internal` пусто.
10. Секреты не логируются: тест или ревью лог-вызовов в `webauth.go` — ни пароля, ни `Authorization`.
11. `helm template` с `webConfig.existingSecret` рендерит volume, mount и `SERVER_WEB_CONFIG_FILE`; без него — нет; probes по умолчанию на `/-/healthy`, `/-/ready`.
12. Документация и CHANGELOG по таблице Scope; ADR-011 в `DECISIONS.md`.
13. `go vet ./...`, `go test ./...` (с учётом известного `PUBLISHING-WARMUP-TEST-FLAKY`), `go build`, `git diff --check` — зелёные.

## Риски

| Риск | Смягчение |
|---|---|
| bcrypt на горячем пути ingest (Prometheus шлёт каждый eval-цикл) | кэш (D6.4), AC5 |
| Timing/enumeration пользователей | хеш-заглушка (D6.3) |
| Опечатка в Secret при ротации роняет ingest | reload сохраняет прежний набор (D4), метрика + ERROR |
| Смена путей probes в Helm ломает кастомные деплои | меняются только дефолты; явно заданные пути не трогаем; строка в CHANGELOG |
| Будущий config-reloader sidecar получит 401 на `/-/reload` | заметка в `CONFIG-RELOADER-SIDECAR`: basic-креды или SIGHUP; loopback-исключение запрещено (research F6) |
| Отклонения от upstream (пустые пользователи ⇒ ошибка, reload не 500, исключения путей) | явно перечислены в `ALERTMANAGER_COMPATIBILITY.md` |

## Нарезка

Одна задача, два коммита-среза, мержится целиком:

1. Go: `webauth.go` + конфиг + `main.go` + тесты (AC1–10, 13). ~1d.
2. Helm + доки + ADR (AC11–12). ~0.5d.

Если срез 1 выйдет за ~1.5d — срез 2 выносится в отдельную задачу `PROD-AUTH-HELM-DOCS`, а срез 1 мержится с минимальной записью в CHANGELOG.
