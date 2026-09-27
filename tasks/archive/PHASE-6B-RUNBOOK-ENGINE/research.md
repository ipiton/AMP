# PHASE-6B-RUNBOOK-ENGINE — Research

Дата: 2026-09-27. Основа: `requirements.md`, код `go-app/` на ветке `claude/start-task-pyym8f` (от `main` @ `beab7df`).

## 1. Что есть в коде

### Agent loop (`go-app/internal/core/investigation/agent_loop.go`)
- `AgentLLMClient.InvestigateWithTools(ctx, alert, classification, tools, history)` — единственный контракт loop → LLM.
- `Run()` стартует с **пустой** `history`; loop сам system-сообщение не создаёт.
- `trimHistory()` сохраняет только ведущее `RoleSystem`-сообщение, остальное срезает с головы (`MaxHistoryMsgs=40`).
- Конфиг loop — `DefaultAgentLoopConfig()`, из `config.yaml` не читается.

### Сборка промпта (`go-app/internal/infrastructure/llm/investigate_with_tools.go`)
- `buildOpenAIMessages()` **сам генерирует system prompt**, но только если `history[0]` не `RoleSystem`: alert JSON (`CoreAlertToLLMRequest` → labels + annotations) + classification.
- Initial user prompt («Investigate this alert…») добавляется только при `len(history)==0`.
- Следствие: если loop положит в history своё `RoleSystem`-сообщение, пропадут alert JSON и classification; если положит `RoleUser` — пропадёт стандартный user prompt, а `trimHistory` в итоге выкинет runbooks. **Инъекция через history — плохой путь.**
- Формат только OpenAI-compatible (Claude идёт через совместимый endpoint) — отдельной ветки Anthropic нет, вставлять в одно место.

### One-shot путь 5A (`client.go:521` `InvestigateAlert`)
- Свой захардкоженный system prompt, вызывается из `queue.go:215`, когда `llm.agent_mode=false`. Интерфейс `LLMClient` в `infrastructure/investigation/queue.go:21`.

### Wiring (`go-app/internal/application/service_registry.go:~2172`)
- Под `if r.config.LLM.AgentMode`: `NewToolRegistry()` → регистрация tools из `investigation.tools.*` → `NewAgentLoop(llmClient, registry, DefaultAgentLoopConfig())` → `queue.SetAgentLoop`.
- Естественная точка: загрузить runbooks здесь же и передать в `AgentLoop`.

### Конфиг (`go-app/internal/config/config.go:82`, `investigation_tools.go`)
- `InvestigationConfig` с `mapstructure`-тегами; `Tools InvestigationToolsConfig`. Пример — `config.yaml.example:108-132`. Новая секция `investigation.runbooks` ложится рядом без ломки.

### Alert model (`go-app/internal/core/interfaces.go:39`)
- ⚠️ `AlertName` — **отдельное поле**, `Labels["alertname"]` может отсутствовать. Matcher обязан брать `alertname` из `alert.AlertName` (fallback), иначе главный ключ matching'а тихо не сработает.

### Зависимости
- `gopkg.in/yaml.v3` уже прямая зависимость — frontmatter парсится без новых модулей.

### Hot reload
- Есть `config.Reloadable` / `ReloadCoordinator` (`service_registry_reload.go:222-226`), но investigation-пайплайн в reload не участвует (agent loop собирается один раз при старте). Hot reload runbooks = новый Reloadable + атомарная подмена стора — отдельный срез.

### Helm
- `helm/amp/templates/deployment.yaml` не имеет generic `extraVolumes/extraVolumeMounts`. Смонтировать ConfigMap с runbooks через чарт сейчас нельзя без правки темплейта. Код от этого не зависит (читает директорию), но «ConfigMap в K8s» из BACKLOG без чарта — только ручной патч.

## 2. Варианты

### A. Как доставить runbooks в промпт

| # | Вариант | + | − |
|---|---------|---|---|
| A1 | Расширить `InvestigateWithTools` явным параметром/структурой (напр. `PromptContext{Runbooks string}`), `buildOpenAIMessages` дописывает секцию в system prompt | Явно, тестируемо, переживает `trimHistory` (system prompt пересобирается каждую итерацию) | Меняет интерфейс: 1 prod-реализация + моки в тестах |
| A2 | Передать через `context.Context` (по образцу `WithAlertTime`) | Ноль изменений сигнатур | Бизнес-данные в ctx, неявно; прецедент слабый — `WithAlertTime` в prod вообще не вызывается (см. §5) |
| A3 | Seed `history` сообщением от loop | Без изменений в llm-пакете | Ломает alert-контекст или user prompt, срезается `trimHistory` (см. §1) |
| A4 | Runbooks как tool (`search_runbooks`/`get_runbook`) | Контекст не тратится, если не нужно | LLM может не вызвать; +1 итерация; не решает «подмешать автоматически» из требований |

**Рекомендация: A1.** A4 — как follow-up поверх того же стора (дёшево, когда движок готов).

