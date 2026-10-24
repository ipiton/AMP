---
id: UPSTREAM-CORE-POC-GATE
slug: upstream-core-poc-gate
stream: Architecture / ADR-019
type: feature
status: approved
created_at: 2026-10-24
updated_at: 2026-10-24
based_on:
  - requirements.md
  - research.md
---

# Specification: контрактные сьюты и amtool на upstream-ядре, обновляемость ядра

**Version:** 1.0
**Status:** Approved (self, PoC)

## Summary

Сьюты `main_phase0_contract_test.go` и `main_upstream_parity_regression_test.go` бегут на upstream-ядре через тот же шов `registerRoutes` под build-тегом `upstreamcore`; раннер сравнивает листовые падения с baseline известных расхождений и валит гейт на любом отклонении. amtool гоняется на `cmd/amp-upstream` версией из `go.mod`. Обновление upstream — скрипт: карантин всех сдвинутых модулей, `go get`, дифф портированных файлов; тест дрейфа порта не даёт поднять версию молча.

## Requirements Coverage

| Requirement / Criterion | Covered by |
|---|---|
| Сьюты на upstream-ядре без правки тел | `upstreamcore_compat.go`, `registerRoutesImpl` |
| Классификация падений | `divergences.md` + классы в baseline |
| Ожидаемые падения явно, остальное — гейт | baseline + `scripts/upstream-core-gate.sh` |
| amtool на `cmd/amp-upstream`, версия из `go.mod` | `upstream-core-gate.sh` § amtool |
| Одна точка версии | `release-gate.sh` amtool-образ из `go list -m` |
| Тест дрейфа порта | `internal/upstreamcore/port_test.go` |
| Скрипт и процедура обновления | `scripts/upstream-bump.sh`, `docs/UPSTREAM_UPGRADE.md` |
| Шаг release-gate / CI | `release-gate.sh` шаг `upstream-core` |
| Вердикт по ADR-019 | `divergences.md` § Verdict, `DONE.md` |

## Current State

- **Code:** шов `registerRoutes` строит своё ядро (`futureparity_compat.go`); `cmd/amp-upstream` и `internal/upstreamcore` — из среза 1a.
- **Tests:** сьюты зелёные только на своём ядре; на upstream — 146 pass / 50 fail после нормализации `Content-Type` (research).
- **Gate:** amtool-образ `v0.34.0` захардкожен в `release-gate.sh`, `go.mod` — v0.34.1.

## Design Premises

| Premise | Confirmed by | Class | If wrong |
|---|---|---|---|
| Сьюты получают сервер только через `registerRoutes` | прототип: все тесты бегут на upstream после подмены | measured | пришлось бы править тела |
| Группы upstream появляются асинхронно после ingest | `-count=20`: 15 fail; проба: через ~1ms группа есть | measured | расхождение было бы потерей данных (класс c) |
| amtool собирается из модуля upstream без нашего `go.mod` | `go run .../cmd/amtool@v0.34.1 --version` | measured | нужен был бы docker-образ |
| Тест-бинарь видит версию зависимости | `debug.ReadBuildInfo` → `v0.34.1` | measured | нужен парсер `go.mod` |
| `go list -m -json mod@ver` отдаёт время публикации | `Time` в ответе proxy | code-read | карантин через `.info` proxy |

## Target Design

**Харнесс.** `futureparity_compat.go`: `registerRoutes` → `registerRoutesImpl(mux)`, по умолчанию `registerOwnCoreRoutes` (прежнее тело). Новый `upstreamcore_compat.go` (`futureparity && upstreamcore`): в `init()` подменяет реализацию на `registerUpstreamCoreRoutes` — `compat.InitFromFlags` один раз, `upstreamcore.New` с конфигом из `resolveRuntimeConfigPath()`, временным `DataDir`, своим `prometheus.Registry`; ошибка `New` → `registerFutureParityBootstrapFailure`. Обёртка ставит `Content-Type: application/json` на POST без заголовка (расхождение записано). Ядра в тестах не останавливаются — у шва нет `t`; процесс тестов короткий, утечка горутин ограничена прогоном (записано как компромисс).

**Baseline.** `go-app/cmd/server/testdata/upstreamcore-divergences.tsv`: `<test path>\t<class a|b|c|d>\t<fails|flaky>\t<reason>`; `#` — комментарии. Полное описание и решение по каждой группе — `tasks/<slug>/divergences.md` (после finalize — архив; baseline ссылается на класс, не на файл задачи).

**Раннер** `scripts/upstream-core-gate.sh`:
1. `go test -tags futureparity,upstreamcore -count=1 -json ./cmd/server/` → листовые падения (тест упал и ни один его подтест не упал);
2. новое падение вне baseline → FAIL с выводом теста; запись `fails`, которая прошла, → FAIL «устарела — удалить или переклассифицировать»; `flaky` может и упасть, и пройти;
3. amtool: собрать `cmd/amp-upstream`, поднять на свободном порту с `testdata/upstreamcore-alertmanager.yml` и временным `--storage.path`, `go run github.com/prometheus/alertmanager/cmd/amtool@$VER` — `alert query`, `config show`, `silence add`, `silence query`; остановить SIGTERM, код 0.
`VER` = `go list -m -f '{{.Version}}' github.com/prometheus/alertmanager` из `go-app`.

