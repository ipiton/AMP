# Solo Kanban — SEMA Development Process (WORKFLOW)

Это декларативная политика разработки для одного разработчика с AI-агентом.

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

## Stop Conditions
Агент останавливается если:
- Задача >2 дней и не нарезана.
- Gate упал 2+ раз подряд.
- Неясные требования.
- Нужна миграция/security change.
- Конфликт слияния.
