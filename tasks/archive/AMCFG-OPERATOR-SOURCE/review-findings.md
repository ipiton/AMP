# Deep Review Findings: AMCFG-OPERATOR-SOURCE

**Trigger classification:** mandatory (сигнал S, Full)
**Reviewer perspective:** независимый агент, пять проходов
**Reviewed at:** 2026-11-18
**Reviewed tree:** claude/amcfg-operator-source-b26de5 @ 29af20d
**Verdict:** pass (см. `review-verdict.json`)

Ревьюер проверял рендером и локальным запуском бинарника с поддельными данными. Кластер ревьюер не трогал.

## Findings

### 1 — Пароль в env reloader читается через `$(RELOAD_URL)` в AMC
- **Severity:** blocker
- **Disposition:** fix-here
- **Follow-up:** закрыта: в env только `POD_NAME`, `SHARD`

### 2 — `$(UNSET)` в AMC не даёт стартовать новому поду
- **Severity:** major
- **Disposition:** reject (reason: поведение основного Alertmanager; принято и описано в ADR-020 и README)
- **Follow-up:** n/a

### 3 — Неатомарное переключение режима
- **Severity:** major
- **Disposition:** fix-here
- **Follow-up:** `operator.prepare`, порядок и проверки в README

### 4 — Сбои reloader не видны в метриках
- **Severity:** major
- **Disposition:** defer-backlog
- **Follow-up:** `AMCFG-RELOADER-METRICS`

### 5 — Токен ServiceAccount доступен reloader
- **Severity:** major
- **Disposition:** fix-here
- **Follow-up:** запрет discovery в режиме оператора

### 6 — Пароль в логах и cmdline
- **Severity:** major
- **Disposition:** fix-here
- **Follow-up:** URL с паролем удалён

### 7 — `{}` не сбрасывает нижний слой values
- **Severity:** minor
- **Disposition:** fix-here
- **Follow-up:** описано в README

### 8 — Обход проверки auth через extraArgs и extraEnv
- **Severity:** minor
- **Disposition:** fix-here
- **Follow-up:** проверка не нужна после loopback-listener

### 9 — nil pointer при `--reuse-values`
- **Severity:** minor
- **Disposition:** fix-here
- **Follow-up:** проверка в `_validate.tpl`

### 10 — Проверка CRD не видела `Alertmanager`
- **Severity:** minor
- **Disposition:** fix-here
- **Follow-up:** проверка GVK

### 11 — Неточный список полей в README
- **Severity:** minor
- **Disposition:** fix-here
- **Follow-up:** исправлен

### 12 — Байтовый diff в режиме `values`
- **Severity:** nit
- **Disposition:** fix-here
- **Follow-up:** diff 0 строк

### 13 — Длинное имя ломает StatefulSet оператора
- **Severity:** nit
- **Disposition:** fix-here
- **Follow-up:** рендер отказывает при длине больше 50

### 14 — Образ reloader не закреплён
- **Severity:** nit
- **Disposition:** fix-here
- **Follow-up:** закреплён по digest

### N1 — Анонимный `/-/reload` публикуется через Ingress
- **Severity:** blocker
- **Disposition:** fix-here
- **Follow-up:** открытый путь больше не нужен

### N2 — Анонимный reload срывает доставку
- **Severity:** major
- **Disposition:** fix-here
- **Follow-up:** loopback-listener

### N3 — Обход проверки auth ведёт к устаревшему конфигу
- **Severity:** major
- **Disposition:** fix-here
- **Follow-up:** проверка удалена вместе с причиной

### N4 — Ответ на POST раскрывает фрагмент конфига
- **Severity:** minor
- **Disposition:** fix-here
- **Follow-up:** общий текст ошибки

### N5 — Список путей без probe проходил рендер
- **Severity:** minor
- **Disposition:** fix-here
- **Follow-up:** требование снято

### N6 — Неполные значения при `--reuse-values`
- **Severity:** minor
- **Disposition:** fix-here
- **Follow-up:** проверка `version`; часть про селектор снята: `null` неотличим от отсутствия ключа

### N7 — `AMCFG-RELOADER-METRICS` нет в планировании
- **Severity:** nit
- **Disposition:** defer-backlog
- **Follow-up:** запись в BACKLOG на finalize

### F1 — Webhook зацикливает перезагрузку через loopback
- **Severity:** major
- **Disposition:** fix-here
- **Follow-up:** перезагрузка только при изменении файла; 787 → 1 за 15 с

### F2 — Чарт по умолчанию ставит образ без флага
- **Severity:** major
- **Disposition:** fix-here
- **Follow-up:** рендер отказывает на теге `0.1.0`

### F3 — Граница доверия шире описанной
- **Severity:** minor
- **Disposition:** reject (reason: риск принят, описан в README и ADR-020)
- **Follow-up:** n/a

### F4 — Ошибка `Serve` завершает AMP без drain
- **Severity:** minor
- **Disposition:** reject (reason: редкий сбой, под перезапустится)
- **Follow-up:** n/a

### F5 — Дубль флага в `extraArgs` ломает reload
- **Severity:** nit
- **Disposition:** reject (reason: явная ошибка оператора чарта)
- **Follow-up:** n/a

### G1 — Пропуск по хэшу после загрузки другим путём
- **Severity:** minor
- **Disposition:** reject (reason: чарт эти пути не использует; описано в ADR-020)
- **Follow-up:** n/a

### G2 — Невалидный файл и webhook дают повторные попытки
- **Severity:** nit
- **Disposition:** reject (reason: dispatcher не перезапускается)
- **Follow-up:** n/a

### G3 — Изменение только внешних `templates:` не применяется
- **Severity:** nit
- **Disposition:** reject (reason: чарт кладёт в base Secret один файл)
- **Follow-up:** n/a

### G4 — Границы проверки тега образа
- **Severity:** nit
- **Disposition:** reject (reason: закрыт основной случай, тег по умолчанию)
- **Follow-up:** n/a

### H1 — Формулировка ADR про порог имени
- **Severity:** nit
- **Disposition:** fix-here
- **Follow-up:** уточнена

### H2 — G1 и mesh описаны только в ADR
- **Severity:** nit
- **Disposition:** reject (reason: внутренние ограничения)
- **Follow-up:** n/a

## Anti-Pattern Check

- [x] Self-audit was not treated as a substitute for independent review.
