# EXAMPLES-HISTORICAL-DOC-DRIFT - Spec

**Status**: Implemented  
**Date**: 2026-03-09  
**Inputs**: `requirements.md`, `research.md`  
**Chosen Direction**: `clean only the Kubernetes publishing examples by aligning them to the canonical publishing target Secret contract and generic namespace story`
**Result**: `implemented as Kubernetes-examples-only slice; residual source-example prose moved to SOURCE-EXAMPLES-HISTORICAL-DRIFT`

**Related Planning**:
- `docs/06-planning/NEXT.md`
- `docs/06-planning/BUGS.md`
- `docs/06-planning/DECISIONS.md`
- `docs/CONFIGURATION_GUIDE.md`
- `tasks/archive/PHASE-4-PRODUCTION-PUBLISHING-PATH/Spec.md`

---

## 1. Problem Statement

`EXAMPLES-HISTORICAL-DOC-DRIFT` initially looked like a general cleanup task for `examples/**`, but research showed that the cluster mixes two different drift domains:

1. source-example prose in `.go` files;
2. outdated Kubernetes sample contract in `examples/k8s/*.yaml`.

The most immediate and deterministic mismatch is now in these two files:

- `examples/k8s/pagerduty-secret-example.yaml`
- `examples/k8s/rootly-secret-example.yaml`

Current problems:

1. historical namespace story:
   - `namespace: alert-history`
   - usage commands and troubleshooting examples tied to `-n alert-history`
2. outdated publishing target payload shape:
   - `stringData.target.json`
   - discrete `data.name/type/url/api_key` fields
3. stale publishing discovery narrative that no longer matches the canonical contract documented after `PHASE-4-PRODUCTION-PUBLISHING-PATH`.

Current source of truth is already explicit:

- canonical discovery label: `publishing-target=true`;
- canonical payload key: `data.config` / `stringData.config`;
- examples should be treated as reference manifests, not as historical `Alert History` deployment instructions.

So this spec intentionally does **not** attempt to clean all examples at once. It chooses the narrowest slice with the highest current user-facing risk: the two Kubernetes sample manifests.

---

## 2. Goals

1. Align the two Kubernetes publishing examples with the current canonical Secret contract.
2. Remove stale `alert-history` namespace narrative from example usage and troubleshooting text.
3. Keep the examples readable as reference YAML without inventing new broader product claims.
4. Leave `.go` example prose cleanup for a follow-up slice instead of mixing prose rewrites with manifest contract changes.

---

## 3. Non-Goals

1. Do not edit `examples/custom-classifier/main.go`.
2. Do not edit `examples/custom-publisher/main.go`.
3. Do not edit `examples/README.md`, which is already aligned.
4. Do not change runtime behavior, Helm behavior, or the publishing implementation itself.
5. Do not introduce real secrets, real routing keys, or production-specific tenant values.
6. Do not expand the task into a repo-wide branding rename beyond these example manifests.

---

## 4. Key Decisions

### 4.1 The First Slice Targets Only `examples/k8s/*.yaml`

Research showed that `.go` examples and Kubernetes manifests require different kinds of fixes:

- `.go` examples need prose narrowing;
- `k8s` examples need concrete contract alignment.

Following that split, this spec covers only:

- `examples/k8s/pagerduty-secret-example.yaml`
- `examples/k8s/rootly-secret-example.yaml`

This keeps the slice mergeable and avoids inventing a new integration story for the `.go` examples.

### 4.2 Canonical Publishing Secret Contract Wins Over Historical Example Shapes

The source of truth for publishing target discovery is already fixed in:

- `docs/CONFIGURATION_GUIDE.md`
- `tasks/archive/PHASE-4-PRODUCTION-PUBLISHING-PATH/Spec.md`

Canonical shape:

- label `publishing-target=true`
- JSON payload in `data.config`
- `stringData.config` is acceptable in hand-authored YAML

Therefore:

- `target.json` should not remain in examples;
- discrete secret fields like `data.name`, `data.type`, `data.url`, `data.api_key` should not remain either.

### 4.3 Structural YAML Changes Are In Scope Here

Unlike the previous Helm docs slices, this task cannot be solved by comments-only edits. The example manifests themselves encode the example contract.

Allowed changes therefore include:

- changing field shape from legacy keys to `stringData.config`;
- changing namespace examples;
- rewriting usage comments around those examples.

Not allowed:

- changing actual runtime code;
- introducing a different contract than the one already documented.

### 4.4 Namespace Examples Should Be Generic, Not Historical

Hardcoded `alert-history` namespace examples now imply a historical deployment story that is no longer the current repo truth.

