---
id: AMP-CACHE-NO-RECONNECT
slug: amp-cache-no-reconnect
stream: Cache
type: bug
status: draft
created_at: 2026-10-22
updated_at: 2026-10-22
based_on:
  - requirements.md
  - research.md
---

# Specification: AMP не переподключается к кешу, недоступному на старте

**Version:** 1.0
**Status:** Draft

## Summary

В standard-профиле `initializeCache` повторяет подключение к Redis/Valkey с backoff в пределах `redis.startup_wait` (дефолт 60s), прежде чем уйти на in-memory fallback. Гонка старта AMP и Valkey на `helm upgrade` перестаёт оставлять AMP без общего состояния; при выключенном кеше чарт ставит окно `0s`.

## Requirements Coverage

| Requirement / Criterion | Covered by |
|---|---|
| Ретрай в окне, успех → Redis без degraded | Target Design § цикл; тест `initializeCache` с поздним miniredis |
| После окна — прежний fallback, лог с попытками | Target Design § исход; тест с недоступным адресом |
| Lite и `startup_wait: 0` — одна попытка | Target Design § условия; тесты lite / 0 |
| Чарт: `REDIS_STARTUP_WAIT=0s` при выключенном кеше | Component Architecture § deployment.yaml; render-тест |
| Отрицательное значение отвергается | `Config.Validate`; тест |
| NOTES, docs, CHANGELOG | Component Architecture § docs |
| k3d: upgrade без ручного рестарта | testing, `evidence/k3d-acceptance.md` |

## Current State

- **Code:** `service_registry.go:1197-1218` — одна попытка `NewRedisCache`, ошибка → `MemoryCache` + `addDegradedReason("cache backend unavailable: …")`. `dialRedis` (`cache/redis.go:76`) — PING с таймаутом 5s поверх `context.Background()`.
- **Data:** не применимо.
- **Tests:** `cluster_status_test.go`, `service_registry_grouping_test.go` используют miniredis; тестов на `initializeCache` нет.
- **Docs:** `docs/CONFIGURATION_GUIDE.md` § redis, `config.yaml.example`, `helm/amp/README.md` (`cache.enabled`), `NOTES.txt:44-48`, `CHANGELOG.md` migration note `AMP-CACHE-NOAUTH`, ADR-017 «Следствие» (`DECISIONS.md:302`).

## Design Premises

| Premise | Confirmed by | Class | If wrong |
|---|---|---|---|
| HTTP-сервер (и пробы) поднимается только после `Initialize`, поэтому ожидание в `initializeCache` не валит liveness | `cmd/server/main.go:120` → `:190` | code-read | liveness убивает под во время ожидания |
| `startupProbe` чарта даёт ≥ 60s до проверки liveness | `deployment.yaml:314-323`: 10s + 30 × 10s | code-read | под рестартует в ожидании (тогда хоть и рестарт, но не хуже `main`) |
| Все потребители Redis берут клиент из `r.cache` после `initializeCache`; ни один не создаёт свой клиент раньше | grep `r.cache.(*infrastructurecache.RedisCache)` / `NewRedisCache` по `go-app` (`:782`, `:841`, `:905`, `:1879`, `:1981`, `service_registry_reload.go:226`; `NewRedisCache` в prod-коде вызывается только в `initializeCache`) | call-path-traced | часть подсистем осталась бы на старом решении |
| Разрыв старта AMP/Valkey на upgrade — секунды, а не минуты | k3d 2026-10-20: 3 s (`AMP-CACHE-NOAUTH/evidence/k3d-acceptance.md` § A2) | measured | окна 60s не хватит — fallback как сейчас (не хуже `main`) |
| При `cache.enabled=false` и в lite чарт передаёт `REDIS_ADDR=localhost:6379`, где никто не слушает | `deployment.yaml:235-236` | code-read | без `REDIS_STARTUP_WAIT=0s` такой старт ждал бы 60s |
| Пустая env-переменная не перекрывает дефолт viper (`AutomaticEnv` без `AllowEmptyEnv`) — поэтому выключатель через `REDIS_ADDR=""` не годится, нужен отдельный ключ | `config.go:671,838` | code-read | — (выбран ключ, не пустой адрес) |

## Target Design

