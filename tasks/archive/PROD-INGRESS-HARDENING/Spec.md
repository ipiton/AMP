---
id: PROD-INGRESS-HARDENING
slug: prod-ingress-hardening
stream: Security
type: feature
status: draft
created_at: 2026-10-01
updated_at: 2026-10-01
based_on:
  - requirements.md
  - research.md
---

# Specification: Закрыть внешний и внутрикластерный доступ к AMP в прод-профиле

**Version:** 1.1 (2026-10-01: в scope добавлена починка ServiceMonitor — решение владельца)
**Status:** Draft

## Summary

Чарт перестаёт рендерить Ingress, если в процессе не включён auth и оператор явно не сказал, что auth обеспечивается снаружи. Появляется ingress-only NetworkPolicy для pod'ов AMP с явными источниками трафика; в `values-production.yaml` она включена. Bundled ServiceMonitor становится рабочим и совместимым с auth. Решение фиксируется как ADR-015.

## Requirements Coverage

| Requirement / Criterion | Covered by |
|---|---|
| Прод-профиль не рендерит Ingress без защиты, решение в ADR | D1, `ingress.yaml` guard, ADR-015 |
| NetworkPolicy для AMP с настраиваемыми источниками, включена в prod; решение по egress | D2–D4, `networkpolicy.yaml` |
| Render-тесты: prod без auth / с auth / opt-out; NetworkPolicy вкл/выкл, источники | Test Plan (`render-ingress-auth.sh`, `render-networkpolicy.sh`) |
| `release-gate.sh` зелёный | D6 (placeholder'ы, шаг `helm-tests`) |
| Доки + CHANGELOG с migration notes | Component Architecture: docs |
| US3 — рабочий ServiceMonitor с auth | D7, `servicemonitor.yaml`, `service.yaml` |
| US4 — явный opt-out для auth-proxy | D1 `ingress.externalAuth` |

## Current State

- **Code:**
  - `helm/amp/templates/ingress.yaml` рендерит Ingress по `ingress.enabled` без каких-либо проверок.
  - `values-production.yaml:337` включает nginx-Ingress на `/` без auth.
  - У pod'ов AMP метки только `amp.selectorLabels` (`deployment.yaml:24-28`).
  - NetworkPolicy есть только у postgres (в prod включена) и redis (выключена; её `from` требует `component: application`, которой нет ни у одного pod'а).
  - `webConfig.existingSecret` (PROD-AUTH, ADR-011) включает basic auth на весь API, кроме `/-/healthy` и `/-/ready`.
  - `servicemonitor.yaml` рендерится безусловно (CRD обязателен даже без prometheus-operator), селектит `app`/`release`, которых у Service нет, — не скрейпит ничего; `basicAuth` нет. ServiceMonitor и PrometheusRule redis gated на `monitoring.prometheusEnabled` (default `true`). Остальные Service чарта несут `selectorLabels` + свой `component`.
- **Data:** not applicable.
- **Tests:** `helm/amp/tests/render-config-reloader.sh`, `render-image-tag.sh` (вне гейта). Шаги гейта `helm-production` (lint+template prod) и `helm-rbac` (`scripts/release-gate.sh:166-168, 188-194, 288`).
- **Docs:** `helm/amp/values.yaml:138-158` (webConfig), `docs/CONFIGURATION_GUIDE.md` §4 (auth) и `:707` (`helm install -f values-production.yaml`), `helm/amp/README.md` (про Ingress/NetworkPolicy ничего нет).

## Design Premises

| Premise | Confirmed by | Class | If wrong |
|---|---|---|---|
| Ни один ключ values не отличает прод от дефолта | `values.yaml:5,15` и `values-production.yaml:17,19`: одинаковые `environment`/`profile` | code-read | guard можно было бы сузить до прод-профиля; на текущий дизайн не влияет |
| Ingress включён только в `values-production.yaml`; ни CI, ни e2e не рендерят чарт с Ingress, кроме гейта | grep `values-production\|ingress\|helm install` по `deploy/`, `.github/`, `scripts/`, `values*.yaml` | call-path-traced | guard уронит неучтённый потребитель |
| Реплики AMP не общаются друг с другом по сети, только через Redis и Postgres | `internal/infrastructure/cluster/heartbeat.go:1-8`; grep gossip/memberlist/peer — только Redis-реализации | code-read | NetworkPolicy рвёт HA-координацию; нужно правило AMP ↔ AMP |
| Процесс слушает один порт `http` (API, `/metrics`, probes); на `metrics:9090` ничего нет | `cmd/server/main.go:150` — единственный `ListenAndServe` | code-read | политика без 9090 отрежет живой порт |
| Kubelet probes с ноды проходят при любой NetworkPolicy | документация Kubernetes NetworkPolicy («traffic to and from the node … always allowed»); зависит от CNI | assumed | probes падают, pod'ы перезапускаются → риск в Rollout |
| Метка в pod template без изменения `spec.selector` — обычный rolling update | Deployment `spec.selector` и template — разные поля; селектор immutable, template — нет | code-read | `helm upgrade` падает с immutable-ошибкой |
| `from` в `redis-networkpolicy.yaml:35` начнёт матчить pod'ы AMP после добавления метки | `redis-networkpolicy.yaml:31-35` (selectorLabels + `component: application`) | code-read | политика redis останется сломанной (сейчас не включена) |
| Bundled ServiceMonitor сейчас не скрейпит ничего; починка добавляет новую нагрузку скрейпа, но не ломает работающего | `servicemonitor.yaml` селектит `app`/`release`, у `service.yaml` метки только `amp.labels` | code-read | у кого-то уже работающий скрейп получит дубли; CHANGELOG |
| Селектор `selectorLabels + component: application` по metadata Service выбирает только Service AMP | `postgresql-service.yaml:7-9`, `postgresql-exporter-service.yaml:7-9`, `redis-service.yaml:20-22,46-48`: у всех свой `component` | code-read | ServiceMonitor скрейпит чужой Service |
| prometheus-operator берёт Secret для `basicAuth` из namespace ServiceMonitor, поля `basicAuth.username/password` — `SecretKeySelector` | prometheus-operator `Documentation/user-guides/basic-auth.md`, `api-reference/api.md` (`Endpoint.basicAuth`, `BasicAuth`), сверено через context7 2026-10-01; status-пример: «Referenced Secret … in namespace 'monitoring'» — namespace ServiceMonitor | code-read (upstream docs) | скрейп 401 при правильном Secret → риск в Rollout |

## Target Design

1. **Guard на Ingress (D1).** `ingress.yaml` вызывает `fail`, если `ingress.enabled`, `webConfig.existingSecret` пуст и `ingress.externalAuth` не `true`. Проверка работает в любом профиле: по-другому прод не отличить.
2. **Метка pod'а (D3).** В pod template Deployment добавляется `app.kubernetes.io/component: application`. Селекторы Deployment и Service не меняются.
3. **NetworkPolicy (D2, D4).** Новый `networkpolicy.yaml` выбирает pod'ы по `selectorLabels + component: application` и открывает только порт `http`. Источники — списки `NetworkPolicyPeer` по ролям: `ingressController` (учитывается только при `ingress.enabled`), `alertSenders`, `metricsScrapers`; плюс сырые правила `extraIngress`. Egress не ограничивается.
4. **Fail на пустых источниках.** Если политика включена, а действующих источников нет, рендер падает: молча отрезанные отправители алертов хуже ошибки на `helm upgrade`.
5. **Прод-values.** `values-production.yaml` включает политику с пустыми списками и закомментированными примерами. Оператор обязан назвать свои namespace; угаданный дефолт при промахе молча теряет алерты.
6. **ServiceMonitor (D7).** Рендерится при `monitoring.prometheusEnabled && monitoring.serviceMonitor.enabled`, выбирает Service AMP по `component: application` (метка добавляется в metadata Service, селектор не трогаем), скрейпит `http` `/metrics`, при заданном `basicAuth.secretName` передаёт креды. Если включён `webConfig`, а basicAuth нет — `fail`.
7. **Гейт.** Новые render-тесты подключаются в `release-gate.sh`; рендер prod в гейте получает placeholder'ы.

## Key Decisions

- **D1. Ключ opt-out — `ingress.externalAuth: false`.** `true` означает «защиту даёт Ingress/proxy». Чарт этого не проверяет: аннотации контроллер-специфичны, проверка дала бы ложную уверенность. Ключ — осознанное заявление оператора, и в сообщении `fail` названы оба выхода.
- **D2. Только Ingress-направление, без egress.** Адреса нотификаций произвольны (research Q3); egress-политика молча убьёт доставку. Кому нужно ограничение egress, пишет свою политику. Фиксируется в ADR-015.
- **D3. Селектор Service не меняется.** Если сменить его в том же релизе, старые pod'ы выпадут из endpoints раньше, чем новые станут ready, и приём алертов прервётся. То, что Service матчит postgres/redis через порт `metrics`, — отдельный баг `SERVICE-METRICS-PORT-MISROUTED`.
- **D4. Только порт `http`, по имени.** `ports: [{protocol: TCP, port: http}]` — NetworkPolicy поддерживает именованные порты. Мёртвый 9090 не открываем.
- **D5. Правило на каждую роль** (`- from: <peers>` + ports). Так в отрендеренном объекте видно, кто зачем пущен; семантика та же, что у одного объединённого правила.
- **D6. Гейт.** Рендер prod в `step_helm_values` и `step_helm_rbac` получает `--set webConfig.existingSecret=release-gate-placeholder --set networkPolicy.alertSenders[0].namespaceSelector.matchLabels.kubernetes\.io/metadata\.name=release-gate-placeholder`. Точную форму `--set` подобрать при реализации (точки в ключе экранируются), допустим `-f` с временным файлом. Новый шаг `helm-tests` запускает `helm/amp/tests/*.sh`: закрывает `HELM-RENDER-TEST-IN-GATE` (~0.1d, тот же файл). Оба существующих теста на 2026-10-01 зелёные (`render-config-reloader.sh`, `render-image-tag.sh`: rc=0), поэтому шаг подключает весь каталог.

- **D7. ServiceMonitor.**
  - Gate: существующий `monitoring.prometheusEnabled` (как у redis) **и** новый `monitoring.serviceMonitor.enabled: true`. Второй ключ — выход из `fail` без отключения мониторинга redis. Default «нужен CRD» для всего семейства `monitoring.prometheusEnabled` не меняем (касается и redis): отдельная запись `MONITORING-CRD-DEFAULT` в BUGS.
  - Селектор: `selectorLabels + app.kubernetes.io/component: application`; ту же метку получает `metadata.labels` Service (не `spec.selector`). `namespace` — как у Service; `namespaceSelector.matchNames: [<namespace>]`.
  - Метки: `amp.labels` + `monitoring.serviceMonitor.labels` (для `serviceMonitorSelector` Prometheus, например `release: kube-prometheus-stack`). Старые `app`/`release: <AMP release>` убираются — по ним никто не мог селектить осмысленно.
  - Endpoint: `port: http`, `path: /metrics`, `interval`/`scrapeTimeout` из values (30s/10s как сейчас); `basicAuth.username/password` = `{name: secretName, key: usernameKey|passwordKey}`.
  - `fail`, если `webConfig.existingSecret` задан, ServiceMonitor рендерится и `basicAuth.secretName` пуст: скрейп, который всегда получает 401, виден только по пустым графикам.

## API Contracts

### Helm values (контракт чарта)

```yaml
ingress:
  externalAuth: false      # true: auth на Ingress/proxy; снимает требование webConfig.existingSecret

networkPolicy:
  enabled: false           # values-production.yaml: true
  ingressController: []    # []NetworkPolicyPeer; правило рендерится только при ingress.enabled
  alertSenders: []         # []NetworkPolicyPeer: Prometheus, vmalert, … пишущие в Service напрямую
  metricsScrapers: []      # []NetworkPolicyPeer: кто скрейпит /metrics
  extraIngress: []         # []NetworkPolicyIngressRule, рендерятся как есть

monitoring:
  prometheusEnabled: true  # существующий ключ, без изменений
  serviceMonitor:
    enabled: true          # app ServiceMonitor; требует monitoring.prometheusEnabled
    interval: 30s
    scrapeTimeout: 10s
    labels: {}             # доп. метки для serviceMonitorSelector Prometheus
    basicAuth:
      secretName: ""       # Secret в namespace релиза; обязателен при webConfig.existingSecret
      usernameKey: username
      passwordKey: password  # пароль в открытом виде (не bcrypt-хеш из web-config)
```

- **Pod label:** `app.kubernetes.io/component: application` у pod'ов AMP; можно использовать в селекторах извне.
- **Ошибки рендера** (тексты в тестах, по подстроке):
  - `ingress.enabled=true exposes the whole AMP API … set webConfig.existingSecret (in-process basic auth) or ingress.externalAuth=true (auth enforced by the ingress/proxy)`
  - `networkPolicy.enabled=true with no sources would block every alert sender … set networkPolicy.alertSenders / ingressController / metricsScrapers / extraIngress`
  - `webConfig.existingSecret protects /metrics, but the ServiceMonitor has no credentials … set monitoring.serviceMonitor.basicAuth.secretName or monitoring.serviceMonitor.enabled=false`
- **Breaking:** `values-production.yaml` без доп. настроек больше не рендерится (auth, источники, basicAuth ServiceMonitor). ServiceMonitor AMP больше не рендерится при `monitoring.prometheusEnabled=false` и начинает реально скрейпить. Migration notes — в CHANGELOG.

## Data Model / Migrations

Not applicable.

## Component Architecture

- `helm/amp/templates/ingress.yaml` — guard D1 в начале, внутри `if .Values.ingress.enabled`.
- `helm/amp/templates/networkpolicy.yaml` — новый: политика + guard пустых источников.
- `helm/amp/templates/deployment.yaml` — метка component в `spec.template.metadata.labels`.
- `helm/amp/templates/service.yaml` — метка component в `metadata.labels` (селектор без изменений).
- `helm/amp/templates/servicemonitor.yaml` — D7: gate, namespace, селектор, метки, basicAuth, guard.
- `helm/amp/values.yaml` — `ingress.externalAuth`, секция `networkPolicy` с комментариями: роли, примеры `kubernetes.io/metadata.name`, CNI-зависимость, kill-switch.
- `helm/amp/values-production.yaml` — `networkPolicy.enabled: true` + закомментированные примеры; комментарий у `ingress:` про требование auth; комментарий у `monitoring:` про `serviceMonitor.basicAuth`.
- `helm/amp/tests/render-ingress-auth.sh`, `render-networkpolicy.sh` — новые, формат `render-config-reloader.sh`.
- `scripts/release-gate.sh` — placeholder'ы (D6), шаг `helm-tests`.
- Docs: `helm/amp/README.md` (раздел «Network exposure»: guard, opt-out, NetworkPolicy, smoke-проверка, kill-switch), `docs/CONFIGURATION_GUIDE.md` §4 (ссылка + `:707` — что нужно перед `helm install`), `CHANGELOG.md` `[Unreleased]` (Security + Breaking/migration notes), `helm/amp/CHANGELOG.md`, `docs/06-planning/DECISIONS.md` ADR-015.
- Planning (на finalize): BUGS `SERVICE-METRICS-PORT-MISROUTED`, `MONITORING-CRD-DEFAULT` (ServiceMonitor/PrometheusRule redis и app требуют CRD при дефолтах); TECH-DEBT — nginx sticky-аннотации в `ingress.yaml`, хардкод `name: monitoring` в политиках postgres/redis; закрыть `HELM-RENDER-TEST-IN-GATE`, если D6 сделан полностью.

## Security Design

- [x] Ownership validation — not applicable (чарт).
- [x] Input validation: `fail` при рендере на две опасные комбинации (D1, пустые источники). Структура peers не валидируется, её проверяет API server при apply.
- [x] Sensitive data not logged: секреты только через `webConfig.existingSecret`; новых секретов в values нет.
- [x] Rate limiting — not applicable.
- [x] Auth path: in-process basic auth (ADR-011) или явное заявление `externalAuth`; NetworkPolicy — defense in depth, не замена auth.

## Invariants

- [ ] `helm template` с `values.yaml` и `values-dev.yaml` рендерится без изменений, кроме метки component (нет Ingress, нет NetworkPolicy).
- [ ] `spec.selector` Deployment и `spec.selector` Service не меняются.
- [ ] ServiceMonitor AMP селектит ровно один Service чарта.
- [ ] Ни одна комбинация values не даёт ServiceMonitor без кредов при включённом `webConfig`.
- [ ] Ни одна комбинация values не даёт Ingress без `webConfig.existingSecret` или `externalAuth: true`.
- [ ] Включённая политика никогда не рендерится без хотя бы одного источника.
- [ ] Политика не содержит `Egress` в `policyTypes`.
- [ ] Шаг `helm-rbac` и остальные шаги гейта остаются зелёными.

## Edge Cases

1. `ingress.enabled=true`, `webConfig.existingSecret` задан → Ingress рендерится, как раньше.
2. `ingress.externalAuth=true` без аннотаций → рендерится: это заявление оператора (D1); в README сказано, что проверки нет.
3. `networkPolicy.enabled`, есть только `ingressController`, а `ingress.enabled=false` → источников нет → fail.
4. Политика включена, есть только `extraIngress` → рендер; правила выводятся как есть.
5. Отправители вне кластера через Ingress → покрыты `ingressController`. Через LoadBalancer/NodePort → источник — IP ноды/LB, нужен `ipBlock` в `alertSenders`/`extraIngress` (README).
6. CNI без поддержки NetworkPolicy → объект создаётся и ничего не делает (fail-open); README даёт smoke-проверку.
7. `profile=lite` → политика применима так же: pod'ы и метка те же.
8. configReloader в том же pod'е → не затронут; его порт `reloader` наружу не нужен.
9. `monitoring.prometheusEnabled=false` → нет ServiceMonitor, guard D7 не срабатывает.
10. `webConfig` + `serviceMonitor.enabled=false` → рендер; оператор скрейпит своим способом.
11. `basicAuth.secretName` без `webConfig` → креды передаются, AMP их игнорирует — допустимо, без ошибки.
12. NetworkPolicy включена, ServiceMonitor есть, `metricsScrapers` пуст → Prometheus получает timeout. Не fail (Prometheus может быть в `alertSenders`), но README называет `metricsScrapers` рядом с ServiceMonitor.

## Impact Analysis

- **Affected modules:** только `helm/amp/`, `scripts/release-gate.sh`, docs. Go-код не меняется.
- **Breaking changes:** `values-production.yaml` требует `webConfig.existingSecret` или `ingress.externalAuth=true`, плюс источники `networkPolicy.*`. Любой install с `ingress.enabled=true` без auth теперь падает. Одноразовый rolling restart из-за метки.
- **New dependencies:** none.
- **Risks:**
  - Неверные источники → отправители получают timeout, алерты не доходят. Митигация: fail на пустых списках, README с метриками отправителя (`prometheus_notifications_errors_total`, `prometheus_notifications_dropped_total`), kill-switch.
  - Probes на экзотическом CNI (premise `assumed`) → см. Rollout.

## Rollout / Rollback

- **Rollout:**
  1. Оператор задаёт auth и источники по migration notes.
  2. `helm upgrade` — один rolling restart.
  3. Smoke из README: pod в чужом namespace → `curl` к Service даёт timeout; отправитель из списка доходит; probes зелёные.
- **Rollback:**
  - kill-switch `helm upgrade --reuse-values --set networkPolicy.enabled=false` удаляет политику без рестарта;
  - `helm rollback` возвращает всё (один restart).
  - Guard'ы срабатывают на рендере: неудавшийся upgrade ничего не меняет в кластере.
- **Риск:** если `basicAuth` ServiceMonitor не подхватывается (namespace Secret, версия operator), target в Prometheus показывает `401`. Действие: проверить Secret в namespace релиза и ключи; временно `monitoring.serviceMonitor.enabled=false`.
- **Риск (premise assumed):** если CNI режет kubelet probes, pod'ы уходят в restart сразу после upgrade. Признак — `Readiness probe failed: … timeout`. Действие — kill-switch и `ipBlock` с CIDR нод в `extraIngress`. В README.
- **Feature flag:** `networkPolicy.enabled`.

## Observability

- **Logs:** not applicable (чарт). Ошибки видны в выводе `helm`.
- **Metrics:** новых нет. В README для обнаружения потерь — метрики отправителя Prometheus (`prometheus_notifications_errors_total`, `prometheus_notifications_dropped_total`) и падение ingest-метрик AMP.
- **Alerts:** not applicable; пример правила на `prometheus_notifications_dropped_total` — в README.

## Test Plan

- `render-ingress-auth.sh`:
  - default → нет Ingress;
  - prod + обязательные пароли, без auth → fail, сообщение по подстроке;
  - prod + `webConfig.existingSecret` → есть `kind: Ingress`;
  - prod + `ingress.externalAuth=true` → есть Ingress;
  - `--set ingress.enabled=true` на дефолтах → fail (guard не привязан к prod-файлу).
- `render-servicemonitor.sh`:
  - default → один app ServiceMonitor, селектор с `component: application`, у Service та же метка в metadata, `spec.selector` Service не изменился;
  - селектор совпадает только с Service AMP (не с postgres/redis/exporter Service);
  - `monitoring.prometheusEnabled=false` или `serviceMonitor.enabled=false` → нет app ServiceMonitor;
  - `webConfig.existingSecret` без basicAuth → fail; с `basicAuth.secretName` → `basicAuth.username/password` с нужными `name`/`key`;
  - `serviceMonitor.labels` попадают в metadata.
- `render-networkpolicy.sh`:
  - default → нет `kind: NetworkPolicy` от приложения;
  - prod без источников → fail;
  - prod с `alertSenders` → политика: `podSelector` с `component: application`, только порт `http`, нет `Egress`, peers проходят как есть;
  - `ingressController` при `ingress.enabled=false` (и при этом без других источников) → fail, а с `alertSenders` правила контроллера нет;
  - `extraIngress` выводится;
  - pod template несёт метку, `spec.selector` Deployment — только selectorLabels;
  - при `valkey.networkPolicy.enabled=true` `from` политики redis совпадает с метками pod'а AMP.
- Мутационная проверка (как в `PROD-RELEASE-V010-PREP`): убрать guard / метку / проверку источников → соответствующий тест краснеет.
- `scripts/release-gate.sh` целиком; `git diff --check`.
- Живого кластера нет: kind + Calico smoke — опционально, если окажется дешёвым; иначе осознанное ограничение в итоге.

## Deep Review

- **Mandatory triggers present:** `S`, 3 сигнала (`S C R`).
- **Discretionary triggers present:** none.
- **Decision:** required.

## Open Questions

- [x] Починка ServiceMonitor — включена в задачу (владелец, 2026-10-01), D7.
- [x] Формат `basicAuth` в `monitoring.coreos.com/v1` сверен с документацией prometheus-operator (2026-10-01).
- [ ] Premise `assumed`: kubelet probes проходят при NetworkPolicy на целевых CNI. Риск и действие — в Rollout; проверка — kind + Calico smoke, если дёшево.
