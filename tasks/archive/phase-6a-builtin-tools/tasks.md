# PHASE-6A-BUILTIN-TOOLS — Tasks

## Чеклист реализации

### Slice 1: Config + scaffolding (0.5d)
- [ ] Добавить `InvestigationToolsConfig` в `internal/config/` (4 вложенных struct)
- [ ] Добавить `investigation.tools` секцию в `config.yaml` пример (с placeholder endpoints)
- [ ] Создать директорию `internal/core/investigation/tools/`
- [ ] Добавить helper `AlertTimeFromCtx` / `WithAlertTime` в `internal/core/investigation/context.go` (или расширить existing context support)

### Slice 2: Prometheus tool (1.5d)
- [ ] `tools/prometheus.go` — `PrometheusTool` struct с HTTP client, `Definition()`, `Execute()`
- [ ] Парсинг `start_offset`/`end_offset`/`step` из params с дефолтами
- [ ] HTTP вызов `GET /api/v1/query_range`, десериализация ответа, JSON → `ToolResult.Content`
- [ ] `tools/prometheus_test.go` — mock HTTP server, проверка params, error cases

### Slice 3: Loki tool (1.5d)
- [ ] `tools/loki.go` — `LokiTool` struct, аналогично Prometheus
- [ ] LogQL query_range endpoint, конвертация ns timestamp → ISO
- [ ] Optional Basic Auth header если username/password в config
- [ ] `tools/loki_test.go` — mock server

### Slice 4: Kubernetes tool (1.5d)
- [ ] `tools/kubernetes.go` — `KubernetesTool`, dispatch по `action` param
- [ ] Реализовать: `list_pods`, `get_pod`, `get_events`, `get_logs`, `get_deployments`
- [ ] Обернуть существующий `infrastructure/k8s/client.go` (не дублировать client-go setup)
- [ ] `tools/kubernetes_test.go` — fake k8s client или mock

### Slice 5: Database tool (1d)
- [ ] `tools/database.go` — `DatabaseTool`, принимает `*sql.DB`, dispatch по `query_type`
- [ ] 4 SQL запроса: active_queries, slow_queries (graceful если pg_stat_statements нет), replication_lag, connection_stats
- [ ] Результат сериализуется как `[]map[string]any` → JSON
- [ ] `tools/database_test.go` — sqlmock или реальный тест с test DB

### Slice 6: Wiring + интеграционный тест (1d)
- [ ] Найти точку инициализации агента, добавить conditional wiring всех 4 tools
- [ ] Smoke-тест: агент с реальным `ToolRegistry` + все 4 tools зарегистрированы → проверить `Definitions()` не пустой
- [ ] Обновить `README.md` в `internal/core/investigation/` — добавить секцию Built-in Tools

## Вертикальные слайсы (порядок реализации)

Prometheus → Loki → K8s → DB → Wiring

Каждый слайс независим и может быть протестирован отдельно до следующего.
Приоритет: Prometheus и K8s наиболее ценны для первого demo.

## Definition of Done

- [ ] `go build ./...` проходит
- [ ] `go test ./internal/core/investigation/tools/...` проходит
- [ ] Все 4 tools регистрируются при `enabled: true` в config
- [ ] `ToolRegistry.Definitions()` возвращает корректные JSON Schema для всех tools
- [ ] Документация (research.md + Spec.md) актуальна
- [ ] `NEXT.md` обновлён: PHASE-6A перемещён в WIP
