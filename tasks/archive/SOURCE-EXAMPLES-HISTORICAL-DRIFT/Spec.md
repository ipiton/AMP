# SOURCE-EXAMPLES-HISTORICAL-DRIFT - Spec

**Status**: Implemented  
**Date**: 2026-03-09  
**Inputs**: `requirements.md`, `research.md`  
**Chosen Direction**: `очистить только source-example narrative/integration drift в examples/custom-*.go без переписывания executable demo logic`
**Result**: `implemented as narrative/integration cleanup; deeper custom-publisher code-shape drift moved to CUSTOM-PUBLISHER-EXAMPLE-CODE-SHAPE-DRIFT`

**Related Planning**:
- `docs/06-planning/NEXT.md`
- `docs/06-planning/BUGS.md`
- `docs/06-planning/DECISIONS.md`
- `docs/CONFIGURATION_GUIDE.md`
- `docs/MIGRATION_QUICK_START.md`
- `tasks/archive/PHASE-4-PRODUCTION-PUBLISHING-PATH/Spec.md`

---

## 1. Problem Statement

После закрытия Kubernetes examples slice в `examples/custom-*.go` остается отдельный residual drift:

- `examples/custom-classifier/main.go`
- `examples/custom-publisher/main.go`

Research показал, что эти два файла похожи только частично.

### 1.1 `custom-classifier/main.go`

Здесь drift в основном narrative:

- top intro все еще говорит `How to integrate with Alert History Service`;
- footer block все еще называется `Integration with Alert History Service`;
- integration snippet все еще говорит `Configure Alert History to use your classifier`;
- inline `classification.default_classifier` snippet не подтвержден текущими public source-of-truth docs как stable user-facing contract для этого examples pass.

### 1.2 `custom-publisher/main.go`

Здесь drift шире:

- тот же historical `Alert History Service` wording;
- stale inline integration story через:

```yaml
publishing:
  targets:
    - name: ...
      type: ...
      webhook_url: ...
      filters:
        ...
```

Это больше не совпадает с текущим operator-facing contract, где:

- runtime toggles живут в `publishing.*`;
- actual publishing targets canonical-но описываются через Secret discovery:
  - `publishing-target=true`
  - `data.config` / `stringData.config`

При этом сам файл еще и содержит локальный self-contained demo shape (`PublishingTarget` с `webhook_url`), поэтому слишком агрессивная “alignment” правка быстро превратится из docs cleanup в example-code rewrite.

---

## 2. Goals

1. Убрать historical `Alert History Service` wording из обоих source examples.
2. Привести integration guidance в обоих файлах к current repo truth.
3. В `custom-publisher/main.go` перестать учить obsolete target-definition story через `publishing.targets` / `webhook_url` / `filters`.
4. Сохранить slice mergeable и docs-oriented: без переписывания demo code shape и без скрытого перехода в runtime work.

---

## 3. Non-Goals

1. Не менять executable flow, imports, structs или method signatures в `examples/custom-classifier/main.go`.
2. Не менять executable flow, local `PublishingTarget` demo struct или publish logic в `examples/custom-publisher/main.go`.
3. Не переоткрывать `examples/k8s/*.yaml`.
4. Не менять runtime behavior, publishing implementation, Helm templates или active config parser.
5. Не изобретать новый public contract там, где текущие docs его явно не закрепляют.

---

## 4. Key Decisions

### 4.1 Slice Covers Only Two Source Examples

In scope:

- `examples/custom-classifier/main.go`
- `examples/custom-publisher/main.go`

Out of scope:

- `examples/k8s/*.yaml`
- `helm/**`
- `go-app/**`
- broader repo docs cleanup

Это сознательно удерживает task в examples domain и не смешивает его с runtime или chart truth.

### 4.2 This Is Narrative/Integration Cleanup, Not Full Example-Code Alignment

Research показал, что особенно в `custom-publisher/main.go` есть соблазн “дотянуть” local code shape до current `PublishingTarget` contract. Этот slice этого не делает.

Разрешено:

- переписывать top comments;
- переписывать или сокращать footer/integration sections;
- удалять или заменять stale inline guidance;
- добавлять короткие pointers на canonical docs.

Не разрешено:

- переписывать local example types под current runtime contract;
- менять demo execution path;
- синхронизировать пример кода с active runtime implementation ценой code diff.

### 4.3 `custom-classifier` Should Stop Claiming Historical Integration Story

Для `custom-classifier/main.go` выбран самый узкий честный path:

- убрать `Alert History Service` wording;
- переименовать footer block в neutral AMP/current-runtime wording;
- не учить неподтвержденному historical config story как canonical;
- если нужно оставить integration guidance, сделать его general-purpose: implement interface, register in your bootstrap, verify classification path in your deployment.