**Одна точка версии.** `release-gate.sh` `step_amtool_compat`: `amtool_image="quay.io/prometheus/alertmanager:$(upstream version)"`; новый шаг `upstream-core` вызывает раннер. Других упоминаний версии в исполняемом коде нет (комментарии провенанса — не точки версии).

**Дрейф порта.** `internal/upstreamcore`: `const PortedFrom = "v0.34.1"` (в `doc.go` рядом с провенансом); `port_test.go` сравнивает с версией модуля из `debug.ReadBuildInfo()`; сообщение называет обе версии и команду `scripts/upstream-bump.sh --diff <old> <new>`.

**Скрипт обновления** `scripts/upstream-bump.sh <version> [--allow-fresh=<reason>]`:
1. текущая версия из `go list -m`; новая — время публикации `go list -m -json ...@<version>`; младше 7 дней — отказ (кроме `--allow-fresh` с причиной — исключение владельца, как go1.27.3);
2. `go get github.com/prometheus/alertmanager@<version>`, `go mod tidy`;
3. для каждого изменившегося `require` в `go.mod` — время публикации; младше 7 дней — список, отказ с откатом `go.mod`/`go.sum` (`git checkout`), если не `--allow-fresh`;
4. дифф портированных файлов (`app/{app,reloader,metrics,lifecycle,url}.go`) и `template/template.go` (порт `internal/business/templating`) между версиями из module cache — в файл, путь печатается;
5. печать следующих шагов: перенести изменения в порт, обновить `PortedFrom`, `scripts/upstream-core-gate.sh`, пересмотреть baseline, `govulncheck`, `release-gate.sh`.
`--diff <old> <new>` — только шаг 4.

**Процедура.** `docs/UPSTREAM_UPGRADE.md` (English, как `docs/CI.md`): когда обновляться, шаги, что значит каждое изменение baseline, исключения карантина.

## API Contracts

Не меняются. Харнесс и раннер — тестовая инфраструктура.

## Data Model / Migrations

Не применимо.

## Component Architecture

- `go-app/cmd/server/futureparity_compat.go` — `registerRoutesImpl`.
- `go-app/cmd/server/upstreamcore_compat.go` — новый, `futureparity && upstreamcore`.
- `go-app/cmd/server/futureparity_compat_test.go` — тег `futureparity && !upstreamcore`.
- `go-app/cmd/server/testdata/upstreamcore-divergences.tsv`, `testdata/upstreamcore-alertmanager.yml` — новые.
- `go-app/internal/upstreamcore/doc.go` (`PortedFrom`), `port_test.go` — новый.
- `scripts/upstream-core-gate.sh`, `scripts/upstream-bump.sh` — новые.
- `scripts/release-gate.sh` — версия amtool-образа из `go.mod`, шаг `upstream-core`.
- `docs/UPSTREAM_UPGRADE.md` — новый; `docs/CI.md` — строка про шаг.

## Security Design

- [x] Ownership — не применимо.
- [x] Input validation — не применимо (тестовая инфраструктура).
- [x] Sensitive data not logged — не применимо.
- [x] Rate limiting — не применимо.
- [x] Auth/RBAC — `/debug/pprof` на upstream-ядре — класс d, решение владельца до среза 5; `amp-upstream` не выкатывается.
- [x] Supply chain — карантин 7 дней в `upstream-bump.sh` на upstream и на всю сдвинутую транзитивку; amtool через `go run @<версия>` — зависимости из `go.mod` upstream этой версии.

## Invariants

- [ ] Без тега `upstreamcore` поведение шва и все futureparity-тесты прежние (235 ok).
- [ ] Гейт красный на любом падении вне baseline и на прошедшей записи `fails`.
- [ ] Версия upstream в исполняемом коде и скриптах берётся только из `go.mod`.
- [ ] `PortedFrom` ≠ версии в `go.mod` → красный тест.

## Edge Cases

1. Тест переименован → раннер: новое падение не найдено, запись устарела — оба сообщения, понятно, что править.
2. Пакет не компилируется под тегом → `go test` без событий тестов: раннер — FAIL с выводом сборки, а не «0 падений».
3. Порт занят → amp-upstream на `127.0.0.1:0` нельзя (amtool нужен адрес): свободный порт берётся заранее, гонка маловероятна, при ошибке старта — FAIL с логом.
4. `go run amtool@ver` без сети → FAIL с понятным сообщением (как и любой `go run` в гейте).
5. `upstream-bump.sh` на грязном `go.mod` → отказ до изменений.

## Impact Analysis

- **Affected modules:** тестовый шов `cmd/server`, `internal/upstreamcore` (константа + тест), скрипты, docs.
- **Breaking changes:** none.
- **New dependencies:** none (amtool — `go run`, не require).
- **Risks:** baseline как свалка — снят строгостью раннера и классом у каждой записи; флейки асинхронных групп — флаг `flaky`.

## Rollout / Rollback

- **Rollout:** шаг в `release-gate.sh` → CI job `gate`.
- **Rollback:** удалить шаг и новые файлы; `registerRoutesImpl` безвреден.
- **Feature flag:** build-тег `upstreamcore`.

## Observability

Не применимо.

## Deep Review

- **Mandatory triggers present:** diff > ~200 строк.
- **Discretionary triggers present:** вердикт по ADR-019 строится на классификации — независимая проверка классов.
- **Decision:** required.

## Open Questions

- [ ] `/debug/pprof` на upstream-ядре (класс d) — владельцу; ответ нужен к срезу 5.
- [ ] Вердикт по ADR-019 — владельцу по итогам.
