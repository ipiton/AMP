# Research: SECONDARY-REPO-DOC-HISTORICAL-DRIFT

## Контекст
После закрытия `REPO-DOC-LICENSE-DRIFT` top-level public/docs truth уже приведен к honest state, но `docs/06-planning/BUGS.md` все еще фиксирует residual historical markers в secondary repo docs/comments/assets. Текущий bug шире одного mergeable slice, поэтому задача этого research — не “переписать весь хвост”, а выбрать следующий узкий и честный sub-scope для `/spec`.

## Source of Truth
- `README.md`:
  - license = `AGPL-3.0`;
  - текущий допустимый claim = `controlled replacement`;
  - source of truth для runtime surface = active runtime, а не historical docs/tests.
- `docs/06-planning/DECISIONS.md`:
  - `ADR-002`: replacement story = active-runtime-first;
  - `ADR-006`: восстановленные operational endpoints не возвращают repo к broad parity claim.
- `docs/06-planning/BUGS.md`:
  - текущий bug описан как residual cleanup по secondary docs/comments/assets;
  - runtime/API changes сюда не входят.

## Marker Inventory

Первый marker-pass по заявленному scope дал `36+` совпадающих файлов и как минимум `4` разных кластера:

- `go-app/internal/**` — `25` файлов
- `helm/amp/**` — `9` файлов
- `examples/**` — `3` файла
- `grafana/**` — `1` файл

Но это не означает, что все они должны лечь в один slice. Внутри inventory смешаны:

1. настоящие docs/comments/assets drift-маркеры;
2. уже честные упоминания, которые просто содержат keyword match;
3. historical artifacts, которые не обязаны быть переписаны немедленно;
4. runtime/test strings, которые нельзя молча включать в docs-only cleanup.

## Findings

### 1. Helm operator-facing cluster — самый чистый следующий slice

Наиболее связный и наименее спорный drift сейчас лежит в `helm/amp/**`:

- `helm/amp/DEPLOYMENT.md`
  - полностью держит старый narrative `Alert History Service`;
  - использует stale install paths `./helm/alert-history`;
  - использует old release/service names `alert-history-*`;
  - рекламирует устаревший deployment story с `/dashboard`, `/webhook/proxy` и hardcoded LLM proxy URL.
- `helm/amp/values-dev.yaml`
  - comment header все еще говорит `Development values for Alert History Service`.
- `helm/amp/values-production.yaml`
  - comment header все еще говорит `Production values for Alert History Service`.
- `helm/amp/values.yaml`
  - comment block все еще содержит `Production-Ready` marker (`TN-99: Production-Ready`).
- `helm/amp/templates/postgresql-networkpolicy.yaml`
  - comments все еще говорят `Alert History application pods`.
- `helm/amp/templates/postgresql-configmap.yaml`
  - comments все еще держат `Alert History Standard Profile` и `Production-Ready Setup`.
- `helm/amp/templates/postgresql-statefulset.yaml`
  - annotation description все еще говорит `PostgreSQL StatefulSet for Alert History - Production Ready`.

Почему это хороший sub-scope:

- это operator-facing docs/comments, а не runtime behavior;
- все файлы живут в одном домене;
- expected diff mostly textual;
- verification path простой и детерминированный.

### 2. Helm scope уже содержит и ложные цели, и historical artifacts

Не все совпадения в `helm/amp/**` надо включать в следующий slice:

- `helm/amp/README.md` уже синхронизирован с current truth:
  - `controlled replacement`;
  - `AGPL-3.0`;
  - restored operational surface описан честно.
- `helm/amp/CHANGELOG.md` содержит historical entry `Basic Alert History Service`, но это changelog, а не активный operator guide. Это lower-priority artifact, а не лучший первый target.

Вывод:

- следующий slice не должен автоматически трогать весь `helm/amp/**`;
- лучше взять именно operator docs/comments, а aligned README и historical changelog оставить вне первого прохода.

### 3. Examples cluster небольшой, но уже смешивает comments и example contract

Найдены:

- `examples/custom-classifier/main.go`
  - comments вроде `How to integrate with Alert History Service`.
- `examples/custom-publisher/main.go`
  - тот же тип stale comments.
- `examples/k8s/pagerduty-secret-example.yaml`
  - comments говорят `Alert History Service`;
  - manifest использует namespace `alert-history`.

Это выглядит относительно небольшим хвостом, но practical risk выше, чем у Helm comments:

- `.go` examples — это comments в коде;
- `pagerduty-secret-example.yaml` уже выражает примерный deployment contract, а не только prose.

Вывод:

- examples лучше держать отдельным follow-up slice после Helm cleanup, а не смешивать оба домена сразу.

### 4. Grafana asset cluster пока узкий, но не без решения по compatibility semantics

Найден `grafana/dashboards/alert-history-service.json`:

- dashboard title = `AMP - Alert History Service`;
- `uid = amp-alert-history`.

Title исправить легко, но `uid` — уже не просто cosmetic string:

- его могут использовать существующие imports/links/automation;
- менять его в docs-only pass без отдельного решения рискованно.

Вывод:

- Grafana asset лучше выделять отдельно от Helm/docs cleanup;
- как минимум нужен conscious decision: трогать только visible title или еще и identity fields.

