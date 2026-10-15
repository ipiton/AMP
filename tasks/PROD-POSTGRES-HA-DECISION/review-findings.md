# Deep Review Findings: поддерживаемый путь PostgreSQL для прода

**Trigger classification:** mandatory (`M`; 3 сигнала `C M R`, tier Full)
**Reviewer perspective:** два независимых агента без доступа к рассуждениям автора и друг к другу:
- Reviewer A: целостность данных, runbook, premises, guards. Воспроизведения делал в своём namespace на k3d `amp-pg-research` и в локальном `postgres:16.15`.
- Reviewer B: контракт values, rollout/rollback/observability, точность документации. Работал только с рендером.

Автор свёл находки и сверил ключевые утверждения с кодом: `deployment.yaml:11-13`, `NOTES.txt`, guard `dig`, `git diff --check main...HEAD`.
**Reviewed at:** 2026-10-15
**Reviewed tree:** feature/prod-postgres-ha-decision @ ebfd2b8. Повторное ревью фиксов (новый независимый агент) — @ 5632eac: R1–R10, R12–R15, R17 подтверждены, новые R18–R24
**Verdict:** pass (см. `review-verdict.json`; первый прогон @ ebfd2b8 — fix_required)

Поле `Status` в каждой находке читает скрипт вердикта: `open` | `fixed` | `deferred` | `rejected`. В скобках указан номер находки у ревьюера.

## Findings

### R1 — Runbook 3b: при дефолтном HPA `--set replicaCount=0` не останавливает AMP, и тот стартует на пустой БД во время `pg_restore` (A-F1, B-2)
- **Severity:** major
- **Location:** `helm/amp/docs/POSTGRESQL_RESTORE_GUIDE.md` § 3b, строка 9 («Every command … was run end to end»); `helm/amp/templates/deployment.yaml:11-13`
- **Issue:**
  - `replicas:` рендерится только при `not autoscaling.enabled`, а по умолчанию HPA включён (`minReplicas: 2`, в production — 3).
  - Воспроизвёл A: `helm install --set replicaCount=0` → `spec.replicas=1`, затем HPA `SucceededRescale … to 2`.
  - AMP применяет миграции к пустой БД и принимает ingest, а `pg_restore --clean` потом удаляет таблицы прямо под ним. Ровно это runbook и запрещает.
  - V1 прошёл только потому, что в drill-values было `autoscaling.enabled=false`. § 6 evidence проверял HPA лишь для 3a.
- **Recommendation:** в 3b `--set autoscaling.enabled=false --set replicaCount=0`. Возврат к обычным values — финальным `helm upgrade` (уже описан). Перепрогнать 3b с дефолтным HPA (V1-bis) и только потом утверждать «run end to end».
- **Disposition:** fix-here
- **Status:** fixed
- **Follow-up:** n/a

### R2 — Проверка дампа не ловит обрезанный файл, а 3a с таким дампом уничтожает живую БД (A-F2)
- **Severity:** major
- **Location:** `POSTGRESQL_RESTORE_GUIDE.md` § 2 (backup и проверка), § 3a/3b (`pg_restore`)
- **Issue:**
  - `pg_restore --list` читает только TOC в начале файла `-Fc`. A обрезал дамп до трети: `--list` даёт exit 0 и тот же счёт `TABLE DATA`.
  - Затем 3a поверх БД на 200 000 строк: exit 1, таблицы пересозданы пустыми. Живые данные потеряны, дамп битый.
  - С `--single-transaction --exit-on-error` тот же дамп даёт exit 1, данные целы.
  - Полное чтение `pg_restore -f /dev/null < dump` обрезку ловит.
  - Runbook не требует проверять exit code `kubectl exec … pg_dump > file`: при обрыве exec-стрима останется частичный файл с «валидным» sha256.
- **Recommendation:**
  - Backup: `set -o pipefail` / проверка exit code `pg_dump`; проверка дампа через `pg_restore -f /dev/null`.
  - Restore: `--single-transaction --exit-on-error`. Перед публикацией проверить, что на полном дампе с этими флагами exit 0 (прогон).
  - Описать, что делать при ненулевом exit.
