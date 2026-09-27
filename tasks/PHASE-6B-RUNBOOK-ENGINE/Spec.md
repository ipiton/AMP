# Spec: PHASE-6B-RUNBOOK-ENGINE

Статус: draft → ждёт approval перед `/plan`.
Основа: `requirements.md`, `research.md` (2026-09-27).

## 1. Problem
Investigation-агент (agent mode, Phase 5B/6A) расследует алерт только инструментами
и общими знаниями модели. Командное знание — типовые причины, шаги проверки,
ремедиация для конкретных алертов — ему недоступно. Операторам некуда положить
«как мы разбираем `HighMemoryUsage` в prod», чтобы агент это учитывал.

## 2. Goals
1. Markdown-runbooks с YAML frontmatter в директории на диске.
2. Автоматический подбор runbooks по labels алерта.
3. Инъекция подобранных runbooks в system prompt агента на каждой итерации.
4. Выключено или нет совпадений ⇒ промпт **байт-в-байт** как сейчас.

## 3. Non-Goals (→ BACKLOG на `/end-task`)
- Tool `search_runbooks` / `get_runbook` (вариант A4).
- Runbooks в one-shot пути 5A (`InvestigateAlert`, `agent_mode=false`).
- Hot reload runbooks (SIGHUP / `Reloadable`) — только загрузка при старте.
- Regex/glob/negative matching.
- Helm `extraVolumes`/`extraVolumeMounts` для монтирования ConfigMap.
- Запись matched runbooks в БД / API investigation.
- Скачивание `runbook_url` из аннотаций.
- Исправление `WithAlertTime` (заводится отдельно в `BUGS.md`).

## 4. Key Decisions
| ID | Решение | Почему |
|----|---------|--------|
| D1 | Runbooks передаются в LLM-клиент **явным параметром** — новой структурой `PromptContext` в `InvestigateWithTools` | Инъекция через history ломает alert-контекст/user prompt и срезается `trimHistory`; ctx-value неявен (research §2A) |
| D2 | `PromptContext` — структура, а не строка | 5C/6D добавят поля без повторной смены сигнатуры |
| D3 | Секция runbooks дописывается **в конец** system prompt в `buildOpenAIMessages` | System prompt пересобирается каждую итерацию ⇒ переживает `trimHistory` |
| D4 | Логика (parse/match/render) — `internal/core/investigation/runbook/` без I/O; загрузка — `internal/infrastructure/investigation/runbooks/` | Разделение как у `tools/`; логика тестируется без FS |
| D5 | Matching — equality по всем ключам `match`; `alertname` берётся из `Labels["alertname"]`, иначе из `alert.AlertName` | `AlertName` — отдельное поле модели (research §1) |
| D6 | Runbook с пустым `match` отклоняется при загрузке (warn) | Иначе попадёт в каждый промпт |
| D7 | Сортировка: число ключей `match` ↓, затем `name` ↑, затем `Source` ↑ | Специфичность + детерминизм |
| D8 | Ошибки отдельных файлов не фатальны; фатальна только недоступность корня `path` при `enabled=true` → warn + runbooks отключены, сервис стартует | Не ронять сервис из-за контента; поведение как у k8s tool (`service_registry.go` — warn и skip) |
| D9 | Runbooks работают только при `llm.agent_mode=true` (как tools) | Точка инъекции есть только в agent path |

## 5. Scope

### 5.1 Формат runbook
Файл `*.md` (регистр расширения игнорируется). Первая строка — `---`, затем YAML, затем строка `---`, затем тело.
```markdown
---
name: High Memory Usage          # обязательно, непустое
match:                           # обязательно, map[string]string, ≥1 ключ
  alertname: HighMemoryUsage
  severity: critical
tags: [memory, oom]              # опционально
---
## Symptoms
...
```
- Неизвестные поля frontmatter игнорируются.
- Тело — trim пробелов по краям; пустое тело допустимо (warn не нужен).
- Невалидно (файл пропускается, warn + счётчик): нет frontmatter/закрывающего `---`, битый YAML, пустой `name`, пустой `match`, пустое значение-ключ в `match`.
- Файл > 64 KiB — пропускается (защита от случайного мусора).

