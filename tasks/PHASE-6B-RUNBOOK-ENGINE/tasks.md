# Implementation Checklist: PHASE-6B-RUNBOOK-ENGINE

Основа: `Spec.md` (approved 2026-09-27, включая отклонение §5.8 «лог вместо метрик»).
Все пути ниже — относительно `go-app/`.

## Research & Spec
- [x] Research completed — `research.md`
- [x] Spec approved — `Spec.md`

## Baseline (2026-09-27, до изменений)
- [x] `go build ./...` — ок (go1.26.0)
- [x] `go test` `internal/core/investigation/...`, `internal/infrastructure/investigation/...`, `internal/infrastructure/llm/...`, `internal/config/...` — ок

## Slice 1 — Engine + config (без wiring, поведение сервиса не меняется)
Шов из Spec §8: после этого среза можно остановиться и смержить.

- [x] 1.1 `internal/core/investigation/runbook/runbook.go`: тип `Runbook`, `Parse(source, data)` — frontmatter split, `yaml.v3`, валидации §5.1 (name, match ≥1, пустые ключи/значения, 64 KiB), trim тела. *(AC1)*
- [x] 1.2 `internal/core/investigation/runbook/set.go`: `Set`, `NewSet` (сортировка D7), `Len`, `Match(alert, limit)` с `alertname`-fallback на `alert.AlertName` (D5). *(AC2)*
- [x] 1.3 `internal/core/investigation/runbook/render.go`: `Render(rbs, maxChars)` — фиксированный заголовок, обрезка в рунах + `\n…[truncated]`, пустой вход ⇒ `""`. *(AC3)*
- [x] 1.4 `internal/infrastructure/investigation/runbooks/loader.go`: `LoadDir(path, logger) (*runbook.Set, error)` — `WalkDir`, пропуск `.`-путей и не-`.md`, симлинки-файлы читаются, каталоги-симлинки не обходятся, битые файлы → warn + skip, итоговый info `loaded/skipped`; ошибка только на недоступный корень. *(AC4)*
- [x] 1.5 `internal/config/`: `InvestigationRunbooksConfig` + поле `InvestigationConfig.Runbooks`; `viper.SetDefault` для `enabled/path/max_runbooks/max_chars`. *(AC7)*
- [x] 1.6 Проверка среза: `go build ./...`, `go vet` + `go test` для `runbook`, `runbooks`, `config`.

## Slice 2 — Prompt injection + wiring
- [x] 2.1 `internal/core/investigation/`: `PromptContext{Runbooks string}`; добавить `pc PromptContext` в `AgentLLMClient.InvestigateWithTools` (D1/D2).
- [x] 2.2 `internal/infrastructure/llm/investigate_with_tools.go`: принять `pc`, передать в `buildOpenAIMessages`; при `pc.Runbooks != ""` дописать `"\n\n"+pc.Runbooks` в конец system prompt, иначе строка неизменна (D3). *(AC5)*
- [x] 2.3 `AgentLoop`: `SetRunbooks(set, maxRunbooks, maxChars)`; в `Run()` один раз до цикла `Match`+`Render`, один `pc` на все итерации; `AgentRunResult.RunbooksUsed []string` заполняется на всех путях возврата (final/max_iterations/timeout/error). *(AC6)*
- [x] 2.4 Обновить `mockAgentLLM` в `agent_loop_test.go` под новую сигнатуру (+ захват `pc` по итерациям).
- [x] 2.5 `internal/infrastructure/investigation/queue.go`: логировать `runbooks` (`RunbooksUsed`) в `Investigation completed` и `Agent loop returned error`.
- [x] 2.6 `internal/application/service_registry.go` (блок agent mode): при `Runbooks.Enabled` — `LoadDir`, нормализация лимитов (`<=0` ⇒ 3 / 4000), ошибка корня ⇒ warn + skip (D8), пустой set ⇒ info, иначе `SetRunbooks` + info с количеством. Вне agent mode при `Enabled` — warn (D9).
- [x] 2.7 Проверка среза: `go build ./...`, `go vet ./...` для затронутых пакетов, `go test` для `core/investigation/...`, `infrastructure/investigation/...`, `infrastructure/llm/...`, `config`, `application`.

