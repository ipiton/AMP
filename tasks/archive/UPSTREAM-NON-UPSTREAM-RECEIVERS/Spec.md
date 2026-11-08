---
id: UPSTREAM-NON-UPSTREAM-RECEIVERS
slug: upstream-non-upstream-receivers
stream: Architecture / ADR-019
type: feature
artifact: spec
status: approved
created_at: 2026-11-08
updated_at: 2026-11-08
based_on:
  - requirements.md
  - research.md
---

# Spec: K8s-Secret таргеты и Rootly на upstream-ядре (срез 3 ADR-019)

## 1. Решения

| # | Решение | Почему |
|---|---|---|
| D1 | Rootly — без своего `notify.Notifier`: upstream `webhook_configs` на URL alert source «Alertmanager» Rootly, `http_config.authorization` (Bearer), `send_resolved: true`. Документируется в `docs/ALERTMANAGER_COMPATIBILITY.md`. | Rootly принимает webhook Alertmanager (research Q1). |
| D2 | `ROOTLY-INCIDENT-KEY-STABLE` на upstream-ядре не существует (кеша ID инцидентов нет, связь держит Rootly); запись переносится в `BACKLOG.md` → снимается срезом 5 вместе с Rootly-паблишером старого ядра. | Старое ядро не удаляем в этом срезе. |
| D3 | K8s-Secret таргеты — адаптер `internal/upstreamtargets`: снимок таргетов из `business/publishing.TargetDiscoveryManager`, на каждом применении конфига — **копия** `*config.Config` с добавленными интеграциями. Значения Secret'ов не попадают в файл на диске. `/api/v2/status` отдаёт `config.String()` применённого конфига (`api/v2/api.go:183`), т.е. K8s-интеграции там видны, но секретные поля замаскированы `<secret>` типами upstream, как у файловых (поправка по deep-review F3). | Решение владельца 2026-11-08; Option B research. |
| D4 | Точка расширения порта: `upstreamcore.Options.ExtendConfig func(*config.Config) (*config.Config, error)`, вызывается первой строкой `reloader.reload`. Ошибка — reload отклоняется, как ошибка шаблонов. `nil` — поведение upstream. | Одна строка в порте; переживание reload по построению. |
| D5 | Изменения Secret'ов: опрос `Source.Run` с интервалом `publishing.refresh.interval` (default 5m); при смене отпечатка набора таргетов — `core.Reload()`. `/-/reload` и `SIGHUP` берут последний снимок, K8s не опрашивают. | Опрос, как в старом ядре; reload не зависит от доступности K8s API. |
| D6 | Битый таргет пропускается (WARN + gauge), reload файла продолжается. Каждая интеграция проверяется `receiver.BuildReceiverIntegrations` до добавления. `ExtendConfig` адаптера ошибку не возвращает. | Один Secret не должен блокировать конфиг всего инстанса. |
| D7 | Включение: только с `--amp.config.file`, `profile: standard` и `publishing.enabled: true` (как `cmd/server`). Ошибка создания K8s-клиента (не в кластере) — WARN, ядро работает только с файлом (как metrics-only fallback `cmd/server`). | Параллель со старым ядром; локальный запуск с LLM-конфигом не ломается. |
| D8 | `K8S-CLIENT-HEALTH-TIMEOUT` чинится здесь: адаптер зависит от `ListSecrets` (обрезка на 1000) и от `Health` на старте. Флейки refresh-воркера — тесты старого ядра, адаптером не используются → секция «Снимается переходом на upstream (ADR-019)», срез 5. | research Q5. |

## 2. Контракты

### 2.1 `upstreamcore.Options`

```go
// ExtendConfig, when set, returns the configuration the core applies in
// place of conf on every apply (initial and reload). It must not modify conf;
// an error rejects the reload and keeps the active configuration.
ExtendConfig func(conf *config.Config) (*config.Config, error)
```

`doc.go` § AMP extensions — третья точка; `reloader.go` — `extendConfig` в структуре и вызов в начале `reload`.

### 2.2 `internal/upstreamtargets`

```go
func New(d publishing.TargetDiscoveryManager, logger *slog.Logger, reg prometheus.Registerer) (*Source, error)
func (s *Source) Refresh(ctx context.Context) (changed bool, err error)
func (s *Source) ExtendConfig(conf *config.Config) (*config.Config, error) // never errors
func (s *Source) Run(ctx context.Context, interval time.Duration, reload func() error)
```

