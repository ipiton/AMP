# Research: DOCS-HONESTY-PASS

Дата: 2026-03-08

## Context

После `ALERTMANAGER-REPLACEMENT-SCOPE` верхнеуровневый replacement narrative уже сузили до `controlled replacement`, но публичные docs и chart metadata все еще расходятся с active runtime и planning artifacts.

Задача этого research: понять, какие claims остались нечестными, что именно надо править в ближайшем docs slice, и где проходит граница между `must-fix now` и `adjacent follow-up`.

## Executive Summary

Главный вывод: **residual docs drift шире, чем просто README cleanup**.

Сейчас проблема состоит из четырех слоев:

1. top-level docs уже говорят `controlled replacement`, но detailed tables и migration sections местами продолжают описывать более широкий active runtime;
2. performance/resource/install claims остаются неподтвержденными и поданы как факты;
3. license/install narrative между файлами расходится;
4. Helm/chart metadata и chart README продолжают обещать `drop-in replacement` и `100% Alertmanager API compatible`.

### Recommendation

Для следующего `/spec` safest direction:

- трактовать задачу как **truth pass по public/product docs и package metadata**;
- source of truth оставить тем же:
  - `go-app/cmd/server/main.go`
  - `go-app/internal/application/router.go`
  - planning artifacts в `docs/06-planning/`
- в scope ближайшего slice включить не только:
  - `README.md`
  - `docs/ALERTMANAGER_COMPATIBILITY.md`
  - `docs/MIGRATION_QUICK_START.md`
  - `docs/MIGRATION_COMPARISON.md`
- но и как минимум:
  - `helm/amp/README.md`
  - `helm/amp/Chart.yaml`

Иначе противоречие останется в install/package surface даже после правок основных markdown docs.

## Findings

### 1. Top-level replacement claim уже сужен, но detailed compatibility matrix всё ещё врёт про active runtime

Верх README и migration docs уже говорят `controlled replacement`, но `docs/ALERTMANAGER_COMPATIBILITY.md` внутри всё ещё помечает как `ACTIVE` то, чего active router сейчас не монтирует:

- `GET /api/v2/status`
- `GET /api/v2/receivers`
- `POST /-/reload`

Source of truth по runtime:

- `go-app/internal/application/router.go`

Там сейчас активны только:

- `/api/v2/alerts`
- `/api/v2/silences`
- `/api/v2/silence/{id}`
- health/readiness endpoints
- `/metrics`

Вывод:

- основной residual drift сейчас не в заголовках, а в **подробной матрице и route-level claims**;
- без правки этих sections docs останутся внутренне противоречивыми.

### 2. `MIGRATION_COMPARISON.md` всё ещё написан как superiority/comparison doc с непроверенными current-state claims

В `docs/MIGRATION_COMPARISON.md` остаются сильные утверждения, которые не выглядят подтвержденными текущим verified runtime:

- `~5ms p95`, `10x faster`, `75% less resources`
- `SIGHUP (zero downtime)` как current hot reload story
- `Kubernetes HPA` как текущая scaling story
- `Built-in analytics`
- `Built-in UI`
- `100% compatible` для template/runtime areas

Проблема не только в “маркетинговом тоне”. Документ смешивает:

- реальные текущие возможности;
- historical/future features;
- и aspirational product comparison.

Вывод:

- `MIGRATION_COMPARISON.md` требует не косметического edit pass, а **явного reframe**:
  - controlled replacement / pilot comparison,
  - verified current runtime,
  - separate note for future parity/runtime restoration.

### 3. Install narrative расходится между README, migration docs и chart source of truth

Найдены как минимум такие расхождения:

- `README.md` использует:
  - `helm install alertmanager-plus-plus amp/alertmanager-plus-plus`
- `docs/MIGRATION_QUICK_START.md` использует:
  - `helm install amp amp/amp`
- `helm/amp/Chart.yaml` имеет chart name:
  - `amp`

Дополнительно:

- `docs/ALERTMANAGER_COMPATIBILITY.md` по-прежнему обещает migration path в духе `replace container (5 minutes)` и `downtime < 1 minute`;
- для current `controlled replacement` slice это звучит слишком универсально и не опирается на отдельный verified migration smoke path.

Вывод:

- install path и migration messaging нужно привести к **одному каноническому narrative**;
- time-to-migrate statements (`5 minutes`, `5-10 minutes`, `< 1 minute downtime`) лучше либо убрать, либо явно подать как optimistic example, а не гарантированный claim.

### 4. License story всё ещё расходится

Source of truth по лицензии в репозитории:

- `LICENSE` = AGPL-3.0
- badge/annotation surfaces тоже указывают AGPL

