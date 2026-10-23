---
id: PUBLISH-QUICK-FIX-BUNDLE
slug: publish-quick-fix-bundle
stream: Publishing
type: bug
artifact: research-pack
status: complete
created_at: 2026-10-23
updated_at: 2026-10-23
---

# Research Pack - быстрые исправления publishing (бандл)

Уровень 2 (light): шесть известных дефектов с готовым Fix в `BUGS.md`; открыт только выбор хранилища для Rootly и форма half-open.

## 0) TL;DR

- Solving: resolve в PD/Rootly без локального состояния, изоляция состояния по таргету, half-open breaker, graceful stop очереди, секреты в логах.
- Found: PD уже шлёт `dedup_key = fingerprint` на trigger — кеш на resolve лишний. Rootly нужен внешний ID инцидента; `r.cache` (Redis с in-memory fallback) инициализируется раньше publishing и доступен фабрике.
- Chose: PD — детерминированный ключ; Rootly — ID в `r.cache` с TTL 7d, продлеваемым на каждом firing; breaker — одна проба с ограничением по времени; очередь — RW-мьютекс вокруг отправки/закрытия; общий маскер URL в `internal/core`.
- Risks: промах Rootly на resolve теперь ошибка ⇒ считается breaker'ом; инциденты, открытые до апгрейда, resolve не получат.
- Next step: -> spec

## 1) Questions

1. Как закрыть инцидент Rootly без in-memory состояния?
2. Как сделать half-open с одной пробой, не получив вечно занятый слот, если проба не отчиталась?
3. Как остановить очередь без паники от отправки в закрытый канал и без потери заданий?
4. Где жить общему маскеру URL, чтобы его видели `infrastructure/publishing` и `business/publishing`?

## 2) Findings

- **PD (`pagerduty_publisher_enhanced.go`):** trigger шлёт `DedupKey: alert.Fingerprint` (`:93`), кеш хранит `resp.DedupKey` — для Events API v2 это тот же ключ, что передан. Resolve с `DedupKey: alert.Fingerprint` эквивалентен upstream (`notify/pagerduty`: ключ — хеш group key, детерминированный). `EventKeyCache` используется только PD-паблишером и фабрикой (`grep` по репозиторию).
- **Rootly (`rootly_publisher_enhanced.go`, `rootly_client.go`):** клиент умеет только create/update/resolve по ID; поиска нет. ID создаётся Rootly — детерминированного внешнего ключа нет.
- **Хранилище:** `ServiceRegistry.initializeCache` (`service_registry.go:512`, этап infrastructure) ставит `r.cache` (Redis или `MemoryCache`), `initializePublishing` идёт позже (`:2430`). `cache.Cache.Get` на промах — `cache.ErrNotFound` (`cache/interface.go:108`). Пакет `cache` не импортирует `publishing` — цикла нет.
- **Кеши корреляции:** `slackCache`/`rootlyCache` общие на фабрику, ключ — fingerprint (`slack_publisher_enhanced.go:56,131`, `rootly_publisher_enhanced.go:71,117`). У `Publish` есть `target` — ключ `target.Name|fingerprint` доступен без смены интерфейсов.
- **SMTP (`publisher.go:855`):** ключ `host:port`, а `SMTPDialer` запекает весь `SMTPConfig` (`email_models.go:7`: Host, Port, Username, Password, RequireTLS, From).
- **Breaker (`circuit_breaker.go`):** единственный потребитель — `PublishingQueue.processJob` (`queue.go:1001-1157`). Путь abandoned-job с причиной ≠ `Unconfirmed` не вызывает ни `RecordSuccess`, ни `RecordFailure` (`queue.go:1090`) — слот пробы, освобождаемый только записью результата, там бы завис.
- **Очередь (`queue.go`):** `Stop` закрывает три канала без защиты; `submitJob` шлёт в канал в `select` без блокировки — гонка check-then-send закрывается `sync.RWMutex` (send под `RLock`, close под `Lock`), send не блокирует (`default`), значит `Stop` не ждёт submit'ов. Воркер выходит на `nil` из high (`:817`), бросая medium/low. `processJob` без `recover`.
- **Секреты:** `maskURL` (`webhook_auth.go:153`) не маскирует URL короче 30 символов и не разбирает URL; `slack_client.go` уже маскирует свой URL через `maskedError`, Telegram — `maskBotToken`. Не маскированы: `HTTPPublisher.publish/publishBatch` (`*url.Error` и тело ответа), `createEnhancedWebhookPublisher` (INFO с URL), `discovery_impl.go:479` (DEBUG с URL). `io.ReadAll` без лимита — в `publisher.go`, `slack_client.go`, `telegram_client.go`, `pagerduty_client.go`, `rootly_client.go`, `webhook_client.go`, `llm/client.go`, `llm/investigate_with_tools.go`.