- **Disposition:** fix-here
- **Status:** fixed
- **Follow-up:** n/a

### R3 — `helm upgrade --reuse-values` с релиза на чарте `main` всегда падает на backup-guard (A-F3, B-1)
- **Severity:** major
- **Location:** `helm/amp/templates/postgresql-statefulset.yaml:6-8`; `CHANGELOG.md` migration note п.1; `helm/amp/CHANGELOG.md`; `helm/amp/README.md` § Upgrading (`--reuse-values`); ADR-016 («явный `backup.enabled: true`»)
- **Issue:**
  - `--reuse-values` переносит coalesced values старого релиза вместе со старыми дефолтами чарта, а в них `postgresql.backup.enabled: true`.
  - A воспроизвёл на helm v4.3.0: `UPGRADE FAILED … postgresql.backup was removed`. B получил то же симуляцией.
  - Оператор ключ не задавал, а совет «Remove postgresql.backup from your values» ему не помогает. При этом README документирует именно `--reuse-values`.
  - `--reset-then-reuse-values` и `--set postgresql.backup.enabled=false` проходят.
- **Recommendation:**
  - Migration notes (оба CHANGELOG), текст `fail` и README § Upgrading: при `--reuse-values` — `--reset-then-reuse-values` или `--set postgresql.backup.enabled=false`.
  - Поправить формулировку ADR-016.
  - Реальный upgrade в testing.
- **Disposition:** fix-here
- **Status:** fixed
- **Follow-up:** n/a

### R4 — `NOTES.txt` обещает HA и «max_connections: 250 (OK)» для встроенного PG (B-3)
- **Severity:** major
- **Location:** `helm/amp/templates/NOTES.txt:23-26`, `:34`, `:45-58`
- **Issue:**
  - Каждый `helm install` печатает «STANDARD PROFILE (HA-Ready…)», «PostgreSQL external storage (HA)», «Production, HA» и «max_connections: 250 (OK)».
  - Это противоречит ADR-016. Реальный `max_connections=20` (`PG-CONFIG-NOT-APPLIED`).
  - Критерий requirements «вводящие в заблуждение HA-обещания убраны» для вывода, который видит каждый оператор, не выполнен.
- **Recommendation:** при `postgresql.enabled` писать «bundled PostgreSQL: single instance, dev/pilot, no backups (ADR-016)». Блок `max_connections` заменить предупреждением, что `postgresql.config` не применяется (`PG-CONFIG-NOT-APPLIED`).
- **Disposition:** fix-here
- **Status:** fixed
- **Follow-up:** n/a

### R5 — Guard `dig` падает невнятной ошибкой, если `postgresql.backup` равен `null` или не map (A-F4, B-4)
- **Severity:** minor
- **Location:** `helm/amp/templates/postgresql-statefulset.yaml:6`
- **Issue:**
  - `postgresql.backup: ~`, пустой `backup:` (типичный остаток после выполнения migration note) или `--set postgresql.backup=false|true` дают `error calling dig: interface conversion: interface {} is nil, not map[string]interface {}`.
  - Строка CHANGELOG «Other leftover keys are ignored» в этом случае неверна.
- **Recommendation:** проверять `kindIs "map"` перед `dig`. Скаляр `true` тоже считать включённым бэкапом. Кейсы — в `render-postgresql.sh` (write-tests).
- **Disposition:** fix-here
- **Status:** fixed
- **Follow-up:** n/a

### R6 — Runbook и ADR: для внешнего PG сказано «works», хотя прогона не было; `--clean` трогает расширения (A-F5)
- **Severity:** minor
- **Location:** `POSTGRESQL_RESTORE_GUIDE.md` § External PostgreSQL; ADR-016 («один для встроенного и внешнего PG»)
- **Issue:**
  - Миграции создают `uuid-ossp` и `btree_gin`. `--clean` эмитит `DROP EXTENSION IF EXISTS`.
  - На managed PG без superuser это ошибки и ненулевой exit.
