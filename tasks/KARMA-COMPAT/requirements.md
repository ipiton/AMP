# Requirements: KARMA-COMPAT

## Context

`prymitive/karma` — read-only дашборд для Alertmanager (Go + React, Apache-2.0, активный проект). У AMP собственный UI слабый (`cmd/server/legacy_dashboard.go`, 166 строк: плоские списки на 25 записей, без фильтрации, группировки и управления сайленсами), поэтому karma закрывает дыру почти бесплатно — если AMP для неё выглядит как настоящий Alertmanager.

Разбор 2026-09-23 (BACKLOG, секция «UI и экосистема — идеи из karma») показал: протокольно AMP уже совместим. Проверено на живом lite-инстансе (`:19093`, 2 алерта + silence) против того, что karma реально запрашивает (её клиент `internal/mapper/v017/api.go`):

- `GET /api/v2/alerts/groups` совпадает поле в поле: `labels`, `receiver.name`, `alerts[].labels|annotations|startsAt|fingerprint|generatorURL|status.state|silencedBy|inhibitedBy`;
- заглушенный алерт отдаётся как `suppressed` со ссылкой на silence ID — karma рисует его как silenced;
- `GET/POST /api/v2/silences`, `DELETE /api/v2/silence/{id}`, ответ `{"silenceID": "..."}` — как upstream.

Единственное расхождение: **karma определяет версию Alertmanager не из `/api/v2/status`, а разбирая `/metrics`** — `internal/verprobe/verprobe.go` ищет `alertmanager_build_info{version=...}`. У AMP все метрики с префиксом `alert_history_*`, такой метрики нет вообще. karma не падает (пустая версия → `latestIfEmpty` → `999.0` → берётся самый свежий маппер), но на каждом цикле опроса пишет `Error while discovering version`, и любой другой инструмент экосистемы, который пробует версию тем же способом, ошибётся.

Побочная ценность: karma в smoke-стенде — дешёвый детектор регрессий API-парности. Если UI показал алерты и сайленсы, значит контракт `/api/v2/*` реально соблюдён, а не только «покрыт юнит-тестами».

## Goals

- [ ] Экспортировать `alertmanager_build_info` с лейблами `version`/`revision`/`branch`/`goversion` из `internal/buildinfo`, чтобы karma и прочие потребители определяли версию штатным способом.
- [ ] `version` в этой метрике — валидный semver, проходящий констрейнт karma `>=0.22.0` (сейчас `buildinfo.Version` по умолчанию `"dev"`, что semver не является).
- [ ] Добавить karma в smoke-стенд как проверку API-парности.
- [ ] Задокументировать karma как поддерживаемый UI: раздел в `docs/ALERTMANAGER_COMPATIBILITY.md` + рабочий пример подключения.

## Constraints

- Только additive-изменения: существующие метрики `alert_history_*` не переименовывать и не трогать (это отдельный техдолг про смешанные префиксы).
- Не строить свой UI и не тянуть код karma в репозиторий (Apache-2.0 в AGPL втягивается, но берём идеи, а не файлы).
- Не расширять задачу в `PARITY-RESOLVE-TIMEOUT-ENDSAT` (отдельная задача в очереди, karma это поле не читает).
- Не расширять задачу в аутентификацию и ack-as-silence — это `PROD-AUTH` и `ACK-AS-SILENCE`.
- 🔴 karma-образ с `ghcr.io/prymitive/karma` в этой среде не тянется (`docker pull` → `denied`). Пока это не решено, сквозная проверка «karma отрисовала алерты» недостижима, и smoke-шаг нужно либо делать опциональным (skip при недоступном образе), либо проверять версию отдельным контрактным тестом на `/metrics`. Решение — в `/spec`.
- Версия в метрике зависит от `LDFLAGS_VERSION` в `go-app/Makefile`, а дефолт `"dev"` приходит из `internal/buildinfo/buildinfo.go:10` — менять дефолт осторожно, он же попадает в `/api/v2/status`.

## Success Criteria (Definition of Done)

- [ ] `GET /metrics` отдаёт `alertmanager_build_info{version="…",revision="…",branch="…",goversion="…"} 1`.
- [ ] `internal/verprobe`-совместимость подтверждена тестом: экспортированный текст метрик парсится и из него извлекается версия, проходящая `>=0.22.0`.
- [ ] Решено и зафиксировано, что именно попадает в `version` при нерелизной сборке (dev-сборка не должна выдавать себя за релиз, но и не должна ломать пробу).
- [ ] karma подключается к AMP по документированному примеру; если образ недоступен в CI/локально — шаг помечен как опциональный и это явно описано, а не замаскировано.
- [ ] `docs/ALERTMANAGER_COMPATIBILITY.md` содержит раздел про karma: что работает, что нет (агрегация нескольких инстансов неприменима), как подключить.
- [ ] `go vet` + `go test ./...` зелёные; `git diff --check` чистый.
- [ ] `NEXT.md` отражает задачу в WIP, `CHANGELOG.md` `[Unreleased]` пополнен.

## Next Step

`/research` — обязателен по Research Policy (`WORKFLOW.md`): внешняя интеграция плюс минимум два варианта решения по содержимому `version` (эмулировать upstream-версию Alertmanager против отдавать собственную semver-версию AMP). Развилка влияет на контракт и на то, как AMP представляется экосистеме, поэтому решать её в `/spec` без разбора нельзя.
