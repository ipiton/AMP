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

- **Signals:** `C S`
  - `C` — новые ключи values (публичный контракт чарта), аддитивно;
  - `S` — путь доставки секретов receiver'ов.
- **Tier:** Full (`S` форсирует Full; deep-review обязателен).
- **Notes:** Go-код не меняется; диффа в шаблоне немного, риск — в неверной документации путей/прав, конфликтах mountPath и в том, что пользователь положит секрет не туда.

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

## Non-Goals

- Не менять Go-резолв `*_file` (относительные пути секретов — от CWD, как в upstream; см. research).
- Не монтировать extra volumes в сайдкар config-reloader и не добавлять initContainers.
- Не автоматизировать перезагрузку при ротации Secret (reloader смотрит только config-файл).
- Не чинить остальные пункты `HELM-CHART-GAPS`.

## Constraints

- **Scope:** `helm/amp/values.yaml`, `templates/deployment.yaml`, тест, README, CONFIGURATION_GUIDE, planning.
- **Security:** Secret создаёт пользователь; чарт секретов не читает и не рендерит. Права файлов: `fsGroup: 65534` + `defaultMode` Secret по умолчанию (0644) — читаемо для `runAsUser: 65534`.
- **Compatibility:** аддитивно; пустые значения = прежний рендер. `--reuse-values` со старого чарта: ключей нет → `default list`.

## Discovery Notes

- Similar tasks: `tasks/archive/HELM-EXTERNAL-DATABASE/` (existingSecret, render-тест, k3d drill), `webConfig.existingSecret` в `deployment.yaml`.
- Relevant patterns: `helm/amp/tests/render-graceful-shutdown.sh`, `render-external-database.sh`; FU7-B `go-app/internal/infrastructure/routing/file_secrets.go`.
- Open unknowns: нет.
- WIP: на момент claim в WIP две задачи (SLACK-WEBHOOK-PLAIN-OK-RESPONSE + эта), параллельно в соседней сессии берётся SLACK-COLOR-TEMPLATE-REJECTED — три задачи одной пачки «Предпосылки запуска в sema-prod» запущены владельцем параллельно сознательно.
