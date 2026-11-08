---
id: UPSTREAM-AI-HISTORY-INVESTIGATION
slug: upstream-ai-history-investigation
stream: Architecture / ADR-019
type: feature
priority: high
status: active
created_at: 2026-11-08
updated_at: 2026-11-08
---

# Requirements: история и investigation на upstream-ядре (срез 2b ADR-019)

## Problem Framing

- **Symptom:** `cmd/amp-upstream` после среза 2a классифицирует алерты, но не пишет историю в PostgreSQL и не запускает investigation: это есть только в `cmd/server` (`AlertProcessor.ProcessAlert` → `DeduplicationService` → таблица `alerts`; `InvestigationQueue.Submit` после классификации). Классификация тратит LLM на алерты под inhibition, а алерт, заглушённый silence, после снятия silence не классифицируется до GC (`tasks/archive/UPSTREAM-AI-CLASSIFY/Spec.md` § Open Questions).
- **Root Cause:** у upstream-ядра одна точка расширения на пути приёма — `provider/mem.AlertStoreCallback`; в 2a через неё подключена только классификация, а колбэк получает только `silencer.Mutes`.
- **Why Now:** срез 2b эпика ADR-019, вторая половина `UPSTREAM-AI-HOOKS`; claim 2026-11-08 (WIP, параллельно со срезами 3 и 4).
- **How We Measure:** тесты `upstreamai` + `upstreamcore`: алерт через `POST /api/v2/alerts` даёт запись истории по ключу SHA-256 (как у `cmd/server`) без IO в `Put`; после классификации ставится investigation по тому же ключу; алерт под inhibition не классифицируется; после снятия silence алерт классифицируется на следующем `Put`.

## Risk Profile

- **Signals:** `C X R`
  - `C` — `amp-upstream` с `--amp.config.file` и `profile: standard` теперь подключается к PostgreSQL и прогоняет миграции; новая метрика `amp_upstream_history_writes_total`; исход классификации `skipped_silenced` → `skipped_muted`; сигнатура `Options.NewAlertCallback` та же, но её `mutes` теперь учитывает inhibition.
  - `X` — `upstreamcore` (mutes с inhibitor), `upstreamai`, `cmd/amp-upstream`, общие `DeduplicationService`, `PostgresStorageAdapter`, `InvestigationQueue`; второй бинарь пишет в те же таблицы `alerts` / `alert_investigations`, что `cmd/server`.
  - `R` — `PostStore` работает под локом провайдера в пути приёма; LLM-расход (investigation).
- **Tier:** Full
- **Notes:** `S` нет: данные алерта уже уходят LLM в investigation `cmd/server`, промпт не меняется. `M` нет: схема БД не меняется, миграций нет.

## User Stories

1. Как оператор AMP на upstream-ядре, я хочу видеть историю всех алертов (включая заглушённые) в PostgreSQL, как на `cmd/server`.
2. Как дежурный, я хочу, чтобы по firing-алерту запускался investigation и находился по тому же fingerprint, что история.
3. Как владелец LLM-квоты, я хочу, чтобы алерты под inhibition не классифицировались и не расследовались.
4. Как оператор, я хочу, чтобы алерт после снятия silence получил классификацию.

## Success Criteria

- [ ] `PostStore` не делает IO и не блокируется: история — в ограниченную очередь с шардированием по fingerprint, переполнение — дроп с метрикой.
- [ ] История пишется для firing и resolved, включая заглушённые silence/inhibition, через `DeduplicationService` (семантика `cmd/server`: create / update статуса и `EndsAt` / ignore дубля).
- [ ] Ключ строки истории, investigation и кеша классификации — один: `alertconv.Fingerprint` (SHA-256) от labels; FNV upstream выводится из тех же labels (`alertconv.UpstreamFingerprint` == `Alert.Fingerprint().String()`).
- [ ] Investigation ставится после классификации (classified и fallback), только для firing, только с `investigation.enabled`, `llm.enabled` и доступным PostgreSQL.
- [ ] Классификация пропускает алерты, заглушённые silence **или** inhibition активной конфигурации.
- [ ] Пропущенный (заглушённый) алерт не запоминается: следующий `Put` проверяет mute заново.
- [ ] `PreStore` ничего не отвергает и не вызывает LLM/IO.
- [ ] Без `--amp.config.file` поведение `amp-upstream` не меняется; PostgreSQL недоступен на старте — `amp-upstream` стартует без истории и investigation (Error в лог), доставка не зависит от PG.
- [ ] Гейты `WORKFLOW.md` § Гейты AMP зелёные, включая `scripts/release-gate.sh`.

## Non-Goals

- AMP-маршруты истории и investigation в `amp-upstream` (поиск по FNV, UI) — срез 6 `UPSTREAM-API-UI`.
- Колонка `upstream_fingerprint` в БД — решение в срезе 6 (см. research, вариант B).
- Redis L2-кеш классификации и общий dedup investigation между репликами — срез 4.
- Agent mode investigation (инструменты, runbooks) в `amp-upstream` — wiring `service_registry` разбирается в срезе 5; здесь Warn и обычный investigation.
- SQLite (`profile: lite`) для истории `amp-upstream`.
- Изменения `cmd/server`.

## Constraints

- **Scope:** `internal/upstreamai`, `cmd/amp-upstream` (новый файл, минимум правок в `main.go`), `internal/upstreamcore` — только mutes с inhibitor (несколько строк, отмечено в `doc.go`). Параллельные срезы 3/4 правят `upstreamcore` и `cmd/` — правки локальные.
- **Security:** DSN/пароль БД не логируются; секреты LLM — как в 2a.
- **Compatibility:** общие таблицы и ключи `cmd/server` не меняются.

## Discovery Notes

- Similar tasks: `tasks/archive/UPSTREAM-AI-CLASSIFY/`, `tasks/archive/INVESTIGATION-SAFETY-RELIABILITY/`.
- Relevant patterns: `ServiceRegistry.initializeDatabase/initializeDeduplication/initializeInvestigation`; `upstreamai.Classifier` (очередь + воркеры).
