---
id: PROD-POSTGRES-HA-DECISION
slug: prod-postgres-ha-decision
stream: Helm
type: feature
status: draft
created_at: 2026-10-15
updated_at: 2026-10-15
based_on:
  - requirements.md
  - research.md
---

# Specification: поддерживаемый путь PostgreSQL для прода

**Version:** 1.0  
**Status:** Draft

## Summary

Прод-путь AMP — внешний PostgreSQL; встроенный StatefulSet честно объявляется dev/pilot без бэкапов. Из чарта убираются неработающие backup CronJob/PVC/WAL-архив, бессмысленный PDB и фиктивные backup-алерты, опасные комбинации (`replicas != 1`, `backup.enabled: true`) падают на рендере, restore guide заменяется runbook'ом на `pg_dump`/`pg_restore`, проверенным полным прогоном на k3d.

## Requirements Coverage

| Requirement / Criterion | Covered by |
|---|---|
| ADR о поддерживаемом пути | Component Architecture: `DECISIONS.md` ADR-016 |
| PDB Postgres не рендерится при `replicas: 1` | D1; render-тест R1 |
| Встроенный PG помечен dev/pilot, «HA support» убран | D7, D8 |
| Off-cluster бэкапы — ответственность оператора | ADR-016; D3; D8; runbook D9 |
| Проверенный прогон restore на k3d | D9; Verification V1 |
| `CHANGELOG.md` migration note | D10 |

## Current State

- **Chart:** `helm/amp/templates/postgresql-statefulset.yaml` (`replicas: {{ .Values.postgresql.replicas | default 1 }}` без репликации; backup volume `:104-107,239-243`; `image: postgres:16`), `postgresql-poddisruptionbudget.yaml` (`minAvailable: 1`), `postgresql-backup-cronjob.yaml`, `postgresql-backup-pvc.yaml` (без `resource-policy: keep`), `postgresql-configmap.yaml:46-57` (WAL archive под `backup.enabled`), `postgresql-prometheus-rules.yaml:113-133` (`PostgreSQLWALArchivingFailed`, `PostgreSQLBackupOld` — метрику `postgresql_backup_last_success_timestamp_seconds` никто не публикует), `tests/postgresql-test-connection.yaml` (`postgres:16`).
- **Values:** `values.yaml:324-345` (`podDisruptionBudget`, `backup` — оба `enabled: true`), `values-production.yaml:93-96+` (то же).
- **Docs:** `helm/amp/README.md` (раздела про PostgreSQL нет; есть § Storage, § Upgrading), `helm/amp/docs/POSTGRESQL_RESTORE_GUIDE.md` (551 строка, невыполним), `helm/amp/CHANGELOG.md`, корневой `CHANGELOG.md`.
- **Tests:** render-тесты `helm/amp/tests/render-*.sh`, release-gate гоняет все (`scripts/release-gate.sh:228-240`, шаг `helm-tests`); `helm lint/template` для `values.yaml` и `values-production.yaml` с плейсхолдерами (`:155-180`).
- **App:** без изменений; внешняя БД поддержана через `DATABASE_*` (`go-app/internal/config/config.go:329-348`).

## Design Premises

