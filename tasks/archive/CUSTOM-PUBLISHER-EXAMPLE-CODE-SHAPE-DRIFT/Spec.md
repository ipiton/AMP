# CUSTOM-PUBLISHER-EXAMPLE-CODE-SHAPE-DRIFT - Spec

**Status**: Implemented  
**Date**: 2026-03-09  
**Inputs**: `requirements.md`, `research.md`  
**Chosen Direction**: `выровнять только self-contained target shape в examples/custom-publisher/main.go с current canonical publishing target fields без перехода в runtime coupling или broader examples policy rewrite`

**Related Planning**:
- `docs/06-planning/NEXT.md`
- `docs/06-planning/BUGS.md`
- `docs/CONFIGURATION_GUIDE.md`
- `docs/MIGRATION_QUICK_START.md`
- `examples/README.md`
- `go-app/pkg/core/README.md`
- `tasks/archive/PHASE-4-PRODUCTION-PUBLISHING-PATH/Spec.md`

---

## 1. Problem Statement

После narrative cleanup в `examples/custom-publisher/main.go` остался более глубокий residual: executable example по-прежнему учит local target shape, который не совпадает с current canonical publishing target contract.

Текущая local shape в файле:

- `WebhookURL`
- `Headers`
- `Enabled`

Current canonical docs truth:

- `url`
- `headers`
- `filter_config`
- `format`
- Secret discovery via `publishing-target=true` + `data.config` / `stringData.config`

Research показал, что проблема здесь не только в naming mismatch:

1. файл не совпадает с active runtime canonical target shape;
2. файл не совпадает и со strict `pkg/core/interfaces.PublishingTarget`;
3. `examples/` живут вне `go-app/`, поэтому прямой переход на `go-app/internal/core` не является честным target contract для self-contained examples.

Следовательно, следующая задача не должна пытаться “просто импортировать правильный тип” или переписать весь examples policy за один проход. Нужен узкий mergeable slice: сделать local example shape менее misleading, оставив файл self-contained.

---

## 2. Goals

1. Убрать из `examples/custom-publisher/main.go` наиболее misleading `webhook_url`-centric target shape.
2. Привести local `PublishingTarget` example shape ближе к current canonical publishing target fields.
3. Сохранить пример self-contained и легко читаемым.
4. Не превращать задачу в broader rewrite по `pkg/core/interfaces`, `internal/core` или policy для всего `examples/`.

---

## 3. Non-Goals

1. Не импортировать и не использовать `go-app/internal/core`.
2. Не переписывать `examples/custom-publisher/main.go` в strict implementation `pkg/core/interfaces.AlertPublisher`.
3. Не переоткрывать `examples/custom-classifier/main.go`.
4. Не менять `examples/README.md`, `go-app/pkg/core/README.md` или `go-app/pkg/core/interfaces/**` в этом slice.
5. Не менять runtime behavior, publishing implementation, config parser или Helm chart behavior.

---

## 4. Key Decisions

### 4.1 Active Runtime Contract Is A Shape Reference, Not A Direct Code Dependency

Canonical publishing target fields из current docs используются как **reference shape**:

- `url`
- `headers`
- `filter_config`
- `format`
- `enabled`

Но этот task сознательно **не** тянет `go-app/internal/core.PublishingTarget` в source example, потому что:

- `internal/` не является examples-facing contract;
- `examples/` лежат вне `go-app` module subtree;
- это создало бы ненужную runtime coupling.

### 4.2 This Slice Fixes The Misleading Target Shape, Not Full Interface Conformance

Research показал, что более широкий вопрос звучит так:

- should examples in `examples/` be compile-exact demos of `pkg/core/interfaces`?

Это отдельная policy/problem statement и не должно решаться здесь скрыто.

Поэтому этот spec разрешает только:

- local struct field cleanup;
- sample object cleanup;
- request path cleanup;
- small supporting comments if needed.

И запрещает:

- переписывать весь пример под exact `pkg/core/interfaces.AlertPublisher`;
- менять alert model or formatter flow;
- раскрывать broader examples-contract rewrite.

### 4.3 The Honest Mergeable Target Is “Less Misleading”, Not “Perfectly Canonical”

После этого slice пример все еще может оставаться:

- self-contained;
- pedagogical;
- not compile-exact to every current repo contract.

Это допустимо, если он больше не учит очевидно устаревшему target shape.

