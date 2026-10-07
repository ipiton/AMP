---
id: PROD-CONFIG-FILE-FALLBACK
slug: prod-config-file-fallback
stream: production-readiness
type: bug
priority: critical
status: active
created_at: 2026-10-07
updated_at: 2026-10-07
---

# Requirements: Отсутствие файла конфига не должно выбрасывать env

## Problem Framing

- **Symptom:** без файла конфига (`AMP_CONFIG_FILE` не задан ⇒ `./config.yaml`, в образе его нет) AMP пишет `WARN "Config file not found, using defaults"` и стартует на минимальном `Config{Server: {Port: 9093}}`: env и дефолты viper теряются. Дефолтный чарт (`configFile.enabled: false`) передаёт всю конфигурацию через env из ConfigMap `amp-config` ⇒ все её ключи мертвы, а в профиле `standard` процесс выходит с `database host is required` (exit 1) — crash loop. Подтверждено 2026-10-07 на бинаре из `main` @ `fa682d4`. Та же ветка глотает настоящие ошибки чтения/валидации: процесс с невалидным `config.yaml` стартует «на дефолтах».
- **Root Cause:** `config.LoadConfig` (`go-app/internal/config/config.go:659-664`) считает «нет файла» только `viper.ConfigFileNotFoundError`, но при `viper.SetConfigFile(path)` viper возвращает `os.PathError` (`fs.ErrNotExist`) — проверка никогда не срабатывает. `cmd/server/main.go:56-62` на любую ошибку подставляет минимальный конфиг вместо выхода.
- **Why Now:** P0 в `BACKLOG.md` § Production Readiness, первый по порядку. Без фикса дефолтный `helm install` нерабочий.
- **How We Measure:** бинарь без `config.yaml` с `PROFILE=standard DATABASE_HOST=…` (и прочим env из `amp-config`) применяет env; бинарь с невалидным `config.yaml` или нечитаемым путём выходит с ненулевым кодом и понятной ошибкой; тесты и render/smoke-сценарий с `configFile.enabled: false` зелёные.

## Risk Profile

- **Signals:** `C X R`
  - `C` — меняется контракт старта: ошибка чтения/валидации конфига теперь фатальна (раньше — фолбэк); env из ConfigMap начинает действовать.
  - `X` — `internal/config` + `cmd/server` + чарт/smoke (сценарий `configFile.enabled: false`, доки чарта).
  - `R` — поведение дефолтного деплоя меняется: ранее мёртвые ключи `amp-config` (`GROUPING_ENABLED`, `SERVER_EXTERNAL_URL`, `SERVER_GRACEFUL_SHUTDOWN_TIMEOUT`, …) начинают применяться.
- **Tier:** Full
- **Notes:** три сигнала ⇒ Full, `deep-review` обязателен. Смежно с `S`: сейчас ошибка валидации молча отбрасывает `server.auth`/web config (fail-open); фикс делает старт fail-closed — ревью должно это проверить.

## User Stories

1. Как оператор, ставящий чарт с дефолтами, я хочу, чтобы AMP читал конфигурацию из env `amp-config`, чтобы `helm install` без `configFile` работал в профиле `standard`.
2. Как оператор с ошибкой в `config.yaml`, я хочу, чтобы AMP отказался стартовать с понятной ошибкой, а не молча работал на дефолтах без моих маршрутов, auth и ресиверов.

## Success Criteria

