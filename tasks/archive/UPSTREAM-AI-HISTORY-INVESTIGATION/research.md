---
id: UPSTREAM-AI-HISTORY-INVESTIGATION
slug: upstream-ai-history-investigation
stream: Architecture / ADR-019
type: feature
artifact: research-pack
status: complete
created_at: 2026-11-08
updated_at: 2026-11-08
---

# Research Pack - история и investigation на upstream-ядре

## 0) TL;DR

- Solving: история в PG и investigation на `amp-upstream`, общий ключ с `cmd/server`, классификация без заглушённых алертов.
- Found: история `cmd/server` — таблица `alerts` (одна строка на fingerprint) через `DeduplicationService`; ключ — SHA-256 `alertconv.Fingerprint`, его же берёт `upstreamai.toCoreAlert` для кеша классификации. Investigation требует классификацию и ставится после неё. Inhibitor доступен в `reloader.inhibitor` (atomic), колбэк его не видит.
- Chose: отдельный колбэк `History` с шардированными воркерами поверх `DeduplicationService`; investigation — из воркера классификатора; `mutes` = silence ∨ inhibitor активной конфигурации; пропуск по mute не запоминается; `PreStore` ничего не отвергает; ключ — SHA-256, без миграции.
- Risks: правка порта в `core.go` пересекается со срезом 4; повторная запись в PG на каждый resend (как у `cmd/server`).
- Next step: -> spec

## 1) Questions

1. Что такое «история» и как её писать, не нарушая неблокирующий `PostStore`?
2. Где ставить investigation, если ему нужна классификация?
3. Какой ключ у строк истории и investigation, и как к нему прийти от FNV upstream?
4. Какой «дешёвый фильтр» допустим в `PreStore`?
5. Как не классифицировать алерты под inhibition и вернуть классификацию после снятия silence?

## 2) Findings

| # | Finding | Source | Class |
|---|---|---|---|
| F1 | История — таблица `alerts`, upsert по `fingerprint`; `DeduplicationService.ProcessAlert` делает Get → Save/Update (статус, `EndsAt`, `StartsAt` повторного инцидента) или ignore | `internal/core/services/deduplication.go:203-420`, `internal/infrastructure/postgres_storage_adapter.go:52-106` | code-read |
| F2 | `fingerprint` в БД — SHA-256 (64 hex) из `alertconv.Fingerprint`, его ставят парсеры ingest `cmd/server`; FNV (`UpstreamFingerprint`) — только в ответах API | `internal/core/alertconv/alertconv.go:72-133`, `internal/application/handlers/alerts.go:540` | code-read |
| F3 | `upstreamai.toCoreAlert` уже ставит SHA-256 — ключи кеша классификации совпадают с `cmd/server` | `internal/upstreamai/convert.go:13-17` | code-read |
| F4 | Investigation `cmd/server` ставится после классификации (`Submit(alert, classification)`), `Submit` неблокирующий, без IO, dedup по fingerprint+StartsAt+status | `alert_processor.go:851-854`, `internal/infrastructure/investigation/queue.go:146-178`, `dedup.go:52` | code-read |
| F5 | `mem.Put` вызывает `PreStore`/`PostStore` под `a.mtx`; ошибка `PreStore` выкидывает алерт из store (не будет ни доставки, ни API) | `provider/mem/mem.go@v0.34.1:302-345` | code-read |
| F6 | Правила `FilterEngine` `cmd/server`: noise/low_confidence — нужен LLM; empty_alertname, old_resolved, duplicate — upstream их не дропает (алерт без alertname валиден, старые resolved уходят GC, повторы сливает `Merge`/nflog) | `internal/core/services/filter_engine.go:96-128` | code-read |
| F7 | `reloader.inhibitor` — `atomic.Pointer[inhibit.Inhibitor]`, swap на reload; `Inhibitor.Mutes(ctx, lset)` — то, что вызывает notify-стадия и API | `internal/upstreamcore/reloader.go:66-67`, `inhibit/inhibit.go@v0.34.1:178` | code-read |
| F8 | Колбэк получает `silencer.Mutes`; `r` (reloader) объявляется после `mem.NewAlerts`, но замыкание на `r` уже используется для `groupFn` | `internal/upstreamcore/core.go:232-252` | code-read |
| F9 | Пропуск по silence в 2a оставляет запись `skipped` до GC — после снятия silence классификации нет | `internal/upstreamai/classifier.go:196-201` | code-read |
| F10 | `profile` по умолчанию `standard`, `database.host` по умолчанию `localhost`; `PostgresPool.Connect` пингует и падает, если PG нет | `internal/config/config.go:878,903`, `internal/database/postgres/pool.go:115-160` | code-read |

