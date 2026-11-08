---
id: UPSTREAM-HA-MEMBERLIST
slug: upstream-ha-memberlist
stream: Architecture / ADR-019
type: feature
status: draft
created_at: 2026-11-08
updated_at: 2026-11-08
based_on:
  - requirements.md
  - research.md
---

# Specification: HA upstream-ядра на memberlist (срез 4 ADR-019)

**Version:** 1.0
**Status:** Draft

## Summary

В порт upstream `app` (`internal/upstreamcore`) возвращается вырезанный в 1a gossip-кластер upstream (`cluster.Peer`): silences и nflog реплицируются, конвейер доставки ждёт settle и позицию пира. `amp-upstream` получает флаги `--cluster.*` upstream. Образ включает `amp-upstream`, чарт — выключенный по умолчанию StatefulSet upstream-ядра с headless Service и PVC. `e2e-ha` проверяет HA upstream-ядра.

## Requirements Coverage

| Requirement / Criterion | Covered by |
|---|---|
| Настройки кластера в `Options`, пустой адрес — выключен | Target Design § 1 |
| Peer, `AddState`, `Join`, `Settle`, пир в API/конвейере, метрика, `Leave` | Target Design § 1 |
| Флаги `--cluster.*` | Target Design § 2 |
| Тест двух реплик | Test Plan (write-tests) |
| Образ с `amp-upstream` | Target Design § 3 |
| Чарт: StatefulSet, headless, gossip-порт, PVC, single-node | Target Design § 4 |
| `e2e-ha` под memberlist | Target Design § 5 |
| Инвентаризация Redis | `research.md` Q4, итог задачи, follow-up |
| Гейты | Testing |

## Current State

- `upstreamcore.New` (`core.go:150`) строит nflog, silences, provider, API, reloader без пира; `waitFunc` всегда 0; `api.Options.Peer` не задан; `PipelineBuilder.New(..., nil)` (`reloader.go:154`); `clusterEnabled` 0.
- `cmd/amp-upstream/main.go` — флаги `config.file`, `storage.path`, `data.retention`, `web.listen-address`, `web.external-url`, `amp.config.file`.
- `Dockerfile` — один `go build ./cmd/server`.
- `helm/amp` — Deployment своего ядра, Valkey/PG StatefulSet; upstream-ядра нет.
- `deploy/e2e-ha` — 2 реплики `cmd/server` + Valkey + PG, проверки по логам и ключам Redis.

## Design Premises

| # | Premise | How confirmed | Class |
|---|---|---|---|
| P1 | Upstream при кластере делает ровно шаги `app/app.go:202-360,391-445`, `reloader.go:170-187`; другого кода для HA нет | чтение v0.34.1 | `code-read` |
| P2 | `notify` дедуплицирует между репликами по общему nflog: реплика на позиции k ждёт `k × peer_timeout`, затем `DedupStage` видит запись первой | `notify.WaitStage`/`DedupStage`, `app/cluster.go` | `code-read` |
| P3 | `cluster.Create` с портом 0 работает, фактический адрес виден в `/api/v2/status` | `cluster.go:Create` передаёт порт в memberlist; подтверждается тестом двух реплик | `code-read` → проверяется в write-tests |
| P4 | Снапшоты `silences`/`nflog` пишутся в `DataDir` на остановке (закрытие `stopc` → финальный снапшот в `Maintenance`) и читаются на старте | порт 1a, `silence.Maintenance` | `code-read` → проверяется в `e2e-ha` (silence переживает рестарт) |
| P5 | Основной Service чарта выбрал бы поды с `amp.selectorLabels` и портом `http` | `templates/service.yaml` | `code-read` |
| P6 | Pod IP — приватный адрес; `--cluster.advertise-address=$(POD_IP):9094` корректен для memberlist | community-чарт Alertmanager делает так же | `assumed` → риск в Rollout |
| P7 | Срезы 2b/3 не трогают `Dockerfile` и шаблоны `helm/amp/templates/upstream-*` | бриф эпика; конфликт проверяется при мерже | `assumed` → риск в Rollout |

## Target Design

### 1. `internal/upstreamcore`

`Options` получает поля upstream `app.Options` (те же имена и смысл): `ClusterBindAddr`, `ClusterAdvertiseAddr`, `ClusterPeerName`, `Peers`, `PeerTimeout`, `PeersResolveTimeout`, `GossipInterval`, `PushPullInterval`, `TCPTimeout`, `ProbeTimeout`, `ProbeInterval`, `SettleTimeout`, `ReconnectInterval`, `PeerReconnectTimeout`, `TLSConfigFile`, `AllowInsecureAdvertise`, `Label`. Нулевые длительности → умолчания `app.DefaultOptions` (экспортируются константами `Default*`, как уже `DefaultRetention`). Пустой `ClusterBindAddr` — кластера нет, всё как сейчас.

