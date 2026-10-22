---
id: HELM-VCT-CHART-LABELS
slug: helm-vct-chart-labels
stream: Helm
type: bug
status: done
created_at: 2026-10-22
updated_at: 2026-10-22
based_on:
  - requirements.md
---

# Specification: метки volumeClaimTemplates без версии чарта

**Version:** 1.0  
**Status:** Approved

## Summary

`volumeClaimTemplates` StatefulSet кеша получает только стабильные метки (selector + `component`), чтобы бамп версии чарта не менял неизменяемое поле. Разовая миграция для существующих установок — orphan-удаление StatefulSet перед upgrade, описанная в CHANGELOG и README чарта.

## Requirements Coverage

| Requirement / Criterion | Covered by |
|---|---|
| VCT без версионных меток | Target Design, `redis-statefulset.yaml` |
| Одинаковый VCT при разных версиях | render-тест `tests/render-vct-labels.sh` (write-tests) |
| Migration note | `CHANGELOG.md`, `helm/amp/CHANGELOG.md`, README § Upgrading |
| k3d upgrade с бампом | `testing` |

## Current State

- **Code:** `helm/amp/templates/redis-statefulset.yaml:324-329` — `include "amp.labels"` + `component: redis-storage` в VCT. `postgresql-statefulset.yaml:244` — VCT без меток.
- **Data:** PVC `redis-data-<fullname>-redis-<n>`; метки PVC выставляются при создании и потом не синхронизируются.
- **Tests:** render-тестов на VCT нет.
- **Docs:** README § Upgrading, корневой и чартовый CHANGELOG `[Unreleased]`.

## Design Premises

| Premise | Confirmed by | Class | If wrong |
|---|---|---|---|
| Смена меток в `spec.volumeClaimTemplates` существующего StatefulSet отвергается API (`Forbidden`) | k3d 1.35, `evidence/vct-probe.md` | measured | фикс не нужен / миграция не нужна |
| Кроме VCT кеша, в чарте нет неизменяемых полей с `amp.labels` (selector'ы — `amp.selectorLabels`, Job/CronJob нет) | grep `selector:` и `kind: Job` по `helm/amp/templates` | call-path-traced | бамп сломает другой объект |
| Метки PVC никто не селектит (NetworkPolicy, скрипты, доки) | grep `redis-storage`/`redis-data` по `helm/`, `docs/`, `scripts/` | call-path-traced | потребитель потеряет PVC — не применимо: метки существующих PVC не меняются |
| После orphan-удаления новый StatefulSet усыновляет под (selector не меняется) и переиспользует PVC по имени | k3d, `evidence/k3d-acceptance.md` шаг 3: тот же UID пода и PVC, ключ на месте | measured | upgrade пересоздаст под с пустым томом → потеря кеша |

## Target Design

В VCT `redis-data` вместо `amp.labels` — `amp.selectorLabels` и `app.kubernetes.io/component: redis-storage`. `managed-by` тоже не добавляется: он не версионный, но формулировка бага («только selector labels + component») держит VCT минимальным и не зависящим ни от чего, кроме имени релиза. Метки самого StatefulSet и шаблона пода не трогаются — они изменяемы. Существующая установка на первом upgrade получит ту же `Forbidden`, поэтому CHANGELOG (Breaking changes / migration notes) и README § Upgrading описывают разовый шаг: `kubectl -n <ns> delete sts <fullname>-redis --cascade=orphan`, затем `helm upgrade`; под и PVC остаются, StatefulSet пересоздаётся Helm'ом и усыновляет их. Автоматический guard через `lookup` отвергнут: миграция разовая, `lookup` не работает в `helm template`/GitOps, а сообщение API вместе с migration note достаточно для диагностики.

## Component Architecture

- `helm/amp/templates/redis-statefulset.yaml` — метки VCT.
- `helm/amp/tests/render-vct-labels.sh` — новый render-тест (write-tests).
- `CHANGELOG.md`, `helm/amp/CHANGELOG.md`, `helm/amp/README.md` — migration note.

## Security Design

- [x] Не применимо: ни RBAC, ни секретов, ни входных данных.

## Invariants

- [x] Selector StatefulSet и метки пода не меняются (иначе под не усыновится).
- [x] Имя VCT `redis-data` и размер/класс хранилища не меняются (имя PVC = имя VCT + под).
- [x] VCT не зависит от `Chart.yaml` `version`/`appVersion`.

## Edge Cases

1. Свежая установка → VCT сразу со стабильными метками, миграция не нужна.
2. `profile: lite` / `valkey.replicas: 0` → StatefulSet не рендерится (lite) или рендерится с 0 реплик: при 0 реплик миграция та же, PVC может не существовать.
3. Upgrade без миграции → `Forbidden` от API, релиз в `failed`; после orphan-удаления повторный upgrade проходит.
4. Orphan-удаление без `--cascade=orphan` → поды удаляются, PVC остаются (retention по умолчанию `Retain`), кеш поднимется с того же тома; простой кеша на время пересоздания.

## Impact Analysis

- **Affected modules:** Helm chart (StatefulSet кеша).
- **Breaking changes:** разовая миграция для существующих `profile: standard` установок.
- **New dependencies:** none.
- **Risks:** assumed premise об усыновлении — проверка на k3d в `testing`.

## Rollout / Rollback

- **Rollout:** orphan-удаление `<fullname>-redis`, затем `helm upgrade`. Риск (assumed premise): если под не усыновится, Valkey перезапустится на том же PVC — кеш переживает рестарт по AOF/RDB.
- **Rollback:** `helm rollback` на старую ревизию снова упрётся в `Forbidden` (VCT меняется обратно) — тот же orphan-шаг перед rollback.
- **Feature flag:** not applicable.

## Observability

- **Logs / Metrics / Alerts:** not applicable.

## Deep Review

- **Mandatory triggers present:** none (сигнал только `R`).
- **Discretionary triggers present:** none — дифф ~10 строк шаблона + доки, нет `C+X`, паттерн не новый.
- **Decision:** not applicable. Единственное непроверенное утверждение (усыновление после orphan-удаления) — поведенческое, закрывается прогоном на k3d в `testing`, не ревью.

## Open Questions

- [x] Усыновление пода и PVC после orphan-удаления — подтверждено на k3d (`evidence/k3d-acceptance.md` шаг 3). Было: (assumed) — проверить на k3d в `testing`; нужно подтверждение владельца на `kubectl delete` (хук `destructive-command-guard`).
