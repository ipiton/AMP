# Testing — INVESTIGATION-SAFETY-RELIABILITY

| Проверка | Команда | Результат |
|---|---|---|
| Unit/race по затронутым пакетам | `go test -race ./internal/infrastructure/investigation/... ./internal/infrastructure/llm/... ./internal/core/... ./internal/config/... ./internal/application/...` | 1375 passed; 1 FAIL — `TestInitializeGrouping_StandardReconciliationAdoptsOrphanFromCrashedReplica`, известный флейк `GROUPING-ORPHAN-ADOPTION-TEST-FLAKY`; повтор пакета `-count=2` — 580/580 |
| vet | `go vet` по тем же пакетам | чисто |
| Fast gates | `make -C go-app quality-gates-fast` | PASS |
| Release gate | `scripts/release-gate.sh` @ `7341c77` | RESULT: FAIL — три шага, все предсуществующие (ниже) |
| Пробелы | `git diff --check main...HEAD` | чисто |

## Release gate: разбор FAIL

- **lint** — 7 `errcheck` в `delivery_confirmation_test.go` / `redis_event_bus_test.go` (`LINT-ERRCHECK-CHANNEL-RECEIVE`, красный и на `main`). Новых замечаний в диффе нет.
- **test** — `TestBackgroundWorker_CancellationDuringWarmup` (`REFRESH-WORKER-WARMUP-STOP-FLAKY`); пакет `business/publishing` веткой не менялся, изолированно 5/5 зелёный.
- **amtool-compat** — «amp never became healthy»: проба гейта `curl localhost:18080/healthz` попадает не в контейнер — на `127.0.0.1:18080` слушает локальный `agent-memory-mcp`, на `[::1]:18080` — `kubectl port-forward` (ответ 404). Контейнер при этом `healthy`. Ручной прогон того же шага (healthcheck контейнера + те же четыре команды amtool v0.34.1 по docker-сети к `amp:8080`): `alert query`, `config show`, `silence add`, `silence query` — OK. Записано как `RELEASE-GATE-SMOKE-PORT-CONFLICT`.

## Summary

```
STEP             STATUS TIME     NOTE
build            PASS   30s      go build ./... (go-app)
lint             FAIL   273s     golangci-lint run (go-app)
test             FAIL   468s     go test ./... -count=1 (go-app)
futureparity     PASS   33s      build+test, futureparity tag, cmd/server
upstream-core    PASS   48s      contract suites vs divergence baseline + amtool, upstream core
race             PASS   427s     go test -race, publishing/grouping/silencing/templating
helm-deps        PASS   0s       chart dependencies (Chart.lock, no stale charts/)
helm-dev         PASS   1s       lint + template, values-dev.yaml
helm-production  PASS   0s       lint + template, values-production.yaml
helm-small       PASS   0s       lint + template, values-small.yaml
helm-rbac        PASS   1s       RBAC scope: namespaced list-secrets only
helm-tests       PASS   27s      helm/amp/tests/*.sh render tests
amtool-compat    FAIL   402s     real amtool CLI vs deploy/smoke stack
[release-gate] ==================================================
[release-gate] RESULT: FAIL
```
