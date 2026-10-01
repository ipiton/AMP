---
id: PROD-INGRESS-HARDENING
slug: prod-ingress-hardening
stream: Security
type: feature
status: active
created_at: 2026-10-01
updated_at: 2026-10-01
based_on:
  - requirements.md
  - research.md
  - Spec.md
---

# Implementation Plan: Закрыть внешний и внутрикластерный доступ к AMP в прод-профиле

**Based on:** requirements.md / research.md / Spec.md v1.1
**Date:** 2026-10-01

Обозначения в verify:
- `PW` = `--set postgresql.password=x --set cache.auth.password=x` (обязательные пароли prod-values);
- `PROD` = `-f helm/amp/values-production.yaml $PW`;
- `SRC` = `--set-json 'networkPolicy.alertSenders=[{"namespaceSelector":{"matchLabels":{"kubernetes.io/metadata.name":"monitoring"}}}]'`.

## Touched Files

- `helm/amp/templates/ingress.yaml` — guard D1.
- `helm/amp/templates/networkpolicy.yaml` — новый: политика AMP и guard пустых источников.
- `helm/amp/templates/deployment.yaml` — метка `app.kubernetes.io/component: application` в pod template.
- `helm/amp/templates/service.yaml` — та же метка в `metadata.labels`.
- `helm/amp/templates/servicemonitor.yaml` — D7: gate, селектор, метки, basicAuth, guard.
- `helm/amp/values.yaml` — `ingress.externalAuth`, секция `networkPolicy`, `monitoring.serviceMonitor.*`.
- `helm/amp/values-production.yaml` — `networkPolicy.enabled: true` + примеры, комментарий у `ingress:`.
- `scripts/release-gate.sh` — placeholder'ы для prod-рендера, шаг `helm-tests`.
- `helm/amp/tests/render-ingress-auth.sh`, `render-networkpolicy.sh`, `render-servicemonitor.sh` — новые (phase 5).
- `helm/amp/README.md`, `docs/CONFIGURATION_GUIDE.md`, `CHANGELOG.md`, `helm/amp/CHANGELOG.md`, `docs/06-planning/DECISIONS.md` — docs, ADR-015.

## Phase 1: Чарт

> **Wave 1** — независимые шаги

- [x] **1.1** `values.yaml`: добавить `ingress.externalAuth: false` и комментарий: что значит `true`, чарт этого не проверяет, guard D1. <!-- verify: helm template amp helm/amp >/dev/null && grep -n "externalAuth" helm/amp/values.yaml -->
- [x] **1.2** `values.yaml`: секция `networkPolicy` (`enabled: false`, `ingressController`, `alertSenders`, `metricsScrapers`, `extraIngress` — пустые списки). В комментариях: роли, пример с `kubernetes.io/metadata.name`, `ipBlock` для LB/NodePort, fail-open на CNI без поддержки, kill-switch, отсутствие egress. <!-- verify: helm template amp helm/amp | grep -c "kind: NetworkPolicy" → 0 -->
- [x] **1.3** `deployment.yaml`: `app.kubernetes.io/component: application` в `spec.template.metadata.labels` (после `selectorLabels`, до `podLabels`); `spec.selector` не трогать. <!-- verify: helm template amp helm/amp -s templates/deployment.yaml | grep -n -B2 -A4 "component: application" — метка только в template, в selector.matchLabels её нет -->

- [x] **1.3a** `service.yaml`: `app.kubernetes.io/component: application` в `metadata.labels`; `spec.selector` не трогать. <!-- verify: helm template amp helm/amp -s templates/service.yaml — метка в metadata, selector = только name+instance -->
- [x] **1.3b** `values.yaml`: `monitoring.serviceMonitor.{enabled: true, interval: 30s, scrapeTimeout: 10s, labels: {}, basicAuth.{secretName: "", usernameKey: username, passwordKey: password}}` с комментариями (пароль в открытом виде, не bcrypt; Secret в namespace релиза; `labels` для `serviceMonitorSelector`). <!-- verify: helm template amp helm/amp >/dev/null -->

> **Wave 2** — зависит от Wave 1

