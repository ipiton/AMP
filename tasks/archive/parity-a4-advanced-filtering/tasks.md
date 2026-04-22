# PARITY-A4-ADVANCED-FILTERING — Tasks

## Чеклист

### Slice 1: Парсер матчеров (основа)

- [ ] Создать `go-app/internal/application/handlers/matchers.go`
  - [ ] Тип `MatcherOp` + 4 константы (`=`, `!=`, `=~`, `!~`)
  - [ ] Тип `LabelMatcher` с полями `Name`, `Op`, `Value`, `re *regexp.Regexp`
  - [ ] `ParseLabelMatcher(raw string) (*LabelMatcher, error)` — regex парсинг строки
    - regex: `^([a-zA-Z_][a-zA-Z0-9_]*)(=~|!~|!=|=)"(.*)"$`
    - для `=~` / `!~` — компилировать `re` с оборачиванием в `^(?:value)$`
    - ошибка: "invalid matcher syntax: '...'"
  - [ ] `ParseLabelMatchers(rawFilters []string) ([]*LabelMatcher, error)`
  - [ ] `MatchesLabels(matchers []*LabelMatcher, labels map[string]string) bool`
    - отсутствующий label = пустая строка
    - AND-логика по всем матчерам
  - [ ] `MatchesSilenceMatchers(filters []*LabelMatcher, silenceMatchers []core.APISilenceMatcher) bool`
    - возвращает true если каждый filter-matcher нашёл Name в silenceMatchers

### Slice 2: Тесты парсера

- [ ] Создать `go-app/internal/application/handlers/matchers_test.go`
  - [ ] Тесты `ParseLabelMatcher`: 7 кейсов (4 валидных, 3 невалидных из Spec)
  - [ ] Тесты `MatchesLabels`: все 4 кейса из Spec
  - [ ] Тесты `MatchesSilenceMatchers`: 2 кейса из Spec
  - [ ] Тест: несколько `filter` params (AND-логика)
  - [ ] Тест: пустой `filter` slice → все алерты (no-op)

### Slice 3: Интеграция в alerts handler

- [ ] Изменить `go-app/internal/application/handlers/alerts.go`
  - [ ] В `handleAlertsGet`: добавить парсинг `r.URL.Query()["filter"]`
  - [ ] Вернуть `400` при ошибке парсинга
  - [ ] Применить `MatchesLabels` к результату `store.List()`
  - [ ] Удалить комментарий `// For now, simple list...`

### Slice 4: Интеграция в silences handler

- [ ] Изменить `go-app/internal/application/handlers/silences.go`
  - [ ] В `handleSilencesGet`: добавить парсинг `r.URL.Query()["filter"]`
  - [ ] Вернуть `400` при ошибке парсинга
  - [ ] Применить `MatchesSilenceMatchers` к результату `store.List()`
  - [ ] Удалить комментарий `// Filtering by label matchers can be added here later`

### Slice 5: Интеграционные тесты хендлеров

- [ ] Добавить тесты в `go-app/internal/application/handlers/alerts_test.go` (создать если нет)
  - [ ] `GET /api/v2/alerts?filter=alertname="Watchdog"` — возвращает только matching alerts
  - [ ] `GET /api/v2/alerts?filter=severity=~"crit.*"` — regex фильтрация
  - [ ] `GET /api/v2/alerts?filter=bad:syntax` — 400 Bad Request
  - [ ] `GET /api/v2/alerts?status=firing&filter=alertname="X"` — combined params
- [ ] Добавить тесты в `go-app/internal/application/handlers/silences_test.go` (создать если нет)
  - [ ] `GET /api/v2/silences?filter=alertname="Watchdog"` — фильтрация по имени matcher
  - [ ] `GET /api/v2/silences?filter=nonexistent="x"` — пустой результат
  - [ ] `GET /api/v2/silences?filter=bad` — 400 Bad Request

### Slice 6: Верификация

- [ ] `go build ./...` — нет ошибок компиляции
- [ ] `go test ./internal/application/handlers/...` — все тесты зелёные
- [ ] `go vet ./...` — без предупреждений
- [ ] Ручная проверка: curl с filter params против запущенного AMP
- [ ] Обновить `docs/06-planning/NEXT.md` — убрать задачу из Queue

## Порядок выполнения

```
Slice 1 → Slice 2 → Slice 3 → Slice 4 → Slice 5 → Slice 6
```

Слайсы 3 и 4 независимы друг от друга (можно делать параллельно после Slice 1).

## Примечания по реализации

- `alerts[:0]` trick для in-place фильтрации без аллокации — безопасно для slice без ссылок
- Для anchored regex: при компиляции в `ParseLabelMatcher` использовать `"^(?:" + value + ")$"`
- `r.URL.Query()["filter"]` возвращает `[]string{}` (не nil) если param отсутствует — безопасно
  передавать в `ParseLabelMatchers`, функция вернёт пустой slice и nil error
