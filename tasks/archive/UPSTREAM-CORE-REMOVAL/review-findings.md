# Deep Review Findings: UPSTREAM-CORE-REMOVAL

**Trigger classification:** mandatory (сигнал `S`: web auth и секреты; четыре сигнала риска `S`/`C`/`X`/`R`; pre-release review внешне видимого релиза)
**Reviewer perspective:** два независимых general-purpose агента, не писавших код: (A) Go — `cmd/amp-upstream`, `internal/config`, сборка и гейт; (B) Helm-чарт и миграционные заметки. Ни один не получал рассуждений автора, только Spec/requirements/tasks и дифф.
**Reviewed at:** 2026-11-08
**Reviewed tree:** feature/upstream-core-removal @ 4898a8e
**Verdict:** pass (see `review-verdict.json`), после фиксов fix-here ниже

Фиксы внесены поверх `4898a8e` одним коммитом `fix: address deep-review findings (UPSTREAM-CORE-REMOVAL)`. Проверка фиксов: `go build`/`go vet`/`golangci-lint`, `go test` затронутых пакетов, ручной запуск `amp-upstream` (F1, F2, F3), `helm lint` (дефолты и production), все 12 render-тестов, полный `scripts/release-gate.sh`.

## Ревьюер A — Go, сборка, гейт

### F1 — `server.web_config_file` и `SERVER_WEB_CONFIG_FILE` молча игнорировались, API оставался открытым
- **Severity:** major (S)
- **Location:** `go-app/cmd/amp-upstream/main.go`, `auth.go`, `go-app/internal/config/config.go`
- **Issue:** basic auth `amp-upstream` включался только флагом `--web.config.file`. Ключ AMP-конфига и env, которые документировались для старого ядра, не читались: оператор, перенёсший конфиг, получал открытый API без единого лога.
- **Recommendation:** читать ключ и env, порядок флаг → ключ → env.
- **Disposition:** fix-here — `ServerConfig.WebConfigFile`, `resolveWebConfigFile`; проверено запуском: ключ → 401, env → 401, `/-/healthy` 200.
- **Follow-up:** тесты проводки — write-tests (F6).

### F2 — Удалённые ключи AMP-конфига игнорировались без лога
- **Severity:** minor (C)
- **Location:** `go-app/internal/config/config.go`, `config.yaml.example`
- **Issue:** `route:`, `receivers:`, `grouping`, `redis`, `server.port` и т. п. молча не действовали.
- **Recommendation:** warn со списком непрочитанных ключей; обновить пример конфига.
- **Disposition:** fix-here для warn (`config.IgnoredKeys`, лог «AMP configuration keys ignored»; проверено: `["route","server.port"]`); `config.yaml.example` — defer-backlog (срез 7, миграционные доки).
- **Follow-up:** BACKLOG, срез 7.

### F3 — Бинари не из Docker отдавали пустые revision/branch/buildDate в `/api/v2/status`
- **Severity:** minor (R)
- **Location:** `go-app/Makefile`
- **Disposition:** fix-here — `-X` для `prometheus/common/version.*` в Makefile; проверено `versionInfo`.
- **Follow-up:** n/a

### F4 — Версия контракта: Dockerfile и `version.go` по-разному обрабатывали `replace`; karma-тест удалён без замены
- **Severity:** minor
- **Location:** `Dockerfile`, `go-app/Makefile`, `go-app/cmd/amp-upstream/version.go`
- **Disposition:** fix-here для сборки (`go list` учитывает `.Replace` в обоих местах); тест `setContractVersion` и karma-probe — write-tests.
- **Follow-up:** `tasks.md` § write-tests.

### F5 — Ручной `RACE_PKGS` пропускал конкурентные пакеты
- **Severity:** minor (X)
- **Location:** `scripts/release-gate.sh`
- **Disposition:** fix-here — добавлены `infrastructure/k8s`, `infrastructure/llm`, `core/resilience`, `core/services`, `webauth`, `cmd/amp-upstream`. Ручной список остаётся — UPSTREAMCORE-TEST-GROUPS-RACE обновить в finalize.
- **Follow-up:** TECH-DEBT UPSTREAMCORE-TEST-GROUPS-RACE.

### F6 — Нет тестов проводки auth и agent mode в `cmd/amp-upstream`
- **Severity:** minor
- **Location:** `go-app/cmd/amp-upstream/auth.go`, `agent.go`, `amp.go`
- **Disposition:** fix-here в фазе write-tests.
- **Follow-up:** `tasks.md` § write-tests.

### F7 — `go.mod` не tidy
- **Severity:** nit
- **Disposition:** fix-here — `go mod tidy`.

### F8 — `.dockerignore` и `.gitignore` указывали на старые имена бинаря
- **Severity:** nit
- **Disposition:** fix-here.

### F9 — Docker HEALTHCHECK не учитывает `--web.external-url` с путём
- **Severity:** nit
- **Location:** `Dockerfile`
- **Disposition:** fix-here — комментарий: HEALTHCHECK только для Docker, при пути в external URL переопределить.

### F10 — Записи BUGS.md / TECH-DEBT.md ссылаются на удалённый код
- **Severity:** minor (maintainability)
- **Location:** `docs/06-planning/BUGS.md`, `docs/06-planning/TECH-DEBT.md`
- **Issue:** часть дефектов переехала в живой код и не должна закрываться оптом: INVESTIGATION-MAX-RETRIES-ZERO (`cmd/amp-upstream/amp.go`), SERVER-HARDENING про `bcryptMu` (`internal/webauth/webauth.go`), CONFIG-ENV-KEYS-UNBOUND (пересчитать), LINT-ERRCHECK-CHANNEL-RECEIVE (сузить до пина линтера).
- **Disposition:** fix-here в finalize: переадресовать перенесённые, закрыть устаревшие (UI-ROUTE-PREFIX-LINKS, SQLITE-LITE-HARDENING, REDIS-FLUSH-ALL, UPSTREAM-TARGETS-DISCOVERY-DEPENDENCY и т. п.).
- **Follow-up:** finalize.

