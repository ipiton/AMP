---
id: PROD-DEPS-NET-HTTP2
slug: prod-deps-net-http2
stream: Security
type: bug
artifact: research-pack
status: complete
created_at: 2026-10-15
updated_at: 2026-10-15
---

# Research Pack - net/http и x/net (GO-2026-6603…6617)

## TL;DR

- На `go1.26.8` + `x/net` v0.58.0 `govulncheck` находит **12** достижимых уязвимостей, а не 5 из BACKLOG: 10 в stdlib (`net/http`, `net/textproto`, `crypto/tls`, `html/template`) и 5 из них же — ещё и в `x/net`. Все 12 закрываются парой `go1.26.9` + `x/net` v0.60.0, других фиксов не нужно.
- `x/net` v0.60.0 поднимает транзитивно `x/crypto`, `x/sys`, `x/term`, `x/text` (+ возможно `x/sync`/`x/tools`/`x/mod` через `x/text`); все кандидаты опубликованы до 2026-09-08 — вне карантина.
- `setup-go` v7 с `go-version-file` берёт `toolchain` из `go.mod` — так уже работает с `go1.26.8`.
- Образ `golang:1.26.9-alpine` перепушен 2026-10-10 (формально в карантине до 2026-10-17). **Решение владельца 2026-10-15:** исключение — перепуск официального образа не считается новым релизом, Go 1.26.9 от 2026-10-02 вне карантина. Делаем всё одним изменением после 2026-10-15 17:23 UTC.

## Questions

1. Какие advisory закрывает stdlib, какие — только `x/net`; хватает ли `go1.26.9` (релиз 2026-10-02 старше advisory 2026-10-08)?
2. Что тянет `x/net` v0.60.0 транзитивно и проходит ли это карантин?
3. Читает ли CI директиву `toolchain`?
4. Где ещё зашита версия Go, которую надо поднять вместе?
5. Готов ли образ `golang:1.26.9-alpine` по карантину?

## Findings

### 1. Advisory и фиксы (`measured`)

- Baseline: `GOTOOLCHAIN=go1.26.8 govulncheck@v1.8.0 ./...` → exit 3, 12 достижимых — `evidence/govulncheck-baseline-go1.26.8.txt`. Плюс 1 импортируемая и 1 модульная без вызова (ожидаемый GO-2026-5932 в `x/crypto/openpgp`, см. `docs/CI.md`).
- Фиксы по baseline: `net/http@go1.26.9` ×8, `html/template@go1.26.9` ×2, `net/textproto@go1.26.9`, `crypto/tls@go1.26.9`, `golang.org/x/net@v0.60.0` ×5.
- vuln DB (`vuln.go.dev/ID/GO-2026-66{10,11,12,13,17}.json`, published 2026-10-08T22:31Z): stdlib `fixed: 1.26.9` (и `1.27.2`), `x/net` `fixed: 0.60.0`; GO-2026-6613 — только stdlib. То есть фикс в `go1.26.9` вошёл под эмбарго, advisory опубликованы позже — противоречия нет.
- Сверх списка BACKLOG: GO-2026-6603, -6605, -6607, -6608, -6609 и две в `html/template` — закрываются тем же `go1.26.9`.

### 2. Транзитивка `x/net` v0.60.0 (proxy.golang.org)

| Модуль | Сейчас | Требует `x/net` v0.60.0 / цепочка | Опубликован |
|---|---|---|---|
| `golang.org/x/net` | v0.58.0 | v0.60.0 | 2026-10-08T17:22:58Z → можно с **2026-10-15 17:23 UTC** |
| `golang.org/x/crypto` | v0.56.0 (direct) | v0.57.0 | 2026-09-08 |
| `golang.org/x/sys` | v0.47.0 | v0.48.0 | 2026-08-31 |
| `golang.org/x/term` | v0.45.0 | v0.46.0 | 2026-09-08 |
| `golang.org/x/text` | v0.41.0 (direct) | v0.42.0 | 2026-09-08 |
| `golang.org/x/sync` | v0.22.0 | v0.23.0 (через `x/text` v0.42.0) | 2026-08-31 |
| `golang.org/x/tools` / `x/mod` | v0.48.0 / v0.38.0 | v0.49.0 / v0.41.0 (через `x/text`, если MVS их затронет) | 2026-08-13 / 2026-08-24 |

- `x/net` v0.60.0 и всё в цепочке — `go 1.26.0`, директиву `go` в `go-app/go.mod` поднимать не нужно.
- Итоговый набор зафиксирует `go get`; любой модуль сверх таблицы — проверить дату перед коммитом.
- `x/net` v0.61.0 (2026-10-09) не нужен: v0.60.0 закрывает все пять.

