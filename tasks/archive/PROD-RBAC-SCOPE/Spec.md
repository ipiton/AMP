# Spec: PROD-RBAC-SCOPE

Дата: 2026-09-28. Ветка `feature/prod-rbac-scope`. Источники: `requirements.md`, `research.md`. Пути — от корня репозитория.

## Проблема

`helm/amp/templates/rbac.yaml` при любых values (включая `profile: lite`) выдаёт ServiceAccount AMP чтение `secrets`/`configmaps` во всём кластере (ClusterRole) и `create/update/patch` на `secrets`/`configmaps` в namespace релиза (Role). Тем же SA с автомонтируемым токеном пользуются Redis StatefulSet и CronJob бэкапа PostgreSQL. Компрометация любого из трёх подов = все секреты кластера.

Коду нужно несравнимо меньше (research §2): только `Secrets(ns).List(labelSelector)` в **одном** namespace discovery и только при `profile: standard` + `publishing.enabled`. Values `targetDiscovery.*` и `serviceAccount.rbac.*` делают вид, что управляют этим, но не читаются (research §3).

## Цели

1. RBAC чарта = ровно то, что вызывает код: одна namespaced `Role` с `list` на `secrets` в discovery-namespace.
2. Role рендерится только когда код создаёт K8s-клиент; в `lite` и при `publishing.enabled: false` — никакого RBAC.
3. Ни при каких values нет `ClusterRole`/`ClusterRoleBinding` и write-глаголов.
4. Поды, которым K8s API не нужен (Redis, backup), не получают токен SA.
5. Мёртвые values убраны; осталась одна реальная ручка отключения RBAC.
6. Регрессия ловится release-gate'ом.

## Не-цели

- **Cluster-scope / multi-namespace discovery.** Код читает один namespace; opt-in без потребителя только расширяет поверхность (research §6B). Появится multi-namespace в коде — права добавятся вместе с ним.
- **RBAC для investigation Kubernetes tool** (pods, pods/log, events, deployments). Чарт tool не проводит → BACKLOG `INVESTIGATION-K8S-TOOL-HELM`.
- **`automountServiceAccountToken` для самого пода AMP** в `lite`. Токен без прав безвреден, а пользователь может включить investigation tool через `configFile` со своим RBAC.
- Отдельные ServiceAccount для Redis/backup. Достаточно не монтировать токен (D5).
- Изменения Go-кода, включая удаление неиспользуемого `K8sClient.GetSecret`.
- NetworkPolicy/Ingress (`PROD-INGRESS-HARDENING`), живой кластер в гейте (`PROD-CI-IMAGES`).

## Ключевые решения

### D1. Глаголы: только `list`

Discovery вызывает только `List` (`discovery_impl.go:360`); `GetSecret` вызывающих не имеет, watch/informers нет, health (`ServerVersion`) RBAC не требует. Даём `list`. Если в коде появится `Get`/`Watch` — права добавляются в том же изменении. Риск: при будущем использовании `GetSecret` без правки чарта будет 403 — ловится первым же прогоном, не молча.

Ограничить `list` label selector'ом RBAC не умеет (research §5) — Role даёт `list` всех secrets namespace. Рекомендацию держать publishing-target secrets в отдельном namespace — в README (D7).

### D2. Условие рендера

```
serviceAccount.create && serviceAccount.rbac.create && profile == "standard" && publishing.enabled
```

Ровно зеркалит условие создания K8s-клиента в `publishing_runtime.go` (`Publishing.Enabled` и `Profile == ProfileStandard`). Привязка к `serviceAccount.create` сохраняется, как сейчас: при чужом SA чарт не знает, кому выдавать права (и `amp.serviceAccountName` дал бы `default`).

### D3. Namespace Role

