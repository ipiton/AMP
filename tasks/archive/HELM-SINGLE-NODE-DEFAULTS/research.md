---
id: HELM-SINGLE-NODE-DEFAULTS
slug: helm-single-node-defaults
stream: operations
type: feature
artifact: research-pack
status: complete
created_at: 2026-10-19
updated_at: 2026-10-19
---

# Research Pack - дефолты чарта под одну ноду

## 0) TL;DR

- **Задача:** чарт с дефолтными values должен вставать на одну ноду. Кеш — только Valkey: Redis не подходит по лицензии (решение владельца 2026-10-19).
- **Что нашли:**
  - стек в простое потребляет ~47m CPU / ~88Mi, а запрошено 1,6 vCPU / 2,1Gi;
  - «второй Redis» — сломанный сабчарт `valkey`, в который AMP не ходит;
  - `redis:7-alpine` = Redis 7.4.11 (RSALv2/SSPL);
  - Valkey 8.1/9.0 не читает данные Redis 7.4 (RDB v12);
  - HPA с `resource-policy: keep` остаётся сиротой после выключения.
- **Что выбрали:** убрать сабчарт; собственный StatefulSet перевести на `valkey/valkey:8.1-alpine`; несовместимые данные при старте отодвигать init-контейнером; HPA выключить по умолчанию и снять `keep`; requests и бюджет PG — под 1 реплику.
- **Риски:**
  - при upgrade теряется nflog, поэтому firing-группы один раз уйдут повторно;
  - у существующих релизов HPA-сирота удаляется руками;
  - на Helm 4 возможен SSA-конфликт `.spec.replicas`.
- **Tier:** сигналы `R M` → **Full**, `deep-review` обязателен.
- **Следующий шаг:** spec.

## 1) Questions

1. Откуда второй Redis и можно ли его убрать без потери функциональности?
2. Сколько стек реально потребляет — какие requests обоснованы?
3. Каким должен быть бюджет соединений встроенного PG при выключенном HPA?
4. Что делает `helm upgrade` при переходе HPA on → off?
5. (Добавлен владельцем) Как перейти с Redis на Valkey и что будет с данными существующих установок?

## 2) Findings

### Второй Redis (Q1)

- В `Chart.yaml` есть зависимость `valkey` 2.1.3 (groundhog2k) с `condition: cache.enabled` ⇒ в каждом `standard` рендерится Deployment `<release>-valkey`, NetworkPolicy и PVC 5Gi. Собственный StatefulSet `<fullname>-redis` (`templates/redis-statefulset.yaml`) гейтится только `profile: standard`. Оба читают один и тот же блок `valkey:` в values.
- AMP ходит только в `<fullname>-redis` (`cache.host`, `templates/deployment.yaml:172-175`).
- Сабчарт не стартует: `Init:Error` (BUGS `VALKEY-SUBCHART-INIT-ERROR`). На k3d это воспроизвелось 2026-10-19.
- В Go сабчарт не упоминается. `scripts/release-gate.sh:126-148` (шаг `helm-deps`) существует только ради него. `charts/` не в git.

### Потребление (Q2) — `evidence/idle-top.txt`

- Условия: k3d, 1 нода, нагрузка 10 алертов каждые 15 с (все ответы 200), 10 срезов `kubectl top`.
- Результат (avg / max):

  | Контейнер | CPU | Память |
  |---|---|---|
  | amp | 8m / 14m | 15Mi / 21Mi |
  | postgres | 24m / 39m | 54Mi / 55Mi |
  | pg-exporter | 1m / 2m | 3Mi / 6Mi |
  | valkey | 9m / 12m | 5Mi / 5Mi |
  | redis-exporter | 5m / 8m | 11Mi / 13Mi |

- Итого ~47m / ~88Mi.
- Ориентир аудита — 260m / 640Mi — с запасом покрывает простой. Память PG в простое не показательна: `shared_buffers` 256MB ещё не тронуты, их надо закладывать в request.
- Текущий рендер дефолтов: requests 1600m / 2176Mi на одну реплику AMP; HPA 2–10; PVC: PG 10Gi, redis 5Gi, valkey-сабчарт 5Gi, `amp-pvc` 1Gi. Последний вечно `Pending` (BUGS `AMP-PVC-PENDING-STANDARD`).

### Бюджет PG (Q3)

- Пул AMP — 25 соединений на под (`go-app/internal/config/config.go:898`), чарт его не переопределяет.
- `maxConnections: 250` и память PG 1Gi/2Gi посчитаны под `maxReplicas: 10`.
- Прод по ADR-016 работает на внешнем PG: `values-production.yaml` ставит `postgresql.enabled: false`, HPA 3–20. Встроенный PG — только dev/pilot.
- NOTES уже предупреждает о бюджете при включённом HPA (`templates/NOTES.txt:45+`).

