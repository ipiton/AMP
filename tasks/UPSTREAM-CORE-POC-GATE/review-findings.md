# Deep Review Findings: контрактные сьюты и amtool на upstream-ядре, обновляемость ядра

**Trigger classification:** discretionary-running (дифф > 200 строк; вердикт по ADR-019 стоит на классификации расхождений)
**Reviewer perspective:** независимый агент (Claude, deep-review), не автор кода
**Reviewed at:** 2026-10-24
**Reviewed tree:** feature/upstream-core-poc-gate @ e885982 (первичный), e565c4f (re-review)
**Verdict:** pass после re-review @ e565c4f (первичный — fix_required; см. `review-verdict.json`)

## Что проверено

- Полный дифф `git diff main...HEAD` (18 файлов, +1050/−7), история `release-gate.sh`, `futureparity_compat.go`.
- `./scripts/upstream-core-gate.sh` — два прогона: первый **FAIL** (amp-upstream «did not start», см. F2), второй PASS (146 pass / 50 leaf-fail).
- `go test -tags futureparity ./cmd/server/` — ok (инвариант «без тега upstreamcore ничего не меняется» держится); `go vet -tags futureparity,upstreamcore` — ok; `go test ./internal/upstreamcore/` — ok; `shellcheck` обоих скриптов — чисто; `/bin/bash -n` (3.2.57) — ok.
- `upstream-bump.sh --diff v0.34.0 v0.34.1` — работает; путь отказа (`UPSTREAM_BUMP_MIN_AGE_DAYS=100000 upstream-bump.sh v0.34.0`) — отказ, `go.mod`/`go.sum` возвращены, `git status` чистый.
- Выборочная сверка классификации с исходниками upstream v0.34.1 из `GOMODCACHE` и с фактическими сообщениями падений (23 падения `CoreAPI`/status/silences/reload): причины в baseline совпадают по сути (неточности формулировок — F7). Проверены `api/v2/api.go` (`CheckSilenceMatchesFilterLabels` — точное совпадение типа и значения матчера; `ErrNotFound` → 404), `silence/silence.go` (`canUpdate`), `httpserver/httpserver.go` (`/debug/*` → `http.DefaultServeMux`), `swag.ConvertBool` (невалидный bool → `false`, без ошибки), `getStatusHandler` (`version.Version`).
- Проба асинхронности групп: временная задержка 20ms после `POST /api/v2/alerts` в обёртке харнесса → все 4 `flaky`-теста групп проходят 10/10 (`-count=10`). Файл восстановлен, дерево чистое.

## Design Premises

| Premise | Вывод ревью |
|---|---|
| Сьюты получают сервер только через `registerRoutes` | верно (measured, прогон гейта) |
| Группы появляются асинхронно после ingest | верно; подтверждено пробой (20ms settle → 10/10 pass, состав совпадает). Но следствие для гейта не учтено — F5 |
| amtool собирается из модуля upstream без нашего `go.mod` | верно (`go install …/cmd/amtool@v0.34.1` в гейте) |
| Тест-бинарь видит версию зависимости | верно (`port_test.go` проходит) |
| `go list -m -json mod@ver` отдаёт время публикации | **неверно**: это время коммита — F1 |

**«PoC проходит» — обосновано** в части классификации: выборка падений и исходники upstream подтверждают, что разрывов в ядре доставки нет. Оговорка: группировка на upstream-ядре сейчас гейтом не защищена (F5), а вердикт по группам держался на незакоммиченной пробе — моя проба его подтверждает.

## Findings