| # | Premise | Confirmed by | Class | If wrong |
|---|---|---|---|---|
| P1 | Backup CronJob встроенного PG не создаёт ни одного бэкапа на дефолтном чарте | k3d прогон 2026-10-15, `evidence/k3d-probe-backup-job.txt` (`no pg_hba.conf entry for replication connection`) | measured | если у кого-то бэкапы работают (ручной `pg_hba`) — удаление PVC при апгрейде уничтожит их; смягчение — migration note D4 |
| P2 | WAL archive не пишется: `archive_mode=off`, конфиг чарта не применяется | k3d, `evidence/k3d-probe-effective-config.txt` | measured | то же, что P1 |
| P3 | `postgresql.replicas > 1` даёт независимые primary за одним Service | `postgresql-statefulset.yaml:16` — нет репликации; `postgresql-service.yaml:17-19` селектит все pod'ы `component: database`; `git grep -i 'primary_conninfo\|standby\|recovery.signal'` по `helm/` — только старый guide (PITR), в шаблонах ничего (2026-10-15) | call-path-traced | если бы репликация была — `fail` запретил бы рабочую конфигурацию; её нет |
| P4 | Ни один шаблон/скрипт, кроме удаляемых, не ссылается на `postgresql.backup.*`, `postgresql.podDisruptionBudget.*`, backup PVC и метрику `postgresql_backup_last_success_timestamp_seconds` | `git grep` по репозиторию (без `tasks`, planning, CHANGELOG, guide) 2026-10-15: все ссылки — в удаляемых/правимых шаблонах D1–D3 (`backup-cronjob`, `backup-pvc`, `poddisruptionbudget`, `configmap:46`, `prometheus-rules:126`, `statefulset:104,239`) | call-path-traced | шаблон упадёт на nil — ловит `helm template` в R-тестах |
| P5 | Удаление volume `/backup` и смена тега образа меняют pod template ⇒ один рестарт Postgres при `helm upgrade`; `volumeClaimTemplates` не меняется | `helm template` diff main vs ветка (проверка в implement) | code-read | если `volumeClaimTemplates` изменится — апгрейд упадёт; ловит diff |
| P6 | Рестарт Postgres = кратковременные 500 на ingest; Prometheus/Alertmanager-клиенты переотправляют алерты | `alert_processor.go:520-528` (500 при ошибке БД); переотправка — поведение Prometheus (`resend_delay`), не AMP | code-read / assumed | при клиенте без ретраев — потеря алертов на время рестарта; в migration note |
| P7 | `postgres:16.15` = текущий плавающий `postgres:16` (тот же мажор и патч), опубликован 2026-10-07 — вне карантина | Docker Hub tags; k3d: `SELECT version()` → 16.15 | measured | другая версия — PGDATA 16 совместим в пределах мажора |
| P8 | `pg_dump -Fc` + `pg_restore` в пустую БД до старта AMP даёт рабочий AMP: миграции no-op, firing-алерты и silences восстанавливаются | — | assumed | проверяется прогоном V1; провал — стоп, runbook не публикуется |
| P9 | Пилот на встроенном PG до этой задачи не имел бэкапов, после — тоже; изменения не ухудшают сохранность данных `postgres-data` | P1, P2; `volumeClaimTemplates` не меняется | measured + code-read | — |

## Target Design

Чарт перестаёт обещать то, чего не делает. Встроенный PG остаётся single-instance dev/pilot: число реплик жёстко 1, PDB нет (drain проходит с даунтаймом БД), backup-механизма нет, а попытка его включить падает на рендере с объяснением и ссылкой на runbook. Прод-путь — внешний PostgreSQL ≥ 16 с бэкапами/PITR/HA на стороне оператора (managed или CloudNativePG + barman/S3); его подключение через values — `HELM-EXTERNAL-DATABASE`. Restore — один короткий runbook на `pg_dump -Fc`/`pg_restore`, общий для встроенного и внешнего PG, команды — ровно те, что прошли прогон на k3d. Конфиг Postgres (`PG-CONFIG-NOT-APPLIED`) здесь не чиним: это меняет поведение существующих БД.

### Решения

