---
id: PROD-INGRESS-HARDENING
slug: prod-ingress-hardening
stream: Security
type: feature
artifact: research-pack
status: complete
created_at: 2026-10-01
updated_at: 2026-10-01
---

# Research Pack - Закрыть внешний и внутрикластерный доступ к AMP в прод-профиле

Research level 3: триггеры security + infrastructure + несколько вариантов (Risk `S C R`).

## 0) TL;DR

- Решаем: прод-профиль чарта не должен выставлять анонимный API ни через Ingress, ни внутри кластера.
- Нашли: отличить «прод» по ключу нельзя: `environment: production` стоит и в `values.yaml`. Поэтому guard вешается на сам Ingress. Под AMP не отличить селектором от pod'ов postgres/redis. Политика redis из-за этого сейчас сломана, а ServiceMonitor не матчит ничего.
- Выбрали: `fail` при `ingress.enabled` без auth (есть opt-out для внешнего auth), метка `component: application` на pod'ах, ingress-only NetworkPolicy только на порт `http` с явными источниками. При пустых источниках — `fail`, чтобы алерты не терялись молча.
- Риски: breaking для `values-production.yaml`, установка не пройдёт без двух новых настроек; на CNI без NetworkPolicy политика молча ничего не делает.
- Next step: -> spec

## 1) Questions

1. По какому признаку чарт определит прод-профиль, чтобы повесить на него `fail`?
2. Общаются ли реплики AMP напрямую? Нужно ли правило AMP ↔ AMP?
3. Нужна ли egress-политика?
4. Как задать источники трафика (ingress-контроллер, отправители алертов, скрейперы) без хардкода `name: monitoring`?
5. Что сломает обязательный auth в проде: probes, `/metrics`, sidecar?
6. Как выбрать pod'ы AMP селектором, не задев postgres/redis и не тронув immutable `spec.selector`?

## 2) Findings

- **Q1.** `environment: production` стоит и в `values.yaml:5`, и в `values-production.yaml:17`. `profile` в обоих — `standard` (`values.yaml:15`, `values-production.yaml:19`). Ключа, означающего «прод», нет. Ingress включён только в `values-production.yaml:337`; в `values.yaml:98` и `values-dev.yaml:102` он выключен. Прецедент запрета несовместимой конфигурации — `fail` в `deployment.yaml:37` (configReloader + webConfig).
- **Q2.** Прямой связи между репликами нет. HA и heartbeat идут через Redis (`go-app/internal/infrastructure/cluster/heartbeat.go:1-8`) и Postgres, gossip-порта нет. Правило AMP ↔ AMP не нужно.
- **Q3.** AMP ходит наружу на произвольные адреса: Slack, PagerDuty, webhooks, LLM API, K8s API для target discovery, плюс postgres, redis, DNS. Default-deny egress молча убьёт нотификации. Существующие egress-правила это подтверждают: у postgres выход на S3 разрешён только `podSelector: {}` (свой namespace, `postgresql-networkpolicy.yaml:60-64`), то есть фактически закрыт.
- **Q4.** Обе существующие политики хардкодят Prometheus как `namespaceSelector name: monitoring` + метку app (`postgresql-networkpolicy.yaml:37-44`, `redis-networkpolicy.yaml:44-50`). Метка `name` на namespace сама не ставится; автоматическая — `kubernetes.io/metadata.name`.
- **Q5.**
  - Процесс слушает один порт (`cmd/server/main.go:150`, `service.port` 8080, `http`): API, `/metrics` и probes. `containerPort metrics` 9090 никто не слушает.
  - Probes ходят на `/-/healthy` и `/-/ready`, которые без auth по умолчанию (`internal/config/config.go:291`). Kubelet-трафик с ноды NetworkPolicy не режет.
  - При auth `/metrics` закрыт, а `server.auth.unauthenticated_paths` из чарта не задать (только через `configFile`).
  - **Регрессии не будет:** `templates/servicemonitor.yaml` селектит `app:`/`release:`, а у Service только `app.kubernetes.io/*`, поэтому он не матчит ни один Service уже сейчас. Он же рендерится безусловно: на кластере без CRD prometheus-operator установка падает (`helm template` с дефолтами даёт 2 `ServiceMonitor`).
  - Sidecar config-reloader живёт в том же pod'е; с webConfig он и так запрещён.