This spec allows normalizing namespace examples to a generic operator-facing choice such as `monitoring`, while keeping wording flexible enough that users can adapt it to their own namespace.

### 4.5 Examples Must Stay Safe And Obviously Non-Production

These files should remain examples:

- no real tokens;
- no realistic live credentials;
- placeholders remain placeholders;
- comments should make adaptation expectations explicit where useful.

---

## 5. Scope Model

### 5.1 In Scope

- `examples/k8s/pagerduty-secret-example.yaml`
- `examples/k8s/rootly-secret-example.yaml`
- task artifacts if needed to record verified result

### 5.2 Out Of Scope

- `examples/custom-classifier/main.go`
- `examples/custom-publisher/main.go`
- `examples/README.md`
- any files in `helm/**`
- any files in `go-app/**`
- public compatibility/product claims outside these example manifests

---

## 6. Proposed Implementation

### 6.1 Rewrite PagerDuty Example To Canonical Secret Shape

`pagerduty-secret-example.yaml` should:

- stop referring to `Alert History Service`;
- stop using `stringData.target.json`;
- use `stringData.config` with JSON payload compatible with the current publishing target contract;
- stop hardcoding `alert-history` namespace in usage and troubleshooting examples.

### 6.2 Rewrite Rootly Example To Canonical Secret Shape

`rootly-secret-example.yaml` should:

- stop using discrete base64 fields as the primary example shape;
- move to the same canonical `stringData.config` pattern;
- stop hardcoding `alert-history` namespace.

### 6.3 Preserve Example Intent

Both files should remain easy to read as hand-authored examples:

- keep labels necessary for discovery;
- keep placeholder values obvious;
- avoid over-optimizing the YAML into generated-chart style output.

---

## 7. Deliverables

1. `tasks/EXAMPLES-HISTORICAL-DOC-DRIFT/Spec.md` fixes the first narrow sub-slice on Kubernetes examples only.
2. `pagerduty-secret-example.yaml` reflects the canonical publishing target Secret contract.
3. `rootly-secret-example.yaml` reflects the canonical publishing target Secret contract.
4. `.go` examples remain explicit follow-up work and are not silently rewritten in the same slice.

Implementation result:

- `pagerduty-secret-example.yaml` now uses `stringData.config`, `filter_config` and `monitoring` namespace wording instead of historical `target.json` / `alert-history` examples.
- `rootly-secret-example.yaml` now uses the same canonical Secret contract instead of legacy discrete `data.*` fields.
- `.go` source examples were intentionally left out of this slice and are tracked separately in `SOURCE-EXAMPLES-HISTORICAL-DRIFT`.

---

## 8. Acceptance Criteria

This spec is considered correct if:

1. In-scope is limited to the two `examples/k8s/*.yaml` files.
2. The chosen contract explicitly aligns to `publishing-target=true` + `stringData.config` / `data.config`.
3. Historical `alert-history` namespace narrative is removed from these example manifests.
4. The spec does not silently include `.go` example rewrites.
5. Verification path is explicit: targeted search, manual review against canonical publishing docs, YAML sanity review, and `git diff --check`.

---

## 9. Risks And Mitigations

### 9.1 Risk: Quiet Scope Expansion Into `.go` Examples

Mitigation:

- keep `/implement` limited to the two YAML files;
- treat `.go` example prose as a separate follow-up slice.

### 9.2 Risk: Example YAML Drifts Away From Runtime Contract Again

Mitigation:

- anchor the examples directly to `docs/CONFIGURATION_GUIDE.md` and the archived `PHASE-4` contract;
- use the same `config` payload key as the current documented contract.

### 9.3 Risk: Examples Become Too Generated Or Too Product-Specific

Mitigation:

- preserve hand-authored example readability;
- keep placeholders generic and safe;
- avoid copying chart-generated manifest noise into examples.

### 9.4 Risk: Namespace Choice Is Over-Specified

Mitigation:

- prefer a generic namespace example such as `monitoring`;
- wording should make it clear that the operator can use a different namespace.

---

## 10. Verification Strategy

Primary verification path for the next delivery slice:

```bash
rg -n -i "Alert History|alert-history|target.json|api_key:" examples/k8s
```

Manual review against:

- `docs/CONFIGURATION_GUIDE.md`
- `docs/MIGRATION_QUICK_START.md`
- `tasks/archive/PHASE-4-PRODUCTION-PUBLISHING-PATH/Spec.md`

YAML sanity review:

- `publishing-target=true` present where needed
- `stringData.config` / `data.config` used instead of legacy shapes
- no real secrets or live credentials

And:

- `git diff --check`
