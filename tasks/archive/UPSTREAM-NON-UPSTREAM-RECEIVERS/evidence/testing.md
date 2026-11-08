# Testing — UPSTREAM-NON-UPSTREAM-RECEIVERS

Дата: 2026-11-08, ветка `feature/upstream-non-upstream-receivers` @ `23a816f` (код), toolchain `go1.27.3`.

| Гейт | Результат | Файл |
|---|---|---|
| Затронутые пакеты: `go vet` + `go test -race -count=1` (`upstreamtargets`, `upstreamcore`, `infrastructure/k8s`, `cmd/amp-upstream`, `business/publishing`) | PASS | `packages.txt` |
| `make -C go-app quality-gates-fast` | PASS, `git status` после `go fmt` чистый | `quality-gates-fast.txt` |
| `golangci-lint run` по `upstreamtargets`, `upstreamcore`, `infrastructure/k8s`, `cmd/amp-upstream` | 0 issues | — |
| `git diff --check main...HEAD` | чисто | — |
| Проглоченные ошибки (`_, _ :=`) в диффе | нет | — |
| `scripts/release-gate.sh` | 12/13 PASS, `RESULT: FAIL` на `lint` | `release-gate.txt` |

## Красный `lint` в release-gate — предсуществующий

7 `errcheck` в `internal/infrastructure/publishing/delivery_confirmation_test.go:559,602,645` и `internal/infrastructure/silencing/redis_event_bus_test.go:202,246,268,282` — ровно `BUGS.md` `LINT-ERRCHECK-CHANNEL-RECEIVE` (high). Файлы веткой не тронуты; по пакетам ветки линтер чистый. Остальные шаги, включая `test`, `race`, `upstream-core`, `helm-rbac`, `amtool-compat`, — PASS (флейки refresh и конфликт порта smoke в этом прогоне не проявились).

## Новые тесты

- `upstreamtargets/convert_test.go`: перевод по типам (webhook, rootly, slack, pagerduty), Rootly REST → skip, PagerDuty URL (база, `/`, `/v2/events`, `/v2/enqueue/`, `/v2/change/enqueue`, пустой → global) и routing key (`routing_key`, `Authorization` Bearer/сырой, приоритет `routing_key`, `Authorization` не шлётся), `send_resolved` (bool/строки/числа), заголовки (secrets, зарезервированные отбрасываются, Basic → `basic_auth` с padding и без), отказ на некорректный `Authorization`, глобальные поля Slack, `redact`.
- `upstreamtargets/source_test.go`: принадлежность receiver'у, порядок, исходный `conf` не меняется, причины skip и gauge, секреты не в `conf.String()` и логах, `Refresh` changed/копия снимка/ошибка держит снимок, gauge `invalid`, WARN по отброшенным заголовкам, `Run` → reload только при смене и выход по cancel.
- `upstreamcore/targets_e2e_test.go`: K8s-Secret (фейковый K8s + настоящий discovery) доставляет на httptest рядом с файловой интеграцией, с URL/Authorization/заголовком; `/api/v2/status` без значений; переживает `Reload`; смена Secret + `Refresh` + `Reload` переключает адресата; битый таргет не ломает reload.
- `infrastructure/k8s/client_paging_test.go`: пагинация 1200 Secret'ов по 500 через `continue` (настоящий clientset против httptest API), `Health` на зависшем `/version` — `TimeoutError` за ~5s, отмена контекста — `context.Canceled`, не timeout; `Close` параллельно с `ListSecrets` под `-race`.

## Ограничения

- Размер `amp-upstream`: 43.2 → 78.9 МБ (полный clientset client-go) — `TECH-DEBT.md` `K8S-CLIENTSET-BINARY-SIZE`.
- Реальный кластер и реальные Rootly/PagerDuty/Slack не проверялись; доставка — на httptest.
