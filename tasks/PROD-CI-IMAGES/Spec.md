# Spec: PROD-CI-IMAGES

Дата: 2026-09-28. Ветка `feature/prod-ci-images`. Источники: `requirements.md`, `research.md`. Пути — от корня репозитория.

## Проблема

У проекта нет CI: ни один гейт не запускается автоматически, `main` ничем не защищён. Опубликованных образов тоже нет. Чарт ссылается на три несуществующих образа: `ghcr.io/ipiton/amp:0.0.1`, `ipiton/amp-llm:1.0.0` (Docker Hub), `registry.example.com/amp/config-reloader`. Поэтому любой инсталл падает в `ImagePullBackOff`.

Research показал, что даже готовый гейт в CI не пройдёт «как есть»:

- на чистом checkout helm-шаги падают (`PROD-HELM-CLEAN-CHECKOUT`, research §2);
- govulncheck находит 9 достижимых уязвимостей (§3);
- Dockerfile под multi-arch компилирует Go под QEMU (§4).

## Цели

1. На каждый PR и push в `main` автоматически выполняется тот же гейт, что локально (`scripts/release-gate.sh`), и проверочная сборка обоих образов под `linux/amd64` + `linux/arm64`.
2. Push тега `v*` публикует `ghcr.io/ipiton/amp` и `ghcr.io/ipiton/amp-config-reloader` (multi-arch, теги semver + sha).
3. Гейт проходит на чистой машине без ручных шагов.
4. Тесты, govulncheck и образы собираются одним закреплённым патчем Go.
5. Чарт ссылается на публикуемые образы.
6. Описано, какие checks делать required и как выпустить образ.

## Не-цели

- **Обновление уязвимых зависимостей.** Трогает `go.mod`/`go.sum` и транзитивку grpc/otel, поэтому это отдельная задача `PROD-DEPS-VULN` (заводится в BACKLOG, следующая по порядку перед `PROD-RELEASE-V010`).
- **Выравнивание версий и первый релиз**: `appVersion`, тег `v0.1.0`, CHANGELOG-релиз, `image.tag` в values. Это `PROD-RELEASE-V010`. Здесь меняем только `repository`.
- **Реальная публикация образа.** В этой задаче тег `v*` не пушится. Первый push образов делается в `PROD-RELEASE-V010` или отдельным явным согласием владельца (D9).
- **Включение branch protection** — это внешнее действие в настройках репо. Делает владелец после первого зелёного прогона; здесь только документируется (D10).
- Полный `go test -race ./...` (сейчас `-race` на 4 пакетах), provenance/SBOM/cosign, Trivy — follow-up в BACKLOG.
- `PARITY-GATE-DOES-NOT-GATE`, `QUALITY-GATES-DIRTIES-TREE` — касаются `make`, а не `release-gate.sh`, поэтому CI они не затрагивают.
- Разбиение `release-gate.sh` на отдельные CI-jobs (`--step`). Возвращаемся, только если время прогона станет проблемой.

## Ключевые решения

### D1. Гейт в CI = `scripts/release-gate.sh` одним job'ом

Единственный источник правды: локальный гейт и CI не могут разъехаться, как уже разъехались `make quality-gates` и release-gate (research §5). Job ставит Go (D3), golangci-lint (action, `version: v2.13.2`, `install-only`), helm (`azure/setup-helm`, v4.3.x — как локально). Docker на `ubuntu-latest` уже есть, поэтому testcontainers-тесты и `amtool-compat` реально выполняются, а не уходят в SKIP. `timeout-minutes: 30`.

Ожидаемое время — 10–15 мин (локально 487 s, из них `-race` 303 s). Если окажется больше 20 мин — `race` выносим в параллельный job отдельным изменением, в этой задаче нет.

### D2. Helm на чистом checkout (`PROD-HELM-CLEAN-CHECKOUT` втягивается)

В `release-gate.sh` перед `helm dependency build`:

- добавить все `repository:` из `helm/amp/Chart.lock` через `helm repo add` с детерминированными именами (`amp-dep-<n>`), идемпотентно (`--force-update`);
- ошибку `dependency build` больше не глушить. Вводится отдельный шаг `helm-deps` в summary; его FAIL виден, а не только WARN в логе.

Решаем в скрипте, а не в workflow: иначе гейт остаётся непереносимым на чистую машину (суть BACKLOG-записи). Запись закрывается вместе с задачей.

