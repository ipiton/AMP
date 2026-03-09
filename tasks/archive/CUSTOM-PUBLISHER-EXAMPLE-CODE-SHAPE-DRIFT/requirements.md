# Requirements: CUSTOM-PUBLISHER-EXAMPLE-CODE-SHAPE-DRIFT

## Context
После закрытия `SOURCE-EXAMPLES-HISTORICAL-DRIFT` в `examples/custom-publisher/main.go` больше не осталось stale narrative, но сохранился более глубокий residual: self-contained demo code все еще моделирует local `PublishingTarget` через `webhook_url`-centric shape, тогда как current canonical publishing target contract в репозитории уже другой:

- `url`
- `headers`
- `filter_config`
- Secret discovery через `publishing-target=true` + `data.config` / `stringData.config`

Это уже не чистый docs pass. Следующий slice должен честно отделить:

- possible example-code alignment внутри `examples/custom-publisher/main.go`;
- от runtime behavior, Helm behavior и operator docs truth, которые трогать скрыто нельзя.

## Goals
- [x] Зафиксировать стартовый baseline для `CUSTOM-PUBLISHER-EXAMPLE-CODE-SHAPE-DRIFT` как отдельного follow-up после narrative cleanup.
- [x] Сохранить scope узким: только `examples/custom-publisher/main.go` и связанные task artifacts, без скрытого перехода в runtime/Helm code.
- [x] Подготовить честную базу для следующего `/research`, который подтвердит, какой именно sub-slice mergeable: pure example-code shape alignment, narrower docs pointer cleanup, или отдельное planning split.

## Constraints
- Не менять active runtime behavior, publishing implementation, config parser или Helm chart behavior.
- Не переоткрывать `examples/custom-classifier/main.go` и `examples/k8s/*.yaml`, если для нового slice это не требуется.
- Опора на текущий source of truth: `docs/CONFIGURATION_GUIDE.md`, `docs/MIGRATION_QUICK_START.md`, `docs/06-planning/BUGS.md` и архив `PHASE-4-PRODUCTION-PUBLISHING-PATH`.
- Поскольку задача затрагивает executable example shape, а не только prose, следующий шаг должен пройти через `/research`, а не через прямой `/spec`.

## Success Criteria (Definition of Done)
- [x] Создан task workspace и branch для `CUSTOM-PUBLISHER-EXAMPLE-CODE-SHAPE-DRIFT`.
- [x] `NEXT.md` отражает новую активную задачу в WIP.
- [x] Зафиксирован стартовый requirements baseline для дальнейшего `/research`.
- [x] `examples/custom-publisher/main.go` больше не учит `WebhookURL` / `webhook_url` как primary target shape и использует local example shape, ближе к canonical `url` / `headers` / `filter_config` / `format`.
- [x] `Publish(...)` использует `target.URL`, а request headers берутся из `target.Headers` без расширения diff в runtime/Helm code.
- [x] Slice остается self-contained: `go-app/internal/**`, `pkg/core/interfaces/**`, runtime behavior и Helm/config parser не менялись.