### HPA on → off (Q4) — `evidence/hpa-upgrade-off.txt`

- `templates/hpa.yaml:12` — `helm.sh/resource-policy: keep`; аннотация пришла с `e1d520e` (переименование чарта), обоснования нет.
- Helm берёт `keep` из манифеста **предыдущего** релиза. Поэтому у существующих релизов HPA остаётся сиротой при любом новом чарте и возвращает Deployment к `minReplicas` (воспроизведено).
- Без `keep` в предыдущей ревизии Helm удаляет HPA.
- Helm 4 по умолчанию применяет манифесты через server-side apply. Если HPA-контроллер уже записал `.spec.replicas` (subresource `scale`), upgrade с `replicas:` в Deployment падает: `conflict with "k3s" ... .spec.replicas`. Конфликт зависит от тайминга, обходится `--force-conflicts`.

### Valkey (Q5) — `evidence/valkey-rdb-compat.txt`

- `redis:7-alpine` резолвится в Redis 7.4.11 (RSALv2/SSPLv1). `deploy/e2e-ha/docker-compose.yml:15` использует тот же тег.
- `valkey/valkey:8.1-alpine` (8.1.10) на пустом томе стартует с конфигом чарта без правок. `redis-server`/`redis-cli` в образе — симлинки на `valkey-*`, поэтому `command` и probes шаблона работают.
- На данных Redis 7.4 и 8.1, и 9.0 падают: `Can't handle RDB format version 12`. Pod уйдёт в crashloop.
- Что AMP держит в Redis (разбор кода, file:line в отчёте агента; итоги):
  - durable — только nflog (`grouping/redis_notify_log.go`), группы и таймеры (`redis_group_storage.go`, `redis_timer_storage.go`);
  - silences и алерты лежат в PostgreSQL;
  - остальное (inhibition, кеш классификации, кеши API, leader/heartbeat) — восстановимый кеш или эфемерное.
- Последствия потери данных:
  - firing-группы один раз уйдут повторно;
  - resolved по алертам, погасшим во время простоя, могут не уйти;
  - окно, в котором inhibition ещё не знает источников подавления.
- Если Redis недоступен при старте, AMP не падает, а переходит на in-memory (`service_registry.go:1204-1212`).
- В проде (`values-production.yaml`) кеш — внешний Dragonfly и `valkey.replicas: 0`, так что проблема данных его не касается.

### Unknowns

- Какое `maxmemory` и `requests` ставить для Valkey в `values-small.yaml`. Это решается в spec.

## 3) Options

`Generation:` single-pass. Решение обратимо: это дефолты чарта, откат — один `helm rollback` или values. Направление задано требованием владельца (Valkey), реальные развилки — только в миграции данных. Параллельные линзы этого не окупают.

### Кеш: Option A — убрать сабчарт, свой StatefulSet на образ Valkey

- **Pros:**
  - остаётся один инстанс;
  - сохраняются exporter, ServiceMonitor, PrometheusRule и дашборд чарта;
  - уходит внешний helm-репозиторий и шаг `helm-deps` в гейте;
  - заодно закрывается `VALKEY-SUBCHART-INIT-ERROR`;
  - `cache.host` не меняется.
- **Cons:**
  - удаление Deployment и PVC сабчарта при upgrade (данных там нет: под не стартовал);
  - ключи values, которые читал только сабчарт, станут мёртвыми.
- **Cost/Risk:** low.

### Кеш: Option B — оставить сабчарт, убрать свой StatefulSet

- **Pros:** образ Valkey уже из коробки.
- **Cons:**
  - сабчарт сломан;
  - теряются exporter, правила и дашборд;
  - меняется `cache.host`;
  - внешняя зависимость остаётся.
- **Cost/Risk:** high.

### Миграция данных: Option M1 — init-контейнер отодвигает несовместимые данные

- `config-init` (busybox) читает заголовок `REDIS00NN` в `dump.rdb` и `appendonlydir/*.base.rdb`. Если NN > 11, файлы переносятся в `/data/redis-pre-valkey-<ts>/`, и Valkey стартует с пустым набором.
- **Pros:**
  - upgrade без ручных шагов;
  - старые файлы целы, rollback на Redis-образ может их вернуть;
  - идемпотентно.
- **Cons:**
  - логика в shell, нужен тест;
  - данные (nflog) всё равно теряются.
- **Cost/Risk:** low–medium.

### Миграция данных: Option M2 — только migration note (scale 0, удалить PVC)

- **Pros:** нет кода.
- **Cons:** upgrade без ручного шага уходит в crashloop, а `helm upgrade --wait` — в FAILED.
- **Cost/Risk:** medium (операционный).

### Миграция данных: Option M3 — перенос ключей из Redis 7.4 в Valkey

