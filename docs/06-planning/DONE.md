# DONE

Закрытые срезы и проверенные итоги **текущего месяца**. Прошлые месяцы — в `archive/DONE-YYYY-MM.md`. Проверка «закрыт ли slug» читает этот файл **и** все архивы:

```bash
grep -rn "SLUG" docs/06-planning/DONE.md docs/06-planning/archive/DONE-*.md
```

| Архив | Период |
|---|---|
| `archive/DONE-2026-09.md` | 2026-09 |
| `archive/DONE-2026-08.md` | 2026-08 |
| `archive/DONE-2026-05.md` | 2026-05 |
| `archive/DONE-2026-04.md` | 2026-04 |
| `archive/DONE-2026-03.md` | 2026-03 |
| `archive/DONE-2026-02.md` | 2026-02 |

## 2026-10

- 2026-10-07 — (TASK / Docs) **PLANNING-RESYNC** — planning и публичные доки приведены к фактическому состоянию кода по аудиту 2026-10-06 (`main` @ `fa682d4`, release-gate 11/11 PASS, read-only проверка на однонодовом k3s с kube-prometheus-stack).
  - **BACKLOG** § Production Readiness пересобран по P0/P1/P2, закрытые — в конец. Новые: `PROD-CONFIG-FILE-FALLBACK` (P0, подтверждён на бинаре: без файла конфига env игнорируется, `standard` выходит с кодом 1), `PROD-DEPS-OTEL-145` (P0, required `govulncheck` красный с 2026-10-05), `PROD-HARDCODED-FILTER` (P0), `HELM-SINGLE-NODE-DEFAULTS` (P1), `HELM-PROMRULE-LABELS`, `DEAD-VALUES-FILTERS` (P2). `PROD-GROUPING-DEFAULT` и `FU-TOPLEVEL-INHIBIT-RULES` подняты до P0. Статусы PHASE-5A, PHASE-6B (невлитая ветка), CONFIG-RELOADER-SIDECAR.
  - **NEXT**: порядок P0, решение владельца по v0.1.0 записано (пока не выпускаем); PHASE-6B влит в `main` отдельно (`feature/phase-6b-runbook-engine`); `CONFIG-RELOADER-SIDECAR` снят из Queue, добавлен `HELM-SINGLE-NODE-DEFAULTS`. **ROADMAP** сверен с кодом (был на 2026-04-16): Phase B и C1 закрыты, PHASE-4.5/4.6/5/6 — частично, добавлен PROD-READINESS.
  - **BUGS**: новый `HARDCODED-FILTER-DROPS-ALERTS`; `CONFIG-MISSING-FILE-DROPS-ENV` подтверждён (и для ошибок валидации); `HELM-README-DEFAULT-PROFILE-DRIFT` закрыт правкой README чарта. **TECH-DEBT** `HELM-CHART-GAPS`: уточнено, что `valkey.replicas: 0` обнуляет `amp-redis`.
  - **Публичные доки**: `README.md` (ресиверы из `alertmanager.yml` доставляют, убрано «только Secrets»; статус pilot-ready и четыре ловушки), `docs/ALERTMANAGER_COMPATIBILITY.md` (Known Gaps #13–#15, строка Inhibition 🟡, шаги миграции, убран дубль абзаца), `helm/amp/README.md` (дефолтный профиль `standard`, рабочий `values-small.yaml` — проверен `helm template` и запуском бинаря, порт Service 8080, kube-prometheus-stack), `docs/CONFIGURATION_GUIDE.md` (`inhibit_rules` под `inhibition:` в двух примерах), `docs/MIGRATION_QUICK_START.md` (установка через values-файл).
  - **Проверка:** docs-only, tier Lightweight; `git diff --check`; утверждения сверены с кодом, `helm template` и запуском бинаря; Go/Helm-гейты не запускались — код и чарт не тронуты. Ветка `docs/planning-resync`.
- 2026-10-06 — (TASK / Reliability, P0) **PROD-GRACEFUL-SHUTDOWN** — остановка AMP больше не обрывает in-flight запросы и teardown сервисов.
  - **Приложение** (`cmd/server/shutdown.go`, `runShutdown`): SIGTERM/SIGINT → `/-/ready` и `/readyz` 503 (`ServiceRegistry.BeginShutdown`, liveness не трогается) → `server.Shutdown` (drain) → `registry.Shutdown` (финальный snapshot lite, flush, хранилища) → `main` выходит только после этого, код 1 при ошибке. Раньше registry останавливался до HTTP, а `main` выходил, не дожидаясь. Подключён мёртвый ключ `server.graceful_shutdown_timeout`: один бюджет на всю цепочку, `0` → 30s (защита от `CONFIG-MISSING-FILE-DROPS-ENV`).
  - **Чарт**: preStop `sleep {{ preStopDelay }}` (ключ был мёртвым), новый `gracefulShutdown.timeoutSeconds` → env, `terminationGracePeriodSeconds` 30 → 40. Несогласованный бюджет — WARNING в `NOTES.txt` без `fail`: решение владельца, чтобы не вносить сигнал `C` и не поднимать тир до Full.
  - **Проверка:** tier Standard (`R X`), deep-review не требовался. Unit-тесты на порядок, бюджет, drain на реальном `http.Server`, readiness/liveness; мутация «старый порядок» роняет 3 теста. `render-graceful-shutdown.sh` 17/17. `release-gate.sh` PASS (11 шагов), `e2e-ha` ALL PASS, smoke lite с SIGTERM — exit 0, порядок в логе верный.
  - **Не проверено:** rolling update в живом k8s (preStop ∥ снятие endpoint'ов — `assumed`) → `K8S-LIVE-ROLLOUT-CHECK` в BACKLOG. Ожидание в самом `main()` unit-тестом не покрыто.
  - Документация: `docs/CONFIGURATION_GUIDE.md` § Graceful Shutdown, `helm/amp/README.md` § Graceful Shutdown, `CHANGELOG.md` (Fixed + migration notes), `helm/amp/CHANGELOG.md`.
  - Ветка `bugfix/prod-graceful-shutdown`, workspace `tasks/archive/PROD-GRACEFUL-SHUTDOWN/`. ~1d.
- 2026-10-05 — (TASK / Security, P0) **PROD-SECURITY-MD** — `SECURITY.md` описывает фактическое состояние вместо шаблона 2025-12.
  - **Канал:** GitHub Private Vulnerability Reporting (решение владельца), email не публикуется; `[INSERT SECURITY EMAIL]` убран. Ответ — best effort, подтверждение в течение 5 рабочих дней.
  - **Снято как ложное:** API key / JWT auth, серверный TLS, «configurable CORS» (код есть в `internal/application/middleware.go`, но `main` его не подключает), gosec, «security scans on every commit», версии `1.x`.
  - **Описано как есть:** basic auth (`--web.config.file`, ADR-011), TLS — на ingress/mesh, исходящий `tls_config` у receivers; guard Ingress, NetworkPolicy (ADR-015), namespaced RBAC (ADR-012), securityContext; CI с release-gate и `govulncheck`; отдельный список «Not provided» (CORS, rate limiting, audit log, SBOM/подпись). Чек-лист прод-деплоя переписан под настройки AMP. Раздел Security в `README.md` выровнен.
  - **Проверка:** docs-only, tier Lightweight; `git diff --check`, ссылки 11/11, утверждения сверены с кодом/чартом/CI; Go/Helm гейты не запускались — код и чарт не тронуты.
  - **Владелец:** PVR в репо выключен → `PRIVATE-VULN-REPORTING` в `NEXT.md` § Owner. «govulncheck — required» в `SECURITY.md` не заявлен, пока нет branch protection (`MAIN-BRANCH-PROTECTION`).
  - Ветка `docs/prod-security-md`, workspace `tasks/archive/PROD-SECURITY-MD/`. ~0.25d.
- 2026-10-01 — (TASK / Security, P0) **PROD-INGRESS-HARDENING** — чарт больше не публикует API через Ingress без auth, у pod'ов AMP появилась своя NetworkPolicy, ServiceMonitor заработал (ADR-015).
  - **Ingress guard** (`templates/ingress.yaml`): `ingress.enabled: true` рендерится только с `webConfig.existingSecret` или `ingress.externalAuth: true` (принимается только `true`/`"true"`). Привязан к Ingress, а не к прод-профилю: действует для любых values.
  - **NetworkPolicy AMP** (`templates/networkpolicy.yaml`, `networkPolicy.*`): ingress-only, порт `http`, правило на роль — `ingressController` (только при `ingress.enabled`) / `alertSenders` / `metricsScrapers` + сырой `extraIngress`; без источников — `fail`. Egress не ограничен. Включена в `values-production.yaml`, по умолчанию выключена. Метка `app.kubernetes.io/component: application` на pod template и metadata Service, селекторы не тронуты; `podLabels` не может её перекрыть. Попутно ожила политика redis (`valkey.networkPolicy`), которая ждала эту метку.
  - **ServiceMonitor**: раньше рендерился всегда и селектил метки, которых у Service не было. Теперь гейт `monitoring.prometheusEnabled` + `monitoring.serviceMonitor.enabled`, селектор только на Service AMP, `basicAuth` из Secret; с `webConfig` без кредов — `fail`.
  - **Breaking:** `values-production.yaml` не рендерится, пока не заданы auth, креды ServiceMonitor (с `webConfig`) и источник NetworkPolicy; первый upgrade — через `-f`, не `--reuse-values` (миграция в `CHANGELOG.md`). Шаблоны nil-safe к values старого релиза.
  - **Гейт:** шаг `helm-tests` в `scripts/release-gate.sh` (закрыт `HELM-RENDER-TEST-IN-GATE`), prod-рендер гейта — через `helm/amp/tests/values-production-placeholders.yaml`; `.helmignore` с `/tests/`.
  - **Проверка:** `deep-review` (mandatory, `S C R`) двумя независимыми агентами — 17 находок, 2 major (`--reuse-values` → nil pointer и пропавший ServiceMonitor; `podLabels` снимает метку), `fix_required`; после фиксов повторное независимое ревью подтвердило их и нашло R18 (неякорный `tests/` в `.helmignore` выкидывал hook `helm test`) — исправлено, verdict `pass` @ `093f8a7`. Новые `render-ingress-auth.sh` (16), `render-networkpolicy.sh` (29), `render-servicemonitor.sh` (21) assertions, существующие два адаптированы; мутации 15/15 пойманы. `scripts/release-gate.sh` 11/11 PASS (race 391s).
  - **Осознанные ограничения:** живого кластера не было (kind не установлен) — kubelet probes через политику (premise `assumed`), реальное отсечение и kill-switch не проверены вживую; в README — двусторонняя smoke-проверка для оператора. `service.type` NodePort/LoadBalancer guard'ом не покрыт; `externalAuth` + `ingressController` пускает любой Ingress того же контроллера (README рекомендует `webConfig`).
  - Документация: `helm/amp/README.md` § Network Exposure, `docs/CONFIGURATION_GUIDE.md` §4, `CHANGELOG.md` (Security + migration notes), `helm/amp/CHANGELOG.md`, ADR-015. Follow-ups: BUGS `SERVICE-METRICS-PORT-MISROUTED`, `MONITORING-CRD-DEFAULT`, `HELM-NAMESPACE-OVERRIDE-SPLIT`; BACKLOG `SERVICE-TYPE-EXPOSURE-GUARD`, дополнены `CONFIG-RELOADER-SIDECAR` (R12), `CONFIG-RELOADER-AUTH` (R13); TECH-DEBT `RELEASE-GATE-UNQUOTED-ARGS`, дополнен `HELM-CHART-GAPS` (R17).
  - Ветка `feature/prod-ingress-hardening`, workspace `tasks/archive/PROD-INGRESS-HARDENING/`. ~1d.