### B. Где лежит код
- **Рекомендация:** доменные типы + matcher + рендер — чистый пакет `go-app/internal/core/investigation/runbook/` (без I/O, легко тестировать); загрузка из FS — `go-app/internal/infrastructure/investigation/runbooks/` (по аналогии с `tools/`). Путь `internal/investigation/runbooks/engine.go` из BACKLOG не соответствует текущей раскладке.
- Альтернатива (один пакет в `infrastructure/`) проще, но смешивает I/O и логику; решить на `/spec`.

### C. Семантика matching
- Все пары `match` — equality по labels (`alertname` → `alert.AlertName` fallback). Regex/glob — не в этом срезе.
- Runbook **без** `match` (пустая map) → не матчится ничем (иначе попадёт в каждый промпт); при загрузке — warn.
- Порядок: больше ключей в `match` (специфичнее) → выше; при равенстве — по `name`/пути файла (детерминизм).
- Лимиты (конфиг, с дефолтами): `max_runbooks` (напр. 3), `max_chars` на runbook (напр. 4000) с пометкой `…[truncated]`. Связано с защитами бюджета контекста из BACKLOG (обогащение 2026-09-02).

### D. Формат файла
- `*.md`, frontmatter между первыми двумя строками `---`; поля `name` (обяз.), `match` (обяз., map[string]string), `tags` (опц.). Тело — markdown как есть.
- Битый YAML / нет `name` / нет frontmatter → файл пропускается, warn в лог, счётчик ошибок; сервис стартует.
- Рекурсивный обход директории — да (ConfigMap монтирует плоско, но git-sync/volume может быть с подпапками). Симлинки ConfigMap (`..data`) — учесть: пропускать скрытые `..*` пути, читать через resolved-файлы.

### E. Путь 5A (one-shot, `agent_mode=false`)
- Можно подмешать runbooks и в `InvestigateAlert`, но это второй интерфейс (`queue.go:21`) и второй промпт. **Рекомендация:** в срезе только agent mode (так в требованиях), 5A — follow-up в BACKLOG.

### F. Связь с `runbook_url`
- Аннотации уже попадают в alert JSON промпта — LLM ссылку видит. Внешний URL не скачиваем (сеть, security). Ничего не делаем; упомянуть в docs.

## 3. Риски
- **Бюджет контекста:** runbooks в system prompt повторяются каждую итерацию (до 10) → лимиты обязательны, дефолты консервативные.
- **Prompt injection:** содержимое runbooks — доверенный операторский контент (уровень доверия = config). Labels алерта и так уже в промпте. Новой поверхности нет, но в docs отметить, что каталог должен быть под контролем оператора.
- **Изменение интерфейса `AgentLLMClient`:** затрагивает моки в `agent_loop_test.go` и, возможно, другие тесты — механическая правка.
- **Молчаливый no-match по `alertname`** — закрывается fallback'ом + тестом.

## 4. Импликации для /spec
1. Зафиксировать A1 (форма параметра: отдельная структура, чтобы не менять сигнатуру снова в 5C/6D).
2. Раскладка пакетов (B), конфиг:
   ```yaml
   investigation:
     runbooks:
       enabled: false
       path: /etc/amp/runbooks
       max_runbooks: 3
       max_chars: 4000
   ```
   Требует `llm.agent_mode=true` (как tools).
3. Контракты: `Runbook{Name, Match, Tags, Body, Source}`, `Match(alert) []Runbook`, `Render([]Runbook) string`.
4. Трассировка: записывать имена matched runbooks (лог + по возможности в `AgentRunResult`/steps), чтобы было видно, что подмешано. Решить, нужно ли в БД.
5. Acceptance: unit (парсер, matcher, лимиты, битые файлы, `alertname` fallback), тест `buildOpenAIMessages` с runbooks, тест loop прокидывает контекст, выключено → промпт байт-в-байт прежний.

## 5. Попутные находки (вне скоупа)
- 🐞 **`WithAlertTime` нигде не вызывается в prod** (`grep` по `go-app`: только определение). Tools (prometheus/loki) якорят временной диапазон на `time.Now()` вместо времени срабатывания алерта — для отложенных/ретраенных расследований окно сдвигается. Кандидат в `BUGS.md` (завести на `/spec` или `/end-task`).
- ⚠️ Initial user prompt живёт только в первой итерации (`len(history)==0`); со 2-й итерации последовательность `system → assistant → tool…` без user-сообщения. OpenAI это принимает; для строгих провайдеров — потенциальный риск. Наблюдение, не баг без репро.
- Helm: нет `extraVolumes`/`extraVolumeMounts` → монтирование runbooks ConfigMap требует доработки чарта (follow-up, связан с `HELM-CHART-GAPS`).

## 6. Изменился ли скоуп
Скоуп **не расширился**, уточнён и частично сужен:
- В срез: engine (parse/load/match/render) + конфиг + инъекция в agent-mode system prompt через явный параметр.
- Из среза (→ BACKLOG): tool `search_runbooks` (A4), runbooks в 5A one-shot, hot reload, regex-matching, Helm `extraVolumes` для ConfigMap.
- Оценка ~2d сохраняется; при желании нарезать: (1) engine+config, (2) injection+wiring.