- `Refresh`: `DiscoverTargets`; ошибка K8s — снимок не меняется (discovery держит прошлый кеш), ошибка возвращается. Снимок — копия enabled-таргетов, отсортированная по `Name`; отпечаток — SHA-256 от JSON снимка, `changed` — отпечаток другой.
- `Run`: тикер; `Refresh`; `changed` → `reload()`; ошибки — WARN/ERROR, кроме случая отменённого `ctx` (shutdown); выход по `ctx`.
- `ExtendConfig`: shallow-копия `Config` (исходный `conf` не меняется), клон `Receivers` и клоны слайсов `*_configs` только у receiver'ов, куда что-то добавлено. Порядок: файловые интеграции, затем K8s по имени таргета. Индекс интеграции в nflog — её позиция в типе: новый K8s-таргет с меньшим именем сдвигает индексы следующих K8s-интеграций того же типа, и их записи nflog переходят к соседу: одна интеграция может повторить уже отправленное уведомление, а новый таргет — не получить активные группы, пока они не изменятся или не пройдёт `repeat_interval`. Так же ведёт себя upstream при вставке интеграции в середину файла; файловые индексы не сдвигаются (deep-review F4, принято).

### 2.3 Принадлежность receiver'у

Как `targetMatchesReceiver` старого ядра: пустой `Receivers` — во все receiver'ы конфига; иначе receiver из `Receivers` или с именем `Name`. Имя из `amp.receiver`, которого нет в конфиге, — WARN; таргет, не попавший ни в один receiver, — `skipped` (`no_receiver`). Receiver без маршрута upstream сам не строит (как у upstream).

### 2.4 Перевод таргета

Фрагмент YAML интеграции → `yaml.UnmarshalStrict` в тип upstream (его дефолты и проверки) → глобальные поля по правилам `config.Config.UnmarshalYAML`.

| `type` / `format` | upstream | Поля |
|---|---|---|
| `webhook` / `alertmanager`, `webhook` | `webhook_configs` | `url` ← `url` |
| `rootly` / `rootly` | `webhook_configs` | `url` ← `url`; хост `api.rootly.com`/`api.rootly.io` — `skipped` (`rootly_rest_api`): нужен URL alert source |
| `slack` / `slack` | `slack_configs` | `api_url` ← `url`; `templates`: `title`, `text`, `color`, `channel`, `pretext`, `username`, `title_link`, `icon_emoji`, `icon_url`; `app_url` ← `global.slack_app_url` |
| `pagerduty` / `pagerduty` | `pagerduty_configs` | `url` ← `url` как база Events API v2: суффиксы `/v2/change/enqueue`, `/v2/enqueue`, `/v2/events` срезаются и дописывается `/v2/enqueue` (как `normalizePagerDutyBaseURL` старого ядра); пустой — `global.pagerduty_url`. `routing_key` ← `headers.routing_key`, иначе `Authorization` без `Bearer ` (как `extractRoutingKey`); обязателен; `Authorization` у PagerDuty по HTTP не шлётся никогда (старый клиент заголовков таргета не слал). `templates`: `description`, `severity`, `client`, `client_url`, `details.<key>` |

Общее для всех:

- `send_resolved` — как `targetAcceptsAlertStatus` старого ядра: bool, строка через `strconv.ParseBool`, число (`0` — `false`); нет значения или не распознано — `true` (Rootly без него не резолвит). Прочие ключи `filter_config` старое ядро тоже не применяет — игнорируются.
- `http_config` ← `http_config` Secret'а (имена полей совпадают с upstream). Заголовок `Authorization: <type> <credentials>` → `http_config.authorization`, `Basic <base64 user:password>` (с padding или без) → `http_config.basic_auth` (upstream не принимает тип `Basic`); `Basic` без `:` после декодирования — `skipped` (`invalid`); без схемы или вместе с `authorization`/`basic_auth` в `http_config` — `skipped` (`invalid`). Прочие зарезервированные upstream заголовки (`commoncfg.ReservedHeaders`: `Content-Type`, `User-Agent`, …) отбрасываются с WARN, таргет подключается. Остальные `headers` (кроме ключа routing key у PagerDuty) → `http_config.http_headers.<name>.secrets`.
- Без `http_config` и заголовков — копия `global.http_config`, как у upstream.
- Ключи `templates`, не перечисленные в таблице, игнорируются молча, как их игнорируют паблишеры старого ядра (deep-review F8).