### F1 — Карантин считает возраст по времени коммита, а не публикации
- **Severity:** major
- **Location:** `scripts/upstream-bump.sh:41-48` (`age_days`), `docs/UPSTREAM_UPGRADE.md:33`, `Spec.md` § Design Premises (строка 5)
- **Issue:** `go list -m -json mod@ver` берёт `Time` из `.info` прокси, а это `RevInfo.Time` — **commit time** (`$GOROOT/src/cmd/go/internal/modfetch/codehost/codehost.go:164`: `Time time.Time // commit time`). Committer date задаёт автор коммита; тег, повешенный вчера на коммит с датой «месяц назад», карантин проходит. Для pseudo-version — то же. Класс сценария ровно тот, ради которого карантин заведён (скомпрометированный релиз). Скрипт и runbook называют это «publish time»; премиса помечена `code-read`, но код говорит обратное.
- **Recommendation:** (1) исправить формулировки в скрипте, `UPSTREAM_UPGRADE.md` и премису Spec (класс → опровергнута, «commit time»); (2) для самого `github.com/prometheus/alertmanager` брать дату публикации релиза (`gh api repos/prometheus/alertmanager/releases/tags/<ver> --jq .published_at`) и требовать max(commit, release) ≥ 7 дней; (3) для транзитивки — записать ограничение в TECH-DEBT (варианты: deps.dev `publishedAt` = first-seen прокси; полный скан `index.golang.org` за 7 дней по объёму неприемлем).
- **Disposition:** fix-here (формулировки + дата релиза upstream); транзитивка — defer-tech-debt
- **Follow-up:** TECH-DEBT.md — «карантин транзитивки по commit time» (завести на finalize)

### F2 — Гейт ложно красный на macOS при первом запуске свежего бинаря
- **Severity:** minor
- **Location:** `scripts/upstream-core-gate.sh:113-127`
- **Issue:** ожидание `listening` — 100 × 0.1s = 10s. Свежесобранный неподписанный бинарь на macOS при первом exec стартует ~11s (проверка системой; лог пуст до этого). Воспроизведено: первый прогон гейта — `amp-upstream did not start:` с пустым логом и `Killed: 9`; ручной старт того же бинаря — первая строка лога через ~11s, повторный — <0.2s; второй прогон гейта — PASS. После каждой правки, меняющей бинарь, первый `release-gate.sh` локально красный. Плюс сообщение вводит в заблуждение: процесс жив, лог пуст.
- **Recommendation:** бюджет 60s (цикл и так выходит раньше, если процесс умер); при таймауте писать «не начал слушать за Ns, процесс жив/мёртв» и лог.
- **Disposition:** fix-here
- **Follow-up:** n/a

### F3 — При ошибке сборки гейт не показывает ошибку компилятора; нет проверки `jq`
- **Severity:** minor
- **Location:** `scripts/upstream-core-gate.sh:46-49`; `scripts/upstream-bump.sh:38,45,47,52`; `scripts/release-gate.sh` (нет проверки `jq`)
- **Issue:** `go test -json` отдаёт ошибки компиляции событиями `"Action":"build-output"`, а раннер печатает только `"Action":"output"` — получается одна строка `FAIL … [build failed]` без причины (проба в scratch-модуле: `run_rc=4`, вывод — только `FAIL x [build failed]`). Spec Edge Case 2 обещает «FAIL с выводом сборки». Отдельно: при отсутствии `jq` гейт пишет «no test ran — build or setup failure» и сырой JSON — причина не названа; `release-gate.sh` проверяет `golangci-lint`/`helm`/`docker`, но не `jq`. Попутно: `2>&1` в файл JSON — любая не-JSON строка stderr посреди потока обрывает разбор `jq` (проверено: `jq` останавливается на первой ошибке парсинга); fail-closed, но с непонятной диагностикой.
- **Recommendation:** печатать `select(.Action=="output" or .Action=="build-output") | .Output`; `command -v jq` в начале обоих скриптов с явным сообщением; stderr `go test` писать в отдельный файл и показывать его при «no test ran».
- **Disposition:** fix-here
- **Follow-up:** n/a