## 3) Options

`Generation:` single-pass

### Rootly: Option A - поиск инцидента через Rootly API

- **Pros:** нет своего состояния.
- **Cons:** у клиента нет list/search; фильтры Rootly API по меткам не проверены (нет аккаунта для проверки); title не уникален; лишний запрос на каждый resolve.
- **Cost/Risk:** high

### Rootly: Option B - ID в `r.cache` (Redis), TTL продлевается на firing

- **Pros:** общий для реплик и переживает рестарт AMP; переиспользует существующий кеш; интерфейс `IncidentIDCache` сохраняется.
- **Cons:** без Redis (`MemoryCache`) — только в пределах процесса, как сейчас; инцидент, не обновлявшийся дольше TTL, не закроется.
- **Cost/Risk:** low

### Rootly: Option C - только ошибка при промахе

- **Pros:** минимальный дифф.
- **Cons:** дефект (resolve теряется после рестарта) остаётся, просто становится видимым.
- **Cost/Risk:** low, но не решает задачу

### Breaker: одна проба, освобождаемая по результату или по истечении `Timeout`

Ограничение по времени закрывает путь abandoned-job, где результат не пишется, без изменения `processJob`.

## 4) Decision

- **Chosen:** Rootly — B + C (промах на resolve ⇒ `ErrIncidentNotTracked`); PD — детерминированный ключ, `EventKeyCache` удалить; Slack/Rootly — ключ `target.Name|fingerprint`; SMTP — ключ из всех полей конфига, пароль — `sha256`; breaker — `probeInFlight` + `probeStartedAt`; очередь — `RWMutex` + `stopOnce` + дренаж всех каналов + `recover`; маскер — `core.RedactURL`.
- **Why:**
  - Rootly B — единственный вариант, который закрывает рестарт и HA без непроверенного внешнего API; TTL 7d с продлением на каждый firing покрывает любой `repeat_interval` < 7d.
  - PD — upstream-семантика, удаляет состояние вместо того, чтобы его чинить.
  - Breaker с тайм-аутом пробы не требует, чтобы каждый путь `processJob` отчитывался.
  - `internal/core` уже импортируют оба пакета publishing.

## 5) Spec Inputs

- API/contracts: нет. Внутренняя сигнатура `NewEnhancedPagerDutyPublisher` теряет параметр кеша.
- Data model/migrations: новый ключ в кеше `publishing:rootly:incident:<target>|<fingerprint>` (строка, TTL 7d). Миграции нет.
- Rollout/rollback: обычный rollout. Откат возвращает in-memory кеш; ключи в Redis истекают сами.
- Observability: `ErrIncidentNotTracked` — через существующий `RecordAPIError(rootly, "incidents", ...)`; ошибки хранилища — WARN.
- Security/ownership/RBAC: маскировка URL, лимит тела, нет пароля в ключе кеша и в ошибке.

## 6) References

- Files: `go-app/internal/infrastructure/publishing/{publisher.go,queue.go,circuit_breaker.go,pagerduty_publisher_enhanced.go,rootly_publisher_enhanced.go,slack_publisher_enhanced.go,webhook_auth.go}`, `go-app/internal/application/publishing_runtime.go`, `go-app/internal/infrastructure/cache/interface.go`
- Docs: `docs/06-planning/CODE-REVIEW-2026-10-21.md` C-2, H-4, M-7, M-9, M-10, M-11, L-2, L-19
