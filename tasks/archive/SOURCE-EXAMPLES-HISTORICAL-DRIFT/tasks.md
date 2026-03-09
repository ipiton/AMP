# Implementation Checklist: SOURCE-EXAMPLES-HISTORICAL-DRIFT

## Research & Spec
- [x] Выполнен `research.md` с разделением drift на classifier-side prose cleanup и publisher-side integration-contract drift.
- [x] Подготовлен `Spec.md` с узкой границей только на `examples/custom-classifier/main.go` и `examples/custom-publisher/main.go`.

## Vertical Slices
- [x] **Slice A: Classifier Narrative Cleanup** — убрать historical `Alert History Service` wording и stale integration narrative из `custom-classifier/main.go` без переписывания executable demo logic.
- [x] **Slice B: Publisher Integration Guidance Cleanup** — убрать historical wording и stale inline `publishing.targets` / `webhook_url` / `filters` story из `custom-publisher/main.go` без переписывания local demo types/flow.

## Implementation
- [x] Шаг 1: Обновить `examples/custom-classifier/main.go` так, чтобы:
  - top intro больше не ссылался на `Alert History Service`;
  - footer block получил neutral AMP/current-runtime wording;
  - integration guidance больше не навязывал неподтвержденный `classification.default_classifier` snippet как canonical public path.
- [x] Шаг 2: Проверить classifier-side wording после правок на practical usefulness: сохранить guidance про interface/bootstrap/integration, не сводя блок к пустому branding rename.
- [x] Шаг 3: Обновить `examples/custom-publisher/main.go` так, чтобы:
  - top intro и footer block больше не ссылались на `Alert History Service`;
  - inline integration guidance больше не учила obsolete `publishing.targets` / `webhook_url` / `filters` story.
- [x] Шаг 4: В `custom-publisher/main.go` заменить stale inline config tutorial на короткий current-truth pointer к canonical publishing Secret/discovery docs без переписывания local `PublishingTarget` demo struct или publish flow.
- [x] Шаг 5: После правок вручную проверить diff обоих файлов и подтвердить, что imports, structs, method signatures и executable example flow не менялись.

## Testing
- [x] Прогнать targeted search:
  - `rg -n -i "Alert History Service" examples/custom-classifier/main.go examples/custom-publisher/main.go`
  - `rg -n "^//.*(publishing:|targets:|webhook_url:|filters:)" examples/custom-publisher/main.go`
- [x] Выполнить manual review edited scope против:
  - `docs/CONFIGURATION_GUIDE.md`
  - `docs/MIGRATION_QUICK_START.md`
  - `tasks/archive/PHASE-4-PRODUCTION-PUBLISHING-PATH/Spec.md`
- [x] Выполнить narrative sanity review:
  - classifier guidance остается полезным, но не invents new config contract;
  - publisher guidance больше не противоречит canonical Secret discovery story;
  - diff не трогает executable demo logic.
- [x] Проверить `git diff --check`.

## Write Tests
- [x] Новый code-level test diff не планируется: slice ограничен source-example comments/integration guidance cleanup.
- [x] Роль `/write-tests` для этого slice выполняет explicit docs/example verification path: targeted search, manual review, narrative sanity review и diff hygiene.
- [x] Во время `/implement` не потребовались local example struct / executable flow changes, поэтому slice остается честно доказуемым без code-contract rewrite.

## Documentation & Cleanup
- [x] На `/write-doc` синхронизировать `requirements.md` и `Spec.md` под фактический result narrative/integration-only slice.
- [x] На `/write-doc` deeper `custom-publisher` example-code contract drift не маскируется как “тоже закрытый”: он вынесен в `CUSTOM-PUBLISHER-EXAMPLE-CODE-SHAPE-DRIFT`.
- [x] Planning artifacts обновлены через закрытие текущего narrative slice и явный follow-up bug только для deeper `custom-publisher` code-shape residual.

## Finalization Readiness
- [x] На `/end-task` задача снимается из WIP после green targeted search, narrative sanity review и `git diff --check`.
- [x] В `DONE.md` этот slice зафиксирован как source-example narrative/integration cleanup, а не как full alignment `examples/custom-*.go` to runtime internals.
- [x] Deeper `custom-publisher` code-shape follow-up не скрывается: он вынесен отдельно в `CUSTOM-PUBLISHER-EXAMPLE-CODE-SHAPE-DRIFT`.

## Expected End State
- [x] `custom-classifier/main.go` больше не использует historical `Alert History Service` wording и не навязывает неподтвержденный config story как canonical.
- [x] `custom-publisher/main.go` больше не использует historical wording и не учит obsolete `publishing.targets` / `webhook_url` / `filters` contract в comments.
- [x] Оба source examples остаются self-contained and readable, но их integration guidance не противоречит current docs truth.
- [x] Executable demo logic, local structs и imports остаются нетронутыми.

## Open Assumptions
- [ ] Предполагается, что classifier integration guidance можно сделать честным без явного нового config snippet.
- [ ] Предполагается, что publisher-side stale contract teaching можно убрать на comment level, не переписывая local demo code shape.
- [ ] Предполагается, что short pointers на canonical docs читаются лучше, чем новый inline config tutorial внутри этих source examples.

## Blockers / Stop Conditions
- [ ] Если честный cleanup потребует менять local example structs, method signatures или executable flow, остановиться и вернуть задачу в planning.
- [ ] Если manual review покажет, что current docs truth по classifier integration недостаточно ясен для even generic guidance, не придумывать новый contract в comments без отдельного spec decision.
- [ ] Если publisher cleanup без code changes оставляет misleading guidance сильнее, чем ожидалось, зафиксировать это как отдельный follow-up вместо scope creep.