- [x] **1.4** `ingress.yaml`: guard D1 — `fail` с текстом из Spec §API Contracts, если `ingress.enabled && !webConfig.existingSecret && !ingress.externalAuth`. <!-- depends: 1.1 | verify: helm template amp helm/amp --set ingress.enabled=true → fail с "ingress.externalAuth"; с --set webConfig.existingSecret=s → kind: Ingress; с --set ingress.externalAuth=true → kind: Ingress -->
- [x] **1.5** `templates/networkpolicy.yaml`:
  - рендер при `networkPolicy.enabled`;
  - `podSelector` = selectorLabels + `component: application`;
  - `policyTypes: [Ingress]`;
  - по правилу на каждую непустую роль (`ingressController` — только при `ingress.enabled`), порт `{protocol: TCP, port: http}`;
  - `extraIngress` — как есть;
  - `fail` при отсутствии действующих источников.
  <!-- depends: 1.2, 1.3 | verify: helm template amp helm/amp --set networkPolicy.enabled=true → fail "no sources"; + $SRC → NetworkPolicy, port: http, без Egress; kubeconform/`helm lint` чисто -->
- [x] **1.5a** `servicemonitor.yaml` по D7:
  - gate `monitoring.prometheusEnabled && monitoring.serviceMonitor.enabled`;
  - `namespace` как у Service, `amp.labels` + `serviceMonitor.labels` (старые `app`/`release` убрать);
  - селектор selectorLabels + `component: application`, `namespaceSelector.matchNames`;
  - endpoint `port: http`, `/metrics`, interval/timeout из values, `basicAuth` при `secretName`;
  - `fail` при `webConfig.existingSecret` без `basicAuth.secretName`.
  <!-- depends: 1.3a, 1.3b | verify: helm template default → 1 app ServiceMonitor с component в селекторе; --set monitoring.prometheusEnabled=false → 0; --set webConfig.existingSecret=s → fail "no credentials"; + --set monitoring.serviceMonitor.basicAuth.secretName=m → basicAuth.username.name=m -->
- [x] **1.6** `values-production.yaml`:
  - `networkPolicy.enabled: true`, списки пустые, закомментированные примеры для ingress-nginx и monitoring;
  - комментарий у `ingress:` про требование webConfig/externalAuth;
  - комментарий у `monitoring:` про `serviceMonitor.basicAuth` и `networkPolicy.metricsScrapers`.
  <!-- depends: 1.4, 1.5, 1.5a | verify: helm template amp helm/amp $PROD → fail (auth); + --set webConfig.existingSecret=s → fail (sources или ServiceMonitor); + $SRC + --set monitoring.serviceMonitor.basicAuth.secretName=m → рендер с Ingress, NetworkPolicy, ServiceMonitor с basicAuth -->

**Phase verification:**
- `helm template` для default/dev/lite рендерится, NetworkPolicy и Ingress там нет;
- `diff` рендера default до/после — метка component (pod template, Service metadata) и переписанный ServiceMonitor, больше ничего;
- `helm lint helm/amp $PROD --set webConfig.existingSecret=s $SRC`.

## Phase 2: Гейт

- [x] **2.1** `release-gate.sh`: в `step_helm_values` (prod) и `step_helm_rbac` (production) добавить `--set webConfig.existingSecret=release-gate-placeholder`, `--set monitoring.serviceMonitor.basicAuth.secretName=release-gate-placeholder` и источник-placeholder (`--set-json` или временный values-файл). Комментарий — почему placeholder, а не ослабление guard. <!-- depends: 1.6 | verify: scripts/release-gate.sh — шаги helm-production и helm-rbac PASS -->
- [x] **2.2** `release-gate.sh`: шаг `helm-tests` — запуск всех `helm/amp/tests/*.sh`; падение любого — FAIL шага с именем скрипта. <!-- verify: шаг PASS на текущих двух тестах; временно сломанный тест (exit 1) → FAIL шага -->

**Phase verification:** `scripts/release-gate.sh` все шаги PASS.

## Phase 3: Документация и ADR

