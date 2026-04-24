# PHASE-5A: Двухфазный Async Investigation Pipeline

## Проблема

AMP сейчас делает одно: принимает алерт → классифицирует (LLM) → фильтрует → публикует.
Это полностью синхронный путь. У нас нет механизма расследования: "что случилось, почему, что делать".

Alertmanager этого не умеет вообще. SherlockOps/HolmesGPT/Keep — reference реализации.
**Phase 5A** строит инфраструктуру двухфазного pipeline, который станет основой intelligence-функций AMP.

## Что делает Phase 5A

```
Phase 1 (существующий путь, синхронный):
  Алерт → Dedup → Inhibition → LLM Classification → Filter → Publish
                                       ↓
Phase 2 (новый, асинхронный):
  InvestigationQueue ← Submit(alert, classification)
         ↓
  InvestigationWorker: LLM "расследуй этот алерт"
         ↓
  Сохранить findings в alert_investigations
```

Phase 1 не ждёт Phase 2. Нотификации уходят немедленно.
Phase 2 запускается fire-and-forget после успешной классификации.

**Phase 5A НЕ включает**: инструменты (PromQL, LogQL, K8s) — это Phase 6A.
**Phase 5A строит**: очередь, воркеры, DB-схему, базовый LLM-промпт для расследования.

## Success Criteria

1. После публикации алерта в БД появляется запись в `alert_investigations` со статусом `queued`
2. InvestigationWorker обрабатывает запись: вызывает LLM с промптом расследования
3. Результат (findings, recommendations, confidence) сохраняется в `alert_investigations` со статусом `completed`
4. При отказе LLM: статус `failed`, retry по transient ошибкам (как в PublishingQueue)
5. HTTP endpoint `GET /api/v1/alerts/{fingerprint}/investigation` возвращает результат
6. Метрики Prometheus: `amp_investigation_queue_depth`, `amp_investigations_total{status}`
7. Основной pipeline (Phase 1) не замедляется: `ProcessAlert()` возвращается ≤ 50ms overhead

## Scope

### В scope
- `alert_investigations` таблица (новая миграция)
- `InvestigationQueue` — priority queue с worker pool (по образцу `PublishingQueue`)
- `InvestigationService` — оркестратор Phase 2, вызывает LLM
- Интеграция в `AlertProcessor.ProcessAlert()` — submit после классификации
- `InvestigationConfig` в конфиге приложения
- REST endpoint для чтения результатов расследования
- Unit-тесты для queue, worker, service
- Prometheus-метрики

### Вне scope (будет в следующих фазах)
- Инструменты: PromQL, LogQL, K8s API (Phase 6A)
- Runbook matching (Phase 6B)
- Agentic tool-calling loop (Phase 5B)
- UI для отображения расследований
- Webhook/нотификация с результатами расследования
