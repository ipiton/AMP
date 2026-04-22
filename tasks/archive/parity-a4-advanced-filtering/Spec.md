# PARITY-A4-ADVANCED-FILTERING — Spec

## Архитектурное решение

Фильтрация реализуется как **post-filter в хендлерах** — после вызова `store.List()`.
Store не изменяется. Это минимально инвазивный подход, совместимый с текущей in-memory
архитектурой.

Новый файл: `go-app/internal/application/handlers/matchers.go` — парсер и матчер.

---

## 1. Структуры данных

### `LabelMatcher` — разобранный матчер из query param

```go
// go-app/internal/application/handlers/matchers.go

type MatcherOp string

const (
    MatcherOpEqual    MatcherOp = "="
    MatcherOpNotEqual MatcherOp = "!="
    MatcherOpRegex    MatcherOp = "=~"
    MatcherOpNotRegex MatcherOp = "!~"
)

type LabelMatcher struct {
    Name  string
    Op    MatcherOp
    Value string
    re    *regexp.Regexp // скомпилировано при парсинге, nil для non-regex
}
```

---

## 2. API: новые query params

### `GET /api/v2/alerts`

| Параметр | Тип | Описание |
|----------|-----|----------|
| `filter` | `[]string` (multi-value) | Label matchers: `name="v"`, `name!="v"`, `name=~"r"`, `name!~"r"` |
| `status` | `string` | Существующий: `firing` / `resolved` |
| `resolved` | `bool` | Существующий: включить resolved в ответ |

Поведение:
- Несколько `filter` применяются как **AND** (все должны совпасть)
- Матчеры проверяются против `alert.Labels`
- Пустой `filter` — возвращать всё (текущее поведение)

### `GET /api/v2/silences`

| Параметр | Тип | Описание |
|----------|-----|----------|
| `filter` | `[]string` (multi-value) | Label matchers для фильтрации сайленсов |

Поведение (Alertmanager-совместимое):
- Возвращать только сайленсы, где **для каждого filter-матчера** среди matchers сайленса
  существует хотя бы один matcher с таким же `Name` и значением, которое покрывает переданный
  filter-matcher.
- Упрощённая семантика: сайленс проходит фильтр если каждый `filter` матчер применим хотя бы
  к одному из matchers сайленса по имени `Name`.

---

## 3. Функции: парсер матчеров

```go
// go-app/internal/application/handlers/matchers.go

// ParseLabelMatcher разбирает строку формата: name="value", name!="v", name=~"r", name!~"r"
// Возвращает ошибку при невалидном формате.
func ParseLabelMatcher(raw string) (*LabelMatcher, error)

// ParseLabelMatchers разбирает срез строк из query param "filter".
// Возвращает первую ошибку парсинга.
func ParseLabelMatchers(rawFilters []string) ([]*LabelMatcher, error)

// MatchesLabels возвращает true если все матчеры совпадают с labels (AND-логика).
func MatchesLabels(matchers []*LabelMatcher, labels map[string]string) bool

// MatchesSilenceMatchers возвращает true если каждый filter-matcher присутствует
// (по имени) среди matchers сайленса и совместим.
func MatchesSilenceMatchers(filters []*LabelMatcher, silenceMatchers []core.APISilenceMatcher) bool
```

### Детали реализации `ParseLabelMatcher`

Regex для парсинга: `^([a-zA-Z_][a-zA-Z0-9_]*)(=~|!~|!=|=)"(.*)"$`

Порядок операторов в regex важен: `=~` и `!~` должны идти перед `=` и `!=`.

```go
var matcherRe = regexp.MustCompile(`^([a-zA-Z_][a-zA-Z0-9_]*)(=~|!~|!=|=)"(.*)"$`)
```

При `op == "=~"` или `op == "!~"` — компилировать regex-value в `LabelMatcher.re`.

### Детали реализации `MatchesSilenceMatchers`

Semantics: сайленс проходит фильтр если каждый `filter` matcher "содержится" в матчерах
сайленса. Реализация через проверку по имени label:

```go
func MatchesSilenceMatchers(filters []*LabelMatcher, silenceMatchers []core.APISilenceMatcher) bool {
    for _, f := range filters {
        matched := false
        for _, sm := range silenceMatchers {
            if sm.Name == f.Name {
                matched = true
                break
            }
        }
        if !matched {
            return false
        }
    }
    return true
}
```

---

