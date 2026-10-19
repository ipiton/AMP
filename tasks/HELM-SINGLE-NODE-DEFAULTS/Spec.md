---
id: HELM-SINGLE-NODE-DEFAULTS
slug: helm-single-node-defaults
stream: operations
type: feature
status: draft
created_at: 2026-10-19
updated_at: 2026-10-19
based_on:
  - requirements.md
  - research.md
---

# Specification: дефолты чарта под одну ноду

**Version:** 1.0
**Status:** Draft

## Summary

Дефолтные values чарта переводятся с HA-профиля на одну ноду:
- HPA выключен;
- requests снижены с ~1,6 vCPU / 2,1Gi до ~270m / 512Mi;
- бюджет встроенного PG рассчитан на 1–3 пода.

Кеш остаётся одним инстансом — собственным StatefulSet чарта — на образе Valkey. Неиспользуемый и сломанный сабчарт `valkey` удаляется. Redis не используется: Redis 7.4 распространяется под RSALv2/SSPL (требование владельца).

## Requirements Coverage

| Requirement / Criterion | Covered by |
|---|---|
| HPA выключен по умолчанию; прод включает явно | Target Design § 1; `values-production.yaml:150` уже `enabled: true` (не меняется) |
| Сумма requests под одну ноду | Target Design § 2, таблица; Acceptance A1 |
| Бюджет соединений PG согласован, память по `maxConnections` | Target Design § 3 |
| `values-small.yaml` + README | Target Design § 5 |
| Решение по лишнему Redis | Target Design § 4 (сабчарт удаляется) |
| Кеш — Valkey, без Redis ≥ 7.4 (constraint владельца) | Target Design § 4; e2e-ha compose |
| CHANGELOG + migration notes | Rollout / Rollback |
| Рендер-тесты + release-gate | Component Architecture → tests; `/write-tests` |
| Установка на однонодовом k3d | Acceptance A1–A3 |
| (Включено в scope) BUGS `AMP-PVC-PENDING-STANDARD`, `VALKEY-SUBCHART-INIT-ERROR` | Target Design § 4, § 6 |

## Current State

- **Code:**
  - `helm/amp/values.yaml`:
    - AMP 500m/512Mi (`:64-70`);
    - HPA on 2–10 (`:213-216`);
    - PG 500m/1Gi, `maxConnections: 250`, `sharedBuffers: 256MB` (`:318-363`);
    - `cache.resources` (`:452-459`) — мёртвый ключ;
    - `valkey` — `image: redis:7-alpine`, 250m/256Mi + exporter 50m/64Mi (`:467-515`).
  - `helm/amp/templates/hpa.yaml:12` — `helm.sh/resource-policy: keep`.
  - `helm/amp/templates/redis-statefulset.yaml:66-97` — init `config-init` (busybox): копирует `redis.conf`, дописывает `requirepass`.
  - `helm/amp/templates/pvc.yaml` — без условия на профиль.
  - `helm/amp/Chart.yaml` — `dependencies: valkey 2.1.3` (groundhog2k), `Chart.lock`; `charts/` не в git.
  - `scripts/release-gate.sh:125-151,312` — шаг `helm-deps` читает `Chart.lock`.
  - `helm/amp/templates/NOTES.txt:45-62` — предупреждение о бюджете соединений, fallback `maxConnections` = 250.
  - `deploy/e2e-ha/docker-compose.yml:15` — `redis:7-alpine`.
- **Data:** PVC `redis-data-<fullname>-redis-0`. На существующих установках там RDB/AOF Redis 7.4 (RDB v12). Durable в нём только nflog, группы и таймеры (research § Q5).
- **Tests:**
  - `helm/amp/tests/render-*.sh`; `render-postgresql.sh:188-203` (T6) проверяет NOTES с `Expected: 250` и `allows max_connections=250`;
  - `render-defaults-validate.sh` — дефолты проходят валидацию;
  - гейт `scripts/release-gate.sh`.
- **Docs:**
  - `helm/amp/README.md:15-22,53-54,92,138` — HPA по умолчанию, два Redis, `cache.enabled` default `false` (неверно);
  - `helm/amp/CHANGELOG.md`, `CHANGELOG.md` `[Unreleased]`;
  - BUGS, TECH-DEBT `HELM-CHART-GAPS`, BACKLOG P1.

## Design Premises

