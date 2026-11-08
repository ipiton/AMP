---
id: UPSTREAM-HA-MEMBERLIST
slug: upstream-ha-memberlist
type: research
level: light
created_at: 2026-11-08
---

# Research Pack - HA upstream-ядра на memberlist

## 0) TL;DR

- Кластер переносится из `app/app.go` v0.34.1 почти дословно: `cluster.Create` → `AddState("nfl")`/`AddState("sil")` + `SetBroadcast` → `Join` → `go Settle` → пир в `api.Options.Peer`, в `PipelineBuilder.New` и в `waitFunc` (`clusterWait`: позиция × `peer_timeout`) → `Leave` на `Stop`. Своего кода — ноль, только проводка.
- Снапшоты silences/nflog уже пишутся в `DataDir` (порт 1a); на PVC их кладёт чарт (`--storage.path=/data` + `volumeClaimTemplates`).
- Чарт: отдельный StatefulSet за `upstreamCore.enabled` (default `false`) рядом с Deployment своего ядра — новые файлы, селекторы не пересекаются с `cmd/server`. Одна реплика — без кластера (как community-чарт Alertmanager), N > 1 — пиры по DNS headless-сервиса.
- `e2e-ha` переписывается на два `amp-upstream` и реальный webhook-приёмник: у upstream-ядра доставка работает вне K8s, обходной путь со счётом логов `MetricsOnlyPublisher` больше не нужен.
- Redis своему коду после эпика нужен для: дедупликации investigation между репликами, L2-кеша классификации (и, шире, дедупликации LLM-вызовов: в HA каждый алерт приходит на каждую реплику), хранилища incident ID Rootly. Остальное (lock, heartbeat, группы, таймеры, nflog, шина silences, кеш inhibition) — своё ядро, снимается срезом 5.

## 1) Questions

1. Что именно делает upstream при включённом кластере и что из этого не переносится?
2. Как включать upstream-ядро в чарте, не ломая single-node умолчания и не конфликтуя со срезами 2b/3?
3. Чем проверять HA в `e2e-ha`, если доставка своего ядра вне K8s невозможна?
4. Где Redis/Valkey нужен своему коду после удаления своего ядра?
5. Что делать с кандидатами на снятие (`HELM-REDIS-NOEVICTION`, `TIMER-FIRE-OUTCOME-METRIC`, `INHIBIT-CACHE-PUBSUB-HA`, `FU-REDIS-LIVE-CLIENT-HANDLE`, `CI-E2E-HA-REQUIRED`) и с записями «срез 4» в BUGS/TECH-DEBT, если Redis-код здесь не удаляется?

## 2) Findings

**Q1 — кластер upstream** (`code-read`, `alertmanager@v0.34.1`):

- `app/app.go:202-246`: `cluster.GetTLSTransportConfig(TLSConfigFile)`, при непустом `ClusterBindAddr` — `cluster.Create(...)` с 16 параметрами, сразу `onStop(settleCancel; peer.Leave(10s))`, `clusterEnabled.Set(1)`.
- `app/app.go:299-302, 339-342`: `peer.AddState("nfl", notificationLog, reg)` + `notificationLog.SetBroadcast(c.Broadcast)`; то же для `"sil"` и silences. До `Join`.
- `app/app.go:351-360`: `peer.Join(ReconnectInterval, PeerReconnectTimeout)` (ошибка — только Warn), `Settle(ctx(SettleTimeout), GossipInterval*10)` в горутине, `eventRec.SetClusterPeer(peer)` (event recorder в порте нулевой — не переносится).
- `app/app.go:391-401`: `api.Options.Peer` — интерфейс `cluster.ClusterPeer`, nil-интерфейс при выключенном кластере (типизированный nil-указатель сломал бы проверку).
- `app/app.go:441-444` + `app/cluster.go`: `waitFunc = clusterWait(peer, PeerTimeout)` → `Position() * timeout`; входит и в `timeoutFunc` диспетчера.
- `app/reloader.go:170-187`: в `PipelineBuilder.New` последним аргументом `notify.Peer` (nil-интерфейс без кластера) — конвейер получает `GossipSettleStage` (ждёт `WaitReady`) и `WaitStage` по позиции; дедупликация между репликами — `DedupStage` по общему nflog.
- `cmd/alertmanager/main.go:72-89` + `app/options.go:120-131`: флаги `--cluster.*` и умолчания (`listen-address` `0.0.0.0:9094`, `peer-timeout` 15s, `settle-timeout` = push-pull 1m, остальные — константы `cluster.Default*`).
- `cluster.Create` принимает порт 0 (memberlist сам выбирает), фактический адрес виден в `/api/v2/status` `cluster.peers[].address` — тестам не нужен новый API.
- Не переносится: event recorder (нет в порте с 1a), `--cluster.tls-config` переносится как флаг (одна строка, upstream-функция).

**Q2 — чарт** (`code-read`): образ (`Dockerfile`) собирает только `./cmd/server`. Основной Service выбирает поды по `amp.selectorLabels` (name + instance) и `targetPort: http` — upstream-поды с теми же двумя метками попали бы в его endpoints. В чарте уже есть StatefulSet (Valkey, PG) и рендер-тесты `tests/render-*.sh`, которые `release-gate.sh` (`step_helm_tests`) запускает все. Community-чарт Alertmanager при одной реплике выключает кластер (`--cluster.listen-address=`), при нескольких — пиры `<name>-<i>.<headless>:9094`, `podManagementPolicy: Parallel`, `publishNotReadyAddresses` не нужен для `cluster.peer`, но нужен, чтобы DNS пиров резолвился до readiness (Settle ждёт пиров).

