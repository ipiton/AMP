---
name: solo-kanban-delivery
description: Delivery-phase Solo Kanban skill for /implement, /write-tests, /testing, and /write-doc. Use when executing a planned slice, validating it, and synchronizing docs with verified behavior.
---

# solo-kanban-delivery

## Purpose

Delivery-phase skill for:

- `/implement`
- `/write-tests`
- `/testing`
- `/write-doc`

Use together with:

- `skills/solo-kanban-core/SKILL.md`
- `skills/solo-kanban-planning/SKILL.md`

## Step Semantics

### `/implement`

- execute only the agreed slice
- update task checklist as work lands
- update planning docs only when product/runtime truth changes

### `/write-tests`

- add or adjust tests for changed behavior
- prefer targeted coverage for the touched scope

### `/testing`

- run the strongest realistic checks for the changed scope
- record green and red results explicitly
- separate in-scope failures from preexisting blockers

### `/write-doc`

- update user-facing docs, examples, and planning/task docs affected by the slice
- keep public claims consistent with verified runtime

## Delivery Quality Bar

- no hidden blockers
- no accidental scope expansion
- `git diff --check` should pass for touched files
