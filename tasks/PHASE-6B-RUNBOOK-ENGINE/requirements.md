# Requirements: PHASE-6B-RUNBOOK-ENGINE

## Context
Investigation-агент (Phase 5A/5B/6A) уже умеет вызывать built-in tools
(prometheus, loki, kubernetes, database), но не знает командных знаний:
типовые причины, шаги расследования и ремедиацию для конкретных алертов.
Runbook engine добавляет markdown knowledge base с YAML frontmatter,
автоматически подбирает runbooks по labels алерта и подмешивает их в
контекст LLM. Это завершает Investigation Toolset (Phase 6) и является
частью главного USP AMP. Источник: `docs/06-planning/BACKLOG.md`
(секция «Intelligence — PHASE-6»), референс — SherlockOps.

Текущий код: `go-app/internal/core/investigation/` (agent loop, registry, tools
interface), `go-app/internal/infrastructure/investigation/` (queue, tools).
Путь `internal/investigation/runbooks/engine.go` из BACKLOG устарел —
точное размещение пакета фиксируется на `/spec`.

## Goals
- [ ] Формат runbook: markdown + YAML frontmatter (`name`, `match` — map label→value, `tags`), тело — произвольный markdown (Symptoms / Common Causes / Investigation Steps / Remediation).
- [ ] Загрузка runbooks из директории файловой системы (configurable path; K8s ConfigMap монтируется как директория — отдельной интеграции не нужно).
- [ ] Matching по labels алерта (alertname, severity, namespace и т.п.): все ключи `match` должны совпасть; детерминированный порядок и лимит числа/размера подмешиваемых runbooks.
- [ ] Инъекция matched runbooks в LLM context investigation-агента (system prompt / контекст первого запроса).
- [ ] Конфиг `investigation.runbooks.*` (enabled, path, лимиты) + пример в `config.yaml.example`; выключено/пусто — поведение агента не меняется.
- [ ] Невалидные runbook-файлы не роняют сервис: пропускаются с логом/метрикой.

## Non-Goals (кандидаты в BACKLOG)
- Regex/glob-matching по labels (решить на `/spec`, по умолчанию — только equality).
- Hot reload runbooks через SIGHUP (если не тривиально через существующий reload-механизм).
- Per-environment runbooks (PHASE-6D), MCP tools (PHASE-6C), feedback loop (PHASE-7C).
- UI для runbooks.

## Constraints
- Go, код в `go-app/internal/` (не в `cmd/`).
- Без новых тяжёлых зависимостей; YAML-парсер уже есть в проекте.
- Бюджет контекста LLM: runbooks ограничены по размеру (см. обогащение 2026-09-02 в BACKLOG про защиты бюджета контекста).
- Нет `_, _ :=` / проигнорированных ошибок в diff.
- Срез ~2d; если на `/spec` выйдет больше — нарезать (engine+matching отдельно от injection).

## Success Criteria (Definition of Done)
- [ ] Unit-тесты: парсинг frontmatter, matching (совпадение/несовпадение/частичное), лимиты, битые файлы.
- [ ] Тест agent loop: matched runbook попадает в контекст LLM; без runbooks — поведение прежнее.
- [ ] `go vet`, `go test`, `go build` зелёные для затронутых пакетов.
- [ ] Документация: `config.yaml.example`, README пакета investigation, CHANGELOG `[Unreleased]`.
- [ ] Planning обновлён (NEXT/DONE/BACKLOG) на `/end-task`.

## Open Questions (для /research)
- Куда именно инжектить: system prompt в `infrastructure/llm` или отдельное сообщение в history agent loop?
- Нужен ли runbook как tool (`get_runbook`) вместо/в дополнение к pre-injection?
- Как сочетать с аннотацией `runbook_url` алерта (ссылка на внешний runbook)?
