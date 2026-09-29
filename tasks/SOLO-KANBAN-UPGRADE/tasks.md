---
id: SOLO-KANBAN-UPGRADE
slug: solo-kanban-upgrade
stream: Process
type: docs
status: active
created_at: 2026-09-29
updated_at: 2026-09-29
based_on:
  - requirements.md
  - Spec.md
---

# Implementation Plan: обновление процесса до Solo Kanban 1.1

**Based on:** requirements.md / Spec.md  
**Date:** 2026-09-29

## Touched Files

- `scripts/solo-kanban-sync.sh` — новый, синхронизация с upstream
- `docs/solo-kanban/*` — vendored framework docs + `VERSION`
- `.claude/commands/*.md` — vendored команды; удаляются `plan.md`, `write-doc.md`, `end-task.md`
- `skills/solo-kanban-*/SKILL.md` — vendored навыки
- `tasks/templates/*.md` — vendored шаблоны
- `WORKFLOW.md`, `AGENTS.md`, `CLAUDE.md` — overlay
- `GEMINI.md`, `.gemini/commands/*` — удаляются
- `docs/06-planning/{NEXT,DONE,BUGS,TECH-DEBT}.md`, `docs/06-planning/archive/*` — planning 1.1

## Phase 1: Vendored

- [x] **1.1** Написать `scripts/solo-kanban-sync.sh` <!-- verify: bash -n scripts/solo-kanban-sync.sh -->
- [x] **1.2** Запустить sync, удалить устаревшие команды <!-- verify: ls .claude/commands docs/solo-kanban tasks/templates -->
- [x] **1.3** Идемпотентность: второй запуск не меняет дерево <!-- verify: scripts/solo-kanban-sync.sh && git status --short (без новых изменений) -->

## Phase 2: Overlay

- [x] **2.1** `WORKFLOW.md` → AMP-overlay <!-- verify: ручная сверка с Spec § Overlay -->
- [x] **2.2** `AGENTS.md`, `CLAUDE.md` → конвейер 1.1 <!-- verify: git grep -n -E "/(plan|write-doc|end-task)\b" -- AGENTS.md CLAUDE.md WORKFLOW.md (пусто) -->
- [x] **2.3** Удалить `GEMINI.md`, `.gemini/commands/` <!-- verify: git grep -n -i gemini -- ':!tasks/archive' ':!CHANGELOG.md' (пусто) -->

## Phase 3: Planning

- [x] **3.1** `NEXT.md`: `## Flow Rules` <!-- verify: grep -n "## Flow Rules" docs/06-planning/NEXT.md -->
- [x] **3.2** `TECH-DEBT.md`: `## Bundles` и формат записи <!-- verify: grep -n "## Bundles" docs/06-planning/TECH-DEBT.md -->
- [x] **3.3** Ротация `DONE.md` по месяцам в `archive/` <!-- verify: число строк-записей "^- " до и после совпадает -->
- [x] **3.4** `NEXT.md`: удалить закрытые строки (live state), отдельным коммитом <!-- verify: git grep -n -E "закрыт|~~" docs/06-planning/NEXT.md (только живые) -->

## Definition of Done

- [ ] Критерии `requirements.md` выполнены
- [ ] Discretionary `deep-review` проведён, verdict `pass`
- [x] `git diff --check` чист
- [ ] Finalize: DONE, NEXT, ADR-014, архив workspace