**Q3 — e2e-ha** (`code-read`): старый сценарий доказывает «ровно одна доставка» счётом строк лога, потому что доставка своего ядра идёт только через K8s Secrets (`run.sh` шапка). Upstream-ядро шлёт `webhook_configs` само — достаточно контейнера-приёмника, который пишет каждый POST в stdout.

**Q4 — Redis в своём коде** (`code-read`, `grep redis/go-redis` + потребители `cache.Cache`):

| Где | Зачем | После эпика |
|---|---|---|
| `infrastructure/investigation/dedup.go`, `queue.go` (`SetSharedDedup`, SETNX) | одна investigation на инцидент между репликами | **нужен**: в HA upstream каждый алерт приходит на каждую реплику (Prometheus шлёт во все), `PostStore` (2b) сработает на каждой |
| `core/services/classification.go` (`Cache` L2) | кеш классификации между репликами | **нужен** для HA: сейчас `amp-upstream` — `cache.NewMemoryCache`, N реплик = N вызовов LLM на алерт |
| `infrastructure/publishing/rootly_incident_store.go` | incident ID Rootly для resolve | **нужен**, если Rootly остаётся своим notifier (срез 3): resolve может уйти с другой реплики |
| `infrastructure/template/cache.go` (L2) | кеш шаблонов своего ядра | снимается срезом 5 (шаблоны upstream) |
| `cmd/server/handlers/{silence,dashboard_*}.go` | кеш ответов | срез 5/6 |
| `infrastructure/inhibition/{state_manager,cache}.go` | кеш inhibition своего ядра | срез 5 |
| `infrastructure/grouping/redis_{group_storage,timer_storage,notify_log}.go`, `lock/*`, `cluster/heartbeat.go`, `silencing/redis_event_bus.go` | Redis-HA своего ядра | срез 5 |

**Q5 — кандидаты и записи** (`code-read`, `BACKLOG.md`, `BUGS.md`/`TECH-DEBT.md`):

- `HELM-REDIS-NOEVICTION`, `TIMER-FIRE-OUTCOME-METRIC`, `INHIBIT-CACHE-PUBSUB-HA`, `FU-REDIS-LIVE-CLIENT-HANDLE` — все про Redis-HA своего ядра. Upstream-ядро ими не пользуется: для оставшегося Redis (кеш) `allkeys-lru` правильная политика; inhibition upstream считается локально на каждой реплике (у каждой полный набор алертов). Код живёт до среза 5 → снимаются срезом 5, не здесь.
- `CI-E2E-HA-REQUIRED` — сценарий меняется целиком, «заменить ожидания на опрос» делается здесь; остаётся «10 зелёных прогонов → required».
- Записи «superseded — ADR-019, срез 4» в BUGS/TECH-DEBT (группы, таймеры, nflog, lock, heartbeat Redis) — код не удаляется здесь (решение владельца в брифе среза), переадресуются на срез 5.

## 3) Options

**Generation:** single-author (уровень light: решение обратимо, чарт-значение под флагом выключено по умолчанию).

### Option A - отдельный StatefulSet за `upstreamCore.enabled`, новые файлы шаблонов
- Плюсы: рендер по умолчанию не меняется; ни одного правленого существующего шаблона — нет конфликтов со срезами 2b/3; срез 5 переводит основной трафик на него и удаляет Deployment.
- Минусы: временно две рабочие нагрузки в одном релизе; Ingress/ServiceMonitor/NetworkPolicy на StatefulSet не распространяются до среза 5.

### Option B - переключатель ядра в `deployment.yaml` (Deployment ↔ StatefulSet)
- Плюсы: одна нагрузка, Ingress и прочее работают сразу.
- Минусы: правка самого большого шаблона (23KB) под условие, конфликты со срезом 3 (RBAC/таргеты) и 2b; поведение своего ядра в StatefulSet не нужно никому.

### Option C - отдельный чарт `helm/amp-upstream`
- Минусы: два чарта на один продукт (против ADR-019), дублирование helpers и релизного процесса.

## 4) Decision

- Код: дословный перенос кластера upstream в `upstreamcore` под `Options.Cluster*`; пустой `ClusterBindAddr` — выключен (умолчание пакета, как `app.DefaultOptions`); у бинаря `--cluster.listen-address` по умолчанию `0.0.0.0:9094`, как у upstream.
- Чарт: Option A.
- e2e-ha: два `amp-upstream` + приёмник webhook, ожидания опросом.
- Redis: инвентаризация выше — в итог; follow-up `UPSTREAM-AI-HA-DEDUP` (дедупликация классификации/investigation между репликами через общий Valkey) — в BACKLOG.
- Кандидаты и записи «срез 4» — переадресовать на срез 5; `CI-E2E-HA-REQUIRED` — обновить текст.

## 5) Spec Inputs

- Новые поля `upstreamcore.Options` повторяют имена `app.Options` (`ClusterBindAddr`, …, `Label`); умолчания — `app.DefaultOptions`.
- `upstream-core-gate.sh` поднимает бинарь с явным `--cluster.listen-address` (иначе три параллельные проверки делят порт 9094).
- Чарт: метка `app.kubernetes.io/name: <name>-upstream` у подов, чтобы основной Service и NetworkPolicy их не выбирали.

## 6) References

- `github.com/prometheus/alertmanager@v0.34.1/app/{app.go,reloader.go,cluster.go,options.go}`, `cluster/cluster.go`, `cmd/alertmanager/main.go`.
- `DECISIONS.md` § ADR-019 («чарт: StatefulSet, headless Service и порт gossip; Valkey остаётся только как кеш классификации»).
- `deploy/e2e-ha/run.sh` (шапка — почему старый сценарий считал логи).
