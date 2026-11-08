# Deep Review Findings: HA upstream-ядра на memberlist (срез 4 ADR-019)

**Trigger classification:** mandatory (Full tier: сигналы `C X R`; плюс diff > 200 строк и первое применение `cluster.Peer`)
**Reviewer perspective:** independent subagent (general-purpose)
**Reviewed at:** 2026-11-08
**Reviewed tree:** feature/upstream-ha-memberlist @ c46d7e06163c579f7c279fd33a315736a88f57ed (повторная проверка исправлений; первый проход — @ 6af8171974f5e488504b8dbb41ba8c09f8e08900, verdict `fix_required`)
**Verdict:** pass (see `review-verdict.json`)

Объём: `git diff 14fc041..HEAD` (73aa9b0, 52cb3e0, 6af8171). Сверка с `github.com/prometheus/alertmanager@v0.34.1` из модульного кэша: `app/app.go`, `app/cluster.go`, `app/options.go`, `cluster/cluster.go`, `notify/notify.go`, `httpserver/httpserver.go`, `cmd/alertmanager/main.go`.

Что проверено и подтверждено (не находки):

- Порт кластера в `core.go` построчно совпадает с `app/app.go:202-246,299-302,339-360,391-401,441-444` (порядок `Create` → `onStop(Leave)` → `clusterEnabled` → `AddState("nfl")` → `AddState("sil")` → `Join` → `go Settle`, nil-интерфейсы для `api.Options.Peer` и `notify.Peer`). Порядок остановки: `Leave` зарегистрирован первым и выполняется последним, после финальных снапшотов (`close(stopc)`) — как у upstream. P1, P2 подтверждены (`notify.go:163-215`: `GossipSettleStage` → … → `ClusterWaitStage` → `DedupStage` по общему nflog).
- Флаги `--cluster.*`: имена, help и умолчания совпадают с `cmd/alertmanager/main.go:72-89` (исключение — семантика нуля, F2).
- Локальный прогон двух бинарей (`127.0.0.1:19194/19294`): `/api/v2/status` → 2 пира, `alertmanager_cluster_enabled 1`, `alertmanager_cluster_members 2`, SIGTERM без ошибки `Leave`.
- `go build`, `go vet ./internal/upstreamcore/ ./cmd/amp-upstream/` — чисто.
- Чарт: `helm template` по умолчанию идентичен базе `14fc041` (кроме случайного пароля); все 15 существующих `tests/render-*.sh` зелёные; `helm lint` с `enabled=true, replicas=3` чист; `replicas=0` падает с понятной ошибкой; префикс external URL попадает в пробы (`/am/-/healthy`); `volumeClaimTemplates` без chart-версии в метках (совместимо с HELM-VCT-CHART-LABELS); селекторы upstream-подов (`name: <name>-upstream`, `component: upstream-core`) не пересекаются с `amp.selectorLabels` основного Service/NetworkPolicy/PDB/ServiceMonitor и наоборот (P5 подтверждена).
- Адреса: `--cluster.advertise-address=[$(POD_IP)]:9094` — `net.SplitHostPort` принимает IPv4 в скобках, `setInitialFailed` сравнивает с `host:port` без скобок, т.е. себя в список failed не добавляет (P6 правдоподобна, класс `assumed` честный; финальная проверка — выкатка). Нерезолвящийся DNS пира не блокирует старт (`resolvePeers`: ошибка lookup → адрес как есть, затем reconnect через `setInitialFailed`).
- `readOnlyRootFilesystem` + `fsGroup: 65534`: пишется только `/data` (PVC), memberlist на диск не пишет.
- P7: ветки 2b (`feature/upstream-ai-history-investigation`) и 3 (`feature/upstream-non-upstream-receivers`) не трогают `Dockerfile`, `helm/`, `deploy/e2e-ha` — подтверждено `git diff 14fc041...<branch>`.

## Findings

