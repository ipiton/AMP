---
id: AMP-CACHE-NO-RECONNECT
slug: amp-cache-no-reconnect
stream: Cache
type: bug
status: active
created_at: 2026-10-22
updated_at: 2026-10-22
based_on:
  - requirements.md
  - research.md
  - Spec.md
---

# Implementation Plan: AMP не переподключается к кешу, недоступному на старте

**Based on:** requirements.md / research.md / Spec.md
**Date:** 2026-10-22

## Touched Files

- `go-app/internal/config/config.go` — `RedisConfig.StartupWait`, дефолт, валидация
- `go-app/internal/application/service_registry.go` — цикл ожидания в `initializeCache`, поля backoff
- `helm/amp/templates/deployment.yaml`, `helm/amp/templates/NOTES.txt` — env и подсказка
- `CHANGELOG.md`, `helm/amp/CHANGELOG.md`, `docs/CONFIGURATION_GUIDE.md`, `config.yaml.example`, `helm/amp/README.md`
- тесты (Phase 3): `go-app/internal/application/service_registry_cache_test.go`, `go-app/internal/config/*_test.go`, `helm/amp/tests/render-cache-startup-wait.sh`

## Phase 1: Код

- [x] **1.1** `RedisConfig.StartupWait` + `viper.SetDefault("redis.startup_wait", "60s")` + `Validate`: `< 0` → ошибка. <!-- verify: cd go-app && go build ./... && go vet ./internal/config/ -->
- [x] **1.2** `initializeCache`: окно (standard → `StartupWait`, иначе 0), цикл с backoff 1s→×2→5s, ctx-aware сон, логи по Spec; поля `cacheRetryInitialBackoff`/`cacheRetryMaxBackoff` с дефолтами в конструкторе. <!-- depends: 1.1 | verify: cd go-app && go vet ./internal/application/ && go test ./internal/application/ -run 'Cache|Cluster|Grouping' -count=1 -->

## Phase 2: Чарт и доки

- [x] **2.1** `deployment.yaml`: `REDIS_STARTUP_WAIT: "0s"` в ветке без кеша; NOTES на upgrade. <!-- verify: helm lint helm/amp && for t in helm/amp/tests/render-*.sh; do bash $t >/dev/null || echo FAIL $t; done -->
- [x] **2.2** Docs: CHANGELOG (Fixed), `helm/amp/CHANGELOG.md`, CONFIGURATION_GUIDE + `config.yaml.example` (ключ), README чарта § Cache. <!-- verify: grep -rn startup_wait CHANGELOG.md docs/CONFIGURATION_GUIDE.md config.yaml.example -->

**Phase verification:** `make -C go-app quality-gates-fast`, `git diff --check`.

## Phase 3: Тесты (write-tests, после verdict `pass`)

- [x] **3.1** `initializeCache`: miniredis стартует на зарезервированном адресе позже → `*RedisCache`, нет degraded reason; недоступный адрес + малое окно → `MemoryCache` + degraded; lite и `startup_wait: 0` → одна попытка (время < паузы); ctx отменён → быстрый fallback. <!-- verify: cd go-app && go test ./internal/application/ -run InitializeCache -count=1 -race -->
- [x] **3.2** Config: дефолт `60s`, env `REDIS_STARTUP_WAIT`, отрицательное отвергается. <!-- verify: cd go-app && go test ./internal/config/ -run StartupWait -count=1 -->
- [x] **3.3** `render-cache-startup-wait.sh`: env есть при `cache.enabled=false` и lite, нет на дефолтах standard. <!-- verify: bash helm/amp/tests/render-cache-startup-wait.sh -->

**Результат write-tests:** `service_registry_cache_test.go` — 5 тестов (поздний miniredis на зарезервированном адресе → `*RedisCache` без degraded; окно 300ms → fallback не раньше 250ms; lite и `0s` — одна попытка < 1s; доступный кеш — `attempts=1`; отменённый ctx → fallback < 2s), `-race -count=10` зелёные. `TestLoadConfig_RedisStartupWait` — дефолт `1m0s`, env `0s`, `-1s` отвергается. `render-cache-startup-wait.sh` — 5 проверок, на чарте `main` 2 FAIL (нет `REDIS_STARTUP_WAIT` в lite и при `cache.enabled=false`). Go-тесты на коде `main` не собираются (нет полей backoff), поэтому «красный на main» для них показан только сценарием: на `main` первая неудачная попытка сразу даёт fallback.

## Phase 4: Testing

- [ ] **4.1** Гейты WORKFLOW.md § Гейты AMP (затронутые пакеты, `quality-gates-fast`, `scripts/release-gate.sh`). <!-- verify: evidence/release-gate.txt -->
- [ ] **4.2** k3d: install `main` → upgrade веткой, рестартующий AMP и Valkey (или удаление пода Valkey + `rollout restart` AMP одновременно) → в логе AMP `Redis cache initialized` (с `attempts > 1`, если гонка случилась) без ручного рестарта. <!-- verify: evidence/k3d-acceptance.md -->

## Implementation Notes

- Окно проверяется перед паузой: повтор делается, только если до дедлайна остаётся больше текущей паузы. Сама попытка не ограничена дедлайном; dial идёт внутри PING с таймаутом 5s, поэтому фактическое ожидание может превысить `startup_wait` на одну попытку (≤ ~5s) — укладывается в `startupProbe`.
- Тесты `internal/application` на ветке и `main` — 19.5s / 20.6s: существующие тесты не стали ждать окно (их конфиги — lite или литералы с `StartupWait: 0`).
- Рендер чарта: дефолты standard — без изменений (кроме случайных паролей); `profile=lite` и `cache.enabled=false` — только `REDIS_STARTUP_WAIT: "0s"`.

## Definition of Done

- [ ] All steps are complete or explicitly marked blocked/skipped
- [ ] Success criteria from `requirements.md` are covered
- [ ] Contracts from `Spec.md` are implemented or deviations are recorded
- [ ] Deep review verdict is `pass`
- [ ] Tests for changed behavior are added or updated
- [ ] Phase checks pass
- [ ] Docs/planning are updated