### F4 — Слепые зоны раннера: падения родителей, пакета, статус baseline не валидируется
- **Severity:** minor
- **Location:** `scripts/upstream-core-gate.sh:52-64`
- **Issue:** (1) учитываются только листовые падения: если родитель (`TestPhase0Contracts_CoreAPI`, `…/alertmanager_compatibility_probes_contract`) падает собственным `t.Error` при уже упавшем известном подтесте, новое падение скрыто; (2) падение уровня пакета (`Test == null`, например паника в `TestMain`/`stopUpstreamCores` после `m.Run`) игнорируется — пакет и так `FAIL` из-за baseline; (3) колонки `class`/`expectation` не валидируются: опечатка `fail` вместо `fails` молча превращает запись в терпимую к любому исходу (как `flaky`), класс `e` проходит; (4) `flaky`-записи никогда не становятся «устаревшими» — ни при исчезновении теста, ни при стабильном прохождении. Это та самая «свалка», от которой Spec обещает защиту строгостью раннера.
- **Recommendation:** валидировать `class ∈ {a,b,c,d}`, `expectation ∈ {fails,flaky}`, иначе FAIL; `flaky`-запись для теста, который не запускался, — FAIL (`NOT RUN`); для упавших не-листьев проверять собственный вывод родителя на `_test.go:NN:` — если есть, считать падением; падение пакета без единого листового падения — FAIL.
- **Disposition:** fix-here (1, 3, 4 — дёшево); (2) — допустимо defer-tech-debt
- **Follow-up:** n/a

### F5 — Группировка на upstream-ядре гейтом не защищена
- **Severity:** major
- **Location:** `go-app/cmd/server/testdata/upstreamcore-divergences.tsv:56-59`; `go-app/cmd/server/upstreamcore_compat.go:75-80`
- **Issue:** все 4 теста формы и фильтров `/api/v2/alerts/groups` помечены `flaky`, то есть гейт принимает любой их исход. Регрессия формы групп, `group_by`, фильтров `receiver`/`filter` на upstream-ядре в срезах 2–6 (они как раз строят на dispatcher) пройдёт незамеченной. В `internal/upstreamcore` есть только базовая проверка labels/receiver одной группы (`core_test.go:140-149`). Вердикт «группировка работает (с поправкой на асинхронность)» опирался на незакоммиченную пробу. Проба ревью: 20ms после `POST /api/v2/alerts` в обёртке харнесса → 4 теста проходят 10/10, то есть детерминировать их дёшево, и расхождение остаётся только в «eventual consistency».
- **Recommendation:** в обёртке харнесса после успешного `POST /api/v2/alerts` дожидаться, пока dispatcher примет алерты (опрос групп с ограничением ~1s, а не фиксированный sleep), записать это в baseline-шапке как известное расхождение «eventual consistency» и убрать 4 записи `flaky` — тесты станут настоящим гейтом. Механизм `flaky` оставить для будущих случаев.
- **Disposition:** fix-here
- **Follow-up:** n/a

### F6 — Spec расходится с реализацией
- **Severity:** nit
- **Location:** `tasks/UPSTREAM-CORE-POC-GATE/Spec.md` § Target Design, Edge Cases 3
- **Issue:** Spec: amtool через `go run`, свободный порт выбирается заранее, «ядра в тестах не останавливаются». Код: `go install` в `GOBIN` временного каталога, `--web.listen-address=127.0.0.1:0` + разбор лога `listening`, остановка ядер и удаление данных в `TestMain` (`upstreamcore_compat_test.go`). Реализация лучше Spec, но Spec — источник для тестов и следующих срезов.
- **Recommendation:** обновить Target Design и Edge Case 3; отметить связь гейта с форматом лога `amp-upstream` (`msg=="listening"`, `.address`) — при бампе смена сообщения даст «did not start».
- **Disposition:** fix-here
- **Follow-up:** n/a

### F7 — Неточности в причинах baseline и `divergences.md`
- **Severity:** nit
- **Location:** `go-app/cmd/server/testdata/upstreamcore-divergences.tsv:32,34,45-47`; `tasks/UPSTREAM-CORE-POC-GATE/divergences.md` (b — ошибки, поведение; d)
- **Issue:** (1) `silence_by_id_invalid_id_contract` и `TestUpstreamParity_SilenceByIDInvalidUUIDReturnsNotFound` ждут 404, а не 400 (`expected 404, got 422`), причина «422, not 400» неверна; (2) невалидный `active=` upstream не «игнорирует», а читает как `false` (`swag.ConvertBool`): `?active=garbage` молча исключает активные алерты — видимое клиенту изменение, его стоит внести в migration notes среза 7; (3) `canUpdate`: у активного silence **любое** изменение `startsAt` создаёт новый silence, не только сдвиг в прошлое; (4) класс d: `/debug/*` проксирует весь `http.DefaultServeMux` — в `amp-upstream` слинкованы и `net/http/pprof`, и `expvar` (`/debug/vars`, `cmdline`, `memstats`) — вопрос владельцу шире, чем pprof.
- **Recommendation:** поправить формулировки; дополнить вопрос d и список breaking для среза 7.
- **Disposition:** fix-here
- **Follow-up:** n/a

