---
id: AMP-CACHE-NO-RECONNECT
slug: amp-cache-no-reconnect
stream: Cache
type: bug
artifact: research-pack
status: complete
created_at: 2026-10-22
updated_at: 2026-10-22
---

# Research Pack - AMP-CACHE-NO-RECONNECT

## Goal

Выбрать способ, которым AMP перестанет навсегда оставаться на in-memory кеше, если Valkey поднялся позже AMP. Уровень 2: несколько вариантов + инфраструктура (Helm).

## Sources

- `go-app/internal/application/service_registry.go:1197-1218` (`initializeCache`), `:782`, `:841`, `:905`, `:1879`, `:1981` — потребители `r.cache.(*RedisCache)` при инициализации.
- `go-app/internal/application/service_registry_reload.go:224` — `RedisReloadable` получает nil при fallback.
- `go-app/internal/infrastructure/cache/redis.go:59-100` — `NewRedisCache` → `dialRedis`, PING 5s, ctx = `Background`.
- `go-app/cmd/server/main.go:120,190` — `Initialize` до `ListenAndServe`.
- `helm/amp/templates/deployment.yaml:202-237` (env кеша), `:314-323` (`startupProbe` 10s + 30×10s), `NOTES.txt:44-48`.
- `tasks/archive/AMP-CACHE-NOAUTH/evidence/k3d-acceptance.md` § A2.1 — под AMP старше пода Valkey на ~6 s, `i/o timeout` (single-node k3d).

## Findings

- Тип `r.cache` на старте определяет 7 подсистем; каждая берёт `ShareClient()` один раз. Поздний переход на Redis = перестройка grouping/nflog/silence-sync/leader election/heartbeat — это отдельная архитектурная работа.
- HTTP-сервер стартует после `Initialize`; ожидание внутри `initializeCache` покрыто `startupProbe` (до ~310 s), liveness до конца startup не проверяется.
- При `cache.enabled=false` и в lite чарт ставит `REDIS_ADDR=localhost:6379`; `README.md` чарта обещает «AMP on in-memory state». Цикл ожидания без выключателя удлинил бы такой старт на всё окно.
- Postgres на старте не ретраится: ошибка подключения → `os.Exit(1)` → рестарт контейнера.

## Options

| Вариант | Плюсы | Минусы |
|---|---|---|
| A. Ретрай на старте в пределах окна, затем прежний fallback | маленький дифф в одном месте; покрывает гонку upgrade; поведение после окна не меняется | Valkey, недоступный дольше окна, всё ещё оставляет AMP на in-memory |
| B. Падать на старте, если кеш недоступен (как Postgres) | никогда не застревает на in-memory | Valkey лежит → AMP не принимает алерты вовсе; ломает «кеш опционален» (TN-202) |
| C. Runtime-переход in-memory → Redis | полное решение | перестройка 7 подсистем, гонки при переключении состояния; >2d |
| D. Readiness = false, пока кеш недоступен | трафик не идёт на деградированный под | без C никогда не восстановится; при одной реплике — нет приёма алертов |

## Decision

- Вариант **A** с выключателем: `redis.startup_wait` (дефолт `60s`, `0` = одна попытка), только standard-профиль; чарт ставит `0s`, когда кеш выключен. Backoff 1s → ×2 → потолок 5s, ctx-aware.
- C — follow-up в TECH-DEBT при закрытии (если гонка проявится дольше окна).

## Follow-ups

- [ ] TECH-DEBT: runtime failforward с in-memory на Redis для всех потребителей кеша (вариант C).