### F1 — В коммит попал 43 МБ Mach-O бинарь `go-app/amp-upstream`
- **Severity:** blocker
- **Location:** `go-app/amp-upstream` (коммит 52cb3e0), `.gitignore:226-232`
- **Issue:** локальный артефакт `go build ./cmd/amp-upstream` (Mach-O arm64, 43 631 986 байт) закоммичен вместе с кодом кластера. `.gitignore` игнорирует `go-app/server` и `go-app/bin/`, но не `go-app/amp-upstream`. После мержа бинарь навсегда остаётся в истории `main` (репозиторий уже однажды переписывался для чистки истории); к тому же это платформенно-зависимый, невоспроизводимый файл в исходниках. Ветка ещё не запушена (`git branch -r --contains 52cb3e0` пусто), так что исправление дешёвое.
- **Recommendation:** переписать ветку так, чтобы файла не было ни в одном коммите (например, `git rebase` с fixup-коммитом на 52cb3e0, удаляющим файл, или пересборка 52cb3e0 через `reset`/`commit`), и добавить `go-app/amp-upstream` в `.gitignore` рядом с `go-app/server`. Проверка: `git log --all --stat -- go-app/amp-upstream` по ветке пусто. После перезаписи — новый коммит HEAD, deep-review повторить (verdict привязан к коммиту).
- **Disposition:** fix-here
- **Follow-up:** n/a
- **Fix verified (c46d7e0):** история переписана: `60de6b8` (бывший 52cb3e0) и `c58ef51` (бывший 6af8171); `git diff 6af8171 c58ef51` — только удаление `go-app/amp-upstream` и строка `go-app/amp-upstream` в `.gitignore`. `git ls-tree` ни одного коммита `14fc041..HEAD` файла не содержит, `git log --all -- go-app/amp-upstream` пусто. Закрыто.

### F2 — Нулевые длительности `--cluster.*` ведут себя не как у upstream: `--cluster.settle-timeout=0s` ждёт минуту
- **Severity:** major
- **Location:** `go-app/internal/upstreamcore/core.go:185-194` (`Options.defaults`), `go-app/cmd/amp-upstream/main.go` (`clusterFlags`)
- **Issue:** `defaults()` заменяет любое значение `<= 0` на умолчание, и флаги бинаря идут через неё. У upstream (`app/options.go:204-229`) иначе: `SettleTimeout` намеренно не проверяется — ноль означает «не ждать settle», на этом стоят acceptance-тесты upstream (`--cluster.settle-timeout=0s`); остальные кластерные длительности `<= 0` при включённом кластере — ошибка старта, а не тихая подмена. Проверено прогоном: с `--cluster.settle-timeout=0s` через 6 с `/api/v2/status` отдаёт `cluster.status=settling`, лог `gossip not settled but continuing anyway ... elapsed 4.5s` (у upstream settle завершился бы сразу). Это расхождение контракта флагов (сигнал `C`, требование «те же имена и умолчания» и Constraint «имена флагов как у upstream»), и оно прямо ударит по запланированному в 7.1 тесту двух реплик и по `e2e-ha`, если в них захотят отключить settle по образцу upstream. P1 («ровно шаги upstream») формально про `app.go`, но пропускает `app/options.go`.
- **Recommendation:** повторить upstream: не подставлять умолчание для `SettleTimeout` (ноль = без ожидания; бинарь и так передаёт 1m по умолчанию из флага; документировать в комментарии `Options`), а для остальных кластерных длительностей при непустом `ClusterBindAddr` — либо вернуть ошибку, как `app.Options.validate`, либо оставить подмену только для программного API, но тогда отклонять явный `0` в `clusterFlags`. Покрыть в write-tests (`settle-timeout=0` → `cluster.status=ready` сразу).
- **Disposition:** fix-here
- **Follow-up:** n/a
- **Fix verified (c46d7e0):** `defaults()` больше не подменяет `SettleTimeout` (комментарий `Options` и `doc.go` § Known differences обновлены); `validateClusterFlags` в `cmd/amp-upstream` отклоняет `<= 0` для девяти длительностей при непустом `--cluster.listen-address`, список совпадает с `app/options.go:211-224`, `SettleTimeout` исключён, как у upstream. Прогон: `--cluster.peer-timeout=0s` с кластером → `--cluster.peer-timeout must be positive when clustering is enabled`; с `--cluster.listen-address=` тот же флаг принят (`cluster.status=disabled`); `--cluster.settle-timeout=0s` → `gossip not settled but continuing anyway ... elapsed 1458ns`, через 3 с `cluster.status=ready`. `go vet`, `go test ./internal/upstreamcore/` зелёные. Остаточное расхождение (программный `Options` с нулями получает умолчания, а не ошибку) задокументировано в `doc.go`. Закрыто.

