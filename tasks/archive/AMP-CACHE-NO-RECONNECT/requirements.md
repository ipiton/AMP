---
id: AMP-CACHE-NO-RECONNECT
slug: amp-cache-no-reconnect
stream: Cache
type: bug
priority: medium
status: done
created_at: 2026-10-22
updated_at: 2026-10-22
---

# Requirements: AMP не переподключается к кешу, недоступному на старте

## Problem Framing

- **Symptom:** после `helm upgrade`, который рестартует и AMP, и Valkey, AMP может стартовать раньше кеша: в логе `Redis cache unavailable, falling back to in-memory cache`, под Ready, но таймеры групп, nflog, дедуп и silence-синк живут в памяти до следующего рестарта. Воспроизведено на k3d 2026-10-20 (`tasks/archive/AMP-CACHE-NOAUTH/evidence/k3d-acceptance.md` § A2.1: под AMP стартовал на ~6 s раньше пода Valkey, ошибка `dial tcp …: i/o timeout`).
- **Root Cause:** `initializeCache` (`go-app/internal/application/service_registry.go:1197`) делает одну попытку `NewRedisCache` (PING ≤ 5 s) и при ошибке навсегда ставит `MemoryCache`. От типа `r.cache` в момент старта зависит вся проводка: Redis group/timer storage, nflog, silence event bus, leader election, cluster heartbeat, `RedisReloadable` — переключить их на Redis позже нельзя без перестройки графа.
- **Why Now:** после `AMP-CACHE-NOAUTH` AMP на дефолтах чарта реально ходит в Valkey, поэтому гонка бьёт по каждому upgrade, рестартующему оба пода; сейчас это лечится только ручным `rollout restart` (подсказка в NOTES).
- **How We Measure:** Go-тест: Redis поднимается через N секунд после старта `initializeCache` → `r.cache` — `*RedisCache`, без degraded reason; на `main` тот же сценарий даёт `MemoryCache`. k3d: upgrade, рестартующий AMP и Valkey, — в логе AMP `Redis cache initialized` без ручного рестарта.

## Risk Profile

- **Signals:** `C X R`
  - `C` — новый ключ конфигурации приложения `redis.startup_wait` (env `REDIS_STARTUP_WAIT`), аддитивно.
  - `X` — Go-приложение и Helm-чарт (env для `cache.enabled=false`, NOTES).
  - `R` — меняется поведение старта в standard-профиле.
- **Tier:** Full (три сигнала)
- **Notes:** `S`/`M` нет: данные и креды не трогаются, хранилища не мигрируют.

## User Stories

1. Как оператор AMP на Helm, я хочу, чтобы после `helm upgrade` AMP сам дожидался своего Valkey и работал с общим состоянием, чтобы не делать `rollout restart` руками и не терять дедуп уведомлений.
2. Как оператор, который осознанно выключил кеш (`cache.enabled=false`) или работает в lite-профиле, я хочу, чтобы старт AMP не замедлился.

## Success Criteria

- [x] Standard-профиль: если кеш недоступен на старте, AMP повторяет подключение с backoff в пределах `redis.startup_wait` (дефолт 60s) и при успехе работает на Redis как при обычном старте (`Redis cache initialized`, без degraded reason).
- [x] По истечении окна поведение как сейчас: in-memory fallback + degraded reason; в логе видно число попыток и время ожидания.
- [x] Lite-профиль и `redis.startup_wait: 0` — одна попытка, как сейчас (старт не замедляется).
- [x] Чарт: при `cache.enabled=false` (и в lite) AMP получает `REDIS_STARTUP_WAIT=0s`; рендер с дефолтами не меняется, кроме этого env там, где кеш выключен.
- [x] Отрицательное `redis.startup_wait` отвергается при загрузке конфига.
- [x] NOTES на upgrade и docs отражают новое поведение; CHANGELOG `[Unreleased]` Fixed.
- [x] k3d: upgrade, рестартующий AMP и Valkey, — AMP на Valkey без ручного рестарта (evidence).

## Non-Goals

- Переключение с in-memory на Redis во время работы процесса (перестройка grouping/nflog/silence-sync/leader election) — после окна старта fallback остаётся до рестарта, как сейчас.
- Readiness, зависящая от кеша; падение процесса при недоступном кеше.
- Новая метрика бэкенда кеша: текущий бэкенд виден по логу и по `status: degraded` + причине в health-отчёте.
- `REDIS-FLUSH-ALL` (`FlushAll` → `FlushDB`), кроме прокидывания ctx в подключение, если понадобится.

## Constraints

- **Scope:** `initializeCache`, `RedisConfig` + дефолт/валидация, чарт (env + NOTES), docs.
- **Security:** не применимо; пароль кеша не логируется (логируется только `addr`).
- **Compatibility:** новый ключ аддитивный; для standard-профиля без доступного кеша старт удлиняется до окна (по умолчанию 60s) — вписывается в `startupProbe` чарта (10s + 30 × 10s). Изменение `redis.startup_wait` при hot reload — W601 restart required, как прочие поля `redis.*` в standard.

## Discovery Notes

- Similar tasks: `tasks/archive/AMP-CACHE-NOAUTH/` (A2 — воспроизведение), `tasks/archive/HELM-SINGLE-NODE-DEFAULTS/review-findings.md` R7.
- Relevant patterns: backoff-цикл resubscribe silence event bus (`service_registry.go:988-1010`, `silenceSubscribeRetryBackoff`); `grouping.StorageManager` — runtime failback/failforward только для group storage.
- Open unknowns: нет (см. `research.md`).