`publishing.discovery.namespace`, если задан, иначе `.Values.namespace | default .Release.Namespace` — тот же выбор, что `PUBLISHING_DISCOVERY_NAMESPACE` в `deployment.yaml:131-139` (fieldRef пода = namespace Deployment'а). RoleBinding — в том же namespace, subject — SA в namespace релиза. Вынести выбор namespace в helper `amp.discoveryNamespace` в `_helpers.tpl` и использовать его и в `deployment.yaml`? — **Нет**: в deployment ветка fieldRef, а не строка; дублирование одной строки `default` оставляем (Simplicity Gate).

### D4. Values

- `serviceAccount.rbac.create` (дефолт `true`) — единственная ручка; комментарий объясняет, что именно выдаётся и когда.
- Удаляются: `serviceAccount.rbac.crossNamespace`, секция `targetDiscovery:` целиком, комментарий `# (duplicate targetDiscovery section removed)`.
- `values-dev.yaml` / `values-production.yaml` этих ключей не содержат — не трогаем.

### D5. Redis и backup без токена

`automountServiceAccountToken: false` в pod spec `redis-statefulset.yaml` и `postgresql-backup-cronjob.yaml` (jobTemplate). `serviceAccountName` оставляем (не ломаем imagePullSecrets/PSA-привязки, если кто-то вешал их на SA). По шаблонам ни один контейнер/init этих подов в K8s API не ходит.

### D6. Регрессия в release-gate

Новый шаг `helm-rbac` в `scripts/release-gate.sh` (после `helm-production`, та же зависимость от `helm` + `helm dependency build`). Рендерит четыре варианта: дефолт, `values-dev.yaml`, `values-production.yaml` (с placeholder-паролями, как `step_helm_values`), дефолт + `--set profile=lite`. Падает, если:

- в любом рендере есть `kind: ClusterRole` или `kind: ClusterRoleBinding`;
- в любом рендере в `rules` встречается глагол кроме `list` — проверка по строке `verbs:` (все Role чарта в одну строку `verbs: [...]`);
- `lite`-рендер содержит `kind: Role`/`RoleBinding`;
- `standard`-рендеры не содержат ровно одной `kind: Role` (защита от «починили удалением»).

Реализация — `helm template | grep`/`awk` в функции `step_helm_rbac`, без новых зависимостей (yq не требуем).

### D7. Документация и миграция

- `CHANGELOG.md` `[Unreleased]`: `### Changed` / security — что выдаётся теперь; **Breaking/migration**: ClusterRole/ClusterRoleBinding/write-Role удаляются на `helm upgrade`; кто привязывал к SA AMP свои нагрузки — выдать права сам; `targetDiscovery.*`, `serviceAccount.rbac.crossNamespace` удалены (игнорировались и раньше); при `publishing.discovery.namespace` ≠ namespace релиза установщику нужны права создавать Role в том namespace.
- `helm/amp/README.md`: раздел RBAC — что, где, при каких values; рекомендация отдельного namespace для target-secrets; проверка `kubectl auth can-i list secrets --as=system:serviceaccount:<ns>:<sa> -n <discovery-ns>`.
- `go-app/internal/infrastructure/k8s/README.md` §RBAC Requirements: `verbs: ["list"]`, ссылка на чарт.
- `DECISIONS.md`: ADR-012 «RBAC чарта = фактические вызовы кода, без cluster-scope» (коротко: контекст, решение D1–D2, отклонённые варианты B/C из research §6).
- BACKLOG: закрыть `PROD-RBAC-SCOPE`, завести `INVESTIGATION-K8S-TOOL-HELM`.

## Scope (файлы)

| Файл | Изменение |
|---|---|
| `helm/amp/templates/rbac.yaml` | переписан: одна Role (`list secrets`) + RoleBinding под условием D2, namespace D3 |
| `helm/amp/values.yaml` | D4 |
| `helm/amp/templates/redis-statefulset.yaml` | `automountServiceAccountToken: false` |
| `helm/amp/templates/postgresql-backup-cronjob.yaml` | `automountServiceAccountToken: false` |
| `scripts/release-gate.sh` | шаг `helm-rbac` (D6) |
| `helm/amp/README.md`, `go-app/internal/infrastructure/k8s/README.md`, `CHANGELOG.md`, `docs/06-planning/DECISIONS.md`, `BACKLOG.md` | D7 |

## Acceptance Criteria

1. Дефолт, `values-dev.yaml`, `values-production.yaml`: ровно одна `Role` + одна `RoleBinding`; Role в namespace релиза, правило одно — `resources: ["secrets"]`, `verbs: ["list"]`; нет `ClusterRole`/`ClusterRoleBinding`.
2. `--set profile=lite`, `--set publishing.enabled=false`, `--set serviceAccount.rbac.create=false`, `--set serviceAccount.create=false`: нет ни одной Role/RoleBinding/ClusterRole/ClusterRoleBinding.
3. `--set publishing.discovery.namespace=targets`: Role и RoleBinding в `targets`, subject — SA в namespace релиза.
4. Pod spec Redis StatefulSet и backup CronJob содержат `automountServiceAccountToken: false`; Deployment AMP — без изменений.
5. `grep -rn 'targetDiscovery\|crossNamespace' helm/` пусто.
6. `scripts/release-gate.sh`: шаг `helm-rbac` зелёный; при ручной порче (вернуть `ClusterRole` или добавить `get` в verbs) — красный (проверено вручную, результат в `tasks.md`).
7. `helm lint` для дефолта, dev, production — зелёный; `helm template` для production по-прежнему требует пароли (guard не ослаблен).
8. Документация по D7; `git diff --check` чист.

## Риски

| Риск | Смягчение |
|---|---|
| Чья-то нагрузка опиралась на широкие права SA AMP | breaking-note в CHANGELOG (D7) |
| Будущий `GetSecret`/`Watch` без правки чарта → 403 | ошибка громкая (WARN при discovery, пустой кэш); D1 фиксирует правило «права вместе с кодом» |
| `publishing.discovery.namespace` в чужом namespace: helm-установщику не хватает прав | README + CHANGELOG |
| Условие D2 разойдётся с кодом, если поменяют `publishing_runtime.go` | комментарий в `rbac.yaml` со ссылкой на функцию; гейт D6 проверяет оба профиля |
| grep-проверка D6 хрупка к переформатированию `verbs` | Role чарта одна, формат фиксирован в том же изменении; гейт падает (а не молчит) при отсутствии ожидаемой строки |
| Проверка только статическая | ручная `kubectl auth can-i` в README; живой кластер — `PROD-CI-IMAGES` |

## Нарезка

Один срез, ~0.5d: чарт + гейт (AC1–7) и доки (AC8) одним мержем.
