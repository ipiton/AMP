---
id: PROD-POSTGRES-HA-DECISION
slug: prod-postgres-ha-decision
stream: Helm
type: feature
artifact: research-pack
status: complete
created_at: 2026-10-15
updated_at: 2026-10-15
---

# Research Pack - поддерживаемый путь PostgreSQL для прода

## TL;DR

- Встроенный PostgreSQL чарта хуже, чем «single primary без failover»: **кастомный конфиг не применяется вообще**, **бэкап не сработал ни разу**, PITR и restore guide — фикция. Измерено на k3d.
- PostgreSQL — в **критическом пути приёма алертов**: БД недоступна ⇒ алерт не сохраняется и не маршрутизируется (500), AMP не стартует. Потеря/недоступность БД = тишина алертинга.
- Решение (вариант A, все три независимые линзы сошлись): **прод — только внешний PostgreSQL** (managed или оператор вроде CloudNativePG), встроенный StatefulSet — dev/pilot; сломанный backup-механизм убрать из дефолтов и закрыть guard'ом, PDB Postgres не рендерить, `postgresql.replicas != 1` — `fail`; один проверенный restore-runbook на `pg_dump -Fc`/`pg_restore` с выносом дампа за пределы кластера.
- Починка применения конфига (`archive_mode`, `max_connections`, `pg_hba`, `init.sql`) — **отдельный баг** `PG-CONFIG-NOT-APPLIED`: меняет поведение существующих установок и требует своей проверки.
- Следствие для планирования: без `HELM-EXTERNAL-DATABASE` поддерживаемый прод-путь через чарт не собрать — предлагается поднять его до P0 следующим.

## Questions

1. Какие данные AMP держит в PostgreSQL и насколько они критичны?
2. Работает ли сейчас встроенный PG так, как заявляют values и доки (конфиг, WAL archive, бэкап)?
3. Что именно блокирует drain и есть ли смысл в PDB вообще?
4. Какой путь объявить поддерживаемым для прода, что делать со встроенным?
5. Чем и как проверять restore?
6. Что ломается у существующих установок при `helm upgrade`?

## Findings

### 1. Данные и критичность (`code-read`, весь `go-app/`)

- Standard-профиль: `alerts` — источник правды истории, firing-алерты поднимаются в память на старте (`internal/application/service_registry.go:489-581`); `silences` — запись сначала в БД, восстановление на старте (`:583-625`), синхронизация реплик через Redis с перечитыванием из БД (`:906,1067-1108`); `alert_investigations` (`:2165`). Ряд таблиц мёртвый (`alert_classifications`, `alert_publishing_history`, `filter_rules`, `publishing_targets`, `system_metrics`, config/template-таблицы — нет production call site).
- nflog / dedup-state / group timers — в Redis с fallback в память (`infrastructure/grouping/redis_notify_log.go`, `service_registry.go:1898,1982,1204-1212`).
- **Ingest зависит от PG синхронно:** dedup читает/пишет PG (`core/services/deduplication.go:226-238,300,384`); ошибка БД ⇒ «alert persistence failed», алерт не маршрутизируется, POST 500/207 (`core/services/alert_processor.go:520-528`, `handlers/alerts.go:441-479`). Старт падает без PG (миграции, `service_registry.go:1126-1136`); `/-/ready` красный при недоступной БД (`service_registry_health.go:46-70`).
- Lite-профиль: SQLite для алертов, silences/nflog в памяти (+ файловые снапшоты при `storage.path`), PG нет.
- Внешняя БД кодом поддерживается: `database:` / `DATABASE_*` (`internal/config/config.go:329-348,890-903`, `ssl_mode=require` по умолчанию). Чарт ставит `DATABASE_*` только для встроенного PG (`helm/amp/templates/deployment.yaml:94-116`); values `externalDatabase.*` нет — это `HELM-EXTERNAL-DATABASE`.

### 2. Фактическое состояние встроенного PG (`measured`, k3d v1.35.5+k3s1, 2026-10-15)

Только ресурсы с `component: database|database-backup` из `helm template` на дефолтных values + `postgresql.password`. Сырые данные: `evidence/k3d-probe-effective-config.txt`, `evidence/k3d-probe-backup-job.txt`.

