# release-gate, прогон 2 (2026-10-20, @ 91fb0d8, без параллельных прогонов)

```text
[release-gate] ==================== SUMMARY ====================
STEP             STATUS TIME     NOTE
build            PASS   12s      go build ./... (go-app)
lint             PASS   20s      golangci-lint run (go-app)
test             PASS   61s      go test ./... -count=1 (go-app)
futureparity     PASS   18s      build+test, futureparity tag, cmd/server
race             PASS   384s     go test -race, publishing/grouping/silencing/templating
helm-deps        PASS   0s       chart dependencies (Chart.lock, no stale charts/)
helm-dev         PASS   1s       lint + template, values-dev.yaml
helm-production  PASS   0s       lint + template, values-production.yaml
helm-small       PASS   0s       lint + template, values-small.yaml
helm-rbac        PASS   1s       RBAC scope: namespaced list-secrets only
helm-tests       PASS   17s      helm/amp/tests/*.sh render tests
amtool-compat    PASS   96s      real amtool CLI vs deploy/smoke stack
[release-gate] ==================================================
[release-gate] RESULT: PASS
```
