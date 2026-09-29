# AGENTS.md

## Purpose

This repository follows **Solo Kanban 1.1** for one developer working with AI agents. `WORKFLOW.md` is the AMP overlay (paths, language, gates, release process, deviations); the framework is vendored in `docs/solo-kanban/`.

Source of truth, in order:

1. `WORKFLOW.md`
2. `docs/06-planning/NEXT.md` (Queue, WIP, Flow Rules)
3. `docs/06-planning/BUGS.md`
4. the task workspace under `tasks/<TASK-ID>/`

If these files disagree, the earlier item wins; `WORKFLOW.md` also wins over the vendored framework. Keep the mismatch explicit in the task docs.

## Language

- Use **Russian** for communication, planning docs, and task artifacts.
- Use **English** for `README.md`, identifiers, and commit messages.

## Skills

Repository-local Solo Kanban skills (vendored from upstream; do not edit by hand):

- `skills/solo-kanban-core/SKILL.md` — state, tiers, artifacts
- `skills/solo-kanban-planning/SKILL.md` — start-task, research, spec, plan-task, plan-improve
- `skills/solo-kanban-delivery/SKILL.md` — implement, deep-review, write-tests, testing, qa-check
- `skills/solo-kanban-finalize/SKILL.md` — finalize, merge

Framework docs referenced by the skills: `docs/solo-kanban/workflow.md`, `docs/solo-kanban/agent-policies.md`, `docs/solo-kanban/artifact-contract.md`. Planning files are in `docs/06-planning/`.

## Pipeline

The tier comes from the Risk Profile in `requirements.md`:

```text
Lightweight: implement -> testing -> finalize -> merge
Standard:    start-task -> research -> spec -> plan-task -> implement -> write-tests -> testing -> finalize -> merge
Full:        start-task -> research -> spec -> plan-task -> implement -> deep-review -> write-tests -> testing -> finalize -> merge
```

`deep-review` is mandatory for the Full tier (any `S`, any `M`, 3+ signals, or a diff over ~200 lines; see `docs/solo-kanban/workflow.md` § Tiers) and before every `v*` release tag. It runs before `write-tests` and writes `tasks/<TASK-ID>/review-verdict.json`. AMP has no `deploy` step (see `WORKFLOW.md`).

## Branching

Dedicated branches from `main`: `feature/<slug>`, `bugfix/<slug>`, `docs/<slug>`, `hotfix/<slug>`. The only direct write to `main` is the `start-task` claim commit (planning files only).

## Quality Gates

Run the AMP gates from `WORKFLOW.md` § Гейты AMP and `git diff --check` before `finalize`. If gates are red because of preexisting issues, do not hide them: record them in `BUGS.md` and in the task's final status.

## Agent Behavior

- Read existing code and planning docs before changing anything.
- Keep diffs minimal and aligned with the current repo state.
- Do not silently expand scope from docs cleanup into runtime work, or from runtime work into product rewrites.
- If a requested task is unclear, resolve it through repo context first; ask only if ambiguity remains risky.
- Do not push, force-push, or rewrite history unless the user explicitly asks.
