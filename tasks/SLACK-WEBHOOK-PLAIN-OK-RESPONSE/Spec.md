---
id: SLACK-WEBHOOK-PLAIN-OK-RESPONSE
slug: slack-webhook-plain-ok
stream: operations
type: bug
status: approved
created_at: 2026-10-20
updated_at: 2026-10-20
based_on:
  - requirements.md
---

# Specification: ответ `ok` Slack Incoming Webhook считается ошибкой

**Version:** 1.0
**Status:** Approved

## Summary

Slack-клиент перестаёт требовать JSON на 2xx: Incoming Webhook отвечает `ok` в `text/plain`, и это успех. JSON-ответ Web API разбирается как раньше. Publisher не кладёт в кеш threading-запись, когда ответ не дал `ts`.

## Requirements Coverage

| Requirement / Criterion | Covered by |
|---|---|
| 2xx `ok`/пустое — успех; другой текст — ошибка | Target Design п.1–2; unit-тесты клиента |
| JSON ok=true/false как раньше | Target Design п.1; unit-тесты клиента |
| Не-2xx текст → ошибка с текстом, классификация по статусу | Target Design п.3 (без изменений `parseSlackError`); unit-тесты 400/429/5xx |
| Маскировка URL | Invariants; `slack_client_mask_test.go` |
| Нет threading-записи с пустым `ts` | Target Design п.4; тест publisher'а |
| e2e: одна отправка за несколько `group_interval` | `internal/application/config_only_delivery_e2e_test.go` |
| Моки вебхука отвечают `ok` | `queue_target_publisher_test.go` |

## Current State

- **Code:** `slack_client.go:263` — `if StatusCode == 200 { json.Unmarshal(body) ... if !ok → HTTPAPIError }`; иначе `parseSlackError` (JSON `{ok:false,error}` либо сырое тело) + `PublishingClassifier.IsRetryable` (429/5xx). 201/204 сейчас уходят в ветку ошибок.
- **Consumers `*SlackResponse`:** `slack_publisher_enhanced.go:110-135` (`resp.TS` → `MessageEntry{MessageTS, ThreadTS}` в `MessageIDCache`), `:173` (ответ игнорируется), `Health` (только `err`). Поиск по всему `go-app/` — других нет.
- **Tests:** `slack_client_mask_test.go` (маскировка), `slack_publisher_test.go` (мок-клиент), `queue_target_publisher_test.go:388` (мок отвечает JSON), `config_only_delivery_e2e_test.go` (`recordingEndpoint` отвечает `ok`, проверяет только первый запрос).

## Design Premises

| Premise | Confirmed by | Class | If wrong |
|---|---|---|---|
| Incoming Webhook на успех отвечает 200 и телом `ok` в `text/plain` | воспроизведение 2026-10-20 (мок как Slack), Slack docs «Handling errors», upstream `checkTextResponseError` | code-read | фикс не снимает симптом — проверяется e2e на прод-конфиге после выката |
| Web API `chat.postMessage` отвечает `application/json` `{ok,ts,...}`, ошибки — 200 + `ok:false` | upstream `slackResponseHandler`, текущий код AMP | code-read | ok=false по Web API может пройти как успех — закрыто правилом «тело начинается с `{` → JSON» |
| `SlackResponse.TS` читает только `EnhancedSlackPublisher.postMessage` | `grep -rn 'PostMessage(\|ReplyInThread(\|\.TS\b' go-app/` | call-path-traced | другой потребитель с пустым `ts` — нет таких |
| Подтверждение группы = `PublishingResult.Success` = `err == nil` от publisher'а | `publishing_adapter.go:118-145` | code-read | nflog не запишется и после фикса — ловит e2e-тест |

## Target Design