## 4. Изменения в хендлерах

### `handleAlertsGet` — `go-app/internal/application/handlers/alerts.go:50`

```go
func handleAlertsGet(store *memory.AlertStore, silences *memory.SilenceStore, w http.ResponseWriter, r *http.Request) {
    status := parseAlertsStatusQuery(r.URL.Query().Get("status"))
    includeResolved := parseBoolQueryLenient(r.URL.Query().Get("resolved"), false)
    if status == "resolved" {
        includeResolved = true
    }

    // NEW: parse filter matchers
    matchers, err := ParseLabelMatchers(r.URL.Query()["filter"])
    if err != nil {
        writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid filter: " + err.Error()})
        return
    }

    alerts := store.List(status, includeResolved)

    // NEW: apply label filter
    if len(matchers) > 0 {
        filtered := alerts[:0]
        for _, alert := range alerts {
            if MatchesLabels(matchers, alert.Labels) {
                filtered = append(filtered, alert)
            }
        }
        alerts = filtered
    }

    now := time.Now().UTC()
    gettableAlerts := make([]core.APIGettableAlert, 0, len(alerts))
    for _, alert := range alerts {
        gettableAlerts = append(gettableAlerts, toGettableAlert(alert, silences, now))
    }
    writeJSON(w, http.StatusOK, gettableAlerts)
}
```

### `handleSilencesGet` — `go-app/internal/application/handlers/silences.go:59`

```go
func handleSilencesGet(store *memory.SilenceStore, w http.ResponseWriter, r *http.Request) {
    // NEW: parse filter matchers
    matchers, err := ParseLabelMatchers(r.URL.Query()["filter"])
    if err != nil {
        writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid filter: " + err.Error()})
        return
    }

    silences := store.List(time.Now().UTC())

    // NEW: apply silence matcher filter
    if len(matchers) > 0 {
        filtered := silences[:0]
        for _, s := range silences {
            if MatchesSilenceMatchers(matchers, s.Matchers) {
                filtered = append(filtered, s)
            }
        }
        silences = filtered
    }

    writeJSON(w, http.StatusOK, silences)
}
```

---

## 5. Ответы об ошибках

```
400 Bad Request
Content-Type: application/json

{"error": "invalid filter: invalid matcher syntax: 'severity:critical'"}
```

---

## 6. Тесты

### `matchers_test.go` — `go-app/internal/application/handlers/matchers_test.go`

Тест-кейсы для `ParseLabelMatcher`:

| Input | Ожидание |
|-------|----------|
| `alertname="Watchdog"` | `{Name:"alertname", Op:"=", Value:"Watchdog"}` |
| `severity!="info"` | `{Name:"severity", Op:"!=", Value:"info"}` |
| `instance=~".*prod.*"` | `{Name:"instance", Op:"=~", re: compiled}` |
| `job!~"canary"` | `{Name:"job", Op:"!~", re: compiled}` |
| `severity:critical` | error |
| `=""` | error (пустое имя) |
| `=~"[invalid"` | error (невалидный regex) |

Тест-кейсы для `MatchesLabels`:

```go
labels := map[string]string{"alertname": "Watchdog", "severity": "critical"}
// filter: alertname="Watchdog"             → true
// filter: alertname="Watchdog",severity=~"crit.*" → true
// filter: alertname="Other"               → false
// filter: missing_label="x"              → false (пустая строка != "x")
```

Тест-кейсы для `MatchesSilenceMatchers`:

```go
silence := APISilence{Matchers: [{Name:"alertname",...}, {Name:"severity",...}]}
// filter: alertname="Watchdog"  → true (имя найдено)
// filter: namespace="prod"      → false (имя отсутствует в матчерах)
```

---

## 7. Зависимости

Нет новых внешних зависимостей. Используется только стандартная `regexp`.

---

## 8. Алерт-матчинг: edge cases

- Если label отсутствует в `alert.Labels`, значение считается пустой строкой (`""`)
- Для `=~` regex должен совпадать полностью (используем `re.MatchString`, что проверяет
  подстроку — Alertmanager использует `re2` с `^...$` оберткой при компиляции)
- Для Alertmanager-совместимости regex надо оборачивать: `^(?:value)$` при компиляции

> **Важно:** Alertmanager при `=~` проверяет полное совпадение (anchored regex). Это
> достигается оборачиванием в `^(?:...)$` при компиляции, а не при парсинге.
