# Requirements: PROD-AUTH

## Context

Блокер P0 из аудита production readiness (2026-09-21, BACKLOG «Production Readiness — блокеры», рекомендуемый порядок начинается с этой задачи).

HTTP API AMP полностью без аутентификации: в `go-app/cmd/server/main.go` `http.Server.Handler` — голый mux, обёрнутый только в `application.WithRoutePrefix`, никакого auth-middleware. Анонимно доступны:

- `POST /api/v1/alerts`, `POST /api/v2/alerts` — инъекция алертов;
- `POST /api/v2/silences`, `DELETE /api/v2/silence/{id}` — можно заглушить всё;
- `POST /-/reload` — перезагрузка конфига;
- `/api/v1/alerts/{fp}/investigation` — запуск LLM-расследования (стоимость);
- `/dashboard/*`.

Auth-middleware в `internal/application/application.go` (`setupMiddleware`) — мёртвый код, `main` его не вызывает. В `main.go` висит blank-import `net/http/pprof`: сейчас безвреден (свой mux, не `DefaultServeMux`), но откроет pprof анонимно при первом же рефакторинге.

Upstream Alertmanager решает это через `--web.config.file` (`prometheus/exporter-toolkit`): `basic_auth_users` с bcrypt-хешами, TLS; auth применяется ко всем эндпоинтам. Совместимость с этим форматом — естественный путь для drop-in замены.

## Goals

- [ ] Включаемая аутентификация HTTP API: минимум basic auth (bcrypt), плюс bearer token — финальный набор механизмов фиксируется в `/spec`.
- [ ] Формат конфигурации совместим с upstream `web.config` (`basic_auth_users`) там, где это разумно; отклонения — осознанные и задокументированные. Выбор между флагом `--web.config.file`, секцией в основном конфиге и обязательным auth-proxy — на `/research`.
- [ ] Защищены: мутирующие эндпоинты (alerts, silences), `/-/reload`, investigation, dashboard. `/-/healthy`, `/-/ready`, `/metrics` — исключаемы из auth настройкой (для kubelet probes и Prometheus scrape).
- [ ] Судьба мёртвого `setupMiddleware`: переиспользовать или удалить — решить на `/research`.
- [ ] Удалить blank-import `net/http/pprof` из `main.go`.

## Constraints

- Security-задача ⇒ обязательны `/research` и `/spec` до кода (WORKFLOW Research Policy, Stop Conditions).
- Обратная совместимость: без явной настройки auth текущий пилотный деплой не должен сломаться молча. Дефолт (выключен + громкий WARN на старте vs включён) — решение в `/spec`, фиксируется в `DECISIONS.md`.
- Секреты не логировать; сравнение токенов — constant-time; пароли — только bcrypt-хеши в конфиге.
- Совместимость клиентов экосистемы: `amtool`, Grafana, Prometheus (`alertmanagers[].basic_auth` / `authorization`), karma — должны работать с включённым auth через их штатные настройки.
- Не расширять в PROD-INGRESS-HARDENING, PROD-RBAC-SCOPE, PROD-SECURITY-MD (соседние блокеры, отдельные задачи); TLS на уровне процесса — вне скоупа, если `/research` не покажет, что он почти бесплатен вместе с `web.config`.
- Оценка ~2d; при превышении — нарезать (например: middleware + конфиг ⇒ Helm/доки отдельным срезом).

## Success Criteria (Definition of Done)

- [ ] При включённом auth анонимный `POST /api/v2/silences` ⇒ `401`; с валидными credentials ⇒ успех.
- [ ] Тесты на middleware: валидные/невалидные basic и bearer, исключённые пути, работа под `route_prefix`.
- [ ] Health/ready/metrics остаются доступными при соответствующей настройке исключений.
- [ ] Blank-import `net/http/pprof` удалён.
- [ ] Раздел в `CONFIGURATION_GUIDE.md`, строка в `ALERTMANAGER_COMPATIBILITY.md`, запись в `CHANGELOG.md` `[Unreleased]` (с migration notes, если дефолт меняет поведение), Helm values — как минимум способ передать credentials.
- [ ] `go vet` + `go test ./...` зелёные (с учётом известного флейка `PUBLISHING-WARMUP-TEST-FLAKY`); `git diff --check` чистый.
