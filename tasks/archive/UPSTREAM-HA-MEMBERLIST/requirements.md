---
id: UPSTREAM-HA-MEMBERLIST
slug: upstream-ha-memberlist
stream: Architecture / ADR-019
type: feature
priority: high
status: done
created_at: 2026-11-08
updated_at: 2026-11-08
---

# Requirements: HA upstream-ядра на memberlist (срез 4 ADR-019)

## Problem Framing

- **Symptom:** `cmd/amp-upstream` (upstream-ядро, срезы 1–2a) работает только одной репликой: в `internal/upstreamcore` нет `cluster.Peer`, silences и nflog не реплицируются, `alertmanager_cluster_enabled` = 0, `/api/v2/status` без `cluster`. Две реплики за одним Service шлют каждое уведомление дважды и не видят silences друг друга. В чарте `helm/amp` upstream-ядра нет вовсе: образ собирает только `cmd/server`, HA своего ядра держится на Redis (lock, heartbeat, группы, таймеры, nflog — ADR-016/017). `deploy/e2e-ha` проверяет Redis-HA своего ядра.
- **Root Cause:** порт upstream `app` (срез 1a) намеренно вырезал gossip-кластер до этого среза (`upstreamcore/doc.go` § Known differences).
- **Why Now:** срез 4 эпика ADR-019, взят в WIP 2026-11-08 параллельно со срезами 2b и 3. Без него срез 5 (удаление своего ядра и Redis-HA) не может стартовать, а релиз v0.1.0 — выкатиться в собственный k3s.
- **How We Measure:** тест на двух `upstreamcore.Core` в одном процессе (silence с одной реплики виден на другой; один алерт на обе реплики — одно уведомление в webhook); `deploy/e2e-ha/run.sh` на двух контейнерах `amp-upstream` с реальным webhook-приёмником; `helm template` с `upstreamCore.enabled` для 1 и 3 реплик.

## Risk Profile

- **Signals:** `C X R`
  - `C` — новые флаги `amp-upstream` (`--cluster.*`, имена и умолчания upstream), новые поля `upstreamcore.Options`, новые values чарта (`upstreamCore.*`), `/api/v2/status` отдаёт `cluster`, образ содержит второй бинарь.
  - `X` — `internal/upstreamcore`, `cmd/amp-upstream`, `Dockerfile`, `helm/amp`, `deploy/e2e-ha`, CI.
  - `R` — путь доставки в HA (дедупликация уведомлений по nflog и позиции пира), сетевой порт gossip, PVC со снапшотами.
- **Tier:** Full
- **Notes:** три сигнала → Full (так и заявлено в BACKLOG). `S` нет: gossip без TLS — как у upstream по умолчанию, порт не публикуется наружу; TLS gossip доступен флагом upstream `--cluster.tls-config`. `M` нет: снапшоты silences/nflog — формат upstream; перенос silences из PG своего ядра — срез 7 (migration notes), не здесь.

## User Stories

1. Как оператор, я хочу запустить N реплик `amp-upstream`, которые образуют кластер memberlist, чтобы потеря одной реплики не теряла уведомления и silences.
2. Как оператор, я хочу, чтобы Prometheus слал алерты во все реплики, а уведомление уходило один раз (как у upstream Alertmanager).
3. Как оператор, я хочу, чтобы silences и nflog переживали рестарт пода (снапшоты на PVC).
4. Как оператор k3s на одной ноде, я хочу, чтобы чарт по умолчанию рендерился и ставился как раньше, а upstream-ядро включалось одним флагом и с одной репликой работало без кластера.

## Success Criteria

