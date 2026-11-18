# Heartbeat при переходе с основного Alertmanager на AMP

Runbook для `sema-prod`. Все шаги выполняются только с подтверждения владельца. Значения URL и токенов не печатать.

## Как устроено сейчас

| | Основной AM (`sema-monitoring`) | AMP (`amp`) |
|---|---|---|
| Маршрут `Watchdog` | `heartbeat` | `drop-unmatched` |
| Таймеры маршрута | `group_wait 0s`, `group_interval 1m`, `repeat_interval 2m` | те же |
| Фактический интервал | 3 минуты, 480 пингов в сутки | пингов нет |
| Проверка healthchecks.io | `sema-prod-watchdog`, период 5m, grace 5m | нет |
| Где лежит URL | Secret `alertmanager-heartbeat`, ключ `url` | будет `amp-notify`, ключ `heartbeat_url` |

## Главное правило

**У AMP своя проверка `sema-prod-amp-watchdog`, и URL основного AM в него не копируется ни на каком этапе.** Если оба AM пингуют одну проверку, живой AMP скроет падение основного AM.

Своя проверка остаётся у AMP и после перехода. В момент перехода URL не переключается, поэтому окна, где два отправителя делят одну проверку, нет. Старая проверка ставится на паузу вместе с выключением основного AM.

## Этап 1. Включить heartbeat в AMP (параллельная работа)

На этом этапе AMP начинает делать один внешний вызов: пинг своей проверки. Остальные receiver'ы остаются без интеграций. Основной AM не меняется.

1. В healthchecks.io создать проверку `sema-prod-amp-watchdog`: период 5m, grace 5m, те же каналы оповещения, что у `sema-prod-watchdog`. Отправить из неё тестовое уведомление.

2. Добавить URL в Secret (команды для zsh, ввод не отображается и не попадает в историю):

   ```bash
   read -rs 'HC_URL?AMP ping URL: '
   kubectl --context sema-prod -n amp patch secret amp-notify --type merge -p "{\"stringData\":{\"heartbeat_url\":\"$HC_URL\"}}"
   unset HC_URL
   ```

3. Проверить, что URL отличается от URL основного AM. Команда печатает только результат:

   ```bash
   bash -c 'a=$(kubectl --context sema-prod -n amp get secret amp-notify -o jsonpath="{.data.heartbeat_url}" | base64 -d | tr -d "[:space:]"); b=$(kubectl --context sema-prod -n sema-monitoring get secret alertmanager-heartbeat -o jsonpath="{.data.url}" | base64 -d | tr -d "[:space:]"); if [ -z "$a" ] || [ -z "$b" ]; then echo "STOP: empty value"; elif [ "$a" = "$b" ]; then echo "STOP: same URL"; else echo "OK: URLs differ"; fi'
   ```

   Продолжать только при `OK: URLs differ`.

4. Подождать появления файла в поде, около минуты:

   ```bash
   kubectl --context sema-prod -n amp exec amp-0 -c amp -- test -s /etc/amp/secrets/heartbeat_url && echo present
   ```

5. Выгрузить текущие values. Файл может содержать чувствительные значения, держать его вне репозитория:

   ```bash
   (umask 077; helm --kube-context sema-prod -n amp get values amp -o yaml > "$HOME/amp-values.yaml")
   ```

6. Внести в `~/amp-values.yaml` две правки. `extraVolumes` и `extraVolumeMounts` уже подключают `amp-notify` в `/etc/amp/secrets`, их не менять.

   В маршруте `alertname="Watchdog"` заменить только receiver, таймеры оставить:

   ```yaml
   - receiver: heartbeat   # было drop-unmatched
     matchers:
       - alertname="Watchdog"
   ```

   В `alertmanager.config.receivers` добавить:

   ```yaml
   - name: heartbeat
     webhook_configs:
       - url_file: /etc/amp/secrets/heartbeat_url
         send_resolved: false
   ```

7. Применить тем же чартом, что у текущей ревизии (`amp-0.1.0`, тег `v0.1.0`), затем удалить файл. Под перезапустится:

   ```bash
   helm --kube-context sema-prod -n amp upgrade amp <путь-к-чарту-v0.1.0> -f "$HOME/amp-values.yaml"
   ```

   ```bash
   rm "$HOME/amp-values.yaml"
   ```

8. Через 10 минут проверить:
   - `sema-prod-amp-watchdog` зелёная, пинги идут примерно каждые 3 минуты;
   - `sema-prod-watchdog` зелёная, основной AM работает как раньше;
   - у AMP растёт `alertmanager_notifications_total{integration="webhook"}`, а `alertmanager_notifications_failed_total{integration="webhook"}` не растёт.

Откат этапа: вернуть `receiver: drop-unmatched` у маршрута `Watchdog`, выполнить `helm upgrade`, поставить `sema-prod-amp-watchdog` на паузу. Ключ в Secret ни на что не влияет, его можно удалить отдельно.

## Этап 2. Переход на AMP

Выполняется вместе с общим переходом, когда закрыты остальные блокеры (AlertmanagerConfig, доступ к API, PostgreSQL).

1. Убедиться, что `sema-prod-amp-watchdog` зелёная и получает пинги. URL в AMP не менять.
2. Поставить `sema-prod-watchdog` на паузу непосредственно перед выключением основного AM. Иначе придёт ложная тревога.
3. Переключить Prometheus и выключить основной AM по общему плану перехода.
4. В течение 15 минут убедиться, что пинги в `sema-prod-amp-watchdog` идут без пропусков.
5. Перенацелить `AlertmanagerFailedToSendAlerts` и `AlertmanagerClusterFailedToSendAlerts` на AMP. Сейчас они смотрят только на `job="kube-prometheus-stack-alertmanager"`, `namespace="sema-monitoring"` и сбои доставки в AMP не увидят.

Откат: включить основной AM, вернуть Prometheus на него, снять `sema-prod-watchdog` с паузы. AMP может продолжать пинговать свою проверку.

## Что проверено и что нет

- `deploy/e2e-heartbeat/run.sh` проверяет на Docker интервал с боевыми таймерами, тишину при остановке AMP, возобновление после запуска и тишину после истечения `Watchdog`.
- Правка из шага 6, наложенная на values ревизии 4 релиза `amp`, проходит `amtool check-config`, и `Watchdog` попадает в `heartbeat`. Проверка выполнялась без применения в кластер.
- Обращение к настоящему healthchecks.io и путь из `sema-prod` до него этим тестом не покрыты. Их подтверждает шаг 8 этапа 1.
