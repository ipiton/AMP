# Solo Kanban в AMP (WORKFLOW)

AMP ведётся по **Solo Kanban 1.1** — процессу для одного разработчика с AI-агентом. Этот файл — AMP-overlay: только то, чем AMP отличается от фреймворка или что фреймворк оставляет проекту. Всё остальное — во фреймворке.

## Фреймворк

- Лежит в `docs/solo-kanban/` (версия и коммит upstream — `docs/solo-kanban/VERSION`):
  - `workflow.md` — state machine, tier'ы по Risk Profile, гейты, Definition of Done, stop conditions;
  - `agent-policies.md` — refactoring, retry, границы агента, discovery cost, enforcement;
  - `artifact-contract.md` — форматы task-артефактов и planning-файлов;
  - `method.md`, `ai-agent-playbook.md` — модель работы.
- Команды — `.claude/commands/`, навыки — `skills/solo-kanban-*/`, шаблоны задач — `tasks/templates/`.
- Всё перечисленное **vendored**: копия upstream с заменой путей, руками не правится — следующая синхронизация перетрёт. Обновление: `scripts/solo-kanban-sync.sh [путь-к-upstream]`, затем ревью диффа. Локальное правило пишется сюда, а не в vendored-файл.
- Если этот файл и фреймворк расходятся — прав этот файл.

## Пути

| Во фреймворке | В AMP |
|---|---|
| planning (`NEXT.md`, `DONE.md`, …) | `docs/06-planning/` |
| архив журналов | `docs/06-planning/archive/` |
| task workspace | `tasks/<TASK-ID>/` — каталог по ID в верхнем регистре (`PROD-DEPS-VULN`), не по slug |
| архив задач | `tasks/archive/<TASK-ID>/` |
| integration branch | `main` |
| ветки задач | `feature/`, `bugfix/`, `docs/`, `hotfix/` + slug |

Flow Rules (WIP-лимит, пополнение, баланс) — только в `docs/06-planning/NEXT.md` § Flow Rules.

## Язык

Общение, planning и task-артефакты — по-русски. `README.md`, публичные продуктовые доки, идентификаторы, commit messages (conventional commits) — по-английски. Vendored-файлы фреймворка остаются на английском.

## Конвейер

Tier выбирается по Risk Profile (`docs/solo-kanban/workflow.md` § Step Matrix):

```text
Lightweight: implement -> testing -> finalize -> merge-to-main
Standard:    start-task -> research -> spec -> plan-task -> implement -> write-tests -> testing -> finalize -> merge-to-main
Full:        start-task -> research -> spec -> plan-task -> implement -> deep-review -> write-tests -> testing -> finalize -> merge-to-main
```

Отличия AMP:

- **`deploy` нет.** AMP — продукт, а не сервис, который агент выкатывает. Сигнал `R` закрывается записью в `CHANGELOG.md` `[Unreleased]` (с migration notes для breaking changes) и релизным процессом ниже.
- **Pre-release `deep-review` обязателен** перед каждым тегом `v*`: тег публикует образы в GHCR (`.github/workflows/release.yml`).

## Гейты AMP

`testing` в AMP означает:

1. Затронутые пакеты: `cd go-app && go vet ./<pkg>/... && go test ./<pkg>/...`.
2. Быстрый гейт: `make -C go-app quality-gates-fast` — только `go fmt` + `go vet` по всему модулю; `go fmt` переписывает файлы, после него проверить `git status`.
3. Перед `finalize` задачи с изменением Go-кода или чарта: `scripts/release-gate.sh` (build, golangci-lint, тесты, `-race`, helm lint/template, RBAC-scope, amtool-smoke).

Плюс всегда: `git diff --check`, в диффе нет проглоченных ошибок (`_, _ :=`). Если гейт красный из-за уже существующих проблем — не скрывать: запись в `BUGS.md` и в итог задачи.

## Журналы

- `DONE.md` ротируется помесячно в `docs/06-planning/archive/DONE-YYYY-MM.md`. Владелец ротации — `finalize` первой задачи нового месяца. Проверка «закрыт ли slug» читает `DONE.md` **и** `archive/DONE-*.md`.
- `DECISIONS.md` **не ротируется** (отклонение от фреймворка): это реестр ADR с номерами, на которые ссылаются код и доки (`ADR-011` и т. п.); ротация по месяцам ломает поиск по номеру. Новые решения — следующим номером ADR.

## Release Process (cutting release notes)

Источник — только `CHANGELOG.md`'s `[Unreleased]` блок (не git log, не память).
Шаблон — `docs/RELEASE_NOTES_TEMPLATE.md`. Пример — `docs/RELEASE_NOTES_v0.1.0-draft.md`.

1. **Collect**: скопировать весь `[Unreleased]` блок целиком — это единственный источник правды на момент релиза.
2. **Split by section**: разнести bullet'ы по `### Added` (→ Features), `### Changed`/`### Improved` (→ Performance/Improvements), `### Breaking changes / migration notes` (→ Breaking Changes) в шаблон. Не пересочинять формулировки — конденсировать features можно, breaking changes — только копировать verbatim.
3. **Verify breaking changes against migration notes**: каждый bullet под "Breaking changes" в CHANGELOG обычно ссылается на epic/wave-код (`FU-*`, `AMP-PARITY-WAVE*`) — сверить, что соответствующий "Added"-bullet выше в CHANGELOG содержит миграционные заметки (обычно секция "Migration notes" внутри самого Added-bullet), и что draft переносит upgrade-шаги, а не только факт breakage.
4. **Backward compatibility**: явно перечислить, что НЕ меняется (например: K8s-Secret-provisioned targets, webhook payload shape) — раздел существует специально, чтобы не пришлось перечитывать код при каждом вопросе "а вот это не сломается?".
5. **Upgrade steps**: свести breaking changes в конкретные действия (`grouping.reconciliation_grace: 20s` → убрать/поднять; `email_configs` → добавить `global.smtp_smarthost`; и т.п.) — читатель должен уйти со списком команд/диффов, не с списком фактов.
6. После `finalize` релизной задачи: переименовать `*-draft.md` → `RELEASE_NOTES_vX.Y.Z.md`, обновить `CHANGELOG.md` (закрыть `[Unreleased]` в `## [X.Y.Z] - YYYY-MM-DD`).
