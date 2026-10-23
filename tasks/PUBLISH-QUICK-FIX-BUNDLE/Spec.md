---
id: PUBLISH-QUICK-FIX-BUNDLE
slug: publish-quick-fix-bundle
stream: Publishing
type: bug
status: draft
created_at: 2026-10-23
updated_at: 2026-10-23
based_on:
  - requirements.md
  - research.md
---

# Specification: быстрые исправления publishing (бандл)

**Version:** 1.0  
**Status:** Draft

## Summary

Убираем зависимость resolve от памяти процесса (PD — детерминированный ключ, Rootly — ID в общем кеше), изолируем состояние таргетов, чиним half-open breaker и остановку очереди, маскируем секреты в логах и ошибках.

## Requirements Coverage

| Requirement / Criterion | Covered by |
|---|---|
| PD resolve без trigger в процессе | § Target Design 1 |
| Slack/Rootly ключ `target.Name\|fingerprint` | § Target Design 2 |
| SMTP-клиент по всем полям конфига | § Target Design 2 |
| Rootly ID в общем хранилище, промах ⇒ ошибка | § Target Design 3 |
| Half-open с одной пробой | § Target Design 4 |
| Graceful stop, Submit после Stop, recover | § Target Design 5 |
| Маскировка URL, лимит тела, пароль | § Target Design 6 |

## Current State

- **Code:** `go-app/internal/infrastructure/publishing/` — `publisher.go` (фабрика, кеши, `HTTPPublisher`), `pagerduty_publisher_enhanced.go`, `pagerduty_cache.go`, `rootly_publisher_enhanced.go`, `rootly_cache.go`, `slack_publisher_enhanced.go`, `circuit_breaker.go`, `queue.go`, `webhook_auth.go` (`maskURL`), клиенты `*_client.go`; `business/publishing/discovery_impl.go`; `application/publishing_runtime.go`; `config/config.go`; `infrastructure/llm/client.go`, `investigate_with_tools.go`.
- **Data:** in-memory кеши; Redis/`MemoryCache` через `r.cache`.
- **Tests:** `pagerduty_*_test.go`, `rootly_*_test.go`, `slack_*_test.go`, `circuit_breaker_test.go`, `queue_*_test.go`, `discovery_log_redaction_test.go`.
- **Docs:** `CHANGELOG.md`, `docs/` по PagerDuty/Rootly (проверить на фразы про кеш при finalize).

## Design Premises

| Premise | Confirmed by | Class | If wrong |
|---|---|---|---|
| PD trigger уже шлёт `dedup_key = fingerprint`, так что resolve с тем же ключом закрывает тот же инцидент | `pagerduty_publisher_enhanced.go:93`; upstream `alertmanager@v0.32.0/notify/pagerduty/pagerduty.go:239` шлёт `key.Hash()` и в trigger, и в resolve без хранения | code-read | resolve не закроет инцидент — тот же исход, что сейчас после рестарта |
| `EventKeyCache` и `ErrEventNotTracked` используются только PD-паблишером и фабрикой | `grep -rn` по `go-app` (вне тестов) | call-path-traced | удаление сломает сборку — видно сразу |
| `r.cache` инициализирован до `initializePublishing` и не nil | `service_registry.go:512` (infrastructure) → `:1344` (fallback на `MemoryCache`) → `:2430` (publishing) | code-read | фабрика остаётся на in-memory кеше Rootly (как сейчас) — проверка на nil |
| `CircuitBreaker` из `publishing` используется только `PublishingQueue.processJob` | `grep -rn "CanAttempt()\|RecordSuccess()\|RecordFailure()"` по `go-app` | call-path-traced | другой потребитель получит новую семантику half-open |
| Путь abandoned-job (причина ≠ `Unconfirmed`) не пишет результат в breaker | `queue.go:1079-1107` | code-read | без тайм-аута пробы breaker завис бы в half-open |
| `PublishingQueue.Stop` вызывается после остановки источников заданий, но поздний `Submit` возможен (coordinator/grouping живут своим жизненным циклом) | `service_registry.go` порядок shutdown — не трассирован полностью | assumed | без защиты поздний `Submit` паникует; с защитой получает ошибку — защита нужна в обоих случаях |
| Тело ответа таргета, нужное коду, укладывается в 64 KiB (publishing) и 1 MiB (LLM) | ответы Slack/PD/Rootly/Telegram/webhook — короткие JSON/`ok`; LLM-ответ с инструментами — десятки KiB | assumed | обрезанный JSON ⇒ ошибка декодирования ответа; лимит LLM взят с запасом x16 |

