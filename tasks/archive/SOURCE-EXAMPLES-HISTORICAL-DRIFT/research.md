# Research: SOURCE-EXAMPLES-HISTORICAL-DRIFT

## Context

После закрытия Kubernetes examples slice (`EXAMPLES-HISTORICAL-DOC-DRIFT`) в examples domain остался residual drift уже не в YAML manifests, а в source examples:

- `examples/custom-classifier/main.go`
- `examples/custom-publisher/main.go`

Цель этого research — понять, где здесь просто historical wording, а где уже начинается example-contract drift, который нельзя чинить “по инерции” в docs-only pass.

---

## Files Reviewed

- `examples/custom-classifier/main.go`
- `examples/custom-publisher/main.go`
- `docs/CONFIGURATION_GUIDE.md`
- `docs/MIGRATION_QUICK_START.md`
- `tasks/archive/PHASE-4-PRODUCTION-PUBLISHING-PATH/Spec.md`
- `go-app/pkg/core/interfaces/classifier.go`
- `go-app/pkg/core/interfaces/publisher.go`
- `go-app/internal/core/interfaces.go`

---

## Findings

### 1. `custom-classifier/main.go` mostly contains prose/integration drift

Confirmed residuals:

- top-level intro still says `How to integrate with Alert History Service`;
- footer block is titled `Integration with Alert History Service`;
- integration snippet still says `Configure Alert History to use your classifier`;
- the snippet uses `config.yml` with:

```yaml
classification:
  default_classifier: ml-classifier
```

Why this matters:

- the historical product naming is stale;
- `classification.default_classifier` is not confirmed by current public source-of-truth docs used in this repo cleanup pass;
- however, the actual executable body of the example is not obviously the main problem here. The drift is concentrated in comment blocks and integration narrative.

Conclusion:

- `custom-classifier/main.go` looks like a safe candidate for a comments/prose cleanup slice.

### 2. `custom-publisher/main.go` is broader than classifier and mixes prose drift with contract drift

Confirmed residuals:

- top-level intro still says `How to integrate with Alert History Service`;
- footer block is titled `Integration with Alert History Service`;
- the inline integration example still teaches:

```yaml
publishing:
  targets:
    - name: ops-team
      type: ms-teams
      webhook_url: ...
      enabled: true
      filters:
        ...
```

Why this matters:

- current repo truth for publishing targets is no longer “inline `publishing.targets` with `webhook_url` and `filters`” as the canonical operator contract;
- current operator-facing contract is:
  - runtime toggles in `publishing.*`;
  - target discovery via Kubernetes Secret with label `publishing-target=true`;
  - target payload in `data.config` / `stringData.config`;
- this is explicit in `docs/CONFIGURATION_GUIDE.md`, `docs/MIGRATION_QUICK_START.md`, and archived `PHASE-4` spec.

Additional nuance:

- the example also defines a local `PublishingTarget` struct with `WebhookURL string 'json:"webhook_url"'`;
- the example’s `Publish(...)` path uses that local shape directly.

This means `custom-publisher/main.go` has two different drift classes:

1. historical wording/integration comments;
2. a self-contained example code shape that no longer matches the current canonical publishing target contract.

Conclusion:

- `custom-publisher/main.go` is not a pure comments-only file;
- a full “make the example contract current” pass would require code/example-shape decisions, not just wording cleanup.

### 3. Current source of truth distinguishes runtime config from target contract

From the reviewed docs:

- `publishing.enabled`, `publishing.discovery.namespace`, refresh/health/queue settings are still real runtime config;
- but actual target definitions are now documented through Secret discovery:
  - label `publishing-target=true`;
  - payload in `data.config`;
  - hand-authored YAML may use `stringData.config`.

This distinction matters because the stale part in `custom-publisher/main.go` is not merely old wording. It teaches an older target-definition story.

### 4. There is no evidence that the current task should rewrite runtime or Helm sources

