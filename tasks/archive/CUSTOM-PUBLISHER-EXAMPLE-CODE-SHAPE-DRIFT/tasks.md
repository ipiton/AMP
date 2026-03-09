# Implementation Checklist: CUSTOM-PUBLISHER-EXAMPLE-CODE-SHAPE-DRIFT

## Research & Spec
- [x] Выполнен `research.md` с подтверждением, что `examples/custom-publisher/main.go` живет на локальном третьем target shape и не должен скрыто тянуть `go-app/internal/core`.
- [x] Подготовлен `Spec.md` с узкой границей только на self-contained code-shape alignment внутри `examples/custom-publisher/main.go`.

## Vertical Slices
- [x] **Slice A: Local Target Shape Alignment** — привести local `PublishingTarget` и sample target object в `examples/custom-publisher/main.go` к current canonical field names (`url`, `headers`, `filter_config`, `format`, `enabled`) без broader interface rewrite.
- [x] **Slice B: Publish Path Truthfulness** — перевести request path на `target.URL` и, если diff остается простым и полезным, честно применить `target.Headers` без изменения demo flow за пределами publisher request setup.

## Implementation
- [x] Шаг 1: Обновить local `PublishingTarget` в `examples/custom-publisher/main.go` так, чтобы primary shape больше не была `WebhookURL` / `json:"webhook_url"`.
- [x] Шаг 2: Добавить в local target shape только те поля, которые уже являются current docs truth и помогают сделать пример менее misleading:
  - `URL`
  - `Headers`
  - `FilterConfig`
  - `Format`
  - `Enabled`
- [x] Шаг 3: Обновить `Publish(...)`, чтобы HTTP request создавался через `target.URL` вместо `target.WebhookURL`.
- [x] Шаг 4: Если это остается маленьким и самоочевидным diff, проставить `target.Headers` в request path; не добавлять retry/config abstractions и не переписывать surrounding flow.
- [x] Шаг 5: Обновить sample target object в `main()` так, чтобы он отражал новую local shape и оставался явно illustrative example, а не production-ready config dump.
- [x] Шаг 6: После правок вручную проверить, что imports, formatter flow, alert model usage и surrounding demo logic не изменились за пределами target shape/request setup.

## Testing
- [x] Прогнать targeted search:
  - `rg -n "WebhookURL|webhook_url" examples/custom-publisher/main.go`
  - `rg -n "FilterConfig|Format|URL|Headers" examples/custom-publisher/main.go`
- [x] Выполнить manual contract review edited scope против:
  - `docs/CONFIGURATION_GUIDE.md`
  - `tasks/archive/PHASE-4-PRODUCTION-PUBLISHING-PATH/Spec.md`
  - `examples/README.md`
- [x] Выполнить example sanity review:
  - file остается self-contained source example;
  - local target shape больше не противоречит current canonical publishing target truth;
  - diff не превращается в broader `pkg/core/interfaces` rewrite.
- [x] Проверить `git diff --check`.

## Write Tests
- [x] Новый code-level test diff не планируется: `examples/` лежат вне `go-app` module root, и для этого slice нет честного repo-local compile gate.
- [x] Роль `/write-tests` для этого slice выполняет explicit example verification path: targeted search, manual contract review, example sanity review и diff hygiene.
- [x] Во время `/implement` compile-validated rewrite не понадобился: narrow local shape alignment остается честно доказуемым без ложного test harness.

## Documentation & Cleanup
- [x] На `/write-doc` синхронизировать `requirements.md` и `Spec.md` под фактический result self-contained code-shape slice.
- [x] На `/write-doc` не маскировать более широкий вопрос strict `pkg/core/interfaces` conformance examples как “тоже закрытый”, если он останется вне scope.
- [x] Planning artifacts обновлять только если implementation реально меняет truth о residual drift; не плодить новый follow-up без подтвержденного остатка.

## Finalization Readiness
- [x] На `/end-task` задача снимается из WIP только после green targeted search, manual contract review и `git diff --check`.
- [x] В `DONE.md` этот slice фиксируется как `custom-publisher` local code-shape alignment, а не как full examples-contract rewrite.
- [x] После implementation новый residual по broader examples policy не подтвержден как отдельный planning bug и не маскируется в формулировках done-state.

## Expected End State
- [x] `examples/custom-publisher/main.go` больше не учит `WebhookURL` / `webhook_url` как primary target shape.
- [x] Local `PublishingTarget` и sample target object ближе к current canonical publishing target fields.
- [x] Request path использует `target.URL`, а headers при необходимости применяются прозрачно и без лишней сложности.
- [x] Файл остается self-contained and readable; runtime coupling, `internal/core` imports и broader interface rewrite не появляются.

## Open Assumptions
- [ ] Предполагается, что для honest example alignment достаточно local shape cleanup, request URL cleanup и, возможно, header application.
- [ ] Предполагается, что добавление `FilterConfig` и `Format` в local example shape улучшает truthfulness и не делает пример визуально перегруженным.
- [ ] Предполагается, что отсутствие direct compile gate для `examples/` допустимо, если verification path остается explicit и узким.

## Blockers / Stop Conditions
- [ ] Если честный fix потребует менять `pkg/core/interfaces/**`, `examples/custom-classifier/main.go` или runtime code, остановиться и вернуть задачу в planning.
- [ ] Если local shape alignment тянет значительный formatter/alert-model rewrite, не продолжать под видом narrow slice.
- [ ] Если manual review покажет, что current canonical publishing truth уже расходится между docs и archived `PHASE-4` spec, сначала зафиксировать mismatch, а не изобретать новую shape локально в example.
