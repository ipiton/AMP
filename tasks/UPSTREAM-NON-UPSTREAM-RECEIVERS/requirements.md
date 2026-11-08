---
id: UPSTREAM-NON-UPSTREAM-RECEIVERS
slug: upstream-non-upstream-receivers
stream: Architecture / ADR-019
type: feature
priority: high
status: active
created_at: 2026-11-08
updated_at: 2026-11-08
---

# Requirements: интеграции на upstream-ядре — Rootly и K8s-Secret таргеты (срез 3 ADR-019)

## Problem Framing

- **Symptom:** `cmd/amp-upstream` доставляет уведомления только через интеграции из `alertmanager.yml`. Таргеты, которые AMP сегодня находит в Kubernetes Secret'ах (`publishing-target=true`, Helm `publishingTargets` и `rootly.*`), upstream-ядро не видит: после переключения на него (срез 5) такие установки молча перестанут получать уведомления. Rootly в upstream нет, а свой Rootly-паблишер AMP живёт в удаляемом ядре и теряет ID открытых инцидентов при перестановке `rootly_configs` (`ROOTLY-INCIDENT-KEY-STABLE`).
- **Root Cause:** у upstream одна точка входа для интеграций — `config.Receiver`, собираемый из файла; источника «интеграции из кластера» в нём нет.
- **Why Now:** срез 3 эпика ADR-019, параллельно с 2b и 4 (WIP 3, владелец 2026-11-08). Решение владельца 2026-11-08: K8s-Secret таргеты — адаптер в upstream `config.Receiver`, а не удаление.
- **How We Measure:** тест на `upstreamcore` с фейковым K8s: Secret-таргет доставляет уведомление через upstream `notify/*` в receiver из своего `amp.receiver`, переживает `/-/reload`, изменение Secret применяется без рестарта; Rootly-таргет уходит в формате Alertmanager webhook.

## Risk Profile

- **Signals:** `C S X R`
  - `C` — новая точка расширения `upstreamcore.Options`; формат доставки K8s-таргетов меняется на upstream (payload Alertmanager webhook, шаблоны upstream).
  - `S` — адаптер переносит учётные данные из Secret'ов (URL с токеном, `Authorization`, `routing_key`) в конфиг upstream; их нельзя показать в `/api/v2/status` и логах.
  - `X` — `upstreamcore`, новый пакет адаптера, `infrastructure/k8s`, `cmd/amp-upstream`.
  - `R` — путь доставки уведомлений.
- **Tier:** Full
- **Notes:** в `BACKLOG.md` срез оценён как Standard; по матрице `docs/solo-kanban/workflow.md` § Tiers сигнал `S` и три сигнала поднимают до Full — добавлен обязательный `deep-review` (эскалация, не понижение).

## User Stories

1. Как оператор AMP с таргетами в K8s Secret'ах, я хочу, чтобы они работали на upstream-ядре без переписывания в `alertmanager.yml`.
2. Как оператор, я хочу менять Secret и видеть изменение в доставке без рестарта и без потери таргетов на `/-/reload`.
3. Как пользователь Rootly, я хочу документированный способ подключить Rootly к upstream-ядру, без своего кода AMP и без потери связи алерт → инцидент.

## Success Criteria

- [ ] Rootly: проверено по документации Rootly, принимает ли он webhook Alertmanager; решение (свой `notify.Notifier` или `webhook_configs`) записано, `ROOTLY-INCIDENT-KEY-STABLE` закрыт или переоформлен.
- [ ] K8s-Secret таргеты типов `webhook`, `slack`, `pagerduty`, `rootly` превращаются в интеграции upstream (`webhook_configs`, `slack_configs`, `pagerduty_configs`) того receiver'а, к которому таргет относится по правилам старого ядра (`amp.receiver`, без него — все receiver'ы).
- [ ] Таргеты из Secret'ов переживают `/-/reload` и `SIGHUP`; изменение набора Secret'ов применяется периодическим опросом через reload ядра.
- [ ] Невалидный Secret-таргет пропускается с WARN и метрикой и не ломает reload файлового конфига.
- [ ] Секреты Secret'ов не попадают в `/api/v2/status` и логи.
- [ ] Без AMP-конфига, в профиле `lite` или с `publishing.enabled: false` `amp-upstream` ведёт себя как сейчас (K8s не трогается).
- [ ] Записи `K8S-CLIENT-HEALTH-TIMEOUT`, `REFRESH-WORKER-WARMUP-STOP-FLAKY`, `FLAKE-REFRESH-IN-PROGRESS` разобраны: что закрыто, что остаётся и до какого среза.
- [ ] Гейты `WORKFLOW.md` § Гейты AMP зелёные, включая `scripts/release-gate.sh`.

## Non-Goals

- Удаление своих интеграций и своего ядра — срез 5 (`UPSTREAM-CORE-REMOVAL`).
- Helm-проводка `amp-upstream` (RBAC на Secret'ы уже есть у `cmd/server`) — срез 5.
- Типы `telegram`/`email` в Secret'ах: старое ядро их для Secret'ов не принимает (`discovery_validate.isValidTargetType`), новых возможностей не добавляем.
- Watch Secret'ов через informer — опрос, как в старом ядре.
- Миграция ID инцидентов Rootly из кеша старого ядра.

## Constraints

- **Scope:** `internal/upstreamcore` (одна точка расширения, правка порта помечается в `doc.go`), новый пакет адаптера, `internal/infrastructure/k8s` (баги клиента), `cmd/amp-upstream`. Правки общего wiring минимальны: параллельно идут срезы 2b и 4.
- **Security:** значения из Secret'ов — только в `commoncfg.Secret`/`SecretURL` upstream (маскируются при маршалинге); `/api/v2/status` показывает исходный файл; в логах — имя таргета и Secret'а, без URL и заголовков.
- **Compatibility:** формат Secret'а не меняется. Меняется доставка: payload upstream вместо своих форматтеров AMP (`format: webhook` → Alertmanager webhook), Rootly — только через Alertmanager alert source; это breaking для среза 5/7, записывается в CHANGELOG `[Unreleased]` как заметка к `amp-upstream`.

## Discovery Notes

- Similar tasks: `tasks/archive/UPSTREAM-AI-CLASSIFY/` (точки расширения `upstreamcore.Options`), `tasks/archive/UPSTREAM-CORE-POC-WIRING/`.
- Relevant patterns: `business/publishing.NewTargetDiscoveryManager` (list + parse + validate + graceful degradation), `infrastructure/publishing/coordinator.go` `targetMatchesReceiver` (правило принадлежности).
- Open unknowns: нет (см. `research.md`).
