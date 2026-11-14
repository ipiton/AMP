# Пилот AMP в k3s: промежуточный срез 2026-11-14

Срез снят в режиме только чтения: в кластере ничего не применялось, не патчилось и не рестартовалось. Секреты не читались: конфиги AM разобраны до имён receiver'ов и типов интеграций, в тексте ошибки Telegram URL замаскирован.

## Опорные точки времени

| Событие | CET | UTC | unix |
|---|---|---|---|
| helm rev 1 (install) | 2026-11-09 10:37:10 | 09:37:10Z | ~1794217020 |
| helm rev 2 (upgrade), amp-0 стартовал | 2026-11-09 11:02:16 | 10:02:26Z | 1794218580 (10:03Z) |
| **helm rev 3 (фиксы)**, amp-0 стартовал | **2026-11-14 10:51:33** | **09:51:44Z** | 1794649920 (09:52Z) |
| loki-0 перезапущен (ruler → AMP) | 2026-11-14 10:51:52 | 09:51:52Z | |
| Момент среза `T` | 2026-11-14 20:50 | 19:50:00Z | **1794685800** |

Окно «после фикса» = `T − 1794649920` = **35880s** (~9ч58м). Окно «пилот» = `[468780s]` от rev 1 либо `[5d]`.

Важное отличие от брифа: kube-prometheus-stack, основной AM и Loki живут в ns **`sema-monitoring`**, а не в `monitoring`.

## 1. Стабильность

### Поды ns `amp`

| Под | Статус | Restarts | Age | Примечание |
|---|---|---|---|---|
| amp-0 | Running 1/1 | 0 | 9h | пересоздан при helm rev 3 (start 09:51:44Z) |
| amp-postgres-0 | Running 2/2 (postgres, postgres-exporter) | 0 / 0 | 5d10h | с момента install не перезапускался |

- `kube_pod_container_status_restarts_total{namespace="amp"}`: 0 для всех контейнеров.
- `container_oom_events_total` за 5d: 0.
- За пилот было три инстанса amp-0 (IP .163, .173, .186): rev 1, rev 2, rev 3. Все старты совпадают с helm-ревизиями, крашей не было.
- `kubectl get events -n amp`: пусто (события живут ~1ч, недельной истории нет).

### Логи amp (Loki, `{namespace="amp"}`, 6d, все три инстанса amp-0)

`kubectl logs` видит только текущий инстанс (с 09:51Z), поэтому неделя собрана из Loki.

| Уровень | Кол-во за 6d |
|---|---|
| INFO | 4735 |
| WARN | 6 |
| ERROR | 0 |
| panic / goroutine trace | 0 (2 совпадения по `panic` — это имя receiver `panic-telegram`) |

WARN по типам:

| Кол-во | Сообщение | Оценка |
|---|---|---|
| 3 | `HTTP authentication is off: set --web.config.file ...` | по одному на старт, известно (внутрикластерный сервис без auth) |
| 2 | `repeat_interval is greater than the data retention period` (маршруты moving-cron, оба уровня) | появилось с rev 3; repeat_interval маршрута moving-cron больше retention AMP — уведомления могут повторяться чаще ожидаемого |
| 1 | `Notify attempt failed, will retry later`, receiver panic-telegram, alert MLQualityDriftBelow7dMA, 10:46:06Z: `telebot: Post "<URL>": read tcp ...->149.154.166.110:443: connection reset by peer` | транзиентная сетевая ошибка, повтор через 0.36 с — `Notify success` |

Фон INFO: по 121 строке `Starting target discovery` / `Successfully listed secrets` / `Target discovery complete` за 10ч, то есть K8s-Secret publishing discovery раз в ~5 мин.

Логи postgres (6d):
- `FATAL: pg_hba.conf rejects connection for host "10.42.0.1", user "nobody", database "nobody"` ×3, в 09:37:48Z (11-09), 10:02:25Z (11-09) и 09:51:43Z (11-14). Время совпадает со стартами amp-0; вероятно, это init-контейнер `wait-for-postgres` (pg_isready без user/db). Шум, не ошибка.
- `initdb: warning: enabling "trust" authentication for local connections` ×1, при initdb.
- postgres-exporter: `Error loading config ... postgres_exporter.yml: no such file` ×1 (warn, дефолтный конфиг).

