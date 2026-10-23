---
id: EMAIL-RENDER-ESCAPING
slug: email-render-escaping
stream: Publishing
type: bug
priority: high
status: active
created_at: 2026-10-23
updated_at: 2026-10-23
---

# Requirements: экранирование HTML и Subject в email

Запись `BUGS.md` `EMAIL-RENDER-ESCAPING`, находки CODE-REVIEW-2026-10-21 H-3, L-1.

## Problem Framing

- **Symptom:** аннотация алерта с `<script>`/`<a href=…>` попадает в HTML письма как разметка; `runbook_url: javascript:…` становится рабочей ссылкой кнопки «View Runbook»; `alertname` с `\r\n` разрывает заголовок `Subject` и добавляет в письмо произвольные заголовки.
- **Root Cause:** `EnhancedEmailPublisher` рендерит subject/html/text одним `renderTemplate` на `text/template` (без контекстного экранирования); `buildMIMEMessage` пишет `Subject` без `sanitizeHeaderValue` (её получают только extra headers).
- **Why Now:** high-позиция конвейера ревью (`REVIEW-PIPELINE-2026-10-21.md` №3).
- **How We Measure:** тесты на HTML-часть и на сырое MIME-сообщение (см. Success Criteria).

## Risk Profile

- **Signals:** `S R`
  - `S` — HTML/ссылки и заголовки письма из данных, присланных в `/api/v2/alerts`.
  - `R` — меняется вывод письма (экранирование значений в HTML).
- **Tier:** Full
- **Notes:** `S` ⇒ Full и обязательный deep-review. Конфиг и API не меняются.

## User Stories

1. Как дежурный, я хочу, чтобы письмо об алерте показывало присланные значения как текст, а не исполняло их разметку и ссылки.
2. Как владелец почтового домена, я хочу, чтобы данные алерта не могли добавить заголовки письма.

## Success Criteria

- [ ] HTML-часть fallback-рендера экранирует значения: `<script>` в аннотации — `&lt;script&gt;`, `javascript:` в `runbook_url` — не попадает в `href`.
- [ ] Subject и text-часть остаются plain text (без HTML-экранирования).
- [ ] `Subject` в MIME-сообщении без CR/LF для ASCII и не-ASCII темы; в сообщении нет заголовка, внедрённого через `alertname`.
- [ ] Дефолтный HTML-шаблон (`defaults.DefaultEmailHTML`) рендерится через `html/template` без ошибок; алерт без `instance`/`severity`/`description` рендерится (`default`), а не роняет письмо.

## Non-Goals

- Удаление fallback-пути и перевод email целиком на `business/templating` (крупнее, меняет поведение при `publishing.templates.enabled=false`).
- Валидация CR/LF в лейблах на ingest (upstream их допускает).

## Constraints

- **Scope:** `internal/infrastructure/publishing/email_publisher_enhanced.go`, `email_client.go`, тесты; `CHANGELOG.md`.
- **Security:** значения алерта не меняют структуру HTML и заголовков письма.
- **Compatibility:** конфиг не меняется. Пользовательский `html_template` (header таргета), который полагался на вставку сырого HTML из данных, теперь получит экранированный текст; шаблон с HTML-ошибкой контекста (`html/template` отвергает, например, действие внутри `<script>` с неоднозначным контекстом) даёт ошибку рендера — фиксируется в CHANGELOG.

## Discovery Notes

- Similar tasks: `tasks/archive/PUBLISH-QUICK-FIX-BUNDLE/` (секреты/инъекции в publishing).
- Relevant patterns: `business/templating` — один источник шаблона парсится в `text` и `html` экземпляры (`template.go:129`); `template_formatter.go:494` — HTML-поле email через `ExecuteHTMLString`.
- Open unknowns: нет.