`setup` повторяет upstream по порядку: TLS-конфиг → `cluster.Create` → `onStop(settleCancel; Leave(10s))` → `clusterEnabled.Set(1)` → nflog + `AddState("nfl")`/`SetBroadcast` → silences + `AddState("sil")`/`SetBroadcast` → `Join` (ошибка — Warn) → `go Settle(ctx, GossipInterval*10)` → `api.Options.Peer` (nil-интерфейс без кластера) → `waitFunc = Position() * PeerTimeout`. `reloader` получает поле `peer` и передаёт `notify.Peer` в `PipelineBuilder.New` (nil-интерфейс без кластера). Ошибка `Leave` логируется (у upstream `onStop` возвращает ошибку в общий лог остановки).

`doc.go`: § Known differences — «no gossip cluster» убирается, остаётся «no event recorder (и `SetClusterPeer`)»; `metrics` — комментарий про `clusterEnabled`.

### 2. `cmd/amp-upstream`

Флаги upstream `cmd/alertmanager/main.go:72-89` с теми же именами, описаниями и умолчаниями (`--cluster.listen-address=0.0.0.0:9094`, `--cluster.peer` повторяемый). Повторяемый флаг — локальный `flag.Value` (stdlib `flag`, без kingpin).

### 3. `Dockerfile`

Второй `go build` с теми же `-ldflags` → `/app/amp-upstream`; `CMD` не меняется (`/app/amp`).

### 4. Чарт `helm/amp`

Values (новая секция, default выключено):

```yaml
upstreamCore:
  enabled: false
  replicas: 1          # 1 — без кластера; > 1 — memberlist между подами
  config: {...}        # alertmanager.yml (toYaml); по умолчанию — один receiver без интеграций
  peerTimeout: 15s
  persistence: {size: 1Gi, storageClass: "", accessMode: ReadWriteOnce}
  resources: {...}
  extraArgs: []
```

Новые файлы: `upstream-configmap.yaml` (`alertmanager.yml`), `upstream-statefulset.yaml`, `upstream-service.yaml` (ClusterIP, порт `web` 9093), `upstream-service-headless.yaml` (`clusterIP: None`, `publishNotReadyAddresses: true`, порты `web` 9093, `gossip-tcp`/`gossip-udp` 9094). Помощники `amp.upstream.fullname`, `amp.upstream.selectorLabels` (`app.kubernetes.io/name: <name>-upstream`, `instance`, `component: upstream-core`) в `_helpers.tpl` (дописываются в конец).

StatefulSet: `serviceName` = headless, `podManagementPolicy: Parallel`, образ — тот же, что у Deployment, `command: [/app/amp-upstream]`, аргументы `--config.file=/etc/amp-upstream/alertmanager.yml`, `--storage.path=/data`, `--web.listen-address=:9093`, `--web.external-url=http://<svc>:9093/` (по умолчанию; `upstreamCore.externalURL` переопределяет). Кластер: при `replicas > 1` — `--cluster.listen-address=0.0.0.0:9094`, `--cluster.advertise-address=$(POD_IP):9094`, `--cluster.peer=<fullname>-<i>.<headless>.<ns>.svc:9094` для каждого i, `--cluster.peer-timeout`; при 1 — `--cluster.listen-address=`. Пробы `/-/healthy`, `/-/ready`. `podSecurityContext`/`securityContext` — общие values чарта (`readOnlyRootFilesystem` совместим: пишется только `/data`). PVC — `volumeClaimTemplates` `data`. Аннотация `checksum/config` по ConfigMap — смена `alertmanager.yml` перекатывает поды (hot reload `/-/reload` — без сайдкара, срез 5).

Рендер-тест `tests/render-upstream-core.sh`: по умолчанию ни одного `upstream`-объекта; `enabled` + 1 реплика — кластер выключен, PVC есть; 3 реплики — 3 `--cluster.peer`, `Parallel`, headless с UDP 9094; селектор upstream не совпадает с селектором основного Service.

### 5. `deploy/e2e-ha`

Compose: `amp-a`, `amp-b` — образ из `Dockerfile`, `command` `/app/amp-upstream` с кластером (`--cluster.peer` друг на друга, `--cluster.peer-timeout=5s`, `--cluster.settle-timeout`/`gossip-interval` короче), том `data` на каждую реплику; `sink` — `python:3.13-alpine` с однострочным HTTP-сервером, печатающим каждый POST. `config.yaml` → `alertmanager.yml` (route на webhook `sink`, `group_wait` 2s). Valkey/PG убираются — upstream-ядру не нужны.