### F8 — Расхождение 415 без `Content-Type` не попадает в машинный baseline
- **Severity:** minor
- **Location:** `go-app/cmd/server/upstreamcore_compat.go:71-80`; `go-app/cmd/server/testdata/upstreamcore-divergences.tsv` (шапка)
- **Issue:** обёртка проставляет `Content-Type`, поэтому гейт это расхождение не видит. Запись о нём — только в `divergences.md` задачи (уйдёт в архив) и в комментарии, ссылающемся на «task's divergence table». При бампе изменение поведения upstream здесь не проявится. Скрытия других расхождений нормализация не даёт: сьюты нигде не проверяют `Content-Type` (grep), обёртка трогает только POST без заголовка.
- **Recommendation:** зафиксировать расхождение в шапке TSV (класс b) и сослаться на неё из комментария харнесса; по желанию — маленький тест в `internal/upstreamcore`, что POST без заголовка → 415, чтобы бамп его показал.
- **Disposition:** fix-here
- **Follow-up:** n/a

### F9 — Та же гонка групп в `TestCoreEndToEnd`
- **Severity:** nit
- **Location:** `go-app/internal/upstreamcore/core_test.go:140-149` (вне диффа, срез 1a)
- **Issue:** тест читает `/api/v2/alerts/groups` сразу после POST — та же асинхронность, что в F5. Локально 100/100 проходит (промежуточный GET `/alerts` даёт время), но это латентный флейк на медленном CI.
- **Recommendation:** `require.Eventually` вокруг чтения групп.
- **Disposition:** defer-bug
- **Follow-up:** BUGS.md (завести на finalize)

### F10 — Пробелы скрипта обновления: переименованные файлы, toolchain
- **Severity:** nit
- **Location:** `scripts/upstream-bump.sh:26,57-60,133-135`
- **Issue:** (1) `PORTED_FILES` — фиксированный список: если upstream переименует/разнесёт файл, дифф покажет только удаление (`-N`), новое место кода не попадёт; (2) если `go get` поднимет `go`/`toolchain`, при `GOTOOLCHAIN=auto` новый тулчейн скачивается во время `go get` — вне карантина; скрипт только предупреждает постфактум.
- **Recommendation:** при отсутствии файла в одной из версий печатать явное предупреждение «файл исчез/появился — проверь, куда переехал код»; упомянуть оба пункта в `UPSTREAM_UPGRADE.md`.
- **Disposition:** defer-backlog
- **Follow-up:** BACKLOG.md (завести на finalize)

## Re-review @ e565c4f (2026-10-24)

**Что проверено:** `git diff e885982 e565c4f` (7 файлов, +210/−59), `./scripts/upstream-core-gate.sh` — PASS (149 pass, 47 известных падений; amtool + 415 + SIGTERM ok); 4 бывших `flaky`-теста групп `-count=20` — 80/80 pass; `go test -tags futureparity ./cmd/server/` — ok (инвариант без тега держится); `go vet -tags futureparity,upstreamcore` — ok; `shellcheck`, `/bin/bash -n` (3.2) — чисто; `UPSTREAM_BUMP_MIN_AGE_DAYS=100000 upstream-bump.sh v0.34.0` — возраст считается (запрос к GitHub проходит), отказ, `go.mod`/`go.sum` возвращены; ручной прогон `amp-upstream` + `curl` для проверки 415. Дерево после проверок чистое.

