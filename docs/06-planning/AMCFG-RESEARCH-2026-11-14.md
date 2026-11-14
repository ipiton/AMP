# Research: поддержка `AlertmanagerConfig` (monitoring.coreos.com/v1alpha1) в AMP

Дата: 2026-11-14. Режим: только чтение (репозиторий, кластер `sema-prod`, документация). Ничего не менялось.

Обозначения: **[ФАКТ: источник]** — проверено; **[ГИПОТЕЗА]** — не проверено, требует подтверждения.

---

## 1. Проблема

- AMP (`amp-upstream`) читает один файл `--config.file` (upstream `alertmanager.yml`), собранный из `alertmanager.config` values чарта. Prometheus-operator к нему не причастен.
- Основной Alertmanager kube-prometheus-stack получает итоговый конфиг от prometheus-operator: базовый Secret + все выбранные `AlertmanagerConfig` (AMC), смерженные оператором. AMP-копия базового `alertmanager.yaml` этих маршрутов не видит.
- Пример `moving/moving-cron` пришлось переносить руками (receivers `moving-cron-vitaly-dm`/`moving-cron-null`, токен — ключ в Secret `amp-notify`). Каждый новый AMC требует того же; без автоматизации AMP не может заменить основной AM, если команды продолжат описывать маршрутизацию через AMC.

Уточнение к брифу: основной AM живёт в ns **`sema-monitoring`**, не `monitoring` [ФАКТ: `kubectl get alertmanager -A`].

---

## 2. Как оператор превращает AMC в `alertmanager.yaml`

Основной код — `pkg/alertmanager/amcfg.go` (`configBuilder`), селекция — `pkg/alertmanager/operator.go`.
Ссылки:
- код v0.70.0 (версия в кластере): https://github.com/prometheus-operator/prometheus-operator/blob/v0.70.0/pkg/alertmanager/amcfg.go
- код main: https://github.com/prometheus-operator/prometheus-operator/blob/main/pkg/alertmanager/amcfg.go
- user guide: https://prometheus-operator.dev/docs/developer/alerting/
- API reference: https://prometheus-operator.dev/docs/api-reference/api/

### 2.1 Селекция AMC
- `spec.alertmanagerConfigSelector` — label selector по AMC. [ФАКТ: API ref]. Семантика пустого/nil: `{}` выбирает все AMC, `nil` — ни одного [ГИПОТЕЗА: по памяти кода `selectAlertmanagerConfigs`, в доке не сказано; в кластере стоит `{}`, так что для нас неважно].
- `spec.alertmanagerConfigNamespaceSelector` — namespaces для поиска; «If nil, only check own namespace» [ФАКТ: API ref]; `{}` — все namespaces.
- Невалидные AMC оператор отбрасывает (с событием/статусом), остальные мержит [ГИПОТЕЗА: по памяти; поведение `checkAlertmanagerConfigResource`].

### 2.2 Имена
- `makeNamespacedString(in, crKey)` → `<namespace>/<name>/<in>`; пустая строка остаётся пустой. Применяется к receivers, `route.receiver`, именам `mute_time_intervals`, ссылкам `mute_time_intervals`/`active_time_intervals` в маршрутах [ФАКТ: amcfg.go v0.70 и main].
- Пример из кластера: `moving/moving-cron/vitaly-dm`, `moving/moving-cron/null` [ФАКТ: структура `-generated`].

