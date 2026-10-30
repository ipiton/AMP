---
id: UPSTREAM-AI-CLASSIFY
slug: upstream-ai-classify
stream: Architecture / ADR-019
type: feature
priority: high
status: active
created_at: 2026-10-30
updated_at: 2026-10-30
---

# Requirements: AI-классификация на upstream-ядре (срез 2a ADR-019)

## Problem Framing

- **Symptom:** `cmd/amp-upstream` (upstream-ядро Alertmanager, срезы 1a/1b) не знает об AI-слое: алерты доставляются без LLM-классификации. В текущем `cmd/server` классификация есть, но синхронна в `POST /api/v2/alerts` (`processEnriched`, `alert_processor.go:823`): до 30s × ретраи на алерт, при outage провайдера ingest удлиняется (`PROD-LLM-ALERT-PATH-ISOLATION`), а на групповом пути, который включён по умолчанию, классификация теряется (`GROUP-NOTIFY-CLASSIFICATION`). LLM-клиент ретраит 429/5xx, но игнорирует `Retry-After` (deep review F4 `INVESTIGATION-SAFETY-RELIABILITY`).
- **Root Cause:** классификация встроена в путь приёма, а не в путь доставки; у upstream-ядра нет точек расширения, кроме `provider/mem.AlertStoreCallback` и стадий `notify`.
- **Why Now:** срез 2 эпика ADR-019 разблокирован вердиктом PoC (2026-10-24); первая позиция Queue. Нарезан на `start-task` 2026-10-30: 2a — классификация (эта задача), 2b `UPSTREAM-AI-HISTORY-INVESTIGATION` — история в PG и investigation.
- **How We Measure:** тест на `upstreamcore` + `upstreamai`: алерт через `POST /api/v2/alerts` принимается без вызова LLM в запросе; вебхук группы получает `llm_*` аннотации; при «зависшем» LLM уведомление уходит не позже бюджета ожидания без `llm_*`.

## Risk Profile

- **Signals:** `C X R`
  - `C` — новые ключи `llm.notify_wait`, `llm.drop_noise`; новый флаг `--amp.config.file` у `amp-upstream`; новые аннотации `llm_*` в уведомлениях; новые поля `upstreamcore.Options`.
  - `X` — `upstreamcore`, новый пакет AI-хуков, `llm`, `config`, `cmd/amp-upstream`.
  - `R` — путь доставки уведомлений (ожидание в notify-стадии), поведение LLM-ретраев в обоих бинарях.
- **Tier:** Full
- **Notes:** три сигнала → Full. `S` нет: данные алерта уходят LLM-провайдеру и сейчас, контракт промпта не меняется.

## User Stories

1. Как оператор AMP, я хочу, чтобы приём алерта не ждал LLM, чтобы outage провайдера не тормозил Prometheus.
2. Как получатель уведомлений, я хочу видеть AI-классификацию в уведомлении группы (шаблоны upstream), а не только на прямом пути.
3. Как оператор, я хочу, чтобы LLM не мог молча заглушить алерт: дроп шума — только за явным флагом, с метрикой и логом.
4. Как владелец LLM-квоты, я хочу, чтобы клиент соблюдал `Retry-After` и не классифицировал заглушённые silence алерты.

## Success Criteria

- [ ] `PostStore` не вызывает LLM и не блокируется: постановка в ограниченную очередь, при переполнении — дроп с метрикой.
- [ ] Классифицируются только firing-алерты, один раз на fingerprint, пока алерт в store; заглушённые silence — не классифицируются.
- [ ] Notify-стадия ждёт незавершённые классификации группы не дольше `llm.notify_wait` (и не дольше половины оставшегося таймаута стадии), затем отправляет как есть.
- [ ] Готовая классификация попадает в аннотации `llm_severity`, `llm_confidence`, `llm_reasoning`, `llm_recommendations` копий алертов (соглашение `formatAlertmanager`); алерты в store не мутируются.
- [ ] `llm.drop_noise: true` убирает из уведомления алерты с `severity=noise`; по умолчанию `false`; дроп — метрика и лог.
- [ ] LLM-клиент: 429/503 с `Retry-After` больше `MaxDelay` ретраев — сразу ошибка без повтора; меньше — следующая попытка не раньше `Retry-After`.
- [ ] `amp-upstream` без `--amp.config.file` или с `llm.enabled: false` ведёт себя как сейчас (чистый upstream); контрактные сьюты и `upstream-core-gate.sh` зелёные.
- [ ] Метрики: исходы классификации, исходы ожидания, дропы шума.

## Non-Goals

- История в PostgreSQL, постановка investigation, фильтр в `PreStore`, соответствие fingerprint FNV ↔ SHA-256 для строк БД — срез 2b.
- Изменение `cmd/server` (кроме общего LLM-клиента: `Retry-After`) — его ядро удаляется в срезе 5.
- Проверка inhibition перед классификацией (inhibitor пересоздаётся на reload) — known gap, срез 2b/5.
- Helm-проводка `amp-upstream` и его AMP-конфига — срез 5.
- Ретраи `Retry-After` в investigation (`chat.go`) — отдельный путь, не в этом срезе.
- Hot reload LLM-конфига в `amp-upstream` — `SIGHUP` перечитывает только `alertmanager.yml`.

## Constraints

- **Scope:** `internal/upstreamcore` (две точки расширения в `Options`), новый пакет AI-хуков, `internal/infrastructure/llm` (`Retry-After`), `internal/config` (два ключа), `cmd/amp-upstream`. Порт upstream (`core.go`/`reloader.go`) меняется минимально — каждая правка помечается как отличие от upstream в `doc.go`.
- **Security:** секреты LLM не логируются (`api_key` из AMP-конфига); `llm_reasoning` обрезается до 500 символов.
- **Compatibility:** без флага поведение `amp-upstream` не меняется; `cmd/server` получает только изменение ретраев LLM.

## Discovery Notes

- Similar tasks: `tasks/archive/UPSTREAM-CORE-POC-WIRING/`, `tasks/archive/UPSTREAM-CORE-POC-GATE/`, `tasks/archive/INVESTIGATION-SAFETY-RELIABILITY/`.
- Relevant patterns: `formatAlertmanager` (`internal/infrastructure/publishing/formatter.go:272-318`) — `llm_*` аннотации; `ServiceRegistry.initializeClassification` / `sharedLLMClient` — сборка сервиса; `silence.Silencer` — `AlertStoreCallback` upstream.
- Open unknowns: нет блокирующих; выбор места ожидания и формы точек расширения — в `research.md`.