- [x] **3.1** `DECISIONS.md` ADR-015: guard на Ingress вместо «прод-профиля», смысл `externalAuth`, ingress-only без egress, селектор Service не меняем, fail на пустых источниках, ServiceMonitor с обязательным basicAuth при webConfig. <!-- verify: grep -n "ADR-015" docs/06-planning/DECISIONS.md -->
- [x] **3.2** `helm/amp/README.md` раздел «Network exposure»:
  - guard и два выхода;
  - примеры аннотаций nginx `auth-url`/`auth-secret` для `externalAuth`;
  - NetworkPolicy: роли, примеры, `ipBlock`;
  - smoke-проверка «чужой pod → timeout»;
  - обнаружение потерь (`prometheus_notifications_errors_total`/`_dropped_total`);
  - kill-switch;
  - probes на нестандартном CNI;
  - ServiceMonitor: `labels` для `serviceMonitorSelector`, Secret для basicAuth (`kubectl create secret generic … --from-literal=username=… --from-literal=password=…`), связка с `metricsScrapers`.
  <!-- verify: ручная вычитка; команды из README прогнаны через helm template -->
- [x] **3.3** `docs/CONFIGURATION_GUIDE.md`: §4 — ссылка на README; у `helm install -f values-production.yaml` (`:707`) — что задать до установки. <!-- verify: grep -n "externalAuth\|networkPolicy" docs/CONFIGURATION_GUIDE.md -->
- [x] **3.4** `CHANGELOG.md` `[Unreleased]`: `### Security` и `### Breaking changes / migration notes` (шаги апгрейда для values-production); `helm/amp/CHANGELOG.md`. <!-- verify: grep -n "PROD-INGRESS-HARDENING" CHANGELOG.md helm/amp/CHANGELOG.md -->

**Phase verification:** `git diff --check`; ссылки и ключи в доках совпадают с `values.yaml`.

## Phase 4: Deep review (обязателен)

- [x] **4.1** `/deep-review` по диффу phases 1–3 → `review-findings.md`, `review-verdict.json`. <!-- depends: 1.*, 2.*, 3.* | verify: jq -r .gate tasks/PROD-INGRESS-HARDENING/review-verdict.json → pass -->

## Phase 5: Тесты (после `gate: pass`)

- [x] **5.0** Адаптировать существующие `render-config-reloader.sh` (`:115-117`) и `render-image-tag.sh` (`:68-70`): prod-рендер + `-f values-production-placeholders.yaml`. Сейчас оба падают на guard'ах этой задачи (gate 2026-10-01: шаг `helm-tests` FAIL, «networkPolicy.enabled=true with no sources»). Отложено из `implement`: тестовые файлы правятся только после verdict. <!-- depends: 4.1 | verify: bash helm/amp/tests/render-config-reloader.sh && bash helm/amp/tests/render-image-tag.sh -->
- [x] **5.1** `helm/amp/tests/render-ingress-auth.sh` — кейсы из Spec §Test Plan + `--set-string ingress.externalAuth=false|no` → fail, строка `"true"` → рендер (R3). <!-- depends: 4.1 | verify: bash helm/amp/tests/render-ingress-auth.sh → rc=0 -->
- [x] **5.2** `helm/amp/tests/render-networkpolicy.sh` — кейсы из Spec §Test Plan, включая совпадение `from` политики redis с метками pod'а и неизменный `spec.selector`; `podLabels` с `app.kubernetes.io/component` → fail (R2); values без секции `networkPolicy` (как `--reuse-values` со старого релиза) → рендер без политики (R1). <!-- depends: 4.1 | verify: bash helm/amp/tests/render-networkpolicy.sh → rc=0 -->
- [x] **5.2a** `helm/amp/tests/render-servicemonitor.sh` — кейсы из Spec §Test Plan, включая «селектор совпадает только с Service AMP»; values без `monitoring.serviceMonitor` → ServiceMonitor с дефолтами 30s/10s, а с `webConfig` — fail (R1). <!-- depends: 4.1 | verify: bash helm/amp/tests/render-servicemonitor.sh → rc=0 -->
- [x] **5.2b** В одном из render-тестов: дефолтный рендер содержит `templates/tests/postgresql-test-connection.yaml` (R18). <!-- depends: 4.1 | verify: тест падает при `tests/` в `.helmignore` -->
- [x] **5.3** Мутации: убрать guard D1 / метку / guard пустых источников / добавить Egress / убрать guard basicAuth / убрать component из селектора ServiceMonitor — каждая ловится тестом; откатить. <!-- depends: 5.1, 5.2 | verify: записать результат в testing-лог -->

