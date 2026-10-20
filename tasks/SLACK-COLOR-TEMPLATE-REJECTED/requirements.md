---
id: SLACK-COLOR-TEMPLATE-REJECTED
slug: slack-color-template
stream: Config
type: bug
priority: high
status: active
created_at: 2026-10-20
updated_at: 2026-10-20
---

# Requirements: шаблонные presentation-поля не валидируются как литералы при загрузке

## Problem Framing

- **Symptom:** конфиг sema-prod (receiver `errors-slack`, `color: '{{ if eq .Status "firing" }}warning{{ else }}good{{ end }}'`) не загружается: `RouteConfig.Receivers[N].SlackConfigs[0].Color ... failed on the 'slack_color' tag`, AMP не стартует.
- **Root Cause:** struct-теги `routing.SlackConfig`/`PagerDutyConfig` проверяют формат литерала (`slack_color`, `slack_channel`, `emoji`, `url`, `oneof`) у полей, которые с TEMPLATES-EPIC slice 2 рендерятся шаблонизатором при отправке. Значение-шаблон формат литерала не проходит никогда. E2E-тесты шаблонов собирают `RouteConfig` программно и парсер обходят — поэтому баг не ловился.
- **Why Now:** одна из трёх предпосылок запуска AMP рядом с Alertmanager в sema-prod (NEXT.md § Operations).
- **How We Measure:** конфиг с шаблонным `color` (sema и upstream-дефолт) парсится; `color` рендерится в attachment по статусу; статичный невалидный цвет отклоняется.

## Risk Profile

- **Signals:** `R`
  - `R` — меняется поведение загрузки конфига (что принимается при старте/reload) и, через это, что уходит в Slack/PagerDuty.
- **Tier:** Standard
- **Notes:** `C` нет — схема конфига та же, расширяется только множество принимаемых значений (строго шире прежнего, ни один ранее валидный конфиг не ломается). Один пакет кода (`internal/infrastructure/routing`) + тесты.

## User Stories

1. Как оператор, мигрирующий с Alertmanager, я хочу загрузить `alertmanager.yml` с шаблонными `color`/`channel`/`icon_*`/`title_link`/`severity` без правок, чтобы AMP стартовал на моём конфиге.
2. Как оператор, я хочу, чтобы опечатка в статичном значении (`color: red`) по-прежнему ловилась при загрузке, а не в Slack.

## Success Criteria

- [ ] Конфиг с `color: '{{ if eq .Status "firing" }}danger{{ else }}good{{ end }}'` (upstream-дефолт) и с вариантом sema (`warning`/`good`) загружается парсером.
- [ ] Остальные шаблонные поля с жёсткими проверками (Slack `channel`, `icon_emoji`, `icon_url`, `title_link`; PagerDuty `severity`) принимают шаблон.
- [ ] Шаблонный `color` рендерится в attachment как `danger` для firing и `good` для resolved.
- [ ] Статичные невалидные значения (`color: red`, `channel: ops`, `icon_emoji: fire`, `icon_url: not a url`, `severity: bogus`) по-прежнему отклоняются.
- [ ] Гейты AMP зелёные (или красные шаги явно разобраны).

## Non-Goals

- Проверка синтаксиса шаблонов полей при загрузке — upstream её не делает, AMP при ошибке рендера откатывается на фиксированный форматтер (см. Research).
- Валидация отрендеренного значения перед отправкой (upstream шлёт как есть).
- Поля, которые upstream шаблонизирует, а AMP не рендерит вовсе (`email_configs[].to`, `slack_configs[].actions[]`) — follow-up в BUGS.md.

## Constraints

- **Scope:** `go-app/internal/infrastructure/routing` (теги и валидатор), тесты в `routing`, `infrastructure/publishing`, `application`. `slack_client.go` и его тесты не трогать (параллельная задача SLACK-WEBHOOK-PLAIN-OK-RESPONSE).
- **Security:** не применимо — presentation-поля, кредов и эндпоинтов не касается (`api_url` по-прежнему `url,https_production`).
- **Compatibility:** только расширение принимаемых значений; миграций нет.

## Discovery Notes

- Similar tasks: final review finding 7 (`receiver_name_charset_test.go`) — тот же класс «AMP строже upstream и не грузит upstream-конфиг».
- Relevant patterns: `core.IsTemplateExpression` (`internal/core/publishing_templates.go`) — единственное определение «значение — шаблон», им же пользуется рендерер (`template_formatter.go:154`, литерал не рендерится).
- Open unknowns: none.

## Research (mini)

- Source: `parser.go:52-59, 750-791`, `receiver.go` (теги), `business/publishing/config_target_templates.go` (какие поля шаблонные), `infrastructure/publishing/template_formatter.go` (рендер и overlay), upstream v0.34 notifiers.
- Key findings:
  - Шаблонные поля с проверкой литерала, ломающей шаблон: Slack `channel` (`slack_channel`), `icon_emoji` (`emoji`), `icon_url` (`url`), `title_link` (`url`), `color` (`slack_color`); PagerDuty `severity` (`oneof=critical error warning info`). Комментарий `pagerDutyTemplateFields` прямо обещает работу `severity: '{{ .CommonLabels.severity }}'` — парсер её отвергал.
  - `title`/`text`/`pretext`/`username`/`description`/`details`/telegram `message`/email `subject`/`html`/`text`/`headers` валидаторов не имеют — ок.
  - Синтаксис шаблонов полей при загрузке нигде в репо не проверяется (`pkg/templatevalidator` — для файлов шаблонов, на путь загрузки не подключён); upstream тоже не проверяет — ошибка видна при отправке. AMP при ошибке рендера логирует и откатывается на фиксированный форматтер с метрикой fallback.
  - Отрендеренный цвет `overlaySlack` кладёт в attachment как есть — как upstream.
  - go-playground/validator v10.28 поддерживает OR-теги: `omitempty,template_expr|slack_color` — проверено пробой (`template_expr|oneof=…`, `template_expr|url`).
  - Не исправляется здесь (follow-up): `email_configs[].to` (`required,email`) — upstream шаблонизирует и допускает список адресов, AMP `to` не рендерит; `slack_configs[].actions[]` (`url`, `oneof` на `style`) — AMP actions не отправляет вовсе. Разрешить шаблон там без рендера — значит слать литерал `{{ … }}`.
- Decision: тег `template_expr` (через `core.IsTemplateExpression`), OR-комбинация с прежним правилом на шести полях; статичные значения валидируются как раньше.
