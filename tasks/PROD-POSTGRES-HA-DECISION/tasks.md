---
id: PROD-POSTGRES-HA-DECISION
slug: prod-postgres-ha-decision
stream: Helm
type: feature
status: active
created_at: 2026-10-15
updated_at: 2026-10-15
based_on:
  - requirements.md
  - research.md
  - Spec.md
---

# Implementation Plan: поддерживаемый путь PostgreSQL для прода

**Based on:** requirements.md / research.md / Spec.md
**Date:** 2026-10-15

## Touched Files

- `helm/amp/templates/postgresql-statefulset.yaml` — guards D2/D3, без backup volume, `postgres:16.15`, аннотации D6.
- `helm/amp/templates/postgresql-poddisruptionbudget.yaml` — удалить (D1).
- `helm/amp/templates/postgresql-backup-cronjob.yaml`, `postgresql-backup-pvc.yaml` — удалить (D3).
- `helm/amp/templates/postgresql-configmap.yaml` — без WAL archive блока (D3).
- `helm/amp/templates/postgresql-prometheus-rules.yaml` — без `PostgreSQLWALArchivingFailed`, `PostgreSQLBackupOld` (D3).
- `helm/amp/templates/tests/postgresql-test-connection.yaml` — `postgres:16.15` (D5).
- `helm/amp/values.yaml`, `helm/amp/values-production.yaml` — без `podDisruptionBudget`/`backup` в `postgresql:`, комментарии D7.
- `docs/06-planning/DECISIONS.md` — ADR-016.
- `helm/amp/README.md` — `### PostgreSQL` (D8).
- `helm/amp/docs/POSTGRESQL_RESTORE_GUIDE.md` — runbook (D9), после V1.
- `CHANGELOG.md`, `helm/amp/CHANGELOG.md` — D10.
- `helm/amp/tests/render-postgresql.sh` — новый render-тест (Phase 5).
- `tasks/PROD-POSTGRES-HA-DECISION/evidence/` — рендер-диффы, V1, V2.

## Phase 1: Чарт

> **Wave 1** — независимые правки

- [x] **1.0** Снять базовый рендер `main` для сравнения (дефолты и production с плейсхолдерами release-gate). <!-- verify: helm template amp helm/amp (из git worktree/`git stash`-free копии main) > evidence/render-main-default.yaml и …-production.yaml; файлы непустые -->
- [x] **1.1** Удалить `postgresql-poddisruptionbudget.yaml`; убрать `postgresql.podDisruptionBudget` из обоих values (D1). <!-- verify: helm template … | grep -c 'name: amp-postgres$' в PodDisruptionBudget = 0; git grep 'postgresql.podDisruptionBudget' -- helm/amp/templates — пусто -->
- [x] **1.2** Удалить `postgresql-backup-cronjob.yaml`, `postgresql-backup-pvc.yaml`; из StatefulSet — backup volume и mount; из ConfigMap — WAL archive блок; из PrometheusRule — два backup-правила; из обоих values — `postgresql.backup` (D3). <!-- verify: git grep -n 'postgresql.backup\|database-backup\|postgresql_backup_last_success\|wal_archive' -- helm/amp/templates helm/amp/values*.yaml — пусто -->
- [x] **1.3** Guards в `postgresql-statefulset.yaml` (внутри `if .Values.postgresql.enabled`): `fail` при `int replicas != 1` (D2) и при `dig "backup" "enabled" false .Values.postgresql` (D3), тексты со ссылкой на ADR-016 и runbook. <!-- verify: helm template … --set postgresql.replicas=2 → exit≠0 с текстом про независимые базы; --set postgresql.backup.enabled=true → exit≠0 с текстом про ADR-016; --set postgresql.enabled=false --set postgresql.replicas=3 → exit 0 -->
- [x] **1.4** `postgres:16` → `postgres:16.15` в StatefulSet и `templates/tests/postgresql-test-connection.yaml`; аннотации StatefulSet по D6 (без `tn-97 … max_connections=250`). <!-- verify: helm template … | grep -c 'image: postgres:16.15' = 2; grep -c 'postgres:16$\|postgres:16"' по templates = 0 -->
- [x] **1.5** Комментарии блока `postgresql:` в `values.yaml` и `values-production.yaml` по D7 (dev/pilot, single instance, no backups, ADR-016; `config:` сейчас не применяется — `PG-CONFIG-NOT-APPLIED`). <!-- verify: grep -n -i 'HA support\|PITR\|RTO' helm/amp/values*.yaml — пусто в блоке postgresql -->

> **Wave 2** — depends on Wave 1

