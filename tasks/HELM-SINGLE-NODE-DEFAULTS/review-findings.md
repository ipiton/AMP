# Deep Review Findings: дефолты чарта под одну ноду, кеш на Valkey

**Trigger classification:** mandatory (`M`: при upgrade сбрасываются данные кеша; профиль `R M`, tier Full) + discretionary (новый паттерн: перенос данных shell-скриптом в init-контейнере)
**Reviewer perspective:** два независимых агента. У них не было доступа к рассуждениям автора, и друг друга они не видели.
- **Reviewer A** (миграция и откат):
  - init-скрипт прогнан в `busybox:1.36` под uid 999 на фикстурах от `redis:7-alpine` (7.4.11), `valkey/valkey:8.1.10-alpine` и `valkey/valkey:9.0-alpine` (9.0.6);
  - откат и migration notes;
  - Design Premises и их классы;
  - рендер `main` (со скачанным сабчартом) против ветки, неизменяемые поля StatefulSet.
- **Reviewer B** (корректность и `R`):
  - `helm lint`/`template`, NOTES через `--dry-run=client` в трёх режимах, все `render-*.sh`;
  - бюджет памяти и соединений;
  - правила алертов;
  - сверка документации с кодом, устаревшие упоминания по репозиторию.

Кластер k3d ревьюеры не меняли. Находки сведены автором, пересечения объединены.

Автор перепроверил:
- **R2** — лог AMP на `k3d-single-node`: `NOAUTH Authentication required` → `falling back to in-memory cache`. Там же найден R26 (`evidence/cache-noauth.txt`).
- **R3, R6** — процедура восстановления и новый init прогнаны на тех же фикстурах; Redis 7.4 загружает данные Valkey (`evidence/init-fixtures-review.txt`).

**Reviewed at:** 2026-10-19
**Reviewed tree:** feature/helm-single-node-defaults @ 420228d
**Verdict:** fix_required @ 420228d. Все fix-here закрыты в `e610dc5`; повторная проверка автором (§ Re-check) → pass.

## Findings

### R1 — HPA-сирота после upgrade масштабирует до 10 подов против `max_connections=100`, а migration note говорит обратное
- **Severity:** major
- **Location:** `CHANGELOG.md` (Breaking changes, п. 1); `helm/amp/values.yaml` (`autoscaling`, `resources`, `postgresql.config.maxConnections`); `helm/amp/README.md` (Sizing)
- **Issue:**
  - HPA от старого чарта (`keep`) остаётся с прежним spec: 2–10 реплик, CPU 70%, память 80%.
  - Утилизация считается от requests, а они упали в 5 раз. Порог масштабирования — ~70m CPU на под вместо 350m.
  - 4 пода × 25 соединений исчерпывают `max_connections=100` встроенного PG ⇒ `500` на ingest.
  - Note утверждал «scales back to `minReplicas`». Совет «keep autoscaling» молчал о requests.
  - Масштабирование не прогонялось вживую; это арифметика по рендеру и `evidence/hpa-upgrade-off.txt` C. (Reviewer A F1, Reviewer B 1.)
- **Recommendation:**
  - Удаление HPA — обязательный шаг того же окна.
  - NOTES предупреждает о сироте.
  - Для autoscaling — поднять requests.
- **Disposition:** fix-here
- **Follow-up:** A2 — проверить предупреждение NOTES на кластере (`tasks.md` 5.3)
- **Resolution:** `e610dc5`
  - CHANGELOG п. 1 переписан: сирота масштабирует до 10, удалить в том же окне, иначе 500.
  - NOTES через `lookup` печатает предупреждение и команду удаления. Проверено `helm upgrade --dry-run=server` на `k3d-single-node`, где сирота есть.
  - `values.yaml` и README: цели HPA — проценты от requests.

### R2 — на дефолтах AMP не авторизуется в своём Valkey и работает на in-memory кеше
- **Severity:** major
- **Location:**
  - `helm/amp/templates/deployment.yaml:175` (`REDIS_PASSWORD` только при `cache.auth.enabled`, по умолчанию `false`);
  - `templates/redis-statefulset.yaml` (`requirepass` из сгенерированного Secret всегда);
  - доки: `helm/amp/README.md` (Standard Profile), `CHANGELOG.md` п. 3, ADR-017 «Следствие».
