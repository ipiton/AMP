# Requirements: EXAMPLES-HISTORICAL-DOC-DRIFT

## Context
После закрытия Helm-related docs cleanup в репозитории остается отдельный residual cluster в `examples/**`: stale `Alert History` narrative, старый example contract и historical naming в example source comments и sample manifests. По текущему `BUGS.md` уже подтверждены как минимум:

- `examples/custom-classifier/main.go`
- `examples/custom-publisher/main.go`
- `examples/k8s/pagerduty-secret-example.yaml`

Это уже не Helm/operator docs, но и не production runtime logic. Следующий slice должен честно отделить pure example/docs cleanup от более рискованных contract-like изменений в sample manifests и не превращать examples pass в скрытый product rename или runtime rewrite.

## Goals
- [x] Зафиксировать стартовый baseline для `EXAMPLES-HISTORICAL-DOC-DRIFT` как отдельного task id после Helm slices.
- [x] Сохранить scope на examples/comments/manifests без скрытого перехода в runtime behavior, API semantics или broader branding rewrite за пределами examples domain.
- [x] Подготовить честную базу для следующего `/research`, который подтвердит actual inventory по `examples/**` и отделит safe textual cleanup от example-contract-sensitive strings.

## Constraints
- Не менять runtime behavior, API surface, Helm chart behavior или product claims вне примеров.
- Не расширять scope в `helm/**`, `grafana/**`, `go-app/internal/**` или active code paths за пределами examples files.
- Опора на текущий source of truth: `README.md`, `docs/06-planning/DECISIONS.md`, `docs/06-planning/BUGS.md` и архивы предыдущих docs slices.
- Поскольку examples cluster смешивает `.go` comments и Kubernetes sample manifest semantics, следующий шаг должен пройти через `/research`, а не через прямой `/spec`.

## Success Criteria (Definition of Done)
- [x] Создан task workspace и branch для `EXAMPLES-HISTORICAL-DOC-DRIFT`.
- [x] `NEXT.md` отражает новую активную задачу в WIP.
- [x] Зафиксирован стартовый requirements baseline для дальнейшего `/research`.

## Implemented Slice Result
- [x] Задача закрывается как narrow Kubernetes-examples slice, а не как полный cleanup по `examples/**`.
- [x] `examples/k8s/pagerduty-secret-example.yaml` и `examples/k8s/rootly-secret-example.yaml` выровнены с canonical publishing Secret contract: `publishing-target=true`, `stringData.config` / `data.config`, generic `monitoring` namespace.
- [x] Historical `alert-history`, `target.json` и legacy discrete secret-field story удалены из in-scope manifests без изменения runtime behavior.
- [x] Residual drift в `examples/custom-classifier/main.go` и `examples/custom-publisher/main.go` не скрыт и вынесен в follow-up bug `SOURCE-EXAMPLES-HISTORICAL-DRIFT`.