### F3 — values.yaml советует слать алерты в ClusterIP Service, README — во все поды
- **Severity:** minor
- **Location:** `helm/amp/values.yaml` (шапка секции `upstreamCore`: «point Prometheus at <fullname>-upstream:9093») против `helm/amp/README.md` § Upstream Core и комментария к `replicas`
- **Issue:** противоречивое указание оператору. При `replicas > 1` отправка через ClusterIP Service доставляет алерт в один под: если тот на позиции k, уведомление задерживается на `k × peer_timeout`, а при его гибели алерт теряется до повторной отправки Prometheus. Модель HA upstream требует, чтобы Prometheus слал во все реплики.
- **Recommendation:** в шапке values написать то же, что в README: при одной реплике — Service, при нескольких — все поды через headless Service (`dns_sd`/`static_configs` с `<fullname>-upstream-<i>.<headless>`).
- **Disposition:** fix-here
- **Follow-up:** n/a
- **Fix verified (c46d7e0):** шапка `upstreamCore` в `values.yaml`: Service при одной реплике, каждый под `<fullname>-upstream-<i>.<fullname>-upstream-headless:9093` при нескольких — согласовано с README. Закрыто.

### F4 — Инструменты апгрейда upstream не знают о новых портированных файлах
- **Severity:** minor
- **Location:** `scripts/upstream-bump.sh:28` (`PORTED_FILES`), `docs/UPSTREAM_UPGRADE.md:39-40`
- **Issue:** порт теперь включает `app/cluster.go` (`clusterWait` в `core.go`), умолчания из `app/options.go` (`DefaultOptions`, правила валидации — см. F2) и флаги `--cluster.*` из `cmd/alertmanager/main.go`. `doc.go` уже упоминает `cluster.go`, но `PORTED_FILES` и список в `UPSTREAM_UPGRADE.md` — нет. При бампе upstream изменение имени/умолчания флага кластера или порядка шагов не попадёт в diff, который скрипт показывает сопровождающему.
- **Recommendation:** добавить `app/cluster.go app/options.go cmd/alertmanager/main.go` в `PORTED_FILES`, обновить список в `UPSTREAM_UPGRADE.md` (куда что переносится: `cluster.go`/`options.go` → `internal/upstreamcore`, флаги → `clusterFlags` в `cmd/amp-upstream`).
- **Disposition:** fix-here
- **Follow-up:** n/a
- **Fix verified (c46d7e0):** `PORTED_FILES` дополнен `app/cluster.go app/options.go cmd/alertmanager/main.go`; `docs/UPSTREAM_UPGRADE.md` описывает, куда переносятся `cluster.go`/`options.go` (с `validateClusterFlags`) и флаги (`clusterFlags`). Закрыто.

### F5 — В превью StatefulSet нет PDB и разнесения по нодам
- **Severity:** minor
- **Location:** `helm/amp/templates/upstream-statefulset.yaml`
- **Issue:** `affinity`/`nodeSelector`/`tolerations` из values применяются только к Deployment, PDB выбирает только `amp`-поды. На многонодовом кластере все реплики upstream-ядра могут оказаться на одной ноде, а drain выселит их одновременно — история «потеря одной реплики не теряет уведомления» (User Story 1) на такой раскладке не держится. На целевом single-node k3s неактуально, перенос PDB — в Non-Goals (срез 5).
- **Recommendation:** в срезе 5 явно включить в объём: PDB (`maxUnavailable: 1`), `podAntiAffinity` по `amp.upstream.selectorLabels`, `nodeSelector`/`tolerations`. До тех пор — одна строка в README § Upstream Core.
- **Disposition:** defer-backlog
- **Follow-up:** BACKLOG / объём `UPSTREAM-CORE-REMOVAL` (срез 5) — внести на `finalize`

