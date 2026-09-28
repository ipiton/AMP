# Research: PROD-RBAC-SCOPE

Дата: 2026-09-28. Триггер research по WORKFLOW — security / RBAC.

Вопрос: какие права на Kubernetes API реально нужны AMP, в каких namespace и при каких values — и как привести `helm/amp/templates/rbac.yaml` к этому минимуму.

## 1. Что сейчас рендерит чарт

`helm/amp/templates/rbac.yaml` целиком под `{{- if .Values.serviceAccount.create -}}` (дефолт `true`), других условий нет. Проверено `helm template` (копия чарта + `valkey-2.1.3.tgz` в scratchpad):

| values | ClusterRole | ClusterRoleBinding | Role | RoleBinding |
|---|---|---|---|---|
| дефолт (`profile: standard`) | 1 | 1 | 1 | 1 |
| `values-production.yaml` | 1 | 1 | 1 | 1 |
| `values-dev.yaml` | 1 | 1 | 1 | 1 |
| `--set profile=lite` | 1 | 1 | 1 | 1 |

Содержимое:

- `ClusterRole <fullname>-secrets-reader`: `get/list/watch` на `secrets` (правило продублировано, `resourceNames: []` ничего не ограничивает) и `configmaps` во всех namespace; `namespaces` — при `targetDiscovery.crossNamespace`.
- `Role <fullname>-namespace-secrets` в namespace релиза: `get/list/watch/create/update/patch` на `secrets` и `configmaps`.

## 2. Что реально делает код

В Go-коде два клиента K8s API (`grep` по `InClusterConfig` / `CoreV1()`):

### 2.1. Publishing target discovery — `internal/infrastructure/k8s/client.go`

- Создаётся в `internal/application/publishing_runtime.go` (`initializePublishingRuntime`) **только** при `publishing.enabled: true` **и** `profile == standard`. В `lite` (и любом не-standard) работает `NewConfigOnlyTargetDiscoveryManager` — K8s-клиент не создаётся вообще.
- Вызовы:
  - `Secrets(namespace).List(labelSelector)` — единственный рабочий вызов (`discovery_impl.go:360`);
  - `Secrets(namespace).Get` — метод есть в интерфейсе, вызывающих нет;
  - `Discovery().ServerVersion()` — health; RBAC не нужен (`system:public-info-viewer`/`system:discovery`).
- Watch/informers не используются (`grep Informer|.Watch(` — только Redis).
- `configmaps` и `namespaces` не читаются нигде.
- Namespace **один**: `resolvePublishingNamespace` → `publishing.discovery.namespace`, иначе `POD_NAMESPACE`/`K8S_NAMESPACE`/`NAMESPACE`, иначе файл SA namespace, иначе `default`. Чарт всегда выставляет `PUBLISHING_DISCOVERY_NAMESPACE` (`deployment.yaml:131-139`): значение `publishing.discovery.namespace` либо `fieldRef metadata.namespace` пода.
- Cross-namespace / multi-namespace discovery кодом **не поддерживается** — ClusterRole ему не нужна ни в какой конфигурации.
- Write-операций над secrets/configmaps в коде нет.

Если прав нет, `ListSecrets` падает с 403 → `publishing_runtime.go` логирует WARN «Initial publishing target discovery failed, starting with empty cache» (приложение стартует, цели из `receivers:` продолжают работать).

### 2.2. Investigation Kubernetes tool — `internal/infrastructure/investigation/tools/kubernetes.go`

- Регистрируется только при `investigation.tools.kubernetes.enabled` (`service_registry.go:2190`).
- Нужны `pods` list/get, `pods/log` get, `events` list, `deployments` (apps) list — в namespace, который выбрал LLM-агент (параметр `namespace`, произвольный).
- Чарт этот tool **не проводит**: в `values.yaml`/`configmap.yaml` нет `investigation.tools.*`; включить можно только через `configFile`. Текущий RBAC чарта этих прав тоже не даёт (pods/events/deployments в правилах нет) ⇒ сегодня tool в чарте всё равно нерабочий. Вне scope, см. §6.

## 3. Мёртвые/вводящие в заблуждение values

- `targetDiscovery.{enabled,crossNamespace,namespaces,labels,refreshInterval}` — ни один шаблон, кроме `rbac.yaml` (`crossNamespace`), и ни одна env не читает; Go-код о них не знает. Комментарий «disabled - using static publishers» ложный: discovery включён в дефолтном `standard`.
- `serviceAccount.rbac.{create,crossNamespace}` — не читаются нигде.
- Настоящие ручки discovery — `publishing.enabled`, `publishing.discovery.namespace`, `publishing.discovery.labelSelector`, плюс `profile`.

⇒ Исходная формулировка BACKLOG («только при `targetDiscovery.enabled`») опирается на мёртвый ключ. Гейтить надо по реальному условию из кода.

## 4. Смежная находка: SA чарта разделяют чужие поды

`amp.serviceAccountName` используют (`helm template` values-production):

- `Deployment amp` — легитимно;
- `StatefulSet` Redis (`redis-statefulset.yaml:55`) — K8s API не нужен;
- `CronJob` бэкапа PostgreSQL (`postgresql-backup-cronjob.yaml:31`) — K8s API не нужен.