## 2. Уведомления

У `alertmanager_notifications_total` нет метки `receiver` ни в основном AM (0.28.1), ни в AMP (база 0.34.1), поэтому разбивка ниже только по `integration`. Привязку к receiver'ам даёт конфиг: в обоих AM `telegram` = `panic-telegram` + `moving-cron-vitaly-dm`, `slack` = `errors-slack`; `webhook` = `heartbeat` (Watchdog), и он есть **только в основном AM**.

### Точные счётчики (разности сырых значений; `increase()` экстраполирует, поэтому он только для справки)

| integration | Основной AM, пилот (rev2 10:03Z → T) | AMP, пилот (сумма инстансов) | Основной AM, до фикса (10:03Z → 09:52Z) | AMP, до фикса (инстанс .173) | Основной AM, после фикса | AMP, после фикса (инстанс .186) |
|---|---|---|---|---|---|---|
| slack | 54−46 = **8** | 0+2 = **2** | 52−46 = **6** | **0** | 54−52 = **2** | **2** |
| telegram | 715−554 = **161** | 167+23 = **190** | 693−554 = **139** | **167** (+20.1%) | 715−693 = **22** | **23** (+1) |
| webhook | 11140−8545 = 2595 | 0 | 10941−8545 = 2396 | 0 | 11140−10941 = 199 | 0 |
| остальные (email, pagerduty, …) | 0 | 0 | 0 | 0 | 0 | 0 |

`alertmanager_notifications_failed_total`: **0 по всем интеграциям** в обоих AM за весь пилот. Единственная неудачная попытка Telegram в AMP была повторена успешно (см. логи), в failed_total она не попала.

Для справки, `increase()`: за `[5d]` slack amp=2 / main=6, telegram amp=174 / main=148, webhook main=2400; за `[35880s]` slack 2/2, telegram 23/22, webhook main=199.

### Slack из AMP после фикса

Да, **2 уведомления** в час 10:03–11:03Z, синхронно с основным AM (+2 в тот же час). Источник — Loki-алерт `LokiErrorSignalStructured` (ns sema-prod, signal=error), firing 10:04:16Z → resolved 10:08:16Z: одно firing и одно resolved уведомление. В алерт-истории AMP (PG, таблица `alerts`) это единственный алерт с signal=error. До фикса Loki ruler в AMP не слал, поэтому Slack у AMP был 0.

История Slack основного AM по часам за пилот: 11-09 15:03Z (2), 11-12 09:03Z (2), 11-13 15:03Z (1), 16:03Z (1), 11-14 11:03Z (2). AMP — только 11-14 11:03Z (2).

### Почему Telegram у AMP был +20% до фикса (вывод из конфигов, не измерено напрямую)

В ns moving с 11-08 висят 3 алерта `KubeJobFailed`. В основном AM их ловит `moving/moving-cron` → дочерний маршрут `moving/moving-cron/null` (без интеграций), и в Telegram они не идут. В AMP до rev 3 этого маршрута не было: алерты падали в корневой `panic-telegram` и повторялись по repeat_interval, отсюда 167 против 139. После rev 3 маршрут в AMP есть (`moving-cron-vitaly-dm` → `moving-cron-null`), и Telegram сравнялся: 23 против 22.

### Маршруты (сверка, без секретов)

Деревья совпадают по структуре и матчерам. Различия ожидаемые:
- `Watchdog`: основной AM → `heartbeat` (webhook), AMP → `drop-unmatched`.
- Имена moving-cron: в основном AM `moving/moving-cron/...` (генерирует оператор), в AMP `moving-cron-...`. Дочерний матчер в основном AM записан как `match: {alertname: KubeJobFailed}`, в AMP как `matchers: alertname="KubeJobFailed"`; по смыслу одно и то же.