`assumed` вынесены в Open Questions / Rollout.

## Target Design

1. **PD.** `resolveEvent` шлёт `DedupKey: alert.Fingerprint`, без чтения кеша. Поле `cache` и параметр `NewEnhancedPagerDutyPublisher` удаляются, `pagerduty_cache.go`, его тест и `ErrEventNotTracked` — тоже; фабрика не создаёт и не останавливает `pagerDutyCache`.
2. **Изоляция по таргету.** Ключ кеша Slack и Rootly — `target.Name + "|" + fingerprint` (хелпер `correlationKey(target, fingerprint)` в одном месте). Ключ SMTP-клиента — `host|port|user|sha256(password)|tls|from`.
3. **Rootly.** Новая реализация `IncidentIDCache` поверх `cache.Cache` (`rootly_incident_store.go`): ключ `publishing:rootly:incident:<correlationKey>`, значение — ID, TTL 7d, `Set` на create и после успешного update (продление). Ошибка хранилища на `Get` возвращается ошибкой (`Get` → `(id, found, err)`), доставка падает и повторяется на следующем flush; на `Set`/`Delete` — WARN. Фабрика получает хранилище через `SetRootlyIncidentStore(cache.Cache)` из `publishing_runtime.go`; без вызова остаётся in-memory кеш. `resolveIncident` при промахе пишет WARN и `RecordAPIError(rootly, "incidents", "not_tracked")` и возвращает `nil`: исход терминальный, ошибка держала бы алерт в группе и считалась бы breaker'ом на каждом flush (deep-review F1).
4. **Breaker.** `CanAttempt` под write-lock: `Closed` — true; `Open` — если `Timeout` истёк, переход в `HalfOpen`, занять слот пробы, true; `HalfOpen` — true, только если слот свободен или проба висит дольше `Timeout` (тогда слот перезанимается). `RecordSuccess` в `HalfOpen` освобождает слот, считает успех, на `SuccessThreshold` — `Closed`. `RecordFailure` в `HalfOpen` — `Open`, слот свободен. Ветка `RecordSuccess` в состоянии `Open` (переход по успеху без пробы) удаляется: в `Open` попасть в attempt нельзя.
5. **Очередь.** Поля `closeMu sync.RWMutex`, `closed bool`, `stopOnce sync.Once`. `submitJob`: под `RLock` проверить `closed` ⇒ ошибка «shutting down», иначе существующий `select` (send не блокирует). `Stop`: под `stopOnce`+`Lock` выставить `closed` и закрыть каналы; ожидание воркеров — на каждом вызове (повторный `Stop` безопасен). Воркер читает `job, ok := <-ch`; закрытый канал обнуляется локально, выход — когда закрыты все три (или `ctx.Done`). `processJob`: `defer recover()` — паника ⇒ ошибка задания (`job.State=Failed`, `totalFailed`, `cb.RecordFailure`, лог со стеком), outcome — эта ошибка.
6. **Секреты.** `core.RedactURL(raw)`: разбирает URL, возвращает `scheme://host` + `/***`, если были путь, query или userinfo; неразбираемая строка ⇒ `***`. `maskURL` делегирует ему. `HTTPPublisher.publish/publishBatch`: `*url.Error` — заменить `URL` на редактированный (цепочка сохраняется); тело ответа читается через `readLimited(resp.Body, 64<<10)` и в ошибку идёт ≤512 байт. Остальные клиенты publishing — `readLimited`; LLM — `io.LimitReader(…, 1<<20)`. `createEnhancedWebhookPublisher` и `discovery_impl.go` логируют `RedactURL`. `config.go`: ошибка без значения пароля.

## API Contracts

Не применимо: HTTP API, формат конфига и payload таргетов не меняются.

## Data Model / Migrations

### Key: `publishing:rootly:incident:<target>|<fingerprint>`

| Column | Type | Constraints | Notes |
|---|---|---|---|
| value | string | ID инцидента Rootly | TTL 7d, продлевается на каждом успешном firing |

- **Migration:** no
- **Rollback / forward-fix:** откат — ключи истекают сами; in-memory записи старой версии при апгрейде теряются.

## Component Architecture