Nothing in the reviewed repo state suggests this task should touch:

- `examples/k8s/*.yaml` again;
- `helm/**`;
- runtime code;
- publishing implementation;
- active config parser.

So any next slice must stay inside `examples/custom-*.go`.

---

## Option Assessment

### Option A — comments/integration cleanup only in both source examples

Scope:

- rewrite top-level intro wording in both files;
- rewrite/remove `Integration with Alert History Service` footer blocks;
- in `custom-publisher`, replace the stale inline config example with a short pointer to the canonical publishing Secret docs instead of teaching `publishing.targets`/`webhook_url`/`filters`.

Pros:

- smallest mergeable slice;
- fits current `docs/` task/branch direction;
- fixes the highest-signal user-facing drift without touching executable demo logic.

Cons:

- leaves the local `PublishingTarget` demo shape in `custom-publisher/main.go` as-is;
- does not attempt deeper code/example contract alignment.

### Option B — fully align `custom-publisher/main.go` to current publishing contract

Scope:

- rewrite comments plus local example structs/usages to match canonical `PublishingTarget` shape (`url`, `headers`, `filter_config`, etc.).

Pros:

- would make the file more semantically current end-to-end.

Cons:

- turns a docs/examples cleanup into code/example-contract rewrite;
- requires new decisions about how much of the self-contained example should track active runtime types versus remain pedagogical;
- materially riskier than the current task framing.

### Option C — split classifier and publisher into separate tasks immediately

Pros:

- maximum clarity of risk separation.

Cons:

- extra planning overhead right now is not necessary if the next slice is kept narrow enough.

---

## Recommendation

The most honest next `/spec` is **Option A**:

- keep the task limited to `examples/custom-classifier/main.go` and `examples/custom-publisher/main.go`;
- treat the next slice as **source-example narrative/integration cleanup**, not as full code contract alignment;
- remove historical `Alert History Service` wording;
- replace stale integration guidance with AMP/current-runtime wording;
- in `custom-publisher`, remove or rewrite the inline `publishing.targets` example so it no longer teaches the obsolete target-definition contract, and point readers to the canonical publishing Secret docs instead.

Important boundary:

- do **not** rewrite local example structs, imports, or executable flow in the first slice;
- if later we want `custom-publisher` to model the canonical target contract in code as well, that should be treated as a separate follow-up decision, not folded into this pass silently.

---

## Proposed `/spec` Scope

Recommended in-scope files:

- `examples/custom-classifier/main.go`
- `examples/custom-publisher/main.go`

Recommended in-scope changes:

- top intro comment cleanup;
- footer/integration section cleanup;
- removal/rewrite of stale inline integration examples that contradict current docs;
- references/pointers to `docs/CONFIGURATION_GUIDE.md` and current publishing discovery story where useful.

Recommended out-of-scope changes:

- `examples/k8s/*.yaml`
- runtime/Helm code
- changing executable demo structs/functions to current runtime contract
- broader repo docs cleanup

---

## Verification Notes For The Next Slice

If `/spec` follows the recommendation above, the future verification path should stay lightweight:

- targeted search for `Alert History Service` in `examples/custom-*.go`;
- targeted search for stale `publishing.targets` / `webhook_url` comment snippets in `examples/custom-publisher/main.go`;
- manual review against:
  - `docs/CONFIGURATION_GUIDE.md`
  - `docs/MIGRATION_QUICK_START.md`
  - `tasks/archive/PHASE-4-PRODUCTION-PUBLISHING-PATH/Spec.md`
- `git diff --check`

---

## Research Outcome

`SOURCE-EXAMPLES-HISTORICAL-DRIFT` is narrower than it first looked, but still not a single-class cleanup:

- classifier file: mostly prose drift;
- publisher file: prose drift plus stale integration-contract story.

The best next move is a **narrative/integration cleanup slice** across both files, while explicitly leaving deeper example-code contract alignment out of scope for now.
