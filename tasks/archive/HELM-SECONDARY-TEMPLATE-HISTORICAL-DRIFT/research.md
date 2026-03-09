# Research: HELM-SECONDARY-TEMPLATE-HISTORICAL-DRIFT

## Контекст
После закрытия `SECONDARY-REPO-DOC-HISTORICAL-DRIFT` основной operator-facing Helm narrative уже выровнен: `helm/amp/DEPLOYMENT.md`, `values*.yaml` и часть PostgreSQL templates синхронизированы с current `AMP` / `controlled replacement` truth. Новый task нужен не для повторного прохода по всему `helm/amp`, а для остаточного cleanup в secondary templates под `helm/amp/templates/**`.

Цель этого research — подтвердить фактический residual inventory и выбрать следующий mergeable sub-slice для `/spec`, не раздувая задачу обратно до всего Helm/docs домена.

## Source of Truth
- `README.md`
- `docs/06-planning/DECISIONS.md`
- `docs/06-planning/BUGS.md`
- `helm/amp/README.md`
- `tasks/archive/SECONDARY-REPO-DOC-HISTORICAL-DRIFT/research.md`

## Inventory Method
- marker scan:
  - `rg -n -i "Alert History|Production-Ready|150% quality|150% observability|alert-history|production hardening|production-ready|quality" helm/amp/templates`
- focused review:
  - `postgresql-poddisruptionbudget.yaml`
  - `postgresql-service-headless.yaml`
  - `postgresql-exporter-configmap.yaml`
  - `postgresql-configmap.yaml`

## Findings

### 1. Реальный residual cluster уже, чем звучит task title

Повторный scan по `helm/amp/templates/**` не нашел широкого хвоста по всему каталогу. Явные historical/overclaim markers остались только в PostgreSQL secondary templates:

- `helm/amp/templates/postgresql-poddisruptionbudget.yaml`
- `helm/amp/templates/postgresql-service-headless.yaml`
- `helm/amp/templates/postgresql-exporter-configmap.yaml`

Дополнительно `postgresql-configmap.yaml` все еще содержит operational wording вроде `Production hardening` и `Observability`, но уже без `Alert History`, `Production-Ready`, `150% quality` или другого явного historical branding.

### 2. Hidden `alert-history` branding в templates больше не осталось

По текущему scan:

- в `helm/amp/templates/**` больше нет `Alert History` / `alert-history`;
- в `README.md`, `docs/06-planning/DECISIONS.md` и `helm/amp/README.md` тоже нет этого stale vocabulary;
- открытый residual bug теперь в основном про `150% quality` / `150% observability` и related overclaim metadata, а не про старое product naming.

Следствие:

- следующий slice можно делать значительно уже, чем предыдущий Helm cleanup;
- нет необходимости снова открывать `DEPLOYMENT.md`, `values*.yaml` или уже очищенные templates.

### 3. `postgresql-poddisruptionbudget.yaml` и `postgresql-service-headless.yaml` — чистый low-risk cleanup

В обоих файлах drift одинаковый и локальный:

- `metadata.annotations.tn-98: "Production hardening (150% quality)"`

Что важно:

- это rendered annotation, а не comment;
- annotation human-facing и не участвует в selectors, names, ports или template conditions;
- правка здесь не требует изменения chart logic.

Вывод:

- оба файла хорошо подходят для следующего mergeable slice;
- cleanup нужно трактовать как rendered metadata wording change, а не как pure comment-only pass.

### 4. `postgresql-exporter-configmap.yaml` тоже подходит, но там scope чуть шире

В exporter ConfigMap найдено сразу несколько stale markers:

- `metadata.annotations.description: "PostgreSQL Exporter custom queries for 150% observability (TN-98)"`
- banner comment `# Target: 50+ Metrics for Enterprise Observability`
- banner comment `# TN-98: 150% Quality Target`

При этом остальное содержимое файла — это сами exporter queries и metric descriptions, которые уже выглядят factual и не требуют rewrite.

Вывод:

- безопасный cleanup тут возможен;
- `/spec` должен явно зафиксировать, что меняются только top-level annotation/banner strings, а не query SQL, metric names или metric descriptions.

