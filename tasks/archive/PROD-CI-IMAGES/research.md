# Research: PROD-CI-IMAGES

Дата: 2026-09-28. Триггеры research по WORKFLOW: внешняя интеграция (GitHub Actions, GHCR), несколько вариантов решения, риск «сломать прод» (образы, на которые ссылается чарт).

Вопрос: что нужно, чтобы на каждый PR автоматически гонялись гейты, а на тег публиковались образы `amp` и `amp-config-reloader`, на которые чарт ссылается по умолчанию. Какие предсуществующие дефекты всплывут в первый же прогон.

## 1. Исходное состояние

| Что | Факт |
|---|---|
| CI | `.github/` нет и никогда не было |
| Репозиторий | `github.com/ipiton/AMP`, **public**, у нас `ADMIN`. `main` **не защищён** (branch protection 404). Actions включены, `allowed_actions: all`, `sha_pinning_required: false` |
| Образы в реестрах | `ghcr.io/ipiton/amp:0.0.1`, `ghcr.io/ipiton/amp:latest`, `docker.io/ipiton/amp-llm:1.0.0` — **не существуют** (`docker manifest inspect`) |
| `values.yaml` | `image: ghcr.io/ipiton/amp:"0.0.1"`; `configReloader.image: repository: "", tag: ""` |
| `values-production.yaml` | `image: ipiton/amp-llm:"1.0.0"` (Docker Hub, чужое имя); `configReloader.image: registry.example.com/amp/config-reloader`, `tag: ""` |
| Шаблон | `deployment.yaml:65,237` — `tag \| default .Chart.AppVersion` (`appVersion: "0.0.1"`) для обоих образов |
| Сборка | `Dockerfile` (сервер: `golang:1.26-alpine` → `alpine:3.19`, buildinfo через `-ldflags -X`), `Dockerfile.config-reloader` (→ `distroless/static-debian12:nonroot`). `.dockerignore` нет |
| Go | `go.mod`: `go 1.26.0`, без `toolchain`. Последние патчи: `go1.26.8`, `go1.27.1` (локально — 1.27.1) |
| Гейт | `scripts/release-gate.sh` — build, golangci-lint, `go test ./...`, futureparity, `-race` (4 пакета), helm dev/prod, helm-rbac, amtool-compat (docker, иначе SKIP). Bash 3.2-совместим — на Linux-runner пойдёт |
| Lint | `go-app/.golangci.yml` `version: "2"`; локально golangci-lint 2.13.2 |
| Helm | локально v4.3.0 |

Вывод: ни одного опубликованного образа — ни дефолтный, ни production-инсталл сейчас не стартуют (`ImagePullBackOff`), независимо от CI.

## 2. Helm на чистом checkout (`PROD-HELM-CLEAN-CHECKOUT`)

`helm/amp/charts/` в `.gitignore:56`, `Chart.lock` в git есть. `release-gate.sh` уже делает `helm dependency build` перед helm-шагами — но молча (`>/dev/null 2>&1`, только WARN в лог).

