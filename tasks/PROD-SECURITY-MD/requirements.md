---
id: PROD-SECURITY-MD
slug: prod-security-md
stream: Security
type: docs
priority: high
status: active
created_at: 2026-10-05
updated_at: 2026-10-05
---

# Requirements: SECURITY.md по фактическому состоянию

## Problem Framing

- **Symptom:** `SECURITY.md` обещает то, чего в AMP нет: «Basic API key & JWT support», «TLS support, configurable CORS», чек-лист «Enable authentication (API keys or JWT)». Контакт для уязвимостей — `[INSERT SECURITY EMAIL]` (стр. 17, 152): сообщить об уязвимости некуда. Таблица версий — `1.x.x` поддерживается, `< 1.0` нет, хотя релизов `1.x` не было (теги `v0.0.1`/`v0.0.2`, готовится `v0.1.0`).
- **Root Cause:** файл — шаблон от 2025-12-01, не обновлялся после PROD-AUTH (ADR-011), PROD-RBAC-SCOPE (ADR-012), PROD-CI-IMAGES (ADR-013), PROD-DEPS-VULN, PROD-INGRESS-HARDENING (ADR-015).
- **Why Now:** прод-блокер P0 из `BACKLOG.md` § Production Readiness; репозиторий публичный, файл виден в GitHub Security tab.
- **How We Measure:** каждое утверждение «Current» в `SECURITY.md` подтверждается кодом/чартом/CI со ссылкой; плейсхолдеров нет; канал сообщения рабочий.

## Risk Profile

- **Signals:** `none`
- **Tier:** Lightweight
- **Notes:** меняется только `SECURITY.md` (и при необходимости ссылка из README); кода, чарта и CI не трогаем. Тема — безопасность, но `S` относится к изменению auth/RBAC/секретов в системе, а не к описанию; ошибка в доке откатывается одним коммитом.

## User Stories

1. Как исследователь безопасности, я хочу знать, куда приватно сообщить об уязвимости, чтобы не публиковать её в issue.
2. Как оператор, я хочу видеть, какие защитные механизмы AMP реально даёт, а какие ложатся на меня (TLS, rate limiting), чтобы не полагаться на несуществующие.

## Success Criteria

- [x] Канал сообщения — GitHub Private Vulnerability Reporting (`https://github.com/ipiton/AMP/security/advisories/new`); email не публикуется; `[INSERT SECURITY EMAIL]` нет.
- [x] Supported Versions соответствует реальности (линия `0.x`, патчи — в последний минорный релиз / `main`).
- [x] Auth описан как есть: basic auth по upstream `--web.config.file` (`basic_auth_users`, bcrypt), в чарте — `webConfig.existingSecret`; ссылка на `docs/CONFIGURATION_GUIDE.md` §4. Bearer/JWT/API keys — не заявлены (bearer → `PROD-AUTH-BEARER`).
- [x] TLS: сервер AMP TLS не терминирует — TLS на Ingress/mesh; заявление «TLS support» убрано или уточнено (исходящий `http_config.tls_config` для receivers — отдельно, если упоминается).
- [x] Network: NetworkPolicy AMP (ADR-015), guard Ingress без auth, namespaced RBAC (ADR-012) — как Current.
- [x] Supply chain / testing: govulncheck required в CI, release-gate; убраны неподтверждённые «security scans on every commit», gosec — только если реально запускается.
- [x] Каждое оставшееся «Current» проверено по коду (CORS, rate limiting, audit logging, input validation) — неподтверждённое снято или переведено в Planned/оператор.
- [x] `Last Updated` актуален.

## Non-Goals

- Реализация TLS на сервере, bearer auth, CORS, rate limiting — только описание.
- Изменения кода, чарта, CI.
- Включение Private Vulnerability Reporting в настройках репо — действие владельца (см. Constraints).

## Constraints

- **Scope:** `SECURITY.md`; по необходимости — одна ссылка в `README.md`. Язык — английский (публичный документ).
- **Security:** контакт не должен вести в никуда. Сейчас `gh api repos/ipiton/AMP/private-vulnerability-reporting` → `{"enabled":false}`: владелец должен включить Settings → Code security → Private vulnerability reporting до мержа (или сразу после). На `finalize` — owner-пункт в `NEXT.md` с `Trigger: owner ready`, если к тому моменту не включено.
- **Compatibility:** не применимо.

## Discovery Notes

- Similar tasks: `tasks/archive/DOCS-HONESTY-PASS/`, `tasks/archive/REPO-DOC-LICENSE-DRIFT/` (сверка доков с кодом).
- Relevant patterns: ADR-011 (auth), ADR-012 (RBAC), ADR-013 (CI), ADR-015 (ingress/NetworkPolicy) в `docs/06-planning/DECISIONS.md`; `docs/CI.md` (required checks, govulncheck); `docs/CONFIGURATION_GUIDE.md` §4.
- Предварительно по коду:
  - серверный TLS (`tls_server_config`) не найден; `tls_config` есть только в исходящем `http_config` (`go-app/internal/core/http_client_config.go`);
  - CORS-заголовки только в `go-app/internal/application/middleware.go` — по PROD-AUTH `setupMiddleware` мёртвый код, `main` его не вызывает; проверить на `implement`;
  - rate limiting встречается в `cmd/server/handlers/silence_advanced.go`, `dashboard_ws.go` — проверить, что именно ограничивается и включено ли.
- Open unknowns: состояние CORS/rate limiting/audit logging (снимается на `implement` чтением кода); кто и когда включает Private Vulnerability Reporting.

## Research (mini)

- Source: `SECURITY.md`, `BACKLOG.md` § PROD-SECURITY-MD, `gh api` (репо public, PVR выключен).
- Key finding: расхождения — auth (API key/JWT), TLS, CORS, версии, контакт; NetworkPolicy и RBAC теперь есть, а в файле «Planned».
- Decision: канал — GitHub Private Vulnerability Reporting (решение владельца 2026-10-05); переписать файл по фактам со ссылками на ADR/доки.

## Verification (2026-10-05)

| Check | Result |
|---|---|
| `git diff --check main...HEAD` | PASS |
| Плейсхолдеры (`INSERT`/`TODO`/`TBD`) в `SECURITY.md` | нет; `JWT`/`API key` — только в «Not provided» и чек-листе секретов |
| Относительные ссылки `SECURITY.md`, `README.md` | 11/11 существуют |
| Утверждения «Provided» сверены с кодом/чартом/CI | auth — `cmd/server/main.go` (`webAuth.Wrap`), `CONFIGURATION_GUIDE.md` §4; TLS сервера нет, `tls_server_config`/`rate_limit` отвергаются; CORS — `internal/application/middleware.go`, не подключён (`main` не вызывает `setupMiddleware`); `automountServiceAccountToken: false` — redis, backup cronjob; securityContext — `helm/amp/values.yaml`; CI — `docs/CI.md`, `.github/workflows/ci.yml`; gosec в `.golangci.yml` нет |
| Private Vulnerability Reporting | `{"enabled":false}` — включает владелец |
| Go/Helm гейты (`quality-gates-fast`, `release-gate.sh`) | SKIP: дифф только markdown, Go-код и чарт не тронуты (`WORKFLOW.md` § Гейты AMP требует release-gate для Go/чарта) |

Отклонение: критерий про supply chain выполнен без слова «required» — branch protection не включён (`MAIN-BRANCH-PROTECTION`), required-статус не enforced.
