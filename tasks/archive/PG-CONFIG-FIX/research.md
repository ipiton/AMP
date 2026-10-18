---
id: PG-CONFIG-FIX
slug: pg-config-fix
stream: Helm
type: bug
artifact: research-pack
status: complete
created_at: 2026-10-17
updated_at: 2026-10-17
---

# Research Pack - встроенный PostgreSQL применяет конфиг чарта

Уровень 3 (full): триггеры — infrastructure (Helm), security (`pg_hba`), performance (`shared_buffers`/`max_connections`/`/dev/shm`), multiple options. В `requirements.md` стоял уровень 2 — поднят по объёму находок.

## 0) TL;DR

- Solving: сервер `postgres:16.15` из чарта должен стартовать с `postgresql.conf`/`pg_hba.conf` чарта — и на свежем PVC, и на уже инициализированном.
- Found: `-c config_file=…` в аргументах сервера работает и на старом PVC, но `hba_file` надо задавать явно; `init.sql` сломан дважды (`ALTER SYSTEM` и view `v_index_usage`); `logging_collector=on` уводит логи из `kubectl logs`; `pg_hba` чарта открывает `replication` суперпользователю из всех RFC1918-сетей.
- Chose: аргументы сервера `config_file` + `hba_file`, ConfigMap каталогом в `/etc/postgresql`; `init.sql` без `ALTER SYSTEM` и без views; `pg_hba` — `<db> <user> all scram-sha-256`, без `replication`; `logging_collector` по умолчанию `off`.
- Risks: рестарт единственного pod'а PG при upgrade; `/dev/shm` 64Mi против параллельных запросов (`assumed`, проверить на k3d); при `maxReplicas: 10` пул AMP выбирает все 250 соединений.
- Next step: -> spec

## 1) Questions

1. Каким механизмом отдать серверу конфиг, чтобы он действовал и без `initdb` (существующий PVC)?
2. Где сервер ищет `hba_file`, если `config_file` вне `PGDATA`?
3. Что ещё в `init.sql` ломает первый старт после удаления `ALTER SYSTEM`?
4. Что меняется для существующих установок и как откатиться?
5. Какой `pg_hba` безопасен и не ломает установки (CIDR pod-сетей, `md5`/`scram`, `replication`)?
6. Логи, память, соединения: что из `postgresql.conf` чарта противоречит дефолтным ресурсам или эксплуатации?

## 2) Findings

Метки: `measured` — docker-прогон `postgres:16.15` под `999:999` с отрендеренным ConfigMap (сырые данные `evidence/docker-probe.txt`); `code-read` — чтение чарта на `main` @ `3a0f723`.

### 2.1 Механизм (Q1, Q2) — `measured`

- Аргументы сервера `-c config_file=/etc/postgresql/postgresql.conf` (ConfigMap смонтирован в `/etc/postgresql`): `config_file`, `max_connections=250`, `shared_buffers=256MB`, `shared_preload_libraries=pg_stat_statements` — из чарта. Entrypoint пробрасывает те же аргументы во временный сервер init-фазы (`docker_temp_server_start "$@"`), поэтому init тоже идёт с конфигом чарта.
- Без `-c hba_file=…` сервер берёт `$PGDATA/pg_hba.conf` (дефолт образа, 7 правил). С `-c hba_file=/etc/postgresql/pg_hba.conf` — 11 правил чарта, `pg_hba_file_rules.error` пуст. Вывод: оба аргумента обязательны.
- Старый PVC (инициализирован раскладкой текущего чарта, воспроизведено: `max_connections=20`, `shared_buffers=400kB`, preload пуст, `auto.conf` пуст): новый запуск — «Skipping initialization», конфиг чарта действует, данные на месте, `pg_stat_statements` начинает отвечать (раньше `must be loaded via shared_preload_libraries`).
- `POSTGRES_INITDB_ARGS="-c config_file=…"` в старом PVC оставил строку `config_file = '/etc/postgresql/postgresql.conf'` в `$PGDATA/postgresql.conf:829` — сервер её игнорирует (параметр только командной строки), вреда нет; новый `config_file` вне `PGDATA` делает `$PGDATA/postgresql.conf` неиспользуемым.
- Откат (старая раскладка на том же томе): стартует, `max_connections=20`, данные на месте — поведение до фикса.
- `code-read`: `checksum/config` (`postgresql-statefulset.yaml:36`) уже рестартует pod при изменении ConfigMap — `subPath`-без-обновления не проблема. `/etc/postgresql` сейчас пустой `emptyDir` (`:93-95`, `:227-228`); `values.postgresql.config` всегда непуст в `values.yaml`, но шаблон обрабатывает и `config: {}` / `null` (ветки `{{- if .Values.postgresql.config }}`).

