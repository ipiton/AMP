---
id: PROD-GRACEFUL-SHUTDOWN
slug: prod-graceful-shutdown
stream: Production Readiness / Reliability
type: bug
artifact: research-pack
status: complete
created_at: 2026-10-06
updated_at: 2026-10-06
---

# Research Pack - корректный graceful shutdown AMP

## Goal

Решить, где держать задержку перед остановкой, чем проверять drain без кластера и какие места в коде затрагиваются. Уровень 2, триггеры: infrastructure (Helm) и несколько вариантов.

## Sources

- `go-app/cmd/server/main.go:166-202`, `internal/application/service_registry.go:2318`, `service_registry_health.go:38`, `handlers/status.go:74`, `router.go:40-45`
- `internal/config/config.go:260,810`, `docs/CONFIGURATION_GUIDE.md:94`, `cmd/server/futureparity_compat.go:160`
- `helm/amp/values.yaml:248-249`, `templates/deployment.yaml:68,188,213`, `Dockerfile:39`, `deploy/e2e-ha/run.sh`
- upstream `alertmanager@v0.32.0/cmd/alertmanager/main.go:266-321,613-626` (локальный module cache)

## Findings

- **Мёртвый ключ таймаута.** `server.graceful_shutdown_timeout` (дефолт 30s) парсится и описан в `CONFIGURATION_GUIDE.md`, но `main` берёт захардкоженные `30*time.Second`. Ещё один мёртвый ключ — `gracefulShutdown.preStopDelay`.
- **Конфиг может не прочитаться.** Из-за бага `CONFIG-MISSING-FILE-DROPS-ENV` дефолтный Helm-деплой работает на минимальном фолбэк-конфиге: env игнорируется, таймаут будет `0`. Нужен фолбэк на дефолт при `0`, иначе `server.Shutdown` отменится мгновенно. Задать таймаут из чарта через env сейчас нельзя.
- **Readiness.** `/-/ready` и `/readyz` вызывают `registry.Readiness`. `/-/healthy` и `/healthz` идут через другой путь. Значит, флаг «останавливаемся» в `Readiness` переключает только readiness, liveness не затронут.
- **Registry.** `ServiceRegistry.Shutdown` синхронный, с внутренними таймаутами по 5s. Финальный snapshot пишется первым. Других `signal.Notify(SIGTERM)` и `log.Fatal` в пути сервера нет.
- **Upstream.** Alertmanager на SIGTERM не переключает readiness и не делает drain HTTP: он выходит из `run()`, а в defer — `close(stopc)` и `wg.Wait()` для финальных snapshot nflog и silences. AMP будет строже upstream, parity не нарушается.
- **Образ и e2e.** Runtime-образ — `alpine:3.24`, так что `exec sleep` в preStop работает. Нативное действие `preStop.sleep` требует k8s ≥ 1.30, а `kubeVersion` в чарте не задан. `e2e-ha` работает на compose без балансировщика: «0 ошибок 5xx при rolling update» там не измерить.

## Decision

- **Задержка — preStop `exec sleep {{ preStopDelay }}` в чарте**, без задержки в процессе. В k8s endpoint'ы снимаются параллельно с preStop, поэтому sleep закрывает гонку с kube-proxy и Ingress. Задержка в процессе тормозила бы Ctrl+C и compose без пользы. Ценой такого выбора preStop зависит от `sleep` в образе: при переходе на distroless хук упадёт, kubelet отправит SIGTERM сразу, pod не сломается.
- **В процессе:** SIGTERM → readiness 503 (флаг в registry) → `server.Shutdown` → `registry.Shutdown` → `main` ждёт завершения. Таймаут — из `server.graceful_shutdown_timeout`, при `0` — дефолт.
- **Бюджет:** `terminationGracePeriodSeconds` ≥ `preStopDelay` + таймаут + запас. Дефолт таймаута, guard в чарте или вычисление из values — решить на `/spec`.
- **Проверка:** Go-тест на последовательность (in-flight запрос завершается 200, readiness 503, registry останавливается после HTTP, `main` ждёт) и render-тест чарта. Rolling update в k8s вживую не проверяется: кластера нет, это ограничение фиксируется в итоге задачи. Parallel options не запускались: откат дешёвый, решения про данные и security нет.

## Follow-ups

- [ ] Spec: как чарт задаёт таймаут, пока не исправлен `CONFIG-MISSING-FILE-DROPS-ENV`: флаг CLI или только дефолт в коде.
