---
id: FU-TOPLEVEL-INHIBIT-RULES
slug: fu-toplevel-inhibit-rules
stream: Parity / Inhibition
type: bug
status: draft
created_at: 2026-10-12
updated_at: 2026-10-12
based_on:
  - requirements.md
  - research.md
---

# Specification: верхнеуровневый `inhibit_rules:` молча игнорируется

**Version:** 1.0  
**Status:** Draft

## Summary

AMP начинает читать верхнеуровневый `inhibit_rules:` (размещение upstream) и применять его наравне с `inhibition.inhibit_rules`: оба источника сливаются в одну функцию на `Config`, через которую идут старт, hot reload, fail-fast загрузки и `configvalidator`. Тип `routing.InhibitRule` получает `source_matchers`/`target_matchers`, чтобы `/api/v2/status` и fingerprint reload'а их не теряли.

## Requirements Coverage

| Requirement / Criterion | Covered by |
|---|---|
| Верхнеуровневый ключ применяется при старте и hot reload, все формы upstream | Target Design п.1–2; тесты T1, T2, T5 |
| Ошибка в правиле валит старт / reload, проходит через `configvalidator` | Target Design п.2–3; тесты T3, T4 |
| Семантика при обоих источниках определена и покрыта тестом | Target Design п.2 (объединение); тест T2 |
| Правка только верхнеуровневого ключа доходит до движка при reload | Design Premises P4–P5; тест T5 |
| Плейсхолдер `routing.InhibitRule` перестаёт вводить в заблуждение | Target Design п.4; тест T6 |
| Доки и CHANGELOG | Component Architecture § Доки |
| Гейты AMP зелёные | `tasks.md` / `testing` |

## Current State

- **Code:**
  - `internal/config/config.go:42` — `Config.Inhibition InhibitionConfig` (`mapstructure:"inhibition"`), правила в `InhibitionConfig.Rules` (`mapstructure:"inhibit_rules"`, тип `InhibitionRuleConfig` с legacy-картами, `source_matchers`/`target_matchers`, `equal`, `name`).
  - `internal/config/inhibition_adapter.go:39` — `(*InhibitionConfig).ToInhibitionRules()`: inline-правила → `rule.Compile()` (fail-fast), затем правила из `config_file`.
  - Потребители (`grep` по всему репозиторию): `config.go:690` (`LoadConfig`), `config.go:798` (`LoadConfigFromEnv`), `application/service_registry.go:1333` (старт движка), `:2699` (hot reload). Остальное — тесты `internal/config`.
  - `internal/config/alertmanager_validation.go:98` — в `configvalidator` подмешиваются только `cfg.Inhibition.Rules`; `inhibit_rules` нет в `alertmanagerSubsetKeys` (`:50`). Валидатор запускается только при `route:` (`loadRouteConfig`).
  - `internal/infrastructure/routing/config.go:99,362` — `RouteConfig.InhibitRules []InhibitRule`, тип без matchers'ов; уходит в `/api/v2/status` `config.original` (`handlers/status_config.go:128`) и в `RoutingFingerprint`.
