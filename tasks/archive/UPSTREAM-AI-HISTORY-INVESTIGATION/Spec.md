---
id: UPSTREAM-AI-HISTORY-INVESTIGATION
slug: upstream-ai-history-investigation
stream: Architecture / ADR-019
type: feature
status: approved
created_at: 2026-11-08
updated_at: 2026-11-08
based_on:
  - requirements.md
  - research.md
---

# Specification: история и investigation на upstream-ядре (срез 2b ADR-019)

**Version:** 1.0
**Status:** Approved (автономный прогон конвейера по поручению сессии-владельца эпика 2026-11-08)

## Summary

`amp-upstream` с AMP-конфигом пишет историю всех алертов в PostgreSQL отдельным колбэком хранилища (неблокирующая очередь, шарды по fingerprint, `DeduplicationService` — та же семантика и тот же ключ SHA-256, что у `cmd/server`). Классификатор после результата ставит investigation; алерты, заглушённые silence или inhibition, не классифицируются; пропуск запоминается на интервал перепроверки (1m), и `Put` после него проверяет mute заново — после снятия mute алерт классифицируется. `Put` уже классифицированного алерта повторно отдаёт его в очередь investigation (как каждый ingest `cmd/server`). `PreStore` ничего не отвергает. Схема БД не меняется.

## Requirements Coverage

| Requirement / Criterion | Covered by |
|---|---|
| `PostStore` без IO, очередь, шарды, дроп с метрикой | `History.PostStore`, Invariants 1–2 |
| История firing/resolved, включая заглушённые, через dedup | `History`, `toCoreAlert`, Edge Cases 1–3 |
| Один ключ SHA-256, FNV из labels | `toCoreAlert`, Contracts § Fingerprint |
| Investigation после классификации, firing, по флагам | `Classifier.classify`, `cmd/amp-upstream/amp.go` |
| Пропуск silence ∨ inhibition | `upstreamcore` mutes, Edge Case 5 |
| Повтор после снятия mute (перепроверка 1m) | `Classifier.classify`/`PostStore`, Edge Case 6 |
| `PreStore` без отказов | Invariant 3 |
| Без флага — как сейчас; PG недоступен — без истории | `amp.go`, Rollout |

## Current State

- **Code:** `internal/upstreamai` (2a: `Classifier`, `NotifyStage`), `internal/upstreamcore` (порт `app` v0.34.1, `Options.NewAlertCallback(silencer.Mutes)`), `cmd/amp-upstream/main.go` (`newClassifier` из `llm.*`). История и investigation — только в `cmd/server`.
- **Data:** таблицы `alerts` (ключ `fingerprint` VARCHAR(64), SHA-256) и `alert_investigations` — без изменений.
- **Tests:** `upstreamai/*_test.go`, `upstreamcore/core_test.go`, контрактные сьюты `futureparity && upstreamcore`, `scripts/upstream-core-gate.sh`.
- **Docs:** `docs/UPSTREAM_UPGRADE.md`, `upstreamcore/doc.go`, `config.yaml.example`, `CHANGELOG.md`.

## Design Premises

