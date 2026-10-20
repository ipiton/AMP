---
id: SLACK-WEBHOOK-PLAIN-OK-RESPONSE
slug: slack-webhook-plain-ok
stream: operations
type: bug
priority: high
status: done
created_at: 2026-10-20
updated_at: 2026-10-20
---

# Requirements: ответ `ok` Slack Incoming Webhook считается ошибкой

## Problem Framing

- **Symptom:** сообщение в Slack доставлено, но AMP логирует `failed to parse response: invalid character 'o'` → `Group publishing delivery not confirmed ... no dedup entry will be recorded`; горящая группа уходит повторно на каждом `group_interval` (5m) вместо `repeat_interval`. Воспроизведено 2026-10-20 на бинаре `main` с конфигом sema-prod и мок-приёмником, отвечающим как Slack.
- **Root Cause:** `HTTPSlackWebhookClient.doRequestWithRetry` (`go-app/internal/infrastructure/publishing/slack_client.go:263`) на HTTP 200 разбирает тело как JSON `SlackResponse{ok,ts,...}` — формат Web API `chat.postMessage`. Incoming Webhook (`hooks.slack.com/services/...`, путь `slack_api_url`/`api_url`) отвечает телом `ok` в `text/plain`. Ошибка разбора возвращается как неретраимая, coordinator отдаёт `Success=false`, nflog не пишется.
- **Why Now:** первый из трёх пунктов «Предпосылки запуска рядом с Alertmanager в sema-prod» (NEXT § Operations): без фикса `errors-slack` (#sema) спамит каждые 5 минут.
- **How We Measure:** e2e-тест группового пути — после первой доставки в Slack-эндпоинт с ответом `ok` за несколько `group_interval` повторной отправки нет; unit-тесты клиента на `ok`/пустое тело/JSON ok=true/false/текстовые 4xx.

## Risk Profile

- **Signals:** `R`
  - `R` — меняется поведение пути доставки в прод (что считается подтверждённой доставкой → запись nflog), метрики доставки Slack.
- **Tier:** Standard
- **Notes:** `C` нет — wire-формат запроса и экспортируемые сигнатуры не меняются (`SlackResponse` остаётся, `TS` может быть пустым — так и было задокументировано `omitempty`). `X` нет — правка в одном пакете `internal/infrastructure/publishing`; e2e-тест в `internal/application` только читает. `S` нет — маскировка webhook URL не трогается, проверяется тестом.

## User Stories

1. Как оператор, перенёсший `alertmanager.yml` с `slack_configs` на Incoming Webhook, я хочу, чтобы успешная доставка в Slack фиксировалась в nflog, чтобы группа повторялась по `repeat_interval`, а не каждые `group_interval`.
2. Как оператор, я хочу видеть в ошибке текст ответа Slack (`invalid_payload`, `no_service`, `channel_not_found`), чтобы понять причину отказа.

## Success Criteria

- [x] 2xx с телом `ok` (text/plain) и с пустым телом — успех; 2xx с другим текстом — неретраимая ошибка с этим текстом (как upstream).
- [x] 2xx с JSON `{"ok":true,...}` — успех, `ts` возвращается как раньше (threading для Web API URL не меняется).
- [x] 2xx с JSON `{"ok":false,"error":"..."}` — ошибка с этим текстом, как сейчас.
- [x] Не-2xx с текстовым телом — ошибка, содержащая текст; retryable/non-retryable по статусу как сейчас (4xx — без ретраев, 429/5xx — с ретраями).
- [x] Ошибки не содержат webhook URL (существующие mask-тесты зелёные).
- [x] При ответе без `ts` publisher не кладёт в кеш threading-запись с пустым `ts`: следующая отправка того же алерта — полноценное сообщение, а не короткий «Still firing» без треда.
- [x] e2e: группа со Slack-таргетом, эндпоинт отвечает `ok` → одна отправка за несколько `group_interval` (nflog записан).
- [x] Моки, изображающие Incoming Webhook, отвечают `ok`.

## Non-Goals

- `SLACK-COLOR-TEMPLATE-REJECTED`, `HELM-EXTRA-VOLUMES`, `SLACK-EXTRA-FIELDS-NOT-UPSTREAM` — отдельные задачи.
- Поддержка Web API `chat.postMessage` с bot-токеном (`http_config.authorization`) сверх того, что уже работает.
- Ограничение размера читаемого тела ответа, переработка ретраев.

## Constraints

- **Scope:** `slack_client.go`, `slack_publisher_enhanced.go`, тесты.
- **Security:** маскировка webhook URL в ошибках сохраняется (`slack_client_mask_test.go`).
- **Compatibility:** только расширение множества ответов, считающихся успехом; JSON-ответы обрабатываются как раньше. Миграций нет.

## Discovery Notes

- Similar tasks: не найдено (`DONE.md`, `archive/DONE-*.md` — по `slack`/`webhook ok` совпадений нет).
- Relevant patterns: `recordingEndpoint` в `internal/application/config_only_delivery_e2e_test.go` уже отвечает `ok` — поэтому `TestConfigOnlyDelivery_SlackWireShape` проходил: он смотрит только на первый запрос и не проверяет подтверждение доставки.
- Потребители `*SlackResponse`: только `EnhancedSlackPublisher.postMessage` (`resp.TS` → `MessageEntry` для threading) и `replyInThread` (ответ игнорируется). `Health` — только ошибка.
- Open unknowns: none.

## Research (mini)

- Source: upstream `prometheus/alertmanager` `notify/slack/slack.go` (`postRequest` → `retrier.Check(statusCode, body)` по статусу; затем `slackResponseHandler`: `Content-Type: application/json` — разбор `{ok,error}`, иначе `checkTextResponseError` — успех только при теле ровно `ok`, любой другой текст — неретраимая ошибка с этим текстом); Slack docs «Handling errors» для Incoming Webhooks (ошибки — текстом при 4xx: `invalid_payload`, `no_service`, `channel_not_found`, `action_prohibited`, …).
- Key finding:
  - upstream не разбирает тело вебхука как JSON; JSON разбирается только для Web API ответа; 2xx с текстом, отличным от `ok`, upstream считает ошибкой (Slack так сообщает об отказах на 200 в старых ответах);
  - успех — любой 2xx; ретраятся только 5xx (у AMP — 429/5xx, это сохраняем);
  - `parseSlackError` AMP уже берёт сырое тело как сообщение, если JSON не разобрался — текстовые 4xx уже дают ошибку с текстом.
- Decision: на 2xx разбирать JSON только если тело — JSON (`Content-Type: application/json` или тело начинается с `{`); иначе успех, если тело (без пробелов) `ok` или пустое, и неретраимая ошибка с текстом тела в остальных случаях. Пустое тело — отклонение от upstream в мягкую сторону (прокси/моки, отвечающие `200` без тела, доставку подтверждают; так просит постановка задачи). Publisher не кеширует threading-запись с пустым `ts`.