- **Issue:**
  - Подтверждено на k3d (`evidence/cache-noauth.txt`): `NOAUTH Authentication required` → `Redis cache unavailable, falling back to in-memory cache`.
  - Баг существовал до задачи.
  - Документация ветки при этом утверждала, что replicas делят nflog и таймеры через Valkey. Значит, рекомендация «`autoscaling.enabled: true`» даёт реплики без общего дедупа, а «один повтор уведомлений» на дефолтах определяется рестартом AMP, а не переносом данных. (Reviewer B 2.)
- **Recommendation:**
  - Баг — отдельной задачей: прокинуть `<fullname>-redis-secret` в AMP, когда `cache.host` указывает на свой StatefulSet.
  - До фикса — честные формулировки.
- **Disposition:** defer-bug (проводка пароля) + fix-here (формулировки)
- **Follow-up:** BUGS `AMP-CACHE-NOAUTH` (finalize); A2 — лог AMP о кеше
- **Resolution:** `e610dc5`
  - README: состояние «meant to live in Valkey», ссылка на баг и строку лога.
  - CHANGELOG п. 3 и ADR-017 «Следствие» дополнены.

### R3 — процедура восстановления исходных данных Redis не работала
- **Severity:** major
- **Location:** `CHANGELOG.md` п. 6; Spec § Data Model (Rollback)
- **Issue:**
  - К моменту восстановления Valkey уже записал свои `appendonlydir` и `dump.rdb`.
  - Предписанный `mv` перезаписывал `dump.rdb` и падал на `appendonlydir` (`Directory not empty`).
  - Redis 7.4 с `appendonly yes` загрузил бы AOF Valkey, а не исходные данные.
  - Проверено прогоном. (Reviewer A F2.)
- **Recommendation:**
  1. Scale до 0.
  2. Удалить то, что записал Valkey.
  3. Вернуть файлы из бэкапа.
  4. Откатить релиз (`--force-conflicts` при SSA-конфликте).
- **Disposition:** fix-here
- **Follow-up:** A3 — процедура на кластере (`tasks.md` 5.4)
- **Resolution:** `e610dc5` — CHANGELOG п. 6 и Spec переписаны. Прогон процедуры: `evidence/init-fixtures-review.txt` § restore — Redis 7.4.11 загрузил исходные ключи, ключа, записанного Valkey, нет.

### R4 — `helm upgrade --reuse-values` молча оставляет Redis 7.4, HPA и 250 соединений
- **Severity:** minor
- **Location:** `CHANGELOG.md` (Breaking changes, вводный абзац)
- **Issue:** `--reuse-values` берёт дефолты старого чарта. Лицензионная цель ADR-017 не достигается, migration notes неприменимы. Вывод из кода Helm; соседняя запись PG-CONFIG-FIX описывает тот же механизм. (Reviewer A F9, Reviewer B 3.)
- **Recommendation:** указать `--reset-then-reuse-values`.
- **Disposition:** fix-here
- **Follow-up:** n/a
- **Resolution:** `e610dc5` — вводный абзац note.

### R5 — устаревший `helm/amp/charts/` возвращает сабчарт; гейт этого не ловит
- **Severity:** minor
- **Location:** `scripts/release-gate.sh` (`step_helm_deps`); `docs/CI.md`; `helm/amp/tests/render-*.sh` (блок `helm dependency build`)
- **Issue:**
  - Helm рендерит всё из `charts/` и без `dependencies`. Каталог в gitignore, поэтому у любого checkout после прошлого `helm dependency build` снова есть `amp-valkey`. Проверено рендером.
  - Без `Chart.lock` шаг проходил даже при вернувшихся `dependencies`.
  - `values-small.yaml` не входил в гейт.
  - (Reviewer A F6, F12; Reviewer B 4, 17.)
- **Recommendation:** падать на непустом `charts/` без зависимостей и на `dependencies` без lock; добавить шаг `helm-small`; строка для контрибьюторов.
- **Disposition:** fix-here (гейт, доки); блоки `helm dependency build` в тестах — в write-tests
- **Follow-up:** `tasks.md` 4.4
- **Resolution:** `e610dc5`
  - `step_helm_deps` проверен на трёх случаях: чисто → 0; stale `charts/` → 1; `dependencies` без lock → 1.
  - Добавлен `run_step "helm-small"`.
  - `docs/CI.md`, `helm/amp/CHANGELOG.md`, root CHANGELOG п. 5.

