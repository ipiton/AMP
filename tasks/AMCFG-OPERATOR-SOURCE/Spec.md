---
id: AMCFG-OPERATOR-SOURCE
slug: amcfg-operator-source
stream: pilot-replacement
type: feature
status: approved
created_at: 2026-11-18
updated_at: 2026-11-18
based_on:
  - requirements.md
  - research.md
---

# Specification: AlertmanagerConfig через prometheus-operator

**Version:** 1.0
**Status:** Approved (решения владельца 2026-11-14, вердикт спайка go)

## Summary

Чарт получает режим `alertmanager.configSource: operator`: prometheus-operator собирает `alertmanager.yml` из базового конфига и выбранных AMC, AMP монтирует результат и перезагружает его через sidecar.

## Requirements Coverage

| Критерий | Чем закрыт |
|---|---|
| Режим `values` не меняется | условия в шаблонах, `render-operator-source.sh` |
| Объекты режима `operator` | `operator-alertmanager.yaml`, `statefulset.yaml` |
| Ошибки рендера | `_validate.tpl` |
| RBAC без изменений | `rbac.yaml` не трогается, гейт `helm-rbac` |
| Сквозная проверка | k3s, `AMCFG-SPIKE-2026-11-18.md` |

## Current State

- **Code:** `helm/amp/templates/{configmap,statefulset,_validate}.yaml|tpl`; `go-app/cmd/amp-upstream`: флаг `--web.reload-listen-address` (только loopback, только `POST /-/reload`).
- **Data:** не применимо.
- **Tests:** `helm/amp/tests/render-*.sh`, запускаются гейтом `helm-tests`.
- **Docs:** `helm/amp/README.md`, ADR-012, ADR-019.

## Design Premises

| Предпосылка | Проверка |
|---|---|
| Оператор создаёт `-generated` при `replicas: 0` | спайк, k3s + v0.70 |
| Базовый конфиг пилота переживает модель оператора | спайк, структурное сравнение |
| AMP читает результат парсером upstream 0.34.1 | спайк, `config.LoadFile` |
| Reloader перезагружает AMP через loopback-listener | сквозной прогон |

## Target Design

Values: `alertmanager.configSource` (`values` | `operator`), `alertmanager.operator.{prepare, version, alertmanagerConfigSelector, alertmanagerConfigNamespaceSelector, alertmanagerConfigMatcherStrategy, reloader}`.

Режим `operator`:

- Secret `<fullname>-operator-base` (`alertmanager.yaml` из `alertmanager.config`) и CR `Alertmanager` `<fullname>` с `replicas: 0`;
- ConfigMap `<fullname>` не рендерится, `--config.file=/etc/alertmanager/config_out/alertmanager.env.yaml`;
- init-контейнер `init-config-reloader` и sidecar `config-reloader`; смена конфига не перезапускает под;
- `prepare: true` создаёт CR и Secret при `configSource: values`: двухшаговое переключение.

## API Contracts

Новые ключи values (выше), значения по умолчанию сохраняют текущее поведение.

## Data Model / Migrations

Нет.

## Component Architecture

`AMC → prometheus-operator → Secret -generated → reloader (gunzip, envsubst) → emptyDir → AMP, POST 127.0.0.1:9095/-/reload`.

## Security Design

- SA AMP новых прав не получает; Secret монтируется kubelet'ом.
- `-generated` содержит токены AMC открытым текстом (принято владельцем). Поэтому рендер запрещает publishing discovery в этом режиме: `list secrets` прочитал бы Secret, а токен SA уровня пода попал бы и в reloader.
- Распакованный файл — в `emptyDir` `medium: Memory`. `-generated` монтируется только в reloader-контейнеры.
- По умолчанию AMC берутся только из ns релиза; все ns — явным `{}`.
- Reloader не получает учётных данных: `$(VAR)` в AMC раскрывается из его окружения. Он вызывает `--web.reload-listen-address=127.0.0.1:9095`: только loopback IP, только `POST /-/reload`, перезагрузка лишь при изменении файла; порт 9093 остаётся за авторизацией.
- Образ reloader закреплён по digest.

## Invariants

- `replicas: 0` у CR всегда: второго Alertmanager нет.
- Режим `values` не создаёт объектов `monitoring.coreos.com` сверх существующих.

## Edge Cases

- Неподдерживаемое поле в базовом конфиге: `Reconciled=False`, прошлый `-generated` действует; при первой установке под ждёт Secret.
- AMC с ошибкой: оператор пропускает только его.
- TLS-материала больше одного шарда: монтируется только `-tls-assets-0` (ограничение, README).
- `$(UNSET)` в AMC: работающий под сохраняет конфиг, новый падает в `init-config-reloader` до исправления AMC (проверено), как у основного Alertmanager.

## Impact Analysis

Только чарт. Пилот не меняется до `AMCFG-PILOT-CUTOVER`.

## Rollout / Rollback

Включение: `prepare: true`, проверка `Reconciled=True`, затем `configSource: operator`. Откат — обратно на `values`. Смена режима перезапускает поды.

## Observability

`alertmanager_config_last_reload_successful` у AMP, `status.conditions` CR, логи `config-reloader`. Метрики reloader не собираются (follow-up `AMCFG-RELOADER-METRICS`).

## Deep Review

Обязателен (`S`): фокус на утечке секретов, поведении режима `values`, отказах.

## Open Questions

Нет.