| Premise | Confirmed by | Class | If wrong |
|---|---|---|---|
| AMP подключается только к `<fullname>-redis`, сабчарт `valkey` никем не используется | `templates/deployment.yaml:172-175` (`cache.host`); `git grep valkey` по `go-app/`, `deploy/`, `scripts/` — только `release-gate.sh` (helm-deps) | call-path-traced | Удаление сабчарта отключит кому-то кеш. Сабчарт в `Init:Error` с 2026-10-15, так что работающих потребителей быть не может |
| `valkey/valkey:8.1.10-alpine` работает с отрендеренным `redis.conf` чарта, `redis-server`/`redis-cli` в образе есть | docker 2026-10-19, `evidence/valkey-rdb-compat.txt` § 4; k3d: под `amp-redis-0` на Valkey 2/2 Ready, AMP ingest 200 | measured | Пришлось бы менять `command`/probes |
| `oliver006/redis_exporter:v1.55.0` снимает метрики с Valkey | k3d 2026-10-19: `redis_instance_info{redis_version="7.2.4",...} 1` с `amp-redis-0` | measured | Не работали бы алерты `redis-prometheusrule` |
| Valkey не читает RDB v12 (Redis 7.4), под падает | docker, Valkey 8.1.10 и 9.0: `Can't handle RDB format version 12` | measured | Init-перенос был бы лишним (вреда нет) |
| Redis 7.4 читает RDB v11, записанный Valkey 8.1 (откат чарта не ломает кеш) | docker 2026-10-19: Valkey пишет `REDIS0011`, `redis:7-alpine` загружает AOF base, `GET k` → `v` | measured | Откат требовал бы ручной очистки PVC |
| Потеря данных кеша = один повтор уведомлений по firing-группам; silences и алерты в PG | разбор кода агентом: `grouping/redis_notify_log.go`, `redis_timer_storage.go`, `service_registry.go:615-651,1204-1212` | code-read | Возможна потеря чего-то ещё, чего нет в PG. Риск — в Rollout |
| Нагрузка стека в простое ~47m / ~88Mi, requests ~270m / 512Mi с запасом | k3d, 10 срезов × 30 с, `evidence/idle-top.txt` | measured | Окно лёгкой нагрузки ≠ пиковая. Requests — минимум для планировщика, limits остаются прежними |
| Helm берёт `resource-policy: keep` из манифеста предыдущей ревизии; без `keep` удаляет HPA при выключении | k3d, `evidence/hpa-upgrade-off.txt` C и B | measured | Migration note про ручное удаление HPA была бы лишней |
| Helm 4 SSA может конфликтовать на `.spec.replicas` с HPA-контроллером | k3d, `evidence/hpa-upgrade-off.txt` A | measured | Рекомендация `--force-conflicts` была бы лишней |
| Пул AMP — 25 соединений на под, чарт его не переопределяет | `go-app/internal/config/config.go:898`; `git grep max_connections helm/` — только PG-конфиг и NOTES | call-path-traced | Бюджет `maxConnections` неверен |
| Прод не использует встроенный PG и встроенный кеш | `values-production.yaml`: `postgresql.enabled: false`, `valkey.replicas: 0` + внешний Dragonfly | code-read | Migration notes коснулись бы прода |
| Valkey 9.x пишет RDB с другой сигнатурой (`VALKEY`/v80), а не `REDIS0012+` | не проверено | assumed | Init-скрипт мог бы трогать данные Valkey 9. Защита: скрипт смотрит только на `REDIS` + версия ≥ 12, а v ≥ 12 под сигнатурой `REDIS` пишет только Redis ≥ 7.4. Риск — в Open Questions |

## Target Design

1. **HPA.** `autoscaling.enabled: false` по умолчанию; Deployment получает `replicas: {{ replicaCount }}` = 1. Аннотация `helm.sh/resource-policy: keep` в `hpa.yaml` снимается. После этого будущие on→off переключения удаляют HPA штатно. HPA, оставшийся от старых ревизий, удаляется руками один раз (migration note).
2. **Requests** (limits не меняются, кроме PG):

   | Контейнер | Requests было | Requests стало | Замер max |
   |---|---|---|---|
   | amp | 500m / 512Mi | 100m / 128Mi | 14m / 21Mi |
   | postgres | 500m / 1Gi | 100m / 256Mi | 39m / 55Mi |
   | postgres-exporter | 50m / 64Mi | 10m / 32Mi | 2m / 6Mi |
   | valkey (`amp-redis`) | 250m / 256Mi | 50m / 64Mi | 12m / 5Mi |
   | redis-exporter | 50m / 64Mi | 10m / 32Mi | 8m / 13Mi |
   | **Итого** | 850m+… (с сабчартом и 2 репликами — 1,6 vCPU / 2,1Gi) | **270m / 512Mi** | 75m / 100Mi |