### 2.2 `init.sql` (Q3) — `measured`

- Строка 56 `ALTER SYSTEM SET pg_stat_statements.max` → `unrecognized configuration parameter` (без preload). Даже с preload `ALTER SYSTEM` дублировал бы `postgresql.conf` в `postgresql.auto.conf`, который перекрывает конфиг чарта навсегда — параметры место в `postgresql.conf`.
- После удаления `ALTER SYSTEM` падает строка 94: view `v_index_usage` читает `tablename`/`indexname` из `pg_stat_user_indexes`, где колонки `relname`/`indexrelname`. Entrypoint запускает psql с `ON_ERROR_STOP=1` ⇒ init прерывается, контейнер выходит с кодом 3. Исправленные колонки — init проходит.
- `code-read`: views `v_database_size`, `v_table_sizes`, `v_index_usage` нигде не используются (grep по чарту, `go-app`, `grafana`). На всех существующих установках их нет (init падал раньше). `pg_stat_statements` используют: алерт `pg_stat_statements_mean_exec_time_seconds` (`postgresql-prometheus-rules.yaml:73`), `helm test` (`tests/postgresql-test-connection.yaml:37`), investigation tool `slow_queries` (`go-app/internal/infrastructure/investigation/tools/database.go:156`, с fallback).

### 2.3 `pg_hba` (Q5) — `measured` + `code-read`

- Дефолт образа: `local all all trust`, `host all all 127.0.0.1/32|::1/128 trust`, `local|host replication all <loopback> trust`, `host all all all scram-sha-256` — вход по паролю с любого адреса в любую БД.
- Чарт: `trust` на socket/loopback; `md5` для `alert_history`/`alert_history` из `10/8`, `172.16/12`, `192.168/16`; `host all alert_history 127.0.0.1/32 md5` (мёртвое — раньше совпадает `trust`); `host replication all <RFC1918> md5`; `reject` остальным.
- `md5` при пароле, хранимом как SCRAM (`password_encryption=scram-sha-256`, `rolpassword=SCRAM-SHA-256$…`), проводит SCRAM-обмен: вход с верным паролем ok, с неверным — `password authentication failed`. Функционально равен `scram-sha-256`, но допускает MD5-хеши, если их кто-то задаст.
- `alert_history` — суперпользователь с `rolreplication=t` (это `POSTGRES_USER`). С правилом чарта `replication=database` из pod-сети проходит (`IDENTIFY_SYSTEM` ok) ⇒ физическая копия всей БД любому pod'у с паролем. Дефолт образа сетевую репликацию не пускает. Бэкап, ради которого правило было (`pg_basebackup`), удалён в ADR-016.
- Вход в другую БД (`postgres`) из pod-сети — `reject` (уже сейчас уже, чем дефолт образа).
- Риск поломки: pod-сеть вне RFC1918 (CGNAT `100.64.0.0/10` в EKS custom networking / некоторых CNI, IPv6-only/dual-stack кластеры) ⇒ после фикса AMP получает `reject` ⇒ ingest 500. Сейчас (дефолт образа) такие кластеры работают. `assumed`: на k3s/k3d pod CIDR `10.42.0.0/16` — проверить на k3d.
- `code-read`: NetworkPolicy PG (`postgresql-networkpolicy.yaml`) пускает на 5432 только pod'ы релиза, но `postgresql.networkPolicy.enabled: false` по умолчанию. Exporter ходит на `localhost` (`trust`), пароль ему не нужен. Probes — через socket (`trust`).