- **Data:** не применимо.
- **Tests:** `internal/config/inhibition_adapter_test.go`, `inhibition_matchers_test.go` (включая reload-сценарий `:271`), `handlers/status_config_test.go:148`, `pkg/configvalidator/validators/inhibition_test.go`.
- **Docs:** `docs/ALERTMANAGER_COMPATIBILITY.md` (строки 14/23/73, Known Gap #9 на 810, миграция ~994), `helm/amp/README.md:38` («that key is ignored»), `config.yaml.example:438`, `CHANGELOG.md`.

## Design Premises

| # | Premise | Confirmed by | Class | If wrong |
|---|---|---|---|---|
| P1 | Правила в runtime попадают только через `ToInhibitionRules` в 4 местах | `grep -rn "ToInhibitionRules()"` по всему репозиторию: `config.go:690,798`, `service_registry.go:1333,2699`; прочее — тесты | call-path-traced | пропущенный потребитель продолжит видеть только вложенные правила |
| P2 | viper декодирует верхнеуровневый список в `[]InhibitionRuleConfig` так же, как вложенный | тот же `viper.Unmarshal` + mapstructure-теги, что у `inhibition.inhibit_rules`; нет `ErrorUnused`/кастомных hook'ов на этот тип | code-read | поле останется пустым — ловится тестом T1 через `LoadConfig` |
| P3 | Добавление поля ничего не ломает в парсинге маршрутов | `routing.Parse` нестрогий (нет `KnownFields`), ключ и сейчас декодируется в `RouteConfig.InhibitRules` | code-read | — |
| P4 | `ServiceRegistry.ReloadConfig` пересобирает правила на любом успешном reload, без гейта по секции | `service_registry.go:2668-2715`: `ReloadFromFile` → `r.config = newConfig` → `ToInhibitionRules` безусловно | call-path-traced | правка не дойдёт до матчера — тест T5 |
| P5 | Reload не отбрасывает правку «только верхнеуровневого `inhibit_rules`» как no-change | короткое замыкание — пустой diff (`reload_coordinator.go:225`); diff — JSON-круг `Config` (`update_diff.go:189`), новое поле без `json:"-"` в него попадёт; плюс `RoutingFingerprint` покрывает `RouteConfig.InhibitRules` | code-read | reload вернёт «no changes» — тест T5 |
| P6 | Ни чарт, ни примеры сейчас не кладут правила на верхний уровень, рассчитывая, что они игнорируются | `grep inhibit_rules helm/ examples/ config.yaml.example` — только комментарии и вложенная форма | call-path-traced (в репозитории) | — |
| P7 | У пользователей есть конфиги с верхнеуровневыми правилами, которые сейчас не действуют, и после апгрейда начнут подавлять | аудит 2026-10-06 (kube-prometheus-stack, 3 правила) | assumed (масштаб) | сам факт ожидаемый; риск — неожиданное подавление → migration note |

## Target Design

1. В `Config` добавляется поле `InhibitRules []InhibitionRuleConfig` с тегами `mapstructure:"inhibit_rules" yaml:"inhibit_rules,omitempty"` — верхнеуровневый ключ upstream, тот же тип, что у вложенных правил.
2. Новый метод `(*Config).InhibitionRules() ([]inhibition.InhibitionRule, error)`: компилирует верхнеуровневые правила (`rule.Compile()`, ошибка с префиксом `inhibit_rules[i]` или имени), затем добавляет результат `c.Inhibition.ToInhibitionRules()` (inline + `config_file`, там префикс уже `inline inhibition rule`). Оба источника **объединяются**; дубль правила безвреден. Все 4 потребителя из P1 переходят на этот метод; `ToInhibitionRules` остаётся как есть.
3. `validateAlertmanagerSubset` передаёт в `configvalidator` объединённый список (`cfg.InhibitRules` + `cfg.Inhibition.Rules`) через существующий `toAlertmanagerInhibitRules`.
4. `routing.InhibitRule` получает `SourceMatchers`/`TargetMatchers []string` (`yaml:"source_matchers,omitempty"` и т. п.); комментарии «placeholder / silently ignored» на `RouteConfig.InhibitRules` и типе заменяются на правду: поле — только зеркало для `config.original` и fingerprint, в runtime правила идут через `Config.InhibitRules`.
5. При старте `initializeInhibition` логирует число правил по источникам (`top_level`, `inhibition`), чтобы оператор видел, откуда правила взялись.

Почему так: один тип и один compile-путь для обоих источников, работает без `route:`, слияние повторяет уже принятую модель inline + `config_file`.

## API Contracts

### Формат конфиг-файла

- **Новое:** верхнеуровневый `inhibit_rules:` (upstream-схема: `source_match`/`_re`, `target_match`/`_re`, `source_matchers`/`target_matchers`, `equal`; AMP-расширение `name`) применяется.
- **Без изменений:** `inhibition.inhibit_rules`, `inhibition.config_file`.
- **Оба сразу:** объединение, порядок — верхнеуровневые, затем вложенные, затем `config_file`.
- **Ошибки:** битый regex/matcher в верхнеуровневом правиле — старт с exit 1 / reload отклонён, как для вложенных. Неверный тип значения (`inhibit_rules: "x"`) — ошибка `viper.Unmarshal`; раньше такой конфиг грузился.

### `GET /api/v2/status` (`config.original`)

- Верхнеуровневые правила теперь сохраняют `source_matchers`/`target_matchers`. Вложенные правила туда по-прежнему не попадают (вне scope, Open Questions).

## Data Model / Migrations

Не применимо.

## Component Architecture

- `go-app/internal/config/config.go` — поле `Config.InhibitRules`; `LoadConfig`/`LoadConfigFromEnv` → `cfg.InhibitionRules()`.
- `go-app/internal/config/inhibition_adapter.go` — метод `(*Config).InhibitionRules()`.
- `go-app/internal/config/alertmanager_validation.go` — объединённый список в валидатор, обновить комментарий `:18-30`.
- `go-app/internal/application/service_registry.go` — `:1333`, `:2699` → `r.config.InhibitionRules()`; лог источников на старте.
- `go-app/internal/infrastructure/routing/config.go` — matchers в `InhibitRule`, честные комментарии.
- Доки: `docs/ALERTMANAGER_COMPATIBILITY.md` (строки 14/23/73 → 🟢, Known Gap #9 — пометить закрытым, ~994), `helm/amp/README.md:38`, `config.yaml.example` (упомянуть верхнеуровневую форму), `CHANGELOG.md` `[Unreleased]` — Fixed + migration note.

## Security Design

- [x] Ownership validation — не применимо.
- [x] Input validation — compile каждого правила fail-fast + `configvalidator` при `route:`.
- [x] Sensitive data not logged — логируются только счётчики правил.
- [x] Rate limiting — не применимо.
- [x] Auth/RBAC — не применимо.

## Invariants

- [ ] Конфиг только с `inhibition.inhibit_rules` ведёт себя побайтно так же (те же правила, тот же порядок, те же ошибки).
- [ ] Ошибка в любом правиле любого источника никогда не приводит к частичной загрузке: старт падает, reload отклоняется, живые правила не меняются.
- [ ] Семантика сопоставления (`internal/infrastructure/inhibition`) не меняется.

## Edge Cases

1. Только верхнеуровневые правила, без `route:` → применяются; `configvalidator` не запускается (как и для вложенных сегодня), compile-проверка работает.
2. Оба источника → объединение; одинаковое правило в обоих → два правила, результат подавления тот же.
3. `inhibit_rules: []` или `null` → нет правил из этого источника, без ошибок.
4. На старте правил не было, верхнеуровневые добавлены reload'ом → существующее поведение: WARN «restart required» (`service_registry.go:2713`), движок не создаётся на лету. Не меняем, упомянуть в доке.
5. Правило без имени с ошибкой → сообщение `inhibit_rules[2]: …`, отличимое от `inline inhibition rule "inhibit_rules[2]"`.

## Impact Analysis

- **Affected modules:** `internal/config`, `internal/application` (2 строки + лог), `internal/infrastructure/routing` (тип), доки.
- **Breaking changes:** поведенческий — ранее молча неактивные верхнеуровневые правила начинают подавлять алерты; конфиг с неверным типом под `inhibit_rules:` перестаёт грузиться. Migration note в CHANGELOG.
- **New dependencies:** none.
- **Risks:** неожиданное подавление после апгрейда (P7) → migration note + лог источников на старте; `GET /api/v2/inhibitions` показывает активные подавления.

## Rollout / Rollback

- **Rollout:** обычный релиз; в CHANGELOG — проверить верхнеуровневые `inhibit_rules:` перед апгрейдом, они начнут действовать.
- **Rollback:** откат версии возвращает старое поведение (правила снова игнорируются); данных не трогаем. Риск P7 (`assumed`) закрывается migration note.
- **Feature flag:** не применимо — флаг сохранил бы молчаливую несовместимость с upstream.

## Observability

- **Logs:** INFO на старте `Initializing inhibition subsystem` получает `top_level_rules`/`inhibition_rules`; ошибки compile — с источником.
- **Metrics:** не применимо (активные подавления уже видны через `/api/v2/inhibitions`).
- **Alerts:** не применимо.

## Deep Review

- **Mandatory triggers present:** none (`C R`, 2 сигнала, без `S`/`M`).
- **Discretionary triggers present:** author doubt — ошибка в этом пути = тихая потеря нотификаций (а не громкий отказ); diff с тестами может приблизиться к ~200 строк.
- **Decision:** recommended, will run — после `implement`, до `write-tests`.

## Open Questions

- [ ] Показывать ли вложенные `inhibition.inhibit_rules` в `/api/v2/status` `config.original` (karma-badge, amtool) — вне scope; при необходимости — отдельная запись в BACKLOG на `finalize`.
- [ ] P7 (`assumed`): масштаб «оживающих» правил у пользователей неизвестен — закрывается migration note, не кодом.
