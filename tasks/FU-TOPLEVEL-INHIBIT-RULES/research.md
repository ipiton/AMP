---
id: FU-TOPLEVEL-INHIBIT-RULES
slug: fu-toplevel-inhibit-rules
stream: Parity / Inhibition
type: bug
artifact: research-pack
status: complete
created_at: 2026-10-12
updated_at: 2026-10-12
---

# Research Pack - верхнеуровневый `inhibit_rules:`

**TL;DR:** читать `inhibit_rules:` через viper в новое поле `Config.InhibitRules` (тип `InhibitionRuleConfig`) и сливать с `inhibition.inhibit_rules` в одной функции на `Config`, которую зовут все 4 точки. `RouteConfig.InhibitRules` не удалять — расширить тип matchers'ами. Level 2, Generation: single pass (откат дешёвый, внешних интеграций нет).

## Findings

- Runtime берёт правила только из `cfg.Inhibition.ToInhibitionRules()`: старт `service_registry.go:1333`, hot reload `:2699` (пересобирается **безусловно** на каждом reload), fail-fast `config.go:690` (`LoadConfig`) и `:798` (`LoadConfigFromEnv`).
- `configvalidator` видит только вложенные правила (`alertmanager_validation.go:98`; `inhibit_rules` нет в `alertmanagerSubsetKeys:50`) и запускается только при `route:` (`loadRouteConfig`, `config.go:722`).
- `RouteConfig.InhibitRules` **не мёртвое**: `yaml.Marshal(cfg.Routing)` отдаёт его в `/api/v2/status` `config.original` (`handlers/status_config.go:128`, тест `status_config_test.go:148`) и в `RoutingFingerprint` (`routing_fingerprint.go`). Тип без `source_matchers`/`target_matchers` ⇒ amtool/karma их не видят, а правка только matchers не меняет fingerprint. Вложенные правила в `config.original` не попадают вовсе.
- Парсер `routing.Parse` нестрогий (`KnownFields` нет), viper `Unmarshal` без `ErrorUnused` — новое поле ничего не ломает. Diff (`update_diff.go:189`) идёт через JSON `Config` ⇒ новое поле с json-тегом видно diff'у; секция `inhibit_rules` уже маппится в компонент `inhibition` (`reload_coordinator.go:645`).

## Options

- **A. Только ошибка/WARN при непустом верхнеуровневом ключе.** Минимум, но verbatim upstream-конфиг по-прежнему требует ручной правки — заявление «drop-in» остаётся ложным.
- **B. viper-поле `Config.InhibitRules` + слияние с вложенными** (`Config.InhibitionRules()` поверх `ToInhibitionRules`). Работает без `route:`, тот же тип и compile-путь, что у вложенных, ошибки называют источник.
- **C. Брать из `cfg.Routing.InhibitRules`.** Только при `route:`, тип-плейсхолдер без matchers/`name` — нужна конвертация и расширение; связывает ингибицию с парсингом маршрутов.

## Decision

- **B.** Оба источника сразу — **объединение** (как inline + `config_file`), INFO-лог с числом правил по источникам; дубль правила безвреден (то же подавление). Ошибка компиляции — с префиксом источника (`inhibit_rules[2]` vs `inhibition.inhibit_rules[2]`).
- `routing.InhibitRule` расширить `SourceMatchers`/`TargetMatchers`/`Name`, чтобы `config.original` и fingerprint не теряли modern-форму. Подмешивать ли туда вложенные правила — вопрос Spec (склоняюсь: нет, вне scope).

## Spec Inputs

- 4 call site'а `ToInhibitionRules` → единая функция на `Config`; валидатор получает объединённый список.
- Тесты: оба формата верхнего уровня реально ингибируют (через `LoadConfig` + matcher), слияние, ошибка с источником, reload при правке только верхнеуровневого ключа, `config.original` сохраняет `source_matchers`.
- Доки: compat Known Gap #9 → закрыт, строки 73 и ~994; CHANGELOG Fixed + migration note «ранее неактивные правила начнут подавлять».