## 3) Options

**Generation:** single pass (без fan-out): решение обратимо, схема БД не меняется, данные `cmd/server` не трогаются.

### Ключ (Q3)

- **A. SHA-256 везде в PG, FNV выводится из labels (chosen).** Строки истории и investigation — `alertconv.Fingerprint`, как у `cmd/server` и кеша классификации; FNV upstream — `alertconv.UpstreamFingerprint` от тех же labels, тестом закрепляется равенство с `Alert.Fingerprint()`. Поиск по FNV (срез 6): живой алерт — `alerts.Get(fp)` → labels → SHA-256; ушедший — по строкам истории (labels в строке). Без миграции, данные пилота совместимы.
- **B. Колонка `upstream_fingerprint` + индекс в `alerts` и `alert_investigations`.** Прямой поиск по FNV в SQL. Миграция (`M`), бэкфилл старых строк в SQL невозможен (FNV по сортированным labels), писать её должны оба бинаря. Отложено в срез 6, где появляются маршруты поиска — там видно, нужен ли индекс.
- **C. FNV как ключ в `amp-upstream`.** Две строки на один алерт при работе рядом с `cmd/server`, ломается кеш классификации — отклонено.

### Где ставить investigation (Q2)

- **Из воркера классификатора после результата (chosen).** Ему нужна классификация (F4), `Submit` неблокирующий. Формально «ставит» его цепочка `PostStore` → очередь классификации.
- Из `PostStore` без классификации — investigation без контекста severity, и для заглушённых алертов тоже — отклонено.

### Фильтр `PreStore` (Q4)

- **Ничего не отвергать (chosen).** Каждое правило `FilterEngine` либо требует LLM, либо расходится с upstream (F6): отказ в `PreStore` убирает алерт из store целиком (F5) — ни доставки, ни silences/inhibition, ни API; контрактные сьюты это ловят. Дроп шума уже есть за флагом в notify-стадии (2a). Правило ADR «`PreStore` — только дешёвый фильтр» выполняется как инвариант: AMP-колбэк в `PreStore` не делает работы.
- Дроп по закешированной классификации `noise` в `PreStore` — отклонено: отказ resend'а существующего алерта оставляет старую копию, она истекает по `EndsAt` и даёт resolved-уведомление по шуму.

### История (Q1)

- **Отдельный колбэк `History`, шарды по fingerprint (chosen).** Неблокирующая отправка в канал шарда `fp % N` (порядок событий одного алерта сохраняется), воркер вызывает `DeduplicationService.ProcessAlert` с таймаутом. Пишутся все алерты, включая заглушённые (ADR-019: «история полная»).
- Один воркер — порядок даром, но ~1 запрос/мс на весь поток resend'ов — узко.

### Mute (Q5)

- **`mutes` = `silencer.Mutes` ∨ `inhibitor.Mutes` активной конфигурации (chosen)** — замыкание на `r` в `core.go` (как `groupFn`), метод `reloader.inhibits`. Сигнатура `Options.NewAlertCallback` не меняется — срезы 3/4 не задеты. Пропуск по mute удаляет запись: следующий `Put` (resend Prometheus) проверяет снова — этим же закрывается повтор после снятия silence.
- Передавать в колбэк две функции — меняет сигнатуру `Options`, конфликтует с параллельными срезами — отклонено.

## 4) Decision

A + investigation из воркера + `PreStore` без отказов + `History` с шардами + mute с inhibitor и без памяти о пропуске. PostgreSQL недоступен на старте → Error и работа без истории/investigation (ADR-019: доставка не зависит от PG); переподключение — TECH-DEBT.

## 5) Spec Inputs

- Новые: `upstreamai/history.go`, `upstreamai/chain.go`, `cmd/amp-upstream/amp.go`.
- Правки: `upstreamai/classifier.go` (investigation, пропуск без записи, outcome `skipped_muted`), `upstreamai/convert.go` (статус resolved), `upstreamcore/core.go` + `reloader.go` (mutes с inhibitor), `upstreamcore/doc.go`, `cmd/amp-upstream/main.go` (вызов).
- Метрики: `amp_upstream_history_writes_total{outcome="written|error|dropped_queue_full"}`.
- Docs: `docs/UPSTREAM_UPGRADE.md`, `config.yaml.example` (если там описан `amp-upstream`), `CHANGELOG.md`.

## 6) References

- `docs/06-planning/DECISIONS.md` § ADR-019; `BACKLOG.md` § ADR-019 (`UPSTREAM-AI-HOOKS`).
- `tasks/archive/UPSTREAM-AI-CLASSIFY/Spec.md` § Open Questions.