## 3. Совпадение активных алертов (`/api/v2/alerts?active=true&silenced=true&inhibited=true`, ~19:50Z)

| | Основной AM | AMP |
|---|---|---|
| Всего | 9 | 9 |
| Только здесь (по alertname+fingerprint) | **0** | **0** |

Множества совпадают полностью, fingerprint'ы идентичны:

| alertname | fingerprint | state | ns | receiver main / AMP |
|---|---|---|---|---|
| CPUThrottlingHigh | 9e37ce403edb6713 | suppressed | amp (postgres) | panic-telegram / panic-telegram |
| CPUThrottlingHigh | a31d8e229e9e9283 | active | moving | panic-telegram / panic-telegram |
| InfoInhibitor | 7e00cdad5c270354 | active | amp | drop-unmatched / drop-unmatched |
| KubeJobFailed ×3 | 1f1bb7f8…, 3106bc55…, d5f192e9… | active | moving | moving/moving-cron/null / moving-cron-null |
| MLQualityDriftBelow7dMA ×2 | 134e8a35…, 16c62e54… | active | sema-prod | panic-telegram / panic-telegram |
| Watchdog | f2a09b9a9fe2776c | active | — | heartbeat / drop-unmatched |

- Watchdog есть в обоих AM, различается только receiver, как и ожидалось.
- Ожидаемое расхождение «ml-service optimization_alerts шлёт только в основной AM» в момент среза **не проявилось**: в основном AM нет алертов, которых нет в AMP. Оба `MLQualityDriftBelow7dMA` пришли из Prometheus-правила (есть метка `prometheus=`), поэтому есть в обоих. Прямых пушей ml-service на момент среза не было; для проверки нужно поймать момент, когда такой алерт активен.

## 4. Prometheus → Alertmanager'ы

| Метрика | Значение |
|---|---|
| `prometheus_notifications_alertmanagers_discovered` | 2 (за пилот 259 из 261 точек с шагом 30м; 2 точки = 1, во время рестартов) |
| `prometheus_notifications_errors_total{alertmanager="http://amp.amp.svc:9093/..."}` | 1 за пилот, ровно в окне 09:47–09:57Z 11-14 (рестарт amp-0 на rev 3) |
| `prometheus_notifications_errors_total{alertmanager=<основной AM .192>}` | 0 |
| `prometheus_notifications_dropped_total` (без разбивки по AM) | сырое 173, `increase[5d]` = **0** (все дропы до пилота) |
| `increase(prometheus_notifications_sent_total[5d])`, AMP / основной | 38507 / 38507 |
| то же за `[35880s]` | 3074 / 3074 |
| `prometheus_notifications_latency_seconds{quantile="0.99"}`, AMP / основной | 0.024 s / 0.022 s |
| `prometheus_notifications_queue_length` | 0 |

Также в метриках есть серии для `10.42.0.165` и `10.42.0.5` с sent=0: это исторические endpoint'ы, к пилоту не относятся.

Loki ruler (`loki_prometheus_notifications_*`, user=fake):

| | AMP | Основной AM |
|---|---|---|
| sent (с рестарта loki-0 09:51Z) | 14 | 14 |
| errors | 0 | 0 |
| dropped (общий) | 0 | |
| alertmanagers_discovered | 1 → **2** с ~10:07Z 11-14 | |

## 5. Ресурсы

PostgreSQL (`psql -U alert_history -d alert_history`, unix-socket):

| Показатель | Значение |
|---|---|
| `pg_database_size` | **10 MB** |
| `alerts`: строк / total / heap | **71** / 1176 kB / 112 kB (n_dead_tup 61, autovacuum 19:42Z) |
| Прочие таблицы | все < 130 kB; silences 0, publishing_dlq 0, config_versions 1 |
| Соединения | 6 client backend, max_connections 60 |
| `xact_commit` с initdb | 364355 (~0.78 tx/s) |
| pg_stat_statements, топ | `UPDATE alerts` 40926 вызовов, всего 52 s, среднее 1.27 ms; `SELECT … FROM alerts` 42078, 9.9 s; `SELECT 1` 109439 |

