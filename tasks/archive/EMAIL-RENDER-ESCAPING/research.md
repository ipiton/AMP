---
id: EMAIL-RENDER-ESCAPING
slug: email-render-escaping
stream: Publishing
type: bug
artifact: research-pack
status: complete
created_at: 2026-10-23
updated_at: 2026-10-23
---

# Research Pack - экранирование HTML и Subject в email

## Goal

Выбрать минимальный фикс для HTML fallback-рендера и Subject.

## Sources

- `go-app/internal/infrastructure/publishing/email_publisher_enhanced.go` (`renderEmailContent`, `renderTemplate`, `emailTemplateFuncs`)
- `go-app/internal/infrastructure/publishing/email_client.go` (`buildMIMEMessage`, `mime2047Subject`, `sanitizeHeaderValue`)
- `go-app/internal/notification/template/defaults/email.go` (`DefaultEmailHTML`)
- `go-app/internal/infrastructure/publishing/template_formatter.go:494` (overlay `RenderEmailContent`)
- `go-app/internal/core/interfaces.go:258` (валидация лейблов на ingest)

## Findings

- Fallback-рендер выполняется всегда, overlay `RenderEmailContent` (уже `html/template`) только заменяет непустые поля. При `publishing.templates.enabled=false`, у таргета без шаблонных полей или при ошибке overlay уходит fallback.
- `html/template.FuncMap` — алиас `text/template.FuncMap`: `emailTemplateFuncs` (`upper`, `lower`, `default`) подходит без изменений.
- `DefaultEmailHTML` содержит одно действие в `<style>` (`{{ if eq .Status "resolved" }}#28a745{{ else }}…`) — выводит только константы шаблона, для `html/template` безопасно; `href="{{ .CommonAnnotations.runbook_url }}"` — `html/template` заменяет небезопасную схему на `#ZgotmplZ`.
- Ingest не валидирует значения лейблов (`parseAlertsForProcessing` → `convertIngestInputToAlert`, `alertname` только `TrimSpace`), CR/LF доходят до шаблона Subject — гипотеза из BUGS подтверждается чтением кода (уточнено на deep-review: `core/interfaces.go:258` — валидация фильтров запросов, не ingest).
- `mime2047Subject` кодирует только не-ASCII темы; ASCII-тема пишется как есть.

## Decision

- HTML-часть — `html/template` с тем же FuncMap; subject и text — по-прежнему `text/template` (это не HTML).
- `buildMIMEMessage`: `mime2047Subject(sanitizeHeaderValue(msg.Subject))` — покрывает и fallback, и overlay.
- Удаление fallback-пути — вне scope (Non-Goals).

## Follow-ups

- [ ] нет