- [x] **1.6** Рендер-дифф `main` → ветка: удалены только PDB/CronJob/PVC Postgres, два правила, WAL-строки ConfigMap, backup volume, тег образа, аннотации; `volumeClaimTemplates` идентичен; Deployment AMP и app-PDB не изменились (Spec § Invariants). <!-- depends: 1.0–1.5 | verify: diff evidence/render-main-*.yaml с новым рендером → evidence/render-diff-{default,production}.txt; ручной просмотр по списку; yq '.spec.volumeClaimTemplates' StatefulSet совпадает -->
- [x] **1.7** `helm lint` + `helm template` дефолтов, production (плейсхолдеры release-gate), `profile=lite`, `postgresql.enabled=false`. <!-- depends: 1.1–1.5 | verify: все четыре — exit 0 -->
- [x] **1.8** Коммит `feat(helm)!: drop non-functional bundled PostgreSQL backup and PDB` (шаблоны + values + evidence рендера). <!-- depends: 1.6, 1.7 | verify: git show --stat HEAD — только helm/amp/templates, values*, evidence -->

**Phase verification:** `scripts/release-gate.sh` шаги `helm-deps`, `helm-values`, `helm-rbac`, `helm-tests` — PASS (существующие render-тесты не сломаны).

### Implementation notes (2026-10-15, Phase 1)

- 1.0: полные рендеры `main` (≈7.4k строк) держу в scratchpad, в `evidence/` — только диффы `render-diff-{default,production}.txt`; случайный пароль valkey (генерируется на каждом рендере) в диффах замаскирован.
- 1.3: guard на `replicas` — через `hasKey` + `int`, без `default 1`: иначе `replicas: 0` молча превращался бы в 1 (Edge Case 4). В шаблоне теперь `replicas: 1` литералом — guard гарантирует, что другого значения нет. Guard на backup — `toString (dig "backup" "enabled" false …) == "true"` (ловит и `"true"` строкой, как `ingress.externalAuth`).
- 1.5: в `values-production.yaml` заодно переписан старый «HONESTY NOTE», который обещал «WAL archiving + daily base backups for PITR, and its own PDB». `postgresql.enabled: true` в production values оставлен: внешнюю БД через values подключить пока нельзя (`HELM-EXTERNAL-DATABASE`) — комментарий говорит об этом прямо. Отклонение от Spec: нет.
- 1.6: изменения рендера (default и production одинаковы): удалены PDB `amp-postgres`, CronJob и PVC `amp-postgres-backup`, WAL-строки ConfigMap (⇒ новый `checksum/config`), backup volume/mount, два правила; `postgres:16` → `16.15` (StatefulSet и test-pod); аннотации StatefulSet. Deployment `amp` — тот же sha256, `volumeClaimTemplates` идентичен.
- 1.7: `helm template` — default, production, `profile=lite`, `postgresql.enabled=false` (+ `replicas=3`, `backup.enabled=true`) — exit 0; `replicas=2`, `replicas=0`, `backup.enabled=true` — fail с текстом guard'а; `backup.enabled=false` — проходит. `helm lint` default/production — 0 failed.
- Phase verification: все 8 `helm/amp/tests/render-*.sh` — PASS. Полный release-gate — в Phase 6.

## Phase 2: Restore-прогон и upgrade-путь (V1, V2)

> **Wave 1**

- [x] **2.1** Образ AMP: `docker build -t amp:pg-drill .` (текущий builder), `k3d image import amp:pg-drill -c amp-pg-research`; пересоздать namespace `amp` на кластере `amp-pg-research` начисто (остатки probe из research). <!-- verify: docker exec k3d-amp-pg-research-server-0 crictl images | grep amp:pg-drill -->

> **Wave 2** — V2 сначала: upgrade с main

- [x] **2.2** `helm install` чарта `main` (worktree) с `image.repository=amp image.tag=pg-drill pullPolicy=IfNotPresent`, `postgresql.password` плейсхолдер; дождаться ready; засеять данные: ≥ 3 firing + ≥ 2 resolved алерта, 1 активный silence (матчит отдельный алерт `drill-silenced`). Счётчики `alerts`/`silences`/`alert_investigations` — в evidence. <!-- depends: 2.1 | verify: /-/ready 200; GET /api/v2/silences — 1 active; psql count(*) записан -->
- [x] **2.3** V2: `helm upgrade` на чарт ветки (с удалёнными из values ключами): backup PVC/CronJob/PDB удалены, `postgres-data` тот же PVC (uid), один рестарт PG, данные (counts) те же; затем `helm rollback` и обратно `helm upgrade` — counts те же. <!-- depends: 1.8, 2.2 | verify: evidence/upgrade-path.md — kubectl get pvc/cronjob/pdb до/после, uid PVC, restartCount, counts -->