### 5. Internal README cluster слишком широкий для ближайшего mergeable slice

Самый тяжелый residual cluster сейчас находится в `go-app/internal/**`.

Representative examples:

- `go-app/internal/infrastructure/k8s/README.md`
  - исторический branding `Alert History`;
  - `Production-Ready`;
  - hard coverage/performance claims;
  - old serviceAccount / object names `alert-history-service`.
- `go-app/internal/infrastructure/grouping/README.md`
  - `Production-Ready (150% Quality)`;
  - benchmark/coverage overclaims;
  - historical naming.
- `go-app/internal/infrastructure/repository/README.md`
  - обещает `/history` API и “Production-ready alert history repository”, хотя history surface сейчас не active runtime truth.
- `go-app/internal/core/services/README_DEDUPLICATION.md`
  - `Production-Ready`, precise coverage numbers, historical repo links.

Это уже не string-replacement work:

- многие документы большие;
- часть claims привязана к historical runtime surface;
- часть файлов требует factual rewrite, а не косметический cleanup.

Вывод:

- internal README cluster не подходит как ближайший slice после `/research`;
- его нужно дробить отдельно после более безопасного Helm/examples pass.

### 6. Часть matches вообще не должна входить в этот bugfix slice

Inventory также зацепил runtime/test strings:

- `go-app/internal/application/application.go`
- `go-app/internal/database/main.go`
- `go-app/internal/infrastructure/publishing/*.go`
- `go-app/internal/notification/template/data.go`
- `go-app/internal/infrastructure/publishing/formatter_test.go`

Это уже не docs/comments/assets cleanup в чистом виде:

- где-то это runtime logs;
- где-то event payload/source fields;
- где-то metric/test fixtures и compatibility data.

Вывод:

- в рамках `SECONDARY-REPO-DOC-HISTORICAL-DRIFT` их трогать нельзя без отдельного решения;
- иначе docs bug незаметно превратится в product rename / compatibility change.

## Scope Assessment

Текущий bug — это umbrella-domain, а не один implementation slice.

Самый безопасный порядок дробления сейчас выглядит так:

1. `helm/amp` operator docs/comments
2. `examples/**` examples/comments
3. `grafana/**` dashboard branding/identity
4. `go-app/internal/**` README-heavy rewrites
5. отдельное решение по runtime/test strings, если оно вообще нужно

## Options

### Option A: Repo-wide sweep сразу
- Плюс: максимальная консистентность.
- Минус: слишком широкий diff, высокий шанс смешать docs cleanup с runtime/product semantics.

### Option B: Сразу брать `go-app/internal/**`
- Плюс: закрывает самый большой residual cluster.
- Минус: это уже не narrow cleanup, а серия factual rewrites по крупным README и historical API narratives.

### Option C: Взять Helm operator-facing docs/comments как первый sub-slice
- Плюс: самый связный домен, текстовый diff, низкий риск scope creep.
- Плюс: правит operator-facing deployment story, где stale branding особенно вреден.
- Минус: examples/grafana/internal README останутся follow-up внутри того же bug domain.

### Option D: Объединить Helm + examples + grafana в один pass
- Плюс: одним коммитом закрывается почти весь non-internal хвост.
- Минус: в одном slice смешиваются chart comments, example manifests и dashboard identity semantics; это уже менее чистая граница.

## Recommendation

Для `/spec` брать `Option C`.

Рекомендованный sub-scope следующего slice:

- `helm/amp/DEPLOYMENT.md`
- `helm/amp/values-dev.yaml`
- `helm/amp/values-production.yaml`
- `helm/amp/values.yaml`
- `helm/amp/templates/postgresql-configmap.yaml`
- `helm/amp/templates/postgresql-networkpolicy.yaml`
- `helm/amp/templates/postgresql-statefulset.yaml`

Что явно не включать в этот `/spec`:

- `helm/amp/README.md` — уже aligned
- `helm/amp/CHANGELOG.md` — historical artifact, lower priority
- `examples/**`
- `grafana/**`
- `go-app/internal/**`
- любые runtime/test strings в `.go`

## Next-Step Implication

`/spec` должен зафиксировать следующий узкий contract:

- docs/comments-only cleanup в Helm operator assets;
- без изменения chart behavior, values schema, templates logic или runtime semantics;
- rewrite `DEPLOYMENT.md` под текущий `./helm/amp` path, current naming и honest active-runtime-first deployment story;
- cleanup stale `Alert History` / `Production-Ready` markers в Helm values/templates comments;
- explicit non-goal: не закрывать этим slice internal README cluster, examples и Grafana.

## Verification Path

- `rg -n 'Alert History|Production-Ready|alert-history' helm/amp/DEPLOYMENT.md helm/amp/values-dev.yaml helm/amp/values-production.yaml helm/amp/values.yaml helm/amp/templates/postgresql-configmap.yaml helm/amp/templates/postgresql-networkpolicy.yaml helm/amp/templates/postgresql-statefulset.yaml`
- manual review against `README.md`, `docs/06-planning/DECISIONS.md`, `helm/amp/README.md`
- `git diff --check`