### 4.4 `WebhookURL` Should No Longer Be The Primary Target Field

Главная misleading часть сейчас:

- `Publish(...)` берет `target.WebhookURL`;
- local `PublishingTarget` advertises `json:"webhook_url"`;
- sample target instance uses `WebhookURL`.

Spec chooses to remove this as the primary example story.

### 4.5 Minimal Honest Alignment Includes Target Metadata That Current Docs Already Teach

Current canonical target docs already teach:

- `url`
- `headers`
- `filter_config`
- `format`
- `enabled`

So adding `Format` and `FilterConfig` to the local example shape is in scope, because this is not inventing a new contract. It is narrowing the example toward the already documented one.

---

## 5. Proposed Implementation

### 5.1 Align Local `PublishingTarget` Shape

In `examples/custom-publisher/main.go` update the local `PublishingTarget` to use fields analogous to the current canonical target shape:

- `Name`
- `Type`
- `URL`
- `Headers`
- `FilterConfig`
- `Format`
- `Enabled`

Implementation note:

- naming may stay Go-idiomatic in struct fields, but JSON tags should no longer teach `webhook_url`;
- `filter_config` and `format` should be present only if they help the example stay truthful and readable.

### 5.2 Align The HTTP Publish Path

Update the request creation path to use:

- `target.URL`

instead of:

- `target.WebhookURL`

Also allowed:

- apply configured headers from `target.Headers` onto the request if this remains simple and improves truthfulness.

### 5.3 Align The Sample Target Object In `main()`

The example target instance in `main()` should match the updated local struct shape:

- use `URL`
- keep placeholder endpoint value
- optionally include representative `Headers`
- optionally include `Format`
- optionally include a minimal `FilterConfig`

The sample should remain obviously illustrative, not production-ready.

### 5.4 Preserve Self-Contained Example Readability

The file should remain a small source example:

- no long config dumps;
- no runtime-specific imports;
- no attempt to model Secret discovery itself inside the executable flow.

If any explanatory comments are added, they should be short and directly support the updated local shape.

---

## 6. Deliverables

1. `examples/custom-publisher/main.go` no longer teaches `WebhookURL` / `webhook_url` as the primary target field shape.
2. The local target object is closer to current canonical publishing target fields.
3. The file remains self-contained and readable.
4. Broader questions about strict `pkg/core/interfaces` conformance remain out of scope and explicit.

---

## 7. Acceptance Criteria

This spec is correct if:

1. In-scope is limited to `examples/custom-publisher/main.go` plus task artifacts.
2. The chosen slice does not import or couple to `go-app/internal/core`.
3. The chosen slice does not attempt a broader `pkg/core/interfaces` conformance rewrite.
4. `WebhookURL` / `webhook_url` are no longer the primary modeled target shape in the example.
5. The updated example shape is clearly closer to current canonical docs truth.
6. Verification path stays lightweight: targeted search, manual contract review, `git diff --check`.

---

## 8. Verification Strategy

Primary verification path for the next delivery slice:

```bash
rg -n "WebhookURL|webhook_url" examples/custom-publisher/main.go
```

Manual review against:

- `docs/CONFIGURATION_GUIDE.md`
- `tasks/archive/PHASE-4-PRODUCTION-PUBLISHING-PATH/Spec.md`
- `examples/README.md`

And:

- `git diff --check`

Expected verification reading:

- if `WebhookURL` remains, it should be only in intentionally retained explanatory context, not as the main target-shape contract;
- example should still read as self-contained source code, not as generated config prose.

---

## 9. Risks And Mitigations

### 9.1 Risk: Scope Quietly Expands Into Full Interface Rewrite

Mitigation:

- keep changes to local target struct, request path, and example target object only;
- do not touch `pkg/core/interfaces` or `custom-classifier`.

### 9.2 Risk: Example Becomes Half-Canonical And More Confusing

Mitigation:

- update the struct, request usage, and sample object together as one coherent local shape;
- prefer a single internally consistent shape over partial renames.

### 9.3 Risk: We Accidentally Imply Runtime Coupling

Mitigation:

- no imports from `go-app/internal/**`;
- keep wording around the file as “self-contained example”, not “drop-in runtime contract”.

### 9.4 Risk: Broader Examples Policy Drift Remains

Mitigation:

- keep that limitation explicit;
- if strict `pkg/core/interfaces` conformance is still desired later, treat it as a separate follow-up task.