### Implementation notes (2026-09-27)
- Лимиты нормализуются методами `InvestigationRunbooksConfig.EffectiveMaxRunbooks/EffectiveMaxChars` (константы `DefaultRunbooks*` в `config`), wiring вынесен в `ServiceRegistry.configureRunbooks`.
- `Parse` дополнительно снимает UTF-8 BOM и нормализует CRLF.
- 2.7: `go build ./...`, `go vet` (application, config, core/investigation/..., infrastructure/investigation/..., infrastructure/llm) и `go test` пакетов investigation/llm/config — зелёные. `go test ./internal/application` — в `/testing`.
- Попутная находка (предсуществующая, вне скоупа): `AgentLoop.Run` на неизвестном `resp.Kind` возвращает `(nil, err)`, а `queue.processJobWithAgent` сразу читает `agentRun.TerminationKind` ⇒ nil-deref panic в воркере. Завести в BUGS.md на `/end-task` (F2).

## Testing (`/write-tests`, `/testing`)
- [x] T1 `runbook/runbook_test.go` — AC1: валидный файл; table-driven невалидные случаи §5.1; неизвестные поля игнорируются; CRLF-переводы строк.
- [x] T2 `runbook/set_test.go` — AC2: полное/частичное совпадение, лишние labels, `alertname` только в `AlertName`, приоритет labels над `AlertName`, `limit`, порядок D7, nil-алерт/пустой set.
- [x] T3 `runbook/render_test.go` — AC3: golden-строка, обрезка кириллицы по рунам, пустой вход.
- [x] T4 `runbooks/loader_test.go` — AC4 на `t.TempDir()`: рекурсия, `..data`/`.hidden`, `.txt`, битый файл, >64 KiB, несуществующий корень, симлинк-файл (skip, если симлинки недоступны).
- [x] T5 `llm` — AC5: `buildOpenAIMessages` с runbooks (секция в конце system); без runbooks — точное совпадение со строкой текущего промпта (регрессия).
- [x] T6 `agent_loop_test.go` — AC6: с set — непустой `pc.Runbooks` на каждой итерации (≥2 итерации через tool call), `RunbooksUsed`; без set — пустой `pc`; set без матча — пустой `pc` и пустой `RunbooksUsed`.
- [x] T7 `config` — AC7: дефолты; парсинг секции из YAML.
### Test notes (`/write-tests`, 2026-09-27)
- Файлы: `core/investigation/runbook/{runbook,set,render}_test.go`, `infrastructure/investigation/runbooks/loader_test.go`, `infrastructure/llm/investigate_with_tools_prompt_test.go`, `core/investigation/agent_loop_runbooks_test.go`, `config/investigation_runbooks_test.go`; мок `mockAgentLLM` записывает `PromptContext` каждой итерации.
- T4 дополнительно воспроизводит реальную раскладку ConfigMap (`..<ts>/`, симлинк `..data`, ключи-симлинки) — runbook грузится ровно один раз; симлинк на каталог не обходится.
- T5 закрепляет текущий system prompt точной строкой; `git diff main` по `buildOpenAIMessages` подтверждает, что формат не менялся, добавлен только хвост.
- T6 проверен мутацией: подмена `pc` на пустой в `Run()` валит `TestAgentLoop_RunbooksInjectedEveryIteration`.
- **Осознанно отложено:** unit-тест `ServiceRegistry.configureRunbooks` (wiring: warn при ошибке корня / вне agent mode) — поднимать `ServiceRegistry` в тесте тяжело, а вся логика ветвления — вызовы `LoadDir`/`Len`/`Effective*`, покрытые выше; проверяется `go build` + `go vet` пакета `application`. Логирование `runbooks` в queue — без теста (только поле лога).

- [x] T8 Прогон: `go vet` + `go test -race` затронутых пакетов, `go build ./...`, `git diff --check`, grep diff на `_, _ :=` / `_ =` для новых ошибок. *(AC8)*

### Testing results (`/testing`, 2026-09-27, HEAD `fdc3ccb`, go1.26.0)

**Green**
- `go build ./...` — ок.
- `go vet` — `internal/application/...`, `core/investigation/...`, `infrastructure/investigation/...`, `infrastructure/llm/...`, `config/...` — ок.
- `go test -race -count=1` — `core/investigation`, `core/investigation/runbook`, `infrastructure/investigation`, `infrastructure/investigation/runbooks`, `infrastructure/investigation/tools`, `infrastructure/llm`, `config` — все ok.
- `go test -count=1 ./internal/application/...` — `application` ok (14.9s), `application/handlers` ok.
- `go test -count=1 ./...` — все пакеты ok, кроме трёх предсуществующих (ниже).
- `golangci-lint run` (v2.14.0, собран go1.26.0, конфиг репо `.golangci.yml`) по затронутым пакетам — **0 issues**.
- `git diff --check main...HEAD` — чисто; `gofmt -l` по изменённым `.go` — чисто; `_, _ :=` / `_ =` в добавленных строках — нет.

