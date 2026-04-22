# PARITY-A4-ADVANCED-FILTERING — Requirements

## Проблема

AMP сейчас не является полной заменой Alertmanager по API: `GET /api/v2/alerts` и
`GET /api/v2/silences` не поддерживают параметр `filter`, обязательный для Alertmanager API v2.

Существующие ограничения:
- `GET /api/v2/alerts` принимает только `status` и `resolved` — нет фильтрации по label-матчерам
- `GET /api/v2/silences` возвращает весь список без фильтрации (`store.List()` без параметров)
- Комментарии в коде явно отмечают это как технический долг:
  - `alerts.go:57` — "Advanced filtering (regex, matchers) will be added later"
  - `silences.go:61` — "Filtering by label matchers can be added here later"

## Контекст

Alertmanager API v2 определяет `filter` как query param типа `[]string`:

```
GET /api/v2/alerts?filter=alertname%3D~"Watchdog"&filter=severity%3D"critical"
GET /api/v2/silences?filter=alertname%3D~"Watchdog"
```

Каждый `filter` — это строка в формате PromQL label matcher:
- `name="value"` — точное совпадение
- `name!="value"` — не равно
- `name=~"regex"` — regex совпадение
- `name!~"regex"` — regex исключение

Без этого AMP-клиент (Grafana, alertmanager-bot, внутренние скрипты) не может фильтровать
алерты/сайленсы по labels, что блокирует production-замену Alertmanager.

## Success Criteria

1. `GET /api/v2/alerts?filter=<matcher>` фильтрует алерты по label-матчерам (AND-логика)
2. `GET /api/v2/silences?filter=<matcher>` фильтрует сайленсы по label-матчерам матчеров
3. Оба эндпоинта принимают несколько `filter` параметров одновременно (multi-value query param)
4. Синтаксис матчеров совместим с Alertmanager: `name="v"`, `name!="v"`, `name=~"r"`, `name!~"r"`
5. Некорректный синтаксис матчера возвращает `400 Bad Request` с понятным сообщением
6. Существующие параметры (`status`, `resolved`) продолжают работать без изменений
7. Behavior при пустом `filter` идентичен текущему (возвращает всё)

## Scope

**In scope:**
- Парсер строк формата `{name}{op}{value}` (4 операции)
- Фильтрация в `handleAlertsGet` (по labels алерта)
- Фильтрация в `handleSilencesGet` (по матчерам сайленса — если сайленс матчит те же labels)
- Unit-тесты парсера и фильтрации

**Out of scope:**
- Фильтрация `GET /api/v2/alerts/groups`
- Pagination для alerts (нет в Alertmanager API)
- `active` / `inhibited` / `silenced` query params для alerts (Alertmanager расширения)
- Изменения в schema хранилища или PostgreSQL-репозитории
