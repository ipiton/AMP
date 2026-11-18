# AMCFG-SPIKE: «теневой» CR `Alertmanager` для AMP

Дата: 2026-11-18. Вопрос: годится ли вариант (б2) из `AMCFG-RESEARCH-2026-11-14.md`.

## Вердикт: GO

Апгрейд оператора v0.70 ради AMP не нужен: базовый конфиг пилота проходит через модель оператора без потерь (условие решения владельца по вопросу 4). Значения секретов в отчёт и в вывод проверок не попадали; в тестовом кластере использовались поддельные токены.

## Стенд

| Компонент | Версия | Дата образа |
|---|---|---|
| k3s (контейнер `rancher/k3s`, вместо k3d: бинарника k3d на машине нет) | v1.29.15-k3s1 | 2025-03-25 |
| prometheus-operator, `bundle.yaml` тега | v0.70.0 (как в `sema-prod`) | 2023-11-30 |
| prometheus-config-reloader | v0.70.0 (как в `sema-prod`) | 2023-11-30 |
| AMP | собран из этой ветки | локальный |

Из `sema-prod` только чтение: `alertmanager.yml` ревизии 3 релиза `amp` (последняя с интеграциями) и spec `moving/moving-cron`. Ручная копия `moving-cron` из базового конфига для проверки убрана.

## Вердикт по вопросам

| Вопрос | Результат | Доказательство |
|---|---|---|
| Создаётся ли `alertmanager-<name>-generated` при `replicas: 0` | **Да** | Secret с ключом `alertmanager.yaml.gz`; StatefulSet `alertmanager-amp` 0/0; `Reconciled=True`. Побочно: Service `alertmanager-operated`, Secret'ы `-tls-assets-0`, `-web-config` |
| Round-trip базового конфига пилота | **Без потерь** | Структурное сравнение: только `continue: false` убран (значение по умолчанию) и добавлен пустой `templates: []` |
| `bot_token_file`, `slack_api_url_file`, `matchers`, `inhibit_rules` с `equal` | **Сохраняются** | Те же значения в том же месте; итог читается `config.LoadFile` upstream 0.34.1 |
| Маршрут AMC | **Как у основного AM** | Первым дочерним, `continue: true`, добавлен `namespace="moving"`, префикс `moving/moving-cron/` |
| `spec.version: v0.34.1` | **Принимается** | Вывод идентичен `v0.28.1` |
| Пути TLS | `/etc/alertmanager/certs/secret_<ns>_<secret>_<key>` | Содержимое — в Secret `alertmanager-<name>-tls-assets-0` |
| Секреты AMC | **Открытым текстом в `-generated`** | `bot_token`, `routing_key`, `api_url`, `auth_password`, `credentials` |
| Перезагрузка AMP | **Работает, без рестарта** | Новый AMC активен через 77–81 с (синхронизация Secret'а kubelet'ом); `restartCount` 0, тот же UID пода, `alertmanager_config_last_reload_successful 1` |
| Авторизация и path prefix | **Работает** | `/-/reload` без пароля — 401; reloader с URL из Secret перезагрузил AMP; пароля нет в логах и спецификации пода |
| Изоляция по namespace | **Работает** | Алерт `namespace=moving` ушёл в receiver AMC, тот же алерт из другого namespace — в корневой |

## Ограничения, которые надо учитывать

1. **Модель оператора строгая** (`yaml.UnmarshalStrict`). Неизвестное поле отклоняет весь базовый конфиг: `Reconciled=False`, прошлый `-generated` не меняется. Отказ безопасный, тихих потерь нет.
   - Отклоняются (проверено): `msteamsv2_configs`, `jira_configs`, `rocketchat_configs`, `mattermost_configs`, `incidentio_configs`, Telegram `message_thread_id`, `webhook_url_file` у Discord и MS Teams, Slack `app_token_file` и `timeout`, `global.telegram_bot_token_file`, `name` у `inhibit_rules`, `tracing`.
   - Проходят (проверено): `url_file` у webhook, `routing_key_file`, `api_key_file` у OpsGenie и VictorOps, Slack `api_url_file`, `http_config` с `credentials_file` и TLS-файлами, `time_intervals`, `active_time_intervals`, `mute_time_intervals`, `templates`.
2. **AMC с ошибкой пропускается** (нет Secret'а, битый CA) с предупреждением в логе оператора; остальные применяются.
3. **`$(VAR)` раскрывает reloader**; незаданная переменная даёт ошибку и конфиг не обновляется. Так же у основного AM.
4. **Селекторы:** без `alertmanagerConfigSelector` не выбирается ничего; без `alertmanagerConfigNamespaceSelector` — только свой namespace.

## Находка: `list secrets` в ns пилота

У пилота `publishing.enabled` по умолчанию `true`, а `publishing.discovery.namespace` пуст, поэтому роль discovery даёт ServiceAccount AMP `list secrets` в `amp`. RBAC не фильтрует `list` по меткам, так что AMP смог бы прочитать `-generated` с токенами AMC. Это шире, чем решил владелец («чтение Secret'ов в ns `amp` держать таким же узким, как в `sema-monitoring`»). Чарт теперь не рендерит такую комбинацию. В `amp` нет Secret'ов с `publishing-target=true`, так что на пилоте достаточно `publishing.enabled: false`.