### 2.3 Маршруты: порядок, `continue`, namespace-матчер
- Корневой маршрут AMC превращается в **дочерний** маршрут корня базового конфига. Ключи AMC сортируются (`sort.Strings` / `sortutil.SortedKeys`, ключ `<ns>/<name>`), и все такие маршруты **ставятся перед** маршрутами базового конфига: `cfg.Route.Routes = append(subRoutes, cfg.Route.Routes...)` [ФАКТ: amcfg.go `addAlertmanagerConfigs`/`AddAlertmanagerConfigs`].
- Верхнему маршруту каждого AMC принудительно ставится `continue: true` (в v0.70 — внутри `processRoute` обоих enforcer'ов, в main — обёртка `continueToNextRoute`); `continue` в корне AMC запрещён валидацией (`checkAlertmanagerConfigRootRoute`) [ФАКТ: amcfg.go].
- Namespace-матчер (`alertmanagerConfigMatcherStrategy`):
  - `OnNamespace` (по умолчанию; в кластере поле `null` ⇒ оно) — к верхнему маршруту AMC добавляется `namespace="<ns AMC>"` (v2 `matchers` при AM ≥ 0.22, иначе `match`) [ФАКТ: `namespaceEnforcer.processRoute`].
  - `None` — `noopEnforcer`, ничего не добавляет [ФАКТ].
  - `OnNamespaceExceptForAlertmanagerNamespace` — есть в main (`otherNamespaceEnforcer`): AMC из namespace самого Alertmanager не ограничиваются, остальные — как `OnNamespace` [ФАКТ: amcfg.go main]. В v0.70 этого значения нет (только `None` vs default) [ФАКТ: amcfg.go v0.70].
  - Базовый конфиг enforcer не трогает [ФАКТ].
- Матчеры дочерних маршрутов: если у матчера AMC задан `matchType`, получается v2 `matchers`, иначе — устаревший `match`/`match_re` [ФАКТ: `convertRoute`]. В кластере дочерний маршрут `moving-cron` пришёл как `match: {alertname: …}` — ровно из-за отсутствия `matchType` [ФАКТ: структура `-generated`].

### 2.4 Inhibit rules
- `inhibitRules` AMC добавляются в общий список; при `OnNamespace` в `source_matchers` и `target_matchers` дописывается `namespace="<ns>"`, а из `match`/`match_re` удаляются ключи `namespace` (`processInhibitRule`) [ФАКТ: amcfg.go]. AMC без `route` всё равно вносит свои inhibit rules [ФАКТ].

### 2.5 Time intervals
- v1alpha1 `muteTimeIntervals` → `mute_time_intervals` с префиксом имени; `time_intervals` из CRD не заполняется; `sanitize` выкидывает `mute_time_intervals` для AM < 0.22 и `time_intervals` для < 0.24 [ФАКТ: amcfg.go v0.70/main].

### 2.6 Секреты (`SecretKeySelector`)
- Значения из Secret'ов namespace AMC читаются оператором (`store.GetSecretKey`) и **вписываются в итоговый конфиг открытым текстом** (bot_token, routing_key, api_key, auth_password, bearer token, URL'ы через `getValidURLFromSecret`) [ФАКТ: amcfg.go].
- Исключения в main: часть полей передаётся как `*_file` (Telegram `BotTokenFile`, Pushover `UserKeyFile`/`TokenFile`, global `*File`); TLS-материал — пути под `tlsAssetsDir` (`/etc/alertmanager/certs/...`), содержимое оператор кладёт в отдельный Secret, смонтированный в под [ФАКТ: amcfg.go main, `convertTLSConfig`].
- В кластере `moving/moving-cron/vitaly-dm` → `telegram_configs` с ключом `bot_token` (не `bot_token_file`), т.е. токен вписан inline [ФАКТ: структура `-generated`, без значений].
- Итог пишется в Secret `alertmanager-<name>-generated` ключом `alertmanager.yaml.gz` (gzip); sidecar `prometheus-config-reloader` распаковывает, делает envsubst в `config_out/alertmanager.env.yaml` и дёргает `POST /-/reload` [ФАКТ: ключи Secret'а и args sts в кластере].
- Права оператора: он читает Secret'ы во всех выбранных namespaces (cluster-wide), поэтому AM-поду эти права не нужны [ФАКТ: так устроен оператор; ГИПОТЕЗА про точный ClusterRole kube-prometheus-stack — не проверял].

### 2.7 `alertmanagerConfiguration` (глобальный конфиг из CRD)
- `spec.alertmanagerConfiguration.name` — AMC в namespace Alertmanager, из которого строится **базовый** конфиг вместо `configSecret`; имеет приоритет над `configSecret`; namespace-матчер к нему не применяется; есть `global` (`AlertmanagerGlobalConfig`: SMTP, HTTP, `resolveTimeout`, Slack/OpsGenie/PagerDuty URL) и `templates` [ФАКТ: user guide, API ref, `initializeFromAlertmanagerConfig`/`convertGlobalConfig`].
- В кластере не используется (`null`) [ФАКТ].

### 2.8 Важная деталь: базовый конфиг проходит через модель оператора
Оператор парсит базовый `alertmanager.yaml` в свои структуры (`alertmanagerConfig` в `pkg/alertmanager/types.go`) и маршалит обратно, с version-gated `sanitize` по `spec.version` [ФАКТ: amcfg.go `sanitize`]. Поля, которых нет в модели оператора данной версии, теряются или дают ошибку [ГИПОТЕЗА: точное поведение для неизвестных полей не проверял]. Для AMP это значит: конфиг, прошедший через оператор v0.70, ограничен возможностями AM ~0.26–0.28, а не upstream 0.34.1.

---

## 3. Как это сделано у других

| Кто | Как | Переиспользуемо для AMP? |
|---|---|---|
| **prometheus-operator** | `pkg/alertmanager` (amcfg.go ~3k строк + validation) | Импортировать пакет нельзя без последствий: модуль main требует `prometheus/alertmanager v0.35.0`, `client-go/api/apimachinery v0.37.2`, `controller-runtime v0.25.2`, go 1.26.10 [ФАКТ: go.mod main]. У AMP — точный пин `alertmanager v0.34.1` + `PortedFrom` guard (`internal/upstreamcore/doc.go`), `client-go v0.29.0` [ФАКТ: go.mod AMP]. MVS поднимет AM до 0.35 и сломает гейт; `pkg/alertmanager` тянет `pkg/assets`, `internal/sortutil`, операторный store — Go API не стабилен. Отдельно импортируемы только типы: модуль `github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring` (и `pkg/client`) — отдельные модули в том же репо [ФАКТ: replace в go.mod]. Лицензия Apache-2.0; AMP — AGPL-3.0 [ФАКТ: LICENSE] — копирование кода (порт amcfg.go) допустимо с сохранением NOTICE/копирайта (Apache-2.0 → AGPLv3 совместимы в одну сторону). |
| **Grafana Alloy** `mimir.alerts.kubernetes` | Experimental компонент: discover AMC (label selectors на ns и AMC), мерж с `global_config` Secret, `matcher strategy` OnNamespace/None, push в Mimir Alertmanager API; ClusterRole get/list/watch на namespaces и alertmanagerconfigs; без кластеризации [ФАКТ: https://grafana.com/docs/alloy/next/reference/components/mimir/mimir.alerts.kubernetes/] | Модель «внешний синкер пишет готовый конфиг в AM» = вариант (б). Код Alloy — Apache-2.0, но это компонент Alloy, не библиотека [ГИПОТЕЗА про переиспользуемость]. |
| **Mimir/Cortex** | Своей поддержки CRD нет; конфиг грузится через API (`mimirtool alertmanager load`) или Alloy выше [ГИПОТЕЗА: по памяти] | — |
| **VictoriaMetrics vmoperator** | Конвертер prometheus-operator CRD → свои CRD, включая `AlertmanagerConfig` → `VMAlertmanagerConfig` (по умолчанию включён, отключается `disable_prometheus_converter`; ownerReferences по `VM_ENABLEDPROMETHEUSCONVERTEROWNERREFERENCES=true`, иначе конвертированные объекты не удаляются). `VMAlertmanagerConfig` имеет принудительный namespace-матчер (отключается `spec.disableNamespaceMatcher` на VMAlertmanager), оператор сам мержит части в один конфиг [ФАКТ: https://docs.victoriametrics.com/operator/integrations/prometheus/, https://docs.victoriametrics.com/operator/resources/vmalertmanagerconfig/] | Подтверждает: все, кто поддерживает AMC, делают это **оператором/контроллером с cluster-wide чтением Secret'ов** и рендером в файл; внутри самого Alertmanager-процесса никто. |
| **Grafana (unified alerting)** | Импорт AM-конфига через API/provisioning; AMC не читает [ГИПОТЕЗА] | — |

Вывод по п.2 брифа: готовой независимой Go-библиотеки «AMC → alertmanager config» нет. Есть либо сам оператор (как процесс), либо порт его amcfg.go.

---

## 4. Состояние кластера (`--context sema-prod`, только get/list)

- AMC всего **один**: `moving/moving-cron` (возраст ~7d), без labels [ФАКТ].
- Используемые поля `moving-cron`: `receivers` (2: `null` без интеграций; `vitaly-dm` с `telegramConfigs`: `botToken` как SecretKeySelector на Secret `hermes-telegram` ключ `token`, `chatID`, `message`, `parseMode: HTML`, `sendResolved: false`), `route` (`groupBy`, `groupWait`, `groupInterval`, `repeatInterval`, `matchers` с `matchType: =~`, `receiver`, один дочерний `routes[]` без `matchType`). Не используются: `inhibitRules`, `muteTimeIntervals`, TLS, HTTP config [ФАКТ].
- CR `Alertmanager` `sema-monitoring/kube-prometheus-stack-alertmanager`: `alertmanagerConfigSelector: {}`, `alertmanagerConfigNamespaceSelector: {}` (все AMC во всех ns), `alertmanagerConfigMatcherStrategy: null` (⇒ OnNamespace), `alertmanagerConfiguration: null`, `configSecret: alertmanager-kube-prometheus-stack-alertmanager`, `secrets: [alertmanager-heartbeat]`, image `quay.io/prometheus/alertmanager:v0.28.1`, replicas 1 [ФАКТ].
- Оператор: `quay.io/prometheus-operator/prometheus-operator:v0.70.0` (ноябрь 2023), deployment `sema-monitoring/kube-prometheus-stack-operator`, флаг `--secret-field-selector=type!=…dockercfg,…service-account-token,…helm.sh/release.v1` [ФАКТ].
- Структура `alertmanager-kube-prometheus-stack-alertmanager-generated` (`alertmanager.yaml.gz`; значения не печатались, выведены только имена/ключи):
  - top: `global` (`resolve_timeout`, `slack_api_url`), `route`, `receivers`, `inhibit_rules` (3), `templates`; `time_intervals`/`mute_time_intervals` — нет;
  - receivers: `drop-unmatched`, `panic-telegram`, `errors-slack`, `heartbeat`, `moving/moving-cron/null`, `moving/moving-cron/vitaly-dm`;
  - route: корень `panic-telegram`; **первый** дочерний — `moving/moving-cron/vitaly-dm`, `continue: true`, matchers `alertname=~"MovingCron.*|KubeJobFailed"`, `namespace="moving"` (внук → `moving/moving-cron/null` через `match`); дальше маршруты базового конфига (`heartbeat`, `errors-slack`, `drop-unmatched`, …) [ФАКТ].
- StatefulSet основного AM: args `--config.file=/etc/alertmanager/config_out/alertmanager.env.yaml --storage.path=/alertmanager --data.retention=120h --cluster.listen-address= --web.listen-address=:9093 --web.external-url=… --web.route-prefix=/ --cluster.label=… --cluster.peer=… --cluster.reconnect-timeout=5m --web.config.file=/etc/alertmanager/web_config/web-config.yaml`; sidecar `config-reloader` (`--reload-url=http://127.0.0.1:9093/-/reload --config-file=/etc/alertmanager/config/alertmanager.yaml.gz --config-envsubst-file=… --watched-dir=…`) [ФАКТ].
- AMP в ns `amp`: `ghcr.io/ipiton/amp:0.1.0`, args `--config.file=/etc/amp/config/alertmanager.yml --amp.config.file=/etc/amp/config/amp.yaml --storage.path=/data --web.listen-address=:9093 --web.external-url=… --cluster.listen-address=`; без reloader-sidecar; `/etc/amp/secrets` — Secret `amp-notify` [ФАКТ].

---

## 5. Как устроен AMP (относящееся к задаче)

- Флаги `amp-upstream`: `go-app/cmd/amp-upstream/main.go:47-55` (`config.file`, `storage.path`, `data.retention`, `web.listen-address`, `web.external-url`, `web.config.file`, `amp.config.file`) и cluster-флаги `main.go:215-233` (полный набор upstream, включая `cluster.label`, `cluster.reconnect-timeout`). **Нет `--web.route-prefix`, `--log.level`, `--log.format`** [ФАКТ]. Stdlib `flag` ⇒ неизвестный флаг = выход с ошибкой.
- Загрузка конфига: `go-app/internal/upstreamcore/core.go:273` (`config.LoadFile(opts.ConfigFile)`); `Core.Reload()` — `core.go:523` (перечитать файл, при ошибке активный конфиг остаётся).
- Триггеры reload: SIGHUP — `main.go:176`; `POST /-/reload` — upstream handler в `core.Handler()` (ADR-019, `reloader.go`); периодический poll K8s-таргетов — `main.go:160` (`go targets.Run(..., core.Reload)`).
- **Готовый шов для внешних receivers**: `upstreamcore.Options.ExtendConfig` — `core.go:120-124`, вызывается на каждом apply в `reloader.go:110`; описание в `internal/upstreamcore/doc.go:81` («must keep all of them in place» при bump upstream). Им пользуется `internal/upstreamtargets/source.go:144` (`Source.ExtendConfig`, копия конфига + доп. интеграции, ошибки не ломают reload) и `source.go:113` (`Run`: опрос + `reload` при изменении). Подключение — `cmd/amp-upstream/targets.go:29` (`newTargetSource`, только `profile=standard` + `publishing.enabled`).
- K8s-клиент: `go-app/internal/infrastructure/k8s/client.go:41,138` — только `ListSecrets(ns, labelSelector)` с пагинацией, client-go v0.29.0. Investigation tool `go-app/internal/infrastructure/investigation/tools/kubernetes.go:35,73` — свой clientset (in-cluster/kubeconfig), pods/logs/events/deployments, чартом не обслуживается (`BACKLOG.md:43`, `INVESTIGATION-K8S-TOOL-HELM`). Informer'ов/watch нет.
- RBAC: `helm/amp/templates/rbac.yaml` — одна `Role` `list secrets` в одном namespace discovery, рендер только при discovery; ADR-012 (`docs/06-planning/DECISIONS.md:161`): «ничего cluster-scoped ни при каких values», глагол добавляется вместе с кодом, `release-gate.sh` (`helm-rbac`) проверяет форму.
- Чарт: смена конфига = рестарт пода через `checksum/config|amp-config|secret` (`helm/amp/templates/statefulset.yaml:44-46`); hot reload в чарте не используется. Секреты интеграций — `extraVolumes`/`extraVolumeMounts` (`helm/amp/values.yaml:705-723`) и `*_file` поля.
- ADR-019 (`DECISIONS.md:346`): один конфиг — upstream `alertmanager.yml`, точный пин upstream, AMP-расширения — через швы `upstreamcore`.
- Лицензия AMP — AGPL-3.0.

---

## 6. Варианты

| | Суть | Оценка | C | S | M | X | R | ADR-019 | Минусы/риски |
|---|---|---|---|---|---|---|---|---|---|
| **(а)** встроенный контроллер | AMP watch'ит AMC + Secret'ы cluster-wide, порт amcfg.go, результат через `ExtendConfig` | 8–12d | да | **да** | нет | да | да | совместим (шов есть) | ClusterRole `get/list/watch secrets` во всех ns — прямой конфликт с ADR-012 (нужен новый ADR); bump client-go 0.29→0.3x; порт ~3k строк + validation с вечной синхронизацией с оператором; дублирует работу оператора, если он стоит |
| **(б1)** читать `-generated` основного AM | Role `get` с `resourceNames` на один Secret в `sema-monitoring`; AMP распаковывает gz, вытаскивает receivers с префиксом `<ns>/<name>/` и их маршруты, мержит через `ExtendConfig` | 2–3d | да | **да** | нет | да | да | совместим | Жёсткая связка с основным AM: умирает ровно в момент замены; SA AMP получает все inline-секреты основного AM; эвристика «AMC-маршрут = receiver с префиксом» хрупкая |
| **(б2)** «теневой» CR `Alertmanager` + config-reloader | В ns `amp` объявить CR `Alertmanager` `amp` с `replicas: 0`, `configSecret` = AMP-базовый конфиг, те же селекторы; оператор пишет `alertmanager-amp-generated`; AMP-под монтирует его + sidecar `prometheus-config-reloader` (gunzip, envsubst, `POST /-/reload`) | 2–4d (чарт + ops, Go-кода 0) | да (values) | **да** | нет | да | да | совместим: AMP читает обычный upstream-файл | Работает только при наличии prometheus-operator; `replicas: 0` + генерация конфига [ГИПОТЕЗА, проверить]; базовый конфиг проходит через модель оператора v0.70 (§2.8); inline-секреты всех AMC в Secret ns `amp`; оператор создаёт пустой sts/Service `alertmanager-operated` в ns `amp`; пути TLS/`secrets` (`/etc/alertmanager/...`) надо монтировать так же |
| **(в)** AMP как образ CR `Alertmanager` | `spec.image: ghcr.io/ipiton/amp`, `spec.containers` патчит args/env/volumes | 3–5d + постоянно | да | да | нет | да | да | частично | Сейчас **не стартует**: оператор передаёт `--web.route-prefix=/` (флага нет), при `logLevel`/`logFormat` — `--log.*`; `--amp.config.file`, PG-env, `amp.yaml` — только через strategic-merge `containers` (args заменяются целиком); два способа установки (чарт и CR); `--web.config.file` оператор генерирует в формате exporter-toolkit (TLS/headers), AMP понимает `basic_auth_users` [ГИПОТЕЗА о полном совпадении]; конфиг ограничен моделью оператора |
| **(г)** ничего в коде | Документ + скрипт: из `kubectl get amc -o yaml` (или `amtool`-совместимого рендера) сгенерировать фрагмент values, секреты — ключами в `amp-notify` | 0.5–1d | нет | нет | нет | нет | нет | совместим | Ручной шаг на каждый AMC; расхождения незаметны; не снимает блокер замены, но при 1 AMC в кластере проблема пока объёмом мала |

### Разбор

**(а)** Единственный вариант, работающий без prometheus-operator (для внешних пользователей AMP). Цена — S: AMP-процесс с токеном, который читает любые Secret'ы кластера, при этом в том же процессе работает LLM-investigation с tool'ами — это заметно расширяет blast radius prompt injection по сравнению с отдельным оператором. Смягчение: читать только Secret'ы, на которые ссылаются AMC (`get` по имени, без `list`), но RBAC `get` без `resourceNames` всё равно cluster-wide. Импорт `pkg/alertmanager` оператора отпадает (AM v0.35 ломает точный пин и `PortedFrom`; Go API не стабилен), реально только порт кода + импорт модуля типов `pkg/apis/monitoring` (тоже тянет apimachinery 0.37 ⇒ bump client-go).

**(б1)** Самый быстрый «код в AMP» для параллельного пилота, но временный по определению: источник истины — основной AM, который мы хотим убрать. Плюс S: Role на чтение Secret'а, где inline лежат и Slack/Telegram-токены основного AM. Не рекомендую.

**(б2)** Переиспользует оператор целиком: он уже имеет cluster-wide права и семантику один-в-один (префиксы, порядок, `continue`, namespace-матчер, inline секретов). Для AMP это ноль Go-кода и ноль новых прав SA (монтирование Secret своего ns RBAC не требует). Работает и в пилоте (оба AM видят одни и те же AMC, как сейчас с дублированием), и после замены (основной CR удаляется, теневой остаётся — оператор нужен всё равно для Prometheus). Главные неизвестные: генерирует ли v0.70 конфиг при `replicas: 0`, и что модель оператора делает с полями upstream 0.34, которых нет в 0.70 (у нас в базовом конфиге — `*_file` поля; `bot_token_file` в telegram появился в AM 0.26 [ГИПОТЕЗА: точная версия]). Отдельно: где живёт CR — в чарте AMP (условно, `.Capabilities.APIVersions.Has "monitoring.coreos.com/v1"`) или в helmfile Sema.

**(в)** Формально «правильный» операторный путь, но AMP — не drop-in по CLI (нет `--web.route-prefix`), а AMP-специфичные части (PG, `amp.yaml`, LLM-секреты) придётся проталкивать через `spec.containers` — фактически второй способ деплоя, конкурирующий с чартом. Имеет смысл только если AMP позиционируется как «drop-in image для kube-prometheus-stack»; тогда минимум — принять `--web.route-prefix` и `--log.*`. Отдельный продуктовый вопрос.

**(г)** Честный stopgap на сейчас: AMC один, новые появляются редко [ГИПОТЕЗА по темпу].

---

## 7. Рекомендация и срезы

**Рекомендация:** (г) сразу как stopgap, затем **(б2)** как решение блокера замены в этом кластере. (а) — в BACKLOG с триггером «нужен пользователь AMP без prometheus-operator»; (б1) и (в) — отклонить (в — вернуть, если появится цель «drop-in image для kube-prometheus-stack»).

Срезы:

0. **AMCFG-SPIKE** (0.5d, ops, без кода; Risk: `R`, но выполняется на тестовом кластере/k3d — тогда `none`). Проверить на k3d с оператором v0.70: CR `Alertmanager` с `replicas: 0` → создаётся ли `alertmanager-<name>-generated`; что оператор делает с базовым конфигом AMP (все ли поля переживают round-trip: `bot_token_file`, `slack_api_url_file`, `matchers`, `inhibit_rules` с `equal`); какие пути (`/etc/alertmanager/secrets/...`, `/etc/alertmanager/certs/...`) появляются в итоге. Выход: go/no-go для (б2).
1. **AMCFG-DOC-MANUAL** (0.5–1d; Risk: `none` → Lightweight). Runbook ручного переноса AMC в values (префиксы, `continue: true` первым маршрутом, namespace-матчер, секрет → ключ `amp-notify` + `*_file`) + скрипт сверки «AMC в кластере vs маршруты в AMP-конфиге» (только имена, без значений).
2. **AMCFG-OPERATOR-SOURCE** (2–3d; Risk: `C S R X` → **Full**). Чарт: opt-in values (например `alertmanager.configSource: operator`): CR `Alertmanager` (`replicas: 0`, селекторы, `alertmanagerConfigMatcherStrategy`, `configSecret` из `alertmanager.config`), монтирование `-generated` и TLS/секретов по путям оператора, sidecar `prometheus-config-reloader` (точная версия, проверка возраста образа), `--config.file` на `config_out`, отключение checksum-рестарта для этого режима; `_validate.tpl` (CRD есть, режимы взаимоисключающие); `release-gate.sh` (рендер обоих режимов, RBAC SA AMP не меняется — ADR-012 соблюдён); README + ADR-020 (источник конфига — оператор, inline-секреты AMC в Secret ns `amp`). S — из-за inline-секретов чужих ns в ns `amp` и нового образа в поде.
3. **AMCFG-PILOT-CUTOVER** (0.5–1d; Risk: `R X` → Standard + ops). В пилоте включить режим 2, убрать ручную копию `moving-cron` из values, сравнить итоговые конфиги AMP и основного AM по структуре (имена/порядок маршрутов), проверить доставку `MovingCron*`.
4. (Отложено) **AMCFG-NATIVE-CONTROLLER** — вариант (а), ~8–12d, Full (`C S R X`), только по триггеру; сам потребует нарезки (типы+bump client-go; порт конвертера с golden-тестами против операторного вывода; informer'ы+RBAC+ADR пересмотра ADR-012).

---

## 8. Открытые вопросы владельцу

1. Целевой сценарий AMP — только кластеры с prometheus-operator, или нужен и без него? От этого зависит, нужен ли когда-нибудь (а).
2. Допустимо ли, что inline-секреты всех AMC (токены из ns `moving` и др.) окажутся в Secret ns `amp` (б2)? Основной AM сейчас держит их так же в `sema-monitoring`.
3. Где жить CR `Alertmanager` для AMP: в чарте AMP (опционально) или в helmfile Sema рядом с kube-prometheus-stack?
4. Апгрейд оператора v0.70 (2023) — планируется? Он влияет на round-trip базового конфига (§2.8) и даёт `OnNamespaceExceptForAlertmanagerNamespace`.
5. В пилоте оба AM будут подхватывать одни и те же AMC и дублировать уведомления (как сейчас для базового конфига) — устраивает до `AMP-PILOT-WEEK-REVIEW` (2026-11-16)?
6. Особые правила AMP-копии (например, `Watchdog` → `drop-unmatched`, а не heartbeat) остаются в базовом конфиге теневого CR — подтверждаете, что AMC их не касаются?
7. Нужна ли цель «AMP как drop-in image для CR `Alertmanager`» (вариант (в): поддержка `--web.route-prefix`, `--log.*`)? Это отдельная продуктовая задача.

### Решения владельца (2026-11-14)

- **Вопрос 1 (AMP без prometheus-operator):** нужен, но без своего контроллера. В кластере без оператора нет и `AlertmanagerConfig`, маршруты пишутся в `alertmanager.config` — это AMP уже умеет. `AMCFG-NATIVE-CONTROLLER` остаётся отложенным; триггер — AMC в кластере есть, а оператору нельзя доверить генерацию конфига.
- **Вопрос 2 (inline-секреты AMC в ns `amp`):** допустимо, как у основного Alertmanager в `sema-monitoring`. Следствие: чтение Secret'ов в ns `amp` держать таким же узким, как в `sema-monitoring`.
- **Вопрос 3 (где CR `Alertmanager`):** в чарте AMP (opt-in), вместе с монтированием `-generated` и sidecar перезагрузки — CR связан с подом AMP по имени Secret'а и путям. Какие AMC подхватывать (селекторы), задаётся values AMP со стороны Sema.
- **Вопрос 4 (апгрейд оператора v0.70):** ради AMP не обновляем; решается по итогам `AMCFG-SPIKE`. Если базовый конфиг AMP переживает round-trip через модель v0.70 без потерь (§ 2.8) — апгрейд не нужен; если нет — отдельная задача в Sema (kube-prometheus-stack), не в AMP.
- **Вопрос 5 (дублирование уведомлений в пилоте):** устраивает.
- **Вопрос 6 (AMC и особые правила копии AMP):** закрыт проверкой кластера. Единственный AMC `moving-cron` матчит `alertname=~"MovingCron.*|KubeJobFailed"`; оператор добавляет `namespace="moving"` (`OnNamespace`) и `continue: true` верхнему маршруту, так что AMC только добавляет уведомления для алертов своего namespace и не перехватывает маршруты базового конфига (`Watchdog` → `drop-unmatched`).
- **Вопрос 7 (AMP как образ CR `Alertmanager`, вариант (в)):** не нужно. AMP ставится чартом; вернуться при запросе от пользователей.