| Finding | Статус | Комментарий |
|---|---|---|
| F1 | closed (fix-here) + defer-tech-debt | `age_days`: для upstream-модуля возраст = меньший из commit time и `published_at` GitHub-релиза; при недоступности GitHub — «age unknown» → отказ (fail-closed). Формулировки в скрипте, runbook и премисе Spec исправлены (премиса помечена `refuted`). Транзитивные зависимости — TECH-DEBT на finalize |
| F2 | closed | ожидание 60s, сообщение различает «жив, но не слушает» и «вышел» |
| F3 | closed | проверка `go`/`jq`/`curl` (`git` в bump); печатается `build-output`; stderr `go test` — в отдельный файл и выводится при «no test ran» |
| F4 | closed по пп. 1, 3, 4; п. 2 — частично | колонки валидируются (4 поля, класс a–d, `fails\|flaky`); любая запись, которая не запускалась, — `NOT RUN`; родитель с собственным `_test.go:NN:` в выводе считается падением; `panic:` вне тестов → FAIL. Падение уровня пакета без паники не ловится — defer-tech-debt. Побочный эффект — F12 |
| F5 | closed | `awaitDispatched` опрашивает `/alerts/groups` (≤1s, шаг 1ms) после принятого POST, пока не появятся все активные алерты из тела; по таймауту возвращается без ошибки, так что настоящая потеря алерта всё равно видна тесту. 4 записи `flaky` убраны, тесты стали настоящим гейтом. Расхождение описано в шапке TSV |
| F6 | closed | Target Design и Edge Cases 3–4 в Spec соответствуют коду |
| F7 | closed | 404, `active=` → `false`, любое изменение `startsAt`, `/debug/*` с `expvar` — в TSV и `divergences.md`; `active=` попал в breaking-список вердикта |
| F8 | closed с оговоркой | 415 описан в шапке TSV, комментарий харнесса ссылается на неё, гейт проверяет 415 на `amp-upstream`. Но проверка проверяет не тот случай — F11 |
| F9 | defer-bug | в BUGS.md на finalize |
| F10 | defer-backlog | в BACKLOG.md на finalize |

### F11 — Проверка 415 шлёт `application/x-www-form-urlencoded`, а не запрос без `Content-Type`
- **Severity:** minor
- **Location:** `scripts/upstream-core-gate.sh:175` (`curl -s -o /dev/null -w '%{http_code}' -X POST -d '[]' …`)
- **Issue:** `curl -d` сам ставит `Content-Type: application/x-www-form-urlencoded` (видно в `curl -v`: `> Content-Type: application/x-www-form-urlencoded`). Сейчас оба случая дают 415 (проверено: form → 415, `-H 'Content-Type:'` → 415, `application/json` → 200), поэтому гейт зелёный. Но если upstream начнёт принимать POST без заголовка как JSON, а form по-прежнему будет отклонять, проверка останется зелёной — т.е. она не следит за тем расхождением, которое описано в шапке baseline.
- **Recommendation:** `curl … -H 'Content-Type:' -d '[]'` (пустое значение убирает заголовок).
- **Disposition:** fix-here (одна строка; может войти в `write-tests`/`testing`, гейт не блокирует)
- **Follow-up:** n/a

### F12 — Определение «родитель упал сам» срабатывает и на `t.Log`
- **Severity:** nit
- **Location:** `scripts/upstream-core-gate.sh:85-87`
- **Issue:** шаблон `^\s+\S+_test\.go:[0-9]+: ` совпадает и со строками `t.Log`/`t.Logf`, не только с `t.Error`. Если родитель, упавший из-за известного подтеста, что-то логирует, раннер посчитает его новым падением — ложно красный (fail-closed). В текущих сьютах такого нет (гейт PASS).
- **Recommendation:** при появлении ложного срабатывания — различать по тексту или по наличию `--- FAIL` без упавших детей; пока достаточно комментария у шаблона.
- **Disposition:** defer-tech-debt
- **Follow-up:** TECH-DEBT.md (на finalize, вместе с F4 п. 2)

**Итог re-review:** блокеров нет, оба major закрыты в коде; открыты F11 (minor, fix-here одной строкой), F12 и F4 п. 2 (defer-tech-debt), F1-транзитивка (defer-tech-debt), F9 (defer-bug), F10 (defer-backlog). Gate — `pass`.

## Anti-Pattern Check

- [x] Self-audit was not treated as a substitute for independent review.

## Post-review fixes (implementer)

- F11 — исправлено: `curl -H 'Content-Type:'` снимает заголовок, проверяется именно запрос без `Content-Type`; плюс `TestCorePostWithoutContentTypeIs415` в `internal/upstreamcore` (write-tests).