3. **Бюджет встроенного PG** (dev/pilot, ADR-016):
   - `maxConnections: 100`;
   - расчёт: 1 под + 1 при rolling update (`maxSurge 25%` → 1) = 2 × 25 + exporter + `superuserReservedConnections: 10`, запас до 3 подов;
   - память: `sharedBuffers: 128MB`, `effectiveCacheSize: 512MB`; resources — requests 100m/256Mi, limits 1000m/1Gi.
   - Fallback в NOTES (`| default 250`) → 100. Предупреждение NOTES (HPA on и `maxReplicas × 25 ≥ maxConnections`) остаётся: кто включает HPA на встроенном PG, его видит.
4. **Кеш — один инстанс, Valkey.**
   - Из `Chart.yaml` удаляются `dependencies`, удаляется `Chart.lock`. Шаг `helm-deps` в гейте при отсутствии `Chart.lock` пишет «no dependencies» и проходит. Код шага сохраняется: если зависимость вернётся, защита из `PROD-HELM-CLEAN-CHECKOUT` продолжит работать.
   - `valkey.image`: `repository: valkey/valkey`, `tag: "8.1.10-alpine"` (опубликован 2026-09-21, карантин пройден, пин патча — воспроизводимость).
   - Мёртвый `cache.resources` удаляется. Ключ не читался ни одним шаблоном, поведение не меняется; в CHANGELOG — одной строкой.
   - `valkey.enabled` остаётся мёртвым, как и было (TECH-DEBT).
   - В `config-init` добавляется перенос несовместимых данных:
     - выполняется, только если `valkey.image.repository` содержит `valkey`;
     - для `/data/dump.rdb` и `/data/appendonlydir/*.base.rdb` читаются первые 9 байт; при сигнатуре `REDIS` и версии ≥ `0012` весь `appendonlydir` и `dump.rdb` переносятся в `/data/redis-pre-valkey-<unix-ts>/`, в лог пишется одна строка с путём;
     - повторный запуск ничего не находит, то есть идемпотентен.
5. **`helm/amp/values-small.yaml`** — пример для ноды, где под AMP свободно ≤ 1 vCPU / 1Gi, поверх новых дефолтов:
   - PVC PG 5Gi, Valkey 1Gi;
   - Valkey limits 256Mi, `maxmemory: 192mb`;
   - limits: AMP 512Mi, PG 512Mi (`sharedBuffers: 64MB`, `effectiveCacheSize: 256MB`, `maxConnections: 60`).
   - Комментарии объясняют каждый ключ. README чарта ссылается на файл.
6. **`pvc.yaml`** рендерится только при `profile: lite`: там единственный потребитель тома (BUGS `AMP-PVC-PENDING-STANDARD`).
7. **`deploy/e2e-ha/docker-compose.yml`:** `redis:7-alpine` → `valkey/valkey:8.1.10-alpine`. `command` в compose проверить при реализации.

Короче: дефолты описывают одну ноду, HA включается `values-production.yaml` или `autoscaling.enabled`; кеш — один Valkey; несовместимые данные Redis 7.4 init-контейнер откладывает в сторону, чтобы `helm upgrade` не превратился в crashloop.

## API Contracts

Не применимо: HTTP API и формат конфига AMP не меняются. Контракт values: ключи не переименовываются; удаляется только мёртвый `cache.resources`, который не читал ни один шаблон, поэтому сигнал `C` не ставится.

## Data Model / Migrations

Таблиц нет. Данные кеша (PVC `redis-data-*`):

- **Migration:** да, неявная. Init переносит RDB/AOF Redis ≥ 7.4 в `/data/redis-pre-valkey-<ts>/`, Valkey стартует пустым.
- **Потери:** nflog, группы, таймеры. Следствия: один повтор уведомлений по firing-группам; resolved по алертам, погасшим во время апгрейда, могут не уйти; короткое окно без inhibition-источников. Silences и алерты не теряются, они в PG.
- **Rollback / forward-fix:**
  - откат на старый чарт: Redis 7.4 читает данные, которые записал Valkey (v11);
  - вернуть исходные данные Redis: остановить под, перенести файлы из `redis-pre-valkey-*` обратно в `/data` и запустить старый чарт;
  - каталог-бэкап не удаляется автоматически. Его объём не больше `maxmemory` (384mb по умолчанию); удаление — ручное (CHANGELOG).

## Component Architecture

