# Research: EXAMPLES-HISTORICAL-DOC-DRIFT

## Контекст
После закрытия Helm-related docs slices в репозитории остался отдельный residual cluster в `examples/**`. На первый взгляд bug выглядит как простой examples/docs cleanup, но фактический inventory показал смешение двух разных типов drift:

1. stale source comments и integration prose в `.go` examples;
2. устаревший Kubernetes sample contract в `examples/k8s/*.yaml`.

Цель этого research — не “почистить весь examples каталог”, а выбрать следующий mergeable sub-slice для `/spec` без скрытого перехода в runtime/product rewrite.

## Source of Truth
- `README.md`
- `docs/06-planning/DECISIONS.md`
- `docs/06-planning/BUGS.md`
- `examples/README.md`
- `docs/CONFIGURATION_GUIDE.md`
- архив `tasks/archive/PHASE-4-PRODUCTION-PUBLISHING-PATH/`

## Inventory Method
- marker scan:
  - `rg -n -i "Alert History|alert-history|production-ready|alerthistory|drop-in|100%|alertmanager" examples`
- focused review:
  - `examples/custom-classifier/main.go`
  - `examples/custom-publisher/main.go`
  - `examples/k8s/pagerduty-secret-example.yaml`
  - `examples/k8s/rootly-secret-example.yaml`
  - `examples/README.md`
- contract comparison against:
  - `docs/CONFIGURATION_GUIDE.md`
  - `tasks/archive/PHASE-4-PRODUCTION-PUBLISHING-PATH/Spec.md`
  - `helm/amp/templates/secret.yaml`
  - `helm/amp/templates/rootly-secrets.yaml`

## Findings

### 1. `examples/README.md` уже aligned и не должен втягиваться в следующий slice

`examples/README.md` уже описывает examples честно:

- как reference examples around current `pkg/core` contracts;
- без claim о full plugin system;
- с явной оговоркой “starting points, not drop-in production integrations”.

Вывод:

- `examples/README.md` не является текущей проблемой;
- ближайший slice не должен тратить diff на этот файл.

### 2. `custom-classifier/main.go` содержит stale branding и интеграционную историю, не подтвержденную active truth

Найдены два класса drift:

- top comments:
  - `How to integrate with Alert History Service`
- bottom integration section:
  - `Integration with Alert History Service`
  - `Configure Alert History to use your classifier`
  - пример `config.yml` с `classification.default_classifier`

Проблема здесь уже не только в branding:

- `Alert History Service` больше не соответствует current repo truth;
- integration prose выглядит как живой runtime contract, хотя текущие top-level docs описывают examples как shape/wiring references, а не как verified plugin bootstrap path;
- `classification.default_classifier` не подтвержден current active docs как публичный contract для такого расширения.

Вывод:

- этот файл требует не простого search/replace, а более аккуратного narrowing prose;
- это source-example narrative cleanup, а не manifest contract update.

### 3. `custom-publisher/main.go` держит еще более явный contract drift

Найдены:

- top comments:
  - `How to integrate with Alert History Service`
- bottom integration section:
  - `Integration with Alert History Service`
  - example `config.yml` with `publishing.targets`

Это уже прямое противоречие текущему source of truth:

- после `PHASE-4-PRODUCTION-PUBLISHING-PATH` canonical production contract закреплен за Kubernetes Secret discovery;
- `docs/CONFIGURATION_GUIDE.md` фиксирует `publishing-target=true` + `data.config`;
- active runtime не позиционируется как система с verified static `config.yml: publishing.targets` integration story для этого example path.

Вывод:

- `custom-publisher/main.go` содержит не только stale branding, но и stale example contract;
- его cleanup потребует conscious rewriting integration section, а не только переименование строк.

### 4. `pagerduty-secret-example.yaml` — уже не просто docs drift, а устаревший sample manifest contract

В файле найдены:

- historical branding:
  - `Alert History Service`
- historical namespace usage:
  - `namespace: alert-history`
  - usage/troubleshooting examples with `-n alert-history`
- stale secret payload shape:
  - `stringData.target.json`, а не canonical `stringData.config`

По текущему source of truth:

- canonical discovery label = `publishing-target=true`;
- canonical payload key = `data.config` / `stringData.config`;
- current docs используют generic namespace examples вроде `monitoring`, а не hardcoded `alert-history`.

Вывод:

- файл напрямую обучает пользователя outdated contract;
- это сильнее и опаснее, чем просто stale comments в source examples.

### 5. `rootly-secret-example.yaml` всплыл как дополнительный manifest drift, хотя не был явно назван в bug entry

Этот файл уже не содержит `Alert History Service`, но все равно устарел по contract:

- `namespace: alert-history`
- old discrete secret fields:
  - `data.name`
  - `data.type`
  - `data.url`
  - `data.api_key`
- current canonical contract требует один JSON payload в `config`

С точки зрения пользователя это такой же outdated sample manifest, как и PagerDuty example.

Вывод:

- examples cluster фактически включает оба `examples/k8s/*.yaml`, а не только `pagerduty-secret-example.yaml`;
- research должен это зафиксировать явно, even if old bug text named only one of them.

### 6. Examples cluster делится на два разных sub-domains

По результатам review ближайший cleanup нельзя считать единым homogeneous pass.

Есть две независимые подзадачи:

1. **Source-example prose cleanup**
   - `examples/custom-classifier/main.go`
   - `examples/custom-publisher/main.go`

2. **Kubernetes sample contract cleanup**
   - `examples/k8s/pagerduty-secret-example.yaml`
   - `examples/k8s/rootly-secret-example.yaml`

Почему это важно:

- `.go` examples требуют аккуратно переписать integration story без ложного claim о live plugin bootstrap;
- `k8s` examples требуют выровнять конкретный Secret contract с current production publishing truth.

### 7. K8s sample manifests — более сильный и более детерминированный следующий slice

По сравнению с `.go` source examples у `k8s` manifests есть явное преимущество:

- current canonical contract уже четко зафиксирован;
- desired result можно описать детерминированно;
- verification path проще;
- user-facing risk выше: sample YAML сейчас прямо показывает не тот payload shape, который ждёт current runtime.

Вывод:

- если выбирать следующий mergeable sub-slice, лучше брать именно `examples/k8s/*.yaml`;
- `.go` source examples разумнее оставить follow-up within the same broader examples domain.

## Options

### Option A: Sweep всех four files сразу

Включить:

- `custom-classifier/main.go`
- `custom-publisher/main.go`
- `k8s/pagerduty-secret-example.yaml`
- `k8s/rootly-secret-example.yaml`

Плюсы:

- одним проходом закрывается почти весь visible examples tail.

Минусы:

- смешиваются source prose rewrite и manifest contract rewrite;
- повышается шанс нечестно придумать integration story для `.go` examples;
- scope перестает быть очень четким.

### Option B: Только source-example prose cleanup

Включить:

- `custom-classifier/main.go`
- `custom-publisher/main.go`

Плюсы:

- минимальный textual diff;
- не трогаются manifests.

Минусы:

- в репозитории остаются sample YAML, которые сейчас учат outdated publishing target contract;
- это менее полезный следующий slice.

### Option C: Только Kubernetes sample contract cleanup

Включить:

- `examples/k8s/pagerduty-secret-example.yaml`
- `examples/k8s/rootly-secret-example.yaml`

Плюсы:

- strongest source-of-truth alignment;
- current target contract already explicit in docs and previous completed tasks;
- higher user impact than source comments;
- verification path простой: review against canonical `publishing-target=true` + `stringData.config`.

Минусы:

- `.go` example comments останутся отдельным follow-up.

## Recommendation

Для `/spec` брать `Option C`.

Рекомендованный sub-scope:

- `examples/k8s/pagerduty-secret-example.yaml`
- `examples/k8s/rootly-secret-example.yaml`

Что должно войти в этот sub-slice:

- убрать hardcoded `alert-history` namespace narrative в usage/examples;
- перевести examples на canonical publishing target Secret contract;
- использовать `stringData.config` / `data.config` style instead of legacy discrete fields or `target.json`;
- сохранить examples как reference YAML, а не превращать их в broad product/ops guide.

Что явно не включать в следующий `/spec`:

- `examples/README.md`
- `examples/custom-classifier/main.go`
- `examples/custom-publisher/main.go`
- любые runtime or Helm files

## Next-Step Implication

`/spec` должен зафиксировать narrow contract:

- cleanup только Kubernetes publishing examples;
- source of truth = current canonical publishing Secret contract from `PHASE-4-PRODUCTION-PUBLISHING-PATH` and `docs/CONFIGURATION_GUIDE.md`;
- допускаются structural YAML changes в example manifests, потому что они отражают example contract, а не production runtime code;
- `.go` examples остаются explicit follow-up within the broader examples domain.

## Suggested Verification Path

- targeted search:
  - `rg -n -i "Alert History|alert-history|target.json|api_key:" examples/k8s`
- manual review against:
  - `docs/CONFIGURATION_GUIDE.md`
  - `docs/MIGRATION_QUICK_START.md`
  - `tasks/archive/PHASE-4-PRODUCTION-PUBLISHING-PATH/Spec.md`
- YAML sanity review for:
  - `publishing-target=true`
  - `stringData.config` / `data.config`
  - no hardcoded real secrets
- `git diff --check`
