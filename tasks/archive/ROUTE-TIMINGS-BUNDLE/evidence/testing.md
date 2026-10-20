# Testing (2026-10-20)

| Гейт | Результат |
|---|---|
| Затронутые пакеты: `go vet` + `go test -count=1` (`business/routing`, `infrastructure/routing`, `infrastructure/grouping`, `application/handlers`, `config`, `pkg/configvalidator`) | PASS, 1616 тестов в 11 пакетах |
| `make -C go-app quality-gates-fast` | PASS, `git status` после `go fmt` без изменений |
| `scripts/release-gate.sh` @ `9527870` + рабочие правки docs | `RESULT: PASS` 12/12 с первого прогона (`release-gate-summary.txt`), включая `test` и `race` |
| `git diff --check` | PASS (после удаления markdown-переносов в шапках `Spec.md`/`tasks.md`) |
| Проглоченные ошибки `_, _ :=` в диффе | нет |

Известные красные шаги из брифа в этом прогоне не воспроизвелись:

- `GROUPING-ORPHAN-ADOPTION-TEST-FLAKY` — шаг `test` зелёный; отдельно `-count=40` без нагрузки — 40/40 PASS. В deep-review под нагрузкой: 2/20 на ветке против 1/20 на базе — в пределах шума.
- `FUZZ-RANDOM-ALERTS-RACE-TIMEOUT` — шаг `race` зелёный (322s).

Предсуществующий флейк `TestGroupIntervalChain_ResolveGoesOutAndTheGroupIsGone` (4/300 и на `main`, и на ветке) заведён в BUGS как `FLAKE-GROUP-INTERVAL-CHAIN-RESOLVE`.

Smoke на бинаре — `smoke.md`.