### 5.2 Загрузка
- Рекурсивный обход `path`; пропускаются каталоги и файлы с именем, начинающимся с `.` (покрывает `..data`/`..2026_…` ConfigMap); симлинки на файлы читаются (ConfigMap-файлы — симлинки), симлинки на каталоги не обходятся.
- Порядок загрузки детерминирован (`filepath.WalkDir` лексикографический).
- Дубликаты `name` допустимы (различаются `Source`).
- Результат загрузки логируется: `loaded`, `skipped`, `path`.

### 5.3 Контракты (core, `internal/core/investigation/runbook`)
```go
type Runbook struct {
    Name   string
    Match  map[string]string
    Tags   []string
    Body   string
    Source string // относительный путь файла, для логов/детерминизма
}

func Parse(source string, data []byte) (Runbook, error)

type Set struct{ /* immutable, sorted */ }
func NewSet(rbs []Runbook) *Set
func (s *Set) Len() int
// Match возвращает совпавшие runbooks в порядке D7, не более limit (limit<=0 ⇒ все).
func (s *Set) Match(alert *core.Alert, limit int) []Runbook

// Render строит текст секции для system prompt; каждое тело обрезается до maxChars
// рун с суффиксом "\n…[truncated]". Пустой вход ⇒ "".
func Render(rbs []Runbook, maxChars int) string
```
Формат `Render` (фиксирован, покрыт golden-тестом):
```
Relevant runbooks (operator-provided guidance matched by alert labels; verify against tool data before relying on it):

### Runbook: <Name>
<Body>

### Runbook: <Name2>
<Body2>
```

### 5.4 Контракты (core, `internal/core/investigation`)
```go
// PromptContext carries optional extra context for the investigation prompt.
type PromptContext struct {
    Runbooks string // rendered runbook section; "" ⇒ none
}

type AgentLLMClient interface {
    InvestigateWithTools(ctx, alert, classification, tools, history, pc PromptContext) (*AgentResponse, error)
}
```
`AgentLoop` получает runbooks через `SetRunbooks(set *runbook.Set, maxRunbooks, maxChars int)` (nil ⇒ выключено). В `Run()` **один раз** до цикла: `matched := set.Match(alert, maxRunbooks)`, `pc.Runbooks = runbook.Render(matched, maxChars)`; тот же `pc` передаётся во все итерации.
- `AgentRunResult` получает поле `RunbooksUsed []string` (имена matched). Queue логирует их в `Investigation completed`/`Agent loop returned error`. В БД не пишется.
- `core/investigation` → `core/investigation/runbook` — зависимость внутри core, циклов нет (runbook импортирует только `core`).

### 5.5 LLM-клиент (`internal/infrastructure/llm/investigate_with_tools.go`)
- `InvestigateWithTools` принимает `pc inv.PromptContext` и передаёт в `buildOpenAIMessages`.
- В ветке генерации system prompt: если `pc.Runbooks != ""`, дописать `"\n\n" + pc.Runbooks`. Иначе строка не меняется.

### 5.6 Конфиг
`internal/config/investigation_runbooks.go` (или рядом с tools):
```go
type InvestigationRunbooksConfig struct {
    Enabled     bool   `mapstructure:"enabled"      yaml:"enabled"`
    Path        string `mapstructure:"path"         yaml:"path"`
    MaxRunbooks int    `mapstructure:"max_runbooks" yaml:"max_runbooks"`
    MaxChars    int    `mapstructure:"max_chars"    yaml:"max_chars"`
}
// InvestigationConfig.Runbooks InvestigationRunbooksConfig `mapstructure:"runbooks" yaml:"runbooks,omitempty"`
```
Дефолты (`viper.SetDefault`): `enabled=false`, `path=/etc/amp/runbooks`, `max_runbooks=3`, `max_chars=4000`.
Нормализация при wiring: `max_runbooks<=0 ⇒ 3`, `max_chars<=0 ⇒ 4000` (без ошибок валидации — не ломаем старт).
`config.yaml.example`: закомментированный пример секции `investigation.runbooks` под `tools`.

