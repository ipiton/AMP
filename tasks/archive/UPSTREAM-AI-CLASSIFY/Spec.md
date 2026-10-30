---
id: UPSTREAM-AI-CLASSIFY
slug: upstream-ai-classify
stream: Architecture / ADR-019
type: feature
status: approved
created_at: 2026-10-30
updated_at: 2026-10-30
based_on:
  - requirements.md
  - research.md
---

# Specification: AI-классификация на upstream-ядре (срез 2a ADR-019)

**Version:** 1.1 (по deep review F1, F2, F4–F6)
**Status:** Approved (автономный прогон конвейера по поручению владельца 2026-10-30)

## Summary

`amp-upstream` получает опциональный AI-слой: `AlertStoreCallback` ставит firing-алерты в асинхронную классификацию, notify-стадия перед pipeline получателя ждёт её в пределах бюджета и кладёт результат в аннотации `llm_*` копий алертов. Приём алерта LLM не ждёт; дроп шума — только за флагом. LLM-клиент соблюдает `Retry-After`.

## Requirements Coverage

| Requirement / Criterion | Covered by |
|---|---|
| `PostStore` неблокирующий, ограниченная очередь | `Classifier.PostStore`, Invariants 1–2 |
| Только firing, один раз на fingerprint, silence — пропуск | `Classifier.PostStore`, воркер, Edge Cases 1–4 |
| Ожидание ≤ `notify_wait` и ≤ половины таймаута стадии | `Classifier.NotifyStage` |
| `llm_*` в клонах аннотаций | `NotifyStage`, Invariant 3 |
| `drop_noise` (default false), метрика + лог | `NotifyStage`, Observability |
| `Retry-After` | `llm` client, Component Architecture |
| Без флага — чистый upstream | `upstreamcore.Options` nil-поля, Invariant 4 |
| Метрики | Observability |

## Current State

- **Code:** `internal/upstreamcore` (порт `app` upstream v0.34.1), `cmd/amp-upstream` (флаги upstream). AI-слой — только в `cmd/server` (`processEnriched`, синхронно).
- **Data:** not applicable (in-memory).
- **Tests:** `internal/upstreamcore/core_test.go` (ingest → группа → webhook), контрактные сьюты под тегами `futureparity && upstreamcore`, `scripts/upstream-core-gate.sh`.
- **Docs:** `docs/UPSTREAM_UPGRADE.md`, `internal/upstreamcore/doc.go`, `config.yaml.example`.

## Design Premises

| Premise | Confirmed by | Class | If wrong |
|---|---|---|---|
| `Put` вызывает `PostStore` под `a.mtx`, синхронно | `provider/mem/mem.go:302-348` | code-read | блокирующий колбэк остановил бы ingest и GC |
| `Silencer` как колбэк делает работу только в `PostGC` | `silence/silence.go:325-345` | code-read | композиция потеряла бы поведение silencer'а |
| `RoutingStage` — `map[string]Stage`, dispatcher принимает любую `notify.Stage` | `notify/notify.go:163-200`, `dispatch/dispatch.go:104` | code-read | обёртка не скомпилируется |
| flush передаёт копии алертов с общими map аннотаций | `dispatch/dispatch.go:911-930` | code-read | мутация без клона испортит store |
| nflog/dedup ключуются по fingerprint, не по аннотациям | `notify/notify.go` `DedupStage`, `hashAlert` | code-read | добавление `llm_*` давало бы повторные уведомления |
| `MultiStage` останавливается на пустом списке | `notify/notify.go:250-263` | code-read | дроп всех алертов отправил бы пустое уведомление |
| Классификация обычно готова до первого flush (`group_wait` по умолчанию 30s) | дефолт upstream `group_wait`; LLM-латентность — `assumed` | assumed | при `group_wait: 0` первое уведомление уходит без `llm_*` после бюджета — ожидаемо, метрика `timeout` |

## Target Design

