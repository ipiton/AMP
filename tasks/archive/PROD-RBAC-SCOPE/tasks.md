# Implementation Checklist: PROD-RBAC-SCOPE

Ветка `feature/prod-rbac-scope`. Источник: `Spec.md` (решения D1–D7, критерии AC1–AC8). Пути — от корня репозитория.

Один срез (~0.5d), мержится целиком. Порядок: сначала гейт (он должен покраснеть на текущем чарте), затем чарт (гейт зеленеет), затем доки. Это даёт проверку AC6 «гейт ловит регрессию» бесплатно — на реальном старом `rbac.yaml`.

## Допущения и блокеры

- **`helm/amp/charts/` в `.gitignore`** (BACKLOG `PROD-HELM-CLEAN-CHECKOUT`): в worktree сабчарта `valkey` нет, `helm template` падает. Release-gate сам делает `helm dependency build` (нужна сеть). Если сети нет — положить `valkey-2.1.3.tgz` из основного checkout в `helm/amp/charts/` локально (в git не попадает, в `.gitignore`). Не блокер задачи, но зафиксировать в отчёте `/testing`, каким путём рендерили.
- `values-production.yaml` требует пароли — во всех ручных рендерах production добавлять `--set postgresql.password=x --set cache.auth.password=x`, как `step_helm_values`.
- Условие D2 зеркалит `go-app/internal/application/publishing_runtime.go` (`Publishing.Enabled`, `Profile == ProfileStandard`). Перед правкой ещё раз сверить, что в коде за время задачи ничего не поменялось.

## Research & Spec
- [x] Research — `research.md` (discovery = один `List` в одном namespace; `targetDiscovery.*` мёртвые; SA делят Redis и backup)
- [x] Spec — `Spec.md` (D1–D7), 2026-09-28

## Implementation

- [x] **I1. Гейт (D6)** — `scripts/release-gate.sh`:
  - функция `step_helm_rbac`: рендер четырёх вариантов (дефолт; `-f values-dev.yaml`; `-f values-production.yaml` + placeholder-пароли; дефолт + `--set profile=lite`) в переменные/временные файлы;
  - для всех: нет `^kind: ClusterRole$` / `^kind: ClusterRoleBinding$`; каждая строка `verbs:` равна `verbs: ["list"]`;
  - для трёх standard: ровно одна `^kind: Role$` и одна `^kind: RoleBinding$`;
  - для lite: ни одной `Role`/`RoleBinding`;
  - каждое нарушение — `log` с именем варианта и причиной, итог `return 1`;
  - `run_step "helm-rbac" "..." step_helm_rbac` после `helm-production`; обновить шапку-комментарий скрипта со списком шагов;
  - проверить `shellcheck scripts/release-gate.sh` (если установлен).
  - _Отклонение:_ первая версия ловила ClusterRole только в `dev` — `printf | grep -q` под `pipefail`: `grep -q` выходит на первом совпадении, `printf` ловит SIGPIPE, конвейер «падает», `if` ложен. Переведено на here-string (`grep -q ... <<<"$rendered"`), в коде комментарий. Других `| grep -q` в `scripts/*.sh` нет.
- [x] **I1a. Гейт краснеет на текущем чарте** — прогнать шаг на старом `rbac.yaml`, сохранить вывод (ожидание: ClusterRole во всех четырёх, write-глаголы, Role в lite) → в раздел «Результаты» ниже.
- [x] **I2. `helm/amp/templates/rbac.yaml`** — переписать целиком:
  - условие D2: `serviceAccount.create && serviceAccount.rbac.create && eq profile "standard" && publishing.enabled`;
  - namespace D3: `publishing.discovery.namespace | default (.Values.namespace | default .Release.Namespace)`;
  - `Role <fullname>-secrets-lister`: одно правило `apiGroups: [""]`, `resources: ["secrets"]`, `verbs: ["list"]` (в одну строку — на это опирается гейт);
  - `RoleBinding` в том же namespace, subject — SA в namespace релиза;
  - комментарий в шапке: зачем, ссылка на `publishing_runtime.go` (`initializePublishingRuntime`) и `discovery_impl.go`, правило «права вместе с кодом».
- [x] **I3. `helm/amp/values.yaml` (D4)**:
  - `serviceAccount.rbac.create: true` с комментарием (что выдаётся, когда, как отключить);
  - удалить `serviceAccount.rbac.crossNamespace`, секцию `targetDiscovery:` и строку `# (duplicate targetDiscovery section removed)`;
  - `grep -rn 'targetDiscovery\|crossNamespace' helm/` → пусто (AC5).
- [x] **I4. Токены (D5)** — `automountServiceAccountToken: false` в pod spec:
  - `helm/amp/templates/redis-statefulset.yaml` (рядом с `serviceAccountName`);
  - `helm/amp/templates/postgresql-backup-cronjob.yaml` (jobTemplate pod spec, рядом с `serviceAccountName`).
