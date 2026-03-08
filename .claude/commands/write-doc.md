# /write-doc

Use:

- `skills/solo-kanban-core/SKILL.md`
- `skills/solo-kanban-delivery/SKILL.md`

## Goal

Synchronize docs and task artifacts with the implemented slice.

## Do

1. Update user-facing docs affected by the change.
2. Update task docs (`requirements.md`, `Spec.md`, `tasks.md`) if implementation changed assumptions.
3. Keep public claims aligned with verified runtime and planning truth.
4. Run `git diff --check` for the touched docs/files.
