# Smoke: lite без секции `grouping:` (дефолт)

- Дата: 2026-10-10, ветка `bugfix/prod-grouping-default` @ `4fc7300`.
- Команда: `./deploy/smoke/run.sh`; из `deploy/smoke/config.yaml` на время прогона вырезана вся секция `grouping:` (включая `reconciliation_interval`/`reconciliation_grace`), после прогона файл возвращён `git checkout`.
- Результат:

```text
[smoke] PASS: webhook received exactly 1 real delivery for 'SmokeTestAlertDelivered'
[smoke] PASS: webhook payload is v4-shaped (version=4, receiver=default) and carries alertname='SmokeTestAlertDelivered' severity=warning
[smoke] PASS: silence suppressed the notification for 'SmokeTestAlertSilenced'
[smoke] PASS: /-/reload applied the modified config (config.original now contains the reload marker receiver)
[smoke] ALL PASS
```

- Вывод: конфиг с `route:` и без ключа `grouping:` группирует и доставляет после `group_wait` — открытый вопрос из Spec и «не проверено» из `smoke-lite-grouping.md` закрыты.
- Оговорка: первая попытка упала (`got 0`), потому что моя правка конфига по ошибке вырезала и блок `route:`. Это ошибка подготовки прогона, не продукта; повтор с точным вырезанием восьми строк — зелёный.