### D3. Один патч Go везде: `go1.26.8`

- `go-app/go.mod`: добавить `toolchain go1.26.8`. Строка `go 1.26.0` не меняется, минимальная версия языка остаётся прежней.
- CI: `actions/setup-go` с `go-version-file: go-app/go.mod`. Берёт `toolchain`, поэтому получится 1.26.8; проверяется по логу первого прогона. Включён `cache-dependency-path: go-app/go.sum`.
- Оба Dockerfile: `golang:1.26.8-alpine` вместо плавающего `golang:1.26-alpine`.

Разработчики с Go 1.27 локально не затронуты: `toolchain` — нижняя граница, а не точная версия.

### D4. Кросс-компиляция в Dockerfile

В обоих Dockerfile:

- `FROM --platform=$BUILDPLATFORM golang:1.26.8-alpine AS builder`;
- `ARG TARGETOS TARGETARCH`;
- `GOOS=$TARGETOS GOARCH=$TARGETARCH` в `go build`.

`CGO_ENABLED=0` уже стоит. QEMU остаётся нужен только для runtime-стадии серверного образа (`apk add` на arm64), это секунды. Сборка одним `build-push-action` с `platforms: linux/amd64,linux/arm64` (research §4, вариант A).

Попутно в тех же файлах:

- серверный runtime: `alpine:3.19` (EOL) → `alpine:3.24` — та же ветка, на которой собран `golang:1.26.8-alpine` (`/etc/alpine-release` = 3.24.2, сверено на `/plan`);
- `Dockerfile.config-reloader`: `VERSION/REVISION/BUILD_DATE` передаются в OCI-метки через `metadata-action`, мёртвые `ARG` удаляются. Buildinfo сайдкару не нужен: у него нет `/api/v2/status`;
- `.dockerignore` в корне исключает `.git`, `.claude`, `tasks`, `docs`, `helm`, `deploy` (оба Dockerfile копируют только `go-app/`; `*_test.go` не исключаем — безвредны). Цель — не тащить в build context worktrees и `.git`, на содержимое образа это не влияет.

### D5. Два workflow, а не один с условием

- **`.github/workflows/ci.yml`** — `pull_request` + `push: branches: [main]`. `permissions: contents: read` на весь workflow. Jobs:

  | Job | Что делает | Required |
  |---|---|---|
  | `gate` | `./scripts/release-gate.sh` (D1) | да |
  | `images` | matrix `amp` / `config-reloader`: `build-push-action`, `push: false`, обе платформы, `cache-from/to: type=gha` | да (оба) |
  | `actionlint` | `rhysd/actionlint` по `.github/workflows/` | да |
  | `govulncheck` | `go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...` | **нет** (D7) |
  | `e2e-ha` | `./deploy/e2e-ha/run.sh`, `timeout-minutes: 20` | **нет** (D8) |

  `concurrency: group: ci-${{ github.ref }}`, `cancel-in-progress: true` — только для `pull_request`.

- **`.github/workflows/release.yml`** — `push: tags: ['v*']`. Job `publish` с `permissions: contents: read, packages: write`. Matrix по двум образам:
  - `setup-qemu` → `setup-buildx` → `login-action` (ghcr.io, `github.actor`, `GITHUB_TOKEN`) → `metadata-action` → `build-push-action`, `push: true`;
  - build-args для сервера: `VERSION` (из тега), `REVISION` (`github.sha`), `BRANCH` (`github.ref_name`), `BUILD_DATE` (`metadata-action` created).

Почему два файла: право `packages: write` нельзя выдать job'у условно. В общем workflow его получал бы и каждый PR из того же репо. Дублирование — ~15 строк шагов buildx; это меньшее зло.

### D6. Теги образов

`metadata-action`:

- `type=semver,pattern={{version}}`;
- `type=semver,pattern={{major}}.{{minor}}`;
- `type=sha,format=short` (→ `sha-<7>`);
- `latest` — только для тега без pre-release (`flavor: latest=auto`).

Major-тег (`0`) не выпускаем до v1.

Все actions закрепляются по SHA из research §5 с комментарием `# vX.Y.Z`. На `/implement` пины сверяются ещё раз (карантин 7 дней: `setup-qemu`/`build-push` на момент research 13 дней).