### R6 — init переносил AOF, который Valkey может загрузить
- **Severity:** minor
- **Location:** `helm/amp/templates/redis-statefulset.yaml` (`config-init`)
- **Issue:**
  - После отката на Redis 7.4 и повторного upgrade на томе `dump.rdb` v12 и AOF base v11.
  - Скрипт переносил всё, хотя Valkey с `appendonly yes` читает только AOF и стартует с данными.
  - Итог — лишний сброс кеша. Проверено прогоном. (Reviewer A F5.)
- **Recommendation:** решать по каждому артефакту.
- **Disposition:** fix-here
- **Follow-up:** тест на фикстуре «после отката» — `tasks.md` 4.4
- **Resolution:** `e610dc5`
  - Функция `rdb_v12`; `dump.rdb` и `appendonlydir` переносятся раздельно.
  - Матрица `evidence/init-fixtures-review.txt`:
    - r74 → оба;
    - rb → только `dump.rdb`, Valkey 8.1.10 загрузил AOF (k, k2);
    - v81 и v90 не тронуты;
    - aofonly / rdbonly / empty;
    - повторный запуск — no-op.

### R7 — AMP не переподключается к кешу, который был недоступен на старте
- **Severity:** minor
- **Location:** `go-app/internal/application/service_registry.go:1204-1212`; Spec A2
- **Issue:**
  - Upgrade одновременно рестартует AMP, Valkey и PG. Если AMP стартует раньше Valkey, он до конца жизни процесса на in-memory.
  - A2 проверяла только ingest 200, а он не отличает кеш от фолбэка.
  - Гонка не воспроизводилась. (Reviewer A F7.)
- **Recommendation:** проверка лога и `rollout restart` в note и в A2; reconnect — баг.
- **Disposition:** fix-here (note, A2) + defer-bug (reconnect)
- **Follow-up:** BUGS `AMP-CACHE-NO-RECONNECT` (finalize); `tasks.md` 5.3
- **Resolution:** `e610dc5` — CHANGELOG п. 3 (абзац «After the upgrade…»), Spec A2.

### R8 — бюджет соединений считал `superuser_reserved_connections` резервом, но AMP ходит под суперпользователем
- **Severity:** minor
- **Location:** `helm/amp/values.yaml`, `values-small.yaml`, `templates/postgresql-configmap.yaml`, `templates/NOTES.txt`, `README.md`, `CHANGELOG.md` п. 1 и 4
- **Issue:**
  - `POSTGRES_USER` (= `postgresql.username`) — bootstrap-суперпользователь, поэтому «100 − 10 = 90 for clients» неверно.
  - Формулы NOTES (`maxReplicas × 25 ≥ max`) и README (`+ 10`) расходились.
  - В values-small: 2 × 25 + 1 + 10 = 61 > 60.
  - (Reviewer B 6.)
- **Recommendation:** одна формула, честная модель.
- **Disposition:** fix-here
- **Follow-up:** тест NOTES — `tasks.md` 4.4
- **Resolution:** `e610dc5`
  - Везде `maxReplicas × 25 + 5`; NOTES предупреждает при `> maxConnections`. Проверено: при `maxReplicas` 10 предупреждение есть, при 3 — нет.
  - Комментарии переписаны.

### R9 — `values-small.yaml` нельзя наложить на существующий релиз; WAL на томе 5Gi
- **Severity:** minor
- **Location:** `helm/amp/values-small.yaml`; `README.md`
- **Issue:**
  - `volumeClaimTemplates` неизменяемы, поэтому upgrade с этим файлом получит `Forbidden`. Вывод из API, не прогонялось.
  - `min_wal_size 1GB` / `max_wal_size 4GB` на томе 5Gi.
  - (Reviewer B 5, Reviewer A F14.)
- **Recommendation:** «new installs only»; WAL под том.
- **Disposition:** fix-here
- **Follow-up:** n/a
- **Resolution:** `e610dc5` — шапка файла и README; `minWalSize: 256MB`, `maxWalSize: 1GB`.

### R10 — лимиты памяти PG не измерены при полном числе backend'ов
- **Severity:** minor
- **Location:** `helm/amp/values-small.yaml` (комментарий «Within the 512Mi limit»); `values.yaml` (`postgresql.resources`)
- **Issue:**
  - Для values-small: 64MB + 60 × 4MB + `/dev/shm` до 256Mi > 512Mi в худшем случае.
  - Дефолтный 1Gi при 100 backend'ах тоже не измерен. (Reviewer B 7.)
