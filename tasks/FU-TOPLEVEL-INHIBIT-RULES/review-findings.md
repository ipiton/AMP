# Deep Review Findings: верхнеуровневый `inhibit_rules:` молча игнорируется

**Trigger classification:** mandatory (Full tier: `C X R`, 3 сигнала — после Deviation 1.8)
**Reviewer perspective:** R1 — два независимых агента (general-purpose, read-only, `go test -overlay` в scratchpad), не видевших выводов автора и друг друга: (A) корректность + премисы + контракт конфига + сопровождаемость; (B) runtime/rollout + observability + cross-boundary + история git. F1 и F2 перепроверены автором по коду после получения отчётов.
**Reviewed at:** 2026-10-12
**Reviewed tree:** R1 — `bugfix/fu-toplevel-inhibit-rules` @ `60b1584`; R2 — @ `21a84cc`
**Verdict:** R1 fix_required → R2 pass (см. `review-verdict.json`)

## Findings

### F1 — viper приводит к нижнему регистру имена label'ов в `*_match`/`*_match_re`
- **Severity:** major
- **Location:** `go-app/internal/config/config.go:53` (декодирование `viper.Unmarshal`, `config.go:684`); viper v1.21 `util.go:73` `insensitiviseVal` рекурсивно по map'ам внутри списков.
- **Issue:** (A, прогон) `source_match: {clusterName: Prod}` → `SourceMatch: map[clustername:Prod]`. Правило upstream с label'ом в смешанном регистре молча не срабатывает (fail-open) — тот же класс «молча не работает», который закрывает задача. `config.original` (yaml.v3) показывает исходный регистр — оператор видит правильное правило. Значения, `equal:` и строки `*_matchers` регистр сохраняют. Вложенный `inhibition.inhibit_rules` вёл себя так же и до задачи.
- **Recommendation:** декодировать правила ингибиции из сырых байтов файла через yaml.v3 (как `loadRouteConfig`), а не через viper; для обоих размещений — корень общий. Тест со смешанным регистром.
- **Disposition:** fix-here
- **Follow-up:** n/a

### F2 — E150/E151: правило без source или target отвергается, upstream его принимает
- **Severity:** major
- **Location:** `go-app/pkg/configvalidator/validators/inhibition.go:63-71`; путь — `internal/config/alertmanager_validation.go:99-106`.
- **Issue:** (B, прогон) верхнеуровневые правила теперь доходят до `configvalidator` (при `route:` — в verbatim-конфиге он есть всегда). Правило вида `- target_matchers: ['alertname = InfoInhibitor']` без source ⇒ `[E150]`, exit 1 на старте / reload отклонён. Upstream такое правило принимает, runtime AMP трактует пустую сторону как «совпадает со всем» (без `route:` то же правило загрузилось и подавило). На `main` такой конфиг стартовал (правила игнорировались) — после апгрейда перестанет. По памяти ревьюера, это 4-е правило дефолтных values kube-prometheus-stack (не сверено с актуальным чартом; отказ E150 подтверждён прогоном).
- **Recommendation:** понизить E150/E151 до W-кода (upstream-паритет; правило с пустой стороной — легальное, но широкое). Тест T4 переформулировать.
- **Disposition:** fix-here
- **Follow-up:** n/a

### F3 — откат reload после коммита не возвращает прежние inhibit-правила
- **Severity:** minor (предсуществующее)
- **Location:** `go-app/internal/application/service_registry.go:2702-2717` (`UpdateRules`) → `:2730` (`reloadRoutingTree` fail → `rollbackPostCommit`).
- **Issue:** `rollbackPostCommit` восстанавливает config/routing/templates/targets, но не правила матчера. Инвариант Spec «reload отклонён ⇒ живые правила не меняются» на этом пути не держится.
- **Recommendation:** применять ингибицию после `reloadRoutingTree` или восстанавливать `previousConfig.InhibitionRules()` в откате.
- **Disposition:** defer-bug (поведение было и до задачи; Spec-инвариант уточнить)
- **Follow-up:** `BUGS.md` — на `finalize`

### F4 — правила, добавленные reload'ом при выключенном на старте движке, «работают» только по логу
- **Severity:** minor (предсуществующее, задача делает вероятнее)
- **Location:** `go-app/internal/application/service_registry.go:2712-2716`.
- **Issue:** (B, прогон) `/-/reload` → 200, `/health/reload` `healthy:true`, только WARN «restart required». Оператор через config-reloader решит, что правила действуют.
- **Recommendation:** поднимать существующий `RestartRequiredWarning`; минимум — доки и migration note (Spec Edge Case 4).
- **Disposition:** defer-backlog (доки — fix-here в Phase 4)
- **Follow-up:** `BACKLOG.md` — на `finalize`

