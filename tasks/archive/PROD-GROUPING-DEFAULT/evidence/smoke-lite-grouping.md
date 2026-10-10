# Smoke: группировка в lite-профиле

- Дата: 2026-10-10, ветка `bugfix/prod-grouping-default` @ `21dde05` (код не изменён относительно `main` @ `c84c020`).
- Команда: `./deploy/smoke/run.sh` (`deploy/smoke/config.yaml`: `profile: lite`, `grouping.enabled: true`, без Postgres/Redis).
- Результат:

```text
[smoke] posting alert 'SmokeTestAlertDelivered'
[smoke] waiting group_wait + margin (8s) for delivery
[smoke] PASS: webhook received exactly 1 real delivery for 'SmokeTestAlertDelivered'
[smoke] PASS: webhook payload is v4-shaped (version=4, receiver=default) and carries alertname='SmokeTestAlertDelivered' severity=warning
[smoke] PASS: silence suppressed the notification for 'SmokeTestAlertSilenced'
[smoke] PASS: /-/reload applied the modified config (config.original now contains the reload marker receiver)
[smoke] ALL PASS
```

- Вывод: в `lite` алерт попадает в группу и доставляется после `group_wait` — утверждение доков «lite игнорирует `grouping.enabled`» неверно.
- Не проверено: тот же сценарий без ключа `grouping:` в конфиге (дефолт) — появится после реализации.
