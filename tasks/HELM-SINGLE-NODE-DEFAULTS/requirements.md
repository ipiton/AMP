---
id: HELM-SINGLE-NODE-DEFAULTS
slug: helm-single-node-defaults
stream: operations
type: feature
priority: high
status: active
created_at: 2026-10-19
updated_at: 2026-10-19
---

# Requirements: дефолты чарта под одну ноду

## Problem Framing

- **Symptom:** `helm install amp ./helm/amp` с дефолтными values не поднимается на маленьком кластере. На однонодовом k3s (4 vCPU, свободно ~750m CPU, аудит 2026-10-06) поды висят в `Pending`: дефолт рассчитан на HA.
- **Root Cause:** дефолты `values.yaml` скопированы с прод-профиля:
  - `autoscaling.enabled: true`, `minReplicas: 2` (`values.yaml:213-216`) перекрывает `replicaCount: 1` — по умолчанию две реплики AMP (`templates/deployment.yaml:9`);
  - requests ≈1,6 vCPU / 2,1 GiB: AMP 500m/512Mi (`:64-70`), PostgreSQL 500m/1Gi (`:318-323`) + exporter 50m/64Mi, `cache` 250m/256Mi (`:444-459`), `valkey` 250m/256Mi (`:468-486`) + exporter 50m/64Mi;
  - бюджет соединений встроенного PG: `maxConnections: 250` (`:358`) рассчитан на `maxReplicas: 10` × 25 соединений пула; на максимуме HPA пулы AMP занимают все 250, exporter и `kubectl exec psql` не входят (PG-CONFIG-FIX R4/R8). Лимит памяти PG 2Gi измерен при ~13 backend'ах, а не при 250.
- **Why Now:** первая P1 в BACKLOG § Production Readiness. Без неё нельзя внедрить AMP на однонодовый k3s, ни свой, ни у пилотного пользователя. P0 `PROD-RELEASE-V010` отложен владельцем.
- **How We Measure:** `helm template` с дефолтами даёт одну реплику без HPA, сумма requests ≤ ориентира аудита (~260m CPU / ~640Mi) или обоснованное отклонение в Spec; `values-small.yaml` ставится на однонодовый k3d/k3s с ограничением по CPU, все поды `Ready`, `POST /api/v2/alerts` → 200.

## Risk Profile

- **Signals:** `R`
  - `R` — меняются дефолты, которые получит каждый `helm upgrade` без своих values: HPA выключается (Deployment переходит на `replicaCount: 1`), requests и параметры встроенного PG меняются (рестарт PG, новый бюджет соединений). Нужна запись в CHANGELOG с migration note.
- **Tier:** Standard
- **Notes:** `C` не ставлю: ключи values не удаляются и не переименовываются, меняются только значения по умолчанию. Если research покажет, что лишний Redis надо убрать удалением ключа (`cache` vs `valkey`), это меняет контракт values: добавить `C` и пересмотреть tier.

## User Stories

1. Как оператор однонодового k3s, я хочу ставить AMP чартом с дефолтами (или с `values-small.yaml`), чтобы поды стартовали без ручного подбора requests и реплик.
2. Как оператор прод-кластера, я хочу, чтобы HA-настройки оставались в `values-production.yaml` и переход на новые дефолты был описан в CHANGELOG, чтобы `helm upgrade` не уменьшил число реплик незаметно для меня.

## Success Criteria

- [ ] `autoscaling.enabled: false` по умолчанию; `values-production.yaml` явно включает HPA (проверить, что включает уже сейчас).
- [ ] Сумма requests дефолтного рендера (profile `standard`) — под одну ноду. Целевые значения и способ их получения (измерение или ориентир аудита) зафиксированы в Spec.
- [ ] Бюджет соединений встроенного PG согласован: `postgresql.config.maxConnections` ≥ `maxReplicas` × пул + exporter + резерв на админа. Это верно и для дефолта, и для `values-production.yaml`. Память PG соответствует выбранному `maxConnections`.
- [ ] `helm/amp/values-small.yaml` — пример под одну ноду; рендерится и проходит `helm lint`; описан в README чарта.
- [ ] Лишний Redis по умолчанию: решение (выключить, оставить или вынести в TECH-DEBT `HELM-CHART-GAPS`) принято в research/Spec и обосновано.
- [ ] CHANGELOG `[Unreleased]` + `helm/amp/CHANGELOG.md`: изменения дефолтов и migration note для тех, кто полагался на HPA и старые ресурсы.
- [ ] Рендер-тесты чарта (`helm/amp/tests/`) покрывают новые дефолты; `scripts/release-gate.sh` PASS.
- [ ] Проверка установки на однонодовом k3d с ограничением CPU (evidence в workspace).

## Non-Goals

- Не переделывать HA-профиль и `values-production.yaml`, кроме согласования бюджета соединений и явного включения HPA.
- Не чинить остальное из TECH-DEBT `HELM-CHART-GAPS` (`postgresql.existingSecret`, `migration.backupBeforeMigration`, мёртвые флаги `valkey.enabled`/`cache.enabled`). Исключение — если это нужно, чтобы выключить лишний Redis.
- Не менять Go-код: дефолт пула `database.max_connections` остаётся прежним.
- Не трогать профиль `lite`.
- Не делать `HELM-REDIS-NOEVICTION` (отдельная P2).

## Constraints

- **Scope:** `helm/amp/` (values, при необходимости шаблоны и NOTES), тесты чарта, CHANGELOG, README чарта.
- **Security:** не применимо. Auth-guard ADR-015 и NetworkPolicy не трогаются.
- **Compatibility:** `helm upgrade` с дефолтами меняет число реплик и ресурсы. Нужна migration note: как вернуть HA-поведение (`autoscaling.enabled: true` или `-f values-production.yaml`). Изменение параметров PG перезапускает PG.

## Discovery Notes

- Similar tasks: `tasks/archive/HELM-DEFAULTS-VALIDATE/` (дефолты чарта проходят валидацию), `tasks/archive/PG-CONFIG-FIX/` (бюджет соединений, k3d-acceptance — образец evidence), `tasks/archive/HELM-EXTERNAL-DATABASE/`.
- Relevant patterns: рендер-тесты `helm/amp/tests/render-*.sh`; k3d-проверка из `tasks/archive/PG-CONFIG-FIX/evidence/k3d-acceptance.md`; `scripts/release-gate.sh` (helm lint/template).
- Источник: BACKLOG § Production Readiness → P1 `HELM-SINGLE-NODE-DEFAULTS` (аудит 2026-10-06, дополнение PG-CONFIG-FIX 2026-10-18); TECH-DEBT `HELM-CHART-GAPS`.
- Open unknowns:
  - зачем в `standard` два Redis (`cache` и `valkey`) и можно ли один выключить, не трогая шаблоны (сейчас флаги `*.enabled` не гейтят StatefulSet; воркараунд — `valkey.replicas: 0`);
  - реальное потребление AMP/PG/Redis в простое — для обоснования requests (ориентир аудита 260m / 640Mi);
  - дефолтный `maxConnections` при выключенном HPA: считать от `replicaCount` или от `autoscaling.maxReplicas`. Оставить ли NOTES-предупреждение как защиту для тех, кто включит HPA;
  - что `helm upgrade` делает с replicas при переходе HPA on → off (Deployment снова получает `replicas`, HPA удаляется).
