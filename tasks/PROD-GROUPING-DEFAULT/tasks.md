---
id: PROD-GROUPING-DEFAULT
slug: prod-grouping-default
stream: Parity / Grouping
type: bug
status: active
created_at: 2026-10-10
updated_at: 2026-10-10
based_on:
  - requirements.md
  - research.md
  - Spec.md
---

# Implementation Plan: группировка включена по умолчанию

**Based on:** requirements.md / research.md / Spec.md v1.3
**Date:** 2026-10-10

Пути — от корня репозитория. Go-команды выполняются из `go-app/`. Оценка: ~0.5–1d → ~2d после расширения scope (цепочка `group_interval`, 2026-10-10); срез не выделяется.

## Touched Files

- `go-app/internal/config/config.go` — дефолт `grouping.enabled`, doc-комментарий `GroupingConfig.Enabled`.
- `go-app/internal/application/service_registry.go` — WARN в `initializeGrouping`, эффективный флаг для `AlertProcessor`, комментарии (`:199`, `:1612`).
- `go-app/internal/core/services/alert_processor.go` — комментарий `:29` («the default»).
- `go-app/internal/config/grouping_adapter_test.go` — тесты дефолта и явного выключения.
- `go-app/internal/application/service_registry_grouping_test.go` — тесты WARN, дефолта с `route:`, эффективного флага.
- `helm/amp/values.yaml` — `grouping.enabled: true`, комментарий.
- `helm/amp/values-production.yaml` — комментарий.
- `helm/amp/tests/render-grouping-default.sh` (новый) — render-тест.
- `deploy/smoke/config.yaml`, `deploy/e2e-ha/config.yaml` — комментарии.
- `docs/ALERTMANAGER_COMPATIBILITY.md`, `docs/MIGRATION_QUICK_START.md`, `docs/CONFIGURATION_GUIDE.md`, `README.md`, `helm/amp/README.md` — тексты.
- `CHANGELOG.md`, `helm/amp/CHANGELOG.md` — `[Unreleased]`.

## Phase 1: Implementation (код)

> **Wave 1** — независимые шаги

- [x] **1.1** `config.go`: `viper.SetDefault("grouping.enabled", true)`; doc-комментарий `GroupingConfig.Enabled` — включено по умолчанию, действует только при наличии `route:`, читается на старте (не hot-reload). <!-- verify: cd go-app && go build ./... && grep -n 'SetDefault("grouping.enabled", true)' internal/config/config.go -->
- [x] **1.2** `service_registry.go`, `initializeGrouping`, ветка `!Enabled`: при `r.config.HasRouteTree()` — `Warn` из Spec § Target Design п.3, иначе прежний INFO. Комментарии `:199` и `:1612` — убрать «default(s) to false». <!-- verify: cd go-app && go vet ./internal/application/... -->
- [x] **1.3** `service_registry.go`, сборка `AlertProcessorConfig` (`:2320`): `GroupingEnabled: r.config.Grouping.Enabled && r.config.HasRouteTree()` с комментарием, зачем (без `route:` нет ложного fallback-WARN). <!-- verify: cd go-app && go vet ./internal/application/... -->
- [x] **1.4** `alert_processor.go:29`: комментарий `Publisher` — убрать «(the default)». Логика не трогается. <!-- verify: git diff go-app/internal/core/services/alert_processor.go — только комментарий -->

> **Wave 2** — смоук до deep-review (новые тесты не пишем)

- [x] **1.5** Минимальная правка существующего теста, чтобы пакет собирался зелёным: `TestLoadConfig_GroupingDefaults` ждёт `true` (комментарий теста обновить). <!-- depends: 1.1 | verify: cd go-app && go test ./internal/config/... -->
- [x] **1.6** Существующие тесты затронутых деревьев зелёные. <!-- depends: 1.1–1.5 | verify: cd go-app && go vet ./internal/config/... ./internal/application/... ./internal/core/... && go test ./internal/config/... ./internal/application/... ./internal/core/... ./cmd/... -->

**Phase verification:** `make -C go-app quality-gates-fast` + `git status` (fmt ничего не переписал) + `git diff --check`.

## Phase 2: Implementation (чарт, deploy, доки)

> **Wave 1** — независимые шаги