## Phase 6: Testing и finalize

- [x] **6.1** `scripts/release-gate.sh` целиком (шаг `helm-tests` подхватывает новые тесты); `git diff --check`; Go-код не менялся — `quality-gates-fast` для контроля. <!-- verify: все шаги PASS -->
- [x] **6.2** Опционально: kind + Calico smoke (разрешённый отправитель, чужой pod, probes, kill-switch). Если дорого — осознанное ограничение в итоге. <!-- verify: лог в evidence/ или запись «не выполнялось» -->
- [x] **6.3** `/finalize`:
  - BUGS `SERVICE-METRICS-PORT-MISROUTED`, `MONITORING-CRD-DEFAULT` — заведены в fix-раунде review (R10); `HELM-NAMESPACE-OVERRIDE-SPLIT` (R16);
  - BACKLOG `SERVICE-TYPE-EXPOSURE-GUARD` (R4); дописать `CONFIG-RELOADER-SIDECAR` (R12), `CONFIG-RELOADER-AUTH` (R13); TECH-DEBT R14, R17 (`HELM-CHART-GAPS`);
  - TECH-DEBT: sticky-аннотации, хардкод `name: monitoring`;
  - BACKLOG: `PROD-INGRESS-HARDENING`, `HELM-RENDER-TEST-IN-GATE` → закрыты;
  - DONE: ротация `DONE.md` → `archive/DONE-2026-09.md` (первая задача октября).
  <!-- verify: /qa-check -->

## Deviations from Spec

- **D6, placeholder'ы гейта:** вместо `--set`/`--set-json` используется файл `helm/amp/tests/values-production-placeholders.yaml`. Гейт раскрывает строку аргументов без кавычек, а `[0]`/JSON в ней подвержены glob-раскрытию. Файл же переиспользуют render-тесты. Пароли по-прежнему через `--set`, как было.
- **Порядок ошибок prod-рендера:** helm останавливается на первом `fail`, поэтому `values-production.yaml` сообщает о недостающих настройках по одной. README перечисляет все три сразу.
- **`amp-valkey` NetworkPolicy** рендерится сабчартом valkey и в дефолтах, и в проде; была и до задачи. Не затронута.

## Implement log (2026-10-01)

- `scripts/release-gate.sh` целиком: build, lint, test, futureparity, race (296s, зелёный), helm-deps, helm-dev, helm-production, helm-rbac, amtool-compat — PASS. `helm-tests` — FAIL, ожидаемо: существующие тесты рендерят prod без новых настроек (шаг 5.0). Тем самым проверен и шаг 2.2: падение называет скрипт.
- Ручной прогон guard'ов: 12 комбинаций values (ingress без auth / с webConfig / с externalAuth; политика без источников / только контроллер без Ingress / с alertSenders; ServiceMonitor с webConfig без кредов; prometheusEnabled=false; prod как есть / по шагам / полностью) — каждая даёт ожидаемый fail или рендер.
- Дифф рендеров default/dev/lite с базой: метка `component` в pod template и metadata Service, переписанный ServiceMonitor. Больше ничего, не считая случайно генерируемого пароля.
- `helm lint` prod с placeholder'ами, `shellcheck scripts/release-gate.sh`, `git diff --check` — чисто.

## Review fix log (2026-10-01)