> **Wave 3** — V1 restore drill

- [x] **2.4** Backup: `kubectl exec amp-postgres-0 -c postgres -- pg_dump -Fc …` в файл на хосте (scratchpad), sha256, размер. <!-- depends: 2.3 | verify: pg_restore --list <dump> | grep -c 'TABLE DATA' > 0; sha256 в evidence -->
- [x] **2.5** Потеря: `helm uninstall` + `kubectl delete ns amp` (PVC уходят), убедиться, что PV удалены. <!-- depends: 2.4 | verify: kubectl get pvc -A | grep amp — пусто -->
- [x] **2.6** Restore: `helm install` ветки с `replicaCount=0`/AMP scale 0 → PG ready → `kubectl cp`/`exec -i` дампа → `pg_restore --clean --if-exists --no-owner -d …` → scale AMP 1. Засечь RTO от начала install до `/-/ready`. <!-- depends: 2.5 | verify: pg_restore exit 0 (или только ожидаемые warnings, записаны) -->
- [x] **2.7** Проверки V1: лог AMP — миграции не применялись; `/-/ready` 200; counts = исходным; `GET /api/v2/alerts` — firing на месте; silence active; повторный POST `drill-silenced` → `status.state=suppressed`; новый алерт → 2xx. <!-- depends: 2.6 | verify: evidence/restore-drill.md — команды, выводы, RTO, версия PG, размер дампа; без паролей -->

**Phase verification:** `evidence/restore-drill.md` и `evidence/upgrade-path.md` заполнены, все проверки зелёные. Провал 2.7 — стоп, пересмотр D9 (Spec P8).

### Implementation notes (2026-10-15, Phase 2)

- 2.2: `helm install --wait` чарта `main` уходит в FAILED по двум причинам вне задачи: valkey-сабчарт в `Init:Error` (`exec /scripts/init.sh: no such file or directory`) и `amp-pvc` вечно `Pending` (потребителя нет). Ставил без `--wait`, ждал `rollout status` StatefulSet/Deployment. Засеяно: алертов 5 (3 firing, 2 resolved), 1 активный silence на `drill-silenced`, investigations 0.
- 2.2: селекторы Service и Deployment `amp` захватывают поды `amp-redis-0` и `amp-postgres-0`. Это уже известный `SERVICE-METRICS-PORT-MISROUTED`. По EndpointSlice порт `http` ведёт только на AMP, так что HTTP-трафик в порядке. Но `port-forward svc/amp`/`deploy/amp` и `logs deploy/amp` попадают в redis. Port-forward делал на под по имени; в runbook — так же.
- 2.3 (V2): rev1 main → rev2 ветка → rollback rev3 → rev4 ветка. Каждый раз: CronJob, PDB и backup PVC удалены, uid `postgres-data` тот же, под PG пересоздан ровно один раз (`postgres:16.15`), строк 5/1/0. Upgrade и rollout PG — 46 с. Для migration notes: upgrade удаляет PVC `<release>-postgres-backup` вместе с содержимым, rollback создаёт его пустым. Данных там нет: base backup ни разу не создавался.
- 2.4–2.6 (V1): дамп 88 797 байт, в нём 17 TABLE DATA; `pg_restore --clean --if-exists --no-owner` → exit 0, предупреждений нет. RTO 56 с от install до `/-/ready`, из них 31 с — старт пода PG.
- 2.7: отклонение формулировки. Состояния `status.state=suppressed` в AMP нет: firing-алерт, совпавший с активным silence, отбрасывается на ingest (`handlers/alerts.go:433`) и не сохраняется. Проверено поведением: `drill-silenced` получает 200, но отсутствует в API и в БД, а контрольный `DrillPostRestore` записан. Миграции — no-op: последняя строка `goose_db_version` от исходного install (09:31), restore был в 09:38.
- Найдено вне задачи: фоновый воркер silence manager не стартует (`limit must be <= 1000, got 10000`); `POST /api/v2/silences/check` → 404.
- Phase verification: `evidence/upgrade-path.md` и `evidence/restore-drill.md` заполнены, все проверки зелёные.

## Phase 3: Документация

