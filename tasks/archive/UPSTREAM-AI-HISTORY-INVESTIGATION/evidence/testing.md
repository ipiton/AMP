# Testing: UPSTREAM-AI-HISTORY-INVESTIGATION

Дата: 2026-11-08, ветка `feature/upstream-ai-history-investigation`.

| Проверка | Результат |
|---|---|
| `go vet` + `go test -race -count=1 ./internal/upstreamai/ ./internal/upstreamcore/ ./cmd/amp-upstream/` | PASS |
| `make -C go-app quality-gates-fast` | PASS (`go fmt` без изменений) |
| `scripts/release-gate.sh` | 12/13 PASS; `lint` FAIL — см. ниже |
| `golangci-lint run` (повтор после фикса) | 7 issues, все предсуществующие: errcheck `<-done` / `<-handle.Done()` в `redis_event_bus_test.go`, `delivery_confirmation_test.go` (`LINT-ERRCHECK-CHANNEL-RECEIVE`), файлы вне диффа |
| `git diff --check main...HEAD` | чисто |
| Проглоченные ошибки (`_, _ :=`) в диффе | нет |

`lint` в прогоне `release-gate` нашёл также SA4006 в новом `classifier_test.go` (неиспользуемое присваивание) — исправлено в тесте, `golangci-lint` по `upstreamai`/`upstreamcore`/`cmd/amp-upstream` — 0 issues.

## Покрытие

- `history_test.go`: порядок событий одного алерта, ключ SHA-256, неблокирующий `PostStore` при полном шарде, ошибка и таймаут записи, `PreStore` nil, `PostGC` no-op, resolved из `PostDelete`, `PostStore` после `Stop`.
- `classifier_test.go`: mute с перепроверкой (fake clock), investigation после classified/fallback с ключом истории, не после ошибки и mute, повторный `Submit` на resend без второго вызова LLM, нет `Submit` после `Stop`.
- `convert_test.go`: SHA-256 == `alertconv.Fingerprint`, FNV upstream == `alertconv.UpstreamFingerprint`, статус.
- `chain_test.go`: порядок, первая ошибка `PreStore`.
- `upstreamcore/mutes_test.go`: `mutes` истинна под inhibition (через API) и silence, ложна для источника и для несовпавшего `equal`.
- `integration_test.go`: ingest в реальное ядро → история и investigation с одним ключом.
- `cmd/amp-upstream/amp_test.go`: `warnOnly`.

## Не проверено

- Живой PostgreSQL с `amp-upstream` (запись через `DeduplicationService` проверена в `cmd/server`, здесь — через fake `Record`).
- N1 (нагрузка mute-перепроверки при resend 1m) — замера под нагрузкой не было; метрики `skipped_muted`/`dropped_queue_full` позволят увидеть в эксплуатации.
