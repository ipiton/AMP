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

**Based on:** requirements.md / research.md / Spec.md v1.0
**Date:** 2026-10-01

Обозначения в verify:
- `PW` = `--set postgresql.password=x --set cache.auth.password=x` (обязательные пароли prod-values);
- `PROD` = `-f helm/amp/values-production.yaml $PW`;
- `SRC` = `--set-json 'networkPolicy.alertSenders=[{"namespaceSelector":{"matchLabels":{"kubernetes.io/metadata.name":"monitoring"}}}]'`.

## Touched Files

- `helm/amp/templates/ingress.yaml` — guard D1.
- `helm/amp/templates/networkpolicy.yaml` — новый: политика AMP и guard пустых источников.
- `helm/amp/templates/deployment.yaml` — метка `app.kubernetes.io/component: application` в pod template.
- `helm/amp/values.yaml` — `ingress.externalAuth`, секция `networkPolicy`.
- `helm/amp/values-production.yaml` — `networkPolicy.enabled: true` + примеры, комментарий у `ingress:`.
- `scripts/release-gate.sh` — placeholder'ы для prod-рендера, шаг `helm-tests`.
- `helm/amp/tests/render-ingress-auth.sh`, `helm/amp/tests/render-networkpolicy.sh` — новые (phase 5).
- `helm/amp/README.md`, `docs/CONFIGURATION_GUIDE.md`, `CHANGELOG.md`, `helm/amp/CHANGELOG.md`, `docs/06-planning/DECISIONS.md` — docs, ADR-015.

## Phase 1: Чарт

> **Wave 1** — независимые шаги

- [ ] **1.1** `values.yaml`: добавить `ingress.externalAuth: false` и комментарий: что значит `true`, чарт этого не проверяет, guard D1. <!-- verify: helm template amp helm/amp >/dev/null && grep -n "externalAuth" helm/amp/values.yaml -->
- [ ] **1.2** `values.yaml`: секция `networkPolicy` (`enabled: false`, `ingressController`, `alertSenders`, `metricsScrapers`, `extraIngress` — пустые списки). В комментариях: роли, пример с `kubernetes.io/metadata.name`, `ipBlock` для LB/NodePort, fail-open на CNI без поддержки, kill-switch, отсутствие egress. <!-- verify: helm template amp helm/amp | grep -c "kind: NetworkPolicy" → 0 -->
- [ ] **1.3** `deployment.yaml`: `app.kubernetes.io/component: application` в `spec.template.metadata.labels` (после `selectorLabels`, до `podLabels`); `spec.selector` не трогать. <!-- verify: helm template amp helm/amp -s templates/deployment.yaml | grep -n -B2 -A4 "component: application" — метка только в template, в selector.matchLabels её нет -->

> **Wave 2** — зависит от Wave 1

- [ ] **1.4** `ingress.yaml`: guard D1 — `fail` с текстом из Spec §API Contracts, если `ingress.enabled && !webConfig.existingSecret && !ingress.externalAuth`. <!-- depends: 1.1 | verify: helm template amp helm/amp --set ingress.enabled=true → fail с "ingress.externalAuth"; с --set webConfig.existingSecret=s → kind: Ingress; с --set ingress.externalAuth=true → kind: Ingress -->
- [ ] **1.5** `templates/networkpolicy.yaml`:
  - рендер при `networkPolicy.enabled`;
  - `podSelector` = selectorLabels + `component: application`;
  - `policyTypes: [Ingress]`;
  - по правилу на каждую непустую роль (`ingressController` — только при `ingress.enabled`), порт `{protocol: TCP, port: http}`;
  - `extraIngress` — как есть;
  - `fail` при отсутствии действующих источников.
  <!-- depends: 1.2, 1.3 | verify: helm template amp helm/amp --set networkPolicy.enabled=true → fail "no sources"; + $SRC → NetworkPolicy, port: http, без Egress; kubeconform/`helm lint` чисто -->
- [ ] **1.6** `values-production.yaml`:
  - `networkPolicy.enabled: true`, списки пустые, закомментированные примеры для ingress-nginx и monitoring;
  - комментарий у `ingress:` про требование webConfig/externalAuth.
  <!-- depends: 1.4, 1.5 | verify: helm template amp helm/amp $PROD → fail (auth); + --set webConfig.existingSecret=s → fail (sources); + $SRC → рендер с Ingress и NetworkPolicy -->

**Phase verification:**
- `helm template` для default/dev/lite рендерится, NetworkPolicy и Ingress там нет;
- `diff` рендера default до/после — только метка component;
- `helm lint helm/amp $PROD --set webConfig.existingSecret=s $SRC`.

## Phase 2: Гейт