- Конфиг из ConfigMap монтируется в `/docker-entrypoint-initdb.d/{postgresql.conf,pg_hba.conf}` (`postgresql-statefulset.yaml:91-97`), а entrypoint образа исполняет оттуда только `*.sh`/`*.sql`; `/etc/postgresql` — пустой `emptyDir` (`:225`); `POSTGRES_INITDB_ARGS="-c config_file=…"` (`:82`). Итог: `config_file` = дефолтный `$PGDATA/postgresql.conf`, `archive_mode=off`, `max_connections=20` (чарт хочет 250), `shared_buffers=400kB`, `pg_hba` — дефолт образа (нет `replication` из сети pod'ов).
- `init.sql` падает на строке 56 (`ALTER SYSTEM SET pg_stat_statements.max` без preload) — первый старт контейнера завершается ошибкой, после рестарта init пропускается ⇒ `init.sql` применён частично (`RESTARTS 1`).
- Backup CronJob (`pg_basebackup --wal-method=stream`) падает всегда: `FATAL: no pg_hba.conf entry for replication connection`. Ретраи `OnFailure` оставляют пустые каталоги `base/<ts>/` с одним `backup.log`. `wal_archive/` пуст.
- `max_connections=20` против пула AMP 25 на pod (`config.go:897`) и `minReplicas: 2` HPA по умолчанию (`values.yaml:215`) — под нагрузкой исчерпание соединений ⇒ ошибки записи ⇒ 500 на ingest. Значение 20 измерено на k3d; на другом окружении initdb может выбрать иное (`assumed` для прод-нод).
- Образ `postgres:16` без patch-пина (получен 16.15).

### 3. PDB и реплики (`code-read`)

- `postgresql-poddisruptionbudget.yaml`: `minAvailable: 1` по умолчанию, `podDisruptionBudget.enabled: true` в `values.yaml` и `values-production.yaml:93-95` ⇒ при одной реплике `kubectl drain` не может выселить pod.
- `postgresql-statefulset.yaml:16` `replicas: {{ .Values.postgresql.replicas }}` без какой-либо репликации; Service выбирает все pod'ы `component: database` (`postgresql-service.yaml:17-19`) ⇒ `replicas: 2` даёт **два независимых primary за одним Service**, записи AMP расходятся по двум базам. PDB имеет смысл только при реальной репликации, которой нет ⇒ PDB Postgres бессмыслен при любом `replicas`.

### 4. Backup PVC и апгрейд (`code-read`)

- `postgresql-backup-pvc.yaml` без `helm.sh/resource-policy: keep`; при перестании рендериться Helm его удалит. По замеру содержимое — только пустые каталоги от неудачных ретраев (WAL archive никогда не писался, `archive_mode=off`); ручные файлы оператора не исключены.
- RWO backup PVC монтируют и StatefulSet (`:242`), и CronJob — на многонодовом кластере CronJob-pod привязан к ноде Postgres.
- `volumeClaimTemplates` (`postgres-data`) неизменяем — ни один вариант его не трогает.
- Init-путь (`initdb.d`, `POSTGRES_INITDB_ARGS`) на уже инициализированном PVC не действует: любая починка конфига через init сработала бы только на новых установках.

### 5. Restore guide (`code-read`)

`helm/amp/docs/POSTGRESQL_RESTORE_GUIDE.md` — 551 строка, 37 упоминаний старого имени `alert-history`; шаг 2 удаляет PVC, на который монтируется restore Job шага 4; `kubectl wait job/postgresql-restore-*` (glob не работает); опирается на `base.tar.gz` + WAL archive, которых не бывает. Выполнить нельзя.

## Options

Generation: parallel — 3 независимых read-only агента (линзы `minimal-diff`, `reversibility`, `operational`) на одном наборе фактов, без доступа к выводам друг друга; слияние — здесь. Все три выбрали A; совпадение — наблюдение, не подтверждение (общий входной набор фактов).

- **A. Внешний PG — единственный прод-путь; встроенный — dev/pilot, честно без бэкапов.** PDB Postgres не рендерится; `replicas != 1` — `fail`; backup CronJob/PVC/WAL archive — по умолчанию выключены, включение — `fail` с отсылкой к ADR; пин `postgres:16.x`; restore guide → короткий проверенный runbook на `pg_dump -Fc`/`pg_restore`; README — «Supported PostgreSQL topologies». Поведение PG на существующих PVC не меняется, откат — `helm rollback`. ~0.75–1d.
- **B. A + рабочий логический бэкап в чарте** (CronJob `pg_dump` по обычному TCP, ретенция, алерт на `lastSuccessfulTime`). Дамп по-прежнему рядом с базой — не off-cluster; S3-выгрузка — новая поверхность (креды, values). ~1–1.25d без S3, 2–3d с S3 ⇒ слайсинг.
- **C. Починить встроенный PG целиком** (конфиг через `args`, `pg_hba` с replication, basebackup, PITR). При первом рестарте существующей установки меняется поведение: `max_connections=250`/`shared_buffers` могут не влезть в лимит памяти (OOM), новый `pg_hba` может запереть приложение, `archive_mode=on` с упавшей архивацией копит WAL в PVC данных ⇒ диск ⇒ PG падает ⇒ ingest 500. По сути собственный PG-оператор — противоречит non-goal. 2–3d+.

## Decision

**A.** Основания:

- измерено: у встроенного StatefulSet нет ни одного работающего бэкапа и не применяется конфиг — заявленная HA/PITR-история вводит оператора в заблуждение; для системы алертинга ложная уверенность хуже отсутствия функции;
- A — единственный вариант, при котором поведение PG на существующих PVC не меняется и откат — одна команда;
- прод-путь (внешний PG) уже поддержан кодом через `DATABASE_*`; не хватает только values чарта — запланированный `HELM-EXTERNAL-DATABASE` (~0.5d);
- B (логический бэкап в чарте) — кандидат в отдельный слайс после `HELM-EXTERNAL-DATABASE`, если появится спрос на встроенный PG в пилотах; C отклоняется в ADR.

## Spec Inputs

- **ADR-016** в `DECISIONS.md`: прод = внешний PostgreSQL ≥ 16 (managed с PITR или CloudNativePG + barman/S3 — рекомендации, не шаблоны); встроенный StatefulSet — dev/pilot, single-instance, без поддерживаемых бэкапов; бэкапы/PITR/HA — ответственность оператора; PG в критическом пути ingest; отклонены B (как часть этой задачи) и C с причинами.
- **Чарт:**
  - `fail`, если `postgresql.enabled && postgresql.replicas != 1` (текст: нет репликации, два primary разведут данные);
  - PDB Postgres не рендерить (удалить шаблон или жёсткий guard; values-ключ — убрать с migration note, решить в spec);
  - `postgresql.backup.enabled` по умолчанию `false` в `values.yaml` и `values-production.yaml`; `true` — `fail` со ссылкой на ADR-016 и runbook;
  - backup PVC: решить в spec — удаление при апгрейде (содержимое по замеру пустое) с migration note vs `keep`; WAL-архивные строки конфига гейтятся тем же `backup.enabled` и так не применяются;
  - пин `postgres:16.<patch>` (проверить дату тега против карантина 7 дней), только внутри мажора 16;
  - убрать «HA support», RTO/RPO-обещания и подобное из комментариев values.
- **Доки:** `helm/amp/README.md` — «Supported PostgreSQL topologies» + ссылка на ADR; `POSTGRESQL_RESTORE_GUIDE.md` → короткий runbook на `pg_dump -Fc`/`pg_restore`, команды — ровно те, что прошли прогон.
- **Restore-прогон (k3d, полный AMP):** локальная сборка образа + `k3d image import`; данные: firing/resolved алерты, активный silence; `pg_dump -Fc` на хост (sha256); `helm uninstall` + удаление namespace/PVC; чистый install, `pg_restore` до старта AMP (scale 0); проверки — миграции no-op, `/-/ready` 200, count `alerts`/`silences` совпадают, firing в памяти (`GET /api/v2/alerts`), silence глушит повторно отправленный алерт, новый алерт принимается; RTO и версия PG — в `evidence/`. Плюс `helm rollback` на прежний чарт не трогает PVC данных.
- **Planning:** BUGS `PG-CONFIG-NOT-APPLIED` (конфиг/`pg_hba`/`init.sql`, `max_connections=20` vs пул 25 × 2 реплики) — приоритет по решению владельца; `HELM-EXTERNAL-DATABASE` → P0 следующим (предложение владельцу); TECH-DEBT `HELM-CHART-GAPS` — пункт про HA закрыть ссылкой на ADR-016; из `HELM-SINGLE-NODE-DEFAULTS` убрать пункт про PDB Postgres; follow-up-идея — ingest без синхронной зависимости от PG (BACKLOG, вне scope).
- **Risk Profile:** `C M R` подтверждается; `S` не добавляется.

## References

- `tasks/PROD-POSTGRES-HA-DECISION/evidence/k3d-probe-effective-config.txt`, `evidence/k3d-probe-backup-job.txt`
- `helm/amp/templates/postgresql-{statefulset,configmap,backup-cronjob,backup-pvc,poddisruptionbudget,service}.yaml`, `helm/amp/values.yaml`, `helm/amp/values-production.yaml`
- `helm/amp/docs/POSTGRESQL_RESTORE_GUIDE.md`
- `go-app/internal/application/service_registry.go`, `go-app/core/services/{deduplication,alert_processor}.go`, `go-app/internal/config/config.go`
- `docs/06-planning/DECISIONS.md` ADR-015 (стиль fail-loud в чарте); BACKLOG `HELM-EXTERNAL-DATABASE`, `HELM-SINGLE-NODE-DEFAULTS`; TECH-DEBT `HELM-CHART-GAPS`
- docker-library/postgres entrypoint: `/docker-entrypoint-initdb.d` исполняет `*.sh`, `*.sql`, `*.sql.gz` и т. п.; прочие файлы игнорируются