- **Recommendation:** смягчить комментарий; замер в приёмке, иначе TECH-DEBT.
- **Disposition:** fix-here (комментарий) + замер в testing (`tasks.md` 5.4), без него — defer-tech-debt
- **Follow-up:** `tasks.md` 5.4
- **Resolution:** `e610dc5` — комментарий: «sized for light load; not measured with all 60 backends busy».

### R11 — откат пересоздаёт сломанный сабчарт, `amp-pvc` и HPA с `keep`
- **Severity:** minor
- **Location:** `CHANGELOG.md` п. 6
- **Issue:**
  - `helm rollback` на ревизию `main` возвращает `<release>-valkey` (Init:Error); `--wait` завершится FAILED.
  - HPA снова станет сиротой после следующего upgrade.
  - Вывод из манифестов. (Reviewer A F8.)
- **Recommendation:** сказать об этом в п. 6.
- **Disposition:** fix-here
- **Follow-up:** A3
- **Resolution:** `e610dc5`

### R12 — перенос включается по имени репозитория образа, а не по содержимому
- **Severity:** minor
- **Location:** `helm/amp/templates/redis-statefulset.yaml` (`contains "valkey"`)
- **Issue:**
  - Зеркало Valkey под другим именем (air-gapped `registry.local/cache`) пропускает перенос и уходит в crashloop на v12.
  - Обратный случай: путь со словом `valkey`, где лежит Redis, — данные переносятся зря.
  - Проверено рендером. (Reviewer A F3.)
- **Recommendation:** ключ `valkey.migrateRedis74Data` или проверка версии бинаря.
- **Disposition:** fix-here (документировать правило). Ключ отклонён: новый флаг без пользователя, а оператор с зеркалом и так задаёт образ явно (Simplicity Gate).
- **Follow-up:** n/a
- **Resolution:** `e610dc5` — комментарий у `valkey.image`, CHANGELOG п. 3, ADR-017 «Следствие».

### R13 — «остаться на своём Redis» давал несуществующий образ; фолбэк тега `7-alpine`
- **Severity:** minor
- **Location:** `CHANGELOG.md` п. 3; `templates/redis-statefulset.yaml` (`image`)
- **Issue:**
  - Совет менял только `repository`, получался `redis:8.1.10-alpine`; `docker manifest inspect` — нет такого тега.
  - Пустой тег давал `7-alpine`, вопреки ADR-017.
  - (Reviewer A F4, Reviewer B 13.)
- **Recommendation:** задавать `repository` и `tag` вместе; фолбэк убрать.
- **Disposition:** fix-here
- **Follow-up:** тест «пустой тег → ошибка рендера» — `tasks.md` 4.4
- **Resolution:** `e610dc5` — `required "valkey.image.tag is required"` (проверено), CHANGELOG п. 3.

### R14 — метки `volumeClaimTemplates` содержат версию чарта
- **Severity:** minor
- **Location:** `helm/amp/templates/redis-statefulset.yaml` (`volumeClaimTemplates.metadata.labels` ← `amp.labels`)
- **Issue:**
  - В этом диффе неизменяемые поля StatefulSet не меняются: md5 у `main` и ветки совпадает.
  - Но первый бамп `version`/`appVersion` изменит `helm.sh/chart` в шаблоне PVC, и `helm upgrade` получит `Forbidden`.
  - Было до задачи; по спецификации k8s, не прогонялось. (Reviewer A F15.)
- **Recommendation:** в VCT только selectorLabels + component; проверить и StatefulSet PG.
- **Disposition:** defer-bug
- **Follow-up:** BUGS `HELM-VCT-CHART-LABELS` (finalize)

### R15 — неверная цифра «было» в CHANGELOG
- **Severity:** minor
- **Location:** `helm/amp/CHANGELOG.md`, `CHANGELOG.md` (Changed)
- **Issue:** 1.6 vCPU / 2.1Gi — это рендер `main` с одной репликой AMP; с двумя от HPA — ~2.1 vCPU / 2.6Gi. (Reviewer B 9.)
- **Recommendation:** исправить.
- **Disposition:** fix-here
- **Follow-up:** n/a
- **Resolution:** `e610dc5`