- **D1. PDB Postgres:** удалить `templates/postgresql-poddisruptionbudget.yaml` и ключ `postgresql.podDisruptionBudget` из `values.yaml`/`values-production.yaml`. Guard не нужен: PDB при одной реплике только вредит, оставшийся ключ в пользовательских values игнорируется (migration note). App-level PDB (`templates/poddisruptionbudget.yaml`) не трогаем.
- **D2. Реплики:** в `postgresql-statefulset.yaml` — `fail`, если `postgresql.enabled` и `int postgresql.replicas != 1`. Текст: встроенный PostgreSQL без репликации, несколько реплик = несколько независимых баз за одним Service; для HA — внешний PostgreSQL (ADR-016). `replicas: 1` в шаблоне берётся из values (ключ остаётся, `default 1` сохраняем).
- **D3. Бэкап:** удалить `templates/postgresql-backup-cronjob.yaml`, `templates/postgresql-backup-pvc.yaml`, backup volume/mount в StatefulSet, блок WAL archive в `postgresql-configmap.yaml`, правила `PostgreSQLWALArchivingFailed` и `PostgreSQLBackupOld`; ключ `postgresql.backup` — из обоих values. Guard: `fail`, если `dig "backup" "enabled" false .Values.postgresql` истинно — текст: встроенный backup удалён (никогда не работал, ADR-016), бэкапы — ответственность оператора, runbook `helm/amp/docs/POSTGRESQL_RESTORE_GUIDE.md`; убрать `postgresql.backup` из values. Guard ловит явное `backup.enabled: true` в пользовательских values при апгрейде (ADR-015: громко на рендере, а не тихо).
- **D4. Backup PVC при апгрейде:** удаляется Helm'ом (аннотации `keep` нет и добавлять её бессмысленно — политика читается из текущего манифеста релиза). Содержимое по P1/P2 — пустые каталоги. Migration note: если бэкапы на этом PVC делались вручную — скопировать до апгрейда.
- **D5. Образ:** `postgres:16` → `postgres:16.15` в StatefulSet и `tests/postgresql-test-connection.yaml`. Мажор не меняется.
- **D6. Аннотации/комментарии StatefulSet:** убрать `tn-97: "HPA cluster mode support (max_connections=250)"` и подобные утверждения, не соответствующие реальности (см. `PG-CONFIG-NOT-APPLIED`); заменить одной аннотацией-описанием «single-instance, dev/pilot, see ADR-016». Аннотации metadata не входят в pod template — рестарта не дают.
- **D7. Values-комментарии:** в блоке `postgresql:` обоих values — шапка «dev/pilot only, single instance, no backups; production: external PostgreSQL (ADR-016, README § PostgreSQL)»; убрать «HA support», «PITR», RTO-обещания. `config:` блок не трогаем по сути (это `PG-CONFIG-NOT-APPLIED`), но добавить строку, что он сейчас не применяется (ссылка на баг).
- **D8. README:** новый раздел `### PostgreSQL` после `### Storage`: поддерживаемые топологии (prod: external PostgreSQL ≥ 16 — managed с PITR или CloudNativePG + barman/S3; dev/pilot: bundled single instance без бэкапов), что PG в критическом пути ingest (БД недоступна ⇒ 500, клиенты должны ретраить), drain ноды с bundled PG = даунтайм БД, ссылка на runbook и ADR-016; external DB values — «coming in HELM-EXTERNAL-DATABASE», до этого — `database:` в `configFile.content`/env. Английский (README публичный).
- **D9. Runbook:** `helm/amp/docs/POSTGRESQL_RESTORE_GUIDE.md` переписать целиком (имя файла сохраняем — на него ссылаются): Backup (`kubectl exec … pg_dump -Fc` в файл на машине оператора + sha256; для внешнего PG — средства провайдера), Restore (scale AMP 0 → `pg_restore --clean --if-exists --no-owner` в пустую/свежую БД → scale AMP обратно → проверки), Verify (ready, counts, silences), Limitations (нет PITR, RPO = интервал дампов, автоматизация расписания — на операторе). Английский. Команды — только прошедшие V1.
- **Не трогаем:** egress-правило `:443` «for backup archiving» в `postgresql-networkpolicy.yaml:59-64` (политика выключена по умолчанию; селектор `podSelector: {}` и так не ведёт наружу) — к пункту NetworkPolicy в TECH-DEBT `HELM-CHART-GAPS`.
- **D10. CHANGELOG:** корневой `CHANGELOG.md` `[Unreleased]` → `### Breaking changes / migration notes` + `### Changed`; `helm/amp/CHANGELOG.md` `[Unreleased]` → `### Removed` / `### Changed`. Migration notes: удалены `postgresql.backup.*` (явный `enabled: true` теперь падает), `postgresql.podDisruptionBudget.*`, backup PVC удаляется при апгрейде (скопировать, если использовался), `replicas != 1` падает, один рестарт Postgres при апгрейде.

## API Contracts

Not applicable — HTTP API AMP не меняется. Контракт чарта (values) — D1–D3, D10.

## Data Model / Migrations

- **DB schema:** не меняется.
- **Kubernetes data:** удаляется backup PVC (`<fullname>-backup`); `postgres-data` (volumeClaimTemplates) не меняется.
- **Rollback / forward-fix:** `helm rollback` на прежний релиз пересоздаёт CronJob, пустой backup PVC и PDB; данные `postgres-data` не затрагиваются ни в одну сторону.

