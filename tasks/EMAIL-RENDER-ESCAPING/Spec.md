---
id: EMAIL-RENDER-ESCAPING
slug: email-render-escaping
type: bug
status: draft
created_at: 2026-10-23
updated_at: 2026-10-23
based_on:
  - requirements.md
  - research.md
---

# Spec: экранирование HTML и Subject в email

## Design Premises

| Premise | Как проверено | Класс | Если неверна |
|---|---|---|---|
| `html/template.FuncMap` совместим с `emailTemplateFuncs` | `go/src/html/template/template.go:332` (`type FuncMap = template.FuncMap`) | verified | не компилируется — видно сразу |
| `DefaultEmailHTML` валиден для `html/template` | чтение шаблона: одно действие в `<style>` выводит константы; тест рендера | verified (тест) | дефолтное письмо перестанет рендериться — ловит тест |
| CR/LF в значениях лейблов доходят до шаблона Subject | `core/interfaces.go:258` проверяет только длину | call-path-traced | фикс всё равно нужен для overlay-пути |
| Overlay HTML уже экранирован | `template_formatter.go:494-504` (`htmlFields`, `ExecuteHTMLString`) | call-path-traced | вне scope, отдельный баг |

## Design

1. `renderEmailContent`: HTML рендерится новым `renderHTMLTemplate` (`html/template`, `emailTemplateFuncs`); subject и text — существующий `renderTemplate` (`text/template`).
2. `buildMIMEMessage`: `Subject: mime2047Subject(sanitizeHeaderValue(msg.Subject))`.
3. `CHANGELOG.md` [Unreleased] Security.

## Acceptance

- [ ] Аннотация `<script>alert(1)</script>` ⇒ в HTML `&lt;script&gt;`, в text — как есть.
- [ ] `runbook_url: javascript:alert(1)` ⇒ `href` не содержит `javascript:`.
- [ ] `alertname: "A\r\nBcc: x@evil"` ⇒ строка `Subject:` одна, заголовка `Bcc:` нет; то же для не-ASCII темы.
- [ ] Дефолтные шаблоны рендерятся без ошибок.

## Edge Cases

1. Пользовательский `html_template` с действием в неоднозначном контексте ⇒ ошибка рендера (`email: render templates`) — как и ошибка парсинга сейчас.
2. Пустая тема ⇒ без изменений.

## Impact Analysis

- **Affected modules:** `infrastructure/publishing` (email).
- **Breaking changes:** нет для конфига/API. Поведение: значения в HTML экранируются.
- **New dependencies:** none.
- **Risks:** пользовательский `html_template`, вставлявший сырой HTML из данных, теперь покажет его текстом — так и задумано.

## Rollout / Rollback

- **Rollout:** обычный. **Rollback:** предыдущий образ. **Feature flag:** not applicable.

## Observability

- not applicable (ошибка рендера уже логируется и пишет метрику `format_error`).

## Deep Review

- **Mandatory triggers present:** `S`, 2 сигнала (`S R`).
