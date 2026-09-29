---
id: SOLO-KANBAN-UPGRADE
slug: solo-kanban-upgrade
stream: Process
type: docs
status: complete
created_at: 2026-09-29
updated_at: 2026-09-29
---

# Review Findings: SOLO-KANBAN-UPGRADE

Дискреционный `deep-review` (tier Standard, см. `requirements.md` § Risk Profile). Ревьюер — независимый read-only субагент, дифф `main...9870877`. Исправления — коммит `01b1c13`.

| # | Severity | Где | Находка | Disposition |
|---|---|---|---|---|
| 1 | major | `scripts/solo-kanban-sync.sh` | Отсутствующий каталог upstream давал пустой список имён: `set -e` не действует внутри `$(...)`, и скрипт удалял весь vendored-набор с rc=0 (воспроизведено на `templates/`) | fixed: все четыре каталога проверяются заранее, пустой набор — ошибка, `set -e` явно внутри `sync_set` (bash 3.2 без `inherit_errexit`). Проверено: missing dir и empty set → rc=2, дерево не тронуто |
| 2 | major | `TECH-DEBT.md` | 14 закрытых записей удалены, а в DONE находится по slug только одна: DEBT-CLOSURE-SWEEP пишет «8/8» без slug'ов | fixed: записи дословно перенесены в `archive/DONE-2026-{03,04,08}.md` по дате закрытия, все 14 находятся grep'ом |
| 3 | minor | `BACKLOG.md` | ~60 закрытых `- [x]` противоречат правилу live state; часть закрытий (INF-A/INF-B) записана только там | deferred: non-goal в `requirements.md`, follow-up `BACKLOG-LIVE-STATE-CLEANUP` |
| 4 | minor | `AGENTS.md` | В триггерах `deep-review` нет условия «дифф > ~200 строк» | fixed |
| 5 | minor | `AGENTS.md` | «Most current artifact wins» расходится с порядком источников в CLAUDE.md/WORKFLOW.md | fixed: побеждает источник выше по списку |
| 6 | minor | `scripts/solo-kanban-sync.sh` | Dirty upstream вендорится с одним лишь warning | fixed: отказ без `--allow-dirty` |
| 7 | minor | `requirements.md` | Standard при диффе > 200 строк — это отклонение от таблицы tier'ов | fixed: записан как явный override |
| 8 | nit | `docs/solo-kanban/artifact-contract.md` | `docs/planning/`, `tasks/<slug>/` | no change: vendored, отображение путей — в `WORKFLOW.md` § Пути |
| 9 | nit | `WORKFLOW.md` § Гейты AMP | «В порядке усиления» неверно: `quality-gates-fast` = fmt + vet, а `go fmt` переписывает файлы | fixed |
| 10 | nit | `scripts/solo-kanban-sync.sh` | Пути на удаление берутся из VERSION без валидации | fixed: абсолютные пути и `..` → rc=2 |
| 11 | nit | `DECISIONS.md` | ADR-014 обещан в Spec, но ещё не написан | on finalize |

Итог: blocker 0, major 2 (оба fixed), открытых major без disposition нет.
