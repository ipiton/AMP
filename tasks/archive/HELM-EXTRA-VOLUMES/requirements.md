---
id: HELM-EXTRA-VOLUMES
slug: helm-extra-volumes
stream: operations
type: feature
priority: high
status: active
created_at: 2026-10-20
updated_at: 2026-10-20
---

# Requirements: extraVolumes / extraVolumeMounts в чарте helm/amp

## Problem Framing

- **Symptom:** `configFile.content` рендерится в ConfigMap `<fullname>-configfile`, поэтому `telegram_configs[].bot_token` и Slack webhook URL лежат в нём открытым текстом (читается любым, у кого есть `get configmaps` в namespace, и попадает в `helm get values`/Argo CD). Смонтировать Secret в контейнер `amp` и сослаться на него через `*_file`-поля нельзя: в чарте нет `extraVolumes`/`extraVolumeMounts`, нужен патч Deployment. Та же дыра мешает подложить ConfigMap с runbooks.
- **Root Cause:** в `templates/deployment.yaml` список volume'ов фиксирован (lite data, config-file, web-config).
- **Why Now:** предпосылка запуска AMP рядом с Alertmanager в sema-prod (параллельные нотификации в Telegram panic-канал и Slack #sema), переоценка 2026-10-20.
- **How We Measure:** render-тест: Secret из `extraVolumes` смонтирован в `amp`, `configFile.content` ссылается на путь внутри него; k3d: AMP стартует с `bot_token_file` и `api_url_file` из смонтированного Secret, `/api/v2/status` показывает пути, а не секреты.

## Risk Profile

- **Signals:** `C S X`
  - `C` — новые ключи values (публичный контракт чарта), аддитивно;
  - `S` — путь доставки секретов receiver'ов;
  - `X` — после Scope Change: чарт + Go-валидатор конфига.
- **Tier:** Full (`S` форсирует Full; deep-review обязателен).
- **Notes:** Go-код изначально не планировался, после Scope Change правится валидатор (`internal/alertmanager/config`, `pkg/configvalidator`); диффа в шаблоне немного, риск — в неверной документации путей/прав, конфликтах mountPath и в том, что пользователь положит секрет не туда.

## User Stories

1. Как оператор sema-prod, я хочу смонтировать Secret с bot_token Telegram и Slack webhook URL в контейнер AMP, чтобы в ConfigMap оставались только пути (`bot_token_file`, `api_url_file`).
2. Как оператор, я хочу подложить свой ConfigMap (например, runbooks) без форка шаблона Deployment.

## Success Criteria

- [ ] `extraVolumes: []` и `extraVolumeMounts: []` в `values.yaml` с комментарием и примером (Secret + mountPath + ссылка из `configFile.content`).
- [ ] Значения рендерятся в Pod spec: `extraVolumes` → `spec.volumes`, `extraVolumeMounts` → `volumeMounts` контейнера `amp` (и только его).
- [ ] При пустых значениях рендер Deployment байт-в-байт не меняется (дефолт, lite, production).
- [ ] Конфликт с volume'ами чарта (имя или mountPath) падает на рендере с понятной ошибкой, а не на apply.
- [ ] Render-тест в `helm/amp/tests/`, гоняется шагом `helm-tests` release-gate.
- [ ] Живая проверка в k3d: AMP стартует с `bot_token_file`/`api_url_file` из Secret; evidence в `evidence/`.
- [ ] Пример Telegram + Slack через `*_file` в `helm/amp/README.md` (англ.) и `docs/CONFIGURATION_GUIDE.md`.
- [ ] _(расширение scope, 2026-10-20)_ Конфиг, где секрет Slack / webhook / PagerDuty задан только через `*_file`, проходит загрузку (`config.LoadConfig`); без значения и без файла — прежние E113/E115/E122. Тесты на загрузку с каждым `*_file` и на ошибку «ни значения, ни файла». k3d — со Slack `api_url_file` из Secret.

## Scope Change (2026-10-20)

Deep-review F1 (blocker): `pkg/configvalidator`, который `config.LoadConfig` вызывает до `routing.Parse` (`validateAlertmanagerSubset`), работает на типах `internal/alertmanager/config` без `*_file`-полей и отвергает `slack_configs[].api_url_file`, `global.slack_api_url_file` (E115), `webhook_configs[].url_file` (E113), `pagerduty_configs[].routing_key_file`/`service_key_file` (E122). Подтверждено в k3d (`evidence/k3d-extra-volumes.md` §1). Баг FU7-B (`c892f1f`, 2026-08-19), не чарта, но без фикса цель задачи (Slack sema из Secret) недостижима. Владелец выбрал вариант A — чинить здесь (через сессию-оркестратор, 2026-10-20). Добавлены сигнал `X` (чарт + Go-валидатор) и правка Go; tier остаётся Full.

## Non-Goals

- Не менять Go-резолв `*_file` в `routing` (относительные пути секретов — от CWD, как в upstream; см. research). Правка Go ограничена валидатором (Scope Change).
- Не монтировать extra volumes в сайдкар config-reloader и не добавлять initContainers.
- Не автоматизировать перезагрузку при ротации Secret (reloader смотрит только config-файл).
- Не чинить остальные пункты `HELM-CHART-GAPS`.

## Constraints

- **Scope:** `helm/amp/values.yaml`, `templates/deployment.yaml`, тест, README, CONFIGURATION_GUIDE, planning; после Scope Change — `go-app/internal/alertmanager/config/config.go`, `go-app/pkg/configvalidator/validators/receiver.go`, `ERROR_CODES.md`, тесты.
- **Security:** Secret создаёт пользователь; чарт секретов не читает и не рендерит. Права файлов: `fsGroup: 65534` + `defaultMode` Secret по умолчанию (0644) — читаемо для `runAsUser: 65534`.
- **Compatibility:** аддитивно; пустые значения = прежний рендер. `--reuse-values` со старого чарта: ключей нет → `default list`.

## Discovery Notes

- Similar tasks: `tasks/archive/HELM-EXTERNAL-DATABASE/` (existingSecret, render-тест, k3d drill), `webConfig.existingSecret` в `deployment.yaml`.
- Relevant patterns: `helm/amp/tests/render-graceful-shutdown.sh`, `render-external-database.sh`; FU7-B `go-app/internal/infrastructure/routing/file_secrets.go`.
- Open unknowns: нет.
- WIP: на момент claim в WIP две задачи (SLACK-WEBHOOK-PLAIN-OK-RESPONSE + эта), параллельно в соседней сессии берётся SLACK-COLOR-TEMPLATE-REJECTED — три задачи одной пачки «Предпосылки запуска в sema-prod» запущены владельцем параллельно сознательно.