- [x] **3.1** ADR-016 в `docs/06-planning/DECISIONS.md` по Spec § ADR-016 (формат ADR-015). <!-- verify: grep -n '^## ADR-016' docs/06-planning/DECISIONS.md -->
- [x] **3.2** `helm/amp/docs/POSTGRESQL_RESTORE_GUIDE.md` — переписать как runbook (D9), команды — из `evidence/restore-drill.md` с плейсхолдерами `<release>`/`<namespace>`; англ. <!-- depends: 2.7 | verify: grep -c 'alert-history' = 0; каждая команда runbook присутствует в restore-drill.md (ручная сверка) -->
- [x] **3.3** `helm/amp/README.md` — `### PostgreSQL` после `### Storage` (D8), ссылки на runbook и ADR-016; англ. <!-- verify: grep -n '^### PostgreSQL' helm/amp/README.md -->
- [x] **3.4** `CHANGELOG.md` `[Unreleased]` (Breaking changes / migration notes + Changed) и `helm/amp/CHANGELOG.md` `[Unreleased]` (Removed/Changed) по D10. <!-- verify: grep -n 'PROD-POSTGRES-HA-DECISION' CHANGELOG.md helm/amp/CHANGELOG.md -->
- [x] **3.5** Коммит `docs: ADR-016 and verified PostgreSQL restore runbook`. <!-- depends: 3.1–3.4 | verify: git show --stat HEAD -->

### Implementation notes (2026-10-15, Phase 3)

- Перед runbook дополнительно прогнал на k3d сценарий A, откат живой установки на дамп: scale 0 → `pg_restore --clean` в непустую БД → scale 1. Счётчики 6→5, миграции не применялись. Проверил и HPA: при Deployment в 0 он ставит `ScalingDisabled` и не мешает. Всё записано в `evidence/restore-drill.md` §5–6. В runbook только проверенные команды. Исключение — раздел про внешний PG: там прямо сказано, что прогона не было.
- 3.3: в таблице `### Storage` исправлен дефолт `postgresql.enabled`: README писал `false`, в values стоит `true`. Иначе новый раздел противоречил бы таблице рядом. Фраза Spec D8 «внешняя БД через env» в README не попала: в чарте нет хука для произвольного env (`extraEnv`), есть только `configFile.content`.
- 3.1: в ADR-016 разведено, что измерено (конфиг, pg_basebackup, PDB) и что следует из шаблона (реплики без репликации).

### Implementation notes (2026-10-15, fix round после deep review @ ebfd2b8)

- R5 и R3, guard: `postgresql.backup` читается через `kindIs "map"`. Скаляр `true` ловится отдельно. `null`, `false` и пустой `backup:` проходят. В текст `fail` добавлен обход для `--reuse-values`. Рендер-кейсы: 11 вариантов, включая `-f <main>/values.yaml` (fail) и тот же файл с `--set postgresql.backup.enabled=false` (pass).
- R4, `NOTES.txt`: убраны HA-обещания. Для встроенного PG выводится предупреждение dev/pilot, для внешнего — ✅. Блок `max_connections` больше не утверждает, что `postgresql.config` применён. В расчёте пул поправлен 20 → 25: это `database.max_connections` по умолчанию, `config.go`.
- R1, R2, R7, R8: перепрогон на k3d, кластер пришлось поднять после рестарта Docker. Записано в `evidence/restore-drill.md` §7:
  - `--reuse-values` с релиза main падает с новой подсказкой; `--reset-then-reuse-values` работает на реальном upgrade;
  - полная проверка дампа ловит обрезанный файл;
  - 3a с `--single-transaction --exit-on-error`: на обрезанном дампе данные целы, на целом восстановлено;
  - 3b с HPA-values пользователя: AMP 0 подов во время restore, после `helm upgrade` HPA вернулся;
  - `dropdb --force` + `createdb` → restore;
  - потеря только PVC при живом релизе закрывается сценарием 3a.
- R6, R12, R13, R17: формулировки в runbook, README, ADR и CHANGELOG. R15 — ссылки BUGS на якоря. R14 — Spec D2 под реализацию. R9 — README без CronJob. R10 — trailing whitespace; `git diff --check main` чистый.
- R11 (lite рендерит встроенный PG) и R16 (`backupBeforeMigration`) — в BUGS и TECH-DEBT на finalize. Строка README про lite поправлена.
- Проверки: 8/8 `render-*.sh` PASS; `helm lint` default и production (с плейсхолдерами тестов) — 0 failed; рендеры default, production и lite — ok.

### Implementation notes (2026-10-15, повторное ревью @ 5632eac)

