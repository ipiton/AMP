# CLAUDE.md

## Repo Workflow

This repository follows **Solo Kanban 1.1**. `WORKFLOW.md` is the AMP overlay: paths, language, gates, release process, and deviations from the framework. The framework itself is vendored in `docs/solo-kanban/`.

Source of truth, in order:

1. `WORKFLOW.md`
2. `docs/06-planning/NEXT.md` (Queue, WIP, Flow Rules)
3. `docs/06-planning/BUGS.md`
4. the active task workspace in `tasks/<TASK-ID>/`

## Operating Rules

- Communicate in **Russian** unless the artifact is expected in English.
- Keep `README.md` and public product docs in English.
- Respect the WIP limit in `NEXT.md` § Flow Rules.
- Never start implementation from `main` unless explicitly requested. The only direct write to `main` is the `start-task` claim commit (planning files only).
- Prefer small vertical slices over broad multi-area rewrites.

## Pipeline

The tier comes from the Risk Profile in `requirements.md` (`docs/solo-kanban/workflow.md` § Step Matrix):

```text
Lightweight: implement -> testing -> finalize -> merge-to-main
Standard:    start-task -> research -> spec -> plan-task -> implement -> write-tests -> testing -> finalize -> merge-to-main
Full:        start-task -> research -> spec -> plan-task -> implement -> deep-review -> write-tests -> testing -> finalize -> merge-to-main
```

- `deep-review` runs after `implement` and before `write-tests`; tests are written only after `tasks/<TASK-ID>/review-verdict.json` has `"gate": "pass"`.
- `finalize` writes `DONE.md` / `NEXT.md` and archives the workspace **on the task branch**; `merge-to-main` is mechanical.
- AMP has no `deploy` step; see `WORKFLOW.md` for how the `R` signal and pre-release review work here.

Command files live in `.claude/commands/`, shared skills in `skills/`. Both are vendored — change local rules in `WORKFLOW.md`, not there.

## Quality Gates

Before `finalize`, run the AMP gates from `WORKFLOW.md` § Гейты AMP and `git diff --check`. If full gates are blocked by preexisting failures, document that explicitly instead of pretending the gate is green.

## Scope Discipline

- Do not widen a docs task into runtime changes unless the user asks.
- Do not widen a runtime task into product positioning cleanup unless the task requires it.
- If a task exceeds ~2 days, propose or create a smaller slice.
- If a gate fails twice in a row, stop and document the blocker.