### F5 — сработавшее правило верхнего уровня не идентифицируется: `rule=""`
- **Severity:** minor
- **Location:** `go-app/internal/config/inhibition_adapter.go:99`.
- **Issue:** (B, прогон) upstream не знает `name`, поэтому verbatim-правила безымянные: `ruleName:""` в `/api/v2/inhibitions`, `inhibition_match_total{rule=""}`, логи. Для `config_file` парсер подставляет `rule-%d` (`inhibition/parser.go:290`), для inline/top-level — нет.
- **Recommendation:** при пустом имени подставлять путь (`inhibit_rules[i]` / `inhibition.inhibit_rules[i]`).
- **Disposition:** defer-backlog (меняет label метрики и для вложенных правил — отдельное решение)
- **Follow-up:** `BACKLOG.md` — на `finalize`

### F6 — индексы ошибок валидатора сдвигаются для вложенных правил при наличии верхнеуровневых
- **Severity:** minor
- **Location:** `go-app/internal/config/alertmanager_validation.go:101-105`.
- **Issue:** E15x на вложенном правиле j репортится как `inhibit_rules[len(top)+j]`. Для конфигов только с одним размещением вывод не меняется.
- **Recommendation:** валидировать списки раздельно или перенумеровывать в `formatConfigValidatorErrors`.
- **Disposition:** defer-tech-debt (оба размещения сразу — редкость)
- **Follow-up:** `TECH-DEBT.md` — на `finalize`

### F7 — `reload_coordinator`: секция ингибиции никогда не маппится в компонент `inhibition`
- **Severity:** minor (предсуществующее, только логи/ReloadResult)
- **Location:** `go-app/internal/config/reload_coordinator.go:645-646`.
- **Issue:** (A, прогон) diff даёт `InhibitRules`/`Inhibition` (имена Go-полей), а карта ждёт `inhibit_rules`/`inhibition` ⇒ `affected_components=[]`. Reload не пропускается (P5 держится), правила применяются безусловно. Утверждение research «уже маппится» неверно.
- **Recommendation:** поправить формулировку research/Spec; маппинг — через mapstructure-имя.
- **Disposition:** defer-tech-debt; формулировку research — fix-here
- **Follow-up:** `TECH-DEBT.md` — на `finalize`

### F8 — план доков неполон
- **Severity:** minor
- **Location:** `README.md:17`, `docs/CONFIGURATION_GUIDE.md:419,715`, `helm/amp/README.md:249,262`, `docs/RELEASE_NOTES_v0.1.0-draft.md:322`, `go-app/internal/infrastructure/inhibition/README.md:511` (устаревшее имя метрики); verify-grep шага 4.2 их не ловит.
- **Recommendation:** расширить Phase 4; в CHANGELOG — «inhibition crashed startup since PARITY-A2 (`a822faf`, 2026-04-16)», env `INHIBIT_RULES` + ключ в файле ⇒ ошибка decode.
- **Disposition:** fix-here (Phase 4)
- **Follow-up:** n/a

### F9 — Spec обещает лог-ключи `top_level`/`inhibition`, код пишет `rules` + `top_level_rules`
- **Severity:** nit
- **Location:** `go-app/internal/application/service_registry.go:1347-1350`.
- **Disposition:** fix-here (выровнять Spec под код)

### F10 — словарь префиксов ошибок: `top-level` vs `inline`
- **Severity:** nit
- **Location:** `go-app/internal/config/inhibition_adapter.go:70,107`.
- **Disposition:** reject (reason: текст для вложенных правил сохранён побайтно — инвариант Spec; различимость есть; строки закрепить тестами T3)

### F11 — `RouteConfig.Clone()` теряет `InhibitRules`
- **Severity:** nit (предсуществующее, вызовов в проде нет)
- **Location:** `go-app/internal/infrastructure/routing/config.go:254`.
- **Disposition:** fix-here (одна строка; поле теперь задокументировано как источник `config.original`/fingerprint)

### F12 — комментарий `Config.InhibitionRules` переоценивает гарантии
- **Severity:** nit
- **Location:** `go-app/internal/config/inhibition_adapter.go:65-68`.
- **Disposition:** fix-here (после F1; смягчить с учётом F3)

## Round 2 (@ `21a84cc`, третий независимый агент, в R1 не участвовал)

Закрыты: **F1** (регистр сохраняется для обоих размещений; все пути загрузки — `main.go:64`, `futureparity_compat.go:124`, `reload_coordinator.go:365` — идут через `LoadConfig`; int/bool/float/null-скаляры, anchors/merge-keys, `null`, env `INHIBITION_CONFIG_FILE` — без регрессий против viper), **F2** (W150/W151; пустая сторона = «всё» в runtime, правило с обеими пустыми сторонами глушит ничего — как upstream; других потребителей E150/E151 нет), **F9**, **F11**, **F12**. **F8** — Phase 4 не выполнена (ожидаемо), план дополнен (N5).

