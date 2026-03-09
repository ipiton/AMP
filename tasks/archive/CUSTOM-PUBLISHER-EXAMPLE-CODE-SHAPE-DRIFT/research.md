# Research: CUSTOM-PUBLISHER-EXAMPLE-CODE-SHAPE-DRIFT

## Context

После закрытия `SOURCE-EXAMPLES-HISTORICAL-DRIFT` файл `examples/custom-publisher/main.go` больше не учит stale narrative, но его self-contained demo code все еще живет на собственном target shape:

- `WebhookURL`
- `Headers`
- `Enabled`

Это уже не совпадает с current canonical publishing target contract, который в активном runtime и docs зафиксирован как:

- `url`
- `headers`
- `filter_config`
- `format`
- Secret discovery через `publishing-target=true` + `data.config` / `stringData.config`

Цель research — понять, какой следующий slice вообще честен: прямой alignment к active runtime truth, alignment к `pkg/core/interfaces`, или более узкий local example cleanup.

---

## Files Reviewed

- `examples/custom-publisher/main.go`
- `examples/README.md`
- `go-app/pkg/core/README.md`
- `go-app/pkg/core/interfaces/publisher.go`
- `go-app/pkg/core/interfaces/classifier.go`
- `go-app/pkg/core/domain/classification.go`
- `go-app/internal/core/interfaces.go`
- `docs/CONFIGURATION_GUIDE.md`
- `tasks/archive/PHASE-4-PRODUCTION-PUBLISHING-PATH/Spec.md`

---

## Findings

### 1. `custom-publisher` currently uses a third contract, not the repo’s main two

There are actually three different shapes in play:

1. **Local example shape in `examples/custom-publisher/main.go`**
   - `name`
   - `type`
   - `webhook_url`
   - `headers`
   - `enabled`

2. **`pkg/core/interfaces` publishing shape**
   - `Name`
   - `Type`
   - `Endpoint`
   - `Headers`
   - `Config`
   - `Filters`
   - metadata fields

3. **Active runtime / canonical Secret shape**
   - `name`
   - `type`
   - `url`
   - `headers`
   - `filter_config`
   - `enabled`
   - `format`

So the file is not merely “slightly outdated”. It is centered on a local third shape that matches neither `pkg/core/interfaces.PublishingTarget` nor `internal/core.PublishingTarget`.

### 2. Direct alignment to active runtime `internal/core` is not a clean target for `examples/`

Important structural fact:

- the only `go.mod` in the repo is `go-app/go.mod`;
- `examples/` lives outside `go-app/`;
- active runtime target contract lives in `go-app/internal/core/interfaces.go`.

That means a direct “just use `internal/core.PublishingTarget` in the example” is not a good target:

- `internal/` is not intended as an examples-facing contract;
- `examples/` sits outside the module subtree where that internal package naturally belongs;
- even aside from Go import rules, this would be a repo-structure smell and a scope jump into runtime coupling.

Conclusion:

- the active runtime Secret contract can be a **shape reference**, but not the direct code dependency target for this example.

### 3. `examples/README.md` frames examples as shape-and-wiring references, not strict compile-validated plugins

`examples/README.md` explicitly says:

- examples are “small reference examples for extension patterns around the current `pkg/core` contracts”;
- they should be read as “examples of shape and wiring”;
- they are not a promise of a full plugin system.

That matters because it lowers the bar from “must exactly implement a live runtime interface” to “should not teach the wrong shape”.

### 4. But `custom-publisher` is not even a strict `pkg/core/interfaces` example today

`pkg/core/interfaces.AlertPublisher` expects:

- `Publish(ctx context.Context, alert EnrichedAlert, target PublishingTarget) error`

where:

- `EnrichedAlert` is from `pkg/core/interfaces`
- `PublishingTarget` uses `Endpoint`, `Config`, `Filters`

`examples/custom-publisher/main.go` instead uses:

- `Publish(ctx context.Context, alert *domain.EnrichedAlert, target *PublishingTarget) error`
- local `PublishingTarget` with `WebhookURL`

So even if examples are only illustrative, this file is not currently a faithful code example for `pkg/core/interfaces.AlertPublisher` either.

### 5. `custom-classifier` shows this is a broader examples-pattern issue, not just publisher

The neighboring `examples/custom-classifier/main.go` also claims `interfaces.AlertClassifier`, while its method signatures use `*domain.Alert` and `*domain.ClassificationResult`, not the value-based types from `pkg/core/interfaces`.

This is important because it means:

- the examples directory today is not a compile-exact SDK demo surface;
- forcing full `pkg/core/interfaces` conformance inside this one task would be a larger examples policy change, not a narrow publisher-only cleanup.

