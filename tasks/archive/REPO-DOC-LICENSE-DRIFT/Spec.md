# REPO-DOC-LICENSE-DRIFT - Spec

**Status**: Implemented v1  
**Date**: 2026-03-09  
**Inputs**: `requirements.md`, `research.md`  
**Chosen Direction**: `honest stale-contract cleanup inside four pre-scoped docs without repo-wide doc sweep`

**Related Planning**:
- `docs/06-planning/NEXT.md`
- `docs/06-planning/BUGS.md`
- `docs/06-planning/DECISIONS.md`

**Implemented Result**:
- `CONTRIBUTING.md` теперь ссылается на repository `AGPL-3.0` contribution license clause;
- `examples/README.md` заменен на краткий honest index по текущим example paths без historical product claims;
- `go-app/pkg/core/README.md` переписан в factual guide по реальным `domain/` и `interfaces/` subpackages;
- `go-app/internal/infrastructure/llm/README.md` сужен до verified internal contract без readiness/benchmark/provider overclaims;
- более широкий repo-wide historical doc drift найден, но оставлен вне этого slice и вынесен в planning отдельным follow-up.

---

## 1. Problem Statement

После `DOCS-HONESTY-PASS` top-level public/docs surface уже приведен к active-runtime-first narrative, но в репозитории остались четыре internal/subpackage docs, которые продолжают держать старый contract.

Проблема шире, чем две конкретные формулировки `Apache 2.0` и `Production-Ready`:

1. часть файлов использует historical branding `Alert History`;
2. часть README ссылается на несуществующие package paths, каталоги и docs;
3. часть claims обещает compatibility/stability/performance, которые не подтверждены current repo truth;
4. contribution/license wording в одном из файлов прямо конфликтует с `LICENSE`.

Если исправить только отдельные строки про license/status, misleading contract останется в тех же документах.

Цель этого spec: закрыть residual doc drift узким docs-only slice, не превращая задачу в repo-wide documentation migration.

---

## 2. Goals

1. Синхронизировать четыре документа из bug entry с текущим repo truth.
2. Убрать misleading claims про license, production readiness, compatibility, branding и package structure там, где они конфликтуют с текущим состоянием.
3. Сохранить задачу narrow: только docs cleanup, без runtime/API/code changes.
4. Оставить в этих документах полезный, но честный narrative вместо placeholder marketing claims.

---

## 3. Non-Goals

1. Не делать repo-wide cleanup всех historical `Alert History` или `Apache 2.0` references.
2. Не менять top-level `README.md`, runtime code, tests или публичные API contracts.
3. Не восстанавливать historical parity claims через код или новые verification runs.
4. Не переписывать examples или package docs в полноценные manuals, если достаточно factual short-form descriptions.
5. Не закрывать автоматически другие doc drifts, найденные вне четырех целевых файлов.

---

## 4. Key Decisions

### 4.1 Source of Truth

Для этого slice источником истины считаются:

- `LICENSE` для license narrative;
- top-level `README.md` для current public positioning;
- `docs/06-planning/DECISIONS.md`, особенно `ADR-002`, для active-runtime-first claim policy;
- фактическая структура каталогов и symbols в `go-app`.

Если локальный README конфликтует с этими источниками, правится README, а не truth model.

### 4.2 Scope Boundary Stays At Four Documents

Canonical in-scope set:

- `CONTRIBUTING.md`
- `examples/README.md`
- `go-app/pkg/core/README.md`
- `go-app/internal/infrastructure/llm/README.md`

Дополнительные drift-маркеры, найденные поиском вне этих файлов, не чинятся автоматически в этом slice. Их можно только явно упомянуть как residual follow-up, если это понадобится на `/write-doc`.

### 4.3 Cleanup Is Contract-Level, Not String-Level

Этот slice не ограничивается заменой отдельных слов.

Разрешается править соседние claims внутри тех же четырех документов, если они:

- конфликтуют с current license/runtime truth;
- ведут на несуществующие пути;
- описывают несуществующие package/layout contracts;
- обещают compatibility/performance/stability без подтверждения.

Это не считается hidden scope expansion, пока изменение остается внутри уже зафиксированных четырех файлов.

### 4.4 `CONTRIBUTING.md` Gets Narrow Correction

Для `CONTRIBUTING.md` целевой объем минимальный:

- исправить contribution license clause под `AGPL-3.0`;
- не переписывать весь contribution workflow, если он не конфликтует с текущим repo contract.

### 4.5 `examples/README.md` Becomes Honest Examples Index

`examples/README.md` должен остаться коротким guide по extension examples, но без stale repo story.

Допустимые изменения:

- убрать `Alert History` branding;
- выровнять support/resource links с текущим repo;
- убрать `Apache 2.0`;
- исправить broken links;
- оставить только те extension points и usage notes, которые не противоречат текущей структуре examples.

Недопустимо:

- описывать examples как доказательство broader production/runtime claims.

### 4.6 `go-app/pkg/core/README.md` Gets Honest Directory-Level Rewrite