- **Recommendation:** «should work, not drilled». Для внешнего PG — restore в свежую пустую БД без `--clean`. Проверку на внешнем PG отложить в backlog вместе с `HELM-EXTERNAL-DATABASE`.
- **Disposition:** fix-here (формулировка); проверка — defer-backlog
- **Status:** fixed
- **Follow-up:** BACKLOG `HELM-EXTERNAL-DATABASE` — дополнить в finalize

### R7 — 3a при смене версии AMP после дампа: `--clean` не удаляет объекты из более новых миграций (A-F6)
- **Severity:** minor
- **Location:** `POSTGRESQL_RESTORE_GUIDE.md` § 3a
- **Issue:** таблицы и колонки из миграций новее дампа остаются, а `goose_db_version` откатывается. При старте AMP применит эти миграции повторно поверх существующих объектов. Упадёт ли это, зависит от `IF NOT EXISTS`.
- **Recommendation:** одна строка в 3a: если AMP обновлялся после дампа, пересоздать БД и восстанавливать в пустую.
- **Disposition:** fix-here
- **Status:** fixed
- **Follow-up:** n/a

### R8 — Заголовок 3b обещает и случай потери одного PVC, но команды рассчитаны на снесённый релиз (A-F7)
- **Severity:** minor
- **Location:** `POSTGRESQL_RESTORE_GUIDE.md` § 3b
- **Issue:** при потере только PVC релиз жив: `helm install` упадёт («name still in use»). StatefulSet пересоздаст пустой PVC, и AMP накатит на него миграции.
- **Recommendation:** при потере PVC без потери релиза — шаги 3a. 3b — для потери namespace или кластера.
- **Disposition:** fix-here
- **Status:** fixed
- **Follow-up:** n/a

### R9 — README всё ещё упоминает удалённый backup CronJob (B-5)
- **Severity:** minor
- **Location:** `helm/amp/README.md:155`
- **Issue:** «The Redis StatefulSet and the PostgreSQL backup CronJob share the ServiceAccount…».
- **Recommendation:** убрать упоминание CronJob.
- **Disposition:** fix-here
- **Status:** fixed
- **Follow-up:** n/a

### R10 — `git diff --check main...HEAD` красный (B-6)
- **Severity:** minor
- **Location:** `tasks/PROD-POSTGRES-HA-DECISION/Spec.md:16`; `evidence/render-diff-{default,production}.txt`
- **Issue:** trailing whitespace. Проверка автора смотрела только незакоммиченное.
- **Recommendation:** обрезать хвостовые пробелы; в finalize проверять `git diff --check main...HEAD`.
- **Disposition:** fix-here
- **Status:** fixed
- **Follow-up:** n/a

### R11 — Lite-профиль по умолчанию рендерит встроенный PostgreSQL (B-7)
- **Severity:** minor
- **Location:** `helm/amp/templates/postgresql-*.yaml` (гейт только по `postgresql.enabled`); `helm/amp/values.yaml:302` («Overridden by profile»); `helm/amp/README.md:91`
- **Issue:** `--set profile=lite` рендерит StatefulSet, Service и PVC Postgres, которыми AMP не пользуется. Дефект существовал до ветки.
- **Recommendation:** баг в BUGS. В README-строке дописать «set `false` for the lite profile».
- **Disposition:** defer-bug (README-строка — fix-here)
- **Status:** deferred
- **Follow-up:** BUGS — завести в finalize

### R12 — «PostgreSQL ≥ 16» подано как требование, хотя ничем не подкреплено (B-8)
- **Severity:** minor
- **Location:** `helm/amp/README.md` § PostgreSQL; ADR-016; runbook § External
- **Issue:** обоснования нижней границы нет. Тесты приложения идут на `postgres:15-alpine`.
- **Recommendation:** «supported and tested: PostgreSQL 16».
- **Disposition:** fix-here
- **Status:** fixed
- **Follow-up:** n/a

