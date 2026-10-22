# Замер: смена меток volumeClaimTemplates у существующего StatefulSet

2026-10-22, k3d-кластер `vct-labels`, сервер `v1.35.5+k3s1`, `kubectl apply` (client-side).

1. `kubectl apply -f vct-probe-sts.yaml` — StatefulSet с меткой VCT `helm.sh/chart: amp-0.1.0` → `created`.
2. Тот же манифест с `amp-0.1.1` (бамп версии чарта):
   `The StatefulSet "t" is invalid: spec: Forbidden: updates to statefulset spec for fields other than 'replicas', 'ordinals', 'template', 'updateStrategy', 'revisionHistoryLimit', 'persistentVolumeClaimRetentionPolicy' and 'minReadySeconds' are forbidden`
3. Манифест без `helm.sh/chart` и `app.kubernetes.io/version` в VCT (целевые метки фикса) — та же ошибка: сама смена меток тоже требует пересоздать StatefulSet.

Orphan-удаление StatefulSet и повторный apply здесь не прогонялись (удаление ресурсов требует подтверждения владельца) — проверка в `testing`.
