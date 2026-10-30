---
id: UPSTREAM-AI-CLASSIFY
slug: upstream-ai-classify
stream: Architecture / ADR-019
type: feature
artifact: research-pack
status: complete
created_at: 2026-10-30
updated_at: 2026-10-30
---

# Research Pack - AI-классификация на upstream-ядре

## 0) TL;DR

- Solving: классификация вне ingest-пути и в шаблонах группы на `amp-upstream`.
- Found: единственные швы upstream — `AlertStoreCallback` (вызывается под `a.mtx` провайдера) и `notify.RoutingStage` (обычная map, оборачивается в `reloader.go`).
- Chose: `PostStore` ставит firing-алерт в ограниченную очередь воркеров; стадия-обёртка перед pipeline получателя ждёт результат с бюджетом и кладёт `llm_*` в клоны аннотаций.
- Risks: ожидание стоит до mute-стадий (ждём и для заглушённых inhibition); сдвиг порта upstream.
- Next step: -> spec

## 1) Questions

1. Где можно вызвать LLM, не блокируя `Put`?
2. Где ждать классификацию, чтобы она попала в шаблоны группы?
3. Как не тратить LLM на заглушённые алерты?
4. Как соблюдать `Retry-After`, не трогая общий `pkg/retry`?

## 2) Findings

- **Codebase:**
  - `provider/mem.Alerts.Put` держит `a.mtx` на весь цикл и вызывает `PreStore` → `Set` → `PostStore` (mem.go:302-348): колбэк обязан быть неблокирующим.
  - `silence.Silencer` — нынешний колбэк; `PreStore`/`PostStore`/`PostDelete` пустые, `PostGC` чистит кеш (silence.go:325). Композиция «silencer, затем свой» безопасна.
  - `PipelineBuilder.New` возвращает `RoutingStage map[string]Stage`, каждая — `MultiStage{gossipSettle, inhibit, timeActive, timeMute, silence, receiver}` (notify.go:163). Receiver-стадия и метрики builder'а не экспортированы: пересобрать цепочку с AI-стадией после mute нельзя без дубля регистрации метрик.
  - `MultiStage.Exec` прекращает цепочку на пустом списке (notify.go:253) — дроп всех алертов группы безопасен.
  - `aggrGroup.flush` делает `a := *alert`: `Annotations` общие со store — менять только клон map. Dedup/nflog ключуются fingerprint'ом (labels), аннотации на них не влияют.
  - Стадия выполняется под таймаутом `max(group_interval, notify.MinTimeout=10s)` (core.go:262).
  - `Silencer.Mutes(ctx, model.LabelSet) bool` (silence.go:165) — тот же вызов делает API upstream при выдаче статуса.
  - `ClassificationService.ClassifyAlert(ctx, *core.Alert)`: L1/L2 кеш по `Fingerprint`, LLM с `LLMTimeout`, fallback на правила (classification.go:176-262). Требует `cache.Cache` (memory допустим).
  - LLM `HTTPError{StatusCode, Message}` без заголовков; ретраи — `resilience.WithRetryFunc` → `pkg/retry.Do` с собственной задержкой (client.go:225).
- **Constraints:** порт upstream меняется минимально; без AMP-конфига `amp-upstream` — чистый upstream (контрактные сьюты, `upstream-core-gate.sh`).
- **Unknowns:** inhibition до классификации не проверяется (inhibitor пересоздаётся на каждый reload) — переносится как known gap.

## 3) Options

`Generation:` single-pass (донор очевиден: `formatAlertmanager` для аннотаций, `initializeClassification` для сборки)

### Option A - стадия-обёртка перед pipeline получателя

- **Pros:** одна правка в `reloader.go` (`rs[name] = MultiStage{ai, rs[name]}`); upstream-цепочка нетронута; время ожидания ограничено и измеримо.
- **Cons:** ждёт до mute-стадий — для алертов под inhibition ожидание до бюджета, если классификация ещё идёт.
- **Cost/Risk:** low

### Option B - пересобрать цепочку, AI после mute

- **Pros:** не ждёт заглушённые группы.
- **Cons:** receiver-стадия не экспортирована, `NewMetrics` повторно регистрирует метрики; порт расходится с upstream сильнее; бамп upstream дороже.
- **Cost/Risk:** high

### Option C - классификация синхронно в `PreStore`/`PostStore`

- **Pros:** классификация готова к flush.
- **Cons:** блокирует `Put` под мьютексом провайдера — ровно проблема `PROD-LLM-ALERT-PATH-ISOLATION`, только хуже.
- **Cost/Risk:** high (отвергнут)

### Retry-After

- A: поле `RetryAfter` в `llm.HTTPError`; checker отказывается ретраить, если `RetryAfter > MaxDelay`; перед повтором — дождаться остатка `Retry-After`. Локально в `llm`.
- B: поддержка в `pkg/retry.Strategy` — затрагивает все потребители. Отвергнут по scope.

## 4) Decision

- **Chosen:** Option A + Retry-After A; silence-проверка в воркере перед LLM-вызовом; статус «пропущен» снимает ожидание мгновенно.
- **Why:**
  - `PostStore` остаётся O(1) и неблокирующим (требование 1).
  - Группа получает классификацию через стандартные шаблоны upstream (`.Alerts[].Annotations`, `.CommonAnnotations`) — закрывает `GROUP-NOTIFY-CLASSIFICATION`.
  - Минимальный сдвиг порта: два опциональных поля `Options` и одна обёртка в reload.
  - Задержка уведомления ограничена `llm.notify_wait` и половиной таймаута стадии.

## 5) Spec Inputs

- API/contracts: ключи `llm.notify_wait` (default 5s), `llm.drop_noise` (default false); флаг `--amp.config.file`; аннотации `llm_*`; `upstreamcore.Options.NewAlertCallback`, `Options.NotifyStage`.
- Data model/migrations: not applicable.
- Rollout/rollback: опционально по флагу; откат — убрать флаг.
- Observability: `amp_upstream_ai_classifications_total{outcome}`, `amp_upstream_ai_notify_waits_total{outcome}`, `amp_upstream_ai_noise_dropped_total`.
- Security/ownership/RBAC: секреты не логируются; `llm_reasoning` ≤ 500 символов.

## 6) References

- Files: `go-app/internal/upstreamcore/{core.go,reloader.go,doc.go}`, `go-app/internal/core/services/classification.go`, `go-app/internal/infrastructure/llm/{client.go,errors.go}`, `go-app/internal/infrastructure/publishing/formatter.go:272-318`.
- Upstream: `github.com/prometheus/alertmanager@v0.34.1/{provider/mem/mem.go,notify/notify.go,silence/silence.go,dispatch/dispatch.go}`.