- Повторное ревью подтвердило фиксы R1–R10, R12–R15, R17. Из новых R18–R24 исправлены:
  - R18: `<fullname>` теперь берётся из StatefulSet `component=database`; у Deployment нет метки `component`;
  - R19: в NOTES шаги 3–4 выводятся только при встроенном PG, «Expected: 250» заменено, предупреждение `PG-CONFIG-NOT-APPLIED` показывается и без HPA;
  - R20: `DUMP` и проверка checksum + `pg_restore -f /dev/null` перед любым restore;
  - R21: удаление мёртвого PVC и пода в 3a, по порядку после scale 0;
  - R23: Spec D3, комментарий в values (25 conns/pod), ссылки TN-97 в NOTES.
- R22 отклонён: `enabled: 1`/`"True"` проходят как принятое поведение, CronJob всё равно не работал; кейсы — в 5.1. R24 (`port-forward svc/` в NOTES) — TECH-DEBT `HELM-CHART-GAPS` в finalize.
- Повторный deep review для R18–R23 не нужен (рекомендация ревьюера). Проверено:
  - NOTES на 4 вариантах: default, без HPA, без PG, lite;
  - lookup `<fullname>` и `sha256 -c` (нормальный и подменённый дамп) — evidence §7.7.

## Phase 4: Deep review

- [ ] **4.1** `/deep-review` (обязателен: `M`, 3 сигнала, большой дифф): guards и тексты, рендер-дифф против инвариантов, удаление PVC и migration notes, runbook против evidence, ADR. <!-- depends: 1.8, 3.5 | verify: jq -r .gate tasks/PROD-POSTGRES-HA-DECISION/review-verdict.json → pass -->

## Phase 5: Tests

- [ ] **5.1** `helm/amp/tests/render-postgresql.sh` по образцу `render-defaults-validate.sh`: дефолты и production — нет PDB/CronJob/backup PVC Postgres, образ `postgres:16.15`, нет `archive_mode`; `replicas=2`, `replicas=0`, `backup.enabled=true` — render fail с ожидаемым текстом; `postgresql.enabled=false` + `replicas=3`/`backup.enabled=true` — рендер проходит; `profile=lite` — рендер проходит; deep review R5/R22: `backup=null`, `backup=false`, пустой `backup:` в values-файле — проходят, `backup=true` (скаляр), `--set-string backup.enabled=true`, main `values.yaml` через `-f` — fail с подсказкой про `--reuse-values`, `backup.enabled=1`/`"True"` — проходят (принятое поведение, R22); NOTES: при встроенном PG нет «HA» и «Expected: 250», при `postgresql.enabled=false` нет шагов 3–4. <!-- depends: 4.1 | verify: bash helm/amp/tests/render-postgresql.sh → все ok; на main (без изменений чарта) — падает (тест не тавтологичен) -->

## Phase 6: Testing

- [ ] **6.1** `scripts/release-gate.sh` полностью (чарт изменён). <!-- depends: 5.1 | verify: все шаги PASS; вывод в evidence/release-gate.txt -->
- [ ] **6.2** `git diff --check main...HEAD`. <!-- depends: 5.1 | verify: пусто -->

## Phase 7: Finalize

- [ ] **7.1** `/finalize`: `DONE.md`; `NEXT.md` (снять из WIP); BACKLOG: P0 → закрытые, из `HELM-SINGLE-NODE-DEFAULTS` убрать пункт про PDB Postgres, идея «ingest без синхронной зависимости от PG»; TECH-DEBT `HELM-CHART-GAPS` — HA-пункт → ADR-016; приоритеты `PG-CONFIG-NOT-APPLIED` / `HELM-EXTERNAL-DATABASE` — по решению владельца; архив workspace. <!-- depends: 6.1, 6.2 | verify: ls tasks/archive/PROD-POSTGRES-HA-DECISION -->
- [ ] **7.2** Убрать k3d-кластер `amp-pg-research`, вернуть kubectl-контекст владельца. <!-- depends: 7.1 | verify: k3d cluster list — нет amp-pg-research; kubectl config current-context — по указанию владельца -->
- [ ] **7.3** `/merge-to-main`; push — по решению владельца. <!-- depends: 7.1 | verify: git log main -1 -->

## Definition of Done

- [ ] All steps are complete or explicitly marked blocked/skipped
- [ ] Success criteria from `requirements.md` are covered
- [ ] Contracts from `Spec.md` are implemented or deviations are recorded
- [ ] Deep review verdict is `pass`
- [ ] Tests for changed behavior are added or updated (`render-postgresql.sh`)
- [ ] Phase checks pass
- [ ] Docs/planning are updated (ADR-016, README, runbook, оба CHANGELOG, planning в finalize)
