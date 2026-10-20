---
id: SLACK-COLOR-TEMPLATE-REJECTED
slug: slack-color-template
stream: Config
type: bug
status: draft
created_at: 2026-10-20
updated_at: 2026-10-20
based_on:
  - requirements.md
---

# Specification: шаблонные presentation-поля не валидируются как литералы

**Version:** 1.0\
**Status:** Draft

## Summary

Значение шаблонного presentation-поля, содержащее `{{`, при загрузке конфига не проверяется на формат литерала: его форма известна только после рендера. Литералы проверяются, как раньше.

## Requirements Coverage

| Criterion | Covered by |
|---|---|
| Шаблонный `color` (upstream-дефолт, sema) загружается | Design §1–2; T1 |
| Шаблон в `channel`/`icon_emoji`/`icon_url`/`title_link`/`severity` загружается | Design §2; T1 |
| `color` рендерится `danger`/`good` по статусу | T3 (formatter), T4 (e2e из YAML) |
| Статичные невалидные значения отклоняются | T2 |

## Target Design

1. **Тег `template_expr`** в `RouteConfigParser` (`parser.go`): `validateTemplateExpression` → `core.IsTemplateExpression(value)`. Один источник правды с рендерером: то, что рендерер считает литералом и шлёт как есть, валидатор проверяет как литерал.
2. **Теги полей** (`receiver.go`), прежнее правило — через OR:

   | Поле | Было | Стало |
   |---|---|---|
   | `SlackConfig.Channel` | `omitempty,slack_channel` | `omitempty,template_expr\|slack_channel` |
   | `SlackConfig.IconEmoji` | `omitempty,emoji` | `omitempty,template_expr\|emoji` |
   | `SlackConfig.IconURL` | `omitempty,url` | `omitempty,template_expr\|url` |
   | `SlackConfig.TitleLink` | `omitempty,url` | `omitempty,template_expr\|url` |
   | `SlackConfig.Color` | `omitempty,slack_color` | `omitempty,template_expr\|slack_color` |
   | `PagerDutyConfig.Severity` | `omitempty,oneof=critical error warning info` | `omitempty,template_expr\|oneof=critical error warning info` |

   Комментарии полей дополняются строкой «templated upstream; a template value is checked only after render (i.e. not at all — Slack/PagerDuty judge it)».
3. **Синтаксис шаблона при загрузке не проверяется** (как upstream). Сломанный шаблон → при отправке warn-лог + метрика fallback + значение фиксированного форматтера (существующее поведение `templateRenderer.renderField`).
4. **Отрендеренное значение не валидируется**: `overlaySlack`/`overlayPagerDuty` передают его как есть (upstream так же).

Ошибка для невалидного литерала меняет имя тега в тексте: `failed on the 'template_expr|slack_color' tag`. Это часть текста ошибки, не контракт.

## Tests

- **T1** (`routing`, новый `template_fields_validation_test.go`): YAML-конфиг с каждым шаблонным полем парсится `NewRouteConfigParser().Parse`; значения сохраняются verbatim.
- **T2** (там же): статичные `color: red`, `#12345`, `channel: ops`, `icon_emoji: fire`, `icon_url: not a url`, `title_link: nope`, `severity: bogus` — ошибка с именем поля; статичные валидные (`danger`, `#FF0000`, `#ops`, `:fire:`, `critical`) — проходят.
- **T3** (`infrastructure/publishing/template_formatter_test.go`): `color` с upstream-дефолтом → `danger` на firing, `good` на resolved в `attachments[0].color`.
- **T4** (`application/templates_delivery_e2e_test.go`): `RouteConfig` из YAML через парсер (не программно) → доставка firing-алерта → `color == "danger"` на проводе. Закрывает пробел, из-за которого баг не ловился.

## Rollout

Без миграций. Строка в `CHANGELOG.md` `[Unreleased]` → Fixed.
