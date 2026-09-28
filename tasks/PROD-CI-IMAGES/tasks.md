# Implementation Checklist: PROD-CI-IMAGES

Ветка `feature/prod-ci-images`. Источник: `Spec.md` (решения D1–D12, критерии AC1–AC10). Пути — от корня репозитория.

Одна ветка, два логических среза (spec «Нарезка»):

- **Срез A — CI на PR** (I1–I4, ~0.75d): гейт переносим на чистую машину, флейк починен, `ci.yml`.
- **Срез B — образы и публикация** (I3 — Dockerfile, I5–I6, ~0.5d): кросс-компиляция, `release.yml`, адреса образов в чарте.

I3 (Dockerfile) стоит до `ci.yml`: job `images` на PR должен сразу собирать уже кросс-компилируемые Dockerfile, иначе первый прогон arm64 пойдёт под QEMU и ничего не докажет по AC4. Каждый коммит оставляет `./scripts/release-gate.sh` зелёным локально.

## Допущения и блокеры

- 🔴 **AC3 требует PR в GitHub.** Для этого нужен push ветки и открытие PR — внешние действия, **только с явного согласия** на `/testing`. Без PR проверяемы AC1, AC2, AC4 (частично, локальным `buildx`), AC5 (частично), AC6–AC10; AC3 и логи CI остаются непроверенными, и это фиксируется в отчёте честно.
- 🔴 **`origin/main` отстаёт от локального `main`**: не запушены 20 коммитов, включая merge PROD-AUTH и PROD-RBAC-SCOPE (`origin/main` = `beab7df`, расхождения в обратную сторону нет — fast-forward). PR ветки против `origin/main` потащит в diff чужие изменения. Перед PR нужно запушить `main` — тоже с согласия владельца.
- **Первый прогон workflow в PR:** GitHub запускает `pull_request`-workflow из ветки PR, так что `ci.yml` отработает до мержа. `release.yml` не запустится (тег не пушим, D9).
- **Секреты:** не нужны. Публикация — через `GITHUB_TOKEN`, а `ci.yml` вообще ничего не пушит.
- **actionlint локально не установлен.** Запускать `go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12` (релиз 2026-03-30, карантин пройден). Бинарник в репо не кладём.
- **Пины сверены на `/plan` (2026-09-28):** helm `v4.3.0` (2026-09-09), golangci-lint `v2.13.2` (2026-08-27), govulncheck `v1.8.0` (2026-09-08), `golang:1.26.8-alpine` (Alpine 3.24.2), `alpine:3.24` (3.24.2). SHA actions — research §5; на I4 перепроверить `commits/<tag>` ещё раз.
- **`golang:1.26.8-alpine` и `alpine:3.24` пиним тегом, не digest'ом.** По digest — воспроизводимее, но без Dependabot превращается в ручную рутину. Записать в follow-up `CI-SUPPLY-CHAIN`.
- **Сеть** нужна локально для `helm repo add`, `go run …@version` и `docker pull`. Без неё AC1 не проверить.

## Research & Spec
- [x] Research — `research.md` (helm на чистом checkout падает; govulncheck 9 находок; Dockerfile без кросс-компиляции; release-gate 487 s, PASS)
- [x] Spec — `Spec.md` (D1–D12), 2026-09-28; на `/plan` уточнён runtime `alpine:3.22` → `3.24` (D4)

## Implementation

- [x] **I1. Гейт на чистом checkout (D2)** — `scripts/release-gate.sh`, коммит `fix(release-gate): add chart repos before dependency build`:
  - функция `step_helm_deps`:
    - читает `repository:` из `$HELM_CHART_DIR/Chart.lock`, делает `helm repo add amp-dep-<n> <url> --force-update` для каждого;
    - затем `helm dependency build "$HELM_CHART_DIR"`;
    - ошибки не глушит (`>/dev/null` только для stdout успешных команд, stderr в лог);
  - заменить блок `if command -v helm … helm dependency build … >/dev/null 2>&1` на `run_step "helm-deps" "helm repo add + dependency build" step_helm_deps` перед `helm-dev`;
  - не нарушать ограничения скрипта: bash 3.2, без `mapfile`, пустые массивы под `set -u`, here-string вместо `| grep -q`;
  - обновить шапку-комментарий (список шагов);
  - `shellcheck scripts/release-gate.sh`, если установлен.