### 2.5 Наблюдаемость и безопасность

- Gauge `amp_upstream_k8s_targets{state="attached"|"skipped"}` — на каждом `ExtendConfig`; `state="invalid"` — Secret'ы, не прошедшие parse/validate в последнем discovery (`GetStats().InvalidTargets`), на каждом `Refresh`.
- WARN по пропуску: `target`, `type`, `reason` (`invalid`, `rootly_rest_api`, `no_receiver`, `unsupported_type`) и текст ошибки, из которого вырезаны URL и значения заголовков таргета. URL, заголовки, `routing_key` в логи не пишутся.
- Значения живут в `commoncfg.Secret`/`SecretURL`/`SecretTemplateURL` upstream — маскируются при маршалинге конфига, в том числе в `/api/v2/status`.

### 2.6 `cmd/amp-upstream`

- AMP-конфиг загружается один раз (`appconfig.LoadConfig`), передаётся в классификатор и в адаптер.
- Новый файл `targets.go`: `newTargetSource` — D7, namespace: `publishing.discovery.namespace` → `POD_NAMESPACE`/`K8S_NAMESPACE`/`NAMESPACE` → файл service account → `default` (копия `application.resolvePublishingNamespace`: импорт `internal/application` тянет весь `cmd/server`; дубликат удаляется срезом 5). Первый `Refresh` до `upstreamcore.New`; `Run` в горутине до остановки ядра.

### 2.7 `infrastructure/k8s` (D8)

- `Health`: `Discovery().RESTClient().Get().AbsPath("/version").Do(healthCtx)`; без REST-клиента (fake) — прежний `ServerVersion()`. Отмена контекста вызывающего возвращается как есть, `TimeoutError` — только по собственному дедлайну health.
- `ListSecrets`: страницы по 500 через `Continue`, весь цикл внутри retry.
- `Close`: no-op (client-go закрывать нечего), `clientset` не обнуляется — гонки с читателями нет.

## 3. Ошибки и деградация

| Ситуация | Поведение |
|---|---|
| K8s API недоступен на старте (`NewK8sClient`, health) | WARN, адаптер выключен до рестарта, ядро на файле — паритет с metrics-only fallback `cmd/server` (deep-review F6, принято) |
| K8s API недоступен при опросе | WARN, прошлый снимок, reload не зовётся; при остановке (`ctx` отменён) — выход без WARN |
| Невалидный Secret (parse/validate) | discovery пропускает (как сейчас) |
| Таргет не переводится / не строится | `skipped`, WARN, reload продолжается |
| Битый `alertmanager.yml` при reload от опроса | reload отклонён ядром, ERROR в логе, активный конфиг остаётся |

## 4. Совместимость (CHANGELOG `[Unreleased]`, заметка к `amp-upstream`)

- K8s-таргеты на upstream-ядре шлют payload upstream: `format: webhook` → Alertmanager webhook JSON вместо своего; Slack/PagerDuty — шаблоны upstream по умолчанию, `templates` Secret'а переопределяют поля.
- Rootly-таргет с URL REST API (`api.rootly.com/v1/incidents`, в т.ч. из Helm `rootly.*`) пропускается: нужен webhook URL alert source «Alertmanager» и bearer-секрет в `Authorization`.
- Формат Secret'а, label `publishing-target=true`, `amp.receiver` — без изменений. `cmd/server` не меняется.

## 5. Тесты

- `upstreamtargets`: перевод по каждому типу (дефолты upstream, `send_resolved`, `http_config`, заголовки, `routing_key`, шаблоны); Rootly REST → skipped; принадлежность (unscoped, scoped, по имени, неизвестный receiver); исходный `conf` не меняется; секреты не в `conf.String()` и логах; `Refresh` → `changed` только при смене; ошибка K8s держит снимок.
- `upstreamcore`: e2e — K8s-таргет (фейковый K8s) доставляет на httptest webhook; переживает `Reload`; смена Secret + `Refresh` + `Reload` меняет адресата; битый таргет не ломает reload.
- `k8s`: пагинация `Continue`, `Health` с отменённым контекстом, `Close` + конкурентный `ListSecrets` под `-race`.

## 6. Open Questions

- Нет.
