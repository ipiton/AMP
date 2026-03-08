---
name: solo-kanban-core
description: Core Solo Kanban rules for this repository. Use before any workflow step to load source-of-truth files, WIP limits, task workspace conventions, planning artifact roles, and guardrails.
---

# solo-kanban-core

## Purpose

Shared repository skill for all AI agents working in this repo.

Use this skill before any Solo Kanban workflow step.

## Source Of Truth

Read in this order:

1. `WORKFLOW.md`
2. `AGENTS.md`
3. `CLAUDE.md` or `GEMINI.md` when relevant
4. `docs/06-planning/NEXT.md`
5. `docs/06-planning/BUGS.md`
6. current task workspace under `tasks/`

## Core Rules

- Use Russian for communication and planning artifacts.
- Use English for `README.md`, public product docs, identifiers, and commits.
- Respect WIP max `2`.
- Prefer vertical slices.
- Keep all planning state in git-visible files.
- Do not work directly on `main` unless explicitly asked.

## Task Workspace Contract

Active task workspace:

- `tasks/<TASK-ID>/requirements.md`
- `tasks/<TASK-ID>/research.md` when needed
- `tasks/<TASK-ID>/Spec.md`
- `tasks/<TASK-ID>/tasks.md`

Completed task workspace:

- `tasks/archive/<TASK-ID>/`

## Planning Roles

- `NEXT.md` = queue and WIP
- `DONE.md` = completed slices
- `BUGS.md` = open blockers and residual drift
- `BACKLOG.md` = follow-up work
- `DECISIONS.md` = product/runtime truth changes

## Guardrails

- Keep diffs minimal.
- Do not hide failing gates.
- Do not silently widen scope.
- Do not push or rewrite history unless explicitly requested.