- [ ] **2.1** `release-gate.sh`: в `step_helm_values` (prod) и `step_helm_rbac` (production) добавить `--set webConfig.existingSecret=release-gate-placeholder` и источник-placeholder (`--set-json` или временный values-файл). Комментарий — почему placeholder, а не ослабление guard. <!-- depends: 1.6 | verify: scripts/release-gate.sh — шаги helm-production и helm-rbac PASS -->
- [ ] **2.2** `release-gate.sh`: шаг `helm-tests` — запуск всех `helm/amp/tests/*.sh`; падение любого — FAIL шага с именем скрипта. <!-- verify: шаг PASS на текущих двух тестах; временно сломанный тест (exit 1) → FAIL шага -->

**Phase verification:** `scripts/release-gate.sh` все шаги PASS.

## Phase 3: Документация и ADR

- [ ] **3.1** `DECISIONS.md` ADR-015: guard на Ingress вместо «прод-профиля», смысл `externalAuth`, ingress-only без egress, селектор Service не меняем, fail на пустых источниках. <!-- verify: grep -n "ADR-015" docs/06-planning/DECISIONS.md -->
- [ ] **3.2** `helm/amp/README.md` раздел «Network exposure»:
  - guard и два выхода;
  - примеры аннотаций nginx `auth-url`/`auth-secret` для `externalAuth`;
  - NetworkPolicy: роли, примеры, `ipBlock`;
  - smoke-проверка «чужой pod → timeout»;
  - обнаружение потерь (`prometheus_notifications_errors_total`/`_dropped_total`);
  - kill-switch;
  - probes на нестандартном CNI.
  <!-- verify: ручная вычитка; команды из README прогнаны через helm template -->
- [ ] **3.3** `docs/CONFIGURATION_GUIDE.md`: §4 — ссылка на README; у `helm install -f values-production.yaml` (`:707`) — что задать до установки. <!-- verify: grep -n "externalAuth\|networkPolicy" docs/CONFIGURATION_GUIDE.md -->
- [ ] **3.4** `CHANGELOG.md` `[Unreleased]`: `### Security` и `### Breaking changes / migration notes` (шаги апгрейда для values-production); `helm/amp/CHANGELOG.md`. <!-- verify: grep -n "PROD-INGRESS-HARDENING" CHANGELOG.md helm/amp/CHANGELOG.md -->

**Phase verification:** `git diff --check`; ссылки и ключи в доках совпадают с `values.yaml`.

## Phase 4: Deep review (обязателен)

- [ ] **4.1** `/deep-review` по диффу phases 1–3 → `review-findings.md`, `review-verdict.json`. <!-- depends: 1.*, 2.*, 3.* | verify: jq -r .gate tasks/PROD-INGRESS-HARDENING/review-verdict.json → pass -->

## Phase 5: Тесты (после `gate: pass`)

- [ ] **5.1** `helm/amp/tests/render-ingress-auth.sh` — кейсы из Spec §Test Plan. <!-- depends: 4.1 | verify: bash helm/amp/tests/render-ingress-auth.sh → rc=0 -->
- [ ] **5.2** `helm/amp/tests/render-networkpolicy.sh` — кейсы из Spec §Test Plan, включая совпадение `from` политики redis с метками pod'а и неизменный `spec.selector`. <!-- depends: 4.1 | verify: bash helm/amp/tests/render-networkpolicy.sh → rc=0 -->
- [ ] **5.3** Мутации: убрать guard D1 / метку / guard пустых источников / добавить Egress — каждая ловится тестом; откатить. <!-- depends: 5.1, 5.2 | verify: записать результат в testing-лог -->

## Phase 6: Testing и finalize

- [ ] **6.1** `scripts/release-gate.sh` целиком (шаг `helm-tests` подхватывает новые тесты); `git diff --check`; Go-код не менялся — `quality-gates-fast` для контроля. <!-- verify: все шаги PASS -->
- [ ] **6.2** Опционально: kind + Calico smoke (разрешённый отправитель, чужой pod, probes, kill-switch). Если дорого — осознанное ограничение в итоге. <!-- verify: лог в evidence/ или запись «не выполнялось» -->
- [ ] **6.3** `/finalize`:
  - BUGS `SERVICEMONITOR-DEAD`, `SERVICE-METRICS-PORT-MISROUTED`;
  - TECH-DEBT: sticky-аннотации, хардкод `name: monitoring`;
  - BACKLOG: `PROD-INGRESS-HARDENING`, `HELM-RENDER-TEST-IN-GATE` → закрыты;
  - DONE: ротация `DONE.md` → `archive/DONE-2026-09.md` (первая задача октября).
  <!-- verify: /qa-check -->

## Definition of Done

- [ ] All steps are complete or explicitly marked blocked/skipped
- [ ] Success criteria from `requirements.md` are covered
- [ ] Contracts from `Spec.md` are implemented or deviations are recorded
- [ ] Deep review verdict is `pass`, or deep review was not required
- [ ] Tests for changed behavior are added or updated
- [ ] Phase checks pass
- [ ] Docs/planning are updated if behavior, contracts, or process changed