- `helm/amp/Chart.yaml` — удалить `dependencies`; `helm/amp/Chart.lock` — удалить.
- `helm/amp/values.yaml` — § 1–4: HPA, requests, PG-бюджет, `valkey.image`, удаление `cache.resources`; комментарии синхронизировать (`TN-98`/`TN-99`, «HPA cluster mode», «50 conns/pod × 10 replicas»).
- `helm/amp/values-small.yaml` — новый (§ 5).
- `helm/amp/templates/hpa.yaml` — снять `keep`.
- `helm/amp/templates/redis-statefulset.yaml` — init: перенос несовместимых данных (§ 4).
- `helm/amp/templates/pvc.yaml` — условие `profile: lite`.
- `helm/amp/templates/NOTES.txt` — fallback 100; убрать «Resources: 500m CPU, 512Mi RAM (per pod)» как захардкоженный текст (брать из values или удалить строку).
- `scripts/release-gate.sh` — `step_helm_deps`: нет `Chart.lock` → лог и `return 0`.
- `deploy/e2e-ha/docker-compose.yml` — образ Valkey.
- `helm/amp/tests/` — новый `render-single-node.sh` и правка T6 в `render-postgresql.sh` (250 → 100). Пишутся на шаге `/write-tests`, после `deep-review`.
- Docs:
  - `helm/amp/README.md` (§ «Read before installing», список компонентов, таблица `cache.enabled`, бюджет соединений, `values-small.yaml`, Valkey);
  - `helm/amp/CHANGELOG.md`, `CHANGELOG.md` `[Unreleased]` (Changed + migration notes);
  - planning-файлы на `finalize`: BUGS (закрыть два бага), TECH-DEBT `HELM-CHART-GAPS` (снять пункт «два Redis»), BACKLOG.
- `DECISIONS.md` — **ADR-017**: кеш AMP — Valkey (BSD-3); Redis ≥ 7.4 не используется из-за лицензии; внешний Redis-совместимый кеш (Dragonfly и т. п.) — выбор оператора.

## Security Design

- [x] Ownership validation — не применимо.
- [x] Input validation: init-скрипт читает только заголовки файлов на собственном PVC; пути фиксированы; `set -e` сохраняется.
- [x] Sensitive data not logged: init не выводит содержимое файлов и `requirepass` (как и сейчас).
- [x] Rate limiting — не применимо.
- [x] Auth/RBAC — не меняется. `requirepass` из Secret, NetworkPolicy, ADR-015 guard — как были.
- Supply chain: внешний helm-репозиторий (groundhog2k) уходит из сборки; новый образ `valkey/valkey` с пином патча, опубликован > 7 дней назад.

## Invariants

- [ ] `values-production.yaml` рендерится как раньше: HPA 3–20, внешний PG, `valkey.replicas: 0`. Разница рендера — только удалённые объекты сабчарта и `amp-pvc`.
- [ ] `profile: lite` рендерится как раньше: `amp-pvc` есть, HPA нет.
- [ ] `cache.host` и Service `<fullname>-redis` не меняются: AMP и пользовательские values с внешним кешем работают без правок.
- [ ] `helm upgrade` без пользовательских values не уходит в crashloop на данных Redis 7.4.
- [ ] Init не трогает данные, которые Valkey читает (RDB ≤ v11), и ничего не делает с пользовательским образом не-Valkey.
- [ ] Все ранее существовавшие рендер-тесты проходят; `scripts/release-gate.sh` PASS.

## Edge Cases

1. **Существующая установка на старом чарте, кеш Redis 7.4, HPA on.**
   - Ожидание: `helm upgrade` → init переносит данные, Valkey Ready; HPA-сирота остаётся и держит 2 реплики.
   - Migration note: `kubectl delete hpa <fullname>` или `autoscaling.enabled: true`.
2. **Helm 4 и HPA уже писал `.spec.replicas`.**
   - Ожидание: `UPGRADE FAILED: conflict ... .spec.replicas`.
   - Migration note: повторить с `--force-conflicts`.
3. **Пользователь переопределил `valkey.image` на `redis:7.4`.** Init не запускает перенос (repository без `valkey`), данные целы.
4. **Пользовательский образ `bitnami/valkey`** (repository содержит `valkey`) с RDB v11. Init смотрит на версию и не трогает данные.
5. **`valkey.replicas: 0` (прод с Dragonfly).** Подов нет, init не выполняется.
6. **Пустой PVC (свежая установка).** Файлов нет, init ничего не делает.
7. **AOF без RDB-преамбулы** (`*.base.aof`). Glob `*.base.rdb` не совпадает, такие файлы не трогаются. Чарт пишет с преамбулой (по умолчанию), риск теоретический.
8. **Повторный рестарт после переноса.** `/data` уже без v12, init идемпотентен.
9. **Пользователь включает HPA на встроенном PG** (`maxReplicas: 10`). NOTES предупреждает (10 × 25 ≥ 100); README говорит поднять `maxConnections` и память.
10. **`values-small.yaml` + HPA.** Не поддерживается, файл ставит `autoscaling.enabled: false` явно.