### 3. CI

- `.github/workflows/ci.yml:36-39,106-109` — `actions/setup-go@…# v7.0.0` с `go-version-file: go-app/go.mod`; комментарий прямо говорит «Picks the `toolchain` line». Текущий red run показывает `net/http@go1.26.8`, то есть CI уже использует `toolchain`, а не `go 1.26.0`.

### 4. Где зашита версия Go

`git grep 1.26.8` (без planning, архива задач и CHANGELOG):

- `go-app/go.mod:5` — `toolchain go1.26.8`;
- `Dockerfile:7`, `Dockerfile:38` (комментарий про Alpine-ветку), `Dockerfile.config-reloader:12`;
- `.github/workflows/ci.yml:36` — комментарий;
- `docs/CI.md:32` — § Go Version («Bump all three together»).

### 5. Образ builder'а

- Docker Hub: `golang:1.26.9-alpine` `last_updated` 2026-10-10T10:23Z — это index `sha256:3082400e…`; 2026-10-10 перепушены только riscv64 и index, манифесты amd64 (`sha256:397ecc648150…`) и arm64 (`sha256:dccc7a…`) — 2026-10-08T18:04Z, на 2026-10-16 старше 7 дней (уточнено по review F5); для сравнения `1.26.8-alpine` — 2026-09-21. Официальные теги перепушиваются при обновлении базового Alpine, дата пуша ≠ дата релиза Go.
- `1.26.9-alpine` сейчас указывает на Alpine 3.24 (есть `1.26.9-alpine3.24` с той же датой) — комментарий `Dockerfile:38` «Same Alpine branch» с `FROM alpine:3.24` остаётся верным.
- В официальных образах `GOTOOLCHAIN=local`: при рассинхроне образа и `toolchain` сборка молча шла бы на версии образа. Поэтому образ и `go.mod` поднимаются вместе.

## Options

Generation: single-agent (риск низкий, решение по карантину — за владельцем).

- **A. Всё сразу после 17:23 UTC 2026-10-15, образ по исключению.** Один дифф, CI и образы на одной версии. Отступление от правила карантина для образа.
- **B. Разделить:** `go.mod` + доки сегодня, Dockerfile — отдельной задачей после 2026-10-17. CI зеленеет так же быстро, но два дня образы собираются на уязвимом `go1.26.8`.
- **C. Ждать 2026-10-17.** Без отступлений, required check красный ещё ~2 дня.

## Decision

**A** — решение владельца 2026-10-15: перепуск официального образа `golang` не считается новым релизом для карантина; сам Go 1.26.9 (2026-10-02) карантин прошёл. Исключение — только для официального образа `golang` с уже прошедшим карантин patch-релизом Go; на модули Go оно не распространяется (`x/net` — строго после 17:23 UTC).

## Spec Inputs

- Порядок: `go get golang.org/x/net@v0.60.0` (не раньше 2026-10-15 17:23 UTC) → `toolchain go1.26.9` → сверить дельту `go.mod` с таблицей § 2.
- Поднять `1.26.8` → `1.26.9` во всех местах § 4 одним коммитом с `go.mod`.
- Проверка: `GOTOOLCHAIN=go1.26.9 govulncheck@v1.8.0 ./...` → 0 достижимых (остаётся только GO-2026-5932 без вызова) — в `evidence/`; `scripts/release-gate.sh`; `docker build` обоих Dockerfile с `go version` в builder'е.
- `CHANGELOG.md` `[Unreleased]` → Security: перечислить 12 ID и версии.
- Риск: patch-релизы, breaking API не ожидается; поведение HTTP/2-сервера меняется только в части фиксов.

## References

- `tasks/PROD-DEPS-NET-HTTP2/evidence/govulncheck-baseline-go1.26.8.txt`
- https://vuln.go.dev/ID/GO-2026-6610.json (и -6611, -6612, -6613, -6617)
- https://proxy.golang.org/golang.org/x/net/@v/v0.60.0.info, `.mod`; аналогично `x/crypto` v0.57.0, `x/sys` v0.48.0, `x/term` v0.46.0, `x/text` v0.42.0, `x/sync` v0.23.0, `x/tools` v0.49.0, `x/mod` v0.41.0
- https://hub.docker.com/v2/repositories/library/golang/tags/1.26.9-alpine
- `docs/CI.md` § Go Version; `.github/workflows/ci.yml`; `Dockerfile`; `Dockerfile.config-reloader`
- Прецеденты: `tasks/archive/PROD-DEPS-VULN/`, `tasks/archive/PROD-DEPS-OTEL-145/`
