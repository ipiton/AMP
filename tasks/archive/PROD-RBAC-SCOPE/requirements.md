# Requirements: PROD-RBAC-SCOPE

## Context

Блокер P0 (Security) из аудита production readiness (2026-09-21, BACKLOG «Production Readiness — блокеры»). По рекомендуемому порядку идёт вторым, после закрытого PROD-AUTH.

`helm/amp/templates/rbac.yaml` при `serviceAccount.create=true` (дефолт чарта) рендерит безусловно — даже при `targetDiscovery.enabled: false` (тоже дефолт):

- `ClusterRole` `<fullname>-secrets-reader` + `ClusterRoleBinding`: `get/list/watch` на `secrets` и `configmaps` **во всех namespace кластера** (правила про secrets к тому же продублированы, `resourceNames: []` ничего не ограничивает);
- namespaced `Role` `<fullname>-namespace-secrets` + `RoleBinding`: `get/list/watch/create/update/patch` на `secrets` и `configmaps` в namespace релиза.

Итог: компрометация пода AMP = чтение всех секретов кластера и запись секретов в своём namespace, при том что коду нужен только `list secrets` в одном namespace (discovery), а в `profile: lite` K8s API не нужен вовсе.

> Уточнено на `/research` (2026-09-28, см. `research.md` §10): дефолтный `profile: standard` **использует** K8s API (discovery), `targetDiscovery.*` — мёртвые values, cluster-scope кодом не поддерживается; тем же SA пользуются Redis и backup-поды.

## Goals

- [x] Ни при каких values чарт не рендерит `ClusterRole`/`ClusterRoleBinding` (cluster-scope opt-in не делаем — кодом не поддерживается, research §6B).
- [x] При `profile: standard` + `publishing.enabled` рендерится одна namespaced read-only `Role` (`list` secrets) в discovery-namespace; при `lite` — никакого RBAC.
- [x] Redis StatefulSet и backup CronJob не получают токен SA AMP.
- [x] Мёртвые `targetDiscovery.*` / `serviceAccount.rbac.*` убраны или заменены реальной ручкой.
- [x] Write-права (`create/update/patch`) убраны, если код их не использует (проверить `go-app/internal/infrastructure/k8s` и прочих клиентов K8s API).
- [x] Права на `configmaps` и `namespaces` — только если код их реально читает.
- [x] Регрессионная проверка в release-gate (`scripts/release-gate.sh`): `helm template` с дефолтами не содержит `ClusterRole`.

## Constraints

- Scope — Helm-чарт + release-gate + документация чарта; Go-код не меняем, если research не покажет, что без этого нельзя (например, discovery ходит по кластеру через cluster-wide list).
- Существующие инсталляции с `targetDiscovery.enabled: true` не должны молча потерять доступ: изменение поведения описать в `CHANGELOG.md` (breaking/migration notes) и в `helm/amp/README.md`.
- Security-change ⇒ `/research` (что именно и в каких namespace читает discovery) → `/spec` перед реализацией.
- Оценка ~0.5d; при росте — нарезать.

## Success Criteria (Definition of Done)

- [ ] `helm template helm/amp` с дефолтами / dev / production: нет `ClusterRole`/`ClusterRoleBinding`, одна Role без write-глаголов.
- [ ] `helm template --set profile=lite`: нет Role/RoleBinding.
- [ ] Проверка в release-gate падает на регрессии.
- [ ] `helm lint` и существующий release-gate зелёные.
- [ ] Документация чарта (values, README) и `CHANGELOG.md` обновлены; BACKLOG-запись закрыта.
