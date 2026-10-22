---
id: HELM-VCT-CHART-LABELS
slug: helm-vct-chart-labels
stream: Helm
type: bug
priority: medium
status: done
created_at: 2026-10-22
updated_at: 2026-10-22
---

# Requirements: метки volumeClaimTemplates без версии чарта

## Problem Framing

- **Symptom:** первый `helm upgrade` на чарт с другой `version`/`appVersion` падает на StatefulSet кеша: `spec: Forbidden: updates to statefulset spec for fields other than ...` (замер на k3d — `evidence/vct-probe.md`).
- **Root Cause:** `helm/amp/templates/redis-statefulset.yaml` кладёт в `volumeClaimTemplates[].metadata.labels` полный `amp.labels`, включая `helm.sh/chart: amp-<version>` и `app.kubernetes.io/version`. `spec.volumeClaimTemplates` у StatefulSet неизменяем. У `postgresql-statefulset.yaml` меток в VCT нет — не затронут.
- **Why Now:** `PROD-RELEASE-V010` и любой следующий бамп версии чарта упрутся в это на каждой существующей установке со `profile: standard`.
- **How We Measure:** render-тест — в VCT нет `helm.sh/chart` и `app.kubernetes.io/version`, рендер двух разных версий чарта даёт идентичный `volumeClaimTemplates`; на k3d upgrade с бампом версии проходит после разовой миграции.

## Risk Profile

- **Signals:** `R`
- **Tier:** Standard
- **Notes:** `R` — меняется поведение `helm upgrade` существующих установок, нужна разовая операторская миграция (orphan-удаление StatefulSet кеша). `M` не ставлю: `--cascade=orphan` оставляет поды и PVC, PVC не пересоздаются (имя `redis-data-<fullname>-redis-<n>` не меняется), данные фикс не трогает. `C` нет: метки PVC никто не селектит (поиск по `helm/`, `docs/`, `scripts/` — потребителей нет).

## User Stories

1. Как оператор AMP, я хочу обновлять чарт на новую версию обычным `helm upgrade`, чтобы релизы не требовали ручного пересоздания StatefulSet.

## Success Criteria

- [x] В `volumeClaimTemplates` StatefulSet кеша нет меток, зависящих от версии чарта/приложения: только selector labels + `app.kubernetes.io/component: redis-storage`.
- [x] Рендер с разными `version`/`appVersion` в `Chart.yaml` даёт одинаковый `volumeClaimTemplates` (render-тест в `helm/amp/tests/`).
- [x] Migration note в `CHANGELOG.md` и `helm/amp/CHANGELOG.md`: разовое orphan-удаление `<fullname>-redis` перед upgrade, что при этом происходит с подом и PVC.
- [x] k3d: install чарта `main` → orphan-удаление → upgrade веткой → upgrade с бампом версии — оба upgrade проходят, PVC тот же (UID), данные Valkey на месте. *(Итог: до upgrade веткой включительно — полностью; upgrade с бампом принят API, состояние после rollout не снято — кластер удалён.)*

## Non-Goals

- Бамп версии чарта и сам релиз (`PROD-RELEASE-V010`).
- Метки на других объектах (Deployment, Service и т. д.) — они изменяемы.
- Автоматизация миграции (hook / `lookup`-guard).

## Constraints

- **Scope:** `helm/amp/templates/redis-statefulset.yaml`, render-тест, CHANGELOG'и, README чарта (раздел апгрейда, если есть).
- **Security:** не применимо.
- **Compatibility:** ломает `helm upgrade` существующих standard-установок один раз (без миграции — та же `Forbidden`, что и при бампе); после миграции метки стабильны. Свежие установки не затронуты.

## Discovery Notes

- Similar tasks: `tasks/archive/HELM-SINGLE-NODE-DEFAULTS/review-findings.md` R14 (источник бага).
- Relevant patterns: render-тесты `helm/amp/tests/render-*.sh`.
- Open unknowns: поведение orphan-удаления + upgrade на кластере — в `testing` (k3d, нужно подтверждение владельца на удаление ресурсов: хук `destructive-command-guard` блокирует `kubectl delete`).

## Research (mini)

- Source: `evidence/vct-probe.md` (k3d, k8s 1.35), `helm/amp/templates/redis-statefulset.yaml:324-329`, `postgresql-statefulset.yaml:244-255`.
- Key finding: бамп версии и смена меток VCT — одна и та же `Forbidden` (measured); затронут только StatefulSet кеша, других неизменяемых полей с `amp.labels` (Job, selector) в чарте нет.
- Decision: VCT-метки = `amp.selectorLabels` + `component: redis-storage`; миграция — документированное разовое orphan-удаление.
