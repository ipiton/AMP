# HELM-SECONDARY-TEMPLATE-HISTORICAL-DRIFT - Spec

**Status**: Implemented  
**Date**: 2026-03-09  
**Inputs**: `requirements.md`, `research.md`  
**Chosen Direction**: `clean the confirmed residual overclaim wording in three PostgreSQL Helm templates without reopening the broader helm/amp cleanup scope`

**Related Planning**:
- `docs/06-planning/NEXT.md`
- `docs/06-planning/BUGS.md`
- `docs/06-planning/DECISIONS.md`
- `helm/amp/README.md`
- `tasks/archive/SECONDARY-REPO-DOC-HISTORICAL-DRIFT/research.md`

**Implemented Result**:
- `postgresql-poddisruptionbudget.yaml` and `postgresql-service-headless.yaml` now use `Operational hardening baseline` in rendered `tn-98` annotations;
- `postgresql-exporter-configmap.yaml` no longer claims `150% observability`, `50+ Metrics`, or `150% Quality Target` in top-level wording;
- `postgresql-configmap.yaml` stayed out of scope because it no longer carried the same confirmed historical/overclaim pattern;
- verification closed green via marker scan, manual review, `helm template` for lite/dev and standard/prod paths, and `git diff --check`.

---

## 1. Problem Statement

После закрытия `SECONDARY-REPO-DOC-HISTORICAL-DRIFT` основной Helm operator-facing narrative уже выровнен, но в `helm/amp/templates/**` остался узкий residual cluster с overclaim wording, который все еще расходится с current docs truth.

Research подтвердил, что ближайший mergeable scope не равен всему `helm/amp/templates/**`. Явный drift сейчас сосредоточен только в трех PostgreSQL templates:

- `helm/amp/templates/postgresql-poddisruptionbudget.yaml`
- `helm/amp/templates/postgresql-service-headless.yaml`
- `helm/amp/templates/postgresql-exporter-configmap.yaml`

Тип drift тоже уже понятен:

1. `150% quality` в rendered annotations;
2. `150% observability` и `50+ Metrics` в top-level exporter banner strings;
3. отсутствие `Alert History` branding уже не является главной проблемой этого slice.

Поэтому задача этого spec — не “еще раз пройти весь Helm chart”, а закрыть именно confirmed residual wording в narrow 3-file scope.

---

## 2. Goals

1. Убрать confirmed residual overclaim wording из трех PostgreSQL Helm templates.
2. Сохранить diff узким и explainable как docs/metadata cleanup внутри functional YAML files.
3. Не менять chart behavior, rendered manifest semantics, SQL queries, metric names или metric descriptions.
4. Зафиксировать честную границу: `postgresql-configmap.yaml` и прочие Helm files остаются вне этого slice, пока для них нет более сильного drift case.

---

## 3. Non-Goals

1. Не переоткрывать `helm/amp/DEPLOYMENT.md`, `helm/amp/values*.yaml` или уже очищенные templates.
2. Не делать sweep по всему `helm/amp/templates/**`.
3. Не переписывать `postgresql-configmap.yaml`, где остался скорее operational prose, а не тот же confirmed overclaim pattern.
4. Не менять template logic, conditions, selectors, names, ports, labels, queries или metric schema.
5. Не расширять scope в `examples/**`, `grafana/**`, `go-app/internal/**` или runtime/test strings.

---

## 4. Key Decisions

### 4.1 The Slice Is Narrower Than The Bug Title

Хотя task id говорит о secondary Helm templates в целом, research подтвердил, что ближайший честный scope уже:

- `postgresql-poddisruptionbudget.yaml`
- `postgresql-service-headless.yaml`
- `postgresql-exporter-configmap.yaml`

Следствие:

- `/implement` не должен превращаться в broad template cleanup;
- любые дополнительные находки считаются новым planning input, а не “бесплатным заодно”.

### 4.2 Rendered Metadata Changes Are Allowed, But Behavior Changes Are Not

В двух файлах drift сидит в `metadata.annotations.tn-98`, то есть в rendered manifest output, а не только в comments.

Это допустимо, потому что:

- annotation human-facing;
- она не влияет на scheduling, selectors, networking или template branching;
- cleanup остается non-behavioral.

Следствие:

- slice разрешает textual changes в rendered annotations;
- но не разрешает functional edits anywhere else in manifest structure.

### 4.3 Exporter ConfigMap Cleanup Stops At Annotation/Banner Level

В `postgresql-exporter-configmap.yaml` stale wording найдено в:

- `metadata.annotations.description`
- top banner comments внутри `queries.yaml`

При этом сами exporter queries и metric descriptions уже factual.

Следствие:

