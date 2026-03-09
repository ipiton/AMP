# Implementation Checklist: GRAFANA-DASHBOARD-BRANDING-DRIFT

## Research & Spec
- [x] Выполнен `research.md` с подтверждением, что confirmed in-file drift в `grafana/dashboards/alert-history-service.json` сейчас узкий и сводится к top-level `title` и `uid`.
- [x] Подготовлен `Spec.md` с узкой границей на visible-title-only cleanup без изменений `uid`, filename и provisioning/import semantics.

## Vertical Slices
- [x] **Slice A: Visible Dashboard Title Cleanup** — заменить только top-level dashboard title в `grafana/dashboards/alert-history-service.json`, убрав historical `Alert History Service` wording без любых identity-field или dashboard-content changes.

## Implementation
- [x] Шаг 1: Обновить в `grafana/dashboards/alert-history-service.json` только top-level `"title"` с `AMP - Alert History Service` на `AMP - Operations Dashboard`.
- [x] Шаг 2: Явно не трогать:
  - `"uid": "amp-alert-history"`
  - filename `grafana/dashboards/alert-history-service.json`
  - panels, PromQL targets, datasource wiring, tags, templating, schema/version fields
- [x] Шаг 3: После правки вручную проверить diff и подтвердить, что desired change действительно остался one-field JSON wording cleanup без структурных правок файла.

## Testing
- [x] Прогнать targeted search:
  - `rg -n "AMP - Alert History Service|AMP - Operations Dashboard|amp-alert-history" grafana/dashboards/alert-history-service.json`
- [x] Выполнить JSON sanity review:
  - `jq '{title,uid,version}' grafana/dashboards/alert-history-service.json`
- [x] Выполнить manual review edited scope против:
  - `docs/06-planning/BUGS.md`
  - `docs/06-planning/DECISIONS.md`
  - `README.md`
- [x] Проверить `git diff --check`.

## Write Tests
- [x] Новый code-level test diff не планируется: это standalone Grafana dashboard JSON без repo-local execution harness.
- [x] Роль `/write-tests` для этого slice выполняет explicit artifact verification path: targeted search, `jq` sanity parse, manual review и diff hygiene.
- [x] Во время `/implement` не потребовались `uid`/filename cleanup или provisioning changes: narrow visible-title slice остается честно доказуемым без отдельного test harness.

## Documentation & Cleanup
- [x] На `/write-doc` синхронизировать `requirements.md` и `Spec.md` под фактический результат narrow visible-title slice.
- [x] На `/write-doc` не маскировать `uid` и filename как “тоже закрытые”: они остаются explicit out-of-scope identity fields этого pass.
- [x] Planning artifacts обновлены честно: current visible-title bug закрыт как slice, а identity-shaped residual вынесен в отдельный bug вместо подразумеваемого “почти done”.

## Finalization Readiness
- [x] На `/end-task` снимать задачу из WIP только после green marker scan, `jq` sanity parse и `git diff --check`.
- [x] В `DONE.md` фиксировать этот slice как visible dashboard title cleanup, а не как full Grafana identity cleanup.
- [x] После implementation честный residual по `uid` и filename вынесен отдельно в planning artifacts как `GRAFANA-DASHBOARD-IDENTITY-DRIFT`, а не скрыт формулировкой done-state.

## Expected End State
- [x] `grafana/dashboards/alert-history-service.json` больше не показывает historical title `AMP - Alert History Service`.
- [x] Dashboard title становится `AMP - Operations Dashboard`.
- [x] `uid = amp-alert-history` и filename остаются без изменений.
- [x] Dashboard JSON остается syntactically valid и больше не содержит unintended diffs в content/layout/query sections.

## Open Assumptions
- [ ] Предполагается, что visible title cleanup уже достаточно полезен как первый mergeable Grafana slice, даже если `uid` остается historical-shaped.
- [ ] Предполагается, что `AMP - Operations Dashboard` достаточно нейтрален и не overclaims current product/runtime scope.
- [ ] Предполагается, что standalone JSON verification через `rg` + `jq` + manual review является честным quality gate для этого артефакта.

## Blockers / Stop Conditions
- [ ] Если честный cleanup потребует менять `uid`, filename или provisioning/import behavior, остановиться и вернуть задачу в planning.
- [ ] Если manual review покажет, что выбранный новый title сам создает misleading product claim, остановиться и пересогласовать wording на уровне spec.
- [ ] Если diff затронет что-то кроме top-level title, не продолжать под видом narrow slice.
