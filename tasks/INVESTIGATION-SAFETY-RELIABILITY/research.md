---
id: INVESTIGATION-SAFETY-RELIABILITY
slug: investigation-safety-reliability
stream: Investigation / LLM
type: bug
artifact: research-pack
status: complete
created_at: 2026-10-28
updated_at: 2026-10-28
---

# Research Pack - надёжность LLM-вызовов и очереди расследований

## Goal

Закрыть открытые вопросы `requirements.md`: хранилище дедупа, «не задано» vs `0` для `llm.max_retries`, TTL дедупа, потребители статуса `queued`.

## Sources

- `internal/core/resilience/resilience.go:53-85`, `pkg/retry/strategy.go:234-255`, `pkg/httperror/{error.go:178,445,classifiers.go:217}`, `internal/infrastructure/llm/{errors.go,client.go}`
- `internal/config/config.go:933`, `internal/application/service_registry_reload.go:91-101`, `service_registry.go:2209-2300`
- `internal/infrastructure/investigation/queue.go`, `internal/application/handlers/investigation.go:37-46`, `internal/infrastructure/cache/{interface.go,redis.go:129}`

## Findings

- `WithRetryFunc` — единственный потребитель `llm.classifyAlertWithRetry` (grep по модулю). Подмена `MaxRetries==0 → 3` нужна только ему; `retry.Do` сам подменяет `MaxAttempts==0 → 3`, но `WithRetryFunc` передаёт `MaxRetries+1`, поэтому `0` даёт одну попытку, если убрать подмену.
- «Не задано» уже решено на границе: `viper.SetDefault("llm.max_retries", 3)` и `llm.DefaultConfig().MaxRetries=3`; в `HTTPLLMClient` `0` приходит только из явного `max_retries: 0`. Форма конфига не меняется.
- `llm.IsRetryableError` → `LLMClassifier` → `httperror.IsRetryable` знает только `*HTTPAPIError` и сетевые ошибки; `*llm.HTTPError` для него — не ретраится. Тот же `IsRetryableError` использует `errors.go:81`.
- Ретраи расследований — свои, в `processJob` (`classifyError` по строке `HTTP 4xx/5xx`); формат `HTTP %d: …` нужно сохранить.
- `core.Cache` / `cache.Cache` не имеют `SetNX`. Паттерн AMP для межрепличной координации — `redisCache.ShareClient()` при `*RedisCache`, иначе поведение «на каждой реплике» (`initializeSilenceGCElection`). Кеш инициализируется до `initializeInvestigation`.
- Статус `queued` читает только `GET …/investigations/{fingerprint}` через `GetLatestByFingerprint`; пока job в канале, записи не будет → 404 (или прошлое расследование). UI/иные потребители статуса не найдены.
- `processJobWithAgent` при `err != nil` читает `agentRun` — вне скоупа (`AGENT-LOOP-UNKNOWN-KIND-NIL-RESULT`).

## Options

Generation: single pass (решения обратимы, без миграций; fan-out не нужен).

1. Дедуп в `Submit` через Redis — сетевой вызов в ingest-пути, противоречит цели. Отклонено.
2. Только in-memory — дешёво, но при общем Redis N реплик делают N расследований.
3. **Два уровня:** локальный in-memory claim в `Submit` (без IO, отсекает resend до очереди) + `SETNX` в воркере перед `Create`, если есть Redis; ошибка Redis → fail-open.

## Decision

- Вариант 3; TTL — константа 24h (ключ уже идентифицирует инцидент, TTL лишь ограничивает память/Redis); новый ключ конфига не вводим.
- Ретраи: `*llm.HTTPError` 429/5xx ретраится в `llm.IsRetryableError`; подмену `0 → 3` из `WithRetryFunc` убрать; тело ответа — `truncate` 512 байт в логе и `Message`.
- `queued`: `Create` в воркере; изменение видимости статуса — в CHANGELOG.

## Follow-ups

- [ ] `investigation.max_retries: 0` тоже превращается в 3 (`service_registry.go:2241`, `> 0`) — тот же класс, вне скоупа: запись в `BUGS.md` на `finalize`.
