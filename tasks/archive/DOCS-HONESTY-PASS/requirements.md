# Requirements: DOCS-HONESTY-PASS

## Context
После `ALERTMANAGER-REPLACEMENT-SCOPE` верхнеуровневый replacement claim уже сужен до `controlled replacement`, но публичная документация все еще содержит residual drift. В частности, в README и migration/comparison docs остаются неподтвержденные performance/resource figures, несинхронный install narrative и конфликт по license text. Нужно довести docs до состояния, где public story не обещает больше, чем реально подтверждено кодом, planning artifacts и текущим verified runtime.

## Goals
- [x] Синхронизировать README, compatibility и migration docs с active-runtime-first narrative без overclaims.
- [x] Убрать или явно оговорить неподтвержденные claims по performance/resources, plugin/extensibility wording, install path и license.
- [x] Оставить public docs полезными для controlled replacement/pilot deployment, не сваливаясь в marketing claims без verification.

## Constraints
- Это docs/product slice: active runtime, tests и bootstrap scope не расширяются.
- Source of truth: `go-app/cmd/server/main.go`, `go-app/internal/application/router.go`, актуальные planning artifacts в `docs/06-planning/`.
- README должен оставаться на английском; planning/task docs можно вести на русском.
- Дифф должен быть минимальным: только claims, термины, install/licensing consistency и связанные пояснения.

## Success Criteria (Definition of Done)
- [x] README, `docs/ALERTMANAGER_COMPATIBILITY.md`, `docs/MIGRATION_QUICK_START.md` и `docs/MIGRATION_COMPARISON.md` не содержат claims, противоречащих verified runtime и planning.
- [x] License/install/performance statements приведены к одному честному narrative или явно помечены как historical/non-verified.
- [x] Изменения проверены через targeted review и `git diff --check`.

## Final Status

Задача выполнена как top-level public/docs honesty slice.

Реально доставлено:
- `README.md`, compatibility, migration и chart docs/metadata переведены на `controlled replacement` / `active-runtime-first` narrative;
- прямые overclaims про `drop-in replacement`, `100% API compatible`, неподтвержденные benchmark/resource figures и конфликтный install/license story убраны из core public surface;
- chart/package narrative выровнен с repo-local source of truth (`./helm/amp`, AGPL-3.0, phased parity).

Оставшийся follow-up:
- в репозитории остаются более глубокие internal/subpackage docs с историческими `Apache 2.0`, `Production-Ready` и similar claims; это уже вне минимального top-level public scope текущего slice.
