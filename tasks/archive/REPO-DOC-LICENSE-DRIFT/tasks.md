# Implementation Checklist: REPO-DOC-LICENSE-DRIFT

## Research & Spec
- [x] Завершен `research.md` по residual license/status drift, stale branding, broken links и package-contract mismatch в четырех целевых документах.
- [x] Подготовлен `Spec.md` с узкой docs-only границей, source-of-truth policy и acceptance criteria.

## Vertical Slices
- [x] **Slice A: Narrow Repo-Truth Cleanup For `CONTRIBUTING.md` + `examples/README.md`** — исправить contribution license clause, убрать stale branding/license drift и broken links в examples index без расширения в broader docs pass.
- [x] **Slice B: Honest README Rewrite For `pkg/core` + `llm`** — переписать два наиболее drifted package README до verified local contract, затем закрыть targeted verification и planning/doc sync.

## Implementation
- [x] Шаг 1: Исправить `CONTRIBUTING.md` так, чтобы contribution clause больше не конфликтовал с `LICENSE`, не переписывая весь contribution flow.
- [x] Шаг 2: Привести `examples/README.md` к honest examples index:
  - убрать `Alert History` branding;
  - выровнять support/resource links;
  - убрать `Apache 2.0`;
  - убрать или поправить broken references вроде `../docs/adrs/`.
- [x] Шаг 3: Переписать `go-app/pkg/core/README.md` в factual directory-level guide:
  - только реальные подпакеты и реальные import paths;
  - без `Production-Ready`, `100% API v2 compatibility`, stable semver и coverage promises;
  - без ссылок на несуществующие `pkg/core/services` и `../../docs/ARCHITECTURE.md`.
- [x] Шаг 4: Привести `go-app/internal/infrastructure/llm/README.md` к узкому verified contract:
  - сохранить честный BYOK narrative;
  - опираться только на подтвержденные `proxy` и `openai/openai-compatible` paths;
  - убрать `Production-Ready`, `Apache 2.0`, benchmark overclaims и лишний provider marketing.
- [x] Шаг 5: Если при редактировании найдутся дополнительные drift markers вне этих четырех файлов, не чинить их автоматически; при необходимости зафиксировать как residual follow-up на этапе `/write-doc`.

## Testing
- [x] Прогнать targeted search по drift markers в scope этой задачи:
  - `Apache 2.0`
  - `Production-Ready`
  - `Alert History`
  - `yourusername`
  - `100% API v2 compatibility`
  - `docs/adrs`
  - `ARCHITECTURE.md`
- [x] Выполнить manual review edited files против:
  - `LICENSE`
  - top-level `README.md`
  - `docs/06-planning/DECISIONS.md`
- [x] Проверить `git diff --check`.
- [x] Не подменять этот docs slice runtime gates: `go test`, `go build` и `make quality-gates` не являются обязательным acceptance path, если код не меняется.

Примечание:
- Для этого шага не добавлялись новые code-level tests, потому что slice ограничен документацией и не меняет runtime behavior. Роль `/write-tests` здесь выполняет зафиксированный docs verification path из spec.

## Documentation & Cleanup
- [x] На `/write-doc` синхронизировать `requirements.md`, если фактический cleanup окажется уже текущего spec.
- [x] На `/write-doc` синхронизировать `Spec.md`, если `pkg/core` или `llm` README потребуют более узкого финального contract, чем предполагалось.
- [x] Если bug будет фактически закрыт, обновить `docs/06-planning/BUGS.md` и связанные planning artifacts без скрытого закрытия residual drift вне этого scope.

## Expected End State
- [x] `CONTRIBUTING.md` больше не конфликтует с repo license.
- [x] `examples/README.md` больше не держит stale branding, broken links и старый license narrative.
- [x] `go-app/pkg/core/README.md` описывает только существующую структуру и не обещает неподтвержденную compatibility/stability.
- [x] `go-app/internal/infrastructure/llm/README.md` отражает узкий verified LLM contract без overclaims.
- [x] Targeted verification path для doc scope зеленый и задокументирован.

## Testing Result
- [x] Drift-marker scan по целевым четырем файлам чистый.
- [x] Локальные markdown-ссылки, добавленные в edited scope, указывают на существующие repo paths.
- [x] `git diff --check` проходит.
- [x] Runtime/code gates сознательно не запускались повторно, потому что slice не меняет код и не расширяет runtime behavior.

## Open Assumptions
- [ ] Предполагается, что `examples/README.md` можно починить точечно, не переписывая сами example source files.
- [ ] Предполагается, что для `pkg/core` достаточно directory-level README без попытки восстановить historical package/service narrative.
- [ ] Предполагается, что `llm` README можно сузить до code-backed contract без внесения runtime изменений в сам LLM package.

## Blockers / Stop Conditions
- [ ] Если честный rewrite `go-app/pkg/core/README.md` потребует менять фактическую package structure или код, остановиться и не расширять scope.
- [ ] Если `llm` README невозможно привести к verified contract без спорных интерпретаций по provider support, предпочесть более узкое wording вместо новых claims.
- [ ] Если по ходу cleanup всплывет значимый repo-wide doc drift за пределами четырех файлов, не превращать задачу в общий sweep; оставить explicit follow-up.

## Write-Doc Result
- [x] Task artifacts синхронизированы с фактическим scope и verification path.
- [x] `REPO-DOC-LICENSE-DRIFT` закрывается как four-file cleanup slice.
- [x] Более широкий historical repo-doc drift не скрыт и вынесен в planning как отдельный follow-up.
