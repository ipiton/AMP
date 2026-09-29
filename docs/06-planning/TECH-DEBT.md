# TECH-DEBT

Работающее по контракту, но дорогое в сопровождении: плохая структура, хрупкий дизайн, нет тестов на рабочий код, слабая наблюдаемость. Если код нарушает свой контракт — это баг, место ему в `BUGS.md` (граница — `docs/solo-kanban/artifact-contract.md` § Bug Versus Tech Debt).

Погашенный долг удаляется; отчёт — в `DONE.md`.

## Bundles

<!-- Мелкие записи одной подсистемы (одних и тех же файлов) — в одного claimable-родителя.
     В Queue `NEXT.md` идёт только slug бандла. См. docs/solo-kanban/artifact-contract.md § Bundles. -->

## Critical

## High

## Medium

### [medium][Helm][~2d] HELM-CHART-GAPS
- **Title:** расхождения шаблонов чарта с values
- **Problem:** найдено при аудите `values-production.yaml` (INF-B, 2026-08-20), не исправлено (слишком большой темплейт-скоуп для values-аудита):
  - `valkey.enabled`/`cache.enabled` НЕ гейтят деплой `templates/redis-statefulset.yaml` — единственный гейт — `profile: standard`. Рабочий воркараунд — `valkey.replicas: 0` (заведён в values-production.yaml), но сами флаги мёртвые.
  - `postgresql.existingSecret` читается `templates/secret.yaml` (другой Secret, `<fullname>-secrets`), но НЕ читается `templates/postgresql-secret.yaml`/хелпером `amp.postgresql.secretName` — реальный DB-пароль (`DATABASE_PASSWORD`, `POSTGRES_PASSWORD` в StatefulSet) всегда берётся из `postgresql.password`, existingSecret туда не долетает.
  - BACKLOG когда-то просил "PostgreSQL cluster (3 instances)" — чарт этого не умеет: `postgresql-statefulset.yaml` — single-primary StatefulSet без репликации/failover. Реальная HA (CloudNativePG/Patroni) не реализована.
  - Найдено ревью fix-round (2026-08-20): базовый `values.yaml` до сих пор хардкодит слабый dev-дефолт для `postgresql.password` (dev/test zero-config install path). Не трогали — задача была про `values-production.yaml`, и требование "без дефолта" сломало бы dev-путь без правки `secret.yaml`'s fallback-логики. `values-production.yaml` явно перекрывает его на `""` (см. её комментарий) — инконсистентность зафиксирована здесь, не исправлена.
- **Impact:** операторы, включающие `existingSecret` или выключающие valkey флагом, получают не то, что заявлено в values.
- **Fix:** гейтить Redis по `valkey.enabled`, провести `existingSecret` через `amp.postgresql.secretName`; HA Postgres — отдельным решением (`PROD-POSTGRES-HA-DECISION` в BACKLOG).
- **Refs:** `helm/amp/templates/{redis-statefulset,secret,postgresql-secret,postgresql-statefulset}.yaml`
- **Status:** open

## Low

## Entry Format

```markdown
### [priority][area][estimate] DEBT-SLUG
- **Title:** short title
- **Problem:** what makes maintenance risky
- **Impact:** why it matters
- **Fix:** likely direction
- **Refs:** code, issue, task, or review links
- **Status:** open | in-progress | blocked
```

## Bundle Format

```markdown
### AREA-CLEANUP-BUNDLE
- [priority][area][combined estimate] AREA-CLEANUP-BUNDLE
- **Combined verify:** <одна команда; exit 0 — вся пачка готова>
- **Members (ordered):**
  - [ ] DEBT-SLUG-ONE
  - [ ] DEBT-SLUG-TWO
```

Полные записи остаются у участников; приоритет родителя — максимум по участникам.
