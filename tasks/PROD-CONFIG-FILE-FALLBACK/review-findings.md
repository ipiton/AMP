# Deep Review Findings: отсутствие файла конфига не должно выбрасывать env

**Trigger classification:** mandatory (3 signals: `C X R`)
**Reviewer perspective:** два независимых агента (general-purpose), свежий контекст, без доступа к выводам друг друга: (A) security / fail-open / correctness / edge cases; (B) design premises / contract / docs / maintainability. Свод и диспозиции — сессия-автор; ключевые утверждения F1 и F4 перепроверены grep'ом.
**Reviewed at:** 2026-10-07
**Reviewed tree:** `bugfix/prod-config-file-fallback` @ `43b0024` (diff `e757730..43b0024`)
**Verdict:** fix_required (see `review-verdict.json`)

## Findings

### F1 — Доки рекомендуют TLS для встроенного Postgres, которого чарт не поддерживает
- **Severity:** major
- **Location:** `helm/amp/README.md:15`, `CHANGELOG.md` (breaking note 4), `docs/ALERTMANAGER_COMPATIBILITY.md` п. 15
- **Issue:** совет `postgresql.config.ssl: "on"` с сертификатами невыполним:
  - в `templates/postgresql-*.yaml` нет ни томов, ни ключей под сертификат (`grep -i "ssl_cert|ssl_key|server.crt|tls"` пуст);
  - Postgres с `ssl = on` без `server.crt` не стартует;
  - при этом `deployment.yaml:104-105` переключит AMP на `sslmode=require`.
- **Recommendation:** оставить только внешний PostgreSQL с TLS; прямо сказать, что встроенный Postgres TLS не поддерживает и что со встроенным `standard` в `production` не стартует до `HELM-DEFAULTS-VALIDATE`.
- **Disposition:** fix-here
- **Follow-up:** n/a

### F2 — Устаревшие утверждения «без файла env игнорируется» вне диффа
- **Severity:** major
- **Location:** `README.md:17`, `docs/MIGRATION_QUICK_START.md:24`, `helm/amp/values.yaml:256-258`, `deploy/smoke/docker-compose.yml:41-43`, `go-app/cmd/server/main.go:205-208` (комментарий к `webConfigFileEnv`), `go-app/cmd/server/shutdown.go:11-15`, `go-app/cmd/server/shutdown_test.go:139`
- **Issue:** после фикса эти тексты описывают несуществующее поведение. `MIGRATION_QUICK_START.md` — основной путь установки.
- **Recommendation:** переписать. `DECISIONS.md:154` — исторический, не трогать. `shutdown_test.go` — тестовый файл, правится в `write-tests`.
- **Disposition:** fix-here
- **Follow-up:** n/a

### F3 — С дефолтами чарта до проверки SSL дело не доходит
- **Severity:** minor
- **Location:** `helm/amp/README.md:15`, `CHANGELOG.md` breaking note 4, `docs/ALERTMANAGER_COMPATIBILITY.md` п. 15
- **Issue:** при `llm.enabled: true` и пустом `apiKey` в Secret нет `llm-api-key`, а `secretKeyRef` не optional ⇒ `CreateContainerConfigError`. Доки называют первой ошибкой проверку SSL.
- **Recommendation:** описать обе причины по порядку: сначала нет LLM-ключа, потом проверка SSL.
- **Disposition:** fix-here
- **Follow-up:** чинит `HELM-DEFAULTS-VALIDATE` (заводится в `finalize`).

### F4 — Service-link env Kubernetes теперь доходят до конфига на дефолтном чарте
- **Severity:** major
- **Location:** `helm/amp/templates/deployment.yaml` (pod spec), `go-app/internal/config/config.go` (`AutomaticEnv`)
- **Issue:** kubelet инжектирует `<SVC>_PORT=tcp://ip:port` для каждого Service в namespace. Совпадения с ключами AMP ломают unmarshal: `METRICS_PORT` (Service `metrics`; чарт его не задаёт), `DATABASE_PORT` (lite или внешний DB). Probe: `METRICS_PORT=tcp://10.0.0.5:9090` → `'metrics.port' cannot parse value as 'int'`. После фикса это exit 1, crash loop. До фикса дефолтный чарт env игнорировал, `configFile.enabled` был подвержен и раньше. `enableServiceLinks` в чарте нет (`grep` пуст).
- **Recommendation:** `enableServiceLinks: false` в pod spec AMP. AMP service-link переменные не читает, адреса задаются явно. Render-проверка.
- **Disposition:** fix-here
- **Follow-up:** n/a

