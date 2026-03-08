---
name: solo-kanban-finalize
description: Finalization Solo Kanban skill for /end-task and /merge-to-main. Use when closing a task, updating planning artifacts, archiving the workspace, and merging locally without hiding open blockers.
---

# solo-kanban-finalize

## Purpose

Finalization skill for:

- `/end-task`
- `/merge-to-main`

Use together with all preceding Solo Kanban skills.

## Step Semantics

### `/end-task`

- remove task from WIP
- add final entry to `DONE.md`
- update `BUGS.md` and `BACKLOG.md` if needed
- archive workspace under `tasks/archive/`
- record final status and remaining limitations

### `/merge-to-main`

- merge the task branch to `main` locally
- do not push unless explicitly requested
- keep any documented open issues explicit

## Finalization Quality Bar

- branch is not `main` before merge
- task artifacts are complete
- planning files reflect the actual outcome
- unresolved gates are documented, not hidden