- **Cons:** DUMP/RESTORE несут ту же версию RDB, остаётся только поштучный SCAN + чтение по типам с временным Redis-сайдкаром. Сложность несоразмерна кешу, где durable — только nflog с TTL ≤ repeat_interval.
- **Cost/Risk:** high. Отклонено.

## 4) Decision

- **Chosen:** кеш — **A**; данные — **M1** плюс migration note; HPA — `enabled: false` по умолчанию, снять `keep`, migration note.
- **Why:**
  - **A** выполняет требование владельца (Valkey) и одновременно убирает лишний инстанс, то есть ~250m/256Mi и PVC 5Gi;
  - **M1** не превращает обычный `helm upgrade` в аварию. Потерю nflog (один повтор уведомлений) нельзя избежать без M3, поэтому её фиксируем в CHANGELOG, а не прячем;
  - HPA-сирота у существующих релизов неизбежна, так как `keep` читается из старой ревизии. Лечится одной командой в migration note, а снятие `keep` сейчас делает будущие переключения корректными;
  - встроенный PG — только dev/pilot (ADR-016), поэтому бюджет считается от одной-двух реплик. Кто включает HPA, получает предупреждение в NOTES (уже есть).

## 5) Spec Inputs

- **Signals:** `R M` → Full. `M` — данные кеша на существующем PVC отбрасываются при upgrade, nflog теряется.
- **API/contracts:** ключи values не удаляются. Меняются значения: `autoscaling.enabled`, `valkey.image.*`, ресурсы, `postgresql.config.*`. Решить в spec, что делать с ключами `valkey.*`, которые читал только сабчарт (найти их) и с мёртвым `cache.resources`: удалить (тогда добавить `C`) или оставить с пометкой в TECH-DEBT.
- **Chart:**
  - убрать `dependencies` из `Chart.yaml`, удалить `Chart.lock`;
  - шаг `helm-deps` в `scripts/release-gate.sh` — убрать или оставить no-op для чартов без зависимостей;
  - тег образа — конкретный (`8.1-alpine` или пин патча), с учётом карантина 7 дней.
- **Requests (ориентир):** сумма ≈ 260–300m / 512–640Mi; память PG ≥ `shared_buffers` + запас. Точные числа и `maxConnections` (25 × реплики + exporter + `superuserReservedConnections`) — в spec.
- **`values-small.yaml`:** явный пример под одну ноду (без PDB, меньшие PVC, `maxmemory` Valkey). Решить, нужен ли он отдельно, если дефолты уже single-node; BACKLOG его требует как пример.
- **`AMP-PVC-PENDING-STANDARD`:** в каждой single-node установке висит Pending PVC; фикс — одно условие в `pvc.yaml`. Предлагаю включить в задачу.
- **`deploy/e2e-ha/docker-compose.yml`:** тот же `redis:7-alpine` → Valkey (одна строка, по требованию лицензии).
- **Rollout/rollback (migration notes):**
  1. кеш: файлы Redis 7.4 переносятся в `/data/redis-pre-valkey-*`, один раз уходят повторные уведомления по firing-группам;
  2. HPA: `kubectl delete hpa <fullname>` после upgrade, если HPA не нужен (сирота от `keep`), либо `autoscaling.enabled: true`;
  3. Helm 4: при `conflict ... .spec.replicas` повторить с `--force-conflicts`;
  4. рестарт PG из-за новых параметров;
  5. rollback на старый чарт: Redis 7.4 не прочитает RDB, записанный Valkey (проверить в spec или k3d), — описать восстановление из `/data/redis-pre-valkey-*` или очистку.
- **Observability:** нет изменений; exporter (`oliver006/redis_exporter`) работает с Valkey — проверить на k3d при приёмке.
- **Security/ownership/RBAC:** не применимо. Лицензионная причина смены образа — в CHANGELOG.
- **Acceptance (k3d):** свежая установка на одной ноде с ограничением CPU; upgrade с `main` (данные Redis 7.4 + HPA) по migration notes; `POST /api/v2/alerts` → 200.

## 6) References

- **Files:** `helm/amp/Chart.yaml`, `helm/amp/values.yaml:64-78,213-240,301-370,443-530`, `helm/amp/templates/{hpa,deployment,redis-statefulset,redis-config,pvc,NOTES}.*`, `helm/amp/values-production.yaml:100-115,150-155`, `scripts/release-gate.sh:120-150`, `deploy/e2e-ha/docker-compose.yml`, `go-app/internal/config/config.go:898`.
- **Docs:** BUGS `VALKEY-SUBCHART-INIT-ERROR`, `AMP-PVC-PENDING-STANDARD`; TECH-DEBT `HELM-CHART-GAPS`; ADR-016; `tasks/archive/PG-CONFIG-FIX/evidence/k3d-acceptance.md`.
- **Evidence:** `evidence/idle-top.txt`, `evidence/hpa-upgrade-off.txt`, `evidence/valkey-rdb-compat.txt`.
