# Deep Review Findings: extraVolumes / extraVolumeMounts в чарте helm/amp

## Round 2 (fix round) — 2026-10-20

**Trigger classification:** mandatory (`S` — путь доставки секретов receiver'ов; после Scope Change ещё `X`: чарт + Go-валидатор)
**Reviewer perspective:** independent general-purpose agent (не автор реализации и не ревьюер раунда 1)
**Reviewed at:** 2026-10-20
**Reviewed tree:** feature/helm-extra-volumes @ 1674cdb (`git diff f62a517..HEAD` + полный `git diff main...HEAD`)
**Verdict:** pass (см. `review-verdict.json`): открытых blocker нет, major нет; minor/nit ниже с disposition.

### Что проверено (раунд 2)

- **Валидатор, эмпирически.** Временный `_test.go` в scratchpad, подложенный через `go test -overlay` в `internal/config` (дерево не менялось), гоняет `config.LoadConfig` на конфигах с реальными temp-файлами (`ENVIRONMENT` не задан, т.е. production-ветка `https_production`):
  - загружаются: `slack_configs[].api_url_file`; `global.slack_api_url_file` + slack без `api_url`; `webhook_configs[].url_file`; `pagerduty_configs[].routing_key_file`; `telegram_configs[].bot_token_file`; `global.smtp_auth_password_file` с email-receiver;
  - «ни значения, ни файла» → прежние `[E115]` (slack), `[E113]` (webhook), `[E122]` (pagerduty);
  - URL из файла с `http://` (slack, webhook, `global.slack_api_url_file`) → отказ `routing.Parse` на теге `https_production`; мусор вместо URL → тег `url`. Значение файла в тексте ошибки **не появляется** (`fieldErr.Error()` не печатает значение; проверено поиском маркера в сообщении);
  - inline + file (receiver и global) → «not both» от `routing.Parse`; отсутствующий файл → «failed to read secret file "<path>"», в том числе когда `global.slack_api_url` валиден (fallback не маскирует сломанный `api_url_file` — как в routing);
  - `api_url_file` + невалидный inline `global.slack_api_url` → `global.go` по-прежнему ругается (E2xx), т.е. пропуск в `validateSlackConfigs` не выключает проверку global;
  - `pagerduty_configs[].service_key_file` **без** `routing_key` → отказ загрузки (см. R2-F1); тот же отказ и для inline `service_key` без `routing_key` — предсуществующее.
  - Strict-парсер пакета (`configvalidator.New(...).ValidateBytes`, `KnownFields(true)`) на YAML со всеми семью `*_file` ключами: `valid=true`, 0 ошибок, 0 warnings.
- **Паритет со списком FU7-B** (`routing/file_secrets.go`): `global.slack_api_url_file`, `global.smtp_auth_password_file`, `webhook_configs[].url_file`, `pagerduty_configs[].routing_key_file`/`service_key_file`, `slack_configs[].api_url_file`, `telegram_configs[].bot_token_file` — все семь полей добавлены в `internal/alertmanager/config`, лишних нет.
- **Порядок Slack.** Валидатор: inline → file → global inline → global file. Routing: `resolveFileSecrets` (inline XOR file, на receiver и на global) → `resolveGlobalFallbacks` (`if cfg.APIURL == ""`). При взаимоисключении inline/file оба порядка дают одно и то же эффективное значение; конфиги, где они могли бы разойтись (inline+file), отвергает routing. Расхождений нет.
- **HTTPS для URL из файла.** Пропуск E116/E117/E114 в валидаторе не оставляет дыры: файл резолвится в inline-поле до `p.validator.Struct`, а теги `url,https_production` стоят на `SlackConfig.APIURL`, `WebhookConfig.URL` (`required,url,https_production`), `GlobalConfig.SlackAPIURL`. Единственное послабление — `ENVIRONMENT=development` отключает `https_production` для URL из файла, тогда как inline Slack-URL валидатор режет E117 всегда (R2-F3, nit).
- **Прочие валидаторы.** `security.go`: W300 на inline `smtp_auth_password`/`service_key` — для `*_file` предупреждения нет (правильно); W111/S111 по `c.URL` для `url_file` не срабатывают (это warnings, приемлемо). `global.go`/`structural.go` о `*_file` знать не обязаны.
- **Другие потребители `internal/alertmanager/config`:** только `pkg/configvalidator/*` и `internal/config/alertmanager_validation.go` (non-strict unmarshal). Новые поля аддитивны, json-теги согласованы — `json_parser` (`DisallowUnknownFields`) их тоже принимает. gofmt-выравнивание — механический churn в двух struct'ах, `gofmt -l` чист, `go vet` чист.
- **Регресс:** `go test ./pkg/configvalidator/... ./internal/alertmanager/... ./internal/config/...` — 412 passed.
- **Чарт:** I1 перепроверен на 1674cdb против `git archive main helm/amp` (default, lite, configFile+webConfig, configFile+configReloader, `extraVolumes=null`/`extraVolumeMounts=null`) — хеши совпадают. Guard на `/etc/amp/web/web-config.yml` и `/etc/amp/web/` падает с понятной ошибкой, `/etc/amp/web/sub` проходит; Secret-mount попадает только в контейнер `amp`.

### Статус находок раунда 1

| ID | Severity | Disposition | Статус | Проверка |
|---|---|---|---|---|
| F1 | blocker | fix-here | **resolved** | `1674cdb`; `LoadConfig` с каждым `*_file` проходит, без значения — E113/E115/E122 (см. выше). Остаток по PagerDuty `service_key*` — предсуществующий routing-баг, R2-F1 |
| F2 | minor | fix-here | resolved | премиса про `validateAlertmanagerSubset` добавлена в Spec § Design Premises (call-path-traced + measured, k3d §1). `research.md` не дополнен — некритично, Spec — источник истины |
| F3 | minor | fix-here | resolved | `$chartPaths` включает `webConfig.mountPath/secretKey`; README/values упоминают web config file; рендер с `/etc/amp/web/web-config.yml` падает |
| F4 | minor | fix-here | resolved | README и values.yaml: условная формулировка про default `podSecurityContext` и `fsGroup`; k3d §2 подтвердил 0400→0440 `root:nobody`, чтение под 65534 |
| F5 | minor | fix-here | resolved | оба примера на `--from-file`, CONFIGURATION_GUIDE объясняет про shell history |
| F6 | minor | fix-here | resolved | `evidence/render-invariance.txt` есть; снят на f62a517+wt, шаблон потом менялся (R2-F7) — ревьюер перепроверил на 1674cdb, совпадает |
| F7 | nit | reject | closed (reject) | без изменений |
| F8 | nit | reject | closed (reject) | без изменений |
| F9 | nit | fix-here | resolved | переменные и guard один раз в начале `spec:` рядом с проверками `configReloader`; второе объявление `$extraVolumes` удалено; рендер не изменился |
| F10 | nit | fix-here | resolved | README и CONFIGURATION_GUIDE: «`POST /-/reload` (authenticated when `webConfig` is enabled)» |
| F11 | nit | fix-here | resolved | «These receiver secrets have a `*_file` twin» |
| F12 | nit | defer-tech-debt | open → finalize | подтверждено снова: на отсутствующий файл suggestion «Set either api_url or api_url_file, not both». Запись в `TECH-DEBT.md` в finalize |

### Findings (раунд 2)

#### R2-F1 — PagerDuty только с `service_key` / `service_key_file` проходит валидатор, но не загружается
- **Severity:** minor
- **Location:** `go-app/internal/infrastructure/routing/receiver.go:324` (`RoutingKey ... validate:"required"`); затронутые обещания — `go-app/pkg/configvalidator/ERROR_CODES.md` (E122), `docs/CONFIGURATION_GUIDE.md` § Receiver Credentials From Files (список двойников), `docs/ALERTMANAGER_COMPATIBILITY.md` (`service_key` — «live routing-key fallback»)
- **Issue:** `resolveFileSecrets` резолвит `service_key_file` в `ServiceKey`, но struct-валидация `routing.Parse` требует непустой `RoutingKey`, так что конфиг без `routing_key` падает: `Field validation for 'RoutingKey' failed on the 'required' tag` — и для inline `service_key`, и для `service_key_file`. Фолбэк в `config_targets.go` недостижим. Предсуществующее (FU7-B / fu2-d), не регресс этой задачи: до фикса такой конфиг отвергал ещё и валидатор (E122), теперь — только routing. Новая формулировка E122 («or their `routing_key_file` / `service_key_file` variants») обещает больше, чем работает. Попутно: W116 (deprecated `service_key`) не выдаётся для `service_key_file`.
- **Recommendation:** баг в `BUGS.md` (routing: `RoutingKey` required → «routing_key или service_key», тест через `LoadConfig`). Здесь при желании — пол-строки в ERROR_CODES/CONFIGURATION_GUIDE, что `service_key*` без `routing_key*` пока не загружается. Чинить routing в этой задаче не нужно (Non-Goal; основной сценарий — `routing_key_file`).
- **Disposition:** defer-bug
- **Follow-up:** новая запись в `docs/06-planning/BUGS.md` (в finalize)

#### R2-F2 — План и требования не обновлены под Scope Change
- **Severity:** minor
- **Location:** `tasks/HELM-EXTRA-VOLUMES/tasks.md` (Touched Files, Phase 1–3), `tasks/HELM-EXTRA-VOLUMES/requirements.md` § Risk Profile → Notes
- **Issue:** в `tasks.md` нет шагов на Validator Fix (Go-файлы, ERROR_CODES, ALERTMANAGER_COMPATIBILITY), а 3.2 перечисляет только helm-тесты — тесты валидатора и `LoadConfig`, обещанные Spec § Validator Fix и новым критерием успеха, в плане отсутствуют, write-tests легко их пропустит. 3.3 (k3d) не требует пересборки образа: `evidence/k3d-extra-volumes.md` снят на образе из f62a517 (Go = main), §1 фиксирует падение Slack — нужно повторить на образе с 1674cdb+. В requirements Notes осталось «Go-код не меняется».
- **Recommendation:** добавить в `tasks.md` фазу Validator Fix (отмеченную как сделанную, со ссылкой на 1674cdb) и в Phase 3 — шаг тестов Go. Минимум покрытия:
  1. `validators/receiver_test.go`: по одному кейсу на `api_url_file`, `global.slack_api_url_file`, `url_file`, `routing_key_file`, `service_key_file` → нет E113/E115/E122; «ни значения, ни файла» → E113/E115/E122; `api_url_file` + невалидный inline URL не даёт E116/E117 только когда inline пуст (inline+file — валидатор проверяет inline).
  2. Strict-парсер (`ValidateBytes`) принимает все семь ключей (и JSON-вариант).
  3. `internal/config` через `LoadConfig` с реальными temp-файлами: Slack/webhook/PagerDuty/global-Slack загружаются; `http://` в файле → отказ (`https_production`), содержимое файла не попадает в текст ошибки; отсутствующий файл → ошибка с путём.
  4. k3d 3.3: Slack `api_url_file` из Secret на образе с фиксом.
  Notes в requirements поправить.
- **Disposition:** fix-here
- **Follow-up:** n/a

#### R2-F3 — «A URL from a file is not checked for format or HTTPS» читается как отсутствие проверки вообще
- **Severity:** nit
- **Location:** `go-app/pkg/configvalidator/ERROR_CODES.md` (E113, E115), комментарии `validators/receiver.go` (`validateSlackConfigs`, `validateWebhookConfigs`)
- **Issue:** формально верно для валидатора, но оператор поймёт, что `http://` в файле пройдёт. На деле `routing.Parse` режет его тегами `url,https_production`, кроме `ENVIRONMENT=development` — и в этом режиме возникает асимметрия: inline `http://` Slack-URL валидатор отвергает E117 всегда, а тот же URL из файла проходит.
- **Recommendation:** дописать «...by the validator; the loader still rejects a non-HTTPS URL (unless `ENVIRONMENT=development`)». Асимметрию в dev оставить.
- **Disposition:** fix-here
- **Follow-up:** n/a

#### R2-F4 — W300 в `security.go` всё ещё говорит, что у модели нет `*_file`
- **Severity:** nit
- **Location:** `go-app/pkg/configvalidator/validators/security.go` (doc-комментарий `checkHardcodedSecret`, suggestion W300)
- **Issue:** комментарий «this config model has no *_file alternative to point to instead» устарел после 1674cdb; для `smtp_auth_password` и `service_key` подсказка W300 могла бы назвать `smtp_auth_password_file` / `routing_key_file` — это и есть цель задачи.
- **Recommendation:** поправить комментарий; по желанию — suggestion с упоминанием `*_file` для полей, у которых двойник есть.
- **Disposition:** fix-here (опционально, на усмотрение автора)
- **Follow-up:** n/a

#### R2-F5 — `git diff --check main...HEAD` не зелёный
- **Severity:** nit
- **Location:** `tasks/HELM-EXTRA-VOLUMES/Spec.md:16`, `tasks/HELM-EXTRA-VOLUMES/tasks.md:17`
- **Issue:** trailing whitespace (markdown hard break из шаблона). `CLAUDE.md` § Quality Gates требует `git diff --check` перед finalize.
- **Recommendation:** заменить двойной пробел на `<br>`/пустую строку или убрать.
- **Disposition:** fix-here
- **Follow-up:** n/a

#### R2-F6 — Ошибки E114/E116/E123 печатают inline URL, а Slack/webhook URL — это секрет
- **Severity:** minor
- **Location:** `go-app/pkg/configvalidator/validators/receiver.go` (`invalid Slack API URL: %s`, `invalid webhook URL: %s`, `invalid PagerDuty URL: %s`); ошибка уходит в лог `failed to load configuration` (видно в k3d §1)
- **Issue:** при опечатке в inline `api_url` полный webhook URL с токеном попадает в лог пода. Предсуществующее и только для inline-значений; путь `*_file` его как раз обходит (валидатор файл не читает, routing значение не печатает). Вне scope задачи.
- **Recommendation:** в сообщении — маскировка query/пути, как в `internal/infrastructure/routing/utils.go`, или только поле без значения; тест на отсутствие токена в ошибке.
- **Disposition:** defer-bug
- **Follow-up:** новая запись в `docs/06-planning/BUGS.md` (в finalize)

#### R2-F7 — `evidence/render-invariance.txt` снят до переноса guard'а
- **Severity:** nit
- **Location:** `tasks/HELM-EXTRA-VOLUMES/evidence/render-invariance.txt`
- **Issue:** заголовок — «branch @ f62a517 + working tree»; 1b868dc перенёс guard и переменные. Ревьюер перепроверил на 1674cdb — хеши с main совпадают, но evidence должен описывать финальное дерево.
- **Recommendation:** перегенерировать в testing на финальном коммите (и добавить production + placeholders, как в файле).
- **Disposition:** fix-here (в testing)
- **Follow-up:** n/a

### Defer to testing (k3d), обновлено

- Slack `api_url_file` из Secret на образе, собранном с 1674cdb или позже (k3d §1 повторить, ожидаем `Running` и `<secret>` в `/api/v2/status`).
- Ротация Secret + `POST /-/reload` — как в раунде 1.

### Anti-Pattern Check (раунд 2)

- [x] Self-audit was not treated as a substitute for independent review.

---

## Round 1 — 2026-10-20


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
