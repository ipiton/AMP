# GRAFANA-DASHBOARD-BRANDING-DRIFT - Spec

**Status**: Implemented  
**Date**: 2026-03-09  
**Inputs**: `requirements.md`, `research.md`  
**Chosen Direction**: `сузить первый slice до visible-title-only cleanup в grafana/dashboards/alert-history-service.json без изменений uid, filename или provisioning/import semantics`

**Related Planning**:
- `docs/06-planning/NEXT.md`
- `docs/06-planning/BUGS.md`
- `docs/06-planning/DECISIONS.md`
- `docs/MIGRATION_QUICK_START.md`
- `README.md`
- `grafana/dashboards/alert-history-service.json`

---

## 1. Problem Statement

В репозитории остался narrow Grafana branding residual: standalone dashboard JSON `grafana/dashboards/alert-history-service.json` все еще держит historical top-level title:

- `AMP - Alert History Service`

Research подтвердил, что в самом JSON исторический drift сейчас узкий и фактически ограничен top-level identity/branding fields:

- `title`
- `uid`

Но эти поля относятся к разным risk classes:

1. `title` — operator-facing visible wording;
2. `uid` — identity-shaped field с возможными import/update implications;
3. filename `alert-history-service.json` — тоже identity-like artifact path.

Следовательно, задача не должна скрыто превращаться в dashboard reprovisioning или import-identity rewrite. Нужен узкий mergeable slice: убрать historical branding именно из видимого dashboard title, оставив identity fields нетронутыми.

---

## 2. Goals

1. Убрать из dashboard visible title historical wording `Alert History Service`.
2. Сохранить dashboard JSON syntactically valid и пригодным для manual import/use as-is.
3. Не менять `uid`, filename, queries, datasource wiring или layout.
4. Зафиксировать honest scope: это operator-facing title cleanup, а не full Grafana identity cleanup.

---

## 3. Non-Goals

1. Не менять `uid = amp-alert-history`.
2. Не переименовывать `grafana/dashboards/alert-history-service.json`.
3. Не менять PromQL, panels, thresholds, tags, templating, datasource wiring или schema/version fields.
4. Не добавлять и не править Grafana provisioning/import automation.
5. Не открывать broader repo-wide Grafana/dashboard cleanup вне этого одного JSON файла.

---

## 4. Key Decisions

### 4.1 First Slice Is Visible Branding Only

Research показал, что safest first slice — это только visible-title cleanup.

Причина:

- `title` можно менять как operator-facing wording;
- `uid` и filename уже тянут identity/provisioning risk;
- в текущем repo нет достаточно сильного owner/verification path, чтобы менять identity fields в том же проходе.

### 4.2 `uid` Is Treated As Identity, Not Just Another Branding String

Даже если in-repo references на `amp-alert-history` не найдены, spec не считает это достаточным основанием для rename.

Следовательно:

- `uid` остается вне первого implementation slice;
- его cleanup возможен только отдельным follow-up decision/spec, если он вообще понадобится.

### 4.3 Filename Rename Is Also Out Of Scope

`alert-history-service.json` сам по себе исторически окрашен, но rename path несет тот же risk class, что и `uid`:

- filesystem/import identity;
- possible operator automation dependency вне текущего repo.

Поэтому файл не переименовывается в этом slice.

### 4.4 The New Title Should Be Neutral, Honest, And Repo-Consistent

Новый top-level title должен:

- оставаться под `AMP` branding;
- не ссылаться на `Alert History Service`;
- не обещать broader parity or provisioning semantics.

Chosen target title:

- `AMP - Operations Dashboard`

Это wording:

- operator-facing;
- достаточно широкий для текущего набора panels (alerts, pipeline, LLM, publishing, storage, silencing);
- не привязан к historical service identity.

---

## 5. Proposed Implementation

### 5.1 Update Only The Top-Level Dashboard Title

In `grafana/dashboards/alert-history-service.json` replace:

- `"title": "AMP - Alert History Service"`

with:

- `"title": "AMP - Operations Dashboard"`

### 5.2 Leave Identity Fields Intact

Do not change:

- `"uid": "amp-alert-history"`
- filename `grafana/dashboards/alert-history-service.json`

### 5.3 Preserve JSON Shape Exactly Otherwise

No edits to:

- panels
- targets / PromQL
- datasource wiring
- tags
- templating
- version/schemaVersion

The desired diff should be a one-field JSON wording cleanup.

---

## 6. Deliverables

1. `grafana/dashboards/alert-history-service.json` no longer shows historical `Alert History Service` wording in the visible dashboard title.
2. `uid` and filename remain unchanged and explicitly out of scope.
3. Dashboard JSON remains valid and importable as a standalone artifact.

---

## 7. Acceptance Criteria

This spec is correct if:

1. In-scope is limited to `grafana/dashboards/alert-history-service.json` plus task artifacts.
2. The implementation changes only the top-level visible dashboard title.
3. `uid = amp-alert-history` remains unchanged.
4. The file path remains unchanged.
5. No panel/query/datasource/layout fields are modified.
6. Verification stays lightweight: targeted search, JSON sanity parse, manual review, `git diff --check`.

---

## 8. Verification Strategy

Primary verification path for the next delivery slice:

```bash
rg -n "AMP - Alert History Service|AMP - Operations Dashboard|amp-alert-history" grafana/dashboards/alert-history-service.json
```

JSON sanity:

```bash
jq '{title,uid,version}' grafana/dashboards/alert-history-service.json
```

Manual review against:

- `docs/06-planning/BUGS.md`
- `docs/06-planning/DECISIONS.md`
- `README.md`

And:

- `git diff --check`

Expected verification reading:

- historical title is gone;
- new title is present;
- `uid` stays `amp-alert-history`;
- JSON stays valid.

---

## 9. Risks And Mitigations

### 9.1 Risk: The Slice Quietly Expands Into Identity Cleanup

Mitigation:

- spec explicitly forbids `uid` and filename changes;
- implementation target is one top-level field only.

### 9.2 Risk: New Title Overstates Current Product Scope

Mitigation:

- use neutral wording `AMP - Operations Dashboard`;
- avoid terms implying full Alertmanager parity or broader UI guarantees.

### 9.3 Risk: The Bug Name Suggests More Work Than The First Slice Lands

Mitigation:

- document in planning/task artifacts that this is a narrow visible-title slice;
- if `uid`/filename cleanup is still desired later, treat it as a separate follow-up, not hidden scope.
