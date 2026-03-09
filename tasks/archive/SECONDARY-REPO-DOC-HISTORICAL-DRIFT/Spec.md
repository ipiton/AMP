# SECONDARY-REPO-DOC-HISTORICAL-DRIFT - Spec

**Status**: Implemented  
**Date**: 2026-03-09  
**Inputs**: `requirements.md`, `research.md`  
**Chosen Direction**: `take the first narrow sub-slice in the broader secondary-doc domain by cleaning Helm operator-facing docs/comments in helm/amp without touching chart behavior, examples, grafana, or internal README rewrites`

**Related Planning**:
- `docs/06-planning/NEXT.md`
- `docs/06-planning/BUGS.md`
- `docs/06-planning/DECISIONS.md`
- `helm/amp/README.md`
- `tasks/archive/REPO-DOC-LICENSE-DRIFT/Spec.md`

**Implemented Result**:
- `helm/amp/DEPLOYMENT.md` rewritten to current `./helm/amp` + `amp` operator story;
- selected Helm values/templates comments and description strings cleaned from stale `Alert History` / `Production-Ready` markers;
- hardcoded `llm.apiKey` defaults in `helm/amp/values.yaml` and `helm/amp/values-dev.yaml` sanitized;
- remaining secondary-doc umbrella decomposed into narrower follow-up bugs instead of being silently carried under the same task id.

---

## 1. Problem Statement

`SECONDARY-REPO-DOC-HISTORICAL-DRIFT` описывает широкий residual doc-hygiene хвост после `REPO-DOC-LICENSE-DRIFT`: historical markers остались в `helm/amp/**`, `examples/**`, `grafana/**` и `go-app/internal/**`.

Research показал, что это не один implementation slice, а umbrella-domain:

1. `helm/amp/**` содержит operator-facing deployment drift;
2. `examples/**` смешивает comments и example contract;
3. `grafana/**` упирается не только в title, но и в identity fields;
4. `go-app/internal/**` требует уже не cleanup, а больших factual README rewrites;
5. рядом всплывают runtime/test strings, которые нельзя молча включать в docs-only pass.

Поэтому задача этого spec не “закрыть весь bug”, а выбрать первый mergeable sub-slice с низким риском scope creep.

Выбранный target:

- `helm/amp/DEPLOYMENT.md`
- `helm/amp/values-dev.yaml`
- `helm/amp/values-production.yaml`
- `helm/amp/values.yaml`
- `helm/amp/templates/postgresql-configmap.yaml`
- `helm/amp/templates/postgresql-networkpolicy.yaml`
- `helm/amp/templates/postgresql-statefulset.yaml`

Именно здесь еще остаются stale `Alert History` / `Production-Ready` markers, old chart path/naming и устаревший deployment narrative, хотя верхнеуровневый [helm/amp/README.md](/Users/vit/Documents/Projects/AMP/helm/amp/README.md) уже синхронизирован с `controlled replacement` truth.

---

## 2. Goals

1. Синхронизировать Helm operator-facing docs/comments с текущим `AMP` / `controlled replacement` / active-runtime-first truth.
2. Переписать `helm/amp/DEPLOYMENT.md` под текущий `./helm/amp` path, актуционный naming и честный deployment story без stale `Alert History Service` narrative.
3. Убрать stale `Alert History` / `Production-Ready` wording из chart comments и descriptive metadata в выбранных Helm files.
4. Сохранить slice narrow и non-behavioral: без изменений chart logic, values schema, runtime surface или product claims beyond already accepted planning truth.

---

## 3. Non-Goals

1. Не закрывать весь umbrella bug `SECONDARY-REPO-DOC-HISTORICAL-DRIFT` за один проход.
2. Не трогать `examples/**`, `grafana/**` и `go-app/internal/**` в этом slice.
3. Не менять [helm/amp/README.md](/Users/vit/Documents/Projects/AMP/helm/amp/README.md), если не обнаружится прямое противоречие verified truth.
4. Не переписывать `helm/amp/CHANGELOG.md`: historical changelog entries не считаются приоритетным operator-facing target в этом pass.
5. Не менять chart behavior, rendered resource semantics, values keys, default values или runtime/API contracts.
6. Не превращать cleanup comments/metadata в отдельный security/config pass по конкретным value payloads.