Новый пакет `internal/upstreamai` с типом `Classifier`. Он реализует `mem.AlertStoreCallback`: `PostStore` для firing-алерта без записи в таблице создаёт запись `pending` и неблокирующе кладёт задание в канал (1024); при переполнении — запись не создаётся, метрика `dropped_queue_full`, следующий `Put` повторит. Четыре воркера проверяют silence (`Silencer.Mutes`) — заглушённый получает статус `skipped`, остальные классифицируются `ClassificationService.ClassifyAlert` (кеш, LLM, fallback на правила); результат пишется в запись, канал `done` закрывается. `PostDelete`/`PostGC` удаляют записи. `NotifyStage` перед каждой цепочкой получателя ждёт `pending`-записи группы до общего дедлайна `min(notify_wait, остаток таймаута стадии / 2)`, затем для алертов с результатом создаёт копию с клонированными аннотациями и `llm_*`; при `drop_noise` алерты `severity=noise` из уведомления убираются. `upstreamcore` получает два опциональных поля `Options` — без них поведение идентично upstream. `amp-upstream` строит AI-слой из `--amp.config.file`, только если `llm.enabled`.

## API Contracts

### Config `llm.notify_wait` / `llm.drop_noise`

- `llm.notify_wait` (duration, default `5s`, `>= 0`; `0` — не ждать) — бюджет ожидания классификации в notify-стадии `amp-upstream`.
- `llm.drop_noise` (bool, default `false`) — убирать из уведомлений алерты, классифицированные как `noise`.
- `cmd/server` ключи читает, но не использует (его ядро удаляется в срезе 5); в `config.yaml.example` помечено.

### Flag `amp-upstream --amp.config.file`

- Пусто (default) — AI-слоя нет. Путь — AMP-конфиг (`config.LoadConfig`), из него берётся только `llm.*`. Ошибка загрузки — ошибка старта.

### Notification annotations

- `llm_severity`, `llm_confidence` (`%.2f`), `llm_reasoning` (≤ 500 символов), `llm_recommendations` (до 3, через `"; "`) — то же соглашение, что `formatAlertmanager`. Доступны в шаблонах как `.Alerts[i].Annotations` и, если общие, `.CommonAnnotations`. Существующие одноимённые аннотации алерта перезаписываются только в копии уведомления.

### Go: `upstreamcore.Options`

- `NewAlertCallback func(mutes func(context.Context, model.LabelSet) bool) mem.AlertStoreCallback` — вызывается в `setup` с `silencer.Mutes`; результат композируется после silencer.
- `NotifyStage notify.Stage` — на каждом reload `rs[name] = notify.MultiStage{NotifyStage, rs[name]}`.

## Component Architecture

- `go-app/internal/upstreamai/classifier.go` — `Classifier`, `Config`, `New`, `Stop`, колбэк, воркеры, метрики.
- `go-app/internal/upstreamai/stage.go` — `NotifyStage(wait time.Duration, dropNoise bool) notify.Stage`, аннотации.
- `go-app/internal/upstreamai/convert.go` — `*alert.Alert` → `*core.Alert` (fingerprint `alertconv.Fingerprint`, совместимый с ключами кеша `cmd/server`).
- `go-app/internal/upstreamcore/core.go` — два поля `Options`, композитный колбэк.
- `go-app/internal/upstreamcore/reloader.go` — обёртка `RoutingStage`.
- `go-app/internal/upstreamcore/doc.go` — отличия от upstream.
- `go-app/internal/infrastructure/llm/{errors.go,client.go}` — `HTTPError.RetryAfter`, разбор заголовка, checker с `MaxDelay`, ожидание остатка.
- `go-app/internal/config/config.go` — `LLMConfig.NotifyWait`, `LLMConfig.DropNoise`, дефолты, валидация `notify_wait >= 0`.
- `go-app/cmd/amp-upstream/main.go` — флаг, сборка LLM-клиента, memory-кеша, `ClassificationService`, `Classifier`.
- `config.yaml.example`, `docs/UPSTREAM_UPGRADE.md`, `CHANGELOG.md` `[Unreleased]`.

## Security Design

- [x] Ownership validation — not applicable (нет пользовательских объектов).
- [x] Input validation — `notify_wait >= 0` при загрузке конфига; `Retry-After` — только неотрицательные секунды или HTTP-date, иначе игнор.
- [x] Sensitive data not logged — `api_key` не логируется; лог дропа шума — только `alertname`, fingerprint, confidence.
- [x] Rate limiting — очередь 1024, 4 воркера, CB и ретраи LLM-клиента, `Retry-After`.
- [x] Auth/RBAC — not applicable.

## Invariants

- [ ] 1. `PostStore`/`PostDelete`/`PostGC` не блокируются и не вызывают сеть.
- [ ] 2. Приём алерта никогда не зависит от состояния LLM.
- [ ] 3. Алерты в store не мутируются; `llm_*` — только в копиях.
- [ ] 4. Без `NewAlertCallback`/`NotifyStage` `upstreamcore` ведёт себя как сейчас; контрактные сьюты и гейт зелёные.
- [ ] 5. Notify-стадия не возвращает ошибку и не дропает алерты без `drop_noise`.
- [ ] 6. Колбэк silencer'а вызывается всегда и первым.

