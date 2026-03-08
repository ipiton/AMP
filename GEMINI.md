# GEMINI.md

## Solo Kanban Instructions

This repository expects AI agents to follow the local **Solo Kanban** workflow defined in `WORKFLOW.md`.

Use these files as the operational truth:

1. `WORKFLOW.md`
2. `docs/06-planning/NEXT.md`
3. `docs/06-planning/BUGS.md`
4. the current task workspace under `tasks/`

## Default Behavior

- Use **Russian** for communication and planning artifacts.
- Use **English** for `README.md`, public-facing product docs, identifiers, and commits.
- Keep WIP limited to `2`.
- Work on a task branch, not on `main`, unless explicitly told otherwise.
- Prefer minimal diffs and vertical slices.

## Workflow Steps

Expected pipeline:

1. `/start-task`
2. `/research`
3. `/spec`
4. `/plan`
5. `/implement`
6. `/write-tests`
7. `/testing`
8. `/write-doc`
9. `/end-task`
10. `/merge-to-main`

Do not skip prerequisite artifacts between steps.

## Local Commands And Skills

- Gemini command entry points live in `.gemini/commands/`
- Shared Solo Kanban skills live in `skills/`

Gemini-specific command namespace uses `sk-*` to avoid collisions with built-in slash commands and modes.

Examples:

- `/sk-start-task`
- `/sk-research`
- `/sk-spec`
- `/sk-plan`
- `/sk-implement`
- `/sk-write-tests`
- `/sk-testing`
- `/sk-write-doc`
- `/sk-end-task`
- `/sk-merge-to-main`

These map to the same Solo Kanban workflow steps listed above. Use the matching command file together with the shared skills.

## Required Artifacts

For each task, keep:

- `tasks/<TASK-ID>/requirements.md`
- `tasks/<TASK-ID>/research.md` when needed
- `tasks/<TASK-ID>/Spec.md`
- `tasks/<TASK-ID>/tasks.md`

When finished:

- archive to `tasks/archive/<TASK-ID>/`

## Planning Responsibilities

Update the correct planning file as part of the task:

- `NEXT.md` for queue/WIP changes
- `DONE.md` for completed tasks
- `BUGS.md` for open blockers or preexisting failures
- `BACKLOG.md` for future slices
- `DECISIONS.md` for changed source-of-truth decisions

## Research Policy

Run research before coding when the task involves:

- external APIs or integrations
- multiple valid solution paths
- security-sensitive behavior
- performance uncertainty
- likely production impact

## Validation Policy

Before closing a task:

- confirm branch != `main`
- confirm required task docs exist
- run the strongest realistic checks for the touched scope
- run `git diff --check`

If the repo has preexisting failing gates, record them honestly. Do not hide them behind a “done” claim.

## Guardrails

- Read existing code and planning docs first.
- Keep scope tight.
- Do not perform destructive git operations unless explicitly requested.
- Do not push or merge unless explicitly requested.