### R16 — в PrometheusRule зашит текст «384MB limit»
- **Severity:** minor
- **Location:** `helm/amp/templates/redis-prometheusrule.yaml:46,102`
- **Issue:** с `values-small` maxmemory 192mb; выражения верны, текст нет. (Reviewer B 8.)
- **Recommendation:** брать из values.
- **Disposition:** fix-here
- **Follow-up:** `tasks.md` 4.4
- **Resolution:** `e610dc5` — `{{ .Values.valkey.settings.maxmemory }}` (рендер с values-small: `of maxmemory (192mb)`).

### R17 — пробелы в evidence; `git diff --check` красный
- **Severity:** minor
- **Location:** `tasks/HELM-SINGLE-NODE-DEFAULTS/evidence/`
- **Issue:**
  - «Redis 7.4 читает RDB v11 от Valkey» помечено measured, а в `valkey-rdb-compat.txt` прогона нет.
  - Exporter с Valkey в evidence нет.
  - Хвостовые пробелы в `idle-top.txt`, `hpa-upgrade-off.txt`.
  - (Reviewer B 11.)
- **Recommendation:** дописать; очистить пробелы.
- **Disposition:** fix-here
- **Follow-up:** exporter (`redis_up 1`) — A1 (`tasks.md` 5.2)
- **Resolution:** `e610dc5`
  - Откатная совместимость: `evidence/init-fixtures-review.txt` § start — Redis 7.4.11 на данных Valkey 8.1.10, GET k/k2 → v/v2.
  - Пробелы убраны, `git diff --check` чистый.

### R18 — `amp-pvc` назван «never-bound»; смена `lite` → `standard` теперь удаляет SQLite
- **Severity:** minor
- **Location:** `templates/pvc.yaml`; `CHANGELOG.md` п. 5; `helm/amp/CHANGELOG.md`
- **Issue:**
  - С `volumeBindingMode: Immediate` PVC был Bound.
  - Раньше PVC переживал смену профиля, теперь удаляется вместе с SQLite.
  - Из рендера, не прогонялось. (Reviewer B 12, Reviewer A F11.)
- **Recommendation:** точная формулировка + строка про `lite` → `standard`.
- **Disposition:** fix-here (doc)
- **Follow-up:** n/a
- **Resolution:** `e610dc5`

### R19 — неверная оценка размера каталога-бэкапа
- **Severity:** nit
- **Location:** `CHANGELOG.md` п. 3; Spec § Data Model
- **Issue:** «не больше maxmemory» — на деле RDB дважды (dump и base) плюс хвост AOF. (Reviewer A F10, Reviewer B 10.)
- **Recommendation:** переформулировать, предложить `du`.
- **Disposition:** fix-here
- **Follow-up:** n/a
- **Resolution:** `e610dc5`

### R20 — устаревшее «Connection pool: 50 conns/pod»
- **Severity:** nit
- **Location:** `helm/amp/values.yaml` (шапка `valkey`)
- **Issue:** дефолт `redis.pool_size` — 10 (`go-app/internal/config/config.go:909`). (Reviewer B 14.)
- **Recommendation:** исправить.
- **Disposition:** fix-here
- **Follow-up:** n/a
- **Resolution:** `e610dc5`

### R21 — NOTES: строка кеша безусловная, у `lite` захардкожены ресурсы
- **Severity:** nit
- **Location:** `helm/amp/templates/NOTES.txt`
- **Issue:** «✅ Valkey L2» при Dragonfly / `valkey.replicas: 0` / `cache.enabled: false`; у `lite` написано «250m CPU, 256Mi». (Reviewer B 15.)
- **Recommendation:** условие и `dig`.
- **Disposition:** fix-here (заголовок «Distributed» оставлен — вне задачи)
- **Follow-up:** `tasks.md` 4.4
- **Resolution:** `e610dc5`
  - Три варианта строки кеша; production → `External cache L2 (dragonfly…)`.
  - У `lite` requests из values.

### R22 — `values-dev.yaml` расходится с новыми дефолтами
- **Severity:** nit
- **Location:** `helm/amp/values-dev.yaml:22-27,38-40`
- **Issue:** requests dev (500m/512Mi) выше дефолтов; `persistence.size` в standard ничего не делает. (Reviewer B 16.)
- **Recommendation:** выровнять отдельной правкой.
- **Disposition:** defer-backlog
- **Follow-up:** BACKLOG (finalize)

