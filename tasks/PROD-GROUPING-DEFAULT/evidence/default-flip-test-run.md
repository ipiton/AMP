# Прогон тестов с дефолтом `grouping.enabled: true`

- Дата: 2026-10-10, ветка `bugfix/prod-grouping-default` @ `4dd0965`.
- Изменение (временное, откачено `git checkout`): `go-app/internal/config/config.go` — `viper.SetDefault("grouping.enabled", false)` → `true`.
- Команда: `cd go-app && go test ./internal/config/... ./internal/application/... ./internal/core/... ./cmd/...`
- Результат: единственный FAIL — пакет `internal/config`:

```text
--- FAIL: TestLoadConfig_GroupingDefaults (0.00s)
    grouping_adapter_test.go:78:
        Error: Should be false
FAIL	github.com/ipiton/AMP/internal/config	0.807s
```

- Остальные пакеты четырёх деревьев — `ok`. Весь модуль (`go test ./...`), `-race`, helm и e2e не запускались.
