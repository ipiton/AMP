# k3d-приёмка HELM-VCT-CHART-LABELS (3.1)

2026-10-22, кластер `k3d-vct-labels` (k3s v1.35.5), namespace `vct`, релиз `amp`, Helm 4 (server-side apply). Values: `postgresql.enabled=false` + заглушка `externalDatabase.*`, `monitoring.prometheusEnabled=false` — проверяется только StatefulSet кеша, под AMP не нужен. Скрипт: `accept.sh` (scratchpad, вне репозитория).

## 1. install чарта `main` @ `9d2845b`, ключ `vct-probe=kept` + `SAVE`

```
pod uid=8753cf6a-c1ab-452c-9f1e-b3baee8104e7 start=2026-10-22T10:37:44Z restarts=0
pvc uid=3773e5e8-82a5-4efd-99c3-bf21ccfb91b7 labels={"app.kubernetes.io/component":"redis","app.kubernetes.io/instance":"amp","app.kubernetes.io/managed-by":"Helm","app.kubernetes.io/name":"amp","app.kubernetes.io/version":"0.1.0","helm.sh/chart":"amp-0.1.0"}
sts uid=af0a6029-59bd-440f-b8db-dd3559515546 vct-labels={"app.kubernetes.io/component":"redis-storage",...,"app.kubernetes.io/version":"0.1.0","helm.sh/chart":"amp-0.1.0"}
key=kept
```

У PVC `component: redis`, а не `redis-storage` из VCT: контроллер StatefulSet накладывает на PVC метки селектора.

## 2. `helm upgrade` веткой без миграции — ожидаемый отказ

```
Error: UPGRADE FAILED: server-side apply failed for object vct/amp-redis apps/v1, Kind=StatefulSet: StatefulSet.apps "amp-redis" is invalid: spec: Forbidden: updates to statefulset spec for fields other than 'replicas', 'ordinals', 'template', 'updateStrategy', 'revisionHistoryLimit', 'persistentVolumeClaimRetentionPolicy' and 'minReadySeconds' are forbidden
```

Ревизия 2 — `failed`. Под, PVC и ключ не тронуты (UID те же, `key=kept`). Текст ошибки совпадает с migration note (`StatefulSet.apps "<fullname>-redis" is invalid: spec: Forbidden`); у Helm 4 перед ним префикс `server-side apply failed for object ...`.

## 3. orphan-удаление + upgrade веткой + upgrade с бампом версии

Не выполнено: хук `destructive-command-guard` блокирует `kubectl delete` (в т. ч. в разрешённом кластере), передано владельцу.
