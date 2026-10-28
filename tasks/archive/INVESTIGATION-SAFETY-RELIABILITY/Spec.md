---
id: INVESTIGATION-SAFETY-RELIABILITY
slug: investigation-safety-reliability
stream: Investigation / LLM
type: bug
status: approved
created_at: 2026-10-28
updated_at: 2026-10-28
based_on:
  - requirements.md
  - research.md
---

# Specification: надёжность LLM-вызовов и очереди расследований

**Version:** 1.2\
**Status:** Approved

## Summary

LLM-ответы 429/5xx начинают ретраиться, `llm.max_retries: 0` отключает ретраи, тело ответа LLM обрезается до 512 байт в логах и ошибках. Очередь расследований перестаёт ходить в БД из горутины запроса, дедуплицирует расследования по инциденту (`fingerprint+StartsAt`) и ограничивает вывод tools.

## Requirements Coverage

| Requirement / Criterion | Covered by |
|---|---|
| 429/5xx ретраятся, 4xx — нет | Target Design §1, `llm/errors.go` |
| `max_retries: 0` = одна попытка, незаданный = 3 | §1, `resilience.WithRetryFunc` |
| тело ответа LLM ≤ 512 байт в логах/ошибках | §1, `llm/client.go` |
| `Submit` без БД и без блокировки | §2, `investigation/queue.go` |
| дедуп `fingerprint+StartsAt` с TTL, межреплично при Redis, метрика | §3, `queue.go`, `dedup.go`, wiring |
| лимиты вывода tools | §4, tools + `agent_loop.go` |
| CHANGELOG | Rollout |

## Current State

- **Code:** `llm/client.go` (`classifyAlertProxy` :319/323, `classifyAlertOpenAI` :419, `investigate*` :608), `llm/errors.go:26,41`, `core/resilience/resilience.go:55`, `investigation/queue.go:122-160`, `tools/{kubernetes.go:166-190,loki.go:82-118,prometheus.go:107}`, `core/investigation/agent_loop.go:205-225`, `application/service_registry.go:2209-2300`.
- **Data:** `alert_investigations` — без изменений.
- **Tests:** `llm/*_test.go`, `investigation/queue_test.go`, `tools/*_test.go`, `core/investigation/agent_loop_test.go`.
- **Docs:** `CHANGELOG.md` `[Unreleased]`, `config.yaml.example` (`llm.max_retries`), `internal/infrastructure/llm/README.md`.

## Design Premises

| Premise | Confirmed by | Class | If wrong |
|---|---|---|---|
| `WithRetryFunc` вызывается только из LLM-классификации | grep `WithRetryFunc` по модулю: `llm/client.go:216` | call-path-traced | другой потребитель потерял бы дефолт 3 ретрая |
| Незаданный `llm.max_retries` приходит как 3 | `config.go:933` `SetDefault`, `llm.DefaultConfig()` | code-read | без ключа ретраи бы пропали |
| `classifyError` опирается на текст `HTTP %d` | `queue.go:334-355` | code-read | переклассификация permanent/transient |
| Статус `queued` читает только `GET /api/v1/alerts/{fingerprint}/investigation` | grep `InvestigationQueued`, `GetLatestByFingerprint`: `handlers/investigation.go:37` | call-path-traced | UI/клиент увидел бы 404 вместо `queued` |
| `r.cache` создан до `initializeInvestigation` | `service_registry.go` порядок `Initialize`: cache — шаг 1, investigation — после business services | code-read | Redis-дедуп не включится, останется локальный |
| Resend одного инцидента сохраняет `StartsAt`, новый инцидент получает новый | Resend: dedup ядра возвращает сохранённый алерт (`deduplication.go`). Re-fire после resolve шёл через `updateExistingAlert` со старым `StartsAt` (deep-review F1) — исправлено там же | code-read | без правки ядра re-fire глушился бы дедупом 24h |

## Target Design