- [x] **2.1** `helm/amp/values.yaml`: `grouping.enabled: true`; комментарий — включено по умолчанию, действует при наличии `route:`, `lite` — in-memory, `standard` — Redis, выключение возвращает прямую публикацию. `values-production.yaml:251` — формулировка «turns on» → «kept explicit». <!-- verify: helm template t helm/amp | grep 'GROUPING_ENABLED: "true"' -->
- [~] **2.2** Перенесён в фазу 4 как **4.6**: render-тест — тестовый файл, по правилу `implement` пишется после вердикта deep-review. Рендер проверен вручную: дефолт → `GROUPING_ENABLED: "true"`, `--set grouping.enabled=false` → `"false"`.
- [x] **2.3** `deploy/smoke/config.yaml:44`, `deploy/e2e-ha/config.yaml:80`: «must be explicit true (defaults to false)» → «default since PROD-GROUPING-DEFAULT; kept explicit». Значения не менять. <!-- verify: git diff deploy/ — только комментарии -->
- [x] **2.4** `docs/ALERTMANAGER_COMPATIBILITY.md`: Known Gap #13 — группировка включена по умолчанию при наличии `route:`, как отключить, флаг читается на старте, `lite` группирует in-memory; шаг миграции (`:973`) — убрать ручное включение. Проверить таблицу статусов выше по файлу на «off by default». <!-- verify: grep -n -i 'off by default\|defaults to .false.\|ignores the key' docs/ALERTMANAGER_COMPATIBILITY.md → пусто -->
- [x] **2.5** `README.md:17` (три ловушки → две), `docs/MIGRATION_QUICK_START.md:24`, `docs/CONFIGURATION_GUIDE.md` (ключ `grouping.enabled`: дефолт, не hot-reload), `helm/amp/README.md` (`:23` пример `values-small.yaml`, `:53`, `:68` «No grouping» в lite). <!-- verify: grep -rn -i 'grouping stays off\|grouping is off\|grouping.enabled. is ignored\|default false: every alert' README.md docs/*.md helm/amp/README.md → пусто -->
- [x] **2.6** `CHANGELOG.md` `[Unreleased]`: `### Changed` + запись в breaking changes / migration notes (задержка на `group_wait` 30s по умолчанию, сгруппированный payload, откат `grouping.enabled: false` + рестарт). `helm/amp/CHANGELOG.md` — дефолт values. <!-- verify: grep -n 'PROD-GROUPING-DEFAULT' CHANGELOG.md helm/amp/CHANGELOG.md -->

**Phase verification:** `helm lint helm/amp` + все `helm/amp/tests/*.sh` зелёные + `git diff --check`. _(2026-10-10: lint 0 failed, 6/6 render-тестов PASS, `quality-gates-fast` PASS, fmt ничего не переписал.)_

**Заметки implement (2026-10-10):**
- `docs/ROLLBACK_RUNBOOK.md` уже описывал `grouping.enabled` как startup-only выключатель; на fix-раундах R1–R3 дополнен (стартовый WARN, срок жизни групп в Redis, откат только образа).
- `docs/RELEASE_NOTES_v0.1.0-draft.md:167` упоминает `grouping.enabled: false` в старом контексте; draft целиком пересобирается в `PROD-RELEASE-V010` — не трогали.
- `values-production.yaml`: комментарий утверждал, что `false` «falls back to the older per-alert fan-out-to-every-target path» — устарело с wave 6 (receiver scoping), исправлено заодно с формулировкой.
- Для deep-review: `GroupingConfig.Enabled` имеет тег `yaml:"enabled,omitempty"`. Целиком `Config` в YAML не сериализуется (grep `yaml.Marshal`: только `Routing` и subset-карты), JSON-сериализация тегом не затронута — риска «явный `false` теряется при round-trip» не найдено, но стоит перепроверить.

## Phase 2b: Fix round после deep-review R1 (2026-10-10)

- [x] **2b.1** F1 — `manager_impl.go`: `onGroupIntervalExpired` и `onRepeatIntervalExpired` после flush ставят `group_interval`; `startRepeatIntervalTimer` удалён; комментарии `timer_manager.go`, `timer_models.go`. Логи таймеров → Debug. Существующий тест `resolved_prune_test.go` — утверждение о следующем таймере (`group_interval`). <!-- verify: cd go-app && go test -race ./internal/infrastructure/grouping/... -->
- [x] **2b.2** F5 — INFO без `route:` без атрибута `error`. F6 — стейл-комментарии (`alert_processor.go`, `service_registry.go`, `publish_receiver_scoping_test.go`), `CHANGELOG.md` («the default at the time»), `config.yaml.example`, дата в `README.md`. <!-- verify: cd go-app && go vet ./internal/application/... ./internal/core/... -->
- [x] **2b.3** Доки: F2 (classification), F3 (Helm: только values), F4 (`reconciliation_grace`), F8 (ссылки на открытые дефекты), F9 (rollout/rollback в HA), F10 (`--reuse-values`) — `CHANGELOG.md`, `helm/amp/CHANGELOG.md`, `docs/CONFIGURATION_GUIDE.md`, `docs/ALERTMANAGER_COMPATIBILITY.md` (включая оговорку про retry cadence). <!-- verify: git diff --check && helm lint helm/amp -->
- [x] **2b.4** Spec v1.1 (Premises 5, 8, 11–13, Target Design п.6–7, Invariants, Edge Cases 9–12, Rollout / Rollback), `requirements.md` (scope), `research.md` § 7.
- [ ] **2b.5** Follow-ups при `finalize`: BACKLOG — пронос classification в группу (F2), `noeviction` для Redis чарта (F8); TECH-DEBT — «restart required» для `grouping.*` на reload (F11).

## Phase 2c: Fix round после deep-review R2 (2026-10-10)

- [x] **2c.1** G1 — `signatureCovers` в `dedup.go`, использован в `notifyDedupLog.IsDuplicate` и `RedisNotifyLog.IsDuplicate`; doc-комментарий интерфейса. <!-- verify: evidence/group-interval-chain-probes.md -->
- [x] **2c.2** G2 — `GroupTimerManager.HasTimer` + `DefaultGroupManager.ensureGroupTimer` из `AddAlertToGroup` для существующей группы.
- [x] **2c.3** G3 — `localSent` в `DefaultGroupManager`: запись при подтверждённой отправке, чтение при ошибке `IsDuplicate`, `Forget` вместе с группой.
- [x] **2c.4** G4 — per-flush логи в Debug (`manager_impl.go`, `publishing/coordinator.go`). G8 — комментарии про `repeat_interval`-таймер.
- [x] **2c.5** Доки: G5 (migration note п.10), G6 (п.1, п.5), остаток F3 (`helm/amp/README.md`, `values.yaml`, `ROLLBACK_RUNBOOK.md`), F9 (п.9, runbook), запись Changed. Spec v1.2.
- [ ] **2c.6** Follow-ups при `finalize` (дополнение к 2b.5): BUGS — gauge активных таймеров уходит в минус при удалении группы из callback'а; гонка на `len(group.Alerts)`; TECH-DEBT — `CleanupExpiredGroups` не вызывается в проде; уточнить текст `TIMER-STORAGE-KEY-LOSS-SILENCES-FIRE` и `GROUPING-CALLBACK-TRANSIENT-LOAD-BREAKS-CHAIN` (последствие теперь ограничено `ensureGroupTimer`); конвертация legacy `repeat_interval` в `RestoreTimers`.

## Phase 2d: Fix round после deep-review R3 (2026-10-10, решение владельца: «полноценное решение корня»)

- [x] **2d.1** H1 — сигнатура dedup считается по набору получателя (`targetAlerts`), под ней же делается `RecordSent`. <!-- verify: evidence/group-interval-chain-probes.md § R3 -->
- [x] **2d.2** H6 — flush, на котором все опрошенные target'ы покрыты, удаляет resolved-алерты; без опроса (metrics-only) — нет.
- [x] **2d.3** H2/H3/D4 — `localSent` убран из менеджера; `resilientNotifyLog` (`notify_log_fallback.go`) подключён в `newNotifyLog` только для Redis; вытеснение локальных записей по TTL.
- [x] **2d.4** H8 — `alertCount(group)` вместо `len(group.Alerts)` в callback'ах таймеров и `MemoryGroupStorage.Load`. H10 — метрика `timer_rearm` после результата `StartTimer`, комментарии «exact alert set».
- [x] **2d.5** H4 — «Group publishing skipped (metrics-only mode)» возвращён на Info.
- [x] **2d.6** D3 — `deploy/e2e-ha/run.sh` шаг 4: опрос вместо фиксированного `sleep`. <!-- verify: evidence/e2e-ha.md -->
- [x] **2d.7** Доки: CHANGELOG (Changed, notes п.1, 4, 7, 10), compat-док (#13, строка про prune), `CONFIGURATION_GUIDE.md`, `ROLLBACK_RUNBOOK.md` (откат только образа). Spec v1.3 (D5–D7).
- [ ] **2d.8** Follow-ups при `finalize` (дополнение к 2b.5, 2c.6): BUGS — H5 (resolved-only нотификация для неизвестной получателю группы), символ `|` в fingerprint ломает разбор сигнатуры; TECH-DEBT — H9 (Redis GET на алерт у реплики без локального handle, логи без rate-limit), H7 (legacy `repeat_interval`-таймер не сжимается при `RestoreTimers`), ограничение `resilientNotifyLog` (устаревшая локальная запись).

## Phase 3: Deep Review

- [ ] **3.1** `/deep-review` (Spec § Deep Review: required). Фокус: установки без `route:` (нет WARN, нет degraded); Edge Case 5 (дерево не собралось); полнота поиска потребителей флага; честность migration note; доки про `lite`. <!-- depends: Phase 1, Phase 2 | verify: tasks/PROD-GROUPING-DEFAULT/review-verdict.json -->

**Phase verification:** `review-verdict.json` — `"gate": "pass"`.

## Phase 4: Tests (write-tests, после verdict pass)

- [ ] **4.1** `grouping_adapter_test.go`: явный `grouping.enabled: false` из YAML → `false`; `GROUPING_ENABLED=false` из env без файла → `false`. <!-- depends: 3.1 | verify: cd go-app && go test -run 'TestLoadConfig_Grouping' ./internal/config/... -->
- [ ] **4.2** `service_registry_grouping_test.go`: переименовать `TestInitializeGrouping_DisabledByDefault` → `…DisabledExplicitly`, добавить проверку WARN через захват логов; случай «выключено, `route:` нет» → WARN нет. <!-- depends: 3.1 | verify: cd go-app && go test -run 'TestInitializeGrouping' ./internal/application/... -->
- [ ] **4.3** Тест «конфиг из `LoadConfig` с `route:` и без `grouping:` → `initializeGrouping` поднимает groupManager (lite)». <!-- depends: 3.1 | verify: cd go-app && go test -run 'GroupingDefault' ./internal/application/... -->
- [ ] **4.4** Тест эффективного флага: включено + нет `route:` → `AlertProcessor` собран с `GroupingEnabled == false`, алерт публикуется напрямую, fallback-WARN не пишется; включено + `route:` → `true`. Если сборка процессора в registry не тестируется изолированно — вынести выражение флага в неэкспортируемый метод registry и тестировать его (отклонение записать в Spec). <!-- depends: 3.1 | verify: cd go-app && go test -run 'GroupingActive|EffectiveGrouping' ./internal/application/... -->
- [ ] **4.6** `helm/amp/tests/render-grouping-default.sh` по образцу `render-image-tag.sh`: дефолт → `"true"`; `--set grouping.enabled=false` → `"false"`; `values-production.yaml` + `tests/values-production-placeholders.yaml` → `"true"`. Исполняемый бит. <!-- depends: 3.1 | verify: helm/amp/tests/render-grouping-default.sh → 0 FAIL -->
- [ ] **4.7** `internal/infrastructure/grouping`: (а) после fire `group_interval` следующий таймер — `group_interval`; (б) неизменная группа: N flush'ей в пределах `repeat_interval` → одна публикация; (в) алерт, добавленный после первого `group_interval`, уходит на следующем flush; (г) resolve в нотифицированной группе уходит на следующем flush, группа удаляется, таймер не ставится; (д) fire legacy-таймера `repeat_interval` переводит группу на `group_interval`; (е) напоминание после `repeat_interval`. Переименовать `TestTimerChain_GroupWaitToRepeatInterval`. <!-- depends: 3.1 | verify: cd go-app && go test -race -run 'TimerChain|GroupInterval' ./internal/infrastructure/grouping/... -->
- [ ] **4.9** v1.2: `signatureCovers` (табличный: равные, подмножество, новый алерт, смена статуса, повторный firing); `IsDuplicate` обеих реализаций на суженном наборе (Redis — miniredis); `ensureGroupTimer` (нет таймера → `group_wait`; есть таймер → не тронут; ошибка `HasTimer` → не тронут; таймер только в storage → не тронут); `HasTimer`; fallback на `localSent` при ошибке `IsDuplicate` (своя отправка не повторяется, новый алерт уходит, `Forget` чистит). Исправить тесты, проходящие и на старом коде (`TestTimerChain_GroupWaitToRepeatInterval`, `TestTimerContinuation_FullChainFiresRepeatIntervalTwice`: `repeat_interval` ≫ `group_interval`). <!-- depends: 3.1 | verify: cd go-app && go test -race ./internal/infrastructure/grouping/... -->
- [ ] **4.10** v1.3: target с `send_resolved: false` через реальный `PublishingCoordinator` — частичный resolve не даёт повтора, resolved удаляется из группы; `RecordSent` получает сигнатуру набора получателя; покрытый resolved удаляется на flush без отправки, а при publisher'е, который никого не опрашивал, — нет; `resilientNotifyLog` (своя отправка при ошибке чтения → дубликат; чужая → ошибка наверх; `Forget`; вытеснение по TTL; сквозные методы); проводка: `newNotifyLog` для Redis возвращает обёртку, для in-memory — нет (snapshot-интерфейс не теряется); ingest во время flush под `-race`; `timer_rearm` со статусом `error`. Тесты на `localSent` из 4.9 заменяются тестами обёртки. <!-- depends: 3.1 | verify: cd go-app && go test -race ./internal/infrastructure/grouping/... ./internal/application/... -->
- [ ] **4.8** F7: `TestLoadConfig_MissingFile_UsesEnv` — `GROUPING_ENABLED=false` + `assert.False`. <!-- depends: 3.1 | verify: cd go-app && go test -run TestLoadConfig_MissingFile_UsesEnv ./internal/config/... -->
- [ ] **4.5** Мутационная проверка: вернуть `startGroupIntervalTimer` → `repeat_interval`-цепочку → 4.7 (а, в) падают; вернуть `GroupingEnabled: r.config.Grouping.Enabled` → 4.4 падает; вернуть дефолт `false` → 1.5 и 4.3 падают; убрать WARN → 4.2 падает. <!-- depends: 4.1–4.4 | verify: вручную, результат в tasks.md -->

**Phase verification:** `cd go-app && go test -race ./internal/config/... ./internal/application/... ./internal/core/...`

## Phase 5: Testing и Finalize

- [ ] **5.1** `/testing`: гейты `WORKFLOW.md` § Гейты AMP — `quality-gates-fast`, `scripts/release-gate.sh`, `git diff --check`. <!-- depends: Phase 4 | verify: release-gate RESULT: PASS -->
- [ ] **5.2** Smoke на дефолте (Spec Open Question): временная копия `deploy/smoke/config.yaml` без секции `grouping:` → `./deploy/smoke/run.sh` ALL PASS; вывод в `evidence/`. Если `run.sh` не принимает путь к конфигу — временно убрать секцию в рабочем дереве и откатить. <!-- depends: 5.1 | verify: evidence/smoke-lite-default.md -->
- [ ] **5.3** Стартовый лог бинаря: (а) `route:` + дефолт → «Initializing grouping subsystem...»; (б) `route:` + `grouping.enabled: false` → WARN один раз; (в) без `route:` → INFO «not started», без `error`, WARN нет.
- [ ] **5.6** `deploy/e2e-ha/run.sh` (если доступен Docker): шаги 3–6 зелёные при постоянном тике (Spec Edge Case 16); при недоступности — записать как непроверенное. <!-- depends: 5.1 | verify: evidence/e2e-ha.md -->
- [ ] **5.5** Живой бинарь, `lite`, короткие тайминги (`group_wait` 2s, `group_interval` 5s, `repeat_interval` 1h): второй алерт той же группы, отправленный после первого `group_interval`, доставлен в пределах ~`group_interval`; неизменная группа за это время не повторяется. <!-- depends: 5.1 | verify: evidence/group-interval-chain.md --> <!-- depends: 5.1 | verify: вывод в evidence/startup-logs.md -->
- [ ] **5.4** `/finalize`: `DONE.md`, BACKLOG P0 → «Закрыто», `NEXT.md` (WIP очистить, строку Queue обновить), архив workspace. <!-- depends: 5.1–5.3 -->

**Phase verification:** release-gate PASS, evidence приложены.

## Definition of Done

- [ ] All steps are complete or explicitly marked blocked/skipped
- [ ] Success criteria from `requirements.md` are covered
- [ ] Contracts from `Spec.md` are implemented or deviations are recorded
- [ ] Deep review verdict is `pass`
- [ ] Tests for changed behavior are added or updated
- [ ] Phase checks pass
- [ ] Docs/planning are updated if behavior, contracts, or process changed
