# DOCS-HONESTY-PASS - Spec

**Status**: Implemented v1  
**Date**: 2026-03-08  
**Inputs**: `requirements.md`, `research.md`  
**Chosen Direction**: `active-runtime-first docs truth pass with chart metadata alignment`

**Related Planning**:
- `docs/06-planning/ALERTMANAGER-REPLACEMENT-GAP-ANALYSIS.md`
- `docs/06-planning/BUGS.md`
- `docs/06-planning/NEXT.md`

**Implemented Scope**:
- `README.md`
- `docs/ALERTMANAGER_COMPATIBILITY.md`
- `docs/MIGRATION_QUICK_START.md`
- `docs/MIGRATION_COMPARISON.md`
- `helm/amp/README.md`
- `helm/amp/Chart.yaml`

---

## 1. Problem Statement

После `ALERTMANAGER-REPLACEMENT-SCOPE` проект уже не должен заявлять AMP как безоговорочный replacement для Alertmanager, но documentation surface все еще делает это косвенно.

Сейчас одновременно существуют несколько конфликтующих narratives:

1. top-level docs уже говорят `controlled replacement`;
2. detailed compatibility sections продолжают помечать unsupported routes как `ACTIVE`;
3. migration/comparison docs подают неподтвержденные performance/resource/migration guarantees как current facts;
4. chart README и `Chart.yaml` продолжают обещать `drop-in replacement` и `100% Alertmanager API compatible`.

Из-за этого public story остаётся противоречивой даже после truth-alignment по planning и tests.

Этот spec фиксирует **docs honesty slice**: не менять runtime, а довести public/product/package narrative до состояния, где он не обещает больше, чем реально подтверждено кодом и planning artifacts.

---

## 2. Goals

1. Синхронизировать user-facing docs с `active-runtime-first` narrative.
2. Убрать route-level claims, которые противоречат текущему active runtime.
3. Убрать или смягчить неподтвержденные performance/resource/migration guarantees.
4. Выровнять install и license narrative между repo docs и chart/package metadata.
5. Оставить документацию полезной для controlled replacement / pilot usage.

---

## 3. Non-Goals

1. Не расширять active runtime и не добавлять новые handlers/routes.
2. Не чинить `futureparity` suite и не менять verification model beyond doc wording.
3. Не переписывать весь docs corpus репозитория, включая internal package READMEs.
4. Не доказывать benchmark claims новыми measurement runs в этом slice.
5. Не делать marketing refresh или branding rewrite за пределами honesty/consistency pass.

---

## 4. Key Decisions

### 4.1 Source of Truth

Для public replacement/compatibility story источником истины считаются:

- `go-app/cmd/server/main.go`
- `go-app/internal/application/router.go`
- planning artifacts в `docs/06-planning/`

Если любой user-facing doc противоречит этим источникам, правится doc, а не narrative вокруг него.

### 4.2 Current Allowed Positioning

Для текущего runtime допустим только такой public claim:

- **controlled replacement**

Следующие формулировки считаются недопустимыми в scope этого slice:

- `drop-in replacement`
- `100% Alertmanager API compatible`
- `production-ready replacement`
- route-level `ACTIVE` claims для ручек, которых нет в active router

### 4.3 Compatibility Matrix Policy

`docs/ALERTMANAGER_COMPATIBILITY.md` должен быть приведён к одному из двух честных состояний:

1. route/feature помечен как активный только если он есть в current runtime;
2. иначе он переводится в historical/future/backlog wording.

Для ближайшего diff наиболее безопасный путь:

- не пытаться сохранить wide matrix как будто она current;
- явно отделить `active current surface` от `historical/future parity`.

### 4.4 Benchmark / Resource Claims Policy

Точные comparative claims без reproducible benchmark source не должны подаваться как current facts.

Для этого slice принимается правило:

- либо удалить точные цифры (`10x faster`, `75% less resources`, `<5ms p95`);
- либо явно пометить их как historical/non-verified benchmark note.

Предпочтительный путь для минимального и честного diff:

- убрать точные superiority numbers из top-level/product docs.

### 4.5 Install Narrative Policy

Docs должны использовать только install paths, которые можно считать source-of-truth из самого репозитория.

Пока не доказано обратное, canonical chart reference для repo-local docs:

- local chart path `./helm/amp`
- chart name `amp`

Published Helm repo alias/path нельзя подавать как verified default, если он не подтвержден из текущего repo context.