`serviceaccount.yaml` ставит `automountServiceAccountToken: true`. Итог: сейчас компрометация Redis-пода или пода бэкапа = чтение всех secrets кластера. После сужения — чтение secrets discovery-namespace, что всё ещё лишнее.

## 5. Ограничение RBAC

RBAC не умеет ограничивать `list` label selector'ом, а `resourceNames` не работает для `list`. Значит, `list secrets` в discovery-namespace — это все secrets этого namespace (включая пароли PG/Redis, LLM-ключ из того же релиза). Уменьшить это можно только операционно: вынести publishing-target secrets в отдельный namespace (`publishing.discovery.namespace`) — тогда Role живёт там, а не в namespace с секретами AMP. Это стоит задокументировать, но не навязывать дефолтом.

## 6. Варианты

**A. Минимум по коду (рекомендуется).**
- Удалить `ClusterRole`/`ClusterRoleBinding` и write-`Role` целиком.
- Одна `Role` + `RoleBinding`: `secrets` — `list` (и `get` — обсудить в spec: вызовов нет, но интерфейс его обещает; склоняюсь к `list` only), в namespace `publishing.discovery.namespace | default <namespace релиза>`.
- Рендерить только при `serviceAccount.create && rbac.create && profile == "standard" && publishing.enabled`.
- Мёртвые `targetDiscovery.*` и `serviceAccount.rbac.*` убрать; единая ручка `rbac.create` (или оживить `serviceAccount.rbac.create`) для тех, кто управляет RBAC сам.
- Redis StatefulSet и backup CronJob: `automountServiceAccountToken: false` на уровне pod spec (минимальный дифф, не заводя отдельных SA).

**B. A + явный cluster-scope opt-in.** Требование из BACKLOG. Но кодом multi-namespace discovery не поддерживается ⇒ opt-in не даёт функциональности, только расширяет поверхность. Отклоняю (YAGNI); если появится multi-namespace discovery — права добавятся вместе с кодом.

**C. Не рендерить RBAC вовсе, документировать ручную выдачу.** Ломает дефолтный `standard`-инсталл (discovery получит 403). Отклоняю.

## 7. Рекомендация

Вариант A. Регрессия в release-gate: рендер дефолта, `values-dev`, `values-production` и `profile=lite`; гейт падает, если есть `kind: ClusterRole|ClusterRoleBinding`, если в `Role` встречаются write-глаголы, и если `lite` рендерит хоть какую-то Role.

## 8. Риски и совместимость

- **Upgrade**: `helm upgrade` удалит ClusterRole/ClusterRoleBinding и write-Role. Функционально discovery не теряет ничего (он и так читал один namespace). Потеряют доступ только внешние потребители этих прав (кто-то привязал к SA AMP свои поды) — в CHANGELOG как breaking/migration note.
- **`publishing.discovery.namespace` ≠ namespace релиза**: Role создаётся в чужом namespace — у того, кто ставит чарт, должны быть права создавать Role там; раньше это прикрывала ClusterRole. Указать в README.
- **Удаление мёртвых values**: `helm upgrade` с пользовательскими values, где заданы `targetDiscovery.*`, не упадёт (схемы values нет), ключи просто игнорируются — как и сейчас. Упомянуть в CHANGELOG.
- **`automountServiceAccountToken: false`** на Redis/backup: проверить, что ни один init/sidecar этих подов не ходит в K8s API (по шаблонам — нет).
- Проверка только статическая (`helm template`); живого кластера в гейте нет. Для ручной проверки — `kubectl auth can-i --as=system:serviceaccount:<ns>:<sa>`.

## 9. Follow-ups (в BACKLOG, не в этой задаче)

- `INVESTIGATION-K8S-TOOL-HELM` — провести `investigation.tools.kubernetes` через values + отдельная opt-in Role (pods, pods/log, events, deployments), продумать, в каких namespace агенту разрешено смотреть.
- `k8s.README` (`go-app/internal/infrastructure/k8s/README.md` §RBAC Requirements) — пример Role с `get/list` в `default`: синхронизировать в рамках `/write-doc` этой задачи.

## 10. Влияние на scope

Scope **изменился**:

1. Исходная посылка «в дефолтной конфигурации AMP к K8s API не обращается» неверна — дефолтный `profile: standard` делает `list secrets` в namespace релиза. Дефолтный рендер не может быть пустым: остаётся одна namespaced read-only Role. Goal/критерий «с дефолтами нет RBAC на secrets» заменяется на «с дефолтами — только namespaced `list` secrets в discovery-namespace; с `profile=lite` — ничего».
2. Гейт по `targetDiscovery.enabled` отменяется (мёртвый ключ); гейт — по `profile`/`publishing.enabled`.
3. Cluster-scope opt-in не делаем (нет потребителя в коде).
4. Добавляется `automountServiceAccountToken: false` для Redis и backup-подов (тот же blast radius).

Оценка остаётся ~0.5d. Следующий шаг — `/spec`: зафиксировать гейт-условие, набор глаголов (`list` vs `list+get`), судьбу `serviceAccount.rbac.*`/`targetDiscovery.*`, формат проверки в release-gate и migration notes.