## Edge Cases

1. Resolved-алерт без записи → не классифицируется, стадия не ждёт.
2. Алерт заглушён silence → `skipped`, стадия не ждёт; после снятия silence повтор не делается до удаления записи (GC) — known gap, метрика `skipped_silenced`.
3. Очередь полна → запись не создаётся, следующий `Put` (resend Prometheus) повторит; один Warn на эпизод переполнения. Отправитель, который шлёт алерт один раз, классификацию не получит.
4. LLM недоступен → сервис отвечает fallback-классификацией по правилам без ошибки (`EnableFallback` по умолчанию): она идёт в `llm_*` (как на `cmd/server`), исход метрики — `fallback`; переклассификации после восстановления LLM нет (сервис кеширует fallback на 1h) — TECH-DEBT. Ошибка `ClassifyAlert` (только с выключенным fallback; в `amp-upstream` недостижимо) → запись удаляется, следующий `Put` повторит.
5. LLM висит дольше бюджета → уведомление без `llm_*`, метрика `timeout`. Поздняя классификация попадёт только в следующее *отправленное* уведомление группы: dedup upstream (хеш labels) не шлёт повтор ради одних аннотаций — до нового алерта в группе, resolve или `repeat_interval`.
6. `drop_noise` и вся группа — шум → `MultiStage` останавливается, уведомления нет. Дропаются только firing: resolved проходит всегда — его firing мог уйти до готовности классификации, и без resolve инцидент у получателя остался бы открытым (deep review F1).
7. `notify_wait: 0` → не ждём, аннотируем только готовые.
8. `Retry-After` > `MaxDelay` → без повтора; ≤ `MaxDelay` → повтор не раньше `Retry-After`; невалидный заголовок → обычный backoff.
9. Stop → воркеры останавливаются, in-flight LLM-запрос отменяется контекстом; ожидающие стадии выходят по дедлайну/контексту.

## Impact Analysis

- **Affected modules:** `upstreamcore`, новый `upstreamai`, `llm`, `config`, `cmd/amp-upstream`.
- **Breaking changes:** none. `cmd/server` (общий LLM-клиент): 429/503 с `Retry-After` больше `MaxDelay` (10s по умолчанию) не ретраятся — быстрее fallback; короткий `Retry-After` теперь выдерживается, и синхронный ingest `cmd/server` под 429 может стать длиннее, но не дольше `LLMTimeout` (как и раньше). Investigation (`investigate_with_tools.go`) не затронут: его checker без порога.
- **New dependencies:** none.
- **Risks:** сдвиг порта upstream — отмечается в `doc.go`, `scripts/upstream-bump.sh` диффит только upstream-файлы; ожидание до mute-стадий — ограничено бюджетом.

## Rollout / Rollback

- **Rollout:** `amp-upstream` не выкатывается (Helm — срез 5); включение — флаг.
- **Rollback:** убрать `--amp.config.file` или `llm.enabled: false`.
- **Feature flag:** `--amp.config.file` + `llm.enabled`; `llm.drop_noise`.

## Observability

- **Logs:** Info на старте AI-слоя (provider, model, notify_wait, drop_noise); Warn — один на эпизод переполнения очереди; Info на дроп шума (alertname, fingerprint, confidence); Warn на ошибку классификации.
- **Metrics:** `amp_upstream_ai_classifications_total{outcome="classified|fallback|error|skipped_silenced|dropped_queue_full"}` (`dropped_queue_full` — на попытку `Put`, не на алерт), `amp_upstream_ai_notify_waits_total{outcome="ready|timeout"}`, `amp_upstream_ai_noise_dropped_total`.
- **Alerts:** not applicable (срез 5/7 — правила для чарта).

## Deep Review

- **Mandatory triggers present:** 3+ signals (`C X R`).
- **Discretionary triggers present:** novel-pattern (первая точка расширения upstream-ядра).
- **Decision:** required.

## Open Questions

- [ ] Inhibition до классификации (стоимость LLM на подавленные алерты) — срез 2b/5.
- [ ] Повторная классификация после снятия silence — срез 2b (вместе с историей).
- [ ] Redis L2-кеш классификации в `amp-upstream` — с HA (срез 4).