Диск (StorageClass `local-path`: `df` показывает весь диск узла, 193G и 47% занято, а не PVC; `kubelet_volume_stats` для local-path нет, поэтому замер через `du`):

| PVC | Ёмкость | Занято (du) |
|---|---|---|
| data-amp-0 (`/data`) | 1Gi | 8 KB |
| postgres-data-amp-postgres-0 (`$PGDATA`) | 5Gi | 66 MB |

CPU / память (`rate(container_cpu_usage_seconds_total[5m])`, `[5d:5m]`; `container_memory_working_set_bytes`):

| Контейнер | req / lim CPU | CPU сейчас | CPU avg 5d | CPU max 5d | mem сейчас | mem max 5d | req / lim mem |
|---|---|---|---|---|---|---|---|
| amp | 100m / 500m | 1m | 0.8m | 2.1m | 48Mi | 48.6Mi | 128Mi / 512Mi |
| postgres | 100m / 500m | 37m | 37.7m | 60.7m | 58Mi | 78.9Mi | 192Mi / 512Mi |
| postgres-exporter | 10m / 100m | ~0 | 0.2m | 0.5m | 7Mi | 7.5Mi | 32Mi / 128Mi |

**Наблюдение:** у контейнера postgres **37% CFS-throttling** (`container_cpu_cfs_throttled_periods / cfs_periods`, 5m), из-за этого с 11-09 09:53Z firing `CPUThrottlingHigh{container="postgres"}` (severity=info, подавлен InfoInhibitor). При среднем 37m и лимите 500m это значит, что нагрузка всплесковая. Сами запросы занимают ~64 s за 5 дней, их на 37m не хватает. Вероятная причина — ротация соединений: возраст client backend'ов — минуты, на каждую проверку соединения приходится `SELECT 1` (109k), и fork backend'а дорогой. Не подтверждено; на работу пилота пока не влияет.

## 6. `up` и алерты amp-postgres

- `up{namespace="amp"}`: только job `amp` (amp-0:9093), `min_over_time[5d]` = 1 для всех трёх инстансов, `avg_over_time` = 1. Простоев скрейпа не было.
- **postgres-exporter не скрейпится.** В ns amp есть только `ServiceMonitor amp` (приложение), для `amp-postgres-exporter:9187` его нет. `count({__name__=~"pg_.*"})` во всём Prometheus пуст.
- Отсюда: `PrometheusRule amp-postgres` (группа `postgresql.rules`, 11 правил) загружена, health=ok, все inactive, **но слепая**. Все выражения построены на `pg_*`, которых нет. `PostgreSQLDown` (`pg_up == 0`) не сработает даже при падении PG, потому что серии отсутствует.
- `ALERTS{alertname=~"PostgreSQL.*"}` за 7d: пусто.
- Прочие `ALERTS{namespace="amp"}` за 7d: CPUThrottlingHigh (postgres) pending→firing; InfoInhibitor firing; KubeContainerWaiting / KubePodNotReady / KubeStatefulSetReplicasMismatch только pending (рестарты при helm upgrade).

## Что не удалось снять и почему

- **Разбивка уведомлений по receiver**: у `alertmanager_notifications_total` нет метки receiver ни в одном из AM. Разбивка выведена из конфигов.
- **Недельная история событий ns amp**: k8s events живут ~1ч, сейчас список пуст.
- **Недельные логи через `kubectl logs`**: видят только текущий инстанс amp-0 (с 09:51Z). Неделя взята из Loki, это покрытие полное.
- **Использование PVC через kubelet_volume_stats**: для local-path метрик нет, `df` показывает диск узла. Использован `du`.
- **Метрики PostgreSQL (`pg_*`)**: exporter не скрейпится (см. п.6).
- **Расхождение по прямым пушам ml-service**: в момент среза таких алертов не было, проверить нечем.
- Счётчики Telegram/Slack показывают число отправленных уведомлений, а не содержимое. То, что в Telegram ушли одни и те же алерты, по метрикам не проверить.

