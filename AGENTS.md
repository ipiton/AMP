# AGENTS.md

## Purpose

This repository uses **Solo Kanban** as the default development workflow for one developer working with an AI agent.

Primary source of truth:

1. `WORKFLOW.md`
2. `docs/06-planning/NEXT.md`
3. `docs/06-planning/BUGS.md`
4. task workspace under `tasks/`

If these files disagree, follow the most current planning artifact and keep the mismatch explicit in the task docs.

## Language

- Use **Russian** for communication, planning docs, and task artifacts.
- Use **English** for `README.md`, identifiers, and commit messages.

## Solo Kanban Rules

- WIP max: **2** (`1` main task + `1` hotfix if needed).
- Do not start work directly on `main` unless the user explicitly asks.
- Prefer **vertical slices**. If a task is larger than ~2 days, split it.
- Keep all planning artifacts in git. Do not keep hidden task state outside the repo.

## Required Workflow

When the user asks for a workflow step, follow this sequence:

1. `/start-task`
2. `/research` if triggered
3. `/spec`
4. `/plan`
5. `/implement`
6. `/write-tests`
7. `/testing`
8. `/write-doc`
9. `/end-task`
10. `/merge-to-main`

Do not skip required upstream artifacts:

- `requirements.md` before `/spec`
- `Spec.md` before `/plan`
- `tasks.md` before `/implement`

## Task Workspace Contract

Each active task must have a workspace:

- `tasks/<TASK-ID>/requirements.md`
- `tasks/<TASK-ID>/research.md` when research is needed
- `tasks/<TASK-ID>/Spec.md`
- `tasks/<TASK-ID>/tasks.md`

When the task is closed:

- move the workspace to `tasks/archive/<TASK-ID>/`
- update planning artifacts

## Local Skills

Repository-local Solo Kanban skills live in `skills/`:

- `skills/solo-kanban-core/SKILL.md`
- `skills/solo-kanban-planning/SKILL.md`
- `skills/solo-kanban-delivery/SKILL.md`
- `skills/solo-kanban-finalize/SKILL.md`

Use them as the reusable workflow layer before executing a specific step.

## Planning Artifact Roles

- `docs/06-planning/NEXT.md`: queue and WIP
- `docs/06-planning/DONE.md`: completed tasks and slices
- `docs/06-planning/BUGS.md`: open blockers, residual drift, external failures
- `docs/06-planning/BACKLOG.md`: non-immediate follow-up work
- `docs/06-planning/DECISIONS.md`: decisions that change product/runtime truth

If code or docs change product behavior, update the relevant planning file in the same task.

## Branching

Use dedicated branches:

- `feature/<task-slug>`
- `bugfix/<task-slug>`
- `docs/<task-slug>`
- `hotfix/<task-slug>`

`/start-task` should create or switch to the task branch and move the task from Queue to WIP.

## Research Triggers

Run `/research` before implementation when any of these are true:

- external integration or API contract
- multiple reasonable implementation options
- security/auth/permissions impact
- performance/load uncertainty
- high chance of breaking production behavior

## Quality Gates

Before `/end-task`, the agent should verify as much as the repo realistically allows:

- branch is not `main`
- required planning/task artifacts exist
- relevant tests/build checks were run
- `git diff --check` passes

If full quality gates are red because of preexisting issues:

- do not hide them
- document them in `BUGS.md`
- record the limitation in the task workspace final status

## Agent Behavior

- Read existing code and planning docs before changing anything.
- Keep diffs minimal and aligned with the current repo state.
- Do not silently expand scope from docs cleanup into runtime work, or from runtime work into product rewrites.
- If a requested task is unclear, resolve it through repo context first; ask only if ambiguity remains risky.
- Do not push, force-push, or rewrite history unless the user explicitly asks.

## Fast Start

At the start of work:

1. read `WORKFLOW.md`
2. inspect `docs/06-planning/NEXT.md`
3. inspect the current task workspace if one exists
4. then execute the requested workflow step
