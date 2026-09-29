---
id: SOLO-KANBAN-UPGRADE
slug: solo-kanban-upgrade
stream: Process
type: docs
priority: medium
status: active
created_at: 2026-09-29
updated_at: 2026-09-29
branch: docs/solo-kanban-upgrade
---

# Requirements: обновление процесса до Solo Kanban 1.1

## Problem Framing

- **Symptom:** AMP работает на ранней версии процесса (SEMA, до 1.0): десять шагов без Risk Profile, без `deep-review`, шаги `/plan`, `/write-doc`, `/end-task`. Upstream `~/Documents/Projects/solo-kanban` уже на v1.1.0 (`d7645a1`).
- **Root Cause:** фреймворк был скопирован в AMP вручную и адаптирован по месту — обновлять нечего и не с чем сверять.
- **Why Now:** v1.1.0 выпущен сегодня; перед релизом AMP v0.1.0 нужен pre-release `deep-review`, которого в текущем процессе нет.
- **How We Measure:** все файлы фреймворка в AMP совпадают с upstream v1.1.0 с точностью до детерминированной замены путей; повторный запуск скрипта синхронизации не даёт диффа.

## Risk Profile

- **Signals:** `X`
  - `X` — затрагивает все агентские адаптеры (Claude-команды, Codex-навыки, `AGENTS.md`, `CLAUDE.md`), шаблоны задач и planning-файлы.
- **Tier:** Standard
- **Notes:** дифф > 200 строк, но почти весь объём — побайтовая копия upstream. Рукописная часть (overlay, planning) < 200 строк. `deep-review` — дискреционный, запускаем по рукописной части.

## User Stories

1. Как владелец AMP, я хочу работать по актуальной версии Solo Kanban, чтобы review шёл до тестов и tier задачи определялся риском.
2. Как владелец, я хочу обновлять фреймворк одной командой, чтобы следующая версия не требовала ручной сверки.

## Success Criteria

- [ ] Framework docs upstream (`workflow`, `agent-policies`, `artifact-contract`, `method`, `ai-agent-playbook`) лежат в `docs/solo-kanban/` с пометкой версии и коммита upstream.
- [ ] `.claude/commands/` = команды upstream 1.1 (кроме `deploy.md`); старые `plan.md`, `write-doc.md`, `end-task.md` удалены.
- [ ] `skills/solo-kanban-*` = Codex-навыки upstream 1.1.
- [ ] `tasks/templates/` = шаблоны задач upstream 1.1.
- [ ] `scripts/solo-kanban-sync.sh` воспроизводит всё вышеперечисленное; повторный запуск — пустой `git diff`.
- [ ] `WORKFLOW.md` — AMP-overlay: пути, язык, AMP-гейты, релизный процесс, отклонения от upstream.
- [ ] `AGENTS.md`, `CLAUDE.md` описывают конвейер 1.1 по tier'ам.
- [ ] `GEMINI.md` и `.gemini/commands/` удалены, ссылок на них нет.
- [ ] `NEXT.md` содержит `## Flow Rules`; `TECH-DEBT.md` — `## Bundles`.
- [ ] `DONE.md` содержит только текущий месяц, прошлые — в `docs/06-planning/archive/DONE-YYYY-MM.md`, записи не потеряны.

## Non-Goals

- Не переписываем старые task-артефакты и архив задач под новый формат.
- Не делаем хук, который механически запрещает тесты без verdict (шаг 7 Upgrading — опционально, в BACKLOG).
- Не трогаем глобальные `~/.codex/skills` и другие репозитории.
- Не ротируем `DECISIONS.md` (см. Spec).

## Constraints

- **Scope:** только процессные файлы и planning; runtime-код и продуктовые доки не меняются.
- **Security:** не применимо.
- **Compatibility:** существующие ссылки на ADR-NNN и на записи DONE должны остаться находимыми.

## Discovery Notes

- Similar tasks: `tasks/archive/solo-kanban-init/` (первичное внедрение).
- Relevant patterns: upstream `CHANGELOG.md` § «Upgrading From 1.0».
- Open unknowns: none.

## Research (mini)

- Source: `~/Documents/Projects/solo-kanban` v1.1.0, `CHANGELOG.md`, `docs/*.md`, `agents/*`.
- Key finding: команды upstream написаны обобщённо («`docs/workflow.md` or the local workflow policy»), но Codex-навыки и шаблоны ссылаются на `docs/workflow.md` и соседей жёстко — нужна детерминированная замена путей при копировании.
- Decision: vendored-копия + overlay (выбор владельца); Gemini-адаптеры удалить (выбор владельца).
