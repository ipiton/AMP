# Requirements: PARITY-RESOLVE-TIMEOUT-ENDSAT

## Context

Найдено на разборе karma (2026-09-23, BACKLOG «UI и экосистема — идеи из karma»): активный алерт, пришедший через `POST /api/v2/alerts` без `endsAt`, отдаётся из `GET /api/v2/alerts` с `startsAt == endsAt == updatedAt`. Воспроизведено на живом lite-инстансе.

Upstream Alertmanager при отсутствии `endsAt` ставит `endsAt = startsAt + global.resolve_timeout` (default `5m`) и с каждым повторным POST продлевает окно. Любой потребитель, который считает «`endsAt` в прошлом» признаком resolved (ровно так рассуждает сам Alertmanager, а за ним `amtool`, Grafana и т.п.), видит у AMP активные алерты уже отгоревшими.

`resolve_timeout` у AMP распарсен и получает дефолт (`go-app/internal/infrastructure/routing/global.go:22`, `Defaults()` на `:118-120`), но до ingest-пути не доходит: парсеры кладут `EndsAt = nil`, если поле не пришло (`go-app/internal/infrastructure/webhook/parser.go:133-136`, `prometheus_parser.go:288-291`). karma это поле не читает — баг от неё не зависит.

## Goals

- [ ] POST алерта без `endsAt` ⇒ AMP хранит и отдаёт `endsAt = startsAt + global.resolve_timeout`. _(Уточнено на `/research`: upstream считает от времени приёма, `receivedAt + resolve_timeout`; см. `research.md` F2 и `Spec.md`.)_
- [ ] Явно переданный `endsAt` не перетирается.
- [ ] Значение `resolve_timeout` берётся из активного конфига (дефолт `5m`), а не захардкожено; поведение после `/-/reload` с новым значением — определить в `/spec`.
- [ ] Выяснить на `/research`/`/spec`, какие ingest-пути затронуты (`/api/v2/alerts`, webhook `/webhook`, Prometheus-формат) и где правильно применять таймаут: на парсинге, в ingest-сервисе или на отдаче API.

## Constraints

- Узкий runtime-фикс парности; не трогать семантику авто-резолва/жизненного цикла алертов сверх того, что нужно для `endsAt` (если выяснится, что без авто-резолва фикс неполон — вынести в отдельную задачу, а не расширять эту).
- Не менять webhook payload shape для receivers, кроме того, что `endsAt` становится заполненным (сверить с upstream: он тоже отдаёт `endsAt` в webhook).
- Хранилище: если `endsAt` пишется в Postgres/SQLite — без миграций схемы (поле уже есть).
- Не расширять в `PARITY-GATE-DOES-NOT-GATE` и `QUALITY-GATES-DIRTIES-TREE`, хотя они рядом.
- Оценка ~0.5d; если выйдет больше — нарезать.

## Success Criteria (Definition of Done)

- [ ] Тест: POST без `endsAt` ⇒ `GET /api/v2/alerts` отдаёт `endsAt == receivedAt + 5m` при дефолтном конфиге.
- [ ] Тест: кастомный `global.resolve_timeout` учитывается.
- [ ] Тест: явный `endsAt` сохраняется как есть.
- [ ] Повторный POST того же алерта без `endsAt` продлевает окно (поведение upstream) — либо осознанное отклонение зафиксировано в `/spec`.
- [ ] `docs/ALERTMANAGER_COMPATIBILITY.md` / CHANGELOG `[Unreleased]` отражают изменение.
- [ ] `go vet` + `go test ./...` зелёные (с учётом известного флейка `PUBLISHING-WARMUP-TEST-FLAKY`); `git diff --check` чистый.