- **Q6.**
  - У pod'ов AMP только `amp.selectorLabels`, без `component` (`deployment.yaml:24-28`). У postgres и redis — `selectorLabels` + `component` (`redis-statefulset.yaml:43-45`). Селектор только по `selectorLabels` задевает все три.
  - Следствия:
    - `redis-networkpolicy.yaml:35` пускает только `component: application`, такой метки нет ни у кого. Если включить `valkey.networkPolicy`, app → redis будет заблокирован (в проде пока выключена).
    - Service AMP селектит и postgres/redis. У них есть порт `metrics` (`postgresql-statefulset.yaml:168`, `redis-statefulset.yaml:205`), поэтому порт Service `metrics:9090` ведёт на экспортёры, а не на AMP. Порт `http` есть только у AMP, его endpoints корректны.
  - Метку можно добавить в template pod'а: `spec.selector` Deployment не меняется, будет обычный rolling restart.
  - Менять селектор Service опасно: helm применит Service раньше, чем новые pod'ы станут ready. Старые pod'ы сразу выпадут из endpoints, и приём алертов прервётся.
- **Constraints:** fail-closed без молчаливой потери алертов. Секреты только через `existingSecret`. `values.yaml`/`values-dev.yaml` не ломаются. release-gate рендерит `values-production.yaml` с placeholder'ами (`scripts/release-gate.sh:166-168, 188`).
- **Unknowns:** живого кластера с CNI в гейте нет. Поведение политики проверяем только рендером; ручной smoke на kind + Calico — на усмотрение spec.

## 3) Options

`Generation:` parallel (minimal-diff, reuse-first, operational/reversibility)

**Сошлись 3 из 3 линз:**
- guard на Ingress с opt-out-ключом;
- метка `component: application` только в pod template;
- `policyTypes: [Ingress]` без egress;
- без правила AMP ↔ AMP;
- Option «выключить Ingress в прод-values» отвергнута.

**Расхождения:**
- ServiceMonitor: не трогать / opt-in с basicAuth и `fail` / basicAuth с предупреждением в NOTES.
- Модель источников: один сырой список `from` / группы в стиле Bitnami и Loki + client-label / именованные списки peers.
- Порт `metrics`: разрешить / удалить / разрешать только `http`.

### Option A - Guard на Ingress + app NetworkPolicy с именованными источниками (рекомендована)

- `ingress.yaml`: `fail`, если `ingress.enabled && !webConfig.existingSecret && !ingress.externalAuth`. Работает в любом профиле; дефолтных values не касается.
- Pod template: `app.kubernetes.io/component: application`. Политика redis начинает работать без правок.
- Новый `templates/networkpolicy.yaml`: `podSelector` = selectorLabels + component, только порт `http`.
- Источники — списки `NetworkPolicyPeer`:
  - `ingressController` — только при `ingress.enabled`;
  - `alertSenders`;
  - `metricsScrapers`;
  - плюс `extraIngress` (сырые правила).
- `fail`, если политика включена, а пиров нет совсем: иначе молча отрезаны все отправители.
- `values-production.yaml`: `networkPolicy.enabled: true`, списки пустые, примеры закомментированы с `kubernetes.io/metadata.name`. Установка не пройдёт, пока оператор не назовёт свои namespace. Угаданный дефолт (`ingress-nginx`) хуже: при промахе алерты теряются молча.
- **Pros:** fail-closed на обеих осях; ошибка видна на `helm upgrade`, а не по пропавшим алертам; откат — `--set networkPolicy.enabled=false` без рестарта.
- **Cons:** две обязательные настройки для `values-production`; разделение источников по ролям условное — все они ходят в один порт `http`.
- **Cost/Risk:** medium

### Option B - Один сырой список `networkPolicy.from` (minimal-diff)

- Тот же guard и та же метка. Политика `from: {{ toYaml .Values.networkPolicy.from }}`; `fail` при пустом списке.
- **Pros:** минимум ключей и шаблонной логики.
- **Cons:** правило для контроллера не связано с `ingress.enabled`; в values не видно, кого именно надо перечислить. Оператор, у которого алерты идут мимо Ingress, легко забудет отправителей.
- **Cost/Risk:** low

### Option C - Конвенции Bitnami целиком (`allowExternal`, client-label, `allowExternalEgress`) + починка ServiceMonitor (reuse-first)

- **Pros:** знакомые операторам ключи; ServiceMonitor станет рабочим (opt-in, правильный селектор, basicAuth).
- **Cons:** `allowExternal: true` по умолчанию — это fail-open, в проде его надо явно выключать. Починка ServiceMonitor и удаление порта 9090 — отдельные изменения контракта. Объём выходит за ~0.5d.
- **Cost/Risk:** medium-high