### R13 — «Senders that retry (Prometheus/Alertmanager do)» неточно для Prometheus (B-9)
- **Severity:** nit
- **Location:** `helm/amp/README.md` § PostgreSQL; `CHANGELOG.md` migration note п.4; runbook § 3
- **Issue:** Prometheus не ретраит упавший батч, а периодически переотправляет активные алерты. Resolved, отвергнутый один раз, может долго не прийти.
- **Recommendation:** «Prometheus re-sends active alerts periodically».
- **Disposition:** fix-here
- **Status:** fixed
- **Follow-up:** n/a

### R14 — Spec D2 расходится с кодом: `replicas: 1` захардкожен (B-10)
- **Severity:** nit
- **Location:** `helm/amp/templates/postgresql-statefulset.yaml:21`; `Spec.md` D2
- **Issue:** поведение эквивалентно, guard стоит раньше. Отклонение записано только в Implementation notes 1.3.
- **Recommendation:** обновить D2 в Spec.
- **Disposition:** fix-here
- **Status:** fixed
- **Follow-up:** n/a

### R15 — Номера строк в BUGS `PG-CONFIG-NOT-APPLIED` устарели (B-11)
- **Severity:** nit
- **Location:** `docs/06-planning/BUGS.md` (`PG-CONFIG-NOT-APPLIED`)
- **Recommendation:** ссылаться на якоря: `POSTGRES_INITDB_ARGS`, mountPath `docker-entrypoint-initdb.d`.
- **Disposition:** fix-here
- **Status:** fixed
- **Follow-up:** n/a

### R16 — Мёртвое обещание бэкапа `migration.backupBeforeMigration` в values (B-12)
- **Severity:** nit
- **Location:** `helm/amp/values.yaml:561`; egress `:443` в `postgresql-networkpolicy.yaml:59-64`
- **Issue:** ни один шаблон ключ не читает. Egress-правило Spec сознательно оставил вне scope.
- **Recommendation:** пункт в TECH-DEBT `HELM-CHART-GAPS`.
- **Disposition:** defer-tech-debt (egress — reject: учтено в Spec «Не трогаем»)
- **Status:** deferred
- **Follow-up:** TECH-DEBT `HELM-CHART-GAPS` — дополнить в finalize

### R17 — Неточности в именах и командах (A-F8)
- **Severity:** nit
- **Location:**
  - `CHANGELOG.md` migration note п.3, ADR-016: `<release>-postgres-backup`;
  - runbook § 1: определение `<fullname>`;
  - runbook § 2: `shasum`, `> amp-dumps.sha256`.
- **Issue:**
  - Настоящее имя PVC — `<fullname>-postgres-backup`.
  - Определение `<fullname>` не учитывает `nameOverride`.
  - `shasum` есть не везде (на Linux — `sha256sum`), а `>` перезаписывает файл с суммами.
- **Recommendation:** исправить имена; `sha256sum`/`shasum -a 256` с дозаписью (`>>`).
- **Disposition:** fix-here
- **Status:** fixed
- **Follow-up:** n/a

### R18 — Команда поиска `<fullname>` в runbook § 1 ничего не находит (повторное ревью)
- **Severity:** minor
- **Location:** `helm/amp/docs/POSTGRESQL_RESTORE_GUIDE.md` § 1
- **Issue:** `get deploy -l …,component=application` пуст: метка `component` стоит только на pod template, а не на Deployment. `-o name` возвращает `deployment.apps/…`. Определение не учитывало `<release>-<nameOverride>`. Команду внёс fix round, в evidence прогона не было.
- **Recommendation:** брать имя StatefulSet `component=database` (`<fullname>-postgres`), прогнать на кластере.
- **Disposition:** fix-here
- **Status:** fixed
- **Follow-up:** n/a

