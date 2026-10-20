# Testing — SLACK-WEBHOOK-PLAIN-OK-RESPONSE (2026-10-20)

| Гейт | Команда | Результат |
|---|---|---|
| Затронутые пакеты | `go vet` + `go test -count=1 ./internal/infrastructure/publishing/... ./internal/application/... ./internal/business/publishing/...` | 1891 passed, 4 пакета |
| Быстрый | `make -C go-app quality-gates-fast` | `Fast quality gates passed`, `git status` чистый |
| Release | `scripts/release-gate.sh` | прогон 1 — FAIL `test`/`amtool-compat` (конкурентный прогон другой сессии, `release-gate-run1.md`); прогон 2 @ `91fb0d8` — **PASS 12/12** (`release-gate-run2.md`) |
| Пробелы | `git diff --check main..HEAD` | чисто |
| Проглоченные ошибки | `git diff main..HEAD -- '*.go' \| grep '_, _ :='` | нет |

## Регрессионность e2e-теста

`TestConfigOnlyDelivery_SlackWebhookOKConfirmsDelivery` на клиенте из `main` (`git show main:.../slack_client.go`) — **FAIL**: `should have 1 item(s), but has 2` (повтор на `group_interval`, в логе `failed to parse response: invalid character 'o'` → `delivery not confirmed`). С фиксом — PASS (`-count=3`).

Первая версия теста ждала `6 × group_interval` (900 ms) и проходила и без фикса: Slack-клиент ограничен 1 сообщением/с на вебхук, повтор не успевал. Окно увеличено до 1.5 s, причина — в комментарии теста.

## Известные флейки

`GROUPING-ORPHAN-ADOPTION-TEST-FLAKY` и `FUZZ-RANDOM-ALERTS-RACE-TIMEOUT` в прогоне 2 не проявились (`race` — 384 s).
