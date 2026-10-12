---
id: FU-TOPLEVEL-INHIBIT-RULES
slug: fu-toplevel-inhibit-rules
stream: Parity / Inhibition
type: bug
priority: critical
status: active
created_at: 2026-10-12
updated_at: 2026-10-12
---

# Requirements: верхнеуровневый `inhibit_rules:` молча игнорируется

## Problem Framing

- **Symptom:** verbatim `alertmanager.yml` с `inhibit_rules:` на верхнем уровне (размещение upstream) загружается без ошибок и предупреждений, но не ингибирует ничего. Подтверждено аудитом 2026-10-06 на реальном конфиге kube-prometheus-stack (3 правила с `source_matchers`). Оператор, мигрирующий с Alertmanager, получает лишние нотификации и не видит причины.
- **Root Cause:** в runtime доходят только правила из обёртки AMP `inhibition.inhibit_rules` (`internal/config.InhibitionConfig` → `ToInhibitionRules` → `service_registry.go:1333`, hot reload `:2699`). Верхнеуровневый ключ разбирается в `internal/infrastructure/routing.RouteConfig.InhibitRules` (`config.go:99`), но никем не читается; его тип `InhibitRule` к тому же без `source_matchers`/`target_matchers` и `name`. Валидатор (`alertmanager_validation.go:98`) тоже видит только `cfg.Inhibition.Rules`.
- **Why Now:** P0 в BACKLOG § Production Readiness (повышен аудитом 2026-10-06): заявление «замена Alertmanager» ложно для любого конфига с inhibit-правилами.
- **How We Measure:** конфиг upstream-формы с верхнеуровневым `inhibit_rules:` (legacy `source_match` и modern `source_matchers`) реально подавляет target-алерт — проверено тестом через тот же путь, что и при старте и hot reload.

## Risk Profile

- **Signals:** `C X R` _(было `C R`; `X` добавлен 2026-10-12 после смоука — фикс panic'а в `pkg/metrics`, см. tasks.md 1.9)_
  - `C` — формат конфиг-файла: AMP начинает принимать и применять новый верхнеуровневый ключ (аддитивно), нужна семантика при наличии обоих источников.
  - `R` — поведение в проде меняется: конфиги, у которых правила молча не работали, после апгрейда начнут глушить алерты. Нужна запись в `CHANGELOG.md` (Fixed + migration note).
- **Tier:** Full _(было Standard; 3 сигнала. Конвейер фактически не меняется: `deep-review` уже был запланирован как recommended, will run — теперь он обязателен)_
- **Notes:** `X` не ставлю — изменения в пределах одного сервиса и слоя конфига (`internal/config` + выброс неиспользуемого поля в `internal/infrastructure/routing`). Эскалация до Full: если дифф превысит ~200 строк или решение потребует трогать сам движок `internal/infrastructure/inhibition`. Discretionary `deep-review` рекомендован (смена поведения подавления — тихая потеря нотификаций при ошибке).

## User Stories

1. Как оператор, мигрирующий с Alertmanager, я хочу подложить свой `alertmanager.yml` как есть и получить те же inhibit-правила, чтобы не переписывать конфиг под обёртку AMP.
2. Как оператор, я хочу видеть ошибку или явное сообщение при загрузке, если inhibit-правила не будут применены, а не узнавать об этом по шквалу нотификаций.

## Success Criteria

- [ ] Верхнеуровневый `inhibit_rules:` применяется в runtime при старте и при hot reload — со всеми формами upstream (`source_match`/`_re`, `target_match`/`_re`, `source_matchers`/`target_matchers`, `equal`).
- [ ] Ошибка в верхнеуровневом правиле (битый regex/matcher) валит старт и отклоняет reload так же, как для `inhibition.inhibit_rules`, и проходит через `configvalidator`.
- [ ] Семантика при одновременном наличии обоих источников определена в Spec и покрыта тестом (объединение или явная ошибка — решение в research/spec).
- [ ] Изменение верхнеуровневого `inhibit_rules:` при hot reload доходит до движка ингибиции (diff/fingerprint его видят).
- [ ] Мёртвое поле `RouteConfig.InhibitRules` / тип-плейсхолдер `routing.InhibitRule` убраны или перестают вводить в заблуждение.
- [ ] Доки: `ALERTMANAGER_COMPATIBILITY.md` (Known Gap #9 закрыт, строка Inhibition 🟢, инструкции по миграции стр. ~994), `CHANGELOG.md` `[Unreleased]` (Fixed + migration note о том, что ранее неактивные правила начнут работать).
- [ ] Гейты AMP из `WORKFLOW.md` зелёные, включая `scripts/release-gate.sh`.

## Non-Goals

- Не менять движок ингибиции (`internal/infrastructure/inhibition`) и его семантику сопоставления.
- Не трогать `inhibition.config_file` (отдельный файл правил) — кроме того, что потребует семантика слияния.
- Не расширять charset matcher'ов (`FU-MATCHER-LABEL-CHARSET`) и не чинить другие P0 (`PROD-HARDCODED-FILTER`, `HELM-DEFAULTS-VALIDATE`).
- Не убирать поддержку обёртки `inhibition.inhibit_rules` — она остаётся рабочей.

## Constraints

- **Scope:** `go-app/internal/config` (загрузка, адаптер, валидация, reload/diff), `internal/infrastructure/routing` (мёртвое поле), доки и CHANGELOG. Чарт — только если `configFile`/values требуют упоминания.
- **Security:** не применимо (нет auth/секретов); но неверное правило ингибиции = тихая потеря нотификаций — fail-fast на ошибках обязателен.
- **Compatibility:** аддитивно для формата; поведенческий breaking — ранее мёртвые правила оживут после апгрейда → migration note.

## Discovery Notes

- Similar tasks: `parity-a2-inhibition-pipeline`, wave 7 `FU-INHIBIT-MATCHERS` (`tasks/archive/AMP-PARITY/`), `PROD-GROUPING-DEFAULT` (аналогичный класс «verbatim upstream-конфиг молча не работает», migration note в CHANGELOG).
- Relevant patterns:
  - `InhibitionConfig.ToInhibitionRules` (`internal/config/inhibition_adapter.go:39`) — compile + fail-fast, готовая точка для второго источника.
  - `alertmanager_validation.go:98` — подмешивание правил в `amConfig.InhibitRules` для `configvalidator`.
  - `reload_coordinator.go:645` уже маппит секцию `inhibit_rules` → компонент `inhibition`; `routing_fingerprint.go` уже включает `inhibit_rules:` в fingerprint — проверить, что reload реально пересобирает правила при изменении только верхнеуровневого ключа.
  - Ключ виден viper'у как верхнеуровневый: возможен `mapstructure`-тег на `Config` либо чтение из уже разобранного `RouteConfig`.
- Open unknowns:
  - Wire vs warn: BACKLOG допускает минимум «ошибка/WARN», предпочтительно — полноценное чтение. Склоняюсь к чтению; подтвердить в research.
  - Оба источника сразу: объединять (как `config_file` + inline) или отвергать конфиг ошибкой — upstream второго источника не знает.
  - Откуда брать правила: из `RouteConfig` (расширить тип) или отдельным полем в `config.Config` с тегом `inhibit_rules` (переиспользует `InhibitionRuleConfig`).
  - Где ещё читается `cfg.Inhibition.Rules` (API статуса `/api/v2/status` отдаёт конфиг? karma-badge?) — не потерять верхнеуровневые правила там.