### R19 — `NOTES.txt`: «max_connections не применяется» и рядом «Expected: 250» (повторное ревью)
- **Severity:** minor
- **Location:** `helm/amp/templates/NOTES.txt` (шаги 3–4, блок пула)
- **Issue:** шаг 4 ждёт `maxConnections`. Шаги 3–4 печатаются и при `postgresql.enabled=false`. Предупреждение про config выводится только при HPA.
- **Recommendation:** гейтовать шаги по `postgresql.enabled`, переписать «Expected», вынести предупреждение из-под HPA.
- **Disposition:** fix-here
- **Status:** fixed
- **Follow-up:** n/a

### R20 — Restore-секции не задают `$DUMP` и не сверяют checksum (повторное ревью)
- **Severity:** nit
- **Location:** `POSTGRESQL_RESTORE_GUIDE.md` § 3
- **Recommendation:** `DUMP=<path>`, `sha256sum -c` и `pg_restore -f /dev/null` перед restore.
- **Disposition:** fix-here
- **Status:** fixed
- **Follow-up:** n/a

### R21 — Потеря только PVC: runbook не говорит удалить мёртвый PVC и под (повторное ревью)
- **Severity:** minor
- **Location:** `POSTGRESQL_RESTORE_GUIDE.md` § 3a
- **Issue:** PVC, привязанный к потерянному PV, остаётся. StatefulSet новый не создаст, под зависнет в Pending. В evidence § 7.6 PVC и под удалялись явно.
- **Recommendation:** шаги удаления PVC и пода после scale 0, как в evidence.
- **Disposition:** fix-here
- **Status:** fixed
- **Follow-up:** n/a

### R22 — Guard не ловит `enabled: 1` / `"True"`, которые на main включали CronJob (повторное ревью)
- **Severity:** nit
- **Location:** `helm/amp/templates/postgresql-statefulset.yaml:7`
- **Disposition:** reject (reason: CronJob на этих значениях не работал, вреда нет; поведение фиксируется кейсами в задаче 5.1)
- **Status:** rejected
- **Follow-up:** tasks.md 5.1

### R23 — Spec D3, комментарий в values и ссылки TN-97 в NOTES отстали от реализации (повторное ревью)
- **Severity:** nit
- **Location:** `Spec.md` D3; `helm/amp/values.yaml` (комментарий `postgresql.config`); `NOTES.txt` Documentation
- **Disposition:** fix-here
- **Status:** fixed
- **Follow-up:** n/a

### R24 — NOTES советует `port-forward svc/<fullname>`, хотя селектор Service захватывает postgres и redis (повторное ревью)
- **Severity:** nit
- **Location:** `helm/amp/templates/NOTES.txt` (шаг 5)
- **Issue:** дефект был до ветки (`SERVICE-METRICS-PORT-MISROUTED`), и NOTES расходится с runbook § 1.
- **Disposition:** defer-tech-debt
- **Status:** deferred
- **Follow-up:** TECH-DEBT `HELM-CHART-GAPS` — дополнить в finalize

## Premises (Reviewer A)

| # | Статус |
|---|---|
| P1 | верна, `measured` заслужен (для дефолтного pg_hba) |
| P2 | верна, `measured` |
| P3 | верна, `call-path-traced` |
| P4 | верна (`git grep`) |
| P5 | верна; класс фактически `measured` (V2, render-diff) |
| P6 | `assumed`, вынесена в migration note |
| P7 | верна (16.15, карантин пройден) |
| P8 | `measured` только при `autoscaling.enabled=false`; для дефолтных values 3b не доказан → R1 |
| P9 | для чарта верна; runbook вносит свой риск → R2 |

Линза M, данные: `volumeClaimTemplates`, `selector`, `serviceName` и `podManagementPolicy` не меняются. Recreate или orphan `postgres-data` исключён. Upgrade, rollback и повторный upgrade данные не трогают (V2 + render-diff обоих ревьюеров). Новых деструктивных путей в чарте нет.

## Defer to testing

- Реальный `helm upgrade --reuse-values` с релиза на чарте `main` после фикса R3.
- 3b с дефолтным HPA после фикса R1 (V1-bis).
- Runbook с `--single-transaction --exit-on-error` на полном дампе после фикса R2.

## Anti-Pattern Check

- [x] Self-audit was not treated as a substitute for independent review.
