# Requirements: SOURCE-EXAMPLES-HISTORICAL-DRIFT

## Context
После закрытия `EXAMPLES-HISTORICAL-DOC-DRIFT` в examples domain остается отдельный residual cluster уже не в Kubernetes manifests, а в source examples:

- `examples/custom-classifier/main.go`
- `examples/custom-publisher/main.go`

Оба файла все еще держат historical `Alert History Service` wording и stale integration narrative. В `custom-publisher` residual шире, чем просто comments: inline example по `publishing.targets` / `webhook_url` / `filters` уже не совпадает с текущим canonical publishing target Secret contract. Это все еще docs/examples work, но уже не тот же тип drift, что в `examples/k8s/*.yaml`.

Следующий slice должен честно отделить safe prose cleanup от возможного contract-sensitive rewrite в source examples и не превращать examples pass в скрытую правку runtime, Helm templates или publishing implementation.

## Goals
- [x] Зафиксировать стартовый baseline для `SOURCE-EXAMPLES-HISTORICAL-DRIFT` как отдельного follow-up после закрытия Kubernetes examples slice.
- [x] Сохранить scope на `examples/custom-*.go` без скрытого перехода в `examples/k8s`, runtime behavior, Helm behavior или broader repo docs cleanup.
- [x] Подготовить честную базу для следующего `/research`, который подтвердит actual residual inventory в source examples и отделит textual wording cleanup от stale config/integration story.

## Constraints
- Не менять runtime behavior, API surface, publishing implementation или Helm chart behavior.
- Не переоткрывать `examples/k8s/*.yaml`, если для нового source-example slice это не требуется.
- Опора на текущий source of truth: `docs/CONFIGURATION_GUIDE.md`, `docs/MIGRATION_QUICK_START.md`, `docs/06-planning/BUGS.md` и архив `PHASE-4-PRODUCTION-PUBLISHING-PATH`.
- Поскольку source examples смешивают top-level prose и inline integration/config examples, следующий шаг должен пройти через `/research`, а не через прямой `/spec`.

## Success Criteria (Definition of Done)
- [x] Создан task workspace и branch для `SOURCE-EXAMPLES-HISTORICAL-DRIFT`.
- [x] `NEXT.md` отражает новую активную задачу в WIP.
- [x] Зафиксирован стартовый requirements baseline для дальнейшего `/research`.

## Implemented Slice Result
- [x] Задача закрывается как narrative/integration-only cleanup slice по `examples/custom-*.go`, а не как full code-shape alignment.
- [x] `examples/custom-classifier/main.go` и `examples/custom-publisher/main.go` больше не держат historical `Alert History Service` wording в top intro и footer/integration blocks.
- [x] `custom-publisher` больше не учит obsolete inline `publishing.targets` / `webhook_url` / `filters` config story и вместо этого ссылается на current publishing discovery docs.
- [x] Residual deeper drift по local executable `custom-publisher` demo shape не скрыт и вынесен в follow-up bug `CUSTOM-PUBLISHER-EXAMPLE-CODE-SHAPE-DRIFT`.