### N1 — строгий yaml.v3 ломает ранее загружавшиеся вложенные формы
- **Severity:** minor
- **Location:** `go-app/internal/config/config.go:741-749`
- **Issue:** `equal: cluster` (скаляр) и `inhibit_rules:` как одна map под `inhibition:` грузились на `main` (weak decode viper), теперь exit 1. Upstream отвергает обе формы — паритет, но громкий апгрейд-брейк.
- **Disposition:** fix-here (migration note в CHANGELOG, tasks.md 4.3)

### N2 — ключи секции в другом регистре теперь игнорируются
- **Severity:** minor
- **Location:** `go-app/internal/config/config.go:741-752`
- **Issue:** `Inhibition:`, `Inhibit_Rules:`, `Source_Match:` viper сопоставлял без учёта регистра; yaml.v3 — нет, и результат перезаписывает viper ⇒ правила или сторона правила пропадают молча. Upstream такие ключи отвергает как неизвестные. Вероятно, редкость.
- **Disposition:** defer-tech-debt (громкая ошибка на ключ, совпадающий без учёта регистра) + migration note в CHANGELOG (4.3)
- **Follow-up:** `TECH-DEBT.md` — на `finalize`

### N3 — путь `inhibition.config_file` по-прежнему отвергает одностороннее правило
- **Severity:** minor
- **Location:** `go-app/internal/infrastructure/inhibition/parser.go:349-357`, `models.go:192-207`
- **Issue:** target-only правило kube-prometheus-stack грузится на верхнем уровне и inline, но не из `config_file`. Формат `config_file` — AMP-специфичный, verbatim upstream туда не попадает.
- **Disposition:** defer-backlog (ослабить `validateSemantics` — правка движка ингибиции, Non-Goal задачи)
- **Follow-up:** `BACKLOG.md` — на `finalize`

### N4 — текст W151 неверен для правила с обеими пустыми сторонами
- **Severity:** nit
- **Location:** `go-app/pkg/configvalidator/validators/inhibition.go:74-76`
- **Disposition:** fix-here — сделано после R2 (только строка сообщения), закрепить в тесте 3.4

### N5 — E150/E151 как ошибки остались в доках валидатора
- **Severity:** nit
- **Location:** `go-app/pkg/configvalidator/ERROR_CODES.md:317-326,515`, `go-app/pkg/configvalidator/README.md:256-258`; тесты — 3.4
- **Disposition:** fix-here (tasks.md 4.4)

### S1 (подозрение) — разорванное чтение файла на reload
- **Severity:** minor
- **Location:** `config.go:687,729,786`, `reload_coordinator.go:359`
- **Issue:** один reload читает файл 3–4 раза; подмена symlink ConfigMap между чтениями может склеить версии. Класс уже был с `loadRouteConfig`, задача добавляет одно чтение.
- **Disposition:** defer-tech-debt (рядом с `CONFIG-GLOBAL-VIPER-STATE`: читать байты один раз)

### S2 (подозрение) — отсутствующий файл на повторной загрузке
- **Severity:** nit
- **Disposition:** reject (reason: reload проверяет существование файла до загрузки — путь недостижим)

## Подтверждено чистым

- Потребители: только `config.go:698,806`, `service_registry.go:1333,2702`; другие сервисы (`DefaultConfigUpdateService`, `NewConfigService`) в проде не подключены; все reload-входы идут через `LoadConfig`. P1 `call-path-traced` заслужен.
- Копии `Config` (`Sanitizer.deepCopy`, `configToMap`) — JSON-круг, новое поле сохраняется (прогон).
- Метрики: AST-сбор 197 fq-имён — все 14 `alert_history_inhibition_*` уникальны; других регистраций на пути инициализации нет; удалённые поля нигде не используются (включая `grafana/`, `helm/`, `deploy/`). Panic воспроизведён с `metrics.go` из `main` через overlay, на ветке — нет.
- Hot reload с правкой только верхнеуровневого правила доходит до `UpdateRules` (прогон).
- История: дубли — `d096644`/`ce4ce72` (2025-12), достижимым panic стал в `a822faf` (2026-04-16, PARITY-A2). Тот же класс чинили только в тестах подменой registry (`6236f15`, FU-WAVE3-RELIABILITY).

## Notes для write-tests

- T7: свежий `prometheus.NewRegistry()` как Default, lite + filesystem, `NewServiceRegistry` + `Initialize`, `inhibitionMatcher != nil`; через `Gather` проверить, что `alert_history_inhibition_cache_*` в реестре теста (`sync.Once` в `GetCacheMetrics` иначе маскирует регрессию). Черновик ревьюера B: `scratchpad/reviewB/inhib_startup_test.go`.

## Anti-Pattern Check

- [x] Self-audit was not treated as a substitute for independent review.
