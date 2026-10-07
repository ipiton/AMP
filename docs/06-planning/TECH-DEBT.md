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
  - `valkey.enabled`/`cache.enabled` НЕ гейтят деплой `templates/redis-statefulset.yaml` — единственный гейт — `profile: standard`. Рабочий воркараунд — `valkey.replicas: 0` (заведён в values-production.yaml), но сами флаги мёртвые. _(Уточнено аудитом 2026-10-06, `helm template`: `valkey.replicas: 0` обнуляет именно `amp-redis` — тот Redis, в который ходит AMP по умолчанию (`REDIS_ADDR=amp-redis:6379`), — а Deployment сабчарта `amp-valkey` продолжает деплоиться. Обход работает только с внешним Redis (`cache.host`, как Dragonfly в values-production). `cache.enabled: false` ставит `REDIS_ADDR=localhost:6379`. Без внешнего Redis штатно убрать лишний инстанс нельзя — два Redis на каждый `standard`-деплой. Связано с `HELM-SINGLE-NODE-DEFAULTS` в BACKLOG.)_
  - `postgresql.existingSecret` читается `templates/secret.yaml` (другой Secret, `<fullname>-secrets`), но НЕ читается `templates/postgresql-secret.yaml`/хелпером `amp.postgresql.secretName` — реальный DB-пароль (`DATABASE_PASSWORD`, `POSTGRES_PASSWORD` в StatefulSet) всегда берётся из `postgresql.password`, existingSecret туда не долетает.
  - BACKLOG когда-то просил "PostgreSQL cluster (3 instances)" — чарт этого не умеет: `postgresql-statefulset.yaml` — single-primary StatefulSet без репликации/failover. Реальная HA (CloudNativePG/Patroni) не реализована.
  - Найдено ревью fix-round (2026-08-20): базовый `values.yaml` до сих пор хардкодит слабый dev-дефолт для `postgresql.password` (dev/test zero-config install path). Не трогали — задача была про `values-production.yaml`, и требование "без дефолта" сломало бы dev-путь без правки `secret.yaml`'s fallback-логики. `values-production.yaml` явно перекрывает его на `""` (см. её комментарий) — инконсистентность зафиксирована здесь, не исправлена.
  - _(2026-10-01, PROD-INGRESS-HARDENING R17)_ сабчарт valkey рендерит свою NetworkPolicy `amp-valkey` (deny-all по умолчанию) параллельно чартовой `redis-networkpolicy.yaml`; политики postgres/redis хардкодят `namespaceSelector` `name: monitoring` / `name: kube-system` вместо автоматической метки `kubernetes.io/metadata.name`; `ingress.yaml` безусловно добавляет nginx sticky-аннотации (`affinity: cookie` и др.) к любым `ingress.annotations`.
- **Impact:** операторы, включающие `existingSecret` или выключающие valkey флагом, получают не то, что заявлено в values.
- **Fix:** гейтить Redis по `valkey.enabled`, провести `existingSecret` через `amp.postgresql.secretName`; HA Postgres — отдельным решением (`PROD-POSTGRES-HA-DECISION` в BACKLOG).
- **Refs:** `helm/amp/templates/{redis-statefulset,secret,postgresql-secret,postgresql-statefulset}.yaml`
- **Status:** open

## Low

### [low][Gate][~0.1d] RELEASE-GATE-UNQUOTED-ARGS
- **Title:** `scripts/release-gate.sh` собирает аргументы helm строкой без кавычек
- **Problem:** `args="-f $HELM_CHART_DIR/values-dev.yaml"` и `-f $HELM_CHART_DIR/tests/values-production-placeholders.yaml` раскрываются word-split'ом: путь с пробелом ломает шаг, `[`/`*` в значениях подвержены glob-раскрытию (поэтому placeholder'ы PROD-INGRESS-HARDENING ушли в файл, а не в `--set-json`).
- **Impact:** чекаут в каталоге с пробелом роняет helm-шаги гейта; следующий `--set` со спецсимволами — тихая порча аргументов.
- **Fix:** bash-массивы аргументов в `step_helm_values`/`step_helm_rbac`.
- **Refs:** deep-review PROD-INGRESS-HARDENING R14; 2026-10-01.
- **Status:** open

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
