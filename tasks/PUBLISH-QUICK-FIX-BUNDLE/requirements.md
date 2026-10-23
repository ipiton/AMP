---
id: PUBLISH-QUICK-FIX-BUNDLE
slug: publish-quick-fix-bundle
stream: Publishing
type: bug
priority: critical
status: active
created_at: 2026-10-23
updated_at: 2026-10-23
---

# Requirements: быстрые исправления publishing (бандл)

Бандл из `TECH-DEBT.md` § Bundles, участники — записи `BUGS.md` (порядок из бандла): `PROVIDER-RESOLVE-STATELESS-PD`, `PUBLISH-PER-TARGET-STATE`, `PROVIDER-RESOLVE-ROOTLY`, `CIRCUIT-BREAKER-HALF-OPEN`, `QUEUE-GRACEFUL-STOP`, `PUBLISH-SECRETS-IN-LOGS`. Находки CODE-REVIEW-2026-10-21: C-2, H-4, M-7, M-9, M-10, M-11, L-2, L-19.

## Problem Framing

- **Symptom:**
  - PagerDuty-инцидент не закрывается автоматически после рестарта AMP, на другой реплике и через 24h: resolve получает `ErrEventNotTracked`.
  - Rootly-инцидент в тех же условиях остаётся открытым, а AMP считает resolve доставленным (`nil`).
  - Второй Slack-таргет отвечает в тред чужого канала; второй Rootly-таргет обновляет чужой инцидент; после reload с новым SMTP-паролем/`smtp_tls` работает старый клиент; два email-таргета на одном SMTP-хосте шлют с кредами первого.
  - После `Timeout` открытого breaker'а через него проходит весь поток заданий, `State()` показывает `open`.
  - `PublishingQueue.Stop` теряет задания medium/low; `Submit` после `Stop` и повторный `Stop` паникуют; паника паблишера роняет процесс.
  - Токены Teams/Discord/Google Chat/Telegram из URL и тела ответов таргетов попадают в логи; ответы читаются без лимита; ошибка валидации печатает слабый пароль БД.
- **Root Cause:** состояние корреляции хранится в памяти процесса и ключуется только fingerprint'ом; кеш SMTP-клиентов ключуется `host:port`; `CanAttempt` не меняет состояние; `Stop` закрывает каналы без защиты от отправки и воркер выходит на первом закрытом канале; ошибки `*url.Error` и тела ответов попадают в текст ошибки без маскировки. Подробно — в записях `BUGS.md`.
- **Why Now:** critical-позиция конвейера ревью (`REVIEW-PIPELINE-2026-10-21.md` №2), блокер production-ready (CODE-REVIEW § 9).
- **How We Measure:** тесты по каждому участнику (см. Success Criteria) + `go test -race ./internal/infrastructure/publishing/... ./internal/business/publishing/...` зелёный.

## Risk Profile

- **Signals:** `S X R`
  - `S` — секреты таргетов в логах и ошибках, SMTP-креды не того таргета.
  - `X` — `infrastructure/publishing`, `business/publishing` (лог discovery), `application` (wiring хранилища Rootly), `config` (ошибка пароля), `infrastructure/llm` (лимит тела ответа).
  - `R` — меняется рантайм-поведение доставки (resolve, breaker, остановка очереди).
- **Tier:** Full
- **Notes:** `S` ⇒ Full и обязательный deep-review. Публичный API и формат конфига не меняются.

## User Stories

1. Как дежурный, я хочу, чтобы resolve закрывал инцидент PagerDuty/Rootly независимо от рестартов и реплик AMP, чтобы не закрывать инциденты руками.
2. Как оператор с несколькими таргетами одного типа, я хочу, чтобы состояние и креды одного таргета не влияли на другой.
3. Как оператор, я хочу, чтобы недоступный таргет после восстановления получал одну пробу, а не весь накопленный поток, и чтобы остановка AMP не теряла и не роняла задания.
4. Как владелец секретов, я хочу, чтобы токены таргетов не попадали в логи.

## Success Criteria

- [ ] PD: resolve без предварительного trigger в том же процессе шлёт `event_action=resolve` с `dedup_key = fingerprint`; in-memory кеш PD удалён.
- [ ] Slack/Rootly: состояние корреляции ключуется `target.Name|fingerprint` — два таргета одного типа с одним алертом не видят записей друг друга.
- [ ] SMTP-клиент ключуется всеми полями `SMTPConfig` (пароль — хешем): смена пароля/TLS/From или второй таргет с другими кредами получают свой клиент.
- [ ] Rootly: ID инцидента хранится в общем хранилище (Redis, при его отсутствии — in-memory кеш процесса) и переживает рестарт/другую реплику; resolve при промахе возвращает ошибку, а не `nil`.
- [ ] Breaker: после `Timeout` ровно один конкурентный вызов получает пробу, состояние — `half-open`; провал пробы возвращает `open`, успехи до `SuccessThreshold` — `closed`; зависшая проба не блокирует breaker навсегда.
- [ ] Очередь: `Stop` обрабатывает задания всех приоритетов, уже стоящие в каналах; `Submit` после `Stop` — ошибка, не паника; повторный `Stop` безопасен; паника паблишера становится ошибкой задания.
- [ ] Логи и ошибки: URL таргета маскируется (scheme+host), тело ответа в ошибке ≤512 байт, ответы читаются с лимитом; ошибка слабого пароля не содержит значения.

## Non-Goals

- Единый слой ретраев (`PUBLISH-SINGLE-RETRY-LAYER`), жизненный цикл клиентов при reload (`PUBLISH-RELOAD-CLIENT-LIFECYCLE`), консистентность транспортов (`PUBLISH-TRANSPORT-CONSISTENCY`).
- Персистентность Slack-тредов между рестартами (кеш тредов остаётся in-memory, меняется только ключ).
- Редакция ошибок валидации конфига целиком (`CONFIG-VALIDATION-ERROR-REDACTION`).

## Constraints

- **Scope:** `internal/infrastructure/publishing/*`, точечно `business/publishing/discovery_impl.go`, `application/publishing_runtime.go`, `config/config.go`, `infrastructure/llm`.
- **Security:** секреты не логируются; ключи кешей не содержат пароль в открытом виде.
- **Compatibility:** конфиг, API и формат payload не меняются. Поведение меняется к ожидаемому (resolve доходит). Записи старого in-memory кеша Rootly при апгрейде теряются — инциденты, открытые до апгрейда, resolve получат ошибку (фиксируется в CHANGELOG).

## Discovery Notes

- Similar tasks: `tasks/archive/AMP-CACHE-NO-RECONNECT/` (r.cache и его fallback), `tasks/archive/INHIBIT-SOURCE-CACHE-EVICTION/` (Redis-ключи в общем кеше), `tasks/archive/SLACK-WEBHOOK-PLAIN-OK-RESPONSE/`.
- Relevant patterns: `slack_client.go` `maskWebhookURL` + `maskedError` (маскировка с сохранением цепочки ошибок); `discovery_log_redaction_test.go`; компаунд-ключи клиентов в `publisher.go` (`url|key|http_config fingerprint`).
- Open unknowns: способ stateless resolve для Rootly — в `research.md`.