### F5 — Устаревший глобальный viper: повторный `LoadConfig` на исчезнувшем файле
- **Severity:** minor
- **Location:** `go-app/internal/config/config.go:661-666`, `:719` (`loadRouteConfig`), `go-app/internal/config/reload_coordinator.go:359-365`
- **Issue:** при отсутствующем файле `ReadInConfig` не трогает `v.config`, а фикс трактует это как успех. Повторный `LoadConfig` после исчезновения файла вернёт старые значения и `Routing == nil` без ошибки (probe: `port=12345 routing_nil=true err=nil`). Старт не затронут. Reload сначала делает `os.ReadFile`, так что остаётся окно TOCTOU (check-then-use) между `ReadFile` и `LoadConfig`; смена ConfigMap — атомарный rename, на практике окно недостижимо.
- **Recommendation:** зафиксировать инвариант комментарием в `LoadConfig`. Настоящий фикс — `viper.New()` на каждый вызов, или reload передаёт уже прочитанные байты. Это существующий долг глобального viper.
- **Disposition:** defer-tech-debt (комментарий-инвариант — fix-here)
- **Follow-up:** `TECH-DEBT.md` → `CONFIG-GLOBAL-VIPER-STATE` (в `finalize`)

### F6 — Сообщения валидации печатают секреты из URL
- **Severity:** minor
- **Location:** `go-app/cmd/server/main.go` (ERROR `failed to load configuration`), валидаторы E114/E116 в `internal/config`, `config.go` (проверка `external_url`)
- **Issue:** ошибки валидации эхом печатают значение: Slack webhook URL (это credential), userinfo в `external_url`. Проверено probe'ом. Не регрессия: раньше то же уходило в WARN. Но теперь — ERROR на каждом рестарте crash loop. Ошибки YAML и mapstructure значений не печатают.
- **Recommendation:** редактировать userinfo и path URL в сообщениях валидаторов.
- **Disposition:** defer-tech-debt
- **Follow-up:** `TECH-DEBT.md` → `CONFIG-VALIDATION-ERROR-REDACTION` (в `finalize`)

### F7 — Мёртвый фолбэк порта 9093 в `main.go`
- **Severity:** nit
- **Location:** `go-app/cmd/server/main.go:146-149`
- **Issue:** `if port == 0 { port = 9093 }` недостижим: `Validate` отвергает `port <= 0` (`config.go:1061`), а любая ошибка загрузки теперь означает exit. Ветка хранит устаревший дефолт 9093.
- **Recommendation:** удалить.
- **Disposition:** fix-here
- **Follow-up:** n/a

### F8 — Reload читает `AMP_CONFIG_FILE` без `TrimSpace`
- **Severity:** nit
- **Location:** `go-app/internal/application/service_registry.go:327-330`, `:2642-2645`
- **Issue:** `main` делает `TrimSpace`, reload — нет. Значение из одних пробелов или с пробелом в начале разрешается по-разному. Существовало до задачи.
- **Recommendation:** передавать в registry путь, уже разрешённый в `main`.
- **Disposition:** defer-tech-debt
- **Follow-up:** `TECH-DEBT.md` → `CONFIG-PATH-RESOLUTION-DUP` (в `finalize`)

### F9 — `checkConfigPath` и проверяет, и логирует
- **Severity:** nit
- **Location:** `go-app/cmd/server/main.go` (`checkConfigPath`)
- **Issue:** можно было бы записать inline в `main`.
- **Disposition:** reject (reason: отдельная функция нужна, чтобы протестировать её без `os.Exit` (Spec § Component Architecture). Размер — 12 строк.)
- **Follow-up:** n/a

### F10 — `internal/config/example.go` вызывает `LoadConfig`, но сам никем не вызывается
- **Severity:** nit
- **Location:** `go-app/internal/config/example.go:10,140`
- **Issue:** мёртвый код. Вызовов в рантайме нет; в Spec P2 он не упомянут.
- **Disposition:** reject (reason: вне задачи, поведения не несёт. Spec P2 дополнен упоминанием.)
- **Follow-up:** n/a

## Premises (reviewer B)

P1, P3–P8 — подтверждены, класс заслужен. P2 верна для рантайм-вызовов, но список неполон: пропущен мёртвый `example.go`. Поиск охватил весь репозиторий: `cmd/config-reloader` `LoadConfig` не вызывает. P7 закрывает вопрос из implementation notes: `unauthenticated_paths: null`, пустое значение, `auth: {}`, пустой файл дают дефолты; `[]` — явный opt-out оператора. nil не получается ни в одном случае.

## Anti-Pattern Check

- [x] Self-audit was not treated as a substitute for independent review.
