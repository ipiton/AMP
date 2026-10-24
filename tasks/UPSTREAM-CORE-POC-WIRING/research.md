---
id: UPSTREAM-CORE-POC-WIRING
slug: upstream-core-poc-wiring
stream: Architecture / ADR-019
type: feature
artifact: research-pack
status: done
created_at: 2026-10-24
updated_at: 2026-10-24
---

# Research Pack - ядро upstream Alertmanager в бинаре AMP

## 0) TL;DR

- Solving: как собрать ядро upstream Alertmanager из пакетов в бинаре AMP так, чтобы срезы 2, 3, 6 могли встраиваться.
- Found: в v0.34 upstream вынес весь wiring в пакет `app` (`app.New/Start/Stop/Run`) «для встраивания», но **из модуля он не собирается**: `app` импортирует `ui`, а `ui/web.go` встраивает `//go:embed app/dist` — собранного фронтенда в zip модуля нет (`pattern app/dist: no matching files found`). К тому же `app.Options` закрыт: ни `AlertStoreCallback`, ни своих стадий, ни своих маршрутов.
- Chose: свой wiring в `internal/upstreamcore` — порт `app/app.go` (`setup`) и `app/reloader.go` без `ui` и без кластера; HTTP-сервер и сигналы — в `cmd/amp-upstream`.
- Risks: порт ~400 строк upstream — постоянная плата на каждом миноре (ADR-019 её уже учёл, 0.5–1d); граф зависимостей вырос (AWS SDK для `sns`, memberlist, go-openapi).
- Next step: -> spec

## 1) Questions

1. Какую версию upstream закреплять и проходит ли она карантин?
2. Совместим ли граф зависимостей upstream с `go.mod` AMP, что сдвигается?
3. Встраивать через `app` или собирать из пакетов?
4. Где в v0.34.1 точки встраивания для срезов 2/3/6?

## 2) Findings

- **Версия:** `v0.34.1` — 2026-09-17 (37 дней, карантин пройден; `proxy.golang.org/.../@v/v0.34.1.info`). amtool-гейт уже на `quay.io/prometheus/alertmanager:v0.34.0` — та же минорная ветка. Шаблоны AMP портированы с v0.34.0 (`internal/business/templating/template.go:2`).
- **Зависимости (`go mod tidy`):** +79 строк indirect в `go.mod`. Сдвиги уже используемых AMP модулей (MVS до требований upstream): `prometheus/common` 0.66.1→0.70.1, `client_golang` 1.23.2→1.24.1, `procfs` 0.16.1→0.21.1, `otelhttp` 0.61→0.69, `klauspost/compress` 1.18.0→1.19.1, `fsnotify` 1.9.0→1.10.1, `go-openapi/*` (swag 0.22→0.28, jsonpointer/jsonreference →1.0.0), `mapstructure` 2.4→2.5, `x/time` 0.14→0.15, `yaml/v2` 2.4.2→2.4.4. Новые крупные: `aws-sdk-go-v2` (+`sns`, `sts`), `hashicorp/memberlist`, `go-openapi/runtime|spec|validate|strfmt`, `exporter-toolkit`, `miekg/dns`, `prometheus/sigv4`. Все версии взяты из `go.mod` upstream v0.34.1 и опубликованы до 2026-09-17 — карантин пройден.
- **Попутно:** `go.mod` на `main` уже не был tidy — `go mod tidy` без upstream тоже убирает `mattn/go-sqlite3`, `spf13/cobra`, `inconshreveable/mousetrap` и переводит `docker/docker`, `grpc`, `client_model` в direct. Эта уборка входит в дифф.
- **Сборка/тесты:** с upstream в `go.mod` весь модуль собирается (`go build ./...`, `-tags futureparity`), `go test ./...` — 54 пакета ok, `go test -tags futureparity ./cmd/server/...` — 235 тестов ok. Сдвиг `prometheus/common` AMP не сломал.
- **`app` непригоден:** `ui` импортирует только `app/app.go` (grep по модулю); `//go:embed app/dist` при сборке из proxy падает. Обход (свой `replace` с собранным UI, форк) — это форк, отклонённый ADR-019.
- **Точки встраивания (code-read, v0.34.1):**
  - `mem.NewAlerts(ctx, gc, perAlertNameLimit, alertCallback AlertStoreCallback, ...)` (`provider/mem/mem.go:118`). **Upstream передаёт сюда `silencer`** (`app/app.go:362-366`) — `Silencer` и есть `AlertStoreCallback`. Хук среза 2 должен компоноваться с ним (сначала silencer, потом AMP), а не заменять его.
  - `notify.PipelineBuilder.New(...)` возвращает `RoutingStage` = `map[receiver]Stage` (`notify/notify.go:163`) — своя стадия среза 2 оборачивает значения карты; свой `notify.Integration` среза 3 добавляется в `receivers` перед `New`.
  - `api.API.Register(router, prefix) *http.ServeMux` (`api/api.go:169`) — AMP-маршруты среза 6 вешаются на тот же `route.Router`/mux.
  - Всё это доступно только при собственном wiring.