- [x] **I1a. AC1 до и после** — на `git archive HEAD` в `scratchpad/clean-<n>/` с изолированными `HELM_CONFIG_HOME/HELM_CACHE_HOME/HELM_DATA_HOME`:
  - до правки (из research §2 уже есть: `no repository definition`) — не повторять;
  - после: helm-шаги гейта PASS. Гонять только helm-часть: `step_helm_*` через временную обёртку или полный гейт, если время позволяет;
  - негатив: подменить URL на недоступный (`HELM_REPOSITORY_CONFIG` не трогаем — правим копию `Chart.lock` в scratchpad) → `helm-deps` = FAIL в summary.
- [x] **I2. Флейк (D11)** — `go-app/internal/business/publishing/refresh_worker_test.go`, коммит `test(publishing): make warmup test wait for the call instead of sleeping`:
  - `TestBackgroundWorker_WarmupPeriod`:
    - проверка «0 вызовов» — сразу после `Start()`, без `Sleep(5ms)`;
    - «вызов после warmup» — `assert.Eventually(func() bool { return mock.GetDiscoverCallCount() > 0 }, 2*time.Second, time.Millisecond)`;
    - проверку `elapsed >= 10ms` сохранить: она и есть смысл warmup'а. `startTime` зафиксировать до `Start()` и проверять задержку первого вызова (время, когда `Eventually` увидел вызов, ≥ warmup);
  - прод-код не трогать; другие тесты файла не трогать;
  - проверка (AC8): `go test -race -count=50 -run WarmupPeriod ./internal/business/publishing/` — PASS; плюс полный пакет `-race -count=3`.
- [x] **I3. Сборка (D3, D4)** — коммит `build: pin go1.26.8 and cross-compile images for multi-arch`:
  - `go-app/go.mod`: строка `toolchain go1.26.8` после `go 1.26.0`. `go mod tidy` **не** запускать: не трогаем `go.sum`, `PROD-DEPS-VULN` отдельно. Проверить `go build ./...` локально на 1.27.1 — toolchain не должен ничего скачивать;
  - `Dockerfile`:
    - `FROM --platform=$BUILDPLATFORM golang:1.26.8-alpine AS builder`;
    - `ARG TARGETOS TARGETARCH`;
    - `GOOS=$TARGETOS GOARCH=$TARGETARCH` в `go build`;
    - runtime `alpine:3.24`;
  - `Dockerfile.config-reloader`:
    - то же для builder;
    - удалить мёртвые `ARG VERSION/REVISION/BUILD_DATE` (OCI-метки поставит `metadata-action`);
  - `.dockerignore`: `.git`, `.claude`, `tasks`, `docs`, `helm`, `deploy`. Убедиться, что `deploy/smoke` и `deploy/e2e-ha` не берут файлов из build context: они монтируют конфиги volume'ами — сверить `docker-compose.yml`;
  - проверка AC6:
    - `docker buildx build --platform linux/amd64,linux/arm64 -f Dockerfile .` (без `--load`, только сборка), затем `--platform linux/arm64 --load -t amp:ci-local` → `docker run` с минимальным конфигом → `/healthz` 200;
    - reloader: `--load` → `docker run … --help` (или `-h`) → exit 0 и usage;
    - в логе buildx builder-стадия идёт на `linux/arm64` (build platform на Mac) и для amd64-таргета — без QEMU-компиляции Go;
  - гейт: полный `./scripts/release-gate.sh` — `amtool-compat` пересобирает образ из нового Dockerfile.
