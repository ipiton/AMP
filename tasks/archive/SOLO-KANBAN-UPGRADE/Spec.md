---
id: SOLO-KANBAN-UPGRADE
slug: solo-kanban-upgrade
stream: Process
type: docs
status: active
created_at: 2026-09-29
updated_at: 2026-09-29
based_on:
  - requirements.md
---

# Spec: обновление процесса до Solo Kanban 1.1

## Summary

Фреймворк делится на две части: **vendored** — побайтовая копия upstream с заменой путей, её никто не правит руками; **overlay** — всё специфичное для AMP, живёт в `WORKFLOW.md` и planning-файлах. Обновление = `scripts/solo-kanban-sync.sh <upstream>` + ревью диффа.

## Requirements Coverage

| Критерий | Где закрывается |
|---|---|
| framework docs, команды, навыки, шаблоны | Target Design § Vendored |
| скрипт синхронизации | Target Design § Sync script |
| overlay, AGENTS/CLAUDE | Target Design § Overlay |
| Gemini | Target Design § Removal |
| Flow Rules, Bundles, ротация DONE | Target Design § Planning |

## Current State

- `WORKFLOW.md` — «SEMA Development Process», 10 шагов, без tier'ов.
- `.claude/commands/` — 10 тонких обёрток на `skills/`; `skills/` — ранняя версия Codex-навыков.
- `tasks/templates/` — только `requirements.md`, `tasks.md`.
- `GEMINI.md`, `.gemini/commands/sk-*` — Gemini-адаптеры.
- `DONE.md` — 179 строк, февраль–сентябрь 2026; `DECISIONS.md` — реестр ADR-001…013 без группировки по месяцам.

## Design Premises

| Premise | Confirmed by | Class | If wrong |
|---|---|---|---|
| Команды upstream не содержат путей `docs/planning/`, только имена файлов (`NEXT.md`) | `grep -rn "docs/" agents templates` в upstream | `code-read` | агент искал бы planning не там; overlay явно даёт пути |
| Ни один скрипт или CI-шаг AMP не читает `DONE.md` по пути | `git grep -n "DONE.md" -- scripts .github Makefile go-app` | `call-path-traced` | ротация сломала бы гейт — тогда нужен accessor |
| Ссылки вида `ADR-NNN` используются в коде и доках | `git grep -c "ADR-0"` | `code-read` | — (обоснование отказа от ротации DECISIONS) |

## Target Design

### Vendored

| Upstream | AMP |
|---|---|
| `docs/{workflow,agent-policies,artifact-contract,method,ai-agent-playbook}.md` | `docs/solo-kanban/` |
| `agents/claude/commands/*.md` кроме `deploy.md`, `README.md` | `.claude/commands/` |
| `agents/codex/skills/*/SKILL.md` | `skills/*/SKILL.md` |
| `templates/task/*.md` | `tasks/templates/` |

Замена путей при копировании: `docs/workflow.md` → `docs/solo-kanban/workflow.md` (аналогично для `agent-policies.md`, `artifact-contract.md`, `method.md`, `ai-agent-playbook.md`). Больше ничего не меняется.

`docs/solo-kanban/VERSION` — версия и коммит upstream. `deploy.md` не копируем: у AMP нет окружения, которое деплоит агент; `R`-сигнал закрывается релизным процессом из overlay.

### Sync script

`scripts/solo-kanban-sync.sh [upstream-dir]` (по умолчанию `~/Documents/Projects/solo-kanban`): копирует таблицу выше, применяет замену путей, удаляет из `.claude/commands/` команды, которых нет в upstream (по списку прошлой синхронизации в `VERSION`), пишет `VERSION`. Идемпотентен.

### Overlay

`WORKFLOW.md` — на русском, коротко: где лежит фреймворк, где planning (`docs/06-planning/`), ветки, язык, AMP-гейты (`go vet/test/build`, `scripts/release-gate.sh`, нет `_, _ :=`), релизный процесс (перенос без изменений), отклонения от upstream. `AGENTS.md`/`CLAUDE.md` — конвейер по tier'ам и ссылка на `WORKFLOW.md` вместо собственного пересказа.

### Removal

`GEMINI.md`, `.gemini/commands/` — удалить; убрать упоминания из `WORKFLOW.md` (навыки upstream Gemini не упоминают).

### Planning

- `NEXT.md`: `## Flow Rules` по шаблону upstream с числами AMP (WIP 2, баланс 50/50); закрытые строки Queue/WIP удалить (live state) — отдельным коммитом.
- `TECH-DEBT.md`: `## Bundles` + формат записи со статусами `open | in-progress | blocked`.
- `DONE.md`: оставить `## 2026-09`, прошлые месяцы — в `archive/DONE-2026-MM.md` по дате записи, таблица архивов в шапке.
- `DECISIONS.md`: **не ротируем** — это реестр ADR с номерами, на которые ссылаются код и доки; ротация по месяцам ломает поиск по номеру. Фиксируем как отклонение в overlay и ADR-014.

## Security Design

Не применимо.

## Invariants

- Vendored-файлы = upstream + замена путей. Любая ручная правка перетирается следующим sync.
- Число записей DONE до и после ротации совпадает.

## Edge Cases

- Upstream удалил команду → sync удаляет её локально (по списку в `VERSION`), а не оставляет мёртвый файл.
- Локальная команда AMP, которой нет в upstream → sync её не трогает.

## Impact Analysis

Меняется только то, как агенты ведут задачи. Runtime, Helm, CI — не затрагиваются.

## Rollout / Rollback

Откат — revert merge-коммита.

## Observability

Не применимо.

## Deep Review

Дискреционный (дифф > 200 строк за счёт копии). Запускаем независимым агентом по overlay, скрипту и planning; vendored-файлы проверяются скриптом (идемпотентность), не ревью.

## Open Questions

- none
