---
id: PROD-POSTGRES-HA-DECISION
slug: prod-postgres-ha-decision
stream: Helm
type: feature
priority: critical
status: active
created_at: 2026-10-15
updated_at: 2026-10-15
---

# Requirements: поддерживаемый путь PostgreSQL для прода

## Problem Framing

- **Symptom:** встроенный PostgreSQL чарта (`helm/amp/templates/postgresql-statefulset.yaml`) — single primary без репликации и failover, но чарт и доки подают его как production-ready (`values.yaml` «TN-98: HA support», `POSTGRESQL_RESTORE_GUIDE.md` с RTO < 30 мин / RPO < 5 мин). PDB `minAvailable: 1` при `replicas: 1` (`postgresql-poddisruptionbudget.yaml`, дефолт `podDisruptionBudget.enabled: true`) делает `kubectl drain` ноды с Postgres невозможным. Бэкапы (`pg_basebackup` CronJob + WAL archive) пишутся на PVC в том же кластере и namespace — при потере кластера/хранилища теряются вместе с данными.
- **Root Cause:** чарт вырос из TN-97/TN-98 без решения, кто отвечает за доступность и сохранность данных; HA-заявления в values и доках не подкреплены реализацией (TECH-DEBT `HELM-CHART-GAPS`).
- **Why Now:** P0 в BACKLOG § Production Readiness (аудит 2026-10-06): без этого AMP нельзя ставить в прод. `PROD-DEPS-NET-HTTP2` ждёт конца карантина, `PROD-RELEASE-V010` отложен владельцем — это следующая незаблокированная P0.
- **How We Measure:** ADR в `DECISIONS.md`; `helm template` с `postgresql.replicas: 1` не рендерит PDB Postgres; доки чарта прямо говорят, что встроенный PG — dev/pilot, и где ответственность оператора за бэкапы; один реальный прогон restore на k3d по guide с проверкой данных, лог в `evidence/`.

## Risk Profile

- **Signals:** `C M R`
  - `C` — меняется рендер чарта: PDB Postgres пропадает при `replicas: 1` (оператор, полагающийся на него, увидит изменение); возможны правки values.
  - `M` — data integrity: процедура restore (удаление PVC, восстановление из бэкапа) и позиция по бэкапам; ошибка в guide = потеря данных у оператора.
  - `R` — поведение кластера: drain ноды с Postgres начинает проходить (с даунтаймом БД).
- **Tier:** Full
- **Notes:** `M` эскалирует в Full независимо от остального ⇒ `deep-review` обязателен. `S` не ставлю: креды/доступы не меняются (`existingSecret` в `HELM-CHART-GAPS` — вне scope).

## User Stories

1. Как оператор, который ставит AMP в прод, я хочу явно знать, какой PostgreSQL поддерживается для прода и что встроенный — только dev/pilot, чтобы не получить single point of failure под видом HA.
2. Как оператор однонодового/маленького кластера, я хочу, чтобы `kubectl drain` не зависал на PDB Postgres с одной репликой.
3. Как оператор, я хочу понимать, что бэкапы чарта лежат в том же кластере, и иметь рабочую, проверенную процедуру restore.

## Success Criteria

- [ ] ADR в `docs/06-planning/DECISIONS.md` (следующий номер после ADR-015): поддерживаемый путь для прода (внешний managed PG / оператор вроде CloudNativePG), статус встроенного StatefulSet (dev/pilot), позиция по off-cluster бэкапам.
- [ ] PDB Postgres не рендерится при `postgresql.replicas: 1` (или эквивалент, выбранный в spec), render-проверка на дефолтных и production values.
- [ ] `helm/amp/README.md` (и/или `values.yaml` комментарии) — встроенный PG помечен dev/pilot, ссылка на ADR; вводящие в заблуждение «HA support» убраны.
- [ ] Off-cluster бэкапы: либо реализованы (S3), либо явно задокументированы как ответственность оператора — по решению в ADR.
- [ ] Один проверенный прогон restore на k3d: данные AMP до сбоя → backup → потеря PVC → restore по guide → данные на месте; лог и команды в `evidence/`. Guide приведён в соответствие с текущим чартом (имена ресурсов, шаги), если прогон этого требует.
- [ ] `CHANGELOG.md` `[Unreleased]` — изменение рендера PDB (migration note для тех, кто на него полагался).

## Non-Goals

- Реализация CloudNativePG/Patroni в чарте.
- `externalDatabase.*` values и `DATABASE_*` из Secret — это `HELM-EXTERNAL-DATABASE` (P1); ADR на него ссылается.
- Остальное из `HELM-CHART-GAPS` (`existingSecret`, лишний Redis, `ssl` bool-safety, NetworkPolicy selectors).
- Requests/HPA/`values-small.yaml` — `HELM-SINGLE-NODE-DEFAULTS` (пункт про PDB Postgres забирает эта задача; там убрать).
- PITR-прогон — только full restore; PITR — по итогам research.

## Constraints

- **Scope:** `helm/amp/templates/postgresql-poddisruptionbudget.yaml`, `helm/amp/values*.yaml` (комментарии/дефолты PDB), `helm/amp/README.md`, `helm/amp/docs/POSTGRESQL_RESTORE_GUIDE.md`, `DECISIONS.md`, `CHANGELOG.md`, planning (`BACKLOG.md`, `TECH-DEBT.md`). Go-код не трогаем.
- **Security:** not applicable (креды не меняются); в evidence не писать пароли.
- **Compatibility:** изменение рендера PDB — migration note; дефолты values для существующих установок не ломать; restore guide — деструктивные шаги с явными предупреждениями.
- **Size:** оценка ~1d; если restore-прогон вскроет неработающий backup CronJob/WAL archive (а не только устаревший guide) — отдельный баг в `BUGS.md` и, при необходимости, отдельный slice.

## Discovery Notes

- Similar tasks: `tasks/archive/HELM-DEFAULTS-VALIDATE/` (render-проверки чарта, решения владельца по дефолтам); ADR-015 как пример решения по чарту.
- Relevant patterns: render-тесты чарта в `scripts/release-gate.sh` (helm lint/template); k3d есть локально (`/opt/homebrew/bin/k3d`), kind нет.
- Duplicates: в `tasks/`, `tasks/archive/`, `DONE.md`, `archive/DONE-*.md` — нет. Пересечения: `HELM-SINGLE-NODE-DEFAULTS` (PDB), `HELM-EXTERNAL-DATABASE` (внешняя БД), TECH-DEBT `HELM-CHART-GAPS` (HA Postgres).
- Наблюдения при discovery:
  - `POSTGRESQL_RESTORE_GUIDE.md` (551 строка, последний содержательный коммит 2025-12-04) устарел: 37 упоминаний старого имени `alert-history`; шаг 2 удаляет PVC, а restore Job в шаге 4 монтирует этот же PVC (StatefulSet scaled to 0 его не пересоздаст); `kubectl wait job/postgresql-restore-*` — glob в имени не работает.
  - Backup PVC `<fullname>-backup` монтируется и StatefulSet'ом (WAL archive, `postgresql-statefulset.yaml:242`), и CronJob'ом — при RWO и разных нодах CronJob не стартует.
  - Образ `postgres:16` без patch-версии в StatefulSet и CronJob.
- Open unknowns:
  - работают ли сейчас backup CronJob и WAL archive вообще (проверит прогон);
  - нужен ли S3-вариант бэкапов в чарте или достаточно документации (решение в research/ADR);
  - PDB при `replicas > 1` — смысл есть только с реальной репликацией, которой нет; рендерить ли его вообще.