Это самый drifted файл, поэтому здесь допускается narrow rewrite вместо точечных правок.

Целевой contract:

- файл описывает `pkg/core` как directory-level source for domain/interfaces contracts;
- использует только реальные пути и реальные подпакеты;
- не обещает `Production-Ready`, `100% API compatibility`, stable semver guarantees, coverage targets, nonexistent `pkg/core/services`, nonexistent external docs links или placeholder import paths;
- если корневой `pkg/core` не является самостоятельным importable package, README не должен делать вид, что это так.

### 4.7 `go-app/internal/infrastructure/llm/README.md` Narrows To Verified LLM Contract

LLM README должен отражать только то, что подтверждается текущим пакетом.

Безопасный contract для этого slice:

- BYOK narrative допустим;
- `proxy` и `openai/openai-compatible` paths допустимы, потому что они подтверждаются кодом;
- unverified provider-specific promises, hard benchmark numbers и `Production-Ready` wording должны уйти;
- license wording должен быть согласован с repo `LICENSE`.

Если часть текущих примеров про Anthropic/Azure нельзя честно удержать как verified package contract, они должны быть либо удалены, либо явно переформулированы в non-claiming guidance.

### 4.8 English Docs Stay English

Несмотря на то, что planning artifacts ведутся на русском, сами user-facing/internal README в scope этой задачи должны остаться на английском, чтобы не ломать текущий language contract repo docs.

---

## 5. Scope Model

### 5.1 In Scope

- factual cleanup в четырех документах из bug entry;
- correction stale branding, links, package paths и claim wording внутри этих файлов;
- task artifacts, отражающие итоговый narrow contract.

### 5.2 Out Of Scope

- любые runtime/code changes;
- любые test changes;
- top-level README honesty pass;
- repo-wide doc/license sweep;
- cleanup других README за пределами четырех целевых файлов.

---

## 6. Deliverables

### 6.1 `CONTRIBUTING.md`

Файл больше не конфликтует с `LICENSE` и не утверждает `Apache 2.0` для contributions.

### 6.2 `examples/README.md`

Файл остается examples overview, но без stale branding, broken links и старого license narrative.

### 6.3 `go-app/pkg/core/README.md`

Файл переписан в factual package-tree guide, который не обещает больше, чем реально существует в `pkg/core`.

### 6.4 `go-app/internal/infrastructure/llm/README.md`

Файл описывает узкий verified LLM client contract без overclaims про readiness, benchmarks и неподтвержденный provider matrix.

---

## 7. Acceptance Criteria

Слайс считается завершенным, если одновременно выполнены условия:

1. Все четыре документа больше не содержат misleading license/status claims, противоречащих `LICENSE` и active-runtime-first docs truth.
2. `examples/README.md` и `go-app/pkg/core/README.md` не используют stale branding/link/package-path contract, который уже не соответствует репозиторию.
3. `go-app/pkg/core/README.md` не обещает nonexistent structure или stronger compatibility/stability guarantees, чем подтверждает current repo state.
4. `go-app/internal/infrastructure/llm/README.md` не обещает wider provider/readiness/performance contract, чем подтверждает текущий код.
5. Verification path из task artifacts выполняем:
   - targeted `rg` по drift markers;
   - manual review against `LICENSE`, `README.md`, `DECISIONS.md`;
   - `git diff --check`.

---

## 8. Risks And Mitigations

### 8.1 Risk: Over-editing Docs Into New Product Narrative

Если переписать файлы слишком широко, задача расползется в новый docs pass.

Митигация:
- ограничиться четырьмя файлами;
- править только claims, structure и links, нужные для honest local contract.

### 8.2 Risk: Leaving Nearby Misleading Claims In The Same File

Если чинить только `Apache 2.0` и `Production-Ready`, bug формально сдвинется, но по сути останется.

Митигация:
- разрешить contract-level cleanup внутри тех же файлов;
- особенно для `pkg/core/README.md` и `llm/README.md`.

### 8.3 Risk: Replacing False Claims With New Unverified Claims

Есть риск случайно написать более аккуратную, но все еще недоказанную формулировку.

Митигация:
- использовать только claims, которые можно быстро проверить по repo structure и коду;
- в сомнительных местах предпочитать narrower wording.

### 8.4 Risk: Residual Drift Elsewhere In Repo

Поиск уже показывает, что похожие historical markers есть и в других местах repo.

Митигация:
- не скрывать это;
- оставить такие случаи вне scope текущего slice, если они не мешают целевым четырем документам.

---

## 9. Verification Strategy

Минимальный verification path для этого spec:

```bash
rg -n "Apache 2\\.0|Production-Ready|Alert History|yourusername|100% API v2 compatibility|docs/adrs|ARCHITECTURE.md" \
  CONTRIBUTING.md \
  examples/README.md \
  go-app/pkg/core/README.md \
  go-app/internal/infrastructure/llm/README.md

git diff --check
```

Плюс manual review против:

- `LICENSE`
- `README.md`
- `docs/06-planning/DECISIONS.md`