1. `RedisConfig.StartupWait time.Duration` (`mapstructure:"startup_wait"`), дефолт viper `60s`, env `REDIS_STARTUP_WAIT`; `Config.Validate` отвергает `< 0`.
2. `initializeCache`: окно = `StartupWait`, если `Profile == standard`, иначе `0`. Цикл: попытка `NewRedisCache`; успех → `r.cache = redisCache`, лог `Redis cache initialized` с `attempts`; ошибка и до дедлайна остаётся больше, чем пауза → Warn `Redis cache not reachable yet, retrying` (`attempt`, `retry_in`, `addr`, `error`), сон с учётом `ctx.Done()`; пауза 1s → ×2 → потолок 5s.
3. Окно исчерпано (или `0`, или ctx отменён) → как сейчас: Warn `falling back to in-memory cache` (+ `attempts`, `waited`), degraded reason, `MemoryCache`.
4. Чарт: в ветке «кеш выключен / lite» (`deployment.yaml:234-236`) добавить `REDIS_STARTUP_WAIT: "0s"`. NOTES на upgrade: AMP ждёт кеш до 60s; подсказка `rollout restart` остаётся на случай, если Valkey не поднялся за окно.
5. Тестовая точка: пауза и потолок — поля реестра с дефолтами (`cacheRetryInitialBackoff`/`cacheRetryMaxBackoff`), тесты ставят миллисекунды; конфиг-ключ — только окно.

## API Contracts

Не применимо (HTTP API не меняется). Контракт конфигурации:

- `redis.startup_wait` (duration, дефолт `60s`, env `REDIS_STARTUP_WAIT`): сколько standard-профиль ждёт кеш на старте; `0` — одна попытка. Lite игнорирует. Hot reload: изменение → W601 restart required (как прочие `redis.*` в standard; поле участвует в `changedFields`).

## Data Model / Migrations

Не применимо.

## Component Architecture

- `go-app/internal/config/config.go` — поле, дефолт, валидация.
- `go-app/internal/application/service_registry.go` — цикл в `initializeCache`, два поля backoff в `ServiceRegistry`.
- `helm/amp/templates/deployment.yaml` — `REDIS_STARTUP_WAIT=0s` в ветке без кеша.
- `helm/amp/templates/NOTES.txt` — текст подсказки на upgrade.
- Docs: `CHANGELOG.md` (Fixed), `helm/amp/CHANGELOG.md` (Fixed), `docs/CONFIGURATION_GUIDE.md` (ключ), `config.yaml.example`, `helm/amp/README.md` § Cache (поведение на upgrade).

## Security Design

- [x] Ownership — не применимо.
- [x] Input validation — `startup_wait >= 0`.
- [x] Sensitive data not logged — логируются `addr` и ошибка go-redis (пароля в ней нет), как сейчас.
- [x] Rate limiting — backoff с потолком 5s, не долбим кеш.
- [x] Auth/RBAC — не применимо.

## Invariants

- [ ] Кеш доступен с первой попытки → поведение и логи как на `main` (плюс `attempts=1`), без задержки.
- [ ] Lite-профиль никогда не ждёт.
- [ ] После окна — тот же fallback и тот же текст degraded reason, что на `main`.
- [ ] Рендер чарта с дефолтами (standard + кеш включён) байт-в-байт как на `main`, кроме NOTES.

## Edge Cases

1. Неверный пароль (`WRONGPASS`) → ретраи до конца окна, затем fallback (ожидание 60s лишнее, но ошибка видна в каждом Warn; отличать auth-ошибки — не в этой задаче).
2. `ctx` отменён во время сна → немедленный fallback (процесс и так завершается).
3. `startup_wait` меньше первой паузы (например `500ms`) → одна попытка.
4. Каждая попытка длится до `dial_timeout` + 5s PING → фактическое ожидание может превысить окно на одну попытку (≤ ~10s).
5. Кеш поднялся, но упал до старта подсистем → как на `main`: их собственные проверки (grouping `StorageManager` failback, nflog degraded).

## Impact Analysis

- **Affected modules:** application (init), config, Helm-чарт.
- **Breaking changes:** нет. Standard без доступного кеша стартует до 60s дольше (документировано; `0s` возвращает прежнее).
- **New dependencies:** нет.
- **Risks:** docker/compose standard-установки без Redis получают 60s задержку старта → CHANGELOG и CONFIGURATION_GUIDE описывают `REDIS_STARTUP_WAIT=0s`.

## Rollout / Rollback

- **Rollout:** обычный upgrade; ничего делать не нужно.
- **Rollback:** откат версии; либо `redis.startup_wait: 0s` возвращает поведение `main` без отката.
- **Feature flag:** сам ключ `redis.startup_wait`.

## Observability

- **Logs:** Warn на каждую неудачную попытку (`attempt`, `retry_in`, `addr`, `error`); Info `Redis cache initialized` с `attempts`; fallback Warn с `attempts`, `waited`.
- **Metrics:** не добавляются — бэкенд виден по `status: degraded` и причине в `/-/ready`/`/-/healthy` отчёте.
- **Alerts:** не применимо.

## Deep Review

- **Mandatory triggers present:** 3 сигнала (`C X R`).
- **Discretionary triggers present:** none.
- **Decision:** required.

## Open Questions

- [ ] Дефолт окна 60s — компромисс между гонкой upgrade (секунды) и задержкой старта без кеша; проверяется на k3d (testing).