Но в публичных docs остаётся минимум один прямой конфликт:

- `docs/MIGRATION_COMPARISON.md` -> `Apache 2.0`

Research также нашёл более широкий license drift вне минимального scope:

- `CONTRIBUTING.md`
- `examples/README.md`
- `go-app/pkg/core/README.md`
- `go-app/internal/infrastructure/llm/README.md`

Вывод:

- для ближайшего docs slice обязательно надо починить **top-level public mismatch**;
- более широкий license cleanup по repo стоит либо включить в тот же diff, если он останется малым, либо зафиксировать как отдельный follow-up.

### 5. Helm/chart docs и metadata всё ещё overclaim compatibility

Остаточный drift есть не только в repo docs, но и в chart/package surfaces:

- `helm/amp/README.md` заявляет:
  - `drop-in replacement`
  - `All Alertmanager API endpoints are supported`
  - перечисляет `GET /api/v2/status` и `GET /api/v2/receivers` как supported
- `helm/amp/Chart.yaml` в description и ArtifactHub change log всё ещё говорит:
  - `100% Alertmanager API compatible`

Это важно потому, что chart metadata читается отдельно от README и может жить собственной жизнью в package registries.

Вывод:

- если `DOCS-HONESTY-PASS` не затронет `helm/amp/README.md` и `helm/amp/Chart.yaml`, public narrative останется раздвоенным.

### 6. Performance/resource claims сейчас самые слабые с точки зрения verification

Повторяющиеся claims:

- `10-20x faster`
- `sub-5ms latency`
- `75% less resources`
- comparative latency/throughput/memory tables

Я не нашёл рядом с этими утверждениями reproducible benchmark note или ссылку на актуальный benchmark report, который бы подтверждал именно эти цифры для текущего `main`.

Вывод:

- safest docs policy сейчас:
  - либо убрать точные comparative цифры;
  - либо явно пометить их как historical benchmark/example with date and methodology;
  - но не оставлять их как безусловный current claim.

### 7. `Extensible Architecture` уже выглядит приемлемо; это не главный risk area

После предыдущего truth-alignment pass README больше не говорит `plugin system`, а использует более мягкое `Extensible Architecture` и `code-level extension points`.

Это гораздо честнее текущего состояния кода и не выглядит главным blocker для текущего docs slice.

Вывод:

- приоритет нужно держать на:
  - route compatibility claims,
  - performance/resource numbers,
  - install path,
  - license consistency,
  - chart metadata.

## Proposed Scope For Next Spec

### Must Fix In This Slice

- `README.md`
- `docs/ALERTMANAGER_COMPATIBILITY.md`
- `docs/MIGRATION_QUICK_START.md`
- `docs/MIGRATION_COMPARISON.md`
- `helm/amp/README.md`
- `helm/amp/Chart.yaml`

### Likely Fix If Diff Stays Small

- `CHANGELOG.md` if top-level performance claims are duplicated there in a misleading “current product” form
- repo-level license mentions that are user-facing and easy to align

### Better As Separate Follow-Up If Scope Starts Growing

- deep README/doc cleanup inside internal package READMEs
- all historical license references in subpackages/examples
- broader product positioning rewrite beyond honesty/consistency

## Editing Strategy

Наиболее безопасная стратегия для `/spec` и последующего `/implement`:

1. сначала убрать утверждения, которые прямо противоречат active runtime;
2. затем убрать точные benchmark/resource claims без reproducible proof;
3. затем выровнять install/licensing/package metadata;
4. только после этого улучшать tone/wording.

Так дифф останется минимальным и не превратится в “full documentation rewrite”.

## Research Outcome

- Задача действительно требует отдельного docs slice.
- Главный residual drift сейчас находится в:
  - detailed compatibility claims,
  - migration/comparison superiority tables,
  - chart README/metadata,
  - license/install consistency.
- Следующий `/spec` должен явно включить `helm/amp/README.md` и `helm/amp/Chart.yaml` в scope, иначе honesty pass останется неполным.

## Verification Notes

Новые тесты не запускались: это research/documentation step.

Выводы основаны на сравнении:

- `README.md`
- `docs/ALERTMANAGER_COMPATIBILITY.md`
- `docs/MIGRATION_QUICK_START.md`
- `docs/MIGRATION_COMPARISON.md`
- `docs/CONFIGURATION_GUIDE.md`
- `helm/amp/README.md`
- `helm/amp/Chart.yaml`
- `go-app/internal/application/router.go`
- `LICENSE`
- `docs/06-planning/BUGS.md`
- `docs/06-planning/ALERTMANAGER-REPLACEMENT-GAP-ANALYSIS.md`
