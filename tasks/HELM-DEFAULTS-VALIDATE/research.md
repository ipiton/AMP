---
id: HELM-DEFAULTS-VALIDATE
slug: helm-defaults-validate
stream: Helm
type: bug
artifact: research-pack
status: final
created_at: 2026-10-14
updated_at: 2026-10-14
---

# Research Pack - дефолтные values чарта проходят валидацию AMP

Research level 3: сигналы `C S X R`, решение по безопасности. Варианты — параллельно, три линзы.

## 0) TL;DR

- Solving: дефолтный `helm install` — crash loop (нет ключа `llm-api-key` в Secret; `sslmode=disable` запрещён в production).
- Found: в дефолтном рендере ровно одна битая ссылка на Secret — `amp-secrets/llm-api-key`; SSL-проверка живёт только в `Validate()` (`config.go:1474`), hot-reload идёт через тот же `LoadConfig` → `Validate()`; Go уже по умолчанию `llm.enabled=false` (`config.go:913`).
- Chose: bool-флаг `database.allow_insecure_ssl_in_production` в Go (ослабляет только `ssl_mode=disable`), чарт рендерит env только во встроенной ветке PostgreSQL и только при `ssl != on`, ключа в values нет; `Warn` с `host` на старте. LLM: `enabled: false` в `values.yaml` и `values-production.yaml` + `fail` на рендере при `llm.enabled` без ключа.
- Out: внешняя БД с паролем из Secret — отдельный срез `HELM-EXTERNAL-DATABASE` (решение владельца 2026-10-14).
- Risks: флаг глобальный — AMP не отличает встроенную БД от внешней, гарантию «только встроенная» даёт чарт; смена дефолта `llm.enabled` молча выключит LLM тем, кто задавал только `apiKey`.
- Next step: -> spec

## 1) Questions

1. Имя и форма флага, как он попадает в viper (AutomaticEnv видит только известные ключи).
2. Где проверка и где предупреждение; влияет ли hot-reload.
3. Как чарт гарантирует, что флаг не уйдёт на внешнюю БД.
4. LLM: только сменить дефолт или ещё guard; не ломает ли это ESO и release-gate.
5. Внешняя БД с паролем из Secret — в задаче или отдельно.

## 2) Findings

- **Codebase:**
  - `DatabaseConfig` (`go-app/internal/config/config.go:329-344`), viper `AutomaticEnv` + replacer `.`→`_` (`:666`, `:833`); новый ключ нужен в `setDefaults` (`:891` рядом с `ssl_mode`), иначе env не дойдёт (`:972-976`).
  - Проверка `production && ssl_mode == "disable"` — `config.go:1474`, внутри `validateDatabaseCredentials`, вызывается из `Validate()` только для профиля `standard` (`:1141-1158`). Пароль/длина (`:1458-1471`) — отдельно; дефолтный `postgresql.password` (19 символов) их проходит.
  - Hot-reload: `ReloadCoordinator.loadAndParse` → `LoadConfig` → `Validate()` (`reload_coordinator.go:365`); `update_validator.go:259` `ssl_mode` не проверяет. Флаг ведёт себя одинаково на старте и при reload.
  - Предупреждения на старте — `cmd/server/main.go` (`logWebAuthState`, `logger.Warn` на `:238`) — образец тона.
  - Чарт: env БД — `helm/amp/templates/deployment.yaml:94-113`, только при `postgresql.enabled && profile == standard`; `DATABASE_SSL_MODE` = `require` при `postgresql.config.ssl == "on"`, иначе `disable` (`values.yaml:411` `ssl: "off"`, TLS у встроенного PG нет).
  - Рендер дефолтных values (2026-10-14, `helm template amp helm/amp`): все `secretKeyRef` сходятся с ключами Secret, кроме `amp-secrets/llm-api-key` (Deployment). `secret.yaml:44-46` кладёт ключ только при непустом `llm.apiKey`.
  - `values-production.yaml`: `environment: production`, встроенный PG, `llm.enabled: true`, глобальный `externalSecrets` не включён ⇒ тот же битый `secretKeyRef`. release-gate рендерит его с `tests/values-production-placeholders.yaml` (`scripts/release-gate.sh:170-197`), ключа LLM там нет.
  - ESO-путь `externalsecret.yaml:38-44` кладёт `llm-api-key` при `llm.enabled`; условие верхнего уровня — `.Values.externalSecrets.enabled`.
  - Прецедент fail на рендере: `required` для `cache.auth.password` (`secret.yaml:39`).
- **Constraints:** релиза `v0.1.0` не было — контракт values мягкий, но `CHANGELOG [Unreleased]` ведётся с migration notes. `/api/v2/status` — Alertmanager-совместимый контракт, расширять его не стоит.
- **Попутно (вне задачи):** `secret.yaml` рендерит `<fullname>-secrets` безусловно, а `externalsecret.yaml` при `externalSecrets.enabled` целится в тот же Secret с `creationPolicy: Owner` (`externalsecret.yaml:17-19`) — вероятный конфликт Helm и ESO за один объект. Не воспроизводилось; записано в `BUGS.md` как `ESO-SECRET-OWNERSHIP-CONFLICT`.
- **Unknowns:** нет блокирующих.

## 3) Options

`Generation:` parallel (minimal-diff, reversibility, operational)

3/3 линзы сошлись на bool-флаге в Go, env в чарте без ключа в values, `Warn` с `host`. Расхождения: guard для LLM, метрика, пункт 3.

### Option A - узкий bool-флаг, env только во встроенной ветке