### 5. `postgresql-configmap.yaml` сейчас пограничный, но не обязателен для первого pass

В `postgresql-configmap.yaml` остались строки вроде:

- `TN-98: Production hardening and performance tuning`
- `Security (TN-98: Production hardening)`
- `CREATE MONITORING VIEWS (TN-98: Observability)`

Это уже не тот же тип drift, что в трех primary files:

- нет `Alert History`;
- нет `Production-Ready`;
- нет `150% quality` / `150% observability`;
- wording скорее operational, чем marketing/historical.

Вывод:

- включать `postgresql-configmap.yaml` в ближайший slice необязательно;
- если задача останется узкой, его лучше оставить вне первого `/spec`, чтобы не спорить о границе между honest operational prose и residual overclaim cleanup.

### 6. Следующий template slice не должен повторять ошибку “cleanup всего каталога”

В каталоге `helm/amp/templates/` много других файлов, но marker scan не показал там сравнимого residual drift. Поэтому bug title шире, чем фактический ближайший mergeable scope.

Практический смысл:

- не нужно делать repo-like sweep по всем templates;
- лучше закрыть конкретный confirmed residual cluster и только потом решать, остается ли после него что-то достойное отдельного follow-up.

## Options

### Option A: Broad PostgreSQL template sweep

Включить:

- `postgresql-poddisruptionbudget.yaml`
- `postgresql-service-headless.yaml`
- `postgresql-exporter-configmap.yaml`
- `postgresql-configmap.yaml`

Плюсы:

- одним проходом закрывается весь видимый PostgreSQL template tail.

Минусы:

- `postgresql-configmap.yaml` уже не содержит явного historical branding;
- slice быстро превращается из cleanup известных overclaims в более субъективный prose pass.

### Option B: Narrow 3-file cleanup

Включить только:

- `postgresql-poddisruptionbudget.yaml`
- `postgresql-service-headless.yaml`
- `postgresql-exporter-configmap.yaml`

Плюсы:

- минимальный и очень четкий diff;
- все найденные правки относятся к одному типу drift;
- scope легко проверить marker scan + render smoke.

Минусы:

- `postgresql-configmap.yaml` останется потенциальной серой зоной до отдельного решения.

### Option C: Re-open all `helm/amp/templates/**`

Плюсы:

- максимальная консистентность за один pass.

Минусы:

- scope снова становится рыхлым;
- слишком высокий риск притянуть файлы, где уже нет честно подтвержденного drift.

## Recommendation

Для `/spec` брать `Option B`.

Рекомендованный sub-scope:

- `helm/amp/templates/postgresql-poddisruptionbudget.yaml`
- `helm/amp/templates/postgresql-service-headless.yaml`
- `helm/amp/templates/postgresql-exporter-configmap.yaml`

Что явно не включать в следующий `/spec`:

- `helm/amp/DEPLOYMENT.md`
- `helm/amp/values*.yaml`
- уже очищенные PostgreSQL templates
- `postgresql-configmap.yaml` как отдельный operational prose review
- `examples/**`
- `grafana/**`
- `go-app/internal/**`

## Next-Step Implication

`/spec` должен зафиксировать такой contract:

- cleanup только confirmed residual overclaim/historical wording в трех templates;
- допускаются правки rendered annotations и top-level banner comments;
- нельзя менять template conditions, names, selectors, ports, SQL queries, metric names или metric descriptions;
- `postgresql-configmap.yaml` остается вне scope, если во время `/spec` не появится новая, более сильная причина включать его.

## Suggested Verification Path

- targeted marker scan:
  - `rg -n -i "150% quality|150% observability|50\\+ Metrics|Production-Ready|Alert History|alert-history" helm/amp/templates/postgresql-poddisruptionbudget.yaml helm/amp/templates/postgresql-service-headless.yaml helm/amp/templates/postgresql-exporter-configmap.yaml`
- manual review against:
  - `README.md`
  - `docs/06-planning/DECISIONS.md`
  - `helm/amp/README.md`
- render smoke:
  - `helm template amp-dev ./helm/amp -f helm/amp/values-dev.yaml --set profile=lite`
  - `helm template amp ./helm/amp -f helm/amp/values-production.yaml --set profile=standard`
- `git diff --check`