### 2.4 Эксплуатация (Q6)

- `measured`: `logging_collector=on` (дефолт values `loggingCollector: "on"`) ⇒ после старта в stdout контейнера только «redirecting log output to logging collector process», дальше логи в `$PGDATA/pg_log/` на PVC: `kubectl logs` пуст, логи занимают PVC (ротация `1d`/`100MB`, удаления нет). Существующие установки сейчас логируют в stderr (параметр не применялся).
- `code-read`: ресурсы PG `requests 500m/1Gi`, `limits 1000m/2Gi`; `shared_buffers=256MB` укладывается. PVC `10Gi` против `max_wal_size=4GB`/`min_wal_size=1GB` — WAL до ~40% тома.
- _(2026-10-17, spec: проверено — `measured`, `evidence/k3d-shm-probe.txt`: 64M, 2 из 4 одновременных параллельных запросов падают и на `per_gather=4`, и на `2`; `emptyDir` Memory 256Mi снимает. Выбран shm-том.)_ Было `assumed`: `/dev/shm` в pod'е по умолчанию 64Mi (containerd), а `max_parallel_workers_per_gather=4` + `dynamic_shared_memory_type=posix` — параллельные запросы могут упасть `could not resize shared memory segment`. В docker-прогоне `/dev/shm` 3.4G — не показателен.
- `code-read`: бюджет соединений — `max_connections=250`, пул AMP `25`/pod (`go-app/internal/config/config.go:898`), HPA `maxReplicas: 10` (`values.yaml:216`) ⇒ 250 соединений AMP при максимуме, плюс exporter и probes ⇒ на 10 репликах упрётся. При дефолте `minReplicas: 2` — 50. Комментарий в ConfigMap считает 20/pod (устарел).
- `code-read`, вне scope: preStop `su - postgres -c "pg_ctl stop …"` под `runAsUser: 999` без root не работает (`|| true` глушит), остановку делает SIGTERM.

### 2.5 Затронутые места — `code-read`

`templates/postgresql-statefulset.yaml` (env `POSTGRES_INITDB_ARGS`, mounts, `emptyDir`), `templates/postgresql-configmap.yaml` (`postgresql.conf`, `pg_hba.conf`, `init.sql`), `values.yaml:354-356` (NOTE «currently NOT applied»), `templates/NOTES.txt:28,108`, `README.md:105`, `tests/render-postgresql.sh:194` (T6 ждёт 2 упоминания `PG-CONFIG-NOT-APPLIED`), `CHANGELOG.md`.

## 3) Options

`Generation:` parallel (minimal-diff, reversibility, operational). Агенты получили этот research и `evidence/`, друг друга не видели. Сходимость — наблюдение, не подтверждение.

### D1. Механизм — 3/3 сошлись

- **A (выбран).** `args: ["-c","config_file=/etc/postgresql/postgresql.conf","-c","hba_file=/etc/postgresql/pg_hba.conf"]` под `{{- if .Values.postgresql.config }}`, `command` не трогаем (entrypoint сам подставит `postgres` и передаст аргументы во временный init-сервер). ConfigMap — томом в `/etc/postgresql` вместо `emptyDir`; `POSTGRES_INITDB_ARGS` и mounts `*.conf` в `initdb.d` удалить, `init.sql` остаётся в `initdb.d`. В PVC ничего не пишется ⇒ `helm rollback` возвращает всё. Cost/Risk: low.
- **B.** initContainer копирует конфиг в `$PGDATA` / `include` в `$PGDATA/postgresql.conf`. Меняет PVC, откат оставляет следы, лишняя движущаяся часть. Отказ.
- **C.** `ALTER SYSTEM` / `postgresql.auto.conf`. Запись в PVC перекрывает конфиг чарта и переживает rollback. Отказ.
- Расхождение: minimal-diff предлагал два `subPath`-mount'а в `/etc/postgresql`, две другие линзы — каталог целиком. Выбран каталог: меньше строк, нет `emptyDir`, обновление `subPath` не нужно (есть `checksum/config`).

