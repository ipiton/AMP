---
id: UPSTREAM-CORE-POC-GATE
slug: upstream-core-poc-gate
stream: Architecture / ADR-019
type: feature
artifact: research-pack
status: done
created_at: 2026-10-24
updated_at: 2026-10-24
---

# Research Pack - контрактные сьюты и amtool на upstream-ядре, обновляемость

## 0) TL;DR

- Solving: прогнать контрактные сьюты AMP и amtool на upstream-ядре, классифицировать расхождения, заложить обновление upstream как рутину.
- Found: шов `registerRoutes` подменяется одной переменной под build-тегом `upstreamcore` — сьюты бегут на upstream-ядре без правки тел, прогон 1.2s. После нормализации `Content-Type` (единственная системная причина) — 146 pass / 50 листовых fail; **ни одного падения, которое показывает потерю данных или неверную семантику доставки**: AMP-only поверхность, формат ошибок, тесты, закрепившие поведение своего ядра, и пара пробелов wiring.
- Chose: baseline известных расхождений (файл) + раннер, который валит гейт на любом новом падении и на устаревшей записи; версия upstream — только `go.mod`; тест дрейфа порта через `debug.ReadBuildInfo`; скрипт обновления с карантином всех сдвинутых модулей и диффом портированных файлов.
- Risks: baseline может стать свалкой — каждая запись несёт класс и причину, устаревшие записи валят гейт.
- Next step: -> spec

## 1) Questions

1. Можно ли гонять сьюты на upstream-ядре без переписывания?
2. Какие расхождения и какого рода?
3. Как сделать ожидаемые падения явными, не правя тела тестов?
4. Как гонять amtool на upstream-ядре?
5. Что нужно, чтобы обновление upstream было рутиной?

## 2) Findings

- **Шов:** сьюты получают сервер только через `registerRoutes(mux)` (`go-app/cmd/server/futureparity_compat.go`), конфиг — `AMP_CONFIG_FILE`, и он уже в формате `alertmanager.yml` (`validConfigPayload`). Прототип: `registerRoutes` зовёт `registerRoutesImpl`, файл `futureparity && upstreamcore` подменяет её в `init()` на `upstreamcore.New(...)` + `mux.Handle("/", core.Handler())`. `futureparity_compat_test.go` тестирует харнесс своего ядра (`profile` в AMP-конфиге) — исключён тегом `!upstreamcore`. Прогон пакета: 1.2s (measured).
- **Content-Type (системная причина):** сьюты шлют POST без `Content-Type`; AMP принимает, upstream (go-swagger) отвечает 415 — 31 падение одной причины. Prometheus и amtool всегда шлют `application/json`. Харнесс проставляет заголовок, если его нет; само расхождение — запись в таблице.
- **Расхождения после нормализации (50 листовых), measured, классы:**
  - **(a) AMP-only поверхность → срез 6 или удаление (18):** `/health`, `/healthz`, `/ready`, `/readyz`, `/`, `/dashboard*`, `/static/*`, `/api/v2/inhibitions`, `/api/v1/alerts` (upstream — 410 Gone), `?status=resolved`.
  - **(b) тест закрепил поведение своего ядра, upstream — эталон (≈28):** коды и тела ошибок (422 вместо 400 у go-swagger-валидации, ошибка — JSON-строка, а не объект; 404 вместо 400 у update неизвестного id), тело `/-/reload` пустое, а не `OK`, `Method Not Allowed\n` у `POST /-/healthy`, невалидный `active=`/`filter=` у groups, `generatorURL` валидируется (422); старт с битым конфигом (upstream не стартует, reload тестируется отдельно); update silence со `startsAt` в прошлом создаёт новый ID и экспайрит старый (`silence.canUpdate`); фильтр silences не матчит silence с `!=` (тест в комментарии ссылается на «upstream semantics», настоящий upstream даёт 2, а не 3); **группы появляются после ingest асинхронно** — dispatcher получает алерты через подписку на провайдер, тест читает сразу (15/20 запусков падают; через ~1ms группы есть, проба `zz_probe`).
  - **(c) пробел нашего wiring → срез 5 (2):** `versionInfo.version` пустая — в бинаре нужен `-ldflags` для `prometheus/common/version`.
  - **(d) решение владельца (1):** upstream отдаёт `/debug/pprof/*` (через `http.DefaultServeMux`), AMP его намеренно убрал (`debug_removed`). Оставить как upstream или вырезать в нашем wiring.