1. На любом 2xx тело считается JSON, если `Content-Type` начинается с `application/json` или тело (без пробелов) начинается с `{`. JSON разбирается в `SlackResponse`; ошибка разбора или `ok=false` — `HTTPAPIError` со статусом и `error`, как сейчас.
2. Не-JSON 2xx: тело `ok` или пустое (после `TrimSpace`) — успех, возвращается `&SlackResponse{OK: true}` без `ts`. Любой другой текст — `HTTPAPIError{StatusCode, Message: тело}`, без ретраев (у 2xx классификатор и так не ретраит; возвращается сразу, как upstream `Unrecoverable`).
3. Не-2xx — без изменений: `parseSlackError` уже берёт сырое тело в `Message`, классификация по статусу.
4. `EnhancedSlackPublisher.postMessage`: если `resp.TS == ""`, threading-запись не сохраняется (тред не к чему цеплять). Иначе повторная отправка того же алерта ушла бы `replyInThread` с пустым `thread_ts` — коротким «Still firing» в канал вместо полного сообщения. Без записи каждая отправка — полное сообщение, как в upstream.

## Component Architecture

- `go-app/internal/infrastructure/publishing/slack_client.go` — ветка 2xx в `doRequestWithRetry`, helper `parseSlackSuccess` (решение «JSON или текст» одно и меняется целиком).
- `go-app/internal/infrastructure/publishing/slack_publisher_enhanced.go` — условие на `resp.TS` перед `cache.Store`.

## Security Design

- [x] Ownership — не применимо.
- [x] Input validation — тело ответа внешнего API: не JSON и не `ok` → ошибка.
- [x] Sensitive data not logged — webhook URL в тело ошибки не попадает (тело — ответ Slack; URL маскируется в сетевых ошибках как раньше).
- [x] Rate limiting — без изменений.
- [x] Auth/RBAC — не применимо.

## Invariants

- [ ] JSON `{"ok":true,"ts":"..."}` → `TS` возвращается, threading для Web API не меняется.
- [ ] JSON `{"ok":false,"error":"x"}` на 200 → ошибка с `x`.
- [ ] 4xx не ретраятся, 429/5xx ретраятся (`maxRetries=3`).
- [ ] `slack_client_mask_test.go` зелёный.

## Edge Cases

1. 200 + `ok\n` → успех (TrimSpace).
2. 204 без тела → успех.
3. 200 + `text/plain` + `{"ok":false,"error":"x"}` → ошибка `x` (JSON по первому символу).
4. 200 + `application/json` + `ok` → ошибка разбора (заголовок обещал JSON) — как upstream.
5. 200 + `invalid_payload` → неретраимая ошибка с текстом.
6. 400 + `invalid_payload` / 404 + `no_service` → ошибка с текстом, без ретраев.
7. Resolved для алерта без записи в кеше → `postMessage` (как сейчас при cache miss).

## Impact Analysis

- **Affected modules:** `internal/infrastructure/publishing` (Slack).
- **Breaking changes:** none — успехом стало больше ответов; JSON-путь прежний.
- **New dependencies:** none.
- **Risks:** 2xx от промежуточного прокси с пустым телом засчитается доставкой — принято (так просит постановка; статус 2xx — сигнал доставки и для upstream).

## Rollout / Rollback

- **Rollout:** обычный релиз; запись в `CHANGELOG.md` `[Unreleased]` → Fixed.
- **Rollback:** откат коммита; данных и миграций нет.
- **Feature flag:** not applicable.

## Observability

- **Logs:** исчезает ложный `failed to parse response` и `delivery not confirmed` для Slack-вебхуков; `Message posted successfully` логирует `message_ts=""` для вебхука.
- **Metrics:** `RecordMessage(slack, success)` начинает расти для вебхуков вместо `RecordAPIError`.
- **Alerts:** not applicable.

## Deep Review

- **Mandatory triggers present:** none (Risk `R`, Standard).
- **Discretionary triggers present:** none — дифф маленький, паттерн известный (upstream).
- **Decision:** not applicable.

## Open Questions

- [x] none