### 5.7 Wiring (`internal/application/service_registry.go`, блок agent mode)
Если `Investigation.Runbooks.Enabled`: `runbooks.LoadDir(path, logger)` → при ошибке корня warn и skip (D8); при `set.Len()==0` info «no runbooks loaded»; иначе `agentLoop.SetRunbooks(...)` + info с количеством. Если `Enabled` и `!LLM.AgentMode` — warn «runbooks require llm.agent_mode=true».

### 5.8 Метрики
Минимум, без новых подсистем: не добавляем Prometheus-метрики в этом срезе (логов достаточно для ~2d); счётчики — follow-up, если понадобятся. *(Отклонение от requirements «лог/метрика» — выбираем лог.)*

## 6. Acceptance Criteria
- [ ] AC1 `Parse`: валидный файл → все поля; каждое условие невалидности из §5.1 → ошибка.
- [ ] AC2 `Set.Match`: полное совпадение, частичное (нет матча), лишние labels у алерта (матч), `alertname` только в `AlertName` (матч), `alertname` в labels имеет приоритет, `limit` соблюдается, порядок D7.
- [ ] AC3 `Render`: golden-формат, обрезка по `maxChars` в рунах (кириллица не рвётся), пустой вход ⇒ `""`.
- [ ] AC4 `LoadDir`: рекурсия, пропуск `.`-путей, не-`.md`, битых и >64 KiB файлов; несуществующий корень ⇒ ошибка; симлинк-файл читается (тест пропускается, если ОС не даёт симлинки).
- [ ] AC5 `buildOpenAIMessages`: с `pc.Runbooks` — секция в конце system; без — вывод идентичен текущему (регрессионный тест на точную строку).
- [ ] AC6 `AgentLoop`: с set — LLM-мок получает непустой `pc.Runbooks` на **каждой** итерации, `RunbooksUsed` заполнен; без set — `pc` пустой, поведение прежнее.
- [ ] AC7 Конфиг: дефолты применяются; секция парсится из YAML.
- [ ] AC8 `go build ./...`, `go vet` и `go test` затронутых пакетов (`core/investigation/...`, `infrastructure/investigation/...`, `infrastructure/llm`, `config`, `application`) зелёные; `git diff --check` чист; нет игнорируемых ошибок в diff.
- [ ] AC9 Docs: `config.yaml.example`, `internal/core/investigation/README.md` (раздел Runbooks + пример файла + заметка о доверии к каталогу), `CHANGELOG.md [Unreleased] → Added`.
- [ ] AC10 Planning на `/end-task`: follow-ups из §3 в BACKLOG, `WithAlertTime` в BUGS.md.

## 7. Risks
| Риск | Митигация |
|------|-----------|
| Рост контекста: runbooks × до 10 итераций | `max_runbooks=3`, `max_chars=4000` ⇒ ≤ ~12k символов; дефолт `enabled=false` |
| Смена интерфейса `AgentLLMClient` | 2 реализации (HTTP + тестовый мок) — механическая правка |
| Prompt injection через runbooks | Каталог = доверенный операторский контент (уровень config); явная пометка в тексте секции и docs |
| Тихий промах по `alertname` | D5 + AC2 |
| Регрессия промпта при выключенной фиче | AC5 — точное сравнение строки |

## 8. Slicing
Укладывается в ~2d одним PR. Если на `/implement` выйдет больше — резать по шву:
(1) `runbook` + `runbooks.LoadDir` + конфиг (без wiring), (2) `PromptContext` + loop + wiring.