### F6 — Метрики кластера никто не собирает, API и gossip без аутентификации и вне NetworkPolicy
- **Severity:** minor
- **Location:** `helm/amp/templates/upstream-service.yaml`, `helm/amp/README.md` § Upstream Core, Spec § Observability
- **Issue:** Spec называет метрики `alertmanager_cluster_*` «бесплатными», но в чарте их не скрейпит ни один ServiceMonitor (основной выбирает только `amp`-Service), так что `members`/`health_score`/`peer_position` в выкатке не видны (линза `R`). Одновременно upstream-поды не покрыты NetworkPolicy (`values-production.yaml` её включает — оператор может решить, что покрыт весь релиз), API 9093 без `web.config` (порт его не поддерживает), gossip 9094 без TLS: любой под в кластере может создавать silences через API или gossip. Для выключенного по умолчанию превью это допустимо (Spec § Security, Non-Goals), но в README не сказано.
- **Recommendation:** в README § Upstream Core перечислить, чего превью не получает: ServiceMonitor, NetworkPolicy, аутентификация API, TLS gossip (только через `extraArgs`). ServiceMonitor/NetworkPolicy — в объём среза 5.
- **Disposition:** fix-here (строка в README); перенос ServiceMonitor/NetworkPolicy — уже в Non-Goals среза 5
- **Follow-up:** n/a
- **Fix verified (c46d7e0):** README § Upstream Core и шапка `values.yaml` перечисляют, что превью не покрыто ServiceMonitor, NetworkPolicy, PDB, аутентификацией API и шифрованием gossip без `--cluster.tls-config`, с переносом в срез 5. Закрыто.

### F7 — `amp-upstream` в образе без версии: пустые `versionInfo` и `alertmanager_build_info`
- **Severity:** minor
- **Location:** `Dockerfile:37-42`
- **Issue:** бинарь собирается без `-X github.com/prometheus/common/version.*`, поэтому `/api/v2/status.versionInfo.*` и `alertmanager_build_info{version}` пусты. Раньше это касалось только локальной сборки; теперь бинарь поставляется в образе, а клиенты, читающие версию (amtool, дашборды), видят пустую строку (линза `C`). В комментарии Dockerfile отложено на срез 5; в BACKLOG есть смежная `STATUS-VERSIONINFO-CONTRACT` (срез 6).
- **Recommendation:** записать в TECH-DEBT с привязкой к `STATUS-VERSIONINFO-CONTRACT` на `finalize`, либо добавить `-X` с `VERSION`/`COMMIT`/`BUILD_DATE` сейчас (ARG уже есть в builder).
- **Disposition:** defer-tech-debt
- **Follow-up:** TECH-DEBT.md — завести на `finalize`

### F8 — `e2e-ha` перестал проверять HA ядра, которое чарт по-прежнему ставит по умолчанию
- **Severity:** minor
- **Location:** `deploy/e2e-ha/*` (удалены `config.yaml` и Redis/PG-сценарий)
- **Issue:** до среза 5 рабочей нагрузкой чарта по умолчанию остаётся `cmd/server` с Redis-HA (lock, heartbeat, группы, таймеры, nflog), а единственный тест реальной двухрепликовой топологии для него удалён. В `tasks/archive/PROD-DEPS-VULN` `e2e-ha` прямо назван единственным покрытием PG-пути silencing (repository-тесты — `t.Skip`). Spec § Rollout принимает этот риск, но в TECH-DEBT он не записан, а окно между мержами 4 и 5 при трёх параллельных срезах может быть не коротким.
- **Recommendation:** либо сохранить старый сценарий как `deploy/e2e-ha-legacy/` (ручной, вне CI) до среза 5, либо явно записать пробел покрытия в TECH-DEBT с закрытием в `UPSTREAM-CORE-REMOVAL`.
- **Disposition:** defer-tech-debt
- **Follow-up:** TECH-DEBT.md — завести на `finalize`