### D7. govulncheck — отдельный non-required job

Блокирующий govulncheck красил бы каждый PR до закрытия `PROD-DEPS-VULN` — у нас и так нет зелёного CI, который можно потерять. Job запускается и честно красный, но в required не входит. Критерий перевода в required — закрытие `PROD-DEPS-VULN` (вписать в DoD той задачи). `continue-on-error` не используем: он скрыл бы красноту в UI.

### D8. HA e2e — non-required job на каждый PR

Сценарий на фиксированных ожиданиях, на shared runner'е — кандидат во флейки (research §6). Запускается на каждом PR, чтобы копить статистику. Required — после 10 подряд зелёных прогонов на `main` (записать в BACKLOG как follow-up).

### D9. Проверка release-пути без публикации

- Multi-arch сборку обоих образов доказывает `ci.yml` / `images` на PR этой ветки (тот же Dockerfile, те же платформы).
- `release.yml` отличается только login + metadata + `push: true`. Он проверяется `actionlint` и чтением, а не прогоном.
- `workflow_dispatch` в `release.yml` не добавляем: до мержа в `main` он всё равно недоступен (GitHub запускает dispatch только для workflow с default branch), а после мержа даёт ручной путь к публикации в обход тега.
- Первый реальный прогон `release.yml` — первый push тега. Делает владелец (в `PROD-RELEASE-V010`), сразу после — перевод двух пакетов GHCR в Public (research §7).

### D10. Required checks и branch protection — документируются, не включаются

В `docs/CI.md` (новый, English — публичная документация) описываются:

- что запускается и когда;
- список required checks: `gate`, `images (amp)`, `images (config-reloader)`, `actionlint`;
- как воспроизвести локально (`./scripts/release-gate.sh`);
- как выпустить образы (push тега, затем разовый перевод пакетов в Public).