- [x] **I5. Гейт зеленеет** — `step_helm_rbac` проходит на новом чарте.

## Testing (проверки чарта; Go-кода задача не меняет)

- [x] **T1 (AC1)** — дефолт / dev / production: одна Role + одна RoleBinding в namespace релиза, правило `secrets` / `["list"]`, нет Cluster*.
- [x] **T2 (AC2)** — по отдельности `--set profile=lite`, `publishing.enabled=false`, `serviceAccount.rbac.create=false`, `serviceAccount.create=false`: ноль RBAC-объектов.
- [x] **T3 (AC3)** — `--set publishing.discovery.namespace=targets`: Role и RoleBinding в `targets`, subject namespace = namespace релиза (`-n monitoring` при рендере, чтобы отличался).
- [x] **T4 (AC4)** — `automountServiceAccountToken: false` у Redis StatefulSet и backup CronJob; у Deployment AMP pod spec не изменился (diff рендера до/после по Deployment пуст).
- [x] **T5 (AC6)** — ручная порча: вернуть `kind: ClusterRole`, затем добавить `get` в verbs, затем убрать Role целиком — гейт красный в каждом случае; откатить.
- [x] **T6 (AC7)** — `helm lint` дефолт / dev / production зелёный; production без паролей по-прежнему падает на guard.
- [x] **T7** — полный `scripts/release-gate.sh` (или как минимум helm-шаги + `helm-rbac`, если Go-часть/Docker недоступны — зафиксировать, что пропущено и почему).
- [x] **T8** — diff полного рендера production до/после: отличаются только RBAC-объекты и две строки `automountServiceAccountToken` (никакого побочного дрейфа).
- [x] `git diff --check`.

## Documentation (D7)

- [x] `CHANGELOG.md` `[Unreleased]`: запись PROD-RBAC-SCOPE — что выдаётся теперь, когда; **Breaking / migration**: удаление ClusterRole/ClusterRoleBinding/write-Role на `helm upgrade`, удалённые values, права установщика при discovery в чужом namespace.
- [x] `helm/amp/README.md`: раздел RBAC (что/где/когда, `serviceAccount.rbac.create`, отдельный namespace для target-secrets, `kubectl auth can-i list secrets --as=system:serviceaccount:<ns>:<sa> -n <discovery-ns>`).
- [x] `go-app/internal/infrastructure/k8s/README.md` §RBAC Requirements: `verbs: ["list"]`, ссылка на чарт.
- [x] `docs/06-planning/DECISIONS.md`: ADR-012 «RBAC чарта = фактические вызовы кода, без cluster-scope».
- [x] `docs/06-planning/BACKLOG.md`: `PROD-RBAC-SCOPE` → закрыт (ссылка на архив/ADR-012); новый `INVESTIGATION-K8S-TOOL-HELM` (pods, pods/log, events, deployments; в каких namespace разрешено агенту).

Заметки `/write-doc` (2026-09-28):
- CHANGELOG: запись в `### Changed` + пункт в `### Breaking changes / migration notes` (удаляемые объекты, чужие нагрузки на SA, права установщика при discovery в чужом namespace, удалённые values).
- В k8s README пример переименован в `amp-secrets-lister` (как в чарте), `verbs: ["list"]`, `resourceNames`-заглушка заменена объяснением, почему label selector в RBAC не работает.
- Прочие доки (`docs/CONFIGURATION_GUIDE.md`, `MIGRATION_QUICK_START.md`, `helm/amp/DEPLOYMENT.md`, корневой README, examples) о правах SA не говорят — не трогали.
- Замечено, вне scope: `helm/amp/README.md` называет `lite` профилем по умолчанию («Lite Profile (Default)»), а в `values.yaml` дефолт — `standard`. Раздел RBAC написан по фактическому дефолту; сам дрейф не правили.

## Finalization

- [x] Все чекбоксы выше, результаты T1–T8 записаны ниже.
- [x] `/end-task`: `DONE.md`, `NEXT.md` (WIP освобождён), архив `tasks/archive/PROD-RBAC-SCOPE/`.
- [ ] Коммиты conventional (`feat(helm): ...`, `test(release-gate): ...`, `docs(...)`), без AI attribution.

## Результаты

### `/implement` (2026-09-28)

Рендер: `helm dependency build helm/amp` в worktree прошёл по сети (valkey 2.1.3), `charts/` в git не попадает. Шаг гоняли изолированно: `step_helm_rbac` вырезается из `release-gate.sh` и запускается отдельно (раннер в scratchpad), полный гейт — на `/testing` (T7).

