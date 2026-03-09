# Implementation Checklist: HELM-SECONDARY-TEMPLATE-HISTORICAL-DRIFT

## Research & Spec
- [x] Выполнен `research.md` с подтверждением фактического residual inventory в `helm/amp/templates/**` и рекомендацией narrow 3-file slice.
- [x] Подготовлен `Spec.md` с узкой границей на `postgresql-poddisruptionbudget.yaml`, `postgresql-service-headless.yaml`, `postgresql-exporter-configmap.yaml`.

## Vertical Slices
- [x] **Slice A: PostgreSQL Annotation Cleanup** — убрать `150% quality` wording из rendered `tn-98` annotations в `postgresql-poddisruptionbudget.yaml` и `postgresql-service-headless.yaml` без изменения manifest semantics.
- [x] **Slice B: Exporter Banner/Description Cleanup** — убрать `150% observability`, `50+ Metrics` и `150% Quality Target` из top-level wording в `postgresql-exporter-configmap.yaml`, не затрагивая SQL queries, metric names и per-metric descriptions.

## Implementation
- [x] Шаг 1: Обновить `helm/amp/templates/postgresql-poddisruptionbudget.yaml` так, чтобы `metadata.annotations.tn-98` больше не содержал overclaim wording `150% quality`, но оставался узким operational label.
- [x] Шаг 2: Обновить `helm/amp/templates/postgresql-service-headless.yaml` по тому же принципу, синхронно с PodDisruptionBudget.
- [x] Шаг 3: Очистить в `helm/amp/templates/postgresql-exporter-configmap.yaml` только:
  - `metadata.annotations.description`;
  - top banner comments внутри `queries.yaml`;
  не меняя SQL blocks, metric identifiers и `description` полей у конкретных метрик.
- [x] Шаг 4: После правок вручную проверить diff по всем трем YAML-файлам и подтвердить, что template branching, names, selectors, ports, labels и exporter query content не менялись.
- [x] Шаг 5: Не трогать `helm/amp/templates/postgresql-configmap.yaml`, `helm/amp/DEPLOYMENT.md`, `helm/amp/values*.yaml` и любые остальные templates, если для green path этого не требуется.

## Testing
- [x] Прогнать targeted marker scan:
  - `rg -n -i "150% quality|150% observability|50\\+ Metrics|Production-Ready|Alert History|alert-history" helm/amp/templates/postgresql-poddisruptionbudget.yaml helm/amp/templates/postgresql-service-headless.yaml helm/amp/templates/postgresql-exporter-configmap.yaml`
- [x] Выполнить manual review edited scope против:
  - `README.md`
  - `docs/06-planning/DECISIONS.md`
  - `helm/amp/README.md`
- [x] Подтвердить render smoke для обоих supported paths:
  - `helm template amp-dev ./helm/amp -f helm/amp/values-dev.yaml --set profile=lite`
  - `helm template amp ./helm/amp -f helm/amp/values-production.yaml --set profile=standard`
- [x] Проверить `git diff --check`.
- [x] В текущем прогоне `helm template` не упирался в local dependency state: prebuilt chart dependencies уже были доступны, поэтому code-level drift отдельно не маскировался environment issue.

## Write Tests
- [x] Новый code-level test diff не добавлялся: slice ограничен YAML metadata/banner cleanup внутри Helm templates.
- [x] Роль `/write-tests` для этого slice выполняет explicit verification path: marker scan, manual review, render smoke и diff hygiene.
- [x] Во время `/implement` не потребовались behavior-level assertions или test-scope expansion: diff остался доказуемым через scoped verification path.

## Documentation & Cleanup
- [x] На `/write-doc` синхронизировать `requirements.md` и `Spec.md` под фактический результат narrow 3-file slice.
- [x] На `/write-doc` не маскировать `postgresql-configmap.yaml` как “случайно тоже закрытый”: файл остается explicit out-of-scope operational prose review, а не скрытый хвост этого task id.
- [x] Planning artifacts обновлены честно: `HELM-SECONDARY-TEMPLATE-HISTORICAL-DRIFT` переведен в resolved, нового follow-up bug для `helm/amp/templates/**` не потребовалось.

## Finalization Readiness
- [ ] На `/end-task` снимать задачу из WIP только после green marker scan, render smoke и `git diff --check`.
- [ ] В `DONE.md` фиксировать этот slice как narrow residual Helm template cleanup, а не как полный sweep по `helm/amp/templates/**`.
- [ ] Если в процессе обнаружатся новые in-scope historical markers за пределами этих трех файлов, не закрывать их молча этим task id без отдельного planning решения.

## Expected End State
- [x] В `postgresql-poddisruptionbudget.yaml` и `postgresql-service-headless.yaml` больше нет `150% quality` в rendered `tn-98` annotations.
- [x] В `postgresql-exporter-configmap.yaml` больше нет `150% observability`, `50+ Metrics` и `150% Quality Target` в top-level wording.
- [x] Functional content chart'а не изменен: SQL queries, metric names/descriptions и template semantics остаются прежними.
- [x] `postgresql-configmap.yaml` и остальные Helm files остаются либо нетронутыми, либо явно вынесенными из scope без скрытого расширения задачи.

## Open Assumptions
- [ ] Предполагается, что правка `tn-98` annotations в двух rendered manifests остается purely human-facing и не влияет на runtime behavior.
- [ ] Предполагается, что top banner wording в `postgresql-exporter-configmap.yaml` можно сделать честнее без переписывания SQL/query inventory narrative.
- [ ] Предполагается, что `postgresql-configmap.yaml` действительно не нужен для завершения этого slice и не всплывет как обязательный companion diff.

## Blockers / Stop Conditions
- [ ] Если честный cleanup потребует менять SQL queries, metric schema, template expressions или rendered resource semantics, остановиться и вернуть задачу в planning.
- [ ] Если marker scan после правок покажет сопоставимый drift в других templates, не включать их автоматически в этот slice без обновления spec/plan.
- [ ] Если render smoke начнет падать из-за самого diff, а не из-за local dependency state, остановиться и зафиксировать это как реальный blocker задачи.