### D2. `init.sql` — 3/3 за удаление `ALTER SYSTEM`, 2/3 за удаление views

- **A (выбран).** Удалить `ALTER SYSTEM`; `pg_stat_statements.{max,track,track_utility}` — в `postgresql.conf` после `shared_preload_libraries`; удалить секцию 5 (три view). Views без потребителей, ни на одной установке их нет; починка дала бы расхождение «свежая vs обновлённая»; любая SQL-ошибка при `ON_ERROR_STOP=1` — CrashLoop первого старта. Cost/Risk: low.
- **B.** Починить колонки `v_index_usage` (`relname`/`indexrelname`, 2 строки, проверено в B/C). Меньше дифф, но мёртвый код и расхождение установок. Отказ (minimal-diff сам допускал A).

### D3. `pg_hba` — 3/3 за адрес `all`, `scram-sha-256`, без `replication`

- **A (выбран).** `trust` на socket/`127.0.0.1`/`::1`; `host <db> <user> all scram-sha-256`; `host all all all reject`. Удалить три `replication`-правила и мёртвое `127.0.0.1 md5`. По адресам не уже дефолта образа ⇒ кластеры с pod CIDR вне RFC1918 (CGNAT `100.64/10`, IPv6/dual-stack) не ломаются; по БД/пользователю уже образа; сетевое сужение — `postgresql.networkPolicy`. Cost/Risk: low.
- **B.** A + ключ `postgresql.hba.clientCidrs` (дефолт `0.0.0.0/0`, `::/0`). Reversibility рекомендовала, operational — опционально, minimal-diff — против: новый публичный контракт (`C`), а сужение уже даёт NetworkPolicy. Отказ; аварийный выход без нового ключа — `postgresql.config: null` (шаблон уже ветвится, вернёт дефолты образа).
- **C.** Оставить RFC1918-список. Тихий регресс: PG здоров, probes зелёные (socket/loopback), AMP получает `pg_hba.conf rejects connection` ⇒ 500 на ingest. Отказ.

### D4. `logging_collector` — 3/3 за `off`

- **A (выбран).** Дефолт `off` в `values.yaml` и в `default` шаблона; `log_destination = 'stderr'` уже стоит. Логи в `kubectl logs`, PVC не растёт; существующие установки и так пишут в stderr. `on` — opt-in.
- **B.** Оставить `on`. `kubectl logs` пуст ровно тогда, когда нужна диагностика `pg_hba`/OOM. Отказ.

### D5. `/dev/shm`, бюджет соединений — 3/3: сначала замер

- **A (выбран).** На k3d: `df -h /dev/shm` в pod'е и параллельный запрос. Если 64Mi и запрос падает `could not resize shared memory segment` — решение в spec между `emptyDir {medium: Memory, sizeLimit}` на `/dev/shm` (operational, как bitnami `shmVolume`; считается в лимит 2Gi) и `max_parallel_workers_per_gather = 2` (reversibility, только конфиг). `max_connections=250` не трогать, комментарий «20/pod» → 25/pod.
- **B.** Сразу добавить shm-том / поднять `max_connections`. Риск не подтверждён, тюнинг — Non-Goal. Отказ.

## 4) Decision

- **Chosen:** D1-A, D2-A, D3-A, D4-A, D5-A.
- **Why:** конфиг действует на старом PVC без `initdb` и полностью откатывается `helm rollback` (`measured`, probe D); в PVC ничего не пишется; `pg_hba` перестаёт пускать сетевую `replication` суперпользователя и не сужает адреса относительно сегодняшнего поведения, поэтому upgrade не даёт тихого `reject`.
- **Risk Profile:** `S R`, Full — без изменений. `C` не добавляется: новых ключей values нет, меняются дефолт `loggingCollector` и тексты `pg_hba`/`init.sql`.

