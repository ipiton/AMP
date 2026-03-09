# Requirements: GRAFANA-DASHBOARD-BRANDING-DRIFT

## Context
После закрытия secondary docs/examples cleanup в репозитории остался отдельный Grafana residual: [alert-history-service.json](/Users/vit/Documents/Projects/AMP/grafana/dashboards/alert-history-service.json) все еще использует historical dashboard branding.

Сейчас в файле уже подтверждены как минимум два разных drift-маркера:

- dashboard title: `AMP - Alert History Service`
- dashboard uid: `amp-alert-history`

Это не чистый rename-by-search. Visible title и identity-shaped fields тянут разные риски:

- title можно менять как operator-facing wording cleanup;
- `uid` может затронуть import/provisioning expectations и требует отдельного осознанного решения.

Следующий slice должен честно отделить:

- возможный narrow branding cleanup внутри самого dashboard JSON;
- от более широкого dashboard identity/provisioning work, который скрыто трогать нельзя.

## Goals
- [x] Зафиксировать стартовый baseline для `GRAFANA-DASHBOARD-BRANDING-DRIFT` как отдельного follow-up bug.
- [x] Сохранить scope узким: текущий task workspace и `grafana/dashboards/alert-history-service.json`, без скрытого перехода в Grafana provisioning/runtime changes.
- [x] Подготовить честную базу для следующего `/research`, который подтвердит, какой именно slice mergeable: visible-title-only cleanup, title+safe metadata cleanup, или отдельный planning split по identity fields.

## Constraints
- Не менять PromQL queries, panel layout, thresholds или datasource wiring на этапе `/start-task`.
- Не менять Grafana provisioning/import behavior, если это прямо не будет подтверждено отдельным research/spec.
- Опора на текущий source of truth: `docs/06-planning/BUGS.md`, `docs/06-planning/DECISIONS.md`, top-level `README.md` и фактический content dashboard JSON.
- Поскольку здесь есть минимум два разумных направления (`title` vs `uid`/identity fields), следующий шаг должен пройти через `/research`, а не через прямой `/spec`.

## Success Criteria (Definition of Done)
- [x] Создан task workspace и branch для `GRAFANA-DASHBOARD-BRANDING-DRIFT`.
- [x] `NEXT.md` отражает новую активную задачу в WIP.
- [x] Зафиксирован стартовый requirements baseline для дальнейшего `/research`.
- [x] В `grafana/dashboards/alert-history-service.json` больше нет visible historical title `AMP - Alert History Service`; top-level title теперь `AMP - Operations Dashboard`.
- [x] `uid = amp-alert-history`, filename и dashboard content sections не менялись этим slice.
- [x] Если identity-shaped residual остается, он не маскируется как “тоже закрытый”, а фиксируется отдельно в planning artifacts.