Сценарий (каждое ожидание — опрос с дедлайном): (1) обе `/-/ready`; (2) `/api/v2/status` на обеих: `cluster.status=ready`, 2 пира; (3) silence через A виден на B; (4) алерт POST в A и B → ровно одно уведомление в `sink` после `group_wait + peer_timeout + запас`; (5) алерт, попадающий под silence, в обе → ноль уведомлений; (6) `kill` A, новый алерт в B → одно уведомление; (7) `restart` B (A мёртв) → silence из шага 3 на месте (снапшот). `docs/CI.md`, `.github/workflows/ci.yml` — строка/комментарий.

### 6. `scripts/upstream-core-gate.sh`

amtool-гейт запускает бинарь с `--cluster.listen-address=127.0.0.1:0` (кластер включён, как в upstream по умолчанию, но без общего порта 9094 — параллельные прогоны гейта в разных worktree).

## API Contracts

- `/api/v2/status` при включённом кластере отдаёт `cluster` (`name`, `status`, `peers`) — схема upstream, не своя. Без кластера — как сейчас.
- Флаги `amp-upstream`: только добавление.

## Data Model / Migrations

Not applicable: снапшоты — формат upstream в `DataDir`, существовали с 1a.

## Component Architecture

Без новых пакетов и типов, кроме `stringsFlag` в `cmd/amp-upstream`.

## Security Design

- Gossip без шифрования — умолчание upstream; порт 9094 только в headless Service (ClusterIP None), Ingress не трогается. `--cluster.tls-config` доступен через `upstreamCore.extraArgs`.
- `--cluster.label` доступен флагом (защита от перекрёстного gossip двух кластеров в одной сети).
- NetworkPolicy чарта upstream-поды не выбирает (как и до среза: правила только для `component: application`) — перенос в срезе 5, follow-up.

## Invariants

- Без `ClusterBindAddr` поведение `upstreamcore` идентично текущему (контрактные сьюты, `upstream-core-gate.sh`).
- `AddState` для обоих состояний — до `Join`; пир останавливается (`Leave`) после диспетчера и maintenance (обратный порядок `cleanups`).
- `notify.Peer`/`cluster.ClusterPeer` — nil-интерфейс без кластера.
- Рендер чарта с умолчаниями не меняется (существующие `tests/render-*.sh` зелёные без правок).

## Edge Cases

- `Join` без доступных пиров (первый под StatefulSet) — Warn, memberlist переподключается (`ReconnectInterval`); `Settle` ограничен `SettleTimeout`.
- Неверный `--cluster.listen-address` — ошибка старта (`unable to initialize gossip mesh`), как у upstream.
- `replicas: 2` — split-brain не страшен: upstream предпочитает дубль потере (at-least-once).
- Масштабирование StatefulSet меняет список `--cluster.peer` → перекат подов; memberlist пиров находит и без полного списка.

## Impact Analysis

- Срез 5: переводит Service/Ingress/ServiceMonitor/NetworkPolicy/PDB на upstream-нагрузку, удаляет Deployment своего ядра, Redis-HA и записи BUGS/TECH-DEBT «срез 5».
- Срез 2b: `PostStore` будет срабатывать на каждой реплике — нужен общий dedup investigation (Redis уже есть) — follow-up `UPSTREAM-AI-HA-DEDUP`.
- Конфликты мержа: `upstreamcore/{core,reloader,doc}.go`, `cmd/amp-upstream/main.go` (флаги), `_helpers.tpl`/`values.yaml` (дописывание в конец).

## Rollout / Rollback

- Чарт: `upstreamCore.enabled=false` по умолчанию — для существующих установок ничего не меняется. Включение — новый StatefulSet и PVC; откат — `enabled=false` (PVC от `volumeClaimTemplates` остаются, удаляются вручную).
- Риск P6 (advertise-адрес): если memberlist не соберётся, `/api/v2/status` покажет 1 пира — рендер-тест не ловит, ловит выкатка в k3s; фикс — `extraArgs`.
- Риск P7 (конфликты со срезами): правки локальные, при мерже — по месту.
- Риск: `e2e-ha` больше не проверяет Redis-HA своего ядра (код живёт до среза 5) — осознанно, ядро уходит.

## Observability

- Метрики upstream `alertmanager_cluster_*` (`enabled`, `members`, `health_score`, `messages_*`, `peer_info`, `alertmanager_peer_position`) регистрируются `cluster.Create` в переданном `Registerer` — бесплатно.
- Логи `component=cluster` (settle, join/leave пиров).

## Deep Review

- Triggers: 3 сигнала `C X R` → **mandatory** (Full tier); плюс diff > 200 строк и первое применение `cluster.Peer`.
- Decision: `required`.

## Open Questions

- Нет блокирующих. `assumed` P6, P7 — в Rollout как риски.