- **I1a (старый чарт, красный, rc=1):** во всех четырёх вариантах — ClusterRole/ClusterRoleBinding и глаголы `get/list/watch` + `create/update/patch`; в `lite` — `expected 0 kind: Role, got 1` и то же для RoleBinding.
- **I5 (новый чарт):** rc=0.
- **T1:** дефолт `-n monitoring` — `Role amp-secrets-lister` (`secrets`, `["list"]`) + `RoleBinding` в `monitoring`, subject `amp`/`monitoring`; dev и production — то же (гейт).
- **T2:** `profile=lite`, `publishing.enabled=false`, `serviceAccount.rbac.create=false`, `serviceAccount.create=false` — 0 RBAC-объектов в каждом.
- **T3:** `publishing.discovery.namespace=targets`, `-n monitoring` — Role и RoleBinding в `targets`, subject namespace `monitoring`.
- **T4:** `automountServiceAccountToken: false` в pod spec Redis StatefulSet и backup CronJob; образы этих подов (busybox, valkey, redis-exporter, postgres:16) к API не ходят. Deployment AMP не изменился (T8).
- **T5:** на копии чарта: `Role`→`ClusterRole` ⇒ красный; `verbs: ["get", "list"]` ⇒ красный; многострочные `verbs:` ⇒ красный; пустой `rbac.yaml` ⇒ красный (`expected 1 kind: Role, got 0`); откат ⇒ rc=0.
- **T6:** `helm lint` дефолт / dev / production (с паролями) — `0 chart(s) failed`; production без паролей по-прежнему падает на guard.
- **T8:** diff полного рендера до/после (дефолт и production, `-n monitoring`): удалены ClusterRole, ClusterRoleBinding, `amp-namespace-secrets` Role/RoleBinding; добавлены `amp-secrets-lister` Role/RoleBinding и две строки `automountServiceAccountToken: false`. Прочие отличия — только случайно генерируемый пароль в Secret (`randAlphaNum`, меняется на каждом рендере).
- AC5: `grep -rn 'targetDiscovery\|crossNamespace' helm/` — пусто.
- `shellcheck scripts/release-gate.sh` — чисто.

### `/testing` (2026-09-28)

Полный `scripts/release-gate.sh` на рабочем дереве ветки (HEAD = `main` `e3c9b27` + незакоммиченные изменения задачи), `/bin/bash` 3.2 (macOS), Docker доступен, `helm/amp/charts/` собран `helm dependency build`:

| Шаг | Статус | Время |
|---|---|---|
| build | PASS | 51s |
| lint (golangci-lint) | PASS | 51s |
| test (`go test ./... -count=1`) | PASS | 145s |
| futureparity | PASS | 16s |
| race | PASS | 279s |
| helm-dev | PASS | 0s |
| helm-production | PASS | 0s |
| **helm-rbac** (новый) | PASS | 0s |
| amtool-compat | PASS | 142s |

`RESULT: PASS`, exit 0; `--- FAIL` в логе — 0 (флейк `PUBLISHING-WARMUP-TEST-FLAKY` в этот прогон не проявился). Гейт рабочее дерево не изменил (`git status` тот же). Красных проверок нет; красная сторона гейта подтверждена на `/implement` (I1a, T5).

- Условие рендера D2 повторно сверено с `go-app/internal/application/publishing_runtime.go` (`initializePublishing`) — не менялось; `main` за время задачи не двигался.
- Ограничение, не блокер: проверка статическая (`helm template`), живого кластера нет — `kubectl auth can-i` не прогонялся.
- Наблюдение: из-за открытого `CONFIG-MISSING-FILE-DROPS-ENV` (BUGS) дефолтный Helm-деплой может стартовать на фолбэк-конфиге с `Publishing.Enabled=false` — тогда K8s-клиент не создаётся и Role не используется. На корректность чарта не влияет (после починки бага Role нужна ровно такая), но живой проверкой «discovery работает с новой Role» это пока не подтвердить.

## Итог (2026-09-28)

**Статус: DONE.** Все цели `requirements.md` (в редакции после research) и AC1–AC8 `Spec.md` выполнены; полный release-gate зелёный.

Остаточные ограничения (зафиксированы, не скрыты):
- RBAC не фильтрует `list` по label selector — Role читает все secrets discovery-namespace; смягчение только операционное (отдельный namespace, README → RBAC).
- Проверка статическая; `kubectl auth can-i` и работа discovery с новой Role на живом кластере не проверены (кластера в гейте нет — `PROD-CI-IMAGES`; плюс `CONFIG-MISSING-FILE-DROPS-ENV` может выключать discovery в дефолтном деплое).
- Investigation Kubernetes tool чартом не обслуживается — `INVESTIGATION-K8S-TOOL-HELM` (BACKLOG).
- Вне scope замечено: `helm/amp/README.md` «Lite Profile (Default)» против `profile: standard` в `values.yaml`.