### 4.6 License Narrative Policy

Source of truth по лицензии:

- `LICENSE`
- AGPL-related chart annotations/badges

Все top-level user-facing docs в scope этого slice должны быть согласованы с AGPL-3.0.

---

## 5. Scope Model

### 5.1 In Scope

- `README.md`
- `docs/ALERTMANAGER_COMPATIBILITY.md`
- `docs/MIGRATION_QUICK_START.md`
- `docs/MIGRATION_COMPARISON.md`
- `helm/amp/README.md`
- `helm/amp/Chart.yaml`

### 5.2 Optional If Diff Stays Small

- `CHANGELOG.md` для top-level misleading product claims
- другие user-facing repo docs с тем же license/install drift

### 5.3 Out Of Scope

- internal package READMEs
- examples/docs deep cleanup
- benchmark generation
- runtime restoration
- testing beyond targeted doc review / diff validation

---

## 6. Deliverables

### 6.1 Top-Level Docs Alignment

Нужно привести к честному narrative:

- project intro
- compatibility summaries
- quick-start / migration wording
- comparison tables and recommendation sections

### 6.2 Chart Surface Alignment

Нужно убрать overclaims из:

- `helm/amp/README.md`
- `helm/amp/Chart.yaml`

Иначе package/install surface останется в противоречии с repo docs.

### 6.3 Claim Policy By Category

По итогам diff в docs должна быть понятна простая логика:

- current active runtime: можно заявлять;
- historical/future parity: можно описывать только как backlog/target;
- benchmark/resource superiority without proof: нельзя подавать как fact;
- install/license narrative: один canonical story.

---

## 7. Acceptance Criteria

Слайс считается завершённым, если одновременно выполнены условия:

1. `README.md`, `docs/ALERTMANAGER_COMPATIBILITY.md`, `docs/MIGRATION_QUICK_START.md` и `docs/MIGRATION_COMPARISON.md` не содержат claims, противоречащих active runtime и planning.
2. `helm/amp/README.md` и `helm/amp/Chart.yaml` больше не позиционируют AMP как verified `drop-in replacement` или `100% Alertmanager API compatible`.
3. Unsupported current routes (`status`, `receivers`, `reload` и аналогичные) не подаются как active/current features.
4. Performance/resource/license/install statements либо согласованы, либо явно смягчены до honest wording.
5. Изменения проверены через targeted review и `git diff --check`.

---

## 8. Proposed Implementation Direction

### Step 1. Remove Direct Contradictions

Сначала убрать всё, что прямо противоречит active router:

- route matrices
- `all endpoints supported`
- `drop-in replacement`
- `100% compatible`

### Step 2. Reframe Product-Level Comparisons

Затем привести comparison/migration docs к wording уровня:

- pilot / controlled replacement
- phased parity
- explicit verification caveats

### Step 3. Align Install And License Story

После этого:

- привести Helm install examples к repo-verifiable path
- выровнять AGPL wording
- убрать conflicting repo/package text

### Step 4. Optional Small Cleanup

Если diff остаётся компактным:

- зачистить ещё 1-2 top-level misleading references вне core file set

---

## 9. Risks

### Risk A: Cosmetic Edits Without Fixing Detailed Matrices

Если поменять только intro paragraphs, а detailed compatibility tables оставить как есть, docs останутся внутренне противоречивыми.

### Risk B: Scope Creep Into Full Docs Rewrite

Если пытаться одновременно почистить все README в репозитории, slice расползётся и потеряет темп.

### Risk C: Unverified Install Narrative Survives In Chart Surface

Если не тронуть `helm/amp/README.md` и `helm/amp/Chart.yaml`, package metadata продолжит обещать больше, чем top-level docs.

### Risk D: Benchmarks Become Implicitly Re-Endorsed

Если оставить точные цифры без note/proof, они будут читаться как текущая verified product truth.

---

## 10. Implementation Outcome

Слайс реализован по плану:

- top-level docs и chart surface больше не позиционируют current runtime как verified full Alertmanager drop-in replacement;
- unsupported active routes переведены из current claims в backlog/future wording;
- install story приведен к repo-verifiable path `./helm/amp`;
- AGPL narrative выровнен в core public/docs scope;
- точные benchmark/resource superiority claims убраны из current public story.

Residual follow-up intentionally left outside this slice:

- internal/subpackage docs cleanup
- wider repo-wide license wording cleanup
- future parity verification hardening