### 4.4 `custom-publisher` Should Stop Teaching Obsolete Target Definition

Для `custom-publisher/main.go` ключевое решение:

- не переписывать local `PublishingTarget` demo type;
- но и не оставлять comment-level guidance, которая учит устаревшему `publishing.targets` contract;
- footer/integration block должен либо:
  - ссылаться на current publishing Secret contract; либо
  - кратко объяснять, что runtime toggles живут в `publishing.*`, а target definitions идут через discovered Secrets.

То есть source example может оставаться pedagogical, но comments around it не должны противоречить current docs.

### 4.5 Prefer Pointers Over Unverified New Config Snippets

Поскольку classifier-side public config snippet сейчас не подтвержден current docs, spec запрещает придумывать новый “канонический” config example внутри `custom-classifier/main.go`.

Если для clarity нужен guidance block, он должен быть:

- generic;
- short;
- consistent with current repo truth;
- без нового invented YAML contract.

---

## 5. Proposed Implementation

### 5.1 `examples/custom-classifier/main.go`

Обновить:

- top intro bullets;
- footer heading;
- footer text around registration/integration.

Target state:

- no `Alert History Service`;
- no stale “Configure Alert History...” wording;
- no hard claim that `classification.default_classifier` is the canonical public setup path unless repo docs explicitly say so.

### 5.2 `examples/custom-publisher/main.go`

Обновить:

- top intro bullets;
- footer heading;
- stale inline `publishing.targets` example.

Target state:

- no `Alert History Service`;
- no obsolete `publishing.targets` / `webhook_url` / `filters` integration teaching in comments;
- comments point readers toward the canonical publishing discovery story already documented elsewhere.

### 5.3 Preserve Demo Readability

Оба файла должны остаться self-contained examples:

- комментарии должны помогать читать demo;
- не нужно “засорять” их длинными docs dumps;
- pointers to canonical docs должны быть короткими и purposeful.

---

## 6. Deliverables

1. `examples/custom-classifier/main.go` очищен от historical integration wording.
2. `examples/custom-publisher/main.go` очищен от historical wording и stale inline target-definition guidance.
3. Spec остается честно narrow: executable demo logic не переписан.
4. Если deeper example-code alignment понадобится позже, это останется отдельным follow-up, а не скрытым scope creep.

Implementation result:

- `custom-classifier/main.go` now uses neutral AMP classification wording and generic integration guidance instead of historical product naming.
- `custom-publisher/main.go` now points to current publishing discovery docs instead of teaching obsolete inline `publishing.targets` config story.
- local executable `custom-publisher` demo shape intentionally remains unchanged and is tracked separately in `CUSTOM-PUBLISHER-EXAMPLE-CODE-SHAPE-DRIFT`.

---

## 7. Acceptance Criteria

Spec считается корректной, если:

1. In-scope ограничен двумя `examples/custom-*.go` файлами.
2. Выбранный slice явно позиционируется как narrative/integration cleanup, а не runtime/code-contract rewrite.
3. `custom-classifier` больше не навязывает historical `Alert History Service` story.
4. `custom-publisher` больше не учит obsolete `publishing.targets` / `webhook_url` / `filters` story как current contract.
5. Verification path остается lightweight: targeted search, manual review против current docs, `git diff --check`.

---

## 8. Verification Strategy

Primary verification path для следующего delivery pass:

```bash
rg -n -i "Alert History Service" \
  examples/custom-classifier/main.go \
  examples/custom-publisher/main.go

rg -n "^//.*(publishing:|targets:|webhook_url:|filters:)" \
  examples/custom-publisher/main.go
```

Manual review against:

- `docs/CONFIGURATION_GUIDE.md`
- `docs/MIGRATION_QUICK_START.md`
- `tasks/archive/PHASE-4-PRODUCTION-PUBLISHING-PATH/Spec.md`

And:

- `git diff --check`

---

## 9. Risks And Mitigations

### 9.1 Risk: Quiet Scope Expansion Into Example Code Rewrite

Mitigation:

- explicitly keep local structs and executable demo flow out of scope;
- treat comment/integration cleanup as the entire slice.

### 9.2 Risk: Publisher Comments Stay Ambiguous After Cleanup

Mitigation:

- prefer a short pointer to canonical publishing Secret docs over inventing a new inline config tutorial;
- keep wording specific enough to stop teaching the old contract.

### 9.3 Risk: Classifier Guidance Becomes Too Vague

Mitigation:

- keep practical steps like “implement interface”, “register in bootstrap”, “validate in your deployment”;
- avoid only the unverified config snippet, not useful integration guidance altogether.
