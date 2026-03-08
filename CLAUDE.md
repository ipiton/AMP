# CLAUDE.md

## Repo Workflow

When working in this repository, follow the **Solo Kanban** process from `WORKFLOW.md`.

Core source of truth:

1. `WORKFLOW.md`
2. `docs/06-planning/NEXT.md`
3. `docs/06-planning/BUGS.md`
4. active task workspace in `tasks/`

## Operating Rules

- Communicate in **Russian** unless the artifact is expected in English.
- Keep `README.md` and public product docs in English.
- Respect WIP max `2`.
- Never start implementation from `main` unless explicitly requested.
- Prefer small vertical slices over broad multi-area rewrites.

## Required Solo Kanban Pipeline

Use this order:

1. `/start-task`
2. `/research` when needed
3. `/spec`
4. `/plan`
5. `/implement`
6. `/write-tests`
7. `/testing`
8. `/write-doc`
9. `/end-task`
10. `/merge-to-main`

If the user explicitly asks for one of these steps, execute that step and preserve the workflow artifacts.

## Local Commands And Skills

- Claude command entry points live in `.claude/commands/`
- Shared Solo Kanban skills live in `skills/`

Prefer using the matching command file for the requested workflow step together with the shared skills.

## Task Files

Every active task should use:

- `tasks/<TASK-ID>/requirements.md`
- `tasks/<TASK-ID>/research.md` when applicable
- `tasks/<TASK-ID>/Spec.md`
- `tasks/<TASK-ID>/tasks.md`

On task completion, archive to:

- `tasks/archive/<TASK-ID>/`

## Planning Updates

Keep these files in sync with the real repo state:

- `NEXT.md` for Queue/WIP
- `DONE.md` for completed work
- `BUGS.md` for unresolved blockers
- `BACKLOG.md` for follow-up work
- `DECISIONS.md` for truth-changing decisions

Do not declare a task done without updating planning.

## Quality Gates

Before `/end-task`, verify:

- branch is not `main`
- required task artifacts exist
- relevant checks were run
- `git diff --check` passes

If full tests are blocked by preexisting failures, document that explicitly instead of pretending the gate is green.

## Scope Discipline

- Do not widen a docs task into runtime changes unless the user asks.
- Do not widen a runtime task into product positioning cleanup unless the task requires it.
- If a task exceeds ~2 days, propose or create a smaller slice.
- If a gate fails twice in a row, stop and document the blocker.