## Impact Analysis

- **Affected modules:** `helm/amp` (values, 4 шаблона, Chart.yaml/lock), `scripts/release-gate.sh`, `deploy/e2e-ha`, docs.
- **Breaking changes** (для `helm upgrade` с дефолтными values):
  - HPA выключен → 1 реплика; HPA-сирота от `keep` остаётся до ручного удаления;
  - новые requests (меньше) и PG-параметры → рестарт PG;
  - образ кеша Redis → Valkey, данные кеша сбрасываются (один повтор уведомлений);
  - удалены объекты сабчарта `valkey` (Deployment, Service, NetworkPolicy, PVC 5Gi без данных) и `amp-pvc` в standard.
- **New dependencies:** образ `valkey/valkey:8.1.10-alpine`. Уходят: helm-зависимость `groundhog2k/valkey`, образ `redis:7-alpine`.
- **Risks:**
  - повтор уведомлений при апгрейде → CHANGELOG предупреждает, ставить в окно обслуживания;
  - requests ниже пиков → limits прежние, QoS Burstable как и сейчас;
  - init удалит не то → перенос, а не удаление, плюс условие по сигнатуре и версии плюс рендер-тесты.

## Rollout / Rollback

- **Rollout:** AMP не деплоится агентом (WORKFLOW). Сигнал `R` закрывается записями в CHANGELOG (`[Unreleased]` → Changed + «Breaking changes / migration notes»):
  1. HPA: что изменилось; как вернуть (`autoscaling.enabled: true` или `-f values-production.yaml`); одноразовое `kubectl delete hpa <fullname>`, если HPA не нужен.
  2. Helm 4: при конфликте `.spec.replicas` — `--force-conflicts`.
  3. Кеш: Redis → Valkey, причина (лицензия); данные Redis 7.4 переносятся в `/data/redis-pre-valkey-<ts>/`; один повтор уведомлений; как удалить бэкап; что внешний кеш (`cache.host`) не затронут.
  4. PG: новые `maxConnections`/память, рестарт PG; кто держит > 3 подов на встроенном PG — поднять `maxConnections`.
  5. Удалены сабчарт `valkey` и `amp-pvc` (standard); оба без данных.
- **Rollback:**
  - `helm rollback` на прошлую ревизию: Redis 7.4 читает данные Valkey (проверено);
  - HPA вернётся, если был включён;
  - исходные данные Redis — вручную из `redis-pre-valkey-*`.
- **Feature flag:** не применимо. Старое поведение возвращается через values (`autoscaling.enabled`, `valkey.image`, resources).
- **Acceptance (k3d, evidence в `evidence/`):**
  - **A1 — свежая установка.** k3d с `--kubelet-arg=kube-reserved=cpu=7` (allocatable ≈ 1 vCPU на 8-ядерном хосте).
    - чарт `main`: поды `Pending`;
    - ветка: все поды Ready, `POST /api/v2/alerts` → 200, exporter `redis_up 1`, `pg_up 1`.
  - **A2 — upgrade с `main`.** Установка `main` (HPA on, данные Redis 7.4 после потока алертов) → upgrade веткой.
    - init перенёс файлы (лог + `ls`), Valkey Ready, ingest 200;
    - HPA-сирота есть → `kubectl delete hpa` → 1 реплика.
  - **A3 — rollback.** `helm rollback` → Redis 7.4 стартует на данных Valkey.
  - **A4 — `values-small.yaml`.** Установка, всё Ready.

## Observability

- **Logs:** init `config-init` — одна строка при переносе (путь каталога) и одна при отсутствии несовместимых данных.
- **Metrics:** не меняются. Exporter работает с Valkey (проверено).
- **Alerts:** не меняются. Правила `redis-prometheusrule` читают те же метрики.

## Deep Review

- **Mandatory triggers present:** `M` (сброс данных кеша при upgrade).
- **Discretionary triggers present:** novel pattern — shell-перенос данных в init-контейнере.
- **Decision:** required.

## Open Questions

- [ ] (assumed) Valkey 9.x не пишет `REDIS0012+`. Не блокирует: чарт пинит 8.1, где сигнатура `REDIS0011` проверена. При переходе на 9.x перепроверить init-условие (комментарий в шаблоне).
- [ ] Сколько уведомлений реально повторится при upgrade, проверяется на A2 (число отправок на тестовый webhook до и после). Если повтор шире одного раунда по firing-группам, вернуться к spec.