Включение защиты `main` — действие владельца после первого зелёного прогона (GitHub предлагает в required только уже виденные check'и).

### D11. Флейк `PUBLISHING-WARMUP-TEST-FLAKY` — чиним

`TestBackgroundWorker_WarmupPeriod` (`go-app/internal/business/publishing/refresh_worker_test.go`) проверяет «вызов после warmup» фиксированным `time.Sleep(10ms)`. На 4-vCPU runner'е под `-race` этого не хватает.

Замена: положительная проверка через `assert.Eventually(…, 2*time.Second, time.Millisecond)`. Отрицательная проверка «0 вызовов во время warmup» тоже временна́я: `Sleep(5ms)` при warmup 10 ms под нагрузкой может проспать warmup. Она делается сразу после `Start()`, без сна.

Продакшн-код не меняется. Отдельный коммит `test:`; запись в BUGS.md → Resolved. Остальные sleep-тесты пакета (`PeriodicRefresh` и др.) не трогаем без фактов — если флейкнут в CI, заводим отдельно.

### D12. Ссылки на образы в чарте

- `values.yaml`: `configReloader.image.repository: ""` → `ghcr.io/ipiton/amp-config-reloader`. `configReloader.enabled: false` не трогаем; комментарий про «образа нет» обновить на «публикуется release-workflow».
- `values-production.yaml`:
  - `image.repository: ipiton/amp-llm` → `ghcr.io/ipiton/amp`;
  - `configReloader.image.repository: registry.example.com/amp/config-reloader` → `ghcr.io/ipiton/amp-config-reloader`;
  - PLACEHOLDER-комментарий обновить.
- `image.tag` (`"0.0.1"` / `"1.0.0"`) не трогаем — это `PROD-RELEASE-V010`. В `values-production.yaml` рядом с `tag: "1.0.0"` комментарий, что такого тега нет до V010.

## Scope (файлы)

| Файл | Изменение |
|---|---|
| `.github/workflows/ci.yml` | новый (D1, D5, D7, D8) |
| `.github/workflows/release.yml` | новый (D5, D6) |
| `.dockerignore` | новый (D4) |
| `Dockerfile`, `Dockerfile.config-reloader` | D3, D4 |
| `go-app/go.mod` | `toolchain go1.26.8` (D3) |
| `scripts/release-gate.sh` | helm repo add + шаг `helm-deps` (D2) |
| `go-app/internal/business/publishing/refresh_worker_test.go` | D11 |
| `helm/amp/values.yaml`, `helm/amp/values-production.yaml` | D12 |
| `docs/CI.md` | новый (D10) |
| `README.md` | ссылка на `docs/CI.md`, CI-бейдж |
| `CHANGELOG.md` | `[Unreleased]`: Added CI/образы; Changed — репозиторий образов в values (migration note для тех, кто переопределял) |
| `docs/06-planning/*` | BACKLOG: закрыть `PROD-CI-IMAGES`, `PROD-HELM-CLEAN-CHECKOUT`; завести `PROD-DEPS-VULN`, `CI-E2E-HA-REQUIRED`, `CI-SUPPLY-CHAIN` (SBOM/cosign/Trivy/full race). BUGS: флейк → Resolved. DECISIONS: ADR-013 (GHCR + два workflow + гейт = release-gate) |

Нарезка внутри ветки (коммиты по порядку, каждый оставляет гейт зелёным):

1. `fix(release-gate)`: D2.
2. `test(publishing)`: D11.
3. `build`: D3 + D4 (Dockerfile, `.dockerignore`, `go.mod`).
4. `ci`: `ci.yml`.
5. `ci`: `release.yml`.
6. `feat(helm)`: D12.
7. `docs`: `docs/CI.md`, README, CHANGELOG, ADR, planning.

## Критерии приёмки

1. На чистом checkout (`git archive HEAD` в пустой каталог, изолированные `HELM_*_HOME`) `./scripts/release-gate.sh` проходит helm-шаги. При недоступном репо чарта шаг `helm-deps` = FAIL в summary.
2. `actionlint` по `.github/workflows/` — 0 замечаний. Все `uses:` закреплены 40-символьным SHA; `grep -E 'uses: [^@]+@v'` пуст.
3. PR этой ветки в GitHub: `gate`, `images (amp)`, `images (config-reloader)`, `actionlint` зелёные. `govulncheck` красный ровно на находках из research §3 (плюс, возможно, stdlib go1.26.8 — перечислить в `PROD-DEPS-VULN`). `e2e-ha` отработал, результат и время записаны в `tasks.md`.
4. В логе `images` видно, что собраны `linux/amd64` и `linux/arm64`, и Go-стадия шла на build-платформе (без QEMU).
5. В логе `gate` — `go version go1.26.8`.
6. `docker run --rm <локально собранный amp> ` стартует, `/healthz` 200. Образ reloader стартует с `--help`. Проверка локально через `docker buildx build --platform linux/arm64 --load`.
7. `helm template` с `values.yaml` и `values-production.yaml` не содержит `amp-llm` и `private-registry`; release-gate зелёный.
8. `TestBackgroundWorker_WarmupPeriod` проходит `go test -race -count=50 -run WarmupPeriod ./internal/business/publishing/`.
9. `release.yml`: `packages: write` только на job `publish`, триггер только `tags: v*`. Workflow не запускался (тег не пушился).
10. Документация и planning обновлены по таблице Scope; `git diff --check` чистый.

## Риски

| Риск | Митигация |
|---|---|
| На runner'е всплывут новые флейки (Linux, 4 vCPU, testcontainers в docker) | Не маскировать ретраями. Каждый флейк — запись в BUGS с логом; если блокирует `gate` — решение на `/testing` (fix или skip со ссылкой) |
| `gate` дольше 20–30 мин | `timeout-minutes: 30`; при превышении — вынос `race` в отдельный job отдельным изменением |
| Отказ `groundhog2k.github.io` роняет гейт | Осознанно: FAIL виден в `helm-deps`. Вендорить сабчарт — альтернатива в BACKLOG `PROD-HELM-CLEAN-CHECKOUT`, если станет частым |
| Пакеты GHCR private после первого push | Шаг в `docs/CI.md` + DoD `PROD-RELEASE-V010` |
| `release.yml` не прогнан до первого тега | Сборка — та же, что в `images` на PR; отличие — login/metadata/push, проверяемые actionlint'ом. Первый тег — под присмотром владельца |
| Смена `alpine:3.19` → `3.24` меняет runtime | `ca-certificates`, `tzdata`, `adduser` есть во всех ветках; критерий 6 проверяет старт образа |
| Сменили `repository` в values — кто-то переопределял через `--set image.repository` | Не затронуты (явный override сильнее). Migration note в CHANGELOG для тех, кто полагался на `ipiton/amp-llm` |
