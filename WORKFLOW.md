# Solo Kanban — SEMA Development Process (WORKFLOW)

Это декларативная политика разработки для одного разработчика с AI-агентом.

## Agent Entry Points
- `AGENTS.md` — Solo Kanban instructions for generic coding agents
- `CLAUDE.md` — Solo Kanban instructions for Claude-based agents
- `GEMINI.md` — Solo Kanban instructions for Gemini-based agents
- `.claude/commands/` — Claude slash-commands for each workflow step
- `.gemini/commands/` — Gemini command prompts for each workflow step (`/sk-*` namespace to avoid built-in command collisions)
- `skills/` — shared Solo Kanban skills used by multiple agents

Все три файла должны оставаться согласованными с этим `WORKFLOW.md` и planning artifacts в `docs/06-planning/`.

## Принципы
- **Одна задача в фокусе** — WIP max 2 (1 основная + 1 hotfix).
- **Баланс 50/50** — maintenance vs roadmap.
- **Вертикальные срезы** — задача >2 дней → нарезать.
- **Quality gates** — каждый шаг проверяется.
- **Всё в коде** — planning files, task workspace — версионируются.

## State Machine
```
QUEUED ──> ACTIVE ──> [шаги pipeline] ──> DONE ──> MERGED
```
- **active**: NEXT.md -> WIP + feature branch.
- **done**: DONE.md entry + archive.

## Pipeline Шаги
1. **Start** (`/start-task`) — Workspace, branch, requirements.
2. **Research** (`/research`) — Опционально по триггерам.
3. **Spec** (`/spec`) — Контракты, модели (Spec.md).
4. **Plan** (`/plan`) — Чеклист шагов (tasks.md).
5. **Implement** (`/implement`) — Пошаговая реализация.
6. **Write Tests** (`/write-tests`) — Тесты.
7. **Testing** (`/testing`) — Проверка.
8. **Write Doc** (`/write-doc`) — Документация.
9. **End Task** (`/end-task`) — Финализация, архив, DONE.md.
10. **Merge** (`/merge-to-main`) — Merge feature branch.

## Research Policy
Запускается при:
- Внешние интеграции/API.
- 2+ варианта решения.
- Security / Auth / RBAC.
- Неизвестная нагрузка/Perf.
- Риск "сломать прод".

## Quality Gates
- **Branch created**: git branch != main.
- **Requirements exist**: перед /spec.
- **Spec approved**: перед /plan.
- **Plan exists**: перед /implement.
- **Tests pass**: перед /end-task (go vet + test + build).
- **No ignored errors**: нет `_, _ :=` в diff.

## Release Process (cutting release notes)
Источник — только `CHANGELOG.md`'s `[Unreleased]` блок (не git log, не память).
Шаблон — `docs/RELEASE_NOTES_TEMPLATE.md`. Пример — `docs/RELEASE_NOTES_v0.1.0-draft.md`.

1. **Collect**: скопировать весь `[Unreleased]` блок целиком — это единственный источник правды на момент релиза.
2. **Split by section**: разнести bullet'ы по `### Added` (→ Features), `### Changed`/`### Improved` (→ Performance/Improvements), `### Breaking changes / migration notes` (→ Breaking Changes) в шаблон. Не пересочинять формулировки — конденсировать features можно, breaking changes — только копировать verbatim.
3. **Verify breaking changes against migration notes**: каждый bullet под "Breaking changes" в CHANGELOG обычно ссылается на epic/wave-код (`FU-*`, `AMP-PARITY-WAVE*`) — сверить, что соответствующий "Added"-bullet выше в CHANGELOG содержит миграционные заметки (обычно секция "Migration notes" внутри самого Added-bullet), и что draft переносит upgrade-шаги, а не только факт breakage.
4. **Backward compatibility**: явно перечислить, что НЕ меняется (например: K8s-Secret-provisioned targets, webhook payload shape) — раздел существует специально, чтобы не пришлось перечитывать код при каждом вопросе "а вот это не сломается?".
5. **Upgrade steps**: свести breaking changes в конкретные действия (`grouping.reconciliation_grace: 20s` → убрать/поднять; `email_configs` → добавить `global.smtp_smarthost`; и т.п.) — читатель должен уйти со списком команд/диффов, не с списком фактов.
6. После `/end-task` релиза: переименовать `*-draft.md` → `RELEASE_NOTES_vX.Y.Z.md`, обновить `CHANGELOG.md` (закрыть `[Unreleased]` в `## [X.Y.Z] - YYYY-MM-DD`).

## Stop Conditions
Агент останавливается если:
- Задача >2 дней и не нарезана.
- Gate упал 2+ раз подряд.
- Неясные требования.
- Нужна миграция/security change.
- Конфликт слияния.
