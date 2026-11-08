---
id: UPSTREAM-NON-UPSTREAM-RECEIVERS
slug: upstream-non-upstream-receivers
stream: Architecture / ADR-019
type: feature
artifact: research-pack
status: final
created_at: 2026-11-08
updated_at: 2026-11-08
---

# Research Pack - интеграции на upstream-ядре: Rootly и K8s-Secret таргеты

## 0) TL;DR

- Solving: как таргеты из K8s Secret'ов и Rootly доставляются на upstream-ядре (`amp-upstream`).
- Found: Rootly принимает webhook Alertmanager (alert source «Alertmanager», Bearer, `send_resolved: true`) — свой Notifier не нужен. Upstream строит интеграции только из `config.Receiver`; у порта есть место для хука — начало `reloader.reload(conf)`.
- Chose: адаптер `internal/upstreamtargets`: опрос Secret'ов старым `TargetDiscoveryManager`, снимок таргетов, на каждом reload — копия `*config.Config` с добавленными `webhook_configs`/`slack_configs`/`pagerduty_configs` в нужные receiver'ы; смена снимка → `core.Reload()`.
- Risks: битый Secret не должен блокировать reload файла (проверка интеграции до добавления); breaking-изменение payload для `format: webhook` и для Rootly-таргетов на REST API.
- Next step: -> spec

## 1) Questions

1. Принимает ли Rootly webhook Alertmanager? Если да — нужен ли свой Notifier и что с `ROOTLY-INCIDENT-KEY-STABLE`?
2. Где в upstream-ядре добавить интеграции, чтобы они переживали reload и не светились в `/api/v2/status`?
3. Как перевести `core.PublishingTarget` в конфиг upstream с его дефолтами и валидацией, не дублируя `config.Load`?
4. Как подхватывать изменения Secret'ов без рестарта?
5. Что из `K8S-CLIENT-HEALTH-TIMEOUT`, `REFRESH-WORKER-WARMUP-STOP-FLAKY`, `FLAKE-REFRESH-IN-PROGRESS` касается адаптера?

## 2) Findings

- **Rootly (Q1):** документация Rootly (Alerts → Sources → Alertmanager) даёт webhook URL (`https://webhooks.rootly.com/webhooks/incoming/alertmanager_webhooks`) и bearer-секрет; в `alertmanager.yml` — `webhook_configs` с `http_config.authorization` (Bearer) и `send_resolved: true` (без него Rootly не резолвит). Пейджинг — суффиксом `notify/<resource_type>/<resource_id>` в URL или аннотациями. Значит, upstream `notify/webhook` достаточно; связь алерт → инцидент держит Rootly на своей стороне, кеша ID инцидентов у AMP на этом пути нет — `ROOTLY-INCIDENT-KEY-STABLE` на upstream-ядре не воспроизводится, а в старом ядре снимается срезом 5 вместе с `rootly_publisher_enhanced.go`/`rootly_incident_store.go`.
- **Старый Rootly-таргет:** Helm `rootly-secrets.yaml` и пример в `docs/CONFIGURATION_GUIDE.md` указывают REST API `https://api.rootly.com/v1/incidents` — туда payload Alertmanager не примут. Такой таргет адаптер должен отвергнуть с понятным WARN (нужен URL alert source), а не слать 4xx на каждое уведомление.
- **Точка входа (Q2):** `config.Coordinator.Reload` читает файл и зовёт подписчика `reloader.reload(conf)` (`internal/upstreamcore/reloader.go`). Всё, что строит интеграции (`receiver.BuildReceiverIntegrations`), идёт после. `api.Update(conf)` в `/api/v2/status` отдаёт `conf.original` — исходный текст файла, поэтому добавленные в копию конфига интеграции туда не попадают. Порядок интеграций в receiver'е задаёт их индекс в nflog: K8s-интеграции добавляются после файловых, отсортированные по имени таргета — индексы файловых не сдвигаются.
- **Дефолты upstream (Q3):** дефолты типа и валидацию делает `UnmarshalYAML` каждого `*Config` (`webhook.WebhookConfig`, `config.SlackConfig`, `pagerduty.PagerdutyConfig`), а глобальные значения — `Config.UnmarshalYAML` (`config.go:382-470`): для webhook/pagerduty — `HTTPConfig = cmp.Or(local, global)`, для slack — `AppURL` из global, копия global `HTTPConfig`, `pagerduty.URL` из global. Путь «собрать YAML-фрагмент интеграции → `yaml.UnmarshalStrict` в тип upstream → докрутить три глобальных поля» даёт все дефолты и проверки upstream без копии `config.Load`. Поля `core.HTTPClientConfig` названы как upstream (`proxy_url`, `tls_config`, `basic_auth`, `authorization`, `follow_redirects`) — переносятся тем же YAML.
- **Заголовки:** старое ядро шлёт `Headers` как HTTP-заголовки, кроме служебных ключей (`routing_key` у PagerDuty). В upstream — `http_config.http_headers` (prometheus/common v0.70.1), но `Authorization` там зарезервирован (`config/headers.go` `ReservedHeaders`) → переводится в `http_config.authorization` (`<type> <credentials>`).
- **Принадлежность receiver'у:** `targetMatchesReceiver` (`infrastructure/publishing/coordinator.go:349`): пустой `Receivers` — все receiver'ы; иначе совпадение по `Receivers` или `Name == receiver`. `filter_config` реально читает только `send_resolved` (`coordinator.go:392`); остальные ключи (`severity`, `namespaces`) и в старом ядре ничего не фильтруют.
- **Типы:** Secret'ы принимают только `rootly`, `pagerduty`, `slack`, `webhook` (`discovery_validate.go:197`); `format` для `webhook` — `alertmanager` или `webhook`.
- **Обновления (Q4):** `NewTargetDiscoveryManager` + `DiscoverTargets` уже делают list + parse + validate и при ошибке K8s API оставляют прошлый кеш (graceful degradation). Хватает опроса с интервалом `publishing.refresh.interval` и `core.Reload()` при смене отпечатка набора таргетов; `/-/reload` и `SIGHUP` пересобирают конфиг из последнего снимка.
- **BUGS (Q5):** адаптер зовёт `k8s.K8sClient.ListSecrets` — обрезка на 1000 элементов из `K8S-CLIENT-HEALTH-TIMEOUT` его касается напрямую; health с неработающим таймаутом вызывается из `NewK8sClient`, т.е. на старте `amp-upstream`; гонка в `Close` — при shutdown. Запись закрывается в срезе (~0.25d). `REFRESH-WORKER-WARMUP-STOP-FLAKY` и `FLAKE-REFRESH-IN-PROGRESS` — тесты `RefreshManager`/`BackgroundWorker` старого ядра; адаптер их не использует (опрос — свой тикер), они уходят вместе с `refresh_*.go` в срезе 5 — переносятся в секцию «Снимается переходом на upstream (ADR-019)».
- **Constraints:** порт `upstreamcore` меняется минимально, отличие от upstream помечается в `doc.go`; параллельные срезы 2b/4 трогают `core.go`/`cmd/amp-upstream/main.go` — свою проводку держать в отдельном файле `cmd/amp-upstream`.
- **Unknowns:** нет.

