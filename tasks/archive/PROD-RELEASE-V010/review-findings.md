# Deep Review Findings: первый релиз AMP v0.1.0

**Trigger classification:** mandatory (pre-release review: тег `v*` публикует образы в GHCR)
**Reviewer perspective:** независимый агент (general-purpose subagent), не автор правок; повторная проверка фиксов — тот же агент
**Reviewed at:** 2026-11-09
**Reviewed tree:** `feature/prod-release-v010` @ `46cc203` (первый проход), фиксы `7cc214b` (повторный)
**Verdict:** pass (see `review-verdict.json`)

Охват: диф ветки и весь релиз — `release.yml`/`ci.yml`/`Dockerfile`, свежая установка чарта (default, production + placeholders, small, dev, lite, `valkey.enabled`), переименование метрик, сверка CHANGELOG `[0.1.0]` и release notes с кодом, security-дефолты.

## Findings

### F1 — свежая установка с встроенным PG почти всегда стартует без истории
- **Severity:** major
- **Location:** `go-app/cmd/amp-upstream/amp.go:163-185` (`connectDatabase`), `helm/amp/templates/statefulset.yaml`
- **Issue:** AMP подключается к PG один раз при старте; поды AMP и PG стартуют параллельно, PG ещё тянет образ и делает initdb. Readiness БД не проверяет — тихо, одна строка ERROR.
- **Recommendation:** init container, ждущий встроенный PG; настоящий фикс — переподключение.
- **Disposition:** fix-here (init container `wait-for-postgres`, `pg_isready -t 1`, ≤ ~2 мин, затем старт без БД) + defer-tech-debt (переподключение)
- **Follow-up:** `TECH-DEBT.md` `UPSTREAM-HISTORY-PG-RECONNECT` поднят до medium

### F2 — дефолтный рендер требует CRD prometheus-operator
- **Severity:** major
- **Location:** `helm/amp/values.yaml` (`monitoring.prometheusEnabled: true`), `templates/servicemonitor.yaml`, `templates/postgresql-prometheus-rules.yaml`
- **Issue:** без kube-prometheus-stack `helm install` падает на `ServiceMonitor`; требование было описано только в разделе ServiceMonitor.
- **Recommendation:** гейт по `.Capabilities` или хотя бы пререквизит в Quick Start.
- **Disposition:** fix-here (README Quick Start, Known Gaps в release notes) + defer-backlog (гейт по Capabilities)
- **Follow-up:** `BACKLOG.md` `HELM-MONITORING-CRD-GATE`

### F3 — ссылки на чужой репозиторий в NOTES
- **Severity:** minor
- **Location:** `helm/amp/templates/NOTES.txt:138-139`
- **Disposition:** fix-here (→ `https://github.com/ipiton/AMP`, строка Docs убрана)
- **Follow-up:** n/a

### F4 — опубликованный дефолтный пароль встроенного PG без предупреждения
- **Severity:** minor
- **Location:** `helm/amp/values.yaml:268`, `NOTES.txt`
- **Disposition:** fix-here (предупреждение в NOTES, строка в Quick Start) + defer-tech-debt (случайный пароль)
- **Follow-up:** `TECH-DEBT.md` `HELM-CHART-GAPS` (пункт про слабый пароль уже там)

### F5 — нет auth при NodePort/LoadBalancer показывается как info
- **Severity:** minor
- **Location:** `NOTES.txt:33`
- **Disposition:** fix-here (⚠️ при `service.type` ≠ ClusterIP без `webConfig.existingSecret`)
- **Follow-up:** `BACKLOG.md` `SERVICE-TYPE-EXPOSURE-GUARD` (жёсткий guard) остаётся

### F6 — Grafana-дашборд в основном рисует метрики удалённого ядра
- **Severity:** minor
- **Location:** `grafana/dashboards/alert-history-service.json`; CHANGELOG и notes писали «is updated»
- **Disposition:** fix-here (формулировка) + defer-backlog (дашборд на upstream-метриках)
- **Follow-up:** `BACKLOG.md` `GRAFANA-DASHBOARD-UPSTREAM`

### F7 — chart CHANGELOG противоречит себе внутри `[0.1.0]`
- **Severity:** minor
- **Location:** `helm/amp/CHANGELOG.md`, секция UPSTREAM-CORE-REMOVAL (`valkey.replicas: 0`)
- **Disposition:** fix-here (помечено как заменённое PROD-RELEASE-V010)
- **Follow-up:** n/a

### F8 — устаревшие шаги апгрейда Valkey в release notes
- **Severity:** minor
- **Location:** `docs/RELEASE_NOTES_v0.1.0.md` (HELM-VCT-CHART-LABELS, HELM-SINGLE-NODE-DEFAULTS)
- **Disposition:** fix-here (пометка «только с `valkey.enabled: true`» в notes и CHANGELOG, текст breaking сохранён дословно)
- **Follow-up:** n/a

### F9 — `release.yml` не проверяет, что тег на `main` и CI зелёный
- **Severity:** minor
- **Location:** `.github/workflows/release.yml`
- **Disposition:** defer-backlog (для v0.1.0 — ручной процесс: push `main`, зелёный CI, затем тег)
- **Follow-up:** `BACKLOG.md` `RELEASE-TAG-ON-MAIN-GUARD`

### F10 — базовые образы закреплены тегом, не digest
- **Severity:** minor
- **Location:** `Dockerfile:7`, `:47`
- **Disposition:** defer-tech-debt
- **Follow-up:** `TECH-DEBT.md` `IMAGE-BASE-DIGEST-PIN`

### F11 — удалённый ключ, заданный как null, тоже валит рендер
- **Severity:** nit
- **Location:** `helm/amp/templates/_validate.tpl`
- **Disposition:** reject (reason: ключ надо убрать из values, null — не способ; поведение совпадает с проверкой верхнего уровня)
- **Follow-up:** n/a

### F12 — устаревшие комментарии
- **Severity:** nit
- **Location:** `go-app/internal/infrastructure/repository/postgres_history.go:50`, `helm/amp/templates/_helpers.tpl:86`
- **Disposition:** fix-here
- **Follow-up:** n/a

### F13 — ожидание PG могло превысить 120s (повторный проход)
- **Severity:** nit
- **Location:** `helm/amp/templates/statefulset.yaml` (`pg_isready` без `-t`)
- **Disposition:** fix-here (`-t 1`, в доках «около двух минут»)
- **Follow-up:** n/a

### F14 — init container не упомянут в корневом CHANGELOG и notes (повторный проход)
- **Severity:** nit
- **Disposition:** fix-here
- **Follow-up:** n/a

### F15 — образ `postgres:16.15` в init container не переопределяется (повторный проход)
- **Severity:** nit
- **Location:** `helm/amp/templates/statefulset.yaml`
- **Disposition:** defer-tech-debt (как и у StatefulSet PG)
- **Follow-up:** `TECH-DEBT.md` `HELM-CHART-GAPS`

## Anti-Pattern Check

- [x] Self-audit was not treated as a substitute for independent review.