### F11 — Если `queue.Stop` не уложилась в таймаут, tools-DB закрывается под живыми воркерами
- **Severity:** nit
- **Location:** `go-app/cmd/amp-upstream/amp.go`
- **Disposition:** reject (reason: это только путь остановки процесса после истёкшего таймаута; запрос воркера к уже закрытому пулу tools-DB падает с ошибкой, investigation завершается неуспехом, и процесс выходит. Общего состояния, которое это портит, нет: tools-DB — чужая база, только чтение).

## Ревьюер B — Helm-чарт и миграция

### C1 — `helm upgrade --reuse-values` со старого чарта падал с nil pointer
- **Severity:** major
- **Location:** `helm/amp/templates/statefulset.yaml`, `amp-config.yaml`, `configmap.yaml`
- **Disposition:** fix-here — `templates/_validate.tpl` (`amp.validate`): без `alertmanager.*`/`alertmanager.persistence` рендер падает с подсказкой `--reset-then-reuse-values`; то же в CHANGELOG и README § Upgrading. Render-тест `old-values`, `old-persistence`.

### C2 — Удалённые top-level ключи молча игнорировались (маршруты из `configFile.content` терялись)
- **Severity:** major
- **Disposition:** fix-here — `amp.validate` падает на каждом удалённом ключе с названием замены. Render-тесты `old-configfile`, `old-upstreamcore`, `old-replicacount`, `old-cache`.

### C3 — POSTGRESQL_RESTORE_GUIDE не работал, и у чарта не было способа остановить AMP
- **Severity:** major
- **Disposition:** fix-here — `alertmanager.replicas: 0` рендерится (NOTES предупреждает), отрицательное значение падает; гайд переведён на `sts/<fullname>` и `--set alertmanager.replicas=0`, порт 9093, силенсы описаны как снапшоты на `data`, а не в PostgreSQL. Помечено, что сценарии гайда со StatefulSet end-to-end не перепрогонялись.
- **Follow-up:** перепрогон drill — BACKLOG (finalize).

### C4 — Секреты инструментов agent mode только через ConfigMap; обещанного env-пути не было
- **Severity:** major (S)
- **Disposition:** fix-here — `extraEnv` (EnvVar-список после env чарта), комментарий в `values.yaml` исправлен: пароли Prometheus/Loki tools читаются только из файла. Render-тест `extraenv`.

### C5 — Kubernetes-tool investigation без SA-токена, если discovery выключен
- **Severity:** minor
- **Disposition:** defer-tech-debt — дополнить HELM-AMP-SECCOMP.

### C6 — «`valkey.replicas: 0` removes it» фактически неверно
- **Severity:** minor
- **Disposition:** fix-here — NOTES, values, README, оба CHANGELOG: останавливает под, Services/Secret/ServiceMonitor/rules рендерятся. Гейт redis-шаблонов — defer-bug (HELM-CHART-GAPS).

### C7 — `webConfig.mountPath` не проверялся на пересечение с маунтами чарта
- **Severity:** minor
- **Disposition:** fix-here — fail при совпадении/вложенности с `/etc/amp/config` или `/data`. Render-тесты `webconfig-*`.

### C8 — В production есть Ingress, но не задан `alertmanager.externalURL`
- **Severity:** minor
- **Disposition:** fix-here — `values-production.yaml`: `externalURL: "https://amp.your-domain.com/"`.

### C9 — Пробелы render-тестов (webConfig, automount, VCT `data`, llm-ключи `amp.yaml`, NOTES N×LLM, DATABASE_PASSWORD в lite, reuse networkpolicy)
- **Severity:** minor
- **Disposition:** fix-here в фазе write-tests.

### C10 — Вакуумные утверждения в мигрированных тестах
- **Severity:** nit
- **Disposition:** fix-here в фазе write-tests.

### C11 — Устаревшие записи planning-доков после удаления шаблонов
- **Severity:** minor
- **Disposition:** fix-here в finalize: SERVICE-METRICS-PORT-MISROUTED закрыть, RELOADER-WATCH-DEPENDENT-FILES устарел, HELM-AMP-SECCOMP частично, HELM-LITE-RENDERS-POSTGRES, HELM-CHART-GAPS, HELM-NAMESPACE-OVERRIDE-SPLIT, CONFIG-ENV-KEYS-UNBOUND.

### C12 — Пробелы CHANGELOG об эффектах апгрейда
- **Severity:** minor
- **Disposition:** fix-here — CHANGELOG § Upgrading the release: `--reuse-values`, удалённые ключи, одновременная работа старого и нового ядра (возможны дубли), удаление lite PVC `<fullname>-pvc`, осиротевшие `data-<fullname>-upstream-<i>`, `replicas: 0`.

### C13 — Мелочи NOTES
- **Severity:** nit
- **Disposition:** fix-here — шаги без номеров, везде `$ns`, отдельное предупреждение про investigation × N. Бюджет соединений из `ampConfig.database` — defer-backlog.

### C14 — PDB: нет защиты от `minAvailable >= replicas`
- **Severity:** nit
- **Disposition:** defer-backlog.

## Anti-Pattern Check

- [x] Self-audit was not treated as a substitute for independent review.