**Red — предсуществующие, вне скоупа (воспроизведены на `main` в отдельном worktree, идентичный вывод)**
- `internal/database` `TestRunMigrations_ConcurrentReplicas_FreshDB`, `internal/infrastructure/inhibition` `TestIntegration_InhibitionStateManager_Redis`, `internal/infrastructure/repository` `TestGetTopAlerts_EmptyDatabase` — `panic: rootless Docker not found` (testcontainers; в облачном контейнере нет Docker). Ограничение окружения, не регрессия.

**Environment notes**
- Предустановленный `/usr/local/bin/golangci-lint` 2.5.0 собран go1.25 и отказывается грузить конфиг для go1.26 (`make lint` в этом окружении не работает). Линт прогнан временным бинарём v2.14.0 из scratchpad; версия отличается от 2.5.0, но набор линтеров закреплён в `.golangci.yml`.
- Реальный LLM/e2e-прогон с runbooks не выполнялся (нет LLM endpoint); поведение промпта закреплено unit-тестами T5/T6.

## Documentation (`/write-doc`)
- [x] D1 `config.yaml.example` — закомментированная секция `investigation.runbooks` под `tools` (+ «requires llm.agent_mode=true»). *(AC9)*
- [x] D2 `internal/core/investigation/README.md` — раздел Runbooks: формат, matching (`alertname` fallback, специфичность, лимиты), загрузка (ConfigMap `..data`), доверие к каталогу (prompt injection), `runbook_url` не скачивается. *(AC9)*
- [x] D3 `CHANGELOG.md` `[Unreleased] → Added` — PHASE-6B. *(AC9)*
- [x] D4 Пример runbook `examples/runbooks/high-memory-usage.md` (если каталог `examples/` подходит по конвенции; иначе только в README).

### Doc notes (`/write-doc`, 2026-09-27)
- `examples/runbooks/high-memory-usage.md` + раздел в `examples/README.md`; пример проверен временным тестом через `LoadDir` (грузится, матчится, рендерится), тест удалён.
- `config.yaml.example` — секция валидна (проверено `yaml.safe_load`).
- README пакета: раздел Runbooks (формат, matching, loading, config, trust) + Wiring; **исправлено** устаревшее утверждение «агент выставляет alert time в context» — теперь явно помечено как known gap (`WithAlertTime` не вызывается), со ссылкой на BUGS.md (заводится на `/end-task`, F2).
- `CHANGELOG.md [Unreleased] → Added` — PHASE-6B. `docs/RELEASE_NOTES_*-draft.md` не трогали: по WORKFLOW release notes собираются из `[Unreleased]` при релизе.

## Finalization (`/end-task`)
- [ ] F1 BACKLOG: follow-ups из Spec §3 — `search_runbooks` tool, runbooks в 5A, hot reload, regex matching, Helm `extraVolumes` (связать с `HELM-CHART-GAPS`), запись в БД/API, метрики runbooks.
- [ ] F2 BUGS.md: `INVESTIGATION-ALERT-TIME-NOT-SET` — `WithAlertTime` не вызывается в prod (research §5); `AGENT-LOOP-UNKNOWN-KIND-NIL-RESULT` — nil-deref в queue при неизвестном kind ответа LLM.
- [ ] F3 DONE.md запись; NEXT.md: снять из WIP; BACKLOG PHASE-6B → закрыт.
- [ ] F4 DECISIONS.md — если D1 (смена `AgentLLMClient`) считается truth-changing (скорее нет: внутренний интерфейс) — решить на `/end-task`.
- [ ] F5 Архив `tasks/PHASE-6B-RUNBOOK-ENGINE/` → `tasks/archive/`.

## Blockers / Assumptions
- Блокеров нет; baseline зелёный.
- Допущение: ветка остаётся `claude/start-task-pyym8f` (назначена сессией; конвенция `feature/<slug>` не соблюдена — зафиксировано на `/start-task`).
- Допущение: `internal/application` тесты не требуют внешних сервисов для компиляции; если `go test ./internal/application` упадёт на предсуществующем — задокументировать, не чинить.
- Риск объёма: если Slice 2 перерастёт ~1d — мержим Slice 1 отдельно (Spec §8).
