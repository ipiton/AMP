# Deep Review Findings: extraVolumes / extraVolumeMounts в чарте helm/amp

**Trigger classification:** mandatory (`S` — путь доставки секретов receiver'ов)
**Reviewer perspective:** independent general-purpose agent (не автор реализации)
**Reviewed at:** 2026-10-20
**Reviewed tree:** feature/helm-extra-volumes @ f62a517
**Verdict:** fix_required (см. `review-verdict.json`)

## Что проверено

- Дифф `git diff main...HEAD` (chart, README, CONFIGURATION_GUIDE, values-комментарии).
- Инвариант I1 — перерендер против `main` (`git archive main helm/amp`), `--set valkey.password.value=x`: default, `profile=lite`, `configFile.enabled+webConfig.existingSecret`, `values-production.yaml + tests/values-production-placeholders.yaml` (+lite, +configFile), а также `extraVolumes=null`/`extraVolumeMounts=null` (симуляция `--reuse-values` со старого релиза) — **байт-в-байт совпадает** во всех случаях.
- Guard: `/etc/amp/`, `/etc/amp//./config.yaml`, `/etc/amp/config.yaml`, `/data` (lite), `name: config-file` — падают с понятной ошибкой; `name: config-file` при выключенном `configFile` — проходит (как в Spec, Edge Case 3). Extra mounts в `config-reloader` не попадают (I2), volume'ы чарта рендерятся первыми (I3).
- Go-путь загрузки: временный тест через `go test -overlay` (дерево не менялось) гоняет `config.LoadConfig` на примерах из документации с реальными файлами секретов.
- Утечки: `/api/v2/status` (`status_config.go`: `fileSecretRefKeys` оставляет путь, `api_url`/`bot_token` редактируются), `*url.Error` в Telegram/Slack клиентах маскируется (`telegram_client.go:222`, `slack_client.go:166`), `PostgreSQLConfigStorage` в рантайме не подключён.

## Premise check

| Premise (Spec) | Результат ревью |
|---|---|
| `*_file` читаются как есть, без rebase | **Верно для `routing.Parse`** (`file_secrets.go:207`, `os.ReadFile(file)`), относительный путь → fail-closed «failed to read secret file». Но класс `call-path-traced` не заслужен: `loadRouteConfig` (`config.go:792`) **до** `Parse()` вызывает `validateAlertmanagerSubset` (`pkg/configvalidator`), который о `*_file` не знает → F1. |
| upstream так же | Верно: `alertmanager@v0.32.0/config/config.go:157` rebase'ит только templates и `HTTPConfig.SetDirectory`. |
| Вложенный mount в каталог ConfigMap-volume | Правдоподобно (kubelet создаёт nested mountpoints для configMap/secret volume, паттерн уже используется `webConfig` → `/etc/amp/web`). Defer to testing (k3d). |
| Читаемость при `fsGroup: 65534` и `defaultMode: 0440`/`0400` | Правдоподобно (kubelet `SetVolumeOwnership` ставит группу fsGroup и добавляет `g+r` для ro-volume). Defer to testing (k3d). Документационная оговорка — F4. |
| config-reloader секреты не нужны | Верно (args — только `--config-file`, URL'ы). |
| Перечитывание только на load и `/-/reload` | Верно (`file_secrets.go` ROTATION CAVEAT). |

## Findings

### F1 — `api_url_file` (и `url_file`, `routing_key_file`, `global.slack_api_url_file`) не проходят валидацию загрузки: документированный Slack-пример не стартует
- **Severity:** blocker
- **Location:** `go-app/internal/config/config.go:792` (`validateAlertmanagerSubset` до `Parse()`), `go-app/pkg/configvalidator/validators/receiver.go:180-201` (E115), `:216` (E113), `:154` (E122); документация `helm/amp/README.md` § Receiver Credentials From A Secret, `docs/CONFIGURATION_GUIDE.md` § Receiver Credentials From Files, `helm/amp/values.yaml` (пример под `extraVolumeMounts`)
- **Issue:** `pkg/configvalidator` работает на типах `internal/alertmanager/config`, где нет `*_file`-полей, и требует inline-значение. Воспроизведено `config.LoadConfig` на примере из CONFIGURATION_GUIDE и README с реальными файлами:
  - `slack_configs[].api_url_file` → `[E115] receivers[0].slack_configs[0].api_url: Slack API URL is required`;
  - `global.slack_api_url_file` без inline → тот же E115;
  - `webhook_configs[].url_file` → `[E113] webhook URL is required`;
  - `pagerduty_configs[].routing_key_file` → `[E122] PagerDuty routing_key ... is required`;
  - `telegram_configs[].bot_token_file` → **работает** (в configvalidator нет Telegram-проверок), `chat_id: -1001234567890`, `matchers`, `continue` парсятся.

  Т.е. главный сценарий задачи (Slack webhook URL из Secret для sema-prod) не работает ни через `api_url_file`, ни через `global.slack_api_url_file`; AMP с таким ConfigMap упадёт на старте (а `/-/reload` будет отвергнут). Критерий успеха «k3d: AMP стартует с `bot_token_file` и `api_url_file`» недостижим. Баг предсуществующий: FU7-B (`c892f1f`, 2026-08-19) добавил `*_file` после подключения configvalidator к `LoadConfig` (`2339a20`, 2026-08-18) и тестировался только через `routing.Parse`, тестов через `LoadConfig` с `api_url_file`/`url_file`/`routing_key_file` нет. Тот же ложный тезис стоит в `docs/ALERTMANAGER_COMPATIBILITY.md` § `*_file` secret variants. Для чарта это не дефект шаблона, но задача документирует и обещает неработающий путь.
- **Recommendation:** завести баг в `BUGS.md` (например `CONFIGVALIDATOR-FILE-SECRETS`: E113/E115/E122 должны засчитывать `*_file`-двойник, включая `global.slack_api_url_file` как fallback; тест через `LoadConfig`). Дальше одно из двух, решение владельца:
  1. (предпочтительно) починить до merge этой задачи — отдельной bugfix-задачей первой в очереди или, с явным расширением scope в `requirements.md`/`Spec.md` (сейчас Non-Goal «не менять Go-резолв»), здесь: добавить `*_file`-поля в `internal/alertmanager/config` и учесть их в `receiver.go`;
  2. если фикс откладывается — убрать из README/CONFIGURATION_GUIDE/values Slack-`api_url_file` (оставить Telegram), явно написать, что `api_url_file`/`url_file`/`routing_key_file` пока не проходят валидацию, и поправить критерий успеха k3d.

  В обоих случаях k3d-проверка в testing обязана включать Slack `api_url_file`.
- **Disposition:** fix-here
- **Follow-up:** новая запись в `docs/06-planning/BUGS.md` (создать в finalize или раньше, если фикс отдельной задачей)

### F2 — Design Premise «читаются как есть» помечен `call-path-traced`, но трасса не покрывает весь путь загрузки
- **Severity:** minor
- **Location:** `tasks/HELM-EXTRA-VOLUMES/Spec.md` § Design Premises (строка 1), `research.md` § Findings
- **Issue:** трасса остановилась на `routing.Parse`/`resolveFileSecrets`; `loadRouteConfig` перед ним вызывает `validateAlertmanagerSubset`. Не хватает премисы «AMP-загрузчик принимает `*_file` без inline-значения» — именно она ложна (F1).
- **Recommendation:** добавить премису с подтверждением тестом через `LoadConfig` (или k3d), обновить research.
- **Disposition:** fix-here
- **Follow-up:** n/a

### F3 — Guard не защищает файл web-config, хотя файл config.yaml защищает
- **Severity:** minor
- **Location:** `helm/amp/templates/deployment.yaml:322-325`
- **Issue:** для `configFile` в `$chartPaths` попадает и каталог, и сам файл (`mountPath/fileName`), для `webConfig` — только каталог. `extraVolumeMounts` с `mountPath: /etc/amp/web/web-config.yml` (subPath) рендерится без ошибки и молча подменяет файл HTTP-аутентификации (`SERVER_WEB_CONFIG_FILE`). Это не граница доверия (values контролирует оператор), но непоследовательно и именно класс ошибки, который guard обещает ловить; README перечисляет «the config file itself» только для config-file — формально верно, но читатель ожидает симметрии.
- **Recommendation:** добавить `set $chartPaths (clean (printf "%s/%s" .Values.webConfig.mountPath .Values.webConfig.secretKey)) "the web config file (webConfig.mountPath/secretKey)"` и строку в README/values-комментарий; покрыть в T3.
- **Disposition:** fix-here
- **Follow-up:** n/a

### F4 — Утверждение о читаемости при строгом `defaultMode` привязано к дефолтному `podSecurityContext`
- **Severity:** minor
- **Location:** `helm/amp/README.md` (bullet «The container runs as UID 65534 with `fsGroup: 65534`...»), `helm/amp/values.yaml` (комментарий над `extraVolumes`)
- **Issue:** при `defaultMode: 0440` файл принадлежит `root:<fsGroup>`; если оператор переопределит `podSecurityContext` без `fsGroup` (или с другим `runAsUser`/без группы), файл станет нечитаемым и AMP упадёт на загрузке (fail-closed, не утечка). Док подаёт это как безусловное свойство.
- **Recommendation:** сформулировать условно: «with the chart's default `podSecurityContext` (`fsGroup: 65534`)...; without `fsGroup` keep `defaultMode` at 0444 or set it». Само поведение kubelet — подтвердить в k3d (0400 и 0440), как запланировано в 3.3.
- **Disposition:** fix-here
- **Follow-up:** n/a

### F5 — Пример `kubectl create secret --from-literal` кладёт токен в shell history и список процессов
- **Severity:** minor
- **Location:** `helm/amp/README.md`, `docs/CONFIGURATION_GUIDE.md` (блоки `kubectl create secret generic amp-receiver-secrets`)
- **Issue:** для документа, цель которого — убрать секреты из открытого вида, пример с `--from-literal='123456:ABC...'` учит оставлять их в `~/.zsh_history`/`ps`. Текст CONFIGURATION_GUIDE уже упоминает `--from-file`.
- **Recommendation:** показать `--from-file=telegram-bot-token=./bot-token` (файл без лишнего `\n` не обязателен — хвост срезается) или `read -rs` + `--from-literal="$TOKEN"`; заметить про External Secrets/SOPS одной строкой.
- **Disposition:** fix-here
- **Follow-up:** n/a

### F6 — Шаг 1.4 отмечен выполненным, но `evidence/render-invariance.txt` отсутствует
- **Severity:** minor
- **Location:** `tasks/HELM-EXTRA-VOLUMES/tasks.md` (1.4), `tasks/HELM-EXTRA-VOLUMES/evidence/` (пусто)
- **Issue:** verify-ссылка указывает на несуществующий артефакт. Ревьюер перепроверил инвариант независимо — совпадает (см. «Что проверено»), но evidence должен лежать в задаче. Заметка для evidence: production-рендер без `--set valkey.password.value=x` недетерминирован (случайный пароль Valkey) и даёт ложное расхождение.
- **Recommendation:** закоммитить `evidence/render-invariance.txt` (команды + хеши), включая production с placeholders и `extraVolumes=null`.
- **Disposition:** fix-here
- **Follow-up:** n/a

### F7 — `--set ...defaultMode=0440` рендерится строкой `"0440"`
- **Severity:** nit
- **Location:** `helm/amp/values.yaml` / README (пример `defaultMode: 0440`)
- **Issue:** из values-файла YAML 1.1 octal → `288` (корректно). Через `--set` Helm 4 оставляет `"0440"` строкой → ошибка только на apply. Стандартное поведение Helm, примеры даны для values-файла.
- **Recommendation:** при желании — полстроки «in a values file; with `--set` use the decimal value 288».
- **Disposition:** reject (reason: стандартная семантика Helm `--set`, не специфика чарта; ошибка fail-closed на apply)
- **Follow-up:** n/a

### F8 — `null`/скалярные элементы списков дают невнятную ошибку шаблона
- **Severity:** nit
- **Location:** `helm/amp/templates/deployment.yaml:326-336`
- **Issue:** `extraVolumes: [null]` или `[foo]` → `nil pointer evaluating interface {}.name` / `can't evaluate field name`. Рендер всё равно падает (fail-closed), просто без человеческого сообщения. Map вместо списка (`extraVolumes: {a: b}`) тоже падает на guard.
- **Recommendation:** не требуется; при желании `kindIs "map"` с `fail`.
- **Disposition:** reject (reason: ошибка на рендере, а не на apply; цена проверки выше пользы)
- **Follow-up:** n/a

### F9 — Дублирование `$extraVolumes := ... | default list` и guard посреди элемента контейнера
- **Severity:** nit
- **Location:** `helm/amp/templates/deployment.yaml:308`, `:410`
- **Issue:** `$extraVolumes` объявлен дважды (первое объявление уже в области видимости `spec`), guard вставлен между `lifecycle` и `volumeMounts` контейнера `amp` — читается хуже, чем блок проверок в начале `spec:` рядом с остальными `fail`.
- **Recommendation:** объявить переменные и guard один раз рядом с проверками `configReloader` в начале `spec:`; рендер не изменится.
- **Disposition:** fix-here (опционально, на усмотрение автора)
- **Follow-up:** n/a

### F10 — `curl -X POST .../-/reload` в доке не учитывает `webConfig`
- **Severity:** nit
- **Location:** `docs/CONFIGURATION_GUIDE.md` (последний абзац нового раздела), `helm/amp/README.md`
- **Issue:** при включённом `webConfig.existingSecret` `/-/reload` требует credentials (README выше это же говорит про config-reloader).
- **Recommendation:** «an authenticated `POST /-/reload` when `webConfig` is enabled».
- **Disposition:** fix-here
- **Follow-up:** n/a

### F11 — «Every receiver secret has a `*_file` twin» — завышено
- **Severity:** nit
- **Location:** `docs/CONFIGURATION_GUIDE.md` § Receiver Credentials From Files, первый абзац
- **Issue:** у `global.smtp_auth_secret`, OpsGenie/VictorOps `api_key` двойников нет (BACKLOG FU-FILE-SECRETS явно исключает `smtp_auth_secret_file`), а Slack/webhook/PagerDuty-двойники сейчас не проходят валидацию (F1).
- **Recommendation:** «These receiver secrets have a `*_file` twin: ...» + ссылка на ограничения.
- **Disposition:** fix-here
- **Follow-up:** n/a

### F12 — Подсказка «Set either X or X_file, not both» на нечитаемый файл
- **Severity:** nit
- **Location:** `go-app/internal/infrastructure/routing/file_secrets.go` (все `errors.Add(..., "Set either ... not both")`)
- **Issue:** при отсутствующем файле (`failed to read secret file "<path>"`) suggestion говорит про взаимоисключение — вводит в заблуждение при диагностике неверного mountPath. Предсуществующее, вне scope (Go не меняется).
- **Recommendation:** разная подсказка для двух ошибок `resolveFileSecret`.
- **Disposition:** defer-tech-debt
- **Follow-up:** `docs/06-planning/TECH-DEBT.md` (в finalize)

## Defer to testing (k3d)

- Вложенный mount Secret в `/etc/amp/secrets` внутри ConfigMap-volume `/etc/amp` (и рядом с `config-reloader`, который монтирует тот же ConfigMap).
- Читаемость файлов под `runAsUser: 65534` при `defaultMode: 0440` и `0400`.
- `/api/v2/status` живого пода: пути `*_file` видны, `bot_token`/`api_url` — `<secret>`.
- Ротация Secret: подхват после синка kubelet + `POST /-/reload`; config-reloader не реагирует.
- Slack `api_url_file` — только после решения по F1.

## Anti-Pattern Check

- [x] Self-audit was not treated as a substitute for independent review.