- Go: `AllowInsecureSSLInProduction bool` `mapstructure:"allow_insecure_ssl_in_production"`, env `DATABASE_ALLOW_INSECURE_SSL_IN_PRODUCTION`, `SetDefault(false)`; в `config.go:1474` условие `&& !flag`. Флаг снимает только запрет `disable`, остальные проверки без изменений.
- `main.go`: `Warn` «database connection is UNENCRYPTED in production» с `database.host` — по хосту видно, если флаг попал на внешнюю БД.
- Чарт: в ветке `deployment.yaml:94-113` после `DATABASE_SSL_MODE` — env флага под `if ne (default "off" .Values.postgresql.config.ssl) "on"`. В `values.yaml` ключа нет.
- **Pros:** минимальный публичный контракт (один ключ AMP); при будущем TLS у встроенного PG чарт просто перестанет ставить флаг; апгрейд в любом порядке образ/чарт без регрессий (старый бинарь игнорирует env и падает как сейчас).
- **Cons:** гарантия «только встроенная» — на чарте; через `configFile.content`/ручной env флаг можно выставить для внешней БД.
- **Cost/Risk:** low

### Option B - флаг, привязанный к хосту

- `database.insecure_ssl_allowed_host: "<fullname>-postgresql"`; `disable` разрешён, только если `Database.Host` совпадает.
- **Pros:** при смене хоста на внешний AMP снова строгий.
- **Cons:** строковый контракт сложнее объяснять и тестировать; обходится вписыванием обоих значений; почти ничего не добавляет к `Warn` с `host` (reversibility, minimal-diff — против).
- **Cost/Risk:** medium

### Option C - метрика `amp_config_database_tls_disabled` (дополнение к A)

- Gauge 0/1, чтобы ловить флаг по парку правилом Prometheus (operational).
- **Cons:** новый публичный контракт метрики ради одного сценария; лог уже даёт сигнал. Не берём сейчас — кандидат в BACKLOG, если появится парк установок.
- **Cost/Risk:** low, но лишнее для P0.

### LLM: варианты guard

- Только дефолт `false` (minimal-diff): `llm.enabled: true` без ключа по-прежнему даёт `CreateContainerConfigError`.
- `fail` на рендере при `llm.enabled && !llm.apiKey && !externalSecrets.enabled` (operational, прецедент `cache.auth.password`): ошибка на `helm install`, а не невнятный статус pod'а. ESO-установки не ломаются.
- `optional: true` у `secretKeyRef` — прячет ошибку до runtime; отвергнут.
- Предупреждение в `NOTES.txt` о выключенном LLM при заданном ключе (reversibility) — не берём: `NOTES.txt` при `helm upgrade` никто не читает, migration note в CHANGELOG надёжнее.

### Самое сильное возражение (reversibility)

Флаг называется opt-in, но включает его чарт: каждая дефолтная установка в `production` работает с БД без шифрования, а `Warn` становится постоянным шумом. Настоящее несоответствие — `environment: production` при встроенном PG без TLS. Принято осознанно: это решение владельца 2026-10-14 (вариант `environment: development` рассматривался и отклонён). Смягчение — трафик остаётся внутри кластера (Service встроенного PG); TLS для встроенного PG — отдельная задача, после которой чарт перестанет ставить флаг.

## 4) Decision

- **Chosen:** Option A + LLM: `enabled: false` в `values.yaml` и `values-production.yaml` + `fail` на рендере при включённом LLM без ключа и без ESO. Пункт 3 — отдельный срез `HELM-EXTERNAL-DATABASE` (BACKLOG P1).
- **Why:**
  - закрывает оба источника crash loop минимальным диффом; без флага валидатор строг как раньше (критерий requirements);
  - публичный контракт — один ключ AMP, values не растут; откат — убрать env из шаблона;
  - `Warn` с хостом делает обход «только встроенная» наблюдаемым без хрупкой эвристики в Go;
  - `values-production.yaml` с `llm.enabled: true` без ключа после guard перестал бы проходить release-gate — дефолт `false` и там согласован с решением владельца.

## 5) Spec Inputs

- API/contracts: новый ключ конфига `database.allow_insecure_ssl_in_production` (bool, default `false`) / env `DATABASE_ALLOW_INSECURE_SSL_IN_PRODUCTION`; дефолт `llm.enabled` в чарте `true` → `false` (оба values-файла); новый `fail` на рендере. `config.yaml.example` — упомянуть ключ.
- Data model/migrations: not applicable.
- Rollout/rollback: CHANGELOG `[Unreleased]` — fix crash loop + migration note «кто полагался на дефолт `llm.enabled: true`, включите явно»; флаг работает только с образом, где он есть (старый образ падает как раньше). Откат — revert коммита.
- Observability: `Warn` на старте при `production && ssl_mode=disable && flag`, с `host`; без метрики.
- Security/ownership/RBAC: флаг не ослабляет проверки пароля; env рендерится только во встроенной ветке и только при `ssl != on`; render-тест «при `postgresql.enabled=false` флага нет». Go-тест «production + disable без флага → ошибка».

## 6) References

- Files: `go-app/internal/config/config.go`, `go-app/internal/config/reload_coordinator.go`, `go-app/cmd/server/main.go`, `helm/amp/templates/{deployment,secret,externalsecret}.yaml`, `helm/amp/values.yaml`, `helm/amp/values-production.yaml`, `helm/amp/tests/`, `scripts/release-gate.sh`
- Docs: `tasks/archive/PROD-CONFIG-FILE-FALLBACK/evidence/binary-check.md`, `docs/06-planning/BUGS.md` `HELM-DEFAULTS-FAIL-VALIDATION`