Находки deep-review со `fix-here` (R1–R8, R10, R11, R16):
- шаблоны: nil-safe чтение `networkPolicy` и `monitoring.serviceMonitor` (R1), `fail` на `podLabels` с component (R2), `externalAuth` только `true`/`"true"` (R3);
- доки: README (оговорки про `service.type` и общий контроллер, hostNetwork, vmalert, двусторонний smoke, namespace Secret'а), CHANGELOG (формулировка Security, первый upgrade без `--reuse-values`, общий случай `webConfig` + ServiceMonitor, `podLabels`);
- BUGS: `SERVICE-METRICS-PORT-MISROUTED`, `MONITORING-CRD-DEFAULT`; `helm/amp/.helmignore` с `/tests/`.

Проверено:
- матрица из 12 guard'ов без изменений;
- R2: `podLabels.component` → fail, другие ключи рендерятся;
- R3: строки `false`/`no`/`"false"`/`True`/`1` → fail, bool `true` и строка `"true"` → Ingress;
- R1: чарт ветки со значениями `main:helm/amp/values.yaml` рендерится. ServiceMonitor на месте с 30s/10s, политики AMP нет, с `webConfig` guard срабатывает;
- рендеры default/dev/lite совпадают с прошлым прогоном, не считая случайных паролей;
- `helm lint` prod с placeholder'ами чист, в `helm package` нет `tests/`.
- Повторное ревью (R18): неякорный `tests/` в `.helmignore` выкидывал и `templates/tests/postgresql-test-connection.yaml` (hook `helm test`) — из пакета и из `helm template`. Сравнение рендеров выше делалось до появления `.helmignore`, поэтому регрессию не поймало. Исправлено на `/tests/`: hook снова рендерится, в пакете `templates/tests/` есть, корневого `tests/` нет. Плюс доки по R19, R20.

## Write-tests log (2026-10-01)

- 5.0: `render-config-reloader.sh`, `render-image-tag.sh` — prod-рендер с `-f tests/values-production-placeholders.yaml`. Негативный кейс «prod как есть → fail» (R9) живёт в `render-ingress-auth.sh`.
- 5.1 `render-ingress-auth.sh` (16 assert'ов): Ingress нет по дефолту; без auth fail на дефолтах и в prod; строковые `externalAuth` (`false`/`no`/`"false"`/`True`/`1`) fail; `webConfig`, bool `true`, строка `"true"` рендерят Ingress. 5.2b там же: hook `templates/tests/` рендерится и пакуется, `tests/` не пакуется.
- 5.2 `render-networkpolicy.sh` (29): guard пустых источников (prod, дефолты, только контроллер без Ingress); политика prod — podSelector с component, правило на роль, один порт `http`, без Egress; контроллер только с `ingress.enabled`; `extraIngress`; метка только в pod template, селекторы Deployment и Service без неё; `podLabels` с component → fail; `from` политики redis; `networkPolicy=null` → рендер без политики.
- 5.2a `render-servicemonitor.sh` (21): дефолт (port `http`, `/metrics`, 30s/10s, без basicAuth); селектор выбирает ровно Service `amp`, а без component — 7 Service'ов; гейты `prometheusEnabled`/`serviceMonitor.enabled`, redis не затронут; guard basicAuth, ключи по умолчанию и свои; labels, interval; `monitoring.serviceMonitor=null` → рендер с дефолтами, guard работает.
- 5.3 мутации: 15 из 15 пойманы (`evidence/mutations-2026-10-01.txt`).
- Совместимость: helper'ы написаны под macOS bash 3.2 и BSD awk (без `mapfile`, без перевода строки в `awk -v`); извлечение документов — awk, без yq. `shellcheck helm/amp/tests/*.sh` чист.

## Testing log (2026-10-01) @ 45090d0

- `scripts/release-gate.sh` целиком — RESULT: PASS, 11/11 (build, lint, test, futureparity, race 391s, helm-deps, helm-dev, helm-production, helm-rbac, helm-tests, amtool-compat); сводка в `evidence/release-gate-2026-10-01.txt`. `helm-tests` (красный на implement, R9) теперь зелёный: 5 render-тестов.
- `git diff --check main...HEAD` — чисто; `shellcheck` gate + `helm/amp/tests/*.sh` — чисто.
- `quality-gates-fast` не запускался: Go-код не менялся (0 строк `*.go` в диффе), а build/lint/vet покрыты гейтом.
- 6.2 kind + Calico smoke — **не выполнялся**: `kind` не установлен, ставить новый инструмент в рамках задачи не стали. Не проверены вживую: kubelet probes через политику (premise `assumed`), реальное отсечение чужого pod'а, kill-switch, совет `ipBlock` для hostNetwork. Компенсация — двусторонняя smoke-проверка в README для оператора после install.

## Definition of Done

- [x] All steps are complete or explicitly marked blocked/skipped
- [x] Success criteria from `requirements.md` are covered
- [x] Contracts from `Spec.md` are implemented or deviations are recorded
- [x] Deep review verdict is `pass`, or deep review was not required
- [x] Tests for changed behavior are added or updated
- [x] Phase checks pass
- [x] Docs/planning are updated if behavior, contracts, or process changed