- **Ожидаемые падения без правки тел:** `go test -skip` по списку неудобен (регэксп делится по `/` на уровни, альтернатива между уровнями ломается). `go test -json` + сравнение множества листовых падений с baseline-файлом даёт то же и строже: новое падение — красный, запись, которая прошла, — красный («устарела — удалить»). Асинхронные тесты групп — флаг «может пройти». `jq` есть локально и на `ubuntu-latest`.
- **amtool:** `go run github.com/prometheus/alertmanager/cmd/amtool@<версия>` собирается по `go.mod` upstream (measured) — без docker, версия = версия ядра из нашего `go.mod`. Обходит и локальную проблему Docker Desktop с bind-mount из `~/Documents`.
- **Одна точка версии:** `go.mod` require. Сейчас amtool-образ в `scripts/release-gate.sh:272` — `v0.34.0`, `go.mod` — v0.34.1: уже разошлись. Тег образа выводится из `go list -m`.
- **Дрейф порта:** `debug.ReadBuildInfo()` в тест-бинаре отдаёт версию `github.com/prometheus/alertmanager` (measured: `v0.34.1`). Константа «портировано с» в `upstreamcore` + тест сравнения — бамп без сверки порта красный, без новых зависимостей.
- **Карантин при обновлении:** `go get` сдвигает не только upstream, но и транзитивку (срез 1a: ~15 модулей). Проверять возраст надо у каждого изменённого `require` — `go list -m -json mod@ver` даёт `Time`.
- **CI:** `ci.yml` job `gate` запускает `scripts/release-gate.sh` как есть — новый шаг гейта сразу в CI.

## 3) Options

`Generation:` single-pass — шов и способ amtool определены фактами (прототип и сборка); выбор только у механизма ожидаемых падений.

### Option A - пометки в телах тестов (`skipOnUpstreamCore(t)`)

- **Pros:** видно в самом тесте.
- **Cons:** правка ~50 мест в двух сьютах, включая замыкания `t.Run`; не ловит «расхождение исчезло».
- **Cost/Risk:** medium.

### Option B - baseline-файл + раннер на `go test -json`

- **Pros:** тела тестов не трогаются; строгий в обе стороны (новое падение и исчезнувшее); при обновлении upstream изменение поведения видно диффом baseline.
- **Cons:** отдельный скрипт; имена тестов в файле — переименование теста требует правки baseline (раннер это и покажет).
- **Cost/Risk:** low.

## 4) Decision

- **Chosen:** Option B.
- **Why:** строгость в обе стороны и есть механизм обновления — при бампе upstream разница в поведении проявляется как изменение baseline, которое ревьюится.

## 5) Spec Inputs

- API/contracts: контракты `cmd/server` не меняются; харнесс — только под тегами `futureparity && upstreamcore`.
- Data model/migrations: не применимо.
- Rollout/rollback: шаг `upstream-core` в `release-gate.sh` → CI; откат — удалить шаг и файлы.
- Observability: не применимо.
- Security/ownership/RBAC: `/debug/pprof` — вопрос владельцу (класс d); карантин в скрипте обновления.

## 6) References

- `go-app/cmd/server/futureparity_compat.go` (шов), `main_phase0_contract_test.go`, `main_upstream_parity_regression_test.go`.
- Upstream v0.34.1: `api/v2/api.go` (ошибки, go-swagger), `dispatch/dispatch.go:183` (подписка dispatcher), `silence/silence.go` (`canUpdate`), `httpserver/httpserver.go` (`/debug` на `DefaultServeMux`).
- `scripts/release-gate.sh:271` (amtool-шаг), `.github/workflows/ci.yml` (job `gate`).
- `tasks/archive/UPSTREAM-CORE-POC-WIRING/`.