## Вывод

- **Slack выровнялся**: после фикса AMP 2 = основной 2, в тот же час и от того же Loki-алерта. До фикса было 0 против 6, причина — Loki ruler не слал в AMP. Теперь Loki шлёт в оба AM (14/14, 0 ошибок).
- **Telegram выровнялся**: после фикса 23 против 22 (+1, ~4.5%). До фикса было 167 против 139 (+20%), вероятно из-за `KubeJobFailed` в moving без маршрута moving-cron → null. Остаток +1 в пределах шума (окна счётчиков, рестарт). Повторить на 16.11 на окне 2+ суток.
- **Оставшиеся расхождения**:
  - webhook `heartbeat`/Watchdog есть только в основном AM (~2600 за пилот). Это ожидаемо.
  - Прямые пуши ml-service в AMP не идут. Это ожидаемо, на срезе не проявилось.
- **Стабильность**: 0 крашей, 0 OOM и 0 failed-уведомлений. Одна ошибка доставки Prometheus → AMP пришлась на окно рестарта rev 3. Активные алерты совпадают 9/9 по fingerprint.
- **Найденные проблемы обвязки (не про паритет)**:
  1. postgres-exporter не скрейпится, поэтому все 11 правил `amp-postgres` слепые.
  2. postgres троттлится на 37% при среднем 37m CPU.
  3. WARN про `repeat_interval` > retention на маршрутах moving-cron.

## Запросы для повтора 16.11

Подготовка (только чтение):

```sh
kubectl --context sema-prod -n sema-monitoring port-forward svc/kube-prometheus-stack-prometheus 19090:9090
kubectl --context sema-prod -n sema-monitoring port-forward svc/kube-prometheus-stack-alertmanager 19093:9093
kubectl --context sema-prod -n amp port-forward svc/amp 19094:9093
kubectl --context sema-prod -n sema-monitoring port-forward svc/loki 13100:3100
helm history amp -n amp --kube-context sema-prod
```

Хелпер `pq.sh` (bash):

```bash
#!/bin/bash
q="$1"; t="${2:-}"
args=(--data-urlencode "query=$q"); [ -n "$t" ] && args+=(--data-urlencode "time=$t")
curl -s -G http://127.0.0.1:19090/api/v1/query "${args[@]}" | jq -r '.data.result[] | "\(.metric|del(.__name__)|tojson)\t\(.value[1])"'
```

Уведомления. Точные разности считаются по сырым значениям в моменты времени. Счётчик AMP сбрасывается при каждом пересоздании пода, поэтому берётся сумма по инстансам:

```promql
# сырое значение в момент t (подставлять T, F=1794649920, 1794218580)
sum by (job,integration) (alertmanager_notifications_total{integration=~"slack|telegram|webhook"})
# последний счётчик каждого инстанса AMP за пилот
max by (instance,integration) (max_over_time(alertmanager_notifications_total{job="amp",integration=~"slack|telegram|webhook"}[<окно>]))
# для справки
round(sum by (job,integration) (increase(alertmanager_notifications_total[5d])))
round(sum by (job,integration) (increase(alertmanager_notifications_failed_total[5d])))
# Slack по часам (query_range, step=3600)
sum by (job) (increase(alertmanager_notifications_total{integration="slack"}[1h])) > 0.5
resets(alertmanager_notifications_total{integration="telegram"}[5d])
process_start_time_seconds{job=~"amp|kube-prometheus-stack-alertmanager"}
```

Prometheus и Loki ruler:

```promql
prometheus_notifications_alertmanagers_discovered
prometheus_notifications_errors_total
increase(prometheus_notifications_errors_total[5d])
prometheus_notifications_dropped_total
increase(prometheus_notifications_dropped_total[5d])
round(increase(prometheus_notifications_sent_total[5d]))
prometheus_notifications_latency_seconds{quantile="0.99"}
# время ошибок (query_range step=600)
increase(prometheus_notifications_errors_total{alertmanager=~".*amp.*"}[10m]) > 0
sum by (alertmanager,user) (loki_prometheus_notifications_sent_total)
sum by (alertmanager,user) (loki_prometheus_notifications_errors_total)
sum(loki_prometheus_notifications_alertmanagers_discovered)
```