| Premise | Confirmed by | Class | If wrong |
|---|---|---|---|
| `PostStore` вызывается под `a.mtx` провайдера синхронно для каждого успешно сохранённого алерта (и resend'а) | `provider/mem/mem.go@v0.34.1:302-348` | code-read | блокирующий колбэк остановил бы ingest |
| История `cmd/server` — upsert строки `alerts` через `DeduplicationService.ProcessAlert` по `alert.Fingerprint` | `deduplication.go:203-420`, `postgres_storage_adapter.go:52` | code-read | строки двух бинарей разошлись бы |
| Ключ БД `cmd/server` для Prometheus push (`/api/v2/alerts`) — `alertconv.Fingerprint` (SHA-256) от labels; webhook-парсеры берут `fingerprint` из payload, если он есть (Alertmanager webhook — FNV), и только без него считают SHA-256 | `handlers/alerts.go:540`; `webhook/parser.go:123-126`, `prometheus_parser.go:280` | code-read | история `amp-upstream` и `cmd/server` не совпала бы по ключу для push; для webhook-ingest ключи и так разные — `amp-upstream` webhook не принимает |
| `model.LabelSet.Fingerprint()` upstream == `alertconv.UpstreamFingerprint(labels)` | `alertconv.go:116-133` (тот же вызов `prometheus/common`) | code-read | поиск по FNV через labels дал бы другой ключ; закрепляется тестом |
| `InvestigationQueue.Submit` неблокирующий, без IO, dedup по инциденту | `investigation/queue.go:146-178` | code-read | воркер классификатора ждал бы очередь |
| `reloader.inhibitor` установлен до первого `Put` (initial `ApplyConfig` в `New`) и меняется атомарно | `core.go:300-310`, `reloader.go:66` | code-read | `mutes` разыменовал бы nil — проверка `nil` в `inhibits` |
| Источник inhibition виден inhibitor'у к моменту проверки в воркере | inhibitor подписан на алерты асинхронно | assumed | цель, пришедшая в одном батче с источником, может классифицироваться — best-effort, Edge Case 5 |
| Prometheus пересылает firing-алерты периодически (resend), так что повтор на следующем `Put` реально наступает | поведение Prometheus `resend_delay` 1m | assumed | отправитель, шлющий алерт один раз, после снятия silence классификации не получит — Edge Case 6 |

## Target Design

1. **История.** `upstreamai.History` реализует `mem.AlertStoreCallback`. `PostStore` конвертирует алерт (`toCoreAlert`: SHA-256 fingerprint, статус `resolved`, если `a.Resolved()`, иначе `firing`) и неблокирующе кладёт в канал шарда `uint64(fp) % N` (N=4, буфер 256 на шард). Воркер шарда вызывает `Record(ctx, alert)` (в `amp-upstream` — `DeduplicationService.ProcessAlert`) с таймаутом 5s. Ошибка — Warn + метрика, без ретрая (следующий resend повторит). Переполнение шарда — метрика `dropped_queue_full` и один Warn на эпизод. `PreStore` — `nil`; `PostDelete` ставит событие, если `a.Resolved()` (алерт, разрешившийся по `EndsAt` без resolved-`Put`, иначе остался бы `firing`), тем же неблокирующим путём; `PostGC` — no-op (история переживает GC). Warn о переполнении снова взводится, когда шард опустел до половины. Dedup-сервис пишет в лог только с Warn: его Info на каждый алерт на ingest-пути — шум.
2. **Investigation.** `Classifier.Config.Investigations` (интерфейс `Submit(*core.Alert, *core.ClassificationResult)`, опционально). Воркер после результата `ClassifyAlert` (classified и fallback) вызывает `Submit(j.alert, res)` — тот же `*core.Alert` с SHA-256, что в истории.
3. **Mute.** В `upstreamcore.New` колбэк получает `mutes(ctx, lset) = silencer.Mutes(ctx, lset) || r.inhibits(ctx, lset)`; `reloader.inhibits` берёт `r.inhibitor.Load()` (nil → false). `var r *reloader` объявляется до `mem.NewAlerts` (замыкание, как `groupFn`).
4. **Пропуск с перепроверкой.** Заглушённый алерт: запись остаётся с `recheckAt = now + 1m`, `done` закрывается, `result` nil, outcome `skipped_muted`. `Put` до `recheckAt` ничего не ставит (без слота очереди на каждый resend), после — создаёт новое задание. Notify-стадия для такой записи не ждёт (`done` закрыт, результата нет). `Put` классифицированного алерта (`done`, `result` есть) вызывает `Submit` повторно: dedup очереди держит одно investigation на инцидент, повторяет отброшенное и ставит новое на новый `StartsAt`.
5. **Композиция.** `upstreamai.Chain(cbs ...mem.AlertStoreCallback)` — последовательный вызов; `PreStore` — первая ошибка.
6. **Wiring `amp-upstream`.** Новый `cmd/amp-upstream/amp.go`: `newAMPLayer(logger, path, *opts) (stop func(), err error)` — грузит AMP-конфиг; при `profile: standard` подключает PostgreSQL (`PostgresConfigFrom`, connect ограничен `database.connect_timeout`) и `RunMigrations` (контекст 1m, включая ожидание advisory lock), при ошибке — Error и без PG; строит `History` над `PostgresStorageAdapter` + `DeduplicationService`; при `llm.enabled` — классификатор (код 2a переезжает сюда без изменений) и, если `investigation.enabled` и есть PG, `InvestigationQueue` с общим LLM-клиентом (`llm.agent_mode` — Warn, обычный investigation). `opts.NewAlertCallback` собирает `Chain(history, classifier)` из того, что построено. `stop` в обратном порядке: классификатор → investigation (`Stop(5s)`, как `cmd/server`) → история → пул. `main.go` меняет только вызов.

## API Contracts

### Fingerprint

- Ключ строк `alerts`, `alert_investigations` и кеша классификации — `alertconv.Fingerprint(labels)` (SHA-256, 64 hex), как у `cmd/server`.
- FNV upstream (`api/v2`, 16 hex) — `alertconv.UpstreamFingerprint(labels)` == `alert.Fingerprint().String()`. Поиск по FNV — через labels (живой алерт: `Alerts.Get`; ушедший: labels строки истории). Индекс по FNV — открытый вопрос среза 6.

### Go: `upstreamcore.Options.NewAlertCallback`

- Сигнатура не меняется. `mutes` теперь истинна для алерта, заглушённого silence **или** inhibitor'ом активной конфигурации.

### Config / flags

- Новых ключей и флагов нет. `--amp.config.file` с `profile: standard` (дефолт AMP-конфига) включает историю — `database.*` берутся из того же файла; `profile: lite` — без истории (Info).

### Metrics

- Новая: `amp_upstream_history_writes_total{outcome="written|error|dropped_queue_full"}`.
- `amp_upstream_ai_classifications_total`: outcome `skipped_silenced` → `skipped_muted` (silence или inhibition, один раз на интервал перепроверки). Бинарь не выпускался — ломать некому, запись в CHANGELOG.
- Метрики investigation (`amp_investigation_*`) и пула PG — как в `cmd/server`.

## Component Architecture

- `go-app/internal/upstreamai/history.go` — `History`, `HistoryConfig`, `NewHistory`, `Stop`, колбэк, воркеры, метрика.
- `go-app/internal/upstreamai/chain.go` — `Chain`.
- `go-app/internal/upstreamai/classifier.go` — `Config.Investigations`, `Submitter`, пропуск без записи, outcome.
- `go-app/internal/upstreamai/convert.go` — статус по `Resolved()`.
- `go-app/internal/upstreamcore/core.go`, `reloader.go`, `doc.go` — mutes с inhibitor.
- `go-app/cmd/amp-upstream/amp.go` (новый), `main.go` (вызов).
- `docs/UPSTREAM_UPGRADE.md`, `config.yaml.example`, `CHANGELOG.md`.

## Security Design

- [x] Ownership validation — not applicable.
- [x] Input validation — not applicable (новых входов нет; labels уже валидированы `api/v2`).
- [x] Sensitive data not logged — DSN/пароль не логируются (логируются host/database, как `cmd/server`); логи истории — alertname и fingerprint.
- [x] Rate limiting — очереди истории (4×256), классификации (1024) и investigation (`queue_size`); пул PG `max_connections`.
- [x] Auth/RBAC — not applicable.

## Invariants

- [ ] 1. `PostStore`/`PostDelete`/`PostGC` всех AMP-колбэков не блокируются и не делают IO.
- [ ] 2. Приём и доставка не зависят от PostgreSQL во время работы; на старте ожидание ограничено `database.connect_timeout` + 1m на миграции, после чего ядро стартует без истории.
- [ ] 3. `PreStore` AMP-колбэков всегда `nil`.
- [ ] 4. События одного fingerprint пишутся в историю в порядке `Put`.
- [ ] 5. Без `--amp.config.file` `upstreamcore` ведёт себя как сейчас; контрактные сьюты и гейт зелёные.
- [ ] 6. Ключ истории и investigation одного алерта совпадает и равен ключу `cmd/server`.

## Edge Cases

1. Resend firing без изменений → dedup: Get + Update (`EndsAt` сдвигается) — как `cmd/server`; нагрузка — один-два запроса на resend.
2. Resolved → запись `resolved` с `EndsAt`; повторный firing после resolve — новый инцидент (`StartsAt`), dedup-логика `cmd/server`.
3. Шард полон → событие теряется, метрика; следующий resend догонит состояние (кроме последнего resolved, если Prometheus больше не шлёт — known gap, метрика).
4. PG недоступен во время работы → ошибки записи (метрика `error`, Warn), доставка не страдает; на старте → без истории до рестарта (TECH-DEBT `UPSTREAM-HISTORY-PG-RECONNECT`).
5. Inhibition: цель с активным источником → `skipped_muted`; источник, ещё не дошедший до inhibitor'а, → цель классифицируется (best-effort).
6. Silence снят → первый `Put` после `recheckAt` классифицирует (задержка до 1m); алерт, отправленный один раз, — нет.
7. Классификация с ошибкой → как в 2a (запись удаляется), investigation не ставится.
8. `investigation.enabled` без PG или без `llm.enabled` → investigation нет, Info/Warn в лог. `investigation.only_firing: false` не действует (resolved не классифицируются, значит и не расследуются) — Info на старте.
9. Stop → классификатор останавливается первым (новых `Submit` нет), затем очередь investigation с таймаутом, затем история (незаписанное в очереди теряется), затем пул.

## Impact Analysis

- **Affected modules:** `upstreamai`, `upstreamcore` (mutes), `cmd/amp-upstream`.
- **Breaking changes:** `amp-upstream` с `--amp.config.file` и дефолтным `profile: standard` теперь подключается к PostgreSQL (Error и работа без истории, если PG нет); outcome `skipped_silenced` → `skipped_muted`. Бинарь не в Helm и не выпускался.
- **New dependencies:** none (pgx, goose уже в модуле).
- **Risks:** правка `core.go` пересекается со срезом 4 (HA) — 6 строк, отмечено в `doc.go`; оба бинаря рядом пишут одни строки — семантика одинаковая.

## Rollout / Rollback

- **Rollout:** `amp-upstream` не выкатывается (Helm — срез 5).
- **Rollback:** `profile: lite` или без `--amp.config.file` — без истории; `investigation.enabled: false`.
- **Feature flag:** `--amp.config.file`, `profile`, `investigation.enabled`, `llm.enabled`.

## Observability

- **Logs:** Info на старте (history on/off, investigation on/off, причина); Error — PG недоступен на старте; Warn — ошибка записи истории, один на эпизод переполнения шарда; Warn — `agent_mode` не поддержан.
- **Metrics:** см. API Contracts § Metrics.
- **Alerts:** not applicable (правила — срез 5/7).

## Deep Review

- **Mandatory triggers present:** 3 сигнала (`C X R`).
- **Discretionary triggers present:** правка порта upstream при параллельном срезе.
- **Decision:** required.

## Open Questions

- [ ] Индекс по FNV (`upstream_fingerprint`) для поиска истории/investigation — срез 6 `UPSTREAM-API-UI`.
- [ ] Переподключение к PG после неудачного старта — TECH-DEBT.
- [ ] Agent mode investigation в `amp-upstream` — срез 5 (разбор `service_registry`).
- [ ] Общий dedup investigation между репликами (Redis) — срез 4.
