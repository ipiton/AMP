# Requirements: HELM-SECONDARY-TEMPLATE-HISTORICAL-DRIFT

## Context
После закрытия `SECONDARY-REPO-DOC-HISTORICAL-DRIFT` operator-facing Helm docs уже синхронизированы с current `AMP` / `controlled replacement` truth, но render-pass подтвердил остаточный historical drift в secondary Helm templates под `helm/amp/templates/**`. Уже известные примеры: `postgresql-poddisruptionbudget.yaml`, `postgresql-service-headless.yaml`, `postgresql-exporter-configmap.yaml`, где все еще остаются historical metadata/comments вроде `150% quality` и старый branding wording.

Это не ломает active chart behavior, но оставляет часть Helm artifacts несогласованной с текущим docs truth. Следующий slice должен закрывать именно этот residual template-level drift, не возвращаясь к уже выровненным `DEPLOYMENT.md` / `values*.yaml` и не расползаясь в `examples`, `grafana` или `go-app/internal/**`.

## Goals
- [x] Зафиксировать стартовый baseline для `HELM-SECONDARY-TEMPLATE-HISTORICAL-DRIFT` как отдельного task id после декомпозиции umbrella docs bug.
- [x] Сохранить scope на Helm secondary templates/comments/metadata без скрытого перехода в chart logic, values behavior или runtime claims.
- [x] Подготовить честную базу для следующего `/research`, который подтвердит полный residual inventory и отделит safe textual cleanup от потенциально risky identity/behavior strings.

## Constraints
- Не менять chart behavior, template expressions, values schema, rendered manifest semantics или runtime/API contracts.
- Не расширять scope обратно в `helm/amp/DEPLOYMENT.md`, `helm/amp/values*.yaml`, `examples/**`, `grafana/**`, `go-app/internal/**`.
- Опора на текущий source of truth: `README.md`, `docs/06-planning/DECISIONS.md`, `helm/amp/README.md`, `docs/06-planning/BUGS.md` и архив `tasks/archive/SECONDARY-REPO-DOC-HISTORICAL-DRIFT/`.
- Поскольку точный список remaining Helm files пока подтвержден только частично render-pass'ом, следующий шаг должен пройти через `/research`, а не через прямой `/spec`.

## Success Criteria (Definition of Done)
- [x] Создан task workspace и branch для `HELM-SECONDARY-TEMPLATE-HISTORICAL-DRIFT`.
- [x] `NEXT.md` отражает новую активную задачу в WIP.
- [x] Подтвержден и закрыт narrow 3-file residual Helm-template slice без расширения обратно в `helm/amp/DEPLOYMENT.md`, `values*.yaml` или весь `helm/amp/templates/**`.
- [x] Verified result подтвержден через marker scan, manual review против planning truth, оба `helm template` smoke path и `git diff --check`.

## Outcome
- В `helm/amp/templates/postgresql-poddisruptionbudget.yaml` и `helm/amp/templates/postgresql-service-headless.yaml` `tn-98` нормализован до `Operational hardening baseline`.
- В `helm/amp/templates/postgresql-exporter-configmap.yaml` убраны `150% observability`, `50+ Metrics` и `150% Quality Target` из top-level wording без правок SQL queries и metric descriptions.
- `postgresql-configmap.yaml` и остальные Helm templates не переоткрывались: новых confirmed historical markers этого класса по `helm/amp/templates/**` больше не найдено.
- `HELM-SECONDARY-TEMPLATE-HISTORICAL-DRIFT` закрыт в planning как завершенный narrow slice.
