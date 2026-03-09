# Implementation Checklist: EXAMPLES-HISTORICAL-DOC-DRIFT

## Research & Spec
- [x] Выполнен `research.md` с inventory по `examples/**` и явным разделением на source-example prose drift и Kubernetes sample contract drift.
- [x] Подготовлен `Spec.md` с узкой границей только на `examples/k8s/pagerduty-secret-example.yaml` и `examples/k8s/rootly-secret-example.yaml`.

## Vertical Slices
- [x] **Slice A: PagerDuty Example Contract Cleanup** — перевести `pagerduty-secret-example.yaml` на canonical publishing target Secret contract и убрать historical `alert-history` namespace/story.
- [x] **Slice B: Rootly Example Contract Cleanup** — перевести `rootly-secret-example.yaml` на canonical publishing target Secret contract и убрать historical namespace/legacy discrete-field shape.

## Implementation
- [x] Шаг 1: Обновить `examples/k8s/pagerduty-secret-example.yaml` так, чтобы:
  - убрать `Alert History Service` wording;
  - убрать hardcoded `alert-history` namespace narrative из usage/troubleshooting;
  - заменить `stringData.target.json` на canonical `stringData.config`.
- [x] Шаг 2: Проверить JSON payload внутри `stringData.config` в PagerDuty example на согласованность с current publishing target contract без добавления реальных секретов или новых product claims.
- [x] Шаг 3: Обновить `examples/k8s/rootly-secret-example.yaml` так, чтобы:
  - уйти от legacy discrete `data.*` fields;
  - использовать canonical `stringData.config`;
  - убрать hardcoded `alert-history` namespace.
- [x] Шаг 4: После правок вручную проверить, что оба examples остаются hand-authored reference manifests, а не превращаются в generated Helm output.
- [x] Шаг 5: Не трогать `examples/custom-classifier/main.go`, `examples/custom-publisher/main.go` и `examples/README.md`, если для green path этого не требуется.

## Testing
- [x] Прогнать targeted search:
  - `rg -n -i "Alert History|alert-history|target.json|api_key:" examples/k8s`
- [x] Выполнить manual review edited scope против:
  - `docs/CONFIGURATION_GUIDE.md`
  - `docs/MIGRATION_QUICK_START.md`
  - `tasks/archive/PHASE-4-PRODUCTION-PUBLISHING-PATH/Spec.md`
- [x] Выполнить YAML sanity review:
  - `publishing-target=true` сохраняется;
  - examples используют `stringData.config` / `data.config` contract;
  - в diff нет real secrets или live credentials.
- [x] Проверить `git diff --check`.

## Write Tests
- [x] Новый code-level test diff не планируется: slice ограничен example YAML contract cleanup.
- [x] Роль `/write-tests` для этого slice выполняет explicit manifest verification path: targeted search, contract review, YAML sanity review и diff hygiene.
- [x] Во время `/implement` не потребовались broader runtime/Helm changes, поэтому slice остается честно доказуемым в рамках example-manifest verification path.

## Documentation & Cleanup
- [x] На `/write-doc` синхронизировать `requirements.md` и `Spec.md` под фактический Kubernetes-examples-only result.
- [x] На `/write-doc` не маскировать `.go` example prose drift как “тоже закрытый” этим task id.
- [x] Planning artifacts синхронизированы через закрытие текущего Kubernetes slice и явный follow-up bug `SOURCE-EXAMPLES-HISTORICAL-DRIFT` для `.go` source examples.

## Finalization Readiness
- [x] На `/end-task` задача снимается из WIP после green targeted search, YAML sanity review и `git diff --check`.
- [x] В `DONE.md` этот slice зафиксирован как Kubernetes example contract cleanup, а не как полный sweep по `examples/**`.
- [x] Новые stale contract markers этим task id не маскируются: `.go` source-example residual вынесен отдельно в `SOURCE-EXAMPLES-HISTORICAL-DRIFT`.

## Expected End State
- [x] `pagerduty-secret-example.yaml` больше не использует `Alert History` / `alert-history` narrative и legacy `target.json` payload key.
- [x] `rootly-secret-example.yaml` больше не использует legacy discrete secret fields как основной example contract и не держит hardcoded `alert-history` namespace.
- [x] Оба example manifests согласованы с current canonical publishing Secret contract.
- [x] `.go` examples остаются explicit follow-up и не смешиваются с этим manifest-focused slice.

## Open Assumptions
- [ ] Предполагается, что `stringData.config` является лучшим hand-authored example format для обоих manifest files, even though runtime ultimately reads `data.config`.
- [ ] Предполагается, что generic namespace example вроде `monitoring` не создает ложного product claim и читается честнее, чем historical `alert-history`.
- [ ] Предполагается, что `.go` examples действительно можно оставить отдельным follow-up без блокировки закрытия этого Kubernetes-only slice.

## Blockers / Stop Conditions
- [ ] Если честный cleanup потребует менять runtime publishing contract, Helm templates или docs source of truth, остановиться и вернуть задачу в planning.
- [ ] Если при выравнивании examples выяснится, что PagerDuty и Rootly требуют принципиально разные contract stories, не форсировать общий format без нового spec decision.
- [ ] Если targeted review покажет, что current canonical publishing Secret contract itself неустойчив или противоречив, остановиться и поднять это как отдельный planning issue вместо маскировки в examples diff.