## 5) Spec Inputs

- **Шаблоны:** `postgresql-statefulset.yaml` — `args`, volume `custom-config` → `/etc/postgresql` (каталог, readOnly), `init.sql` по `subPath` в `initdb.d`; убрать `emptyDir` `postgres-config` и `POSTGRES_INITDB_ARGS`. `config: {}`/`null` ⇒ ни `args`, ни mounts. `postgresql-configmap.yaml` — D2-A, D3-A, D4-A, комментарий бюджета соединений.
- **values.yaml:** `loggingCollector: "off"`; убрать NOTE «currently NOT applied» (`:354-356`).
- **Render-тест:** `args` содержат оба `-c`; нет `POSTGRES_INITDB_ARGS`, mount'ов `*.conf` в `initdb.d`, `emptyDir` на `/etc/postgresql`; в `init.sql` нет `ALTER SYSTEM` и `CREATE … VIEW`; в `pg_hba.conf` нет `replication` и `md5`, есть `scram-sha-256`; `logging_collector = off`; `config: null` ⇒ без args. T6 `render-postgresql.sh` — 0 упоминаний `PG-CONFIG-NOT-APPLIED`.
- **k3d (evidence):** (1) свежая установка: `SHOW config_file/hba_file/max_connections/shared_preload_libraries/logging_collector`, `pg_hba_file_rules`, `RESTARTS 0`, AMP 2 реплики → `POST /api/v2/alerts` 200, exporter `pg_up 1`, `kubectl logs` PG непуст после старта; (2) upgrade: установка с `main` → данные → `helm upgrade` на ветку → те же проверки, данные на месте; (3) rollback: `helm rollback` → старт, данные на месте; (4) `/dev/shm` по D5; (5) pod CIDR k3d (`10.42/16`) — вход идёт по `scram-sha-256`.
- **Migration note (CHANGELOG `[Unreleased]`):** upgrade рестартует pod PG (короткий простой ingest); после рестарта `max_connections` 20→250, `shared_buffers` 400kB→256MB, preload `pg_stat_statements`, `min/max_wal_size` 1GB/4GB (до ~40% PVC 10Gi); `pg_hba` — только `<db>/<user>` по SCRAM, без сетевой `replication`, другие БД/пользователи из сети — `reject`; логи по-прежнему в stdout. Проверка — `SHOW …` и отсутствие `pg_hba.conf rejects` в логах AMP. Откат — `helm rollback`; если pod PG не стал Ready и StatefulSet (`OrderedReady`) его не пересоздаёт — удалить pod `<release>-postgres-0` вручную (forced rollback). Аварийный выход без отката чарта — `postgresql.config: null`.
- **Docs:** `NOTES.txt:28,108`, `README.md:105`, `values.yaml:354-356` — снять `PG-CONFIG-NOT-APPLIED`; README — бюджет соединений (25 × реплики AMP + exporter; `alert_history` — суперпользователь, резерв `superuser_reserved_connections` его не защищает).
- **Follow-ups (BACKLOG, не здесь):** бюджет `max_connections` при `maxReplicas: 10` → `HELM-SINGLE-NODE-DEFAULTS`; preStop `su - postgres` под `runAsUser: 999` не работает (P2, новая запись); `alert_history` — суперпользователь, отдельная роль приложения (TECH-DEBT `HELM-CHART-GAPS`).

## 6) References

- `evidence/docker-probe.txt` — замеры A–D этого research.
- `tasks/archive/PROD-POSTGRES-HA-DECISION/research.md` § 2, `evidence/k3d-probe-effective-config.txt` — исходный замер бага на k3d.
- `docs/06-planning/BUGS.md` `PG-CONFIG-NOT-APPLIED`; ADR-016 в `docs/06-planning/DECISIONS.md`.
- docker-library/postgres `docker-entrypoint.sh` (`docker_temp_server_start "$@"`, `ON_ERROR_STOP=1`) — поведение подтверждено прогоном.
