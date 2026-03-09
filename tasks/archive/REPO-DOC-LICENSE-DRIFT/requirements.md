# Requirements: REPO-DOC-LICENSE-DRIFT

## Context
После `DOCS-HONESTY-PASS` top-level public/docs surface уже выровнен, но в репозитории остаются internal/subpackage docs с историческими claims вроде `Apache 2.0` и `Production-Ready`. Это создает residual drift между активным planning truth, top-level public docs и локальными README/internal docs в подпакетах.

Текущий известный scope из `docs/06-planning/BUGS.md`:

- `CONTRIBUTING.md`
- `examples/README.md`
- `go-app/pkg/core/README.md`
- `go-app/internal/infrastructure/llm/README.md`

## Goals
- [x] Убрать или скорректировать residual claims про license, production readiness и аналогичные overclaims в указанном doc scope.
- [x] Сохранить top-level honesty contract, зафиксированный после `DOCS-HONESTY-PASS`.
- [x] Держать задачу в рамках docs cleanup, без расширения в runtime/API changes.

## Constraints
- Изменения должны оставаться в документации и planning artifacts; код и runtime behavior трогать нельзя, если на это не укажет отдельный follow-up.
- Нужно опираться на уже зафиксированный source of truth в `README.md`, `docs/06-planning/DECISIONS.md` и `docs/06-planning/BUGS.md`.
- Если в ходе работы найдутся дополнительные drift-маркеры, их нужно явно классифицировать: либо закрыть в этом же narrow slice, либо оставить как follow-up, а не silently расширять scope.

## Success Criteria (Definition of Done)
- [x] Указанные docs больше не конфликтуют с текущим license/runtime/public-claims truth.
- [x] Изменения зафиксированы в task artifacts и, если bug будет закрыт, в planning files.
- [x] Targeted verification path для doc scope определен и выполняем.

## Outcome
- `CONTRIBUTING.md` выровнен по `AGPL-3.0`.
- `examples/README.md` сокращен до honest examples index без stale branding, broken links и старого license narrative.
- `go-app/pkg/core/README.md` переписан в factual directory-level guide по реальным `domain/` и `interfaces/`.
- `go-app/internal/infrastructure/llm/README.md` сужен до verified internal BYOK contract по `proxy` и `openai/openai-compatible`.
- В ходе closure найден более широкий residual repo-doc drift за пределами этих четырех файлов; он не маскируется и выносится в planning отдельным follow-up.