## Component Architecture

- `helm/amp/templates/postgresql-statefulset.yaml` — guards D2/D3, без backup volume, `postgres:16.15`, аннотации D6.
- `helm/amp/templates/postgresql-poddisruptionbudget.yaml` — удалить.
- `helm/amp/templates/postgresql-backup-cronjob.yaml`, `postgresql-backup-pvc.yaml` — удалить.
- `helm/amp/templates/postgresql-configmap.yaml` — без WAL archive блока.
- `helm/amp/templates/postgresql-prometheus-rules.yaml` — без двух backup-правил.
- `helm/amp/templates/tests/postgresql-test-connection.yaml` — `postgres:16.15`.
- `helm/amp/values.yaml`, `helm/amp/values-production.yaml` — D1, D3, D7.
- `helm/amp/README.md` — D8; `helm/amp/docs/POSTGRESQL_RESTORE_GUIDE.md` — D9.
- `helm/amp/tests/render-postgresql.sh` — новый render-тест (write-tests).
- `docs/06-planning/DECISIONS.md` — ADR-016.
- `CHANGELOG.md`, `helm/amp/CHANGELOG.md` — D10.
- Planning (в finalize): BACKLOG P0 → закрытые; `HELM-SINGLE-NODE-DEFAULTS` без пункта про PDB Postgres; TECH-DEBT `HELM-CHART-GAPS` — HA-пункт → ссылка на ADR-016; BACKLOG-идея «ingest без синхронной зависимости от PG».

### ADR-016 (содержание)

- **Контекст:** аудит 2026-10-06 + измерения 2026-10-15 (P1–P3); PG в критическом пути ingest.
- **Решение:** прод = внешний PostgreSQL ≥ 16, бэкапы/PITR/HA — оператор (рекомендации: managed с PITR; CloudNativePG + barman/S3); встроенный — dev/pilot, single instance, без бэкапов, `replicas != 1` и `backup.enabled` падают на рендере; restore — runbook на `pg_dump`/`pg_restore`.
- **Отклонено:** починить встроенный до HA/PITR (меняет поведение существующих БД, по сути свой PG-оператор, 2–3d+); логический бэкап-CronJob в чарте сейчас (бэкап рядом с базой ≠ off-cluster; кандидат в отдельный слайс после `HELM-EXTERNAL-DATABASE`); CNPG-сабчарт (новая зависимость, оператор с CRD — решение оператора кластера, не чарта приложения).
- **Следствие:** breaking values (D10); `HELM-EXTERNAL-DATABASE` становится предпосылкой прод-установки; `PG-CONFIG-NOT-APPLIED` открыт.

## Security Design