---

## 4. Key Decisions

### 4.1 This Bug Is An Umbrella Domain, So The Slice Must Be Explicitly Narrow

Source of truth для этого решения:

- `tasks/SECONDARY-REPO-DOC-HISTORICAL-DRIFT/research.md`
- `docs/06-planning/BUGS.md`
- `README.md`
- `docs/06-planning/DECISIONS.md`

Следствие:

- этот spec фиксирует только первый sub-slice по Helm operator assets;
- remaining clusters не маскируются и не считаются автоматически закрытыми.

### 4.2 Helm README Is Already Aligned And Must Not Drive New Work

`helm/amp/README.md` уже говорит о:

- `controlled replacement`;
- `AGPL-3.0`;
- current active runtime surface.

Следствие:

- rewrite нужен не в README, а в `DEPLOYMENT.md` и comment-heavy Helm files, которые еще держат старый operator story.

### 4.3 `DEPLOYMENT.md` Gets A Factual Operator Guide Rewrite

`helm/amp/DEPLOYMENT.md` сейчас устарел сразу по нескольким осям:

- old repo/chart path `./helm/alert-history`;
- old release/service naming `alert-history-*`;
- historical `Alert History Service` branding;
- overly specific deployment flow, который не выровнен с current active-runtime-first truth.

Решение:

- допускается rewrite файла целиком, если это самый маленький способ вернуть ему honest contract;
- rewrite должен остаться operator-facing и practical, а не превращаться в новый marketing doc.

### 4.4 YAML Values And Templates Are Comment/Metadata Cleanup Only

Для `values*.yaml` и `templates/*.yaml` этот slice ограничен:

- comments;
- descriptive strings;
- non-behavioral metadata вроде human-readable description annotations.

Недопустимо:

- менять keys;
- менять default values;
- менять schema assumptions;
- менять template logic.

Если строка влияет на human-facing description, но не на behavior, это допустимый scope. Если строка участвует в functional contract, она вне scope.

### 4.5 Current Truth Must Come From Active-Runtime-First Planning

Разрешенный narrative для этого slice:

- `AMP`, а не `Alert History Service`;
- `controlled replacement`, а не `general-purpose drop-in replacement`;
- `./helm/amp`, а не `./helm/alert-history`;
- только те runtime/deployment claims, которые совместимы с `README.md` и `ADR-002` / `ADR-006`.

### 4.6 English Helm Docs Stay English

Планирование ведется на русском, но сами operator-facing Helm docs/comments остаются на английском, чтобы не ломать текущий language contract chart artifacts.

---

## 5. Scope Model

### 5.1 In Scope

- `helm/amp/DEPLOYMENT.md`
- `helm/amp/values-dev.yaml`
- `helm/amp/values-production.yaml`
- `helm/amp/values.yaml`
- `helm/amp/templates/postgresql-configmap.yaml`
- `helm/amp/templates/postgresql-networkpolicy.yaml`
- `helm/amp/templates/postgresql-statefulset.yaml`
- task artifacts, если потребуется зафиксировать verified result

### 5.2 Out Of Scope

- `helm/amp/README.md`
- `helm/amp/CHANGELOG.md`
- `examples/**`
- `grafana/**`
- `go-app/internal/**`
- любые `.go` runtime/test strings
- chart logic, values data, Kubernetes manifests semantics, runtime behavior

---

## 6. Proposed Implementation

### 6.1 Rewrite `helm/amp/DEPLOYMENT.md`

Целевой контракт:

- current chart path = `./helm/amp`;
- current naming = `amp`, а не `alert-history`;
- deployment story не обещает шире, чем уже разрешено top-level docs;
- примеры команд и URLs не тянут historical routes/names из старого bootstrap story.

Файл может стать заметно короче, если это делает его честнее и понятнее.

### 6.2 Clean Helm Values Headers And Comment Blocks

В `values-dev.yaml`, `values-production.yaml`, `values.yaml` нужно:

- убрать stale `Alert History Service`;
- убрать `Production-Ready` wording там, где оно работает как historical overclaim;
- оставить только factual comments, которые помогают оператору читать chart values.

### 6.3 Clean Template Comments And Human-Facing Metadata

В `postgresql-configmap.yaml`, `postgresql-networkpolicy.yaml`, `postgresql-statefulset.yaml` нужно:

- убрать `Alert History` branding в comments/description strings;
- убрать `Production-Ready` wording;
- оставить explanations, если они все еще помогают читать template, но уже без misleading historical framing.

### 6.4 Preserve Rendered Behavior

Любая правка должна быть explainable как textual/non-behavioral cleanup.

Если во время implementation окажется, что desired cleanup требует менять actual chart logic или runtime wiring, задача должна остановиться и вернуться к planning, потому что это уже другой slice.

---

## 7. Deliverables

1. `tasks/SECONDARY-REPO-DOC-HISTORICAL-DRIFT/Spec.md` фиксирует narrow Helm-only slice.
2. `helm/amp/DEPLOYMENT.md` больше не описывает старый `alert-history` deployment path.
3. В выбранных Helm values/templates comments и description strings больше нет stale `Alert History` / `Production-Ready` markers.
4. Remaining examples/grafana/internal README drift остается explicit follow-up в отдельных bugs и не маскируется как “случайно закрытый”.

---

## 8. Acceptance Criteria

Слайс считается завершенным, если одновременно выполнены условия:

1. `helm/amp/DEPLOYMENT.md` использует current `./helm/amp` path и не держит stale `Alert History Service` / `alert-history-*` operator narrative.
2. В in-scope Helm files больше нет `Alert History` / `Production-Ready` markers, которые противоречат current planning truth.
3. `helm/amp/README.md` не пришлось переписывать для реализации этого slice.
4. Изменения остаются non-behavioral: не меняются chart keys/defaults/template logic/runtime semantics.
5. Verification path выполняем:
   - targeted `rg` по stale markers;
   - manual review against `README.md`, `docs/06-planning/DECISIONS.md`, `helm/amp/README.md`;
   - `git diff --check`.

---

## 9. Risks And Mitigations

### 9.1 Risk: Accidentally Changing Chart Behavior While Editing YAML

Values и templates находятся в functional files, даже если меняется только prose.

Mitigation:

- трогать только comments, description strings и явно non-behavioral metadata;
- не менять keys, values, conditions или template expressions.

### 9.2 Risk: Replacing Historical Claims With New Unverified Claims

Есть риск убрать старый marketing language, но случайно написать новый, который тоже не подтвержден.

Mitigation:

- опираться только на `README.md`, `DECISIONS.md` и уже aligned `helm/amp/README.md`;
- в сомнительных местах предпочитать более узкую формулировку.

### 9.3 Risk: Hidden Scope Expansion Into Other Clusters

Во время cleanup легко “заодно” пойти в examples, Grafana или internal README.

Mitigation:

- держать scope строго в перечисленных Helm files;
- любые дополнительные находки оставлять explicit follow-up в отдельных bugs.

### 9.4 Risk: Mixing Docs Cleanup With Separate Security/Config Work

В values files могут лежать вопросы, которые уже относятся не к comments cleanup, а к config hygiene.

Mitigation:

- этот slice не должен превращаться в rotation/config-hardening pass;
- если всплывает отдельная config/security проблема, ее нужно фиксировать отдельно, а не скрыто лечить в docs-only задаче.

---

## 10. Verification Strategy

Основной verification path:

```bash
rg -n 'Alert History|Production-Ready|alert-history' \
  helm/amp/DEPLOYMENT.md \
  helm/amp/values-dev.yaml \
  helm/amp/values-production.yaml \
  helm/amp/values.yaml \
  helm/amp/templates/postgresql-configmap.yaml \
  helm/amp/templates/postgresql-networkpolicy.yaml \
  helm/amp/templates/postgresql-statefulset.yaml

git diff --check
```

Плюс manual review против:

- `README.md`
- `docs/06-planning/DECISIONS.md`
- `helm/amp/README.md`