- [x] **I4. `ci.yml` (D1, D5, D7, D8)** — `.github/workflows/ci.yml`, коммит `ci: add PR workflow running release gate and image builds`:
  - `on: pull_request`, `push: branches: [main]`;
  - `permissions: contents: read`;
  - `concurrency: group: ci-${{ github.workflow }}-${{ github.event.pull_request.number || github.ref }}`, `cancel-in-progress: ${{ github.event_name == 'pull_request' }}`;
  - job `gate`, `timeout-minutes: 30`:
    - checkout;
    - setup-go (`go-version-file: go-app/go.mod`, `cache-dependency-path: go-app/go.sum`);
    - `go version`, для AC5;
    - golangci-lint-action (`version: v2.13.2`, `install-only: true`);
    - setup-helm (`version: v4.3.0`);
    - `./scripts/release-gate.sh`;
  - job `images`, matrix `{name: amp, file: Dockerfile}` / `{name: config-reloader, file: Dockerfile.config-reloader}`:
    - `name: images (${{ matrix.name }})` — ровно это имя пойдёт в required checks;
    - setup-qemu, setup-buildx;
    - build-push (`push: false`, `platforms: linux/amd64,linux/arm64`, `cache-from/to: type=gha,scope=${{ matrix.name }}`);
  - job `actionlint`: `go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12` после setup-go. Предпочтительнее docker-action `rhysd/actionlint` по SHA — выбрать то, что не тянет лишнего; зафиксировать выбор здесь;
  - job `govulncheck`: setup-go → `cd go-app && go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...`;
  - job `e2e-ha`: `timeout-minutes: 20`, `./deploy/e2e-ha/run.sh`;
  - все `uses:` — `owner/repo@<40-sha> # vX.Y.Z`; SHA перепроверить через `gh api repos/<a>/commits/<tag>`;
  - `persist-credentials: false` в checkout (job'ам не нужен git push);
  - проверка AC2: actionlint — 0 замечаний; `grep -nE 'uses: [^ ]+@v[0-9]' .github/workflows/*.yml` — пусто.
- [x] **I5. `release.yml` (D5, D6, D9)** — `.github/workflows/release.yml`, коммит `ci: add tag-triggered image publishing to GHCR`:
  - `on: push: tags: ['v*']`, без `workflow_dispatch`;
  - `permissions: {}` на workflow, на job `publish` — `contents: read`, `packages: write`;
  - matrix — как в `images`. `image: ghcr.io/${{ github.repository_owner }}/amp` и `…/amp-config-reloader` — **в нижнем регистре**: владелец `ipiton` уже lowercase, но зафиксировать явно литералом, а не через выражение;
  - шаги:
    - checkout;
    - qemu → buildx;
    - login-action (`registry: ghcr.io`, `username: ${{ github.actor }}`, `password: ${{ secrets.GITHUB_TOKEN }}`);
    - metadata-action: теги D6, `flavor: latest=auto`, labels — `org.opencontainers.image.source/revision/version/created`;
    - build-push (`push: true`, `platforms`, `tags`/`labels` из metadata; для `amp` — `build-args` VERSION/REVISION/BRANCH/BUILD_DATE из metadata outputs);
  - проверка AC9: actionlint; `packages: write` встречается ровно один раз, в job `publish`; триггер — только теги.
- [x] **I6. Адреса образов (D12)** — коммит `feat(helm): point image repositories at GHCR`:
  - `helm/amp/values.yaml`: `configReloader.image.repository: ghcr.io/ipiton/amp-config-reloader`, комментарий про публикацию;
  - `helm/amp/values-production.yaml`:
    - `image.repository: ghcr.io/ipiton/amp`;
    - комментарий у `tag: "1.0.0"` — тега нет до `PROD-RELEASE-V010`;
    - `configReloader.image.repository: ghcr.io/ipiton/amp-config-reloader`;
    - PLACEHOLDER-комментарий переписать;
  - сверить `helm/amp/README.md` и `helm/amp/docs/` на упоминания `amp-llm` / `private-registry` / `config-reloader` image: `grep -rn 'amp-llm\|private-registry' helm/ docs/ README.md`. Правим только ссылки на образы;
  - проверка AC7: `helm template` дефолт / dev / production (placeholder-пароли) — нет `amp-llm`, `private-registry`; release-gate helm-шаги PASS.

## Testing

- [ ] AC1 — чистый checkout, helm-шаги PASS; негатив → `helm-deps` FAIL (I1a)
- [ ] AC2 — actionlint 0; все `uses:` по SHA (I4, I5)
- [ ] AC3 — PR в GitHub: `gate`, `images (amp)`, `images (config-reloader)`, `actionlint` зелёные; `govulncheck` — список находок; `e2e-ha` — результат и время. **Только после согласия на push `main` и ветки** (см. блокеры)
- [ ] AC4 — лог `images`: обе платформы, Go-стадия на build-платформе (в PR); локально — эквивалент через `buildx` (I3)
- [ ] AC5 — `go version go1.26.8` в логе `gate` (PR); локально — `go env GOTOOLCHAIN` / `go version` в `go-app` с `GOTOOLCHAIN=auto` на машине с Go < 1.26.8, если такая есть; иначе только CI
- [ ] AC6 — `amp` стартует, `/healthz` 200; reloader `--help` (I3)
- [ ] AC7 — нет `amp-llm`/`private-registry` в рендерах (I6)
- [ ] AC8 — warmup-тест `-race -count=50` (I2)
- [ ] AC9 — `release.yml`: права и триггер (I5)
- [ ] Полный `./scripts/release-gate.sh` локально — PASS (финальный прогон на последнем коммите)
- [ ] `git diff --check main...HEAD` — чисто

## Documentation & Cleanup

- [ ] **D1. `docs/CI.md`** (English):
  - workflows и триггеры;
  - таблица jobs: required / non-required и почему;
  - как воспроизвести локально;
  - как выпустить образы: push тега `vX.Y.Z` → `release.yml` → **разовый перевод пакетов `amp` и `amp-config-reloader` в Public** в GitHub Packages settings;
  - теги образов (D6);
  - критерии перевода `govulncheck` и `e2e-ha` в required;
  - как включить branch protection (required checks по именам).
- [ ] **D2. `README.md`** — CI-бейдж `ci.yml`, ссылка на `docs/CI.md` в разделе про разработку/релиз.
- [ ] **D3. `CHANGELOG.md` `[Unreleased]`**:
  - Added: CI (PR gate), multi-arch образы в GHCR на тег;
  - Changed: `values-production.yaml` `image.repository` → `ghcr.io/ipiton/amp`, config-reloader → GHCR; runtime-база `alpine:3.24`; Go 1.26.8;
  - Breaking/migration note — для тех, кто полагался на `ipiton/amp-llm` / `registry.example.com` без явного override.
- [ ] **D4. `docs/06-planning/DECISIONS.md`** — ADR-013: GHCR как реестр; гейт CI = `release-gate.sh`; два workflow ради `packages: write`; govulncheck/e2e-ha non-required до условий.
- [ ] **D5. `BACKLOG.md`**:
  - закрыть `PROD-CI-IMAGES` и `PROD-HELM-CLEAN-CHECKOUT` (ссылка на архив и ADR-013);
  - завести `PROD-DEPS-VULN` (находки govulncheck из AC3 или research §3; DoD: govulncheck → required), `CI-E2E-HA-REQUIRED` (10 зелёных прогонов), `CI-SUPPLY-CHAIN` (SBOM/provenance, cosign, Trivy, digest-пины базовых образов, Dependabot для actions, полный `-race`);
  - `PROD-RELEASE-V010`: дописать шаг «после первого push тега — пакеты GHCR в Public, `docker pull` без auth».
- [ ] **D6. `BUGS.md`** — `PUBLISHING-WARMUP-TEST-FLAKY` → Resolved (коммит I2).
- [ ] **D7. `NEXT.md`** — заметка о закрытии, следующий прод-блокер (`PROD-DEPS-VULN` / `PROD-RELEASE-V010`), CONFIG-RELOADER-SIDECAR: образ теперь собирается — обновить пометку в Queue.

## Finalization (`/end-task`)

- [ ] Все AC проверены или явно помечены «не проверено: причина»
- [ ] `DONE.md` — запись
- [ ] Workspace → `tasks/archive/PROD-CI-IMAGES/`
- [ ] Ветка не `main`, `git status` чистый

## Результаты

### `/implement` (2026-09-28)

Коммиты на ветке, по порядку:

| Коммит | Пункт |
|---|---|
| `75da0ec` fix(release-gate): add chart repos before dependency build | I1 |
| `6c29cb7` test(publishing): make warmup test wait for the call instead of sleeping | I2 |
| `7597b0a` build: pin go1.26.8 and cross-compile images for multi-arch | I3 |
| `a53085a` docs(bugs): record two pre-existing -race flakes in the release gate | находка, см. ниже |
| `c0a602d` ci: add PR workflow running release gate and image builds | I4 |
| `d3fb66a` ci: add tag-triggered image publishing to GHCR | I5 |
| `01c3d1d` feat(helm): point image repositories at GHCR | I6 |
| `8c97cf6` test(helm): pass placeholder passwords to the production render | I6, отклонение 4 |

Доказательства, собранные по ходу (формально перепроверяются на `/testing`):

- **AC1.** Проверено на `git archive HEAD helm/amp` плюс рабочий `release-gate.sh` в пустом каталоге, с изолированными `HELM_*_HOME`, только helm-шаги (обёртка в scratchpad):
  - `helm-deps`/`helm-dev`/`helm-production`/`helm-rbac` — PASS, `valkey-2.1.3.tgz` скачан; пользовательский helm-конфиг не создан (`hc/` отсутствует);
  - то же под системным `/bin/bash` 3.2.57 — PASS;
  - негатив (URL репо в копии `Chart.lock`/`Chart.yaml` → `https://127.0.0.1:9/nope`) — `helm-deps` FAIL с `connection refused` в логе;
  - `shellcheck` — чисто.
- **AC2.** `actionlint` v1.7.12 — 0 замечаний на обоих workflow. `grep -nE 'uses: [^ ]+@v[0-9]'` — пусто, 19 `uses:` по SHA. SHA перепроверены `gh api repos/<a>/commits/<tag>` — совпали с research §5, все по-прежнему latest.
- **AC4 (локально).** `docker buildx build --platform linux/amd64,linux/arm64` обоих Dockerfile — OK. В логе `[linux/arm64->amd64 builder …] RUN … GOOS=linux GOARCH=amd64 go build` — Go для amd64 кросс-компилируется на build-платформе, без QEMU. Для CI (x86 runner) ожидается зеркально `amd64->arm64`.
- **AC5 (частично).** `/api/v2/status` собранного образа отдаёт `goVersion: go1.26.8` — образ собран закреплённым патчем. Для `gate` в CI — по логу PR. setup-go v7 README подтверждает: при наличии `toolchain` берётся он.
- **AC6.** `amp:ci-local` (linux/arm64, `--load`) с `deploy/smoke/config.yaml`: `/healthz` 200, `/etc/alpine-release` 3.24.2. `ERROR Failed to connect to Redis` в логе — штатный fallback lite-профиля без Redis. `amp-config-reloader:ci-local --help` — usage, exit 0.
- **AC7.** `helm template` default/dev/production — 0 совпадений `amp-llm|private-registry`. Рендер с `configReloader.enabled=true` до/после I6 отличается только адресами образов (и случайными паролями, генерируемыми на каждый рендер). `helm/amp/tests/render-config-reloader.sh` — all assertions passed.
- **AC8.** `go test -race -count=50 -run 'TestBackgroundWorker_WarmupPeriod$'` — 50/50. Пакет `-race -count=3` — PASS. Мутация (WarmupPeriod 0 в `refresh_test_utils.go`) → тест падает 5/5 на «Expected no calls during warmup», файл восстановлен.
- **AC9.** `packages: write` — ровно одно вхождение (job `publish` в `release.yml`), workflow-level `permissions: {}`, триггер — только `push: tags: ['v*']`. Тег не пушился, workflow не запускался.
- **Полный release-gate** после I3 — **FAIL** на шаге `race`, всё остальное PASS: build 30s, lint 49s, test 127s, futureparity 63s, race 534s, helm-* ~4s, amtool-compat 29s (образ собран по новому Dockerfile). Причина — два предсуществующих флейка (ниже), а не изменения ветки.

### Находка: предсуществующие флейки шага `race`

Упали `TestDefaultTimerManager_TwoReplicasRaceSameGroupTimer_OnlyLockWinnerFires` (grouping, 2 срабатывания вместо 1) и `TestCache_LargeDataset` (silencing, перф-пороги по wall-clock). Воспроизведены на **чистом `main`@`41b8c28`** во временном worktree:

| Тест | Ветка | `main` |
|---|---|---|
| grouping, `-race -count=30` | 3/30 FAIL | 2/30 FAIL |
| silencing, `-race -count=30` | 4/30 FAIL | 2/30 FAIL |

Research-прогону (PASS) повезло. Заведены в BUGS.md: `GROUPING-TIMER-LOCK-RELEASED-BEFORE-LOSER` (гипотеза — TOCTOU: lock отпускается сразу после callback'а, опоздавшая реплика берёт его и срабатывает повторно; возможно, продуктовый дефект, прод предположительно спасает nflog-claim) и `SILENCING-CACHE-PERF-ASSERT-FLAKY`. В рамках задачи **не чинились** (spec: «не маскировать», решение на `/testing`).

🔴 **Следствие для CI:** required check `gate` будет краснеть примерно в 1 прогоне из 7. Варианты — на `/testing`:

- (a) чинить silencing-перф-тест здесь (дёшево: вынести в бенчмарк или пропускать под `-race`), а grouping — отдельной задачей, так как требует разбора продуктового поведения;
- (b) оба — отдельными задачами, `gate` пока не делать required;
- (c) временно `t.Skip` со ссылкой на BUGS для обоих.

### Отклонения от плана

1. **`step_helm_deps` использует временный repository config**, а не `helm repo add --force-update` в глобальный. План этого не предусматривал, но иначе локальный запуск гейта дописывал бы `amp-dep-1` в `~/.config/helm/repositories.yaml` разработчика.
2. **I2:** «проверка `elapsed >= 10ms`» из плана не сохранена — она была тождественно истинной (два `sleep` дают ≥15 ms). Вместо неё — отрицательная проверка «0 вызовов», выполняемая только если с `Start()` прошло меньше warmup: корректна без таймингов, мутация её ловит. `Sleep(warmup/2)` перед ней оставлен намеренно — без него warmup = 0 не ловится.
3. **I6 шире spec D12:**
   - в `values.yaml` было **два** ключа `configReloader:` — действовал последний, первый («values shape only — no template yet», `repository: ""`) был мёртв; удалён, рендер не изменился;
   - `values-dev.yaml` тоже ссылался на `ipiton/amp-llm` (research/spec пропустили) — переведён на `ghcr.io/ipiton/amp`, тег `latest` не тронут.
4. **`helm/amp/tests/render-config-reloader.sh` был сломан** с `0d08c5f` (обязательный `cache.auth.password` в production), в гейт не подключён — никто не заметил. Проверено на чартовых файлах, идентичных `main`: падает так же. Добавлены placeholder-пароли, как в release-gate. Подключение теста в гейт — follow-up (BACKLOG на `/write-doc`).
5. **actionlint в CI** — `go run …@v1.7.12` после setup-go, а не docker-action: не требует ещё одного пина и совпадает с тем, как он запускается локально.
6. `values-production.yaml` `configReloader`: комментарий «flip to true in the same change that adds the build job» заменён — job добавлен, но образа нет до первого релиза; `enabled: false` оставлен.

### Осталось до `/testing`

- Полный release-gate на финальном коммите (после I6 гонялись только helm-шаги и рендер-тест; Go-код после `7597b0a` не менялся).
- AC3, AC4 и AC5 в CI — нужен PR, см. блокеры в начале файла (push `main` и ветки — только с согласия).
- Решение по флейкам `race` (варианты a/b/c выше).