### R23 — ссылки на `tasks/archive/HELM-SINGLE-NODE-DEFAULTS/evidence/`
- **Severity:** nit
- **Location:** `helm/amp/values.yaml` (resources), `README.md` (Sizing)
- **Issue:** путь появится после архивирования. (Reviewer B 18.)
- **Recommendation:** проверить на finalize.
- **Disposition:** reject — путь правильный после finalize; проверка в `tasks.md` 6.1
- **Follow-up:** n/a

### R24 — init: нечитаемый файл роняет init; перенос может разойтись по двум каталогам
- **Severity:** nit
- **Location:** `templates/redis-statefulset.yaml` (`config-init`)
- **Issue:**
  - Под чартом все файлы uid 999, а fail-closed здесь правильнее.
  - Разрыв между двумя `mv` разносит бэкап по двум каталогам — неудобство, не потеря.
  - (Reviewer A F13.)
- **Recommendation:** оставить.
- **Disposition:** reject
- **Follow-up:** n/a

### R25 — инвариант Spec «разница рендера production — только удалённые объекты» неточен
- **Severity:** nit
- **Location:** `tasks/HELM-SINGLE-NODE-DEFAULTS/Spec.md` § Invariants
- **Issue:** при `replicas: 0` меняются и шаблон StatefulSet `-redis`, и аннотация HPA. (Reviewer A, премиссы.)
- **Recommendation:** уточнить.
- **Disposition:** fix-here
- **Follow-up:** n/a
- **Resolution:** `e610dc5`

### R26 — (вне диффа) silence manager не стартует: `limit must be <= 1000, got 10000`
- **Severity:** minor
- **Location:** go-app, silence manager initial cache sync (лог `Initial cache sync failed`)
- **Issue:**
  - На каждом старте AMP пишет `silence manager start failed: initial cache sync failed: invalid filter parameters: limit must be <= 1000, got 10000`.
  - Работает без GC/stats-воркера silences.
  - Найдено автором в том же логе, что и R2 (`evidence/cache-noauth.txt`). К задаче не относится.
- **Recommendation:** отдельный баг.
- **Disposition:** defer-bug
- **Follow-up:** BUGS `SILENCE-SYNC-LIMIT` (finalize)

### R27 — последствия сброса кеша неполные: классификация LLM
- **Severity:** nit
- **Location:** `CHANGELOG.md` п. 3
- **Issue:** при включённой LLM-классификации L2-кеш классификации тоже теряется — повторные вызовы LLM. (Reviewer A, премиссы.)
- **Recommendation:** одна фраза.
- **Disposition:** fix-here
- **Follow-up:** n/a
- **Resolution:** `e610dc5`

## Premises (сводка Reviewer A)

| Посылка | Вердикт |
|---|---|
| Сабчарт `valkey` никем не используется | Верно: `git grep`, рендер `REDIS_ADDR`, CI. Оговорка — R5 |
| Valkey 8.1.10 работает с `redis.conf` чарта | Верно (фикстуры на отрендеренном конфиге) |
| Valkey не читает RDB v12 | Верно |
| Redis 7.4 читает v11 от Valkey | Верно; прогон теперь в evidence (R17) |
| Потеря кеша = nflog/группы/таймеры; silences в PG | В основном верно; + LLM-кеш (R27); на дефолтах кеш не используется (R2) |
| Valkey 9.x не пишет `REDIS0012+` (было assumed) | Измерено: 9.0.6 пишет `VALKEY080` → класс `measured` |
| Пул AMP 25 на под; прод не использует встроенные PG/кеш | Верно |

## Re-check (автор, `e610dc5`)

Повторное независимое ревью не запускалось. В `e610dc5` правки — формулировки, скрипт init и шаг гейта, и каждая проверена прогоном:
- `helm lint` чисто для default / small / dev / production (с плейсхолдерами) / lite.
- Рендер-тесты: 9/10 зелёные. В `render-postgresql.sh` T6 остаются только ожидаемые ассерты 250 → 100 (шаг 4.2).
- NOTES:
  - default;
  - production: внешний кеш, HPA 3–20;
  - HPA on: предупреждение 255 > 100;
  - `maxReplicas` 3: нет предупреждения;
  - lite;
  - `--dry-run=server` на кластере с сиротой: предупреждение с командой.
- Init — матрица из 7 фикстур, плюс старт Valkey после переноса, плюс процедура восстановления (`evidence/init-fixtures-review.txt`).
- `step_helm_deps` — три случая; `bash -n scripts/release-gate.sh`.
- `git diff --check` чистый.
