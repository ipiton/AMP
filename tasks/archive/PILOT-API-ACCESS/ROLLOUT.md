# Доступ к API пилота AMP: выкатка на `sema-prod`

Статус: шаги 1–5 выполнены в `sema-prod` 2026-11-18 с подтверждения владельца, результаты в `evidence/6-sema-prod-rollout.txt`. Правка Sema смержена в `main` (`4cec4497b`), `helmfile diff` из него для `kube-prometheus-stack` и `loki` пуст.

## Что меняется

| Шаг | Что | Перезапуск | Риск для алертов |
|---|---|---|---|
| 1 | NetworkPolicy на порт 9093 | нет | Нет, если селекторы верны. Клиенты не меняются |
| 2 | Secret'ы с кредами | нет | Нет |
| 3 | Prometheus и Loki шлют креды (дифф для Sema) | Prometheus, Loki | Обычный рестарт одной реплики. AMP без auth креды игнорирует |
| 4 | ServiceMonitor скрейпит с кредами | нет | Нет |
| 5 | AMP требует basic auth | `amp-0`, на стенде около 30 с | См. раздел «Потеря алертов» |

Порядок важен: **сначала клиенты, потом требование auth**. Клиент с кредами работает и с открытым AMP, а клиент без кредов после шага 5 получает 401.

## Клиенты AMP

| Клиент | Сейчас | Доступ |
|---|---|---|
| Prometheus, `sema-monitoring` | шлёт алерты, скрейпит `/metrics` | NetworkPolicy, basic auth |
| Loki ruler, `sema-monitoring` | шлёт алерты | NetworkPolicy, basic auth |
| kubelet (пробы) | `/-/healthy`, `/-/ready` | с узла, без кредов |
| Grafana, datasource на основной AM | к AMP не обращается | **после замены основного AM**: правило и креды |
| `ml-service` (`optimization_alerts`) | выключен, `GPT_ALERTMANAGER_*` нигде не заданы | **после замены**: код без basic auth, собственная egress-политика не пускает на 9093 |

В объектах кластера AMP упоминают только конфиг Loki и сам StatefulSet. Адрес для Prometheus лежит в Secret, он взят из values в Sema. Других клиентов в Sema и в кластере не найдено.

## Перед началом

Рабочий каталог: корень репозитория AMP на коммите этой ветки.

```bash
export CTX=sema-prod TASK=$PWD/tasks/archive/PILOT-API-ACCESS
```

Убедиться, что релиз не менялся и шаг 1 добавляет только NetworkPolicy. Команда ничего не применяет, значения Secret'ов `kubectl diff` скрывает. Ожидается один объект: `NetworkPolicy.amp.amp`.

```bash
helm template amp ./helm/amp -n amp --no-hooks --api-versions monitoring.coreos.com/v1 \
  -f <(helm --kube-context $CTX get values amp -n amp -o yaml) \
  -f $TASK/values-step1-networkpolicy.yaml \
  | kubectl --context $CTX diff -n amp --server-side=false -f - | grep '^+++'
```

Если параллельно выкатывались изменения других сессий (AlertmanagerConfig, heartbeat, PostgreSQL), в выводе появятся их объекты. Тогда сначала надо выяснить, чьи это изменения.

Функция для проверок на следующих шагах:

```bash
prom() { kubectl --context $CTX exec -n sema-monitoring prometheus-kube-prometheus-stack-prometheus-0 -c prometheus -- "$@"; }
```

## Шаг 1. NetworkPolicy

```bash
helm upgrade amp ./helm/amp -n amp --kube-context $CTX --reuse-values \
  -f $TASK/values-step1-networkpolicy.yaml
```

Проверка:

```bash
# посторонний под: curl завершается по таймауту, код 000
kubectl --context $CTX run amp-np-check -n default --rm -i --restart=Never --image=curlimages/curl:8.11.1 -- \
  curl -s -m 5 -o /dev/null -w '%{http_code}\n' http://amp.amp.svc:9093/-/healthy
# разрешённый клиент: OK
prom wget -qO- -T 5 http://amp.amp.svc:9093/-/healthy
```

Подождать 3 минуты и проверить в Prometheus:

- `rate(prometheus_notifications_errors_total{alertmanager=~".*amp.amp.svc.*"}[3m])` равно 0;
- `up{namespace="amp",job="amp"}` равно 1;
- `amp-0` Ready, рестартов не прибавилось.

Откат, без перезапуска пода:

```bash
helm upgrade amp ./helm/amp -n amp --kube-context $CTX --reuse-values --set networkPolicy.enabled=false
```

## Шаг 2. Secret'ы

Скрипт генерирует пароли, ничего не печатает и не перезаписывает существующие Secret'ы.

```bash
bash $TASK/create-secrets.sh $CTX
```

| Secret | Для чего |
|---|---|
| `amp/amp-web-config` | bcrypt-хеши пользователей `prometheus`, `loki`, `operator` |
| `amp/amp-metrics-auth` | скрейп через ServiceMonitor |
| `amp/amp-operator-auth` | ручной доступ владельца (amtool, curl) |
| `sema-monitoring/amp-prometheus-auth` | отправка алертов Prometheus |
| `sema-monitoring/amp-loki-auth` | отправка алертов Loki |

Эти Secret'ы живут только в кластере. Перенос в SOPS (как `alertmanager-heartbeat`) стоит сделать отдельно, иначе при пересоздании кластера их придётся выпускать заново.