up, ALERTS, ресурсы:

```promql
up{namespace="amp"}
min_over_time(up{namespace="amp"}[5d])
count({__name__=~"pg_.*"})
max_over_time(ALERTS{alertname=~"PostgreSQL.*"}[7d])
count(count_over_time(ALERTS{namespace="amp"}[7d])) by (alertname, alertstate, container)
sum by (container) (rate(container_cpu_usage_seconds_total{namespace="amp",container!="",container!="POD"}[5m]))
max by (container) (max_over_time(sum by (container) (rate(container_cpu_usage_seconds_total{namespace="amp",container!="",container!="POD"}[5m]))[5d:5m]))
max by (container) (max_over_time(container_memory_working_set_bytes{namespace="amp",container!="",container!="POD"}[5d])) / 1048576
sum by (container) (increase(container_cpu_cfs_throttled_periods_total{namespace="amp"}[5m])) / sum by (container) (increase(container_cpu_cfs_periods_total{namespace="amp"}[5m]))
sum by (container) (increase(container_oom_events_total{namespace="amp"}[5d]))
max by (pod,container) (kube_pod_container_status_restarts_total{namespace="amp"})
```

Сверка активных алертов:

```sh
curl -s 'http://127.0.0.1:19093/api/v2/alerts?active=true&silenced=true&inhibited=true' > main.json
curl -s 'http://127.0.0.1:19094/api/v2/alerts?active=true&silenced=true&inhibited=true' > amp.json
jq -r '.[] | "\(.fingerprint)\t\(.labels.alertname)"' main.json | sort > main.tsv
jq -r '.[] | "\(.fingerprint)\t\(.labels.alertname)"' amp.json  | sort > amp.tsv
comm -3 main.tsv amp.tsv
```

Маршруты без секретов: `curl -s :PORT/api/v2/status | python3 -c '…yaml.safe_load(d["config"]["original"])…'`, выводить только `receivers[].name`, ключи `*_configs` и дерево route (receiver/matchers/continue).

Логи (Loki, LogQL, `time=T`):

```logql
sum by (pod_name, container_name) (count_over_time({namespace="amp"}[6d]))
sum by (level) (count_over_time({namespace="amp", container_name="amp"} | json [6d]))
sum by (level, msg) (count_over_time({namespace="amp", container_name="amp"} | json | level=~"WARN|ERROR" [6d]))
{namespace="amp"} |~ "(?i)panic|goroutine [0-9]+ \\["
{namespace="amp", container_name=~"postgres.*"} |~ "(?i)error|fatal|panic|warning"
```

Текст ошибок Telegram перед выводом прогонять через `sed -E 's#https?://[^ "]*#<URL>#g'`: в URL Telegram API зашит токен бота.

PostgreSQL:

```sh
kubectl --context sema-prod -n amp exec amp-postgres-0 -c postgres -- psql -U alert_history -d alert_history -X -P pager=off \
  -c "select pg_size_pretty(pg_database_size(current_database()));" \
  -c "select count(*), pg_size_pretty(pg_total_relation_size('alerts')) from alerts;" \
  -c "select relname,n_live_tup,n_dead_tup,pg_size_pretty(pg_total_relation_size(relid)),last_autovacuum from pg_stat_user_tables order by pg_total_relation_size(relid) desc;" \
  -c "select calls, round(total_exec_time::numeric) ms, left(regexp_replace(query,'\s+',' ','g'),70) from pg_stat_statements order by total_exec_time desc limit 6;"
kubectl --context sema-prod -n amp exec amp-0 -c amp -- du -sh /data
kubectl --context sema-prod -n amp exec amp-postgres-0 -c postgres -- sh -c 'du -sh "$PGDATA"'
kubectl --context sema-prod -n amp top pod --containers
```