Проверено на `git archive HEAD helm/amp` в пустом каталоге с изолированными `HELM_*_HOME` (как на свежем runner'е):

1. `helm template` → `missing in charts/ directory: valkey`.
2. `helm dependency build` → `Error: no repository definition for https://groundhog2k.github.io/helm-charts. Please add the missing repos via 'helm repo add'`.
3. После `helm repo add groundhog2k https://groundhog2k.github.io/helm-charts` → `dependency build` скачивает `valkey-2.1.3.tgz`, `helm template` — OK.

Итог: **на CI-runner'е шаги `helm-dev`, `helm-production`, `helm-rbac` гарантированно красные**, пока в гейт не добавлен `helm repo add`. Починка — 2–3 строки в `release-gate.sh` (добавить репозитории из `Chart.lock` и не глушить ошибку `dependency build`). Втягивать в срез дешевле, чем обходить в workflow: иначе гейт останется непереносимым на чистую машину, а это и есть суть `PROD-HELM-CLEAN-CHECKOUT`.

## 3. govulncheck — базовая линия

`govulncheck@v1.8.0` (2026-09-08, карантин пройден), локальный toolchain go1.27.1: **9 достижимых уязвимостей в 6 модулях** (+4 в импортируемых пакетах и 27 в модулях без достижимого вызова):

| ID | Модуль | Есть | Fixed in |
|---|---|---|---|
| GO-2026-6348 | `google.golang.org/grpc` | v1.77.0 | v1.83.1 |
| GO-2026-6061 | `google.golang.org/grpc` | v1.77.0 | v1.82.1 |
| GO-2026-5970 | `golang.org/x/text` | v0.31.0 | v0.39.0 |
| GO-2026-5506 | `go.opentelemetry.io/otel` | v1.39.0 | v1.41.0 |
| GO-2026-5426 | `go.opentelemetry.io/otel/sdk` | v1.39.0 | v1.43.0 |
| GO-2026-4394 | `go.opentelemetry.io/otel/sdk` | v1.39.0 | v1.40.0 |
| GO-2026-5026 | `golang.org/x/net` | v0.47.0 | v0.55.0 |
| GO-2026-4918 | `golang.org/x/net` | v0.47.0 | v0.53.0 |
| GO-2026-5004 | `github.com/jackc/pgx/v5` | v5.7.6 | v5.9.2 |

Stdlib-уязвимостей нет — но только потому, что скан шёл на go1.27.1. На CI с `go-version-file: go-app/go.mod` setup-go поставит **go1.26.0** (setup-go берёт `toolchain`, а при его отсутствии — `go`-директиву; `toolchain` в `go.mod` нет — сверить в логе первого прогона) — stdlib-находок почти наверняка прибавится, и тем же go1.26.0 соберутся образы. Отдельно: сам govulncheck, собранный на 1.26, падает на toolchain 1.27 (так сломан локальный `~/go/bin/govulncheck`) — в CI запускать `go run golang.org/x/vuln/cmd/govulncheck@v1.8.0` тем же Go, что и сборку.

Итог: **блокирующий govulncheck будет красным с первого прогона**. Апдейт зависимостей (grpc/otel/x/net/pgx — минорные, но grpc и otel тянут транзитивку) — это изменение Go-кода и `go.sum`, вне scope requirements.

## 4. Multi-arch сборка

Оба Dockerfile собирают Go внутри целевой платформы (`FROM golang:1.26-alpine` без `--platform=$BUILDPLATFORM`, без `TARGETOS/TARGETARCH`). С `docker buildx --platform linux/amd64,linux/arm64` arm64-стадия пойдёт под QEMU — компиляция Go в эмуляции в разы медленнее.

Варианты:

- **A. Кросс-компиляция в Dockerfile** — `FROM --platform=$BUILDPLATFORM golang:…`, `GOOS=$TARGETOS GOARCH=$TARGETARCH`, `CGO_ENABLED=0` уже стоит. QEMU нужен только для runtime-стадии (`apk add` в alpine) — секунды. Один job, одна multi-arch манифест-сборка. Правка ~3 строки на Dockerfile.
- **B. Нативные runner'ы** — `ubuntu-24.04` + `ubuntu-24.04-arm` (для public-репо бесплатны), сборка по digest + отдельный job `imagetools create` для манифеста. Без правки Dockerfile, но workflow втрое сложнее.
- **C. QEMU как есть** — ноль правок, медленно (оценочно десятки минут на тег).

Рекомендация — **A**: минимальный дифф, стандартный паттерн, быстро.

Попутно по Dockerfile (не блокирует CI, фиксирую для `/spec`):

- `alpine:3.19` — EOL (ноябрь 2025) ⇒ runtime без security-апдейтов; поднять до актуальной ветки.
- `golang:1.26-alpine` — плавающий тег ⇒ сборки невоспроизводимы; при этом не совпадает с тем, что поставит CI для тестов.
- `Dockerfile.config-reloader` принимает `VERSION/REVISION/BUILD_DATE`, но в `-ldflags` их не передаёт — мёртвые ARG.
- `.dockerignore` нет ⇒ в build context уходит весь репозиторий (включая `.git`, `.claude/worktrees`). Для `COPY go-app/` на результат не влияет, но раздувает контекст.

## 5. GitHub Actions: выбор и пины

Все кандидаты старше 7 дней (карантин из глобальных правил). SHA — коммит тега последнего релиза (`gh api repos/<a>/commits/<tag>`):

| Action | Тег | Дата | SHA |
|---|---|---|---|
| `actions/checkout` | v7.0.1 | 2026-07-20 | `3d3c42e5aac5ba805825da76410c181273ba90b1` |
| `actions/setup-go` | v7.0.0 | 2026-07-16 | `b7ad1dad31e06c5925ef5d2fc7ad053ef454303e` |
| `golangci/golangci-lint-action` | v9.3.0 | 2026-06-29 | `ba0d7d2ec06a0ea1cb5fa41b2e4a3ab91d21278a` |
| `azure/setup-helm` | v5.0.1 | 2026-06-23 | `9bc31f4ebc9c6b171d7bfbaa5d006ae7abdb4310` |
| `docker/setup-qemu-action` | v4.4.0 | 2026-09-15 | `99012661954931238ded8c8b007157a8430204e1` |
| `docker/setup-buildx-action` | v4.4.1 | 2026-09-16 | `f87e5991a6d7451dcb8d9637bfbc97413f497069` |
| `docker/login-action` | v4.6.0 | 2026-07-29 | `dbcb813823bdd20940b903addbd779551569679f` |
| `docker/metadata-action` | v6.2.0 | 2026-07-02 | `dc802804100637a589fabce1cb79ff13a1411302` |
| `docker/build-push-action` | v7.4.0 | 2026-09-15 | `c3c9e263c25d99ce0380d002d59b67737d91b0dc` |
| `rhysd/actionlint` (для локальной проверки) | v1.7.12 | 2026-03-30 | `914e7df21a07ef503a81201c76d2b11c789d3fca` |

`setup-qemu` и `build-push` вышли 13 дней назад — в пределах нормы, но на `/implement` сверить ещё раз.

Как гнать гейты:

- **Вариант 1 — один job `./scripts/release-gate.sh`.** Один источник правды: локальный гейт = CI. Минус — один required check, не видно, какой шаг упал, без grep по логу (есть summary-таблица в конце, она это частично закрывает). Нужны на PATH golangci-lint (ставится action'ом с пином версии) и helm; docker на `ubuntu-latest` есть ⇒ amtool-compat и testcontainers-тесты (`internal/database/...`, `repository/postgres_history_test.go`, `inhibition/integration_test.go`, `silencing/postgres_silence_repository_test.go`) реально выполнятся, а не SKIP, как было бы без docker.
- **Вариант 2 — матрица отдельных jobs** (build/lint/test/race/helm/…) в YAML. Параллельно и наглядно, но дублирует логику `release-gate.sh` ⇒ два гейта разъедутся (ровно так уже разъехались `make quality-gates` и `release-gate.sh`, см. `PARITY-GATE-DOES-NOT-GATE`).
- **Компромисс** — вызывать шаги release-gate по отдельности: `release-gate.sh --step <name>` (нужна правка скрипта). Можно отложить: начать с варианта 1, дробить по фактическому времени прогона.

Рекомендация — **вариант 1** плюс отдельные jobs только для того, чего в release-gate нет: `govulncheck`, `docker build` обоих образов (без push — проверка Dockerfile на PR), `actionlint`.

`go test -race` из BACKLOG: release-gate гоняет `-race` только на 4 пакетах, полный `-race ./...` — нет. Полный прогон дольше и может вскрыть гонки в остальных пакетах — отдельный неизвестный риск; предлагаю не расширять в этом срезе (записать follow-up).

## 6. HA e2e (`deploy/e2e-ha/run.sh`)

Docker Compose: redis + postgres + 2 реплики AMP, сборка образа из `Dockerfile`. Сценарий содержит фиксированные ожидания (`GROUP_WAIT_MARGIN`, `GROUP_INTERVAL`, `RECONCILE_MARGIN`), рестарт и kill реплик, ожидания `/healthz` до 90 s. Порты `18081/18082` — на runner'е свободны. Внешних зависимостей нет ⇒ на `ubuntu-latest` должен работать как есть.

Время прогона не измерено (оценка — единицы минут плюс сборка образа). Тайминговые сценарии на shared runner'е — кандидат во флейки. Варианты: на каждый PR (дольше, но ловит регресс до мержа) / push в `main` + `workflow_dispatch` (не блокирует PR) / по лейблу. Рекомендация для `/spec`: **отдельный job на каждый PR, но сначала не required**; сделать required после N зелёных прогонов.

## 7. Публикация в GHCR

- Реестр — `ghcr.io/ipiton/amp` и `ghcr.io/ipiton/amp-config-reloader`: совпадает с `values.yaml` и с `LABEL org.opencontainers.image.source` в `Dockerfile.config-reloader`. `ipiton/amp-llm` (Docker Hub) и `registry.example.com/...` заменить. У серверного `Dockerfile` OCI-меток нет — `metadata-action` проставит их сам через `labels:`.
- Auth — `GITHUB_TOKEN` с `permissions: packages: write` только в release-job. PAT не нужен.
- 🔴 **Видимость пакета:** пакет GHCR, созданный первым push'ем, по умолчанию **private**, даже в публичном репозитории. Анонимный `docker pull` (и кластер без `imagePullSecrets`) получит 401/403, пока владелец не переключит видимость в настройках пакета на Public. Это ручной одноразовый шаг владельца — вписать в runbook релиза и в DoD.
- Теги (`metadata-action`): на `v1.2.3` → `1.2.3`, `1.2`, `sha-<short>`; `latest` — только на теге. `1` (major) — не выпускать до v1 (при `0.x` major-тег `0` вводит в заблуждение).
- Build-args сервера (`VERSION/REVISION/BRANCH/BUILD_DATE`) — из контекста тега, чтобы `/api/v2/status versionInfo` отдавал реальную версию.
- Связка с `.Chart.AppVersion`: шаблон по умолчанию берёт `appVersion` (`0.0.1`) — после первой публикации дефолт чарта всё ещё будет указывать на несуществующий тег, пока `PROD-RELEASE-V010` не выровняет `appVersion` = тег образа. Здесь — только правка `repository`; тег/версия — там.
- Supply chain на будущее (не в этом срезе): provenance/SBOM (`build-push-action` `provenance: true`, `sbom: true` — дешёвые флаги), cosign-подпись, Trivy-скан образа — записать follow-up.

## 8. Branch protection

`main` не защищён; у нас `ADMIN`. Включение required checks — изменение настроек репозитория (внешнее действие): делает владелец руками или через `gh api` с явного согласия, после того как job'ы хотя бы раз отработали (GitHub предлагает в required только уже виденные check'и). В задаче — зафиксировать список required checks в документации.

## 9. Предсуществующие дефекты, которые всплывут в CI

| Дефект | Затронет CI? | Предложение |
|---|---|---|
| `PUBLISHING-WARMUP-TEST-FLAKY` (BUGS) | да — `go test ./...` в release-gate, ~1/4 полных прогонов локально; на 2-vCPU runner'е вероятно чаще | не маскировать ретраем job'а. Либо починить (тест на синхронизацию, ~0.25d, отдельный коммит), либо `t.Skip` со ссылкой на BUGS. Решить на `/spec` |
| `PARITY-GATE-DOES-NOT-GATE` (BACKLOG) | нет — CI гоняет release-gate, а не `make test-upstream-parity`; futureparity-шаг в release-gate тег передаёт | вне scope |
| `QUALITY-GATES-DIRTIES-TREE` (BACKLOG) | нет — `make quality-gates` в CI не вызывается | вне scope |
| `PROD-HELM-CLEAN-CHECKOUT` | **да, гарантированно** (§2) | втянуть в срез |
| govulncheck, 9 находок | **да, гарантированно** (§3) | см. §10 |

## 10. Базовая линия release-gate

Локальный прогон `./scripts/release-gate.sh` на `main`@`41b8c28` (macOS arm64, go1.27.1, docker запущен), 2026-09-28: **PASS, 487 s**.

| Шаг | Статус | Время |
|---|---|---|
| build | PASS | 11 s |
| lint | PASS | 20 s |
| test | PASS | 87 s |
| futureparity | PASS | 20 s |
| race | PASS | **303 s** |
| helm-dev / helm-production / helm-rbac | PASS | ~1 s |
| amtool-compat | PASS | 22 s |

Флейк `PUBLISHING-WARMUP-TEST-FLAKY` в этот раз не проявился. Helm-шаги зелёные **только** потому, что локально уже лежит `helm/amp/charts/valkey-2.1.3.tgz` — на чистом runner'е они красные (§2).

Следствие для CI: `-race` — 62 % времени гейта. На hosted runner'е (4 vCPU для public-репо, x86 без кэша сборки) весь гейт оценочно 10–15 мин. Приемлемо для старта одним job'ом; если станет узким местом — первым выносить `race` в параллельный job (это и есть повод для `release-gate.sh --step`, §5). Кэш Go-модулей и build-кэша (`setup-go` `cache: true`, `cache-dependency-path: go-app/go.sum`) обязателен.

## 11. Рекомендация и влияние на `/spec`

Research **меняет scope** в две стороны:

**Втянуть в срез:**

1. `PROD-HELM-CLEAN-CHECKOUT` — `helm repo add` из `Chart.lock` в `release-gate.sh`, ошибки `dependency build` не глушить. Без этого CI красный на helm-шагах. ~0.1d; BACKLOG-запись закрывается вместе с задачей.
2. Кросс-компиляция в обоих Dockerfile (`$BUILDPLATFORM`/`$TARGETARCH`) — иначе multi-arch под QEMU.
3. Go toolchain: закрепить патч-версию (`toolchain go1.26.8` в `go.mod` или явный `go-version` в workflow + тот же тег `golang:1.26.8-alpine` в Dockerfile), чтобы тесты, govulncheck и образ собирались одним и тем же, свежим Go.

**Вынести в отдельный срез / follow-up:**

4. Обновление уязвимых зависимостей (9 находок) — `PROD-DEPS-VULN`, отдельная задача: трогает `go.mod`/`go.sum`, требует полного гейта. В этом срезе govulncheck — **отдельный job, не required**, пока `PROD-DEPS-VULN` не закрыт; затем сделать required. Альтернатива — сразу сделать его required и первым делом закрыть `PROD-DEPS-VULN` (CI красный до тех пор). Выбор — на `/spec`.
5. Полный `go test -race ./...`, provenance/SBOM/cosign/Trivy, `alpine:3.19` → актуальная версия, `.dockerignore`.
6. Включение branch protection — после первого зелёного прогона, отдельным согласованным действием.

**Предлагаемая нарезка** (оценка 1.5d держится, если п.4 вынесен):

- **Срез 1 — CI на PR:** `ci.yml` (release-gate, govulncheck non-required, docker build без push, actionlint) + `PROD-HELM-CLEAN-CHECKOUT` + решение по флейку. ~0.75d.
- **Срез 2 — публикация:** кросс-компиляция в Dockerfile, `release.yml` на `v*` (GHCR, multi-arch, semver+sha), правка `repository` в `values*.yaml`, runbook (включая перевод пакетов в Public). ~0.5d.
- **e2e-ha** — отдельный non-required job; в срез 1, если время прогона приемлемое, иначе в срез 2.

Проверка release-workflow без выпуска релиза: `workflow_dispatch` с `push: false`, либо тег-«пробник» `v0.0.3-rc.0` (публикует настоящий образ ⇒ требует явного согласия). Решить на `/spec`.

## 12. Риски

- Первый прогон CI на runner'е отличается от локального (Linux vs macOS, 2–4 vCPU, docker внутри runner'а для testcontainers) — возможны новые флейки, которых локально не видно.
- Пакеты GHCR private по умолчанию — «опубликовали, а `docker pull` не работает».
- Публикация образа и тег `v*` — внешние необратимые действия; первый реальный выпуск делать только с явного согласия (или отложить до `PROD-RELEASE-V010`).
- Required checks, включённые до стабилизации флейков, заблокируют все PR.