- **Глобальное состояние:** `matcher/compat` держит парсер матчеров в глобалах; upstream `main` вызывает `compat.InitFromFlags(logger, ff)` — без вызова парсер «classic», с `NoopFlags` — «fallback» (дефолт upstream). `httpserver.Register` вешает `/metrics` на `promhttp.Handler()` (default gatherer) и `/debug/*` на `http.DefaultServeMux`.
- **Unknowns:** размер бинаря и `govulncheck` — после реализации (нужен импорт, иначе код недостижим). Сняты на implement (measured, darwin/arm64, go1.27.3, 2026-10-24):
  - размер: `amp-upstream` — 37.9 MiB (25.2 MiB с `-ldflags="-s -w"`), `cmd/server` — 76.9 MiB (51.7 MiB). Upstream-ядро вдвое меньше бинаря AMP целиком; после среза 5 (удаление своего ядра) итоговый бинарь — порядка суммы AMP-уникального кода и этих ~25 MiB;
  - `govulncheck@v1.8.0` (как в CI): upstream принёс достижимую **GO-2026-6505** — `otlptracehttp` v1.44.0 из требований upstream, путь через `tracing.Manager.ApplyConfig`. Поднят до v1.45.0 (2026-08-03, карантин пройден; остальной OTel в AMP уже на v1.45.0). После — 0 достижимых; модульная GO-2026-5932 (`x/crypto/openpgp`, без фикса) была и до задачи. Локальный `~/go/bin/govulncheck` v1.7.0 собран go1.26 и не разбирает исходники go1.27 — использовать `go run ...@v1.8.0`.

## 3) Options

`Generation:` single-pass — вариант `app` отпал по факту сборки, а не по оценке; выбор между оставшимися не дорогой (оба обратимы удалением нового пакета).

### Option A - `app.New/Start/Stop` как есть

- **Pros:** ~50 строк, ноль порта, upstream сам поддерживает wiring.
- **Cons:** не собирается из модуля (`ui` + `go:embed app/dist`); даже собранный — без точек встраивания, срезы 2/3/6 всё равно потребуют свой wiring.
- **Cost/Risk:** блокер.

### Option B - свой wiring из пакетов (порт `app.setup` + `reloader`)

- **Pros:** собирается; все конструкторы вызываются из кода AMP — хуки срезов 2/3/6 встают без форка; без `ui`, кластер добавится срезом 4 по тому же образцу.
- **Cons:** ~400 строк порта, которые надо сверять с upstream на каждом миноре.
- **Cost/Risk:** medium.

### Option C - upstream-PR: точки расширения в `app.Options` и отделение `ui`

- **Pros:** без порта в долгую.
- **Cons:** вне контроля AMP, сроки неизвестны; PoC не может от него зависеть.
- **Cost/Risk:** high по срокам; можно делать параллельно — кандидат в BACKLOG после 1b.

## 4) Decision

- **Chosen:** Option B.
- **Why:**
  - A не собирается — это факт сборки, не оценка;
  - ADR-019 уже описывает wiring «по образцу `cmd/alertmanager`» и учитывает плату за минор;
  - только B даёт точки встраивания, ради которых эпик вообще выбран вместо прокси (ADR-018).
  - Кластер, tracing и event recorder: кластер — срез 4; tracing (`tracing_config` в `alertmanager.yml`) и event recorder (за feature flag, по умолчанию выключен) — переносятся как в upstream, это несколько строк и паритет конфига.

## 5) Spec Inputs

- API/contracts: новый бинарь отдаёт контракт upstream `api/v2` и `/-/*` без изменений; контракты `cmd/server` не трогаются.
- Data model/migrations: не применимо; снапшоты `silences`/`nflog` — в `--storage.path` нового бинаря.
- Rollout/rollback: не выкатывается; откат — удалить пакет, бинарь и строку `require`.
- Observability: метрики upstream (`alertmanager_*`) на `/metrics` нового бинаря; конфликт имён с `alertmanager_build_info` AMP невозможен — разные процессы.
- Security/ownership/RBAC: auth/TLS `--web.config.file` (exporter-toolkit) — вне PoC, бинарь слушает без auth; не выкатывается. Атрибуция Apache-2.0 в портированных файлах.

## 6) References

- Upstream: `github.com/prometheus/alertmanager@v0.34.1` — `app/app.go`, `app/reloader.go`, `app/metrics.go`, `app/url.go`, `app/lifecycle.go`, `cmd/alertmanager/main.go`, `ui/web.go:31`, `provider/mem/mem.go:118`, `notify/notify.go:163`, `api/api.go:169`, `httpserver/httpserver.go:28`, `matcher/compat/parse.go:59`.
- AMP: `go-app/cmd/server/futureparity_compat.go` (шов `registerRoutes`), `go-app/internal/business/templating/doc.go` (атрибуция), `scripts/release-gate.sh:271` (amtool-гейт).
- Решение: `docs/06-planning/DECISIONS.md` § ADR-019.