## 3) Options

`Generation:` single-pass

### Option A - Свой `notify.Notifier` на каждый тип Secret-таргета поверх старых паблишеров

- **Pros:** поведение доставки как в старом ядре (свои форматтеры, Rootly REST).
- **Cons:** тянет удаляемое ядро (`infrastructure/publishing`) в upstream-бинарь, противоречит ADR-019 («все интеграции — upstream `notify/*`»); две реализации доставки.
- **Cost/Risk:** high

### Option B - Адаптер `PublishingTarget` → `config.Receiver` в копии конфига на каждом reload

- **Pros:** доставка, ретраи, nflog, шаблоны — upstream; своего кода только перевод полей; переживает reload по построению; решение владельца 2026-11-08.
- **Cons:** payload меняется на upstream (breaking для `format: webhook` и Rootly REST); нужна одна точка расширения в порте.
- **Cost/Risk:** low

### Option C - Генерировать файл `alertmanager.yml` с K8s-интеграциями и подсовывать его ядру

- **Pros:** порт не меняется.
- **Cons:** секреты из Secret'ов попадают в файл на диске и в `/api/v2/status` (`conf.original`); гонки записи файла с config-reloader.
- **Cost/Risk:** medium (security)

## 4) Decision

- **Chosen:** Option B; Rootly — без своего Notifier, через upstream webhook на Alertmanager alert source.
- **Why:**
  - Требование среза: интеграции — upstream `notify/*` как есть, K8s-Secret — адаптер в `config.Receiver` (владелец 2026-11-08).
  - Секреты остаются в памяти в типах `Secret`/`SecretURL` upstream и не попадают в `/api/v2/status`.
  - Переживание reload — следствие места хука, а не отдельный механизм.
  - Своего кода мало: перевод полей + опрос, discovery переиспользуется.

## 5) Spec Inputs

- API/contracts: `upstreamcore.Options.ExtendConfig`; правила перевода по типам; поведение `rootly` с REST-URL; CHANGELOG-заметка о payload.
- Data model/migrations: not applicable.
- Rollout/rollback: включается только с `--amp.config.file`, профилем `standard` и `publishing.enabled`; откат — убрать флаг.
- Observability: gauge таргетов по исходу (attached/skipped), WARN по пропущенным с причиной, без секретов.
- Security/ownership/RBAC: RBAC на `list secrets` в namespace уже нужен `cmd/server`; значения — только в secret-типах upstream.

## 6) References

- Files: `go-app/internal/upstreamcore/reloader.go`, `go-app/internal/business/publishing/discovery_impl.go`, `go-app/internal/business/publishing/discovery_parse.go`, `go-app/internal/business/publishing/discovery_validate.go`, `go-app/internal/infrastructure/publishing/coordinator.go`, `go-app/internal/infrastructure/k8s/client.go`, `helm/amp/templates/rootly-secrets.yaml`.
- Upstream: `github.com/prometheus/alertmanager@v0.34.1/config/config.go` (`Load`, `UnmarshalYAML` 382-470), `notify/webhook/config.go`, `notify/pagerduty/config.go`, `config/notifiers.go` (`SlackConfig`); `github.com/prometheus/common@v0.70.1/config/headers.go`.
- Links: [Rootly — Alertmanager integration](https://docs.rootly.com/integrations/alertmanager), [Rootly — Alertmanager (ko)](https://docs.rootly.com/ko/integrations/alertmanager).