- [x] Отсутствующий файл по дефолтному пути (`./config.yaml`, `AMP_CONFIG_FILE` не задан) — не ошибка: конфиг собирается из дефолтов viper + env; на старте — INFO с путём. Явный `AMP_CONFIG_FILE` без файла — exit 1 (fail-closed: почти всегда ошибка монтирования; решение — `Spec.md` § Target Design п. 2).
- [x] Любая другая ошибка `LoadConfig` (нет прав, битый YAML, unmarshal, `Validate`, inhibition) — выход с ненулевым кодом и сообщением; минимальный фолбэк `Config{Server: {Port: 9093}}` из `main.go` удалён.
- [x] Ключи, которые чарт передаёт через `amp-config`/env деплоймента, реально доходят до `Config` без файла (проверить, что viper `AutomaticEnv` + `Unmarshal` видит каждый ключ — в т.ч. без `SetDefault`); расхождения имён исправлены или задокументированы.
- [x] Unit-тест: «нет файла + env → env применён»; тест: «битый/невалидный файл → ошибка».
- [x] Тест в гейте: env-имена из шаблонов чарта (`configFile.enabled: false`, дефолтный путь) соответствуют ключам viper и доходят до `Config`. Живой старт дефолтного чарта — в `HELM-DEFAULTS-VALIDATE`: дефолтные values не проходят `Validate` (`research.md`).
- [x] Проверено, что тесты/e2e/smoke не полагаются на фолбэк; обходные пути, появившиеся из-за бага (`resolveWebConfigFile` env-чтение, `DefaultUnauthenticatedPaths` после фолбэка, `effectiveShutdownTimeout`), пересмотрены — оставить как страховку или упростить, решение записать.
- [x] `CHANGELOG.md` `[Unreleased]`: фикс + migration note (невалидный конфиг теперь фатален; env из ConfigMap начинает действовать). Баг `CONFIG-MISSING-FILE-DROPS-ENV` закрыт в `BUGS.md`.

## Non-Goals

- Не менять дефолт `configFile.enabled` в чарте и формат ConfigMap `amp-config`.
- Не чинить `PROD-GROUPING-DEFAULT`, `FU-TOPLEVEL-INHIBIT-RULES` и прочие P0 — даже если фикс сделает их заметнее.
- Не переписывать загрузку конфига (viper остаётся; глобальный `viper` — не трогать без необходимости).
- Не включать `configReloader` и не трогать `CONFIG-RELOADER-*`.

## Constraints

- **Scope:** `go-app/internal/config/config.go` (`LoadConfig`), `go-app/cmd/server/main.go` (обработка ошибки), тесты, render/smoke-сценарий, `helm/amp/README.md`/values-комментарии, planning.
- **Security:** fail-closed: ошибка конфига не должна приводить к старту без `server.auth`/web config. Секреты из env не логировать.
- **Compatibility:** breaking для тех, у кого невалидный `config.yaml` «работал» на фолбэке, — migration note. Дефолтный чарт начнёт применять ранее игнорировавшиеся значения из values — перечислить в CHANGELOG, проверить, что дефолтные values проходят `Validate`.

## Discovery Notes

- Similar tasks: `tasks/archive/PROD-AUTH/` (обход через `resolveWebConfigFile`), `tasks/archive/PROD-GRACEFUL-SHUTDOWN/` (`effectiveShutdownTimeout`); баг — `BUGS.md` § `CONFIG-MISSING-FILE-DROPS-ENV`.
- Relevant patterns: `LoadConfigFromEnv` (`config.go:771`) — env-only путь уже есть; `resolveRuntimeConfigPath` (`main.go:297`); чарт — `helm/amp/templates/configmap.yaml`, `deployment.yaml:162-166`, `configfile.yaml`; smoke/e2e монтируют свой `config.yaml` (`deploy/smoke/`, `deploy/e2e-ha/`).
- Open unknowns:
  - viper `AutomaticEnv` при `Unmarshal` видит только известные ключи (дефолт/файл) — какие ключи `amp-config` без `SetDefault` всё равно не дойдут; имена env (`APP_ENVIRONMENT`, `LLM_BASE_URL`, …) vs ключи структуры.
  - проходят ли дефолтные values чарта в `standard` `Validate` после фикса (DB host/creds из Secret, Redis, `SERVER_EXTERNAL_URL`).
  - есть ли тесты/хелперы, ожидающие старт при отсутствующем/битом конфиге.

## Research

- **Level:** 2 (`research.md`): поведение viper (env без дефолтов, `fs.ErrNotExist`), полная сверка env чарта с ключами `Config`, прогон дефолтного рендера чарта против `Validate`.