### F9 — При HA классификация AMP работает на каждой реплике до settle/dedup
- **Severity:** minor
- **Location:** `go-app/internal/upstreamcore/reloader.go` (AMP `NotifyStage` перед `GossipSettleStage`/`ClusterWaitStage`/`DedupStage`)
- **Issue:** AMP-стадия добавлена перед всей цепочкой получателя, так что при N репликах каждый flush классифицируется N раз (N вызовов LLM), до проверки nflog и silences. В чарте `--amp.config.file` не проводится (Non-Goal), поэтому сейчас это касается только ручного запуска бинаря с кластером и LLM. `research.md` фиксирует follow-up `UPSTREAM-AI-HA-DEDUP`, но в BACKLOG его пока нет.
- **Recommendation:** на `finalize` завести `UPSTREAM-AI-HA-DEDUP` в BACKLOG (как запланировано в 8.2) и упомянуть умножение вызовов LLM в README/доке бинаря.
- **Disposition:** defer-backlog
- **Follow-up:** BACKLOG.md `UPSTREAM-AI-HA-DEDUP` — завести на `finalize`

### F10 — Конфликты мержа со срезами 2b и 3 в `main.go`/`core.go`
- **Severity:** nit
- **Location:** `go-app/cmd/amp-upstream/main.go` (`run(...)` получил позиционный параметр `clusterOpts`), `go-app/internal/upstreamcore/core.go`
- **Issue:** `git merge-tree` HEAD с `feature/upstream-ai-history-investigation` даёт конфликты в `main.go` и `core.go`, с `feature/upstream-non-upstream-receivers` — в `main.go`. Ожидаемо (Spec § Impact), но изменение сигнатуры `run` добавляет конфликт, которого можно было избежать.
- **Recommendation:** при мерже второй ветки свести параметры `run` к одному `upstreamcore.Options`, заполненному в `main`, чтобы следующий срез не правил сигнатуру снова.
- **Disposition:** reject (reason: ожидаемо и учтено в Spec § Impact Analysis; разрешается по месту при мерже, на корректность не влияет)
- **Follow-up:** n/a

### F11 — Независимое усечение `amp.upstream.fullname` и `headlessName` до 63 символов
- **Severity:** nit
- **Location:** `helm/amp/templates/_helpers.tpl` (`amp.upstream.fullname`, `amp.upstream.headlessName`)
- **Issue:** при `amp.fullname` длиной ≥ 54 оба имени усекаются к одному префиксу (`…-upstrea`) и сталкиваются; к тому же имя StatefulSet длиннее 52 символов ломает метку `controller-revision-hash`.
- **Recommendation:** при желании — `trunc 52` для fullname StatefulSet и вывод headless-имени из уже усечённого.
- **Disposition:** reject (reason: тот же паттерн, что у существующего Valkey/PG StatefulSet; такие длинные имена релиза нереалистичны для целевого развёртывания)
- **Follow-up:** n/a

## Re-review (c46d7e0)

Все fix-here (F1, F2, F3, F4, F6) проверены по коду и прогоном, новых находок в исправлениях нет. Открытых blocker нет; единственный major (F2) закрыт. Отложенные F5, F7, F8, F9 заводятся в BACKLOG/TECH-DEBT на `finalize`. Дополнительно: SIGTERM реплики с живым пиром — выход за ~2 с, снапшоты `nflog`/`silences` на диске.

## Deferred to testing

Поведенческие гипотезы, требующие прогона (не утверждаются как находки):

- P3: `cluster.Create` с `127.0.0.1:0` — фактический порт в `/api/v2/status` и работа `upstream-core-gate.sh` (проверяется тестом двух реплик и гейтом).
- P4: снапшот silences на SIGTERM и восстановление при рестарте при мёртвом пире (шаг 7 `e2e-ha`); `docker compose restart` даёт 10 с — `Leave(10s)` выполняется после снапшота, так что таймаут не должен его съесть, но это проверяет прогон.
- Стабильность `e2e-ha` на shared runner (квиет-период 12 с при `peer-timeout` 5 с).
- Rollout StatefulSet при `/-/ready`, не ждущем settle (как у upstream): последовательный перекат 2–3 подов не теряет nflog — проверяется выкаткой в k3s.

## Anti-Pattern Check

- [x] Self-audit was not treated as a substitute for independent review.
