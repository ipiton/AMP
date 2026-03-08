---
name: solo-kanban-planning
description: Planning-phase Solo Kanban skill for /start-task, /research, /spec, and /plan. Use when creating requirements, research, specs, task checklists, and branch/workspace state for a new slice.
---

# solo-kanban-planning

## Purpose

Planning-phase skill for:

- `/start-task`
- `/research`
- `/spec`
- `/plan`

Use together with `skills/solo-kanban-core/SKILL.md`.

## Step Semantics

### `/start-task`

- move selected task from Queue to WIP in `NEXT.md`
- create task workspace
- create or switch to task branch
- note dirty worktree if present

### `/research`

- gather repo context before implementation
- create `research.md`
- record findings, options, recommendation, and next-step implication

### `/spec`

- create `Spec.md`
- fix scope, non-goals, key decisions, acceptance criteria, risks
- use active code + planning artifacts as truth

### `/plan`

- create `tasks.md`
- split work into vertical slices when needed
- include implementation, testing, docs, and finalization checklist

## Planning Quality Bar

- `requirements.md` must exist before `/spec`
- `Spec.md` must exist before `/plan`
- tasks should be explainable as small, mergeable slices
