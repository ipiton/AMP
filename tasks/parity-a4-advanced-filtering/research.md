# PARITY-A4-ADVANCED-FILTERING — Research

## Текущий код: точки изменений

### 1. `GET /api/v2/alerts` — `handleAlertsGet`

Файл: `go-app/internal/application/handlers/alerts.go:50`

```go
func handleAlertsGet(store *memory.AlertStore, silences *memory.SilenceStore, w http.ResponseWriter, r *http.Request) {
    status := parseAlertsStatusQuery(r.URL.Query().Get("status"))
    includeResolved := parseBoolQueryLenient(r.URL.Query().Get("resolved"), false)
    // ...
    alerts := store.List(status, includeResolved)
    // TODO: "Advanced filtering (regex, matchers) will be added later"
```

**Что нужно добавить:** парсинг `r.URL.Query()["filter"]` + пост-фильтрация по `alert.Labels`.

### 2. `GET /api/v2/silences` — `handleSilencesGet`

Файл: `go-app/internal/application/handlers/silences.go:59`

```go
func handleSilencesGet(store *memory.SilenceStore, w http.ResponseWriter, r *http.Request) {
    silences := store.List(time.Now().UTC())
    // TODO: "Filtering by label matchers can be added here later"
```

**Что нужно добавить:** парсинг `r.URL.Query()["filter"]` + пост-фильтрация по матчерам сайленса.

### 3. `AlertStore.List` — сигнатура

Файл: `go-app/internal/infrastructure/storage/memory/alert_store.go:160`

```go
func (s *AlertStore) List(statusFilter string, includeResolved bool) []core.APIAlert
```

Возвращает `[]core.APIAlert`. Структура `core.APIAlert`:
```go
type APIAlert struct {
    Labels      map[string]string
    Annotations map[string]string
    Receivers   []APIReceiver
    StartsAt    string
    UpdatedAt   string
    EndsAt      *string
    GeneratorURL string
    Fingerprint  string
    Status       string // "firing" | "resolved"
}
```

Фильтрацию по матчерам **можно делать в хендлере** после вызова `store.List()` — store остаётся
без изменений. Это минимальный invasive подход, соответствующий текущей архитектуре.

### 4. `SilenceStore.List` — сигнатура

Файл: `go-app/internal/infrastructure/storage/memory/silence_store.go:58`

```go
func (s *SilenceStore) List(now time.Time) []core.APISilence
```

Возвращает `[]core.APISilence`. Структура `core.APISilence`:
```go
type APISilence struct {
    ID        string
    Matchers  []APISilenceMatcher // [{Name, Value, IsRegex, IsEqual}]
    StartsAt  string
    EndsAt    string
    UpdatedAt string
    CreatedBy string
    Comment   string
    Status    APISilenceStatus // {State: "active"|"pending"|"expired"}
}
```

Semantics фильтрации `filter` для сайленсов в Alertmanager: фильтровать сайленсы, **матчеры
которых совпадают с переданными label selectors**. То есть: возвращать только те сайленсы,
где matcher с именем `N` и значением `V` присутствует в `Matchers` сайленса.

### 5. Существующий матчер-движок в SilenceStore

Файл: `go-app/internal/infrastructure/storage/memory/silence_store.go:206`

```go
func silenceMatchesLabels(matchers []core.StoredSilenceMatcher, labels map[string]string) bool
```

Эта функция уже реализует логику сопоставления матчеров с labels (regex + exact, equal + not-equal).
**Переиспользовать** эту же логику для фильтрации в хендлерах.

### 6. Alertmanager API Spec — `filter` parameter

Alertmanager v2 OpenAPI:
- `GET /api/v2/alerts` — `filter: [string]` — List of matchers to filter alerts by.
- `GET /api/v2/silences` — `filter: [string]` — List of matchers to filter silences by.

Формат matcher-строки — PromQL label selector синтаксис (без фигурных скобок):
```
alertname="Watchdog"
severity=~"critical|warning"
namespace!="kube-system"
instance!~".*canary.*"
```

Эти строки **не** обёрнуты в `{}`. Каждый `filter` query param — отдельный matcher.
Несколько `filter` применяются как AND.

### 7. Готовые парсеры в экосистеме

В Go-экосистеме есть несколько парсеров PromQL label matchers:
- `github.com/prometheus/prometheus/model/labels` — полный Prometheus парсер
- `github.com/prometheus/alertmanager/pkg/labels` — точный Alertmanager парсер

Проверить go.mod проекта на наличие зависимостей:

```
go-app/go.mod — нужно проверить наличие prometheus/prometheus или alertmanager
```

Если зависимостей нет — написать минимальный парсер самостоятельно. Синтаксис достаточно
прост: `^(\w+)(=~|!~|!=|=)"([^"]*)"$`

### 8. Go.mod — проверка зависимостей

Файл: `go-app/go.mod` — нужно прочитать перед имплементацией, чтобы понять, есть ли
уже `prometheus/prometheus` в зависимостях или нужен самописный парсер.

## Архитектурные выводы

| Вопрос | Ответ |
|--------|-------|
| Изменять store? | Нет — post-filter в handler, store.List() без изменений |
| Где парсер матчеров? | Новый файл `handlers/matchers.go` |
| Переиспользовать silenceMatchesLabels? | Да — логика совпадения уже есть |
| Для alerts: фильтровать по чему? | `alert.Labels` |
| Для silences: фильтровать по чему? | Наличие matcher с совпадающим name/value/op в `silence.Matchers` |
| Semantics для silences? | Сайленс проходит фильтр если его matchers покрывают переданный filter |
