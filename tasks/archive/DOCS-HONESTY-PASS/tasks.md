# Implementation Checklist: DOCS-HONESTY-PASS

## Research & Spec
- [x] Завершен `research.md` по residual docs drift, chart metadata и install/license inconsistencies.
- [x] Подготовлен `Spec.md` с active-runtime-first docs policy, chart metadata scope и acceptance criteria.

## Vertical Slices
- [x] **Slice A: Direct Contradiction Cleanup** — убрать claims, которые прямо противоречат active runtime и current positioning (`drop-in`, `100% compatibility`, unsupported `ACTIVE` routes, `all endpoints supported`).
- [x] **Slice B: Narrative Alignment** — выровнять migration/comparison/install/license wording под `controlled replacement` и repo-verifiable install story, не расползаясь в full docs rewrite.

## Implementation
- [x] Шаг 1: Синхронизировать top-level positioning в `README.md`, `docs/ALERTMANAGER_COMPATIBILITY.md`, `helm/amp/README.md` и `helm/amp/Chart.yaml`.
- [x] Шаг 2: Убрать или понизить route-level/current-feature claims для unsupported active routes (`status`, `receivers`, `reload` и аналогичных).
- [x] Шаг 3: Переформулировать `docs/MIGRATION_COMPARISON.md` и related sections из superiority narrative в honest controlled-replacement comparison.
- [x] Шаг 4: Выровнять install story и AGPL wording в `README.md`, migration docs и chart/package metadata.
- [x] Шаг 5: Если diff остаётся компактным, добрать 1-2 верхнеуровневых residual drift references вне core set; иначе оставить их как follow-up.

## Testing
- [x] Targeted review edited files against `go-app/internal/application/router.go`, `docs/06-planning/BUGS.md` и `docs/06-planning/ALERTMANAGER-REPLACEMENT-GAP-ANALYSIS.md`.
- [x] Search pass по edited scope подтверждает отсутствие прямых overclaims вроде `drop-in replacement`, `100% Alertmanager API compatible`, `all endpoints supported` как current claims.
- [x] `git diff --check` проходит для touched docs/metadata files.
- [x] Runtime/code changes не вносятся; compile/test suite не используется как primary gate для этого docs slice.

## Documentation & Cleanup
- [x] Синхронизировать `requirements.md`, если в ходе работы фактический docs scope окажется уже или шире текущего spec.
- [x] Обновить `Spec.md`, если придется официально вынести часть chart/package cleanup из текущего slice.
- [x] Перед `/end-task` обновить planning artifacts, если будут закрыты или переформулированы текущие docs-related bugs/follow-ups.

## Final Status
- Top-level public/docs honesty slice завершен и переведен в planning как completed task.
- Core public/docs and chart surface выровнены под `controlled replacement` / `active-runtime-first` narrative.
- Residual doc cleanup сужен до более глубокого repo-wide follow-up `REPO-DOC-LICENSE-DRIFT`.