### 6. The current bug is still real: the local target shape is misleading relative to current docs

Even with the nuance above, the current publisher example is still misleading:

- comments already point readers to the current Secret discovery docs;
- but the executable sample directly below still teaches a `WebhookURL`-centric target object;
- current docs and archived `PHASE-4` spec both teach `url` / `headers` / `filter_config` / `format` as the canonical target shape.

So there is a real mergeable mismatch inside this file.

### 7. There is no direct compile gate for this example in the current repo layout

Trying `go test ./...` from `examples/custom-publisher` is not meaningful here because `examples/` is outside the only repo module (`go-app/go.mod`).

This does not block the task, but it means verification for the next slice will have to stay at:

- diff review
- contract review
- targeted search
- `git diff --check`

rather than repo-local compile/test execution.

---

## Option Assessment

### Option A — align only the local `PublishingTarget` example shape to current canonical target fields

Possible changes:

- rename `WebhookURL` to `URL`
- switch request creation to `target.URL`
- add `Format`
- add `FilterConfig`
- keep `Headers` and `Enabled`
- update the example target instance in `main()`
- optionally apply `target.Headers` in the request path

Pros:

- smallest mergeable code slice;
- fixes the actual misleading target shape without dragging in runtime imports;
- consistent with the docs that the previous task already points to.

Cons:

- file still will not become a strict implementation of `pkg/core/interfaces.AlertPublisher`;
- leaves the wider “examples are not compile-exact interface demos” issue untouched.

### Option B — fully align `custom-publisher` to `pkg/core/interfaces.AlertPublisher`

This would mean changing:

- method signatures
- alert type usage
- target type usage
- likely formatter helper assumptions around `HasClassification()` / `EffectiveSeverity()`

Pros:

- would make the example more faithful to the `pkg/core` extension-point story claimed in `examples/README.md`.

Cons:

- materially larger than the current bug framing;
- effectively opens a broader examples-contract cleanup question;
- likely wants a sibling follow-up for `custom-classifier` too.

### Option C — keep code as-is and only adjust docs wording again

Pros:

- tiny diff.

Cons:

- does not actually close the bug that the local example code shape is misleading;
- would amount to hiding the problem after the previous narrative cleanup.

---

## Recommendation

The most honest next `/spec` is **Option A**:

- keep scope to `examples/custom-publisher/main.go`;
- treat the task as **self-contained target-shape alignment**, not full interface conformance;
- align the local `PublishingTarget` example shape to the current canonical field names:
  - `url`
  - `headers`
  - `filter_config`
  - `format`
  - `enabled`
- update the sample `Publish(...)` path to use `target.URL`;
- optionally set configured headers from `target.Headers` so the example reads as a truthful HTTP publisher pattern.

Important boundary:

- do **not** import or couple to `go-app/internal/core`;
- do **not** attempt to solve the broader “examples are not strict `pkg/core/interfaces` implementations” issue here;
- do **not** reopen `custom-classifier` in this task.

This gives a mergeable slice that removes the most misleading part of the file while keeping the task narrow.

---

## Proposed `/spec` Scope

Recommended in-scope file:

- `examples/custom-publisher/main.go`

Recommended in-scope changes:

- local `PublishingTarget` field shape cleanup
- request path cleanup from `WebhookURL` to `URL`
- sample target object cleanup in `main()`
- small supporting comments inside the file if needed

Recommended out-of-scope changes:

- `examples/custom-classifier/main.go`
- `examples/k8s/*.yaml`
- `go-app/internal/**`
- `go-app/pkg/core/interfaces/**`
- examples policy rewrite about compile-exactness

---

## Verification Notes For The Next Slice

If `/spec` follows the recommendation above, the next verification path should stay lightweight:

- targeted search for `WebhookURL` / `webhook_url` in `examples/custom-publisher/main.go`
- manual review against:
  - `docs/CONFIGURATION_GUIDE.md`
  - `tasks/archive/PHASE-4-PRODUCTION-PUBLISHING-PATH/Spec.md`
  - `examples/README.md`
- manual review that the example still reads as self-contained source example
- `git diff --check`

---

## Research Outcome

`CUSTOM-PUBLISHER-EXAMPLE-CODE-SHAPE-DRIFT` is real, but the honest target is **not** “import active runtime internals” and probably **not** “solve all example-interface drift in one pass”.

The best next move is a narrow code-shape cleanup inside `examples/custom-publisher/main.go` itself:

- keep it self-contained;
- make the local target shape look like current canonical publishing target fields;
- leave broader example/interface policy questions for separate follow-up work.
