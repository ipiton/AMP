# Implementation Checklist: KARMA-COMPAT

Ветка `feature/karma-compat`. Источник: `Spec.md` (решения D1-D6). Слайс один, резать не требуется: ~0.5d, один пакет + тест + доки.

## Research & Spec
- [x] Research завершён — `research.md` (F2: наивная реализация ломает karma; F3: `"dev"` не semver)
- [x] Spec зафиксирован — `Spec.md` (D1-D6)
- [x] Развилка по karma-шагу в гейте снята пользователем 2026-09-23: только хермет-тест

## Implementation

- [x] **S1. Константа версии контракта.** В `go-app/internal/buildinfo/` добавить `AlertmanagerCompatVersion = "0.27.0"` с комментарием: значение привязано к `docs/ALERTMANAGER_COMPATIBILITY.md:5` («Alertmanager Version: v0.27+»), меняется только вместе с ней, обязано оставаться валидным semver `>= 0.22.0` (ниже — karma не найдёт маппер).
- [x] **S2. Коллекторы.** Новый файл `go-app/internal/buildinfo/metrics.go`:
  - `alertmanager_build_info{version,revision,branch,goversion} = 1`, где `version` = `AlertmanagerCompatVersion`, остальное — реальные (`Revision`, `Branch`, `runtime.Version()`);
  - `amp_build_info{version,revision,branch,goversion,build_user,build_date} = 1` — целиком из `buildinfo`;
  - оба как `prometheus.NewGaugeVec` + `WithLabelValues(...).Set(1)`; `version.NewCollector` НЕ использовать (D3).
  - Help-строки явные: у compat-метрики написать, что это версия реализуемого контракта Alertmanager, а не версия AMP.
- [x] **S3. Регистрация.** `func Register(r prometheus.Registerer) error` там же:
  - без `init()` и `promauto` (D4) — тест должен собирать чистый регистр;
  - повторный вызов не паникует: `prometheus.AlreadyRegisteredError` не считать фатальной.
- [x] **S4. Точка вызова.** В `ServiceRegistry.Initialize` рядом с созданием `metricsGate` (`go-app/internal/application/service_registry.go:332`) вызвать `buildinfo.Register(prometheus.DefaultRegisterer)`; ошибку логировать, но **не** ронять Initialize (иначе повторная инициализация реестра в тестах положит процесс). `cmd/` не трогаем — код в `internal/`.
- [x] **S5. go.mod** _(перенесено в `/write-tests`: зависимость нужна тесту, раньше он появится — `go mod tidy` будет ругаться на неиспользуемую прямую зависимость)_. `Masterminds/semver/v3` из indirect в direct (нужен только тесту, D5). Проверить, что `go mod tidy` не тянет ничего нового.

### Проверено на живом сервере (S4, снимает допущение из блокеров)

Lite-профиль на `:19093`, сборка без ldflags и сборка с реальными ldflags:

```
alertmanager_build_info{branch="feature/karma-compat",goversion="go1.27.1",revision="c24b0da",version="0.27.0"} 1
amp_build_info{...,version="v0.0.2-516-gc24b0da-dirty"} 1
```

Подтверждено: дефолтный регистр — тот же, что отдаёт `/metrics`; compat-версия остаётся `0.27.0` независимо от ldflags; реальная версия (та самая, что сломала бы karma) уезжает в `amp_build_info`.

## Testing

- [x] **T1. Контракт karma (главный тест).** Воспроизвести цепочку целиком (D5): чистый `prometheus.NewRegistry()` → `Register` → `promhttp.HandlerFor` → прочитать тело → `expfmt.NewTextParser` (как `verprobe.Detect`) → достать лейбл `version` из `alertmanager_build_info` → `strings.SplitN(v, "-", 2)[0]` (как `fixSemVersion`) → `semver.NewConstraint(">=0.22.0").Check(semver.MustParse(...))`.
- [x] **T2. Негативная проверка теста.** Временно подменить константу на `"dev"` и на `"0.0.2"`, убедиться, что T1 краснеет в обоих случаях, вернуть значение. Это критерий приёмки из Spec, а не факультатив: тест, который не ловит регресс, бесполезен.
- [x] **T3. `amp_build_info`.** Присутствует, значения совпадают с `buildinfo.*`; при сборке без ldflags там `dev`/`unknown` и это **не** протекает в compat-метрику.
- [x] **T4. Идемпотентность.** Двойной `Register` в один регистр не паникует и возвращает ошибку, которую вызыватель вправе проигнорировать.
- [x] **T5. Гейты.** `go build ./...`, `go vet ./...`, `go test ./... -count=1` зелёные. Прогнать `-race` на затронутом пакете.

### Результаты тестов (2026-09-23)

`go-app/internal/buildinfo/metrics_test.go`, 6 тестов, зелёные (в т.ч. под `-race`).
Главный — `TestAlertmanagerBuildInfo_KarmaVersionProbe`: чистый регистр → `promhttp` → `expfmt` → лейбл `version` → `SplitN(v,"-",2)[0]` → `semver.NewConstraint(">=0.22.0")`.

**T2 выполнена, тест ловит регресс** — обе подмены константы краснеют:

```
AlertmanagerCompatVersion="dev":   version "dev" ... is not valid semver: Invalid Semantic Version
AlertmanagerCompatVersion="0.0.2": version "0.0.2" does not satisfy karma's mapper constraint >=0.22.0
```

Падают оба теста (`KarmaVersionProbe` и `CompatVersionConstant`), значение возвращено, `git diff` по `metrics.go` пустой.

Гейты: `go build ./...`, `go vet ./...` — чисто; `go test -race ./internal/buildinfo/` — зелено; `gofmt -l` пусто; `git diff --check` чисто.

**S5:** `Masterminds/semver/v3` переведён в прямой блок `require` **без смены версии** — `v3.3.0`, та же, что уже была в `go.sum`. Промежуточный `go get` поднял её до `v3.5.0`, это откачено: обновление зависимости в скоуп задачи не входит.

⚠️ `go mod tidy` не запускался: у репозитория **предсуществующий** дрейф `go.mod` (tidy хочет выкинуть `spf13/cobra`, `mattn/go-sqlite3`, `oklog/ulid/v2` — в `go-app` их не импортирует ни один файл — и переставить `docker`, `client_model`, `grpc`). Чистка не относится к задаче; правка сделана точечно, диф — 2 строки.

⚠️ **Чужой флейк в полном прогоне:** `TestBackgroundWorker_WarmupPeriod` (`internal/business/publishing`) упал один раз на `go test ./...` («Expected call after warmup», ожидание 10 ms warmup под нагрузкой), 5 прогонов пакета подряд — зелёные. Пакета задача не касается. Кандидат в `BUGS.md` на шаге `/write-doc`.

## Documentation & Cleanup

- [ ] **D1. Compat-дока.** Раздел про дашборды/karma в `docs/ALERTMANAGER_COMPATIBILITY.md`:
  - как karma определяет версию (через `/metrics`, не через `/api/v2/status`) и что именно мы отдаём;
  - пример подключения (`ALERTMANAGER_URI`);
  - что работает: группы с `group_by`, сайленсы (создание/expire), `suppressed` со ссылкой на silence ID — проверено протокольно 2026-09-23;
  - что неприменимо: агрегация нескольких инстансов (AMP сам является инстансом), 24-часовая история karma (тянет из Prometheus; у AMP история в Postgres и наружу не выставлена — `HISTORY-API`);
  - `metrics.enabled: false` → 404 → karma уходит в fallback и продолжает работать;
  - честно: сквозной прогон karma не выполнялся, проверка протокольная.
- [ ] **D2. ADR-009** в `docs/06-planning/DECISIONS.md` — AMP машиночитаемо заявляет версию реализуемого контракта Alertmanager: контекст, решение (две метрики), обоснование `0.27.0`, accepted risk (обязательство перед инструментами), следствие (менять только вместе с compat-докой).
- [ ] **D3. CHANGELOG.** Запись в `[Unreleased] / Added` — обе метрики, с явной оговоркой, что `alertmanager_build_info.version` намеренно не равна версии AMP.
- [ ] **D4. BACKLOG.** Завести вынесенные follow-up'ы: karma-шаг в release-gate (с заметкой: ghcr в текущей среде недоступен, Docker Hub `lmierzwa/karma` работает, но в README karma не задокументирован; пин `v0.132` — `v0.133` под 7-дневным карантином), конфиг-ключ переопределения compat-версии, `versionInfo.version` в `/api/v2/status`.
- [ ] **D5. Planning.** `NEXT.md`: задача остаётся в WIP до `/end-task`; отметить, что karma-шаг выведен из скоупа.

## Finalization
- [ ] `git diff --check` чистый, нерелевантные файлы не затронуты
- [ ] `/write-tests` → `/testing` → `/write-doc` → `/end-task` по пайплайну
- [ ] `DONE.md` + архив `tasks/archive/KARMA-COMPAT/` на `/end-task`

## Блокеры и открытые допущения

- 🔴 **Допущение (не проверено сквозняком):** что karma после правки перестанет ошибаться и покажет алерты — вывод из чтения её кода, а не наблюдение. Образ karma в этой среде не тянется (ghcr → `denied`). Хермет-тест воспроизводит её алгоритм, но не заменяет живой прогон. В доке это должно быть сказано прямо.
- 🔴 **Допущение (F3, не доказано):** что `semver.MustParse("dev")` роняет karma паникой — `recover` в её `Pull()` не найден, но поиск по чужому репозиторию без авторизации ненадёжен. На решение не влияет: инвариант «всегда валидный semver» нужен в любом случае.
- ⚠️ **Обязательство:** `version=0.27.0` — заявка на контракт. Если `/testing` вскроет расхождение с 0.27 в том, что karma реально использует (группы, сайленсы, статусы), останавливаемся и пересматриваем значение, а не «округляем вверх».
- ⚠️ Точка вызова `Register` в `Initialize` предполагает, что дефолтный регистр — тот же, что отдаёт `/metrics`. Проверено по коду (`router.go:59` + `pkg/metrics/v2/registry.go:82`), но подтвердить руками на `/testing`: поднять сервер и увидеть обе метрики в реальной выдаче.