1. **Ретраи LLM.** `llm.IsRetryableError` сначала проверяет `errors.As(err, *HTTPError)`: `429` или `>=500` → true, остальные коды → false; иначе — прежний `LLMClassifier`. `WithRetryFunc` больше не подменяет `MaxRetries==0` (отрицательное → 0). Тело ответа LLM в `logger.Error` и `HTTPError.Message` — `truncateBody(body, 512)` с маркером усечения; формат `HTTP %d: …` сохраняется.
2. **`Submit`.** Только фильтр `OnlyFiring`, локальный dedup-claim и неблокирующая отправка в канал. Переполнение → `DroppedTotal`, лог, освобождение локального claim; записи в БД нет. `processJob` первым делом делает межрепличный claim и `repo.Create` (с таймаутом), затем прежний путь (`UpdateStatus(processing)` …). Ошибка `Create` → job отбрасывается, `DroppedTotal`, оба claim снимаются (F3). Shared-claim ставится на in-flight TTL — худшая длительность job по конфигу очереди (`(max_retries+1)×(llm_timeout+60s)` + agent loop + 5m запаса; ~15m на дефолтах) — и по исходу перезаписывается `SET … EX 24h` (F14), когда job дошёл до исхода; job, прерванный `Stop` или падением процесса, оставляет только короткий claim (F13). Claim и `Create` — с раздельными таймаутами (F8).
3. **Дедуп.** Ключ — `fingerprint + StartsAt` в целых секундах (хранилище держит микросекунды, F6) `+ status` (при `only_firing: false` resolve расследуется отдельно, F7). Re-fire после конца инцидента (resolve или `EndsAt` раньше нового `StartsAt`, F12) берёт новый `StartsAt` в `updateExistingAlert` (F1). Тип `dedup` в `investigation/dedup.go`: in-memory map ключ → срок (мьютекс, ленивое вычищение просроченных при вставке) и опциональный `redis.Cmdable`. `claimLocal(key)` — в `Submit`; `claimShared(ctx,key)` — в воркере: `SETNX amp:investigation:dedup:<key> TTL 24h`; `false` → job пропускается; ошибка Redis → fail-open (расследуем) + Warn. Без Redis (`r.cache` не `*RedisCache`) — только локальный уровень. Метрика `amp_investigations_deduplicated_total{level="local|shared"}`. Wiring: `NewInvestigationQueue` не меняет сигнатуру; `SetSharedDedup(redis.Cmdable)` до `Start()`, как `SetAgentLoop`.
4. **Лимиты tools.** Общая константа `maxToolResponseBytes = 256 KiB` в пакете `tools`. Kubernetes `get_logs`: `tail_lines` clamp `[1,500]`, чтение через `io.LimitReader(limit+1)` — при превышении обрезка + маркер. Loki: `limit` clamp `[1,500]`, тело через `LimitReader(limit+1)` — при превышении `IsError` «response exceeds 256KiB, narrow the query». Prometheus — то же для тела. `AgentLoop`: observation обрезается до `maxObservationBytes = 16 KiB` (по границе руны, маркер с просьбой сузить запрос) до записи в `steps` и `history`. Логи pod — хвост 8 KiB всего окна `tail_lines` (чтение до 4 MiB, `tailBuffer`, F11), чтобы кэп не срезал свежие строки (F9). Hot reload: `llm.max_retries: 0` применяется (F2).

## API Contracts

Не применимо: HTTP API не меняется. Поведенческое изменение `GET /api/v1/alerts/{fingerprint}/investigation` (нет `queued` до взятия job воркером) — в CHANGELOG.

## Data Model / Migrations

Не применимо: схема не меняется.

## Component Architecture

- `go-app/internal/infrastructure/llm/errors.go` — ретраибельность `*HTTPError`.
- `go-app/internal/infrastructure/llm/client.go` — `truncateBody`, усечение в 3 местах (+ лог).
- `go-app/internal/core/resilience/resilience.go` — убрать подмену `MaxRetries`.
- `go-app/internal/infrastructure/investigation/{queue.go,dedup.go,metrics.go}` — `Submit`, `processJob`, dedup, метрика.
- `go-app/internal/infrastructure/investigation/tools/{kubernetes.go,loki.go,prometheus.go}` — лимиты.
- `go-app/internal/core/investigation/agent_loop.go` — кэп observation.
- `go-app/internal/application/service_registry.go` — `SetSharedDedup(redisCache.ShareClient())`.