## Шаг 3. Клиенты (Sema)

Применить `sema.diff` в репозитории Sema, закоммитить с согласия владельца, посмотреть дифф и выкатить по одному релизу:

```bash
git -C ~/code/own/Sema apply $TASK/sema.diff
(cd ~/code/own/Sema/infrastructure/helmfile && helmfile -e prod --kube-context $CTX -l name=kube-prometheus-stack diff)
(cd ~/code/own/Sema/infrastructure/helmfile && helmfile -e prod --kube-context $CTX -l name=kube-prometheus-stack sync)
```

Убедиться, что Prometheus поднялся и ошибок отправки нет, затем то же для Loki:

```bash
(cd ~/code/own/Sema/infrastructure/helmfile && helmfile -e prod --kube-context $CTX -l name=loki diff)
(cd ~/code/own/Sema/infrastructure/helmfile && helmfile -e prod --kube-context $CTX -l name=loki sync)
```

Проверка: `loki-0` и Prometheus Ready, счётчики ошибок отправки в **оба** Alertmanager не растут. Заголовок Loki уходит на оба адреса, поэтому при проблеме с файлом кредов пострадает и основной AM. Смотреть `loki_prometheus_notifications_errors_total` на `/metrics` Loki.

Откат: `git revert` коммита в Sema и `sync` тех же релизов. Если шаг 5 уже выполнен, сначала откатить его.

## Шаг 4. Креды для скрейпа

```bash
helm upgrade amp ./helm/amp -n amp --kube-context $CTX --reuse-values \
  -f $TASK/values-step2-scrape-credentials.yaml
```

Проверка: `up{namespace="amp",job="amp"}` остаётся 1.

## Шаг 5. Basic auth

Ещё раз проверить: шаги 3 и 4 выполнены, Secret `amp/amp-web-config` есть. Без него новый под не запустится, а старый к этому моменту уже будет удалён.

```bash
kubectl --context $CTX get secret -n amp amp-web-config amp-metrics-auth
helm upgrade amp ./helm/amp -n amp --kube-context $CTX --reuse-values \
  -f $TASK/values-step3-webauth.yaml
kubectl --context $CTX rollout status sts/amp -n amp --timeout=180s
```

Проверка:

```bash
# без кредов: 401
prom wget -qO- -T 5 http://amp.amp.svc:9093/api/v2/status
# пробы открыты: OK
prom wget -qO- -T 5 http://amp.amp.svc:9093/-/ready
```

В логе `amp-0` должна быть строка `HTTP authentication enabled` с `users` = 3. Через 3 минуты: ошибки отправки Prometheus и Loki в AMP не растут, `up{namespace="amp",job="amp"}` равно 1, в таблицу `alerts` приходят новые записи.

Откат (перезапускает `amp-0`, клиенты могут оставаться с кредами):

```bash
helm upgrade amp ./helm/amp -n amp --kube-context $CTX --reuse-values --set webConfig.existingSecret=
```

## Аварийный порядок отката

1. Шаг 5: выключить auth. Это снимает все 401.
2. Шаг 1: выключить NetworkPolicy, если остаются таймауты.
3. Креды клиентов и ServiceMonitor можно не трогать: открытый AMP их игнорирует.

## Потеря алертов

- Основной Alertmanager получает всё независимо от AMP, а доставку уведомлений AMP в варианте (б) не выполняет. Поэтому сбой на любом шаге затрагивает только историю в AMP.
- Prometheus и Loki повторяют активные алерты примерно раз в минуту. Алерт, который ещё горит после рестарта `amp-0`, дойдёт. Алерт, успевший и сработать, и разрешиться за время рестарта, в историю AMP может не попасть.
- На стенде за рестарт с включением auth ошибок отправки не было, но окно в 30 с короче цикла отправки, так что это не гарантия.
- **При замене основного AM этот порядок надо пересмотреть:** одна реплика без резерва уже будет влиять на доставку.

## Смена пароля

Обновить Secret `amp-web-config`, AMP перечитает файл без рестарта (на стенде новый пользователь заработал через 85 с). Безопасный порядок: добавить новый хеш, перевести клиента, удалить старый хеш.

## Что осталось открытым

- NetworkPolicy не закрывает трафик с самого узла. В `sema-prod` есть `hostNetwork`-поды (`node-exporter`, `metallb speaker`, `cert-manager`): они дойдут до порта, но после шага 5 получат 401.
- Трафик идёт по HTTP внутри кластера, пароли не шифруются. TLS в `web.config` AMP не поддерживает (ADR-011).
- ServiceMonitor и Prometheus Operator проверены на чарте `kube-prometheus-stack` 55.0.1 с урезанным набором компонентов, а не на полных values Sema. Полные values проверены только рендером.

## Как проверялось

Стенд: контейнер `rancher/k3s:v1.34.2-k3s1` с флагами `sema-prod` (flannel vxlan, встроенный kube-router, `proxy-mode=iptables`, `masquerade-all=true`), Prometheus Operator и Prometheus из чарта 55.0.1, настоящий Loki 3.5.3 из чарта Sema, AMP из этой ветки. Сам `k3d` на машине не установлен, но он запускает этот же образ. Образ `ghcr.io/ipiton/amp:0.1.0` закрыт для анонимного чтения, поэтому AMP собран локально. Чарт этой ветки на values пилота даёт те же манифесты, что развёрнуты сейчас.

Результаты по шагам: `evidence/`. Повторить матрицу доступа: `lab-check.sh`.
