# release-gate, прогон 1 (2026-10-20, @ 91fb0d8, Go-код как в 4064773)

```text
[release-gate] ==================== SUMMARY ====================
STEP             STATUS TIME     NOTE
build            PASS   158s     go build ./... (go-app)
lint             PASS   76s      golangci-lint run (go-app)
test             FAIL   242s     go test ./... -count=1 (go-app)
futureparity     PASS   90s      build+test, futureparity tag, cmd/server
race             PASS   531s     go test -race, publishing/grouping/silencing/templating
helm-deps        PASS   1s       chart dependencies (Chart.lock, no stale charts/)
helm-dev         PASS   3s       lint + template, values-dev.yaml
helm-production  PASS   1s       lint + template, values-production.yaml
helm-small       PASS   0s       lint + template, values-small.yaml
helm-rbac        PASS   2s       RBAC scope: namespaced list-secrets only
helm-tests       PASS   21s      helm/amp/tests/*.sh render tests
amtool-compat    FAIL   1045s    real amtool CLI vs deploy/smoke stack
[release-gate] ==================================================
[release-gate] RESULT: FAIL
```

Красные шаги:

```text
--- FAIL: TestGetAggregatedStats_WithData (11.28s)
    postgres_history_test.go:228: failed to start postgres container: run postgres: generic container: create container: reaper: from container "c9a112f7": wait for reaper c9a112f7: detect internal port: check target: retries: 0, last err: unexpected container status "removing"
FAIL

[release-gate] amtool alert query failed
```

`amtool-compat`: health-check на `localhost:18080` прошёл, затем `amtool: error: Get "http://amp:8080/api/v2/alerts?...": dial tcp 192.168.147.3:8080: connect: connection refused`.

**Разбор:** оба шага инфраструктурные, к задаче не относятся. Одновременно шёл `scripts/release-gate.sh` другой сессии (pid 14101, старт 13:16): smoke-стек у всех прогонов — один compose-проект `amp-smoke` и порт 18080, так что прогоны снимают/подменяют контейнеры друг друга (`down -v` в начале шага); testcontainers-reaper поймал контейнер в статусе `removing` под той же нагрузкой на Docker (сборка образа шла 858 с). `go test -count=1 ./internal/infrastructure/repository/` отдельно — 21 passed. Повтор гейта после завершения чужого прогона — `release-gate-run2.md`, PASS 12/12.
