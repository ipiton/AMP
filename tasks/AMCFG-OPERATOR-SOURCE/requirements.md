---
id: AMCFG-OPERATOR-SOURCE
slug: amcfg-operator-source
stream: pilot-replacement
type: feature
priority: high
status: active
created_at: 2026-11-18
updated_at: 2026-11-18
---

# Requirements: AlertmanagerConfig через prometheus-operator (opt-in режим чарта)

## Problem Framing

- **Symptom:** AMP не читает `AlertmanagerConfig` (`monitoring.coreos.com/v1alpha1`); маршрут `moving/moving-cron` перенесён в values руками.
- **Root Cause:** конфиг AMP — один файл `alertmanager.yml` из values; слияние AMC делает prometheus-operator только для своих `Alertmanager`.
- **Why Now:** один из четырёх блокеров замены основного Alertmanager по итогам пилота 2026-11-18.
- **How We Measure:** при `alertmanager.configSource: operator` AMP на k3s с оператором v0.70 получает маршруты AMC и перезагружает их без рестарта пода.

## Risk Profile

- **Signals:** `C S R X`
- **Tier:** Full
- **Notes:** `S` — токены AMC открытым текстом попадают в Secret ns AMP, новый образ в поде. `C` — новые values. `X` — чарт и prometheus-operator.

## User Stories

1. Как владелец кластера, я хочу описывать маршруты через `AlertmanagerConfig`, чтобы AMP видел их без ручного копирования в values.

## Success Criteria

- [ ] Режим `values` (по умолчанию) рендерится как раньше; новых объектов нет.
- [ ] Режим `operator` создаёт CR `Alertmanager` с `replicas: 0`, Secret базового конфига, sidecar и init reloader; AMP читает сгенерированный файл.
- [ ] Рендер падает с понятной ошибкой: нет CRD `Alertmanager`; неизвестный `configSource`; включён publishing discovery; включена авторизация, а `/-/reload` не открыт.
- [ ] RBAC ServiceAccount AMP не меняется (ADR-012).
- [ ] Сквозная проверка на k3s: новый AMC доходит до AMP через reload, без рестарта.
- [ ] ADR-020, README, CHANGELOG, render-тесты; гейты `WORKFLOW.md` пройдены.

## Non-Goals

- Включение режима в пилоте (`AMCFG-PILOT-CUTOVER`, решение владельца).
- Свой контроллер AMC (`AMCFG-NATIVE-CONTROLLER`), AMP как образ CR `Alertmanager`, апгрейд оператора.

## Constraints

- **Scope:** чарт `helm/amp`, без Go-кода.
- **Security:** чтение Secret'ов в ns AMP не шире, чем в `sema-monitoring` (решение владельца 2026-11-14); образы — карантин 7 дней.
- **Compatibility:** opt-in; при выключенном режиме поведение прежнее.

## Discovery Notes

- Similar tasks: `tasks/archive/` (HELM-*), `helm/amp/tests/render-extra-volumes.sh`.
- Relevant patterns: `amp.monitoringCRDs`, `amp.validate`, `extraVolumes` guard.
- Open unknowns: нет, закрыты спайком (`docs/06-planning/AMCFG-SPIKE-2026-11-18.md`).