## 4) Decision

- **Chosen:** Option A. ServiceMonitor и порт `metrics` в задачу не входят — отдельные записи.
- **Why:**
  - Только guard на Ingress закрывает US1/US3 в любом профиле (Q1). Opt-out `ingress.externalAuth` даёт явный путь для oauth2-proxy и аннотаций Ingress.
  - Именованные списки показывают оператору, кого перечислить. `fail` на пустой политике превращает «тихую потерю алертов» в ошибку рендера — худший исход для alerting-системы.
  - Селектор Service не меняем (Q6, разрыв endpoints при rollout). Политика открывает только `http` — единственный живой порт.
  - ServiceMonitor уже ничего не скрейпит (Q5), значит обязательный auth не даёт регрессии. Его починку держим отдельной задачей, чтобы не раздувать security-дифф.

## 5) Spec Inputs

- **API/contracts (values):**
  - новые `ingress.externalAuth` (bool, default `false`);
  - `networkPolicy.{enabled, ingressController, alertSenders, metricsScrapers, extraIngress}`;
  - метка pod'а `app.kubernetes.io/component: application`.
  - Breaking для `values-production.yaml`: нужны `webConfig.existingSecret` или `ingress.externalAuth=true`, плюс источники NetworkPolicy. Сообщения `fail` называют оба выхода.
  - Решение — в ADR (следующий номер в `DECISIONS.md`).
- **Data model/migrations:** not applicable.
- **Rollout/rollback:**
  - на upgrade — один rolling restart (метка);
  - kill-switch — `helm upgrade --reuse-values --set networkPolicy.enabled=false`, удаляет объект без рестарта;
  - `helm rollback` возвращает всё.
  - Migration notes — в `CHANGELOG.md` `[Unreleased]`, «Breaking changes».
- **Observability:**
  - признак неверных источников — у отправителя `prometheus_notifications_errors_total` / `_dropped_total`, у AMP падает ingest;
  - smoke «чужой pod → timeout» документировать в README;
  - CNI без NetworkPolicy — fail-open, это задокументировать.
- **Security/ownership/RBAC:**
  - egress не ограничиваем — решение и причина в ADR;
  - auth-секреты только через `existingSecret`;
  - guard'ы срабатывают при рендере.
- **Tests:**
  - `helm/amp/tests/render-ingress-auth.sh`: prod без auth → fail с сообщением; с `existingSecret` → рендер; с `externalAuth` → рендер; дефолт — без Ingress.
  - `render-networkpolicy.sh`: выкл по умолчанию; пустые источники → fail; пиры проходят как есть; правило контроллера есть только при `ingress.enabled`; только порт `http`; нет Egress; метка на pod'е есть, `spec.selector` не изменился; `from` политики redis матчит pod AMP.
  - `release-gate.sh`: placeholder'ы для `webConfig.existingSecret` и пиров в обоих местах. Тесты подключить шагом в гейт (пересекается с `HELM-RENDER-TEST-IN-GATE`).
- **Out of scope, завести записи:**
  - BUGS: `SERVICEMONITOR-DEAD` — безусловный рендер ломает установку без CRD, селектор не матчит Service, нет basicAuth;
  - BUGS: `SERVICE-METRICS-PORT-MISROUTED` — Service `metrics:9090` ведёт на экспортёры postgres/redis;
  - TECH-DEBT: nginx sticky-аннотации в `ingress.yaml` и хардкод `name: monitoring` в политиках postgres/redis.

## 6) References

- Files: `helm/amp/templates/{ingress,deployment,service,servicemonitor,postgresql-networkpolicy,redis-networkpolicy}.yaml`, `helm/amp/values{,-dev,-production}.yaml`, `scripts/release-gate.sh`, `go-app/cmd/server/main.go`, `go-app/internal/config/config.go`, `go-app/internal/infrastructure/cluster/heartbeat.go`
- Docs: `tasks/archive/PROD-AUTH/{research,Spec}.md` (ADR-011), `tasks/archive/PROD-RBAC-SCOPE/Spec.md` (ADR-012, паттерн render-гейта)
- Donors (по линзе reuse-first): Bitnami `common` NetworkPolicy и `validateValues`, grafana/loki chart `networkPolicy.*`, kube-prometheus-stack `serviceMonitor.basicAuth`.