- [x] Ownership validation — not applicable.
- [x] Input validation — guards D2/D3 на рендере.
- [x] Sensitive data not logged — runbook не печатает пароль (берёт из Secret в env pod'а через `kubectl exec`); в `evidence/` — без паролей и без дампа (только sha256, размер, counts).
- [x] Rate limiting — not applicable.
- [x] Auth/RBAC — не меняется; удаляемая CronJob использовала SA AMP с `automountServiceAccountToken: false` — ничего не теряем.
- [x] Дамп содержит данные алертов (могут быть чувствительные лейблы/аннотации) — runbook: хранить как секрет.

## Invariants

- [ ] `volumeClaimTemplates` StatefulSet и имя `postgres-data` не меняются (diff рендера main vs ветка).
- [ ] Дефолтные `values.yaml` и `values-production.yaml` (с плейсхолдерами release-gate) рендерятся без ошибок.
- [ ] Lite-профиль и `postgresql.enabled: false` рендерятся как раньше (без новых fail).
- [ ] App-level PDB, Deployment AMP, `DATABASE_*` env не меняются.
- [ ] Никаких изменений в Go-коде.

## Edge Cases

1. Пользовательские values с `postgresql.backup.enabled: true` → `fail` с текстом D3 (не тихое игнорирование).
2. `postgresql.backup.enabled: false` или блок `backup` без `enabled` → рендер проходит.
3. Пользовательские values с `postgresql.podDisruptionBudget.*` → игнорируется, PDB не рендерится.
4. `postgresql.replicas: 0` → `fail` (тоже `!= 1`; для выключения — `postgresql.enabled: false`).
5. `postgresql.enabled: false` + `replicas: 3` / `backup.enabled: true` → guards не срабатывают (StatefulSet не рендерится).
6. `profile: lite` + `postgresql.enabled: true` (дефолт) — поведение как сейчас (StatefulSet рендерится по `postgresql.enabled`); не трогаем.
7. Restore в БД, где AMP уже создал схему (миграции) → `pg_restore --clean --if-exists` перезаписывает; runbook требует scale AMP в 0 до restore.

## Impact Analysis

- **Affected modules:** Helm chart (Postgres-часть), доки чарта, ADR, CHANGELOG.
- **Breaking changes:** да, values-контракт (D10) + удаление backup PVC при апгрейде.
- **New dependencies:** none (пин существующего образа).
- **Risks:** P6/P8 (`assumed`) — см. Rollout; оператор, полагавшийся на «бэкапы чарта», теряет иллюзию бэкапов — именно цель, сообщается в migration notes и README.

## Rollout / Rollback

- **Rollout:** мерж; релиза нет (`PROD-RELEASE-V010` отложен) — изменения попадут в первый релиз с migration notes. Для существующих установок: прочитать migration notes, убрать `postgresql.backup`/`podDisruptionBudget` из values, при ручных бэкапах на backup PVC — скопировать, `helm upgrade` (один рестарт Postgres, краткие 500 на ingest — P6).
- **Rollback:** `helm rollback` — возвращает CronJob/PDB/пустой PVC, данные не трогает.
- **Feature flag:** not applicable.
- **Риск P6 (`assumed`):** клиенты без ретраев теряют алерты на время рестарта Postgres — в migration note («upgrade in a quiet window»).
- **Риск P8 (`assumed`):** проверяется V1 до публикации runbook; провал — стоп и пересмотр D9.

## Verification

- **V1 restore drill (k3d `amp-pg-research`, полный AMP):** локальный образ AMP (`docker build`, `k3d image import`), `helm install` ветки; данные: ≥ 3 firing + ≥ 2 resolved алерта, 1 активный silence (матчит отдельный алерт); `pg_dump -Fc` на хост + sha256; `helm uninstall` + удаление namespace (с PVC); `helm install` заново со scale AMP 0 → `pg_restore` → scale 1; проверки: лог миграций без применённых миграций, `/-/ready` 200, `count(*)` `alerts`/`silences`/`alert_investigations` = исходным, `GET /api/v2/alerts` — firing на месте, `GET /api/v2/silences` — silence active, повторно отправленный алерт под silence — `silenced`, новый алерт принят (2xx). RTO, версия PG, размер дампа — `evidence/restore-drill.md`.
- **V2 upgrade path:** `helm upgrade` с `main`-чарта на ветку на живой установке: backup PVC/CronJob/PDB удалены, `postgres-data` и данные на месте, один рестарт PG; `helm rollback` — PVC данных не тронут. Evidence.
- **V3 render:** `helm/amp/tests/render-postgresql.sh` (в write-tests) + release-gate `helm-values`/`helm-tests`.

## Observability

- **Alerts:** удаляются два backup-правила, которые не могли сработать корректно (`PostgreSQLBackupOld` — на несуществующей метрике; `PostgreSQLWALArchivingFailed` — при выключенном архиве); остальные правила Postgres не меняются.
- **Logs/Metrics:** not applicable.

## Deep Review

- **Mandatory triggers present:** `M` (data integrity: удаление backup PVC, restore-процедура); 3 сигнала `C M R`.
- **Discretionary triggers present:** large diff (удаление ~300 строк шаблонов + переписывание guide ~550 строк).
- **Decision:** required.

## Open Questions

- [ ] P6 (`assumed`) — поведение клиентов при 500 во время рестарта Postgres; закрывается migration note, не кодом.
- [ ] P8 (`assumed`) — restore через `pg_dump`/`pg_restore` даёт рабочий AMP; закрывается V1.
- [ ] Приоритет `PG-CONFIG-NOT-APPLIED` и подъём `HELM-EXTERNAL-DATABASE` до P0 — решение владельца (на эту задачу не влияет).