## Security Design

- [x] Ownership validation — не применимо.
- [x] Input validation: `tail_lines`/`limit` от LLM — clamp; тела внешних ответов — лимит.
- [x] Sensitive data not logged: тело ответа LLM ≤ 512 байт; данные алерта в новые логи не добавляются (только fingerprint).
- [x] Rate limiting: дедуп ограничивает расходы LLM на инцидент.
- [x] Auth/RBAC — не применимо.

## Invariants

- [ ] `Submit` не выполняет IO и не блокирует.
- [ ] Незаданный `llm.max_retries` → 3 ретрая (как раньше).
- [ ] Ошибка Redis не отменяет расследование (fail-open), не роняет воркер.
- [ ] `classifyError` классифицирует ответы как раньше.
- [ ] Resolved-алерты и `OnlyFiring` — как раньше; resolve не освобождает claim (новый инцидент — новый `StartsAt`).

## Edge Cases

1. Тот же инцидент приходит на две реплики с общим Redis → одна выигрывает `SETNX`, вторая пропускает (`level=shared`).
2. Очередь полна → drop, локальный claim снят: следующий resend снова попробует.
3. Job упал (DLQ/failed) → claim держится TTL (F10: agent loop без ретраев — defer): повторного расследования на resend нет (бюджет важнее). Ручной повтор — вне скоупа.
4. `StartsAt` нулевой → ключ `fp:0`, дедуп по fingerprint на 24h.
5. Рестарт процесса → локальная карта пуста; при Redis claim завершённого job переживает рестарт (24h), job, прерванного рестартом, — только in-flight TTL, после него инцидент снова открыт.
6. Тело LLM < 512 байт — без маркера; ≥ — обрезано по границе руны.
7. Loki/Prometheus ответ > 256 KiB → tool-ошибка, LLM сужает запрос; Kubernetes логи > 256 KiB → обрезка с маркером.

## Impact Analysis

- **Affected modules:** llm, core/resilience, infrastructure/investigation (+tools), core/investigation, application wiring.
- **Breaking changes:** `llm.max_retries: 0` теперь означает «без ретраев» (было 3). Статус `queued` не виден до взятия job воркером.
- **New dependencies:** none (`go-redis` уже в модуле, `miniredis` — в тестах).
- **Risks:** ретраи 429/5xx удлиняют синхронный ingest на время backoff, пока не откроется CB (F4, defer — `UPSTREAM-AI-HOOKS` выносит LLM из ingest); рычаг — `llm.max_retries`. `ShareClient()` помечает Redis-клиент общим — hot reload `redis.*` откажется менять клиент; при Redis так уже делает `initializeSilenceGCElection`, нового ограничения нет. Fail-open дедупа при недоступном Redis → дубли между репликами (как сейчас). Дедуп глушит повтор упавшего расследования на 24h — осознанный компромисс.

## Rollout / Rollback

- **Rollout:** обычный релиз; CHANGELOG `[Unreleased]` Fixed + Changed с migration note для `max_retries: 0`. Риск `assumed`-предпосылки про `StartsAt` — fallback «как сейчас».
- **Rollback:** откат коммитов; данных не меняет. Ключи `amp:investigation:dedup:*` истекают сами (24h).
- **Feature flag:** не применимо.

## Observability

- **Logs:** Warn при drop (queue full / Create failed), Warn при ошибке Redis-дедупа, Debug при дедупе.
- **Metrics:** `amp_investigations_deduplicated_total{level}`; `amp_investigations_dropped_total` — также при ошибке `Create`.
- **Alerts:** не применимо.

## Deep Review

- **Mandatory triggers present:** `S`, 3 сигнала (`S X R`).
- **Discretionary triggers present:** вероятный дифф > 200 строк.
- **Decision:** required.

## Open Questions

- [x] Предпосылка `assumed`: resend сохраняет `StartsAt` — подтверждена тестами `TestSubmit_ResendOfIncidentDeduplicated`, `TestDedupKey_SecondsAndStatus`; клиенты, меняющие `startsAt`, остаются без дедупа (не регресс).
