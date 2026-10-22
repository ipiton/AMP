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

## 3. orphan-удаление (выполнил владелец) + `helm upgrade` веткой @ `72b52b3`

Хук `destructive-command-guard` блокирует `kubectl delete`, поэтому orphan-удаление `amp-redis` выполнил владелец (`statefulset.apps "amp-redis" deleted`). Затем:

```
Release "amp" has been upgraded. Happy Helming!
pod uid=8753cf6a-c1ab-452c-9f1e-b3baee8104e7 start=2026-10-22T10:37:44Z restarts=0
pvc uid=3773e5e8-82a5-4efd-99c3-bf21ccfb91b7 labels=<без изменений>
sts uid=85988492-bdc2-4b0c-8963-33d24d22be4b vct-labels={"app.kubernetes.io/component":"redis-storage","app.kubernetes.io/instance":"amp","app.kubernetes.io/name":"amp"}
key=kept
3  deployed  amp-0.1.0  Upgrade complete
```

StatefulSet новый (другой UID), под **тот же** (UID, `startTime`, 0 рестартов — шаблон пода не изменился, под просто усыновлён), PVC тот же, ключ на месте. Premise «усыновление после orphan-удаления» из Spec подтверждён (measured).

## 4. `helm upgrade` копией ветки с `version`/`appVersion` 0.1.1

```
Release "amp" has been upgraded. Happy Helming!
LAST DEPLOYED: Thu Oct 22 12:42:29 2026
error: timed out waiting for the condition        <- rollout status sts/amp-redis
error: context "k3d-vct-labels" does not exist    <- снимок состояния
```

Upgrade с бампом версии API принял — та `Forbidden`, что в шаге 2, не возникла: главный критерий задачи выполнен. Rollout пода (шаблон пода меняется: метки `version`/`chart`) дождаться не удалось — кластер удалён владельцем во время ожидания, состояние после шага 4 (UID PVC, ключ) **не снято**. Сохранность PVC при rollout пода — штатное поведение StatefulSet (PVC по имени, тот же, что в шаге 3), отдельно не проверено.