- [ ] `upstreamcore.Options` принимает настройки кластера (подмножество `app.Options` upstream); пустой адрес — кластер выключен, поведение как сейчас.
- [ ] С кластером: `cluster.Peer` создаётся, silences (`sil`) и nflog (`nfl`) регистрируются как состояния gossip до `Join`, `Settle` запускается, notify-конвейер получает пира (стадия ожидания settle + `waitFunc` по позиции), API — пира (`/api/v2/status` → `cluster`), `alertmanager_cluster_enabled` = 1, на `Stop` — `Leave`.
- [ ] `amp-upstream` принимает флаги `--cluster.*` upstream с теми же именами и умолчаниями.
- [ ] Тест: две реплики в одном процессе — silence, созданный на одной, виден на другой; алерт, отправленный в обе, даёт ровно одно уведомление в webhook.
- [ ] Образ содержит `/app/amp-upstream`.
- [ ] Чарт: `upstreamCore.enabled` (по умолчанию `false`) рендерит StatefulSet (`podManagementPolicy: Parallel`, PVC через `volumeClaimTemplates`), headless Service с портом gossip TCP+UDP 9094, ClusterIP Service для API, ConfigMap с `alertmanager.yml`. С одной репликой кластер выключен (`--cluster.listen-address=`), с N > 1 — пиры по DNS headless-сервиса. Рендер по умолчанию не меняется.
- [ ] `deploy/e2e-ha/run.sh` переписан под две реплики `amp-upstream` в memberlist: кластер из 2 пиров, gossip silence, одно уведомление на алерт в обе реплики, доставка после смерти реплики, silence переживает рестарт (снапшот); ожидания — опросом.
- [ ] Список мест, где Redis/Valkey нужен своему коду, — в `Spec.md` / итоге задачи; upstream-ядро от Redis не зависит.
- [ ] Гейты `WORKFLOW.md` § Гейты AMP зелёные, включая `scripts/release-gate.sh`.

## Non-Goals

- Удаление Redis lock/heartbeat/group/timer storage и nflog своего ядра — срез 5 (`UPSTREAM-CORE-REMOVAL`). Здесь достаточно, что upstream-ядро от них не зависит.
- Замена Deployment `cmd/server` в чарте на upstream-ядро, перенос Ingress/ServiceMonitor/NetworkPolicy/PDB/HPA на StatefulSet — срез 5, когда своё ядро удаляется.
- Helm-проводка AMP-конфига `amp-upstream` (`--amp.config.file`, LLM, PG истории) — срез 5 (решение 2a), PG-история — 2b.
- Redis L2-кеш классификации в `amp-upstream` — отдельная задача после эпика (здесь только инвентаризация).
- Перенос silences из PostgreSQL своего ядра в снапшот upstream — migration notes среза 7.
- TLS gossip в чарте — флаг upstream доступен, проводка в values не делается.

## Constraints

- **Scope:** порт upstream (`core.go`, `reloader.go`) меняется построчно по `app/app.go`/`app/reloader.go` v0.34.1, без своих изобретений; правки общего wiring (`upstreamcore`, `cmd/amp-upstream`, `Dockerfile`) — минимальные и локальные: параллельно идут срезы 2b и 3, ветки мержатся по очереди.
- **Чарт:** новые шаблоны отдельными файлами (`upstream-*.yaml`), существующие шаблоны не трогать; селекторы upstream-подов не пересекаются с селекторами `cmd/server` и Valkey.
- **Compatibility:** без `--cluster.listen-address` на непустой адрес поведение тестов и `upstream-core-gate.sh` прежнее; имена флагов — как у upstream.
- **Security:** gossip-порт только внутри кластера (headless Service, без Ingress); секретов в флагах нет.

## Discovery Notes

- Similar tasks: `tasks/archive/UPSTREAM-CORE-POC-WIRING/` (порт `app`), `tasks/archive/UPSTREAM-AI-CLASSIFY/` (точки расширения порта).
- Relevant patterns: `github.com/prometheus/alertmanager@v0.34.1/app/app.go:202-360,391-445` (кластер), `app/reloader.go:170-190` (пир в конвейере), `app/cluster.go` (`clusterWait`), `cmd/alertmanager/main.go:72-89` (флаги); чарт — `templates/redis-statefulset.yaml` (StatefulSet в этом чарте), `tests/render-*.sh` (рендер-тесты в `release-gate.sh`).
- Нарезка (правило «Slice», оценка 3–5d): 4a — кластер в `upstreamcore` и `amp-upstream`, тест двух реплик, инвентаризация Redis (~1.5d); 4b — образ, чарт, `e2e-ha`, CI (~1.5–2d). Обе — на ветке `feature/upstream-ha-memberlist`, последовательно.
- Open unknowns: нет блокирующих; форма включения в чарте и судьба записей «срез 4» в BUGS/TECH-DEBT — в `research.md`.