- cleanup ограничен top-level wording;
- SQL, metric identifiers и individual metric `description` fields вне scope.

### 4.4 `postgresql-configmap.yaml` Stays Out Of Scope

Несмотря на remaining wording вроде `Production hardening` и `Observability`, этот файл больше не содержит того же confirmed historical/overclaim pattern, что три primary files.

Следствие:

- этот spec не включает `postgresql-configmap.yaml`;
- если позже потребуется его prose review, это должен быть отдельный conscious decision, а не silent scope creep.

---

## 5. Scope Model

### 5.1 In Scope

- `helm/amp/templates/postgresql-poddisruptionbudget.yaml`
- `helm/amp/templates/postgresql-service-headless.yaml`
- `helm/amp/templates/postgresql-exporter-configmap.yaml`
- task artifacts, если потребуется зафиксировать verified result

### 5.2 Out Of Scope

- `helm/amp/DEPLOYMENT.md`
- `helm/amp/values.yaml`
- `helm/amp/values-dev.yaml`
- `helm/amp/values-production.yaml`
- `helm/amp/templates/postgresql-configmap.yaml`
- любые остальные файлы из `helm/amp/templates/**`
- `examples/**`
- `grafana/**`
- `go-app/internal/**`

---

## 6. Proposed Implementation

### 6.1 Normalize `tn-98` Annotation Wording In Two PostgreSQL Manifests

В `postgresql-poddisruptionbudget.yaml` и `postgresql-service-headless.yaml` нужно заменить overclaim wording в `metadata.annotations.tn-98` на более узкую operational формулировку, совместимую с current docs truth.

### 6.2 Clean Exporter Annotation And Banner Strings

В `postgresql-exporter-configmap.yaml` нужно:

- убрать `150% observability` из annotation description;
- убрать `50+ Metrics` и `150% Quality Target` из top banner comments;
- сохранить factual смысл файла как custom exporter queries for PostgreSQL monitoring.

### 6.3 Preserve Functional Content

Во всех трех файлах нельзя менять:

- template branching;
- resource names;
- selectors and labels;
- ports and service wiring;
- SQL queries;
- metric names;
- metric field descriptions.

---

## 7. Deliverables

1. `tasks/HELM-SECONDARY-TEMPLATE-HISTORICAL-DRIFT/Spec.md` фиксирует confirmed 3-file scope.
2. В `postgresql-poddisruptionbudget.yaml` и `postgresql-service-headless.yaml` больше нет `150% quality` wording в rendered `tn-98` annotation.
3. В `postgresql-exporter-configmap.yaml` больше нет `150% observability`, `50+ Metrics` и `150% Quality Target` в top-level wording.
4. Остальной Helm/docs domain не маскируется как “случайно закрытый” этим slice.

---

## 8. Acceptance Criteria

Слайс считается спроектированным корректно, если:

1. In-scope список ограничен тремя файлами и не тянет `postgresql-configmap.yaml` без нового обоснования.
2. Допустимые правки описаны как textual cleanup в rendered annotations и banner strings, а не как behavior/config change.
3. Non-goals явно запрещают правки SQL queries, metric names/descriptions и template semantics.
4. Verification path включает marker scan, manual review, `helm template` smoke и `git diff --check`.

---

## 9. Risks And Mitigations

### 9.1 Risk: Scope Quietly Expands Back To Whole Helm Template Cleanup

Mitigation:

- держать `/implement` только в трех файлах;
- любые новые файлы сначала доказывать через отдельный planning decision.

### 9.2 Risk: Exporter Cleanup Accidentally Touches Query Semantics

Mitigation:

- менять только annotation/banner wording;
- не редактировать SQL blocks, metric keys и per-metric descriptions.

### 9.3 Risk: Rendered Annotation Changes Are Mistaken For Functional Changes

Mitigation:

- верифицировать diff вручную;
- подтверждать render smoke через `helm template` для dev/prod paths.

---

## 10. Verification Strategy

Основной verification path для следующего delivery slice:

```bash
rg -n -i "150% quality|150% observability|50\\+ Metrics|Production-Ready|Alert History|alert-history" \
  helm/amp/templates/postgresql-poddisruptionbudget.yaml \
  helm/amp/templates/postgresql-service-headless.yaml \
  helm/amp/templates/postgresql-exporter-configmap.yaml
```

Дополнительно:

- manual review against `README.md`, `docs/06-planning/DECISIONS.md`, `helm/amp/README.md`
- `helm template amp-dev ./helm/amp -f helm/amp/values-dev.yaml --set profile=lite`
- `helm template amp ./helm/amp -f helm/amp/values-production.yaml --set profile=standard`
- `git diff --check`
