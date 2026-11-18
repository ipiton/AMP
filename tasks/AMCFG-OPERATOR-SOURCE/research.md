---
id: AMCFG-OPERATOR-SOURCE
slug: amcfg-operator-source
stream: pilot-replacement
type: feature
artifact: research-pack
status: final
created_at: 2026-11-18
updated_at: 2026-11-18
---

# Research Pack - AlertmanagerConfig через prometheus-operator

## 0) TL;DR

Вариант (б2) из `docs/06-planning/AMCFG-RESEARCH-2026-11-14.md`, проверен `AMCFG-SPIKE`: **go**. Доказательства — `docs/06-planning/AMCFG-SPIKE-2026-11-18.md`.

## 1) Questions

Вопросы и ответы — в отчёте спайка, § «Вердикт по вопросам».

## 2) Findings

Новые факты сверх исследования 2026-11-14, повлиявшие на дизайн:

- `list secrets` роли discovery (ADR-012) в ns релиза читает `-generated` с токенами AMC.
- При авторизации `/-/reload` закрыт, а reloader не умеет подставлять учётные данные отдельно от URL.
- Оператор разбирает базовый конфиг строго: неизвестное поле отклоняет весь конфиг, прошлый `-generated` остаётся.

## 3) Options

Сравнение вариантов — § 6 исследования 2026-11-14; владелец выбрал (б2) и CR в чарте AMP (§ 8).

Для авторизации reloader рассматривались: открыть `/-/reload` без пароля (отклонено, расширяет поверхность), `--reload-method=signal` (нужен `shareProcessNamespace`, reloader видит процессы AMP), URL из Secret (выбрано).

## 4) Decision

Opt-in `alertmanager.configSource: operator`; подробности — `Spec.md`, ADR-020.

## 5) Spec Inputs

Пути оператора `/etc/alertmanager/{config,config_out,certs}`, Secret'ы `alertmanager-<name>-generated` и `-tls-assets-0`, аргументы reloader — из StatefulSet, созданного оператором v0.70.

## 6) References

- `docs/06-planning/AMCFG-RESEARCH-2026-11-14.md`
- `docs/06-planning/AMCFG-SPIKE-2026-11-18.md`