- `internal/core/redact.go` — `RedactURL`.
- `publishing/publisher.go` — фабрика: без PD-кеша, ключ SMTP, `SetRootlyIncidentStore`, `readLimited`, маскировка ошибок `HTTPPublisher`.
- `publishing/pagerduty_publisher_enhanced.go`, удаление `pagerduty_cache.go`, `ErrEventNotTracked`.
- `publishing/rootly_publisher_enhanced.go`, `rootly_incident_store.go` (новый), `rootly_cache.go` — сигнатура `Get`.
- `publishing/slack_publisher_enhanced.go` — ключ кеша.
- `publishing/circuit_breaker.go`, `publishing/queue.go`.
- `publishing/{slack,telegram,pagerduty,rootly,webhook}_client.go` — `readLimited`.
- `application/publishing_runtime.go` — wiring хранилища.
- `business/publishing/discovery_impl.go`, `config/config.go`, `llm/client.go`, `llm/investigate_with_tools.go`.

## Security Design

- [x] Ownership validation — не применимо.
- [x] Input validation — не применимо (входы — ответы таргетов: ограничены по размеру).
- [x] Sensitive data not logged — URL таргетов, тела ответов, пароль БД; пароль SMTP — только хеш в ключе.
- [x] Rate limiting considered — не меняется.
- [x] Auth/RBAC path defined — не применимо.

## Invariants

- [ ] Таргет без второго экземпляра того же типа ведёт себя как раньше (кроме PD/Rootly resolve).
- [ ] Каждое задание, принятое `submitJob`, получает outcome через `completion` — и при `Stop`, и при панике паблишера.
- [ ] `Closed` breaker пропускает всё; `Open` до `Timeout` не пропускает ничего.
- [ ] Ошибки, возвращаемые клиентами, сохраняют цепочку (`errors.Is/As`).

## Edge Cases

1. PD resolve для алерта, trigger которого ушёл с другой реплики ⇒ `resolve` с тем же ключом.
2. Rootly: Redis недоступен на firing/resolve ⇒ ошибка доставки, повтор на следующем flush; новый инцидент не создаётся (иначе ID открытого затёрся бы — deep-review F5).
3. Rootly: resolve при промахе ⇒ WARN + `not_tracked`, `nil`; breaker и DLQ не затрагиваются.
4. Две конкурентные `CanAttempt` после `Timeout` ⇒ true ровно у одной.
5. Проба abandoned без записи результата ⇒ через `Timeout` слот перезанимается.
6. `Stop` с заданиями во всех трёх каналах ⇒ все обработаны до выхода воркеров (в пределах тайм-аута `Stop`).
7. `Stop` дважды; `Submit` после `Stop` ⇒ ошибка без паники.
8. URL без пути (`https://hooks.example.com`) ⇒ без изменений; с userinfo ⇒ userinfo убран.

## Impact Analysis

- **Affected modules:** publishing (infra), discovery (business), application wiring, config validation, llm client.
- **Breaking changes:** нет для конфига/API. Поведение: Rootly resolve без записи — WARN + метрика (раньше — тихий успех); недоступное хранилище — ошибка доставки; CHANGELOG `Fixed`.
- **New dependencies:** none.
- **Risks:** промах на resolve оставляет инцидент открытым (видно по WARN и `not_tracked`): после апгрейда, рестарта lite-профиля (`MemoryCache`), вытеснения ключей Valkey, перестановки `rootly_configs`. Недоступный Redis блокирует доставку в Rootly до восстановления (повторы на flush, считаются breaker'ом) — принимается: это честнее сиротских инцидентов.

## Rollout / Rollback

- **Rollout:** обычный. Инциденты Rootly, открытые до апгрейда, закрыть вручную (CHANGELOG).
- **Rollback:** предыдущий образ; ключи `publishing:rootly:incident:*` истекают через 7d.
- **Feature flag:** not applicable.
- **Риски из assumed-премис:** поздний `Submit` после `Stop` — защищён ошибкой независимо от порядка shutdown; лимиты тела ответа — при обрезке ошибка декодирования видна в логах.

## Observability

- **Logs:** WARN на ошибки хранилища Rootly; ERROR со стеком на панику паблишера; URL таргетов — только `scheme://host/***`.
- **Metrics:** `RecordAPIError(rootly, "incidents", "not_tracked")`; breaker state теперь правдив (`half-open`).
- **Alerts:** not applicable.

## Deep Review

- **Mandatory triggers present:** `S`, 3 сигнала (`S X R`).
- **Discretionary triggers present:** diff > ~200 строк.
- **Decision:** required.

## Open Questions

- [ ] Порядок shutdown (`Stop` очереди относительно coordinator/grouping) не трассирован полностью — защита от позднего `Submit` нужна в любом случае (assumed).
- [ ] Лимиты 64 KiB / 1 MiB для тел ответов (assumed) — при обрезке видна ошибка декодирования.
