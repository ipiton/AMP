# Smoke: реальный бинарь, реальный webhook-приёмник (2026-10-20)

Скрипт: `evidence/smoke-timings.sh <бинарь> <каталог>`, приёмник `evidence/timing_receiver.py` (пишет время прихода по пути). Нативный запуск `go-app/cmd/server` в lite-профиле, `ENVIRONMENT=development` (plain-http URL, как в `deploy/smoke`). Конфиг — `evidence/smoke-config.yaml`:

- корень: `group_wait: 3s`, `group_interval: 10s`, `repeat_interval: 1h`;
- `route="inherit"` без таймингов, внутри — `sub="grand"` без таймингов;
- `route="zero"` с `group_wait: 0s`.

Четыре алерта отправлены подряд (`POST /api/v2/alerts`), время отправки фиксируется после ответа сервера, поэтому у `ZeroAlert` на ветке задержка получается слегка отрицательной: доставка пришла раньше, чем `curl` вернул управление.

## До: `main` @ `28007e8` (сырые данные — `smoke-main.log`)

| Алерт | Отправка → доставка | `"Alert routed to group"` |
|---|---|---|
| RootAlert | 3.02s | `group_wait` 3s, `group_interval` 10s |
| InheritAlert | 30.05s | 30s, 5m |
| GrandAlert | 29.57s | 30s, 5m |
| ZeroAlert | 2.82s | 3s, 5m |

Оба бага воспроизводятся: наследники получают 30s/5m вместо таймингов корня, явный `0s` заменяется значением корня.

## После: `bugfix/route-timings` @ `91265a3` (сырые данные — `smoke-branch.log`)

| Алерт | Отправка → доставка | `"Alert routed to group"` |
|---|---|---|
| RootAlert | 2.96s | 3s, 10s |
| InheritAlert | 2.96s | 3s, 10s |
| GrandAlert | 2.82s | 3s, 10s |
| ZeroAlert | −0.06s (сразу) | 0, 10s |

За 20 с ожидания каждый приёмник получил ровно одно сообщение: повторов на `group_interval` 10s нет (содержимое групп не менялось, nflog отсекает).
