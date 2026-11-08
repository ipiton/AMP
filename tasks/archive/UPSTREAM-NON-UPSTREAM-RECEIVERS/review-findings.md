# Deep Review: UPSTREAM-NON-UPSTREAM-RECEIVERS

- **Trigger classification:** mandatory, сигналы `C S X R` (requirements.md § Risk Profile, tier Full).
- **Reviewer perspective:** независимый ревьюер. Смотрел корректность адаптера и правок порта, утечки секретов и совместимость. Сверял с исходниками upstream `alertmanager@v0.34.1` и `prometheus/common@v0.70.1` в module cache и со старым ядром (`infrastructure/publishing/coordinator.go`, `publisher.go`, `pagerduty_client.go`).
- **Reviewed at:** 2026-11-08
- **Reviewed tree:** `feature/upstream-non-upstream-receivers @ adc4764` (`git diff main...HEAD -- go-app`). В рабочем дереве на момент ревью была чужая незакоммиченная правка `go-app/internal/upstreamtargets/convert.go`: две строки, тексты ошибок в `httpConfigFragment` с маленькой буквы. Её я не делал и не трогал, на выводы она не влияет.
- **Verdict:** `fix_required`, один major (F1).

Проверки: `go build ./...` и `go vet` по `upstreamtargets`, `upstreamcore`, `infrastructure/k8s`, `cmd/amp-upstream` проходят. Временный тест в `upstreamtargets` (после прогона удалён) подтвердил следующее. Исходный `conf` после `ExtendConfig` не меняется (`conf.String()` до и после совпадает). В `out.String()` и в логах нет URL, `routing_key`, `Authorization` и значений заголовков. PagerDuty URL `https://events.pagerduty.com` уходит в upstream без изменений. Заголовки `Content-Type` и `Authorization: Basic …` приводят к пропуску всего таргета. `send_resolved: "false"`, заданный строкой, даёт `true`. Один и тот же `*WebhookConfig`/`HTTPConfig` попадает в несколько receiver'ов.

## Design Premises

| # | Премиса (research/Spec) | Проверка | Итог |
|---|---|---|---|
| P1 | Хук в начале `reloader.reload` срабатывает на каждом применении: initial, `/-/reload`, SIGHUP, `Core.Reload` | `reloader.go:99-106`: все пути идут через `reload(conf)` | верно |
| P2 | `ExtendConfig` не мутирует `conf` и не делит с ним backing array | `out := *conf`, `slices.Clone(Receivers)`, `append(slices.Clip(...))` (`convert.go:51-60`, `source.go:155-156`). Указатели файловых интеграций общие, но так же, как у upstream между reload'ами (конфиг каждый раз перечитывается из файла). Эксперимент подтвердил | верно |
| P3 | Общие указатели `*XConfig`/`HTTPConfig` между receiver'ами безопасны | `webhook.New`, `slack.New`, `pagerduty.New` конфиг только читают. `Authorization` у Slack пишет `Config.UnmarshalYAML`, а не notifier. Upstream сам делит `global.HTTPConfig` между webhook/pagerduty (`config.go:394,451`) | верно |
| P4 | `/api/v2/status` отдаёт `conf.original`, поэтому K8s-интеграций там нет | **Неверно.** `api/v2/api.go:183`: `original := api.alertmanagerConfig.String()`, то есть маршалинг структуры расширенного конфига. K8s-интеграции там видны. Секреты маскируются (`SecretURL`/`SecretTemplateURL`/`Secret` → `<secret>`, `MarshalSecretValue` в go-app нигде не включается, proxy URL → `Redacted()`) | премиса неверна, требование «без секретов» выполняется (F3) |
| P5 | Индексы файловых интеграций в nflog не сдвигаются | Интеграции добавляются в конец слайса, индекс считается по типу (`receiver.go:66-82`, ключ nflog `{receiver, integration, idx}`, `notify.go:202-206`) | верно для файловых; для K8s-интеграций индексы сдвигаются (F4) |
| P6 | Битый таргет не ломает reload | `Source.build` прогоняет `receiver.BuildReceiverIntegrations` до добавления; ядро строит интеграции тем же кодом и из тех же конфигов. `ExtendConfig` всегда возвращает `nil` | верно |
| P7 | Семантика старого ядра: `targetMatchesReceiver`, `send_resolved` | Принадлежность receiver'у совпадает (`belongsTo` против `coordinator.go:349`). `send_resolved` старое ядро разбирает терпимо (строки через `ParseBool`, числа), адаптер принимает только `bool` | расхождение (F5) |
| P8 | Поля `core.HTTPClientConfig` совпадают с upstream и переносятся через YAML | JSON-теги совпадают, у всех `omitempty`, `follow_redirects` — `*bool`; `UnmarshalStrict` проходит | верно |
| P9 | `Authorization` зарезервирован в `http_headers` и переводится в `http_config.authorization` | `headers.go:29-49` (`ReservedHeaders`, проверка через `CanonicalHeaderKey`). Кроме него зарезервированы `Content-Type`, `User-Agent` и др., а `authorization.type: Basic` upstream запрещает (`http_config.go:416`) | верно, но ломает совместимость с частью Secret'ов (F2) |
| P10 | Run/Stop: опрос не перезагружает остановленное ядро | `stopPoll` (defer) срабатывает до `core.Stop()`. `Core.Reload` после Stop возвращает `errStopped` (`core.go:399-406`), гонки нет | верно (F9, nit) |
| P11 | k8s: retry вместе с пагинацией | Цикл по страницам внутри retry, `secrets = nil` на каждой попытке. 410 Gone (истёкший continue) повторяется по умолчанию (`errors.go:141`). Fake discovery возвращает `RESTClient() == nil`, `Health` уходит в `ServerVersion()` | верно |
| P12 | Без AMP-конфига, в `lite` и при `publishing.enabled: false` K8s не трогается | `targets.go:31-35`, `main.go:78` | верно |

## Findings

### F1. PagerDuty-таргет с базовым URL перестаёт доставлять

- **Severity:** major
- **Location:** `go-app/internal/upstreamtargets/convert.go:108`, `go-app/internal/upstreamtargets/convert.go:159`
- **Issue:** адаптер передаёт `t.URL` в `pagerduty_configs[].url` как есть. Upstream постит ровно по этому адресу (`notify/pagerduty/pagerduty.go:289`, `PostJSON(..., n.conf.URL.String(), ...)`). Старое ядро нормализует URL до базы и само дописывает `/v2/enqueue` (`pagerduty_client.go:103-121`, `normalizePagerDutyBaseURL`). CHANGELOG прямо обещает, что Secret'ы с базовым URL работают без правок. Документированная форма Secret'а — `"url": "https://events.pagerduty.com"`: все четыре примера в `examples/k8s/pagerduty-secret-example.yaml`, база в `docs/ALERTMANAGER_COMPATIBILITY.md:265`. С ней amp-upstream шлёт события в корень `events.pagerduty.com`. Таргет считается `attached`, а уведомления не доходят: upstream ретраит, и каждое уведомление кончается ошибкой. Та же проблема с `/v2/events` — эту форму старое ядро тоже принимает ради совместимости. Путь доставки сломан молча, сигнал `R`.
- **Fix:** в `convert` для `pagerduty` нормализовать URL так же, как `normalizePagerDutyBaseURL`. Пустой путь или `/` → дописать `/v2/enqueue`; `/v2/events` → `/v2/enqueue`; `/v2/enqueue` оставить. Логику скопировать, не импортировать: старое ядро уходит в срезе 5. Добавить юнит-тест на три формы URL и строку в Spec § 2.4.
- **Disposition:** fixed — `pagerdutyURL`: срез `/v2/change/enqueue`, `/v2/enqueue`, `/v2/events`, `TrimRight("/")`, дописывается `/v2/enqueue`; пустой URL → `global.pagerduty_url` (default тот же `https://events.pagerduty.com/v2/enqueue`). Spec § 2.4, CHANGELOG. Тест — в write-tests.

### F2. Заголовки, которые старое ядро принимало, теперь выкидывают таргет целиком

- **Severity:** minor
- **Location:** `go-app/internal/upstreamtargets/convert.go:209-225`, `go-app/internal/upstreamtargets/convert.go:101-109`
- **Issue:** (a) Если в Secret'е есть зарезервированный upstream заголовок, например `Content-Type: application/json` (старое ядро ставит его само, пользователь мог его просто продублировать), весь таргет уходит в `skipped/invalid`. (b) `Authorization: Basic <b64>` пропускается: upstream не разрешает `authorization.type: basic`. (c) `Authorization` без схемы (сырой токен) тоже пропускается. Старое ядро все три случая отправляло как обычные заголовки. (d) У PagerDuty старое ядро берёт routing key из `Authorization` (`Bearer <key>` или сырой ключ, `publisher.go:618-626`) с приоритетом над `routing_key`. Адаптер требует `headers.routing_key`, так что Secret только с `Authorization` пропускается. Spec для (a) так и задумал, но после переключения в срезе 5 таргеты пропадут, и сигналом будет только WARN.
- **Fix:** `Content-Type` без вреда отбрасывать с Debug, а не пропускать таргет. `Basic <b64>` декодировать в `http_config.basic_auth`. Для PagerDuty при пустом `routing_key` брать ключ из `Authorization`, как старое ядро. Если что-то из этого сознательно не переносится, записать в CHANGELOG `[Unreleased]` (Spec § 4) рядом с изменением payload.
- **Disposition:** fixed — (a) зарезервированные заголовки, кроме `Authorization`, отбрасываются, таргет подключается, WARN с именами (не Debug: заголовок не уйдёт, оператор должен видеть); (b) `Basic <b64 user:password>` → `basic_auth`; (c) `Authorization` без схемы — оставлен skip: upstream не умеет слать сырой `Authorization`, кроме PagerDuty, где это routing key; (d) PagerDuty: `routing_key`, иначе `Authorization` без `Bearer ` (порядок как в `extractRoutingKey`: `routing_key` первым), заголовок по HTTP не шлётся. Spec § 2.4, CHANGELOG.

### F3. Неверная премиса: `/api/v2/status` показывает расширенный конфиг, а не исходный файл

- **Severity:** minor
- **Location:** `go-app/internal/upstreamtargets/source.go:12-13`, `go-app/internal/upstreamtargets/source.go:153-154`; Spec D3, § 2.5; research § 2 «Точка входа»
- **Issue:** комментарий в коде («A shallow copy keeps the unexported original text that /api/v2/status serves») и Spec D3 утверждают, что status отдаёт `conf.original`. В upstream v0.34.1 это не так: `getStatusHandler` делает `api.alertmanagerConfig.String()` (`api/v2/api.go:183`), то есть YAML-маршалинг расширенной копии. K8s-интеграции в status видны: имена заголовков, Slack `channel`/`title`, PagerDuty `url`, proxy URL с замаскированным паролем. Значения секретов сейчас закрыты, но только маскированием upstream-типов, а не «файл секретов не видит». Если где-то включат `commoncfg.MarshalSecretValue` или поле не будет secret-типа (например `pagerduty.url` — обычный `*URL`, и токен в query попадёт в status открытым текстом), значения утекут. В требованиях это ограничение подаётся как свойство архитектуры, тестов на него нет.
- **Fix:** исправить комментарий и Spec D3/§ 2.5: status показывает расширенный конфиг, секреты закрывает маскирование upstream. В e2e-тест (Spec § 5) добавить проверку, что `GET /api/v2/status` с K8s-таргетом не содержит URL, `routing_key`, `Authorization` и значений заголовков.
- **Disposition:** fixed — research § 2/§ 4/Option C, Spec D3/§ 2.2/§ 2.5, комментарии `source.go`, CHANGELOG: status показывает расширенный конфиг с маскированием upstream. Проверка `/api/v2/status` без секретов — в e2e (write-tests).

### F4. При изменении набора Secret'ов сдвигаются индексы K8s-интеграций в nflog

- **Severity:** minor
- **Location:** `go-app/internal/upstreamtargets/source.go:87`, `go-app/internal/upstreamtargets/convert.go:51-60`
- **Issue:** ключ nflog — `{receiver, integration type, idx}` (`notify/notify.go:202-206`), индекс считается по типу внутри receiver'а. K8s-таргеты отсортированы по имени и добавляются после файловых. Пусть добавлен Secret `a-hook`, а `b-hook` уже был. Тогда `a-hook` получает индекс `b-hook` вместе с его записью в nflog: DedupStage считает, что текущие firing-группы уже отправлены, и новый таргет не получает их до `repeat_interval` (по умолчанию 4h). Последний в ряду получает пустой индекс и повторно шлёт уже отправленное. `webhook` и `rootly` делят одно пространство `webhook_configs`. Upstream ведёт себя так же при правке файла, но Secret'ы меняются чаще и без участия оператора. В research это не упомянуто, там стабильность доказана только для файловых интеграций.
- **Fix:** как минимум записать ограничение в Spec, в CHANGELOG и в `ALERTMANAGER_COMPATIBILITY.md` (новый таргет получает уже активные группы не раньше `repeat_interval`, возможен один дубль). Если хочется лучше — стабилизировать позицию, например упорядочивать по времени создания Secret'а, тогда новые встают в конец. Вариант на будущее — отдельный receiver на каждый таргет, но это ломает маршрутизацию, так что только записать как техдолг.
- **Disposition:** accepted — ограничение записано в Spec § 2.2 и CHANGELOG (один повтор/пропуск дедупликации для K8s-интеграций того же типа после нового таргета; поведение upstream при вставке в файл). Стабилизация позиции — не в этом срезе.

### F5. `send_resolved` разбирается строже, чем в старом ядре

- **Severity:** minor
- **Location:** `go-app/internal/upstreamtargets/convert.go:171-174`
- **Issue:** `sendResolved` понимает только JSON `bool`. Старое ядро (`coordinator.go:397-416`) понимает и строки через `strconv.ParseBool` (`"false"`, `"0"`, `"f"`), и числа (`0`). Secret с `"send_resolved": "false"` (Helm `filterConfig` из values, где значение легко оказывается строкой) на upstream-ядре начнёт слать resolved. Spec § 2.4 при этом говорит «так считает старое ядро». Проверено экспериментом.
- **Fix:** повторить разбор `targetAcceptsAlertStatus`: `bool`, `string` через `ParseBool`, `float64`/`int`; если значение не разобралось — `true`. Добавить тест на строку и число.
- **Disposition:** fixed — `sendResolved` повторяет `targetAcceptsAlertStatus` (bool, `ParseBool`-строка, `float64`/`int`, иначе true). Spec § 2.4, CHANGELOG. Тест — в write-tests.

### F6. Сбой health-check на старте выключает K8s-таргеты до рестарта

- **Severity:** minor
- **Location:** `go-app/cmd/amp-upstream/targets.go:39-43`, `go-app/internal/infrastructure/k8s/client.go:124-130`
- **Issue:** `NewK8sClient` возвращает ошибку не только вне кластера (`InClusterConfig`), но и при неудачном `Health` (10s). Возьмём pod, стартующий во время рестарта или перегрузки API-сервера. `newTargetSource` пишет WARN и возвращает `nil`: опроса нет, и до рестарта процесса K8s-таргетов не будет совсем. Тихая потеря всех Secret-таргетов на пути доставки. При этом сама ошибка `Refresh` в Spec § 3 описана мягко: прошлый снимок, повтор при следующем опросе.
- **Fix:** выключать адаптер только при ошибке `InClusterConfig` (не в кластере). Ошибку health считать временной: создать clientset без обязательного health (или повторять), запустить `Run`, и пусть первый удачный `Refresh` сделает `Reload`. Это правка в `targets.go`; в k8s-клиент можно добавить конструктор без health-check.
- **Disposition:** accepted — паритет с metrics-only fallback `cmd/server` (тот же `NewK8sClient` с health). Поведение записано в Spec § 3 и CHANGELOG («until restarted»). Ленивое создание клиента требует изменения API k8s-клиента и порядка проводки до `upstreamcore.New` — вне минимального wiring среза.

### F7. Невалидные Secret'ы не видны в метрике amp-upstream

- **Severity:** minor
- **Location:** `go-app/cmd/amp-upstream/targets.go:45`, `go-app/internal/upstreamtargets/source.go:144-148`
- **Issue:** критерий requirements: «Невалидный Secret-таргет пропускается с WARN и метрикой». Secret'ы, отброшенные discovery (ошибка parse/validate), в `amp_upstream_k8s_targets{state="skipped"}` не попадают: gauge считает только то, что пропустил сам адаптер. Метрики discovery выключены (`NewTargetDiscoveryManager(..., nil)`). Битый JSON в Secret'е виден только по WARN в логе.
- **Fix:** добавлять в `skipped` значение `GetStats().InvalidTargets` (или завести отдельный `state="invalid_secret"`) либо передать в discovery метрики, зарегистрированные на `opts.Registerer`.
- **Disposition:** fixed — `Refresh` выставляет `amp_upstream_k8s_targets{state="invalid"}` из `GetStats().InvalidTargets`. Spec § 2.5, CHANGELOG.

### F8. Spec § 2.4: нет Debug для отброшенных ключей `templates`

- **Severity:** nit
- **Location:** `go-app/internal/upstreamtargets/convert.go:184-190`
- **Issue:** Spec говорит, что неизвестные ключи `templates` игнорируются с Debug. `copyTemplates` молча берёт только известные ключи. Опечатку (`titel`) не найти даже на debug-уровне.
- **Fix:** писать в Debug имена неизвестных ключей (только имена, без значений) или убрать это обещание из Spec.
- **Disposition:** fixed (Spec) — обещание Debug убрано: неизвестные ключи игнорируются молча, как у паблишеров старого ядра.

### F9. Шум в логах при shutdown и мелочи `Health`

- **Severity:** nit
- **Location:** `go-app/cmd/amp-upstream/main.go:123-125`, `go-app/internal/upstreamtargets/source.go:120-131`, `go-app/internal/infrastructure/k8s/client.go:237`
- **Issue:** (a) Горутину `Run` никто не ждёт. Если в момент SIGTERM идёт `Refresh`, отмена `pollCtx` даёт WARN «refresh failed». Если гонка случится между `Refresh` и `reload()`, получим ERROR `errStopped`. Это безопасно, но выглядит как сбой. (b) В `Health` отмена родительского контекста (не дедлайн) классифицируется как `TimeoutError`. (c) WARN «names a receiver that is not in the configuration» повторяется на каждом reload, а gauge обновляется и на reload'ах, которые ядро потом отклоняет.
- **Fix:** (a) В `Run` после `Refresh` и после `reload` проверять `ctx.Err()` и выходить молча; при желании ждать горутину через `WaitGroup` до `core.Stop`. (b) Отличать `context.DeadlineExceeded` от `Canceled`. (c) Не обязательно, можно оставить.
- **Disposition:** fixed — (a) `Run` выходит молча, если `ctx` отменён после `Refresh`, и не пишет ERROR reload при отменённом `ctx`; (b) `Health` возвращает ошибку контекста вызывающего как есть, `TimeoutError` — только по своему дедлайну; (c) оставлено.

## Re-review @ b71c653

- **Reviewed at:** 2026-11-08
- **Reviewed tree:** `feature/upstream-non-upstream-receivers @ b71c653` (`git show b71c653`, плюс текущее состояние `convert.go`, `source.go`, `k8s/client.go`, `cmd/amp-upstream/targets.go`). Рабочее дерево чистое.
- **Verdict:** `pass`. Открытых blocker/major нет. Новые находки: одна minor (N1) и три nit (N2–N4).

Проверки: `go build ./...` проходит, `go vet` по `upstreamtargets`, `upstreamcore`, `infrastructure/k8s`, `cmd/amp-upstream` проходит, `go test` по `infrastructure/k8s`, `upstreamcore`, `cmd/amp-upstream` даёт 76 passed. Временный тест в `upstreamtargets` (после прогона удалён) вызывал `convert` напрямую. Результаты:
- PagerDuty URL: `https://events.pagerduty.com`, `…/`, `…/v2/events`, `…/v2/enqueue/`, `…/v2/change/enqueue` и пустой URL дают `https://events.pagerduty.com/v2/enqueue`. URL без схемы → `skipped/invalid` (`unsupported scheme ""`), URL в тексте ошибки нет.
- URL с query-строкой (`https://proxy/pd?token=X`) превращается в `…?token=X/v2/enqueue`. Старое ядро делает то же самое (`pagerduty_client.go:374`, `c.baseURL + endpoint`), так что это паритет, а не регрессия.
- Routing key: `Authorization: Bearer K` даёт `K`, сырой `Authorization: K` (в том числе ключ `authorization` в нижнем регистре) даёт `K`. В обоих случаях заголовок не попадает в `http_config`. `routing_key: ""` вместе с `Authorization` → skip. Старое ядро в этом случае тоже не доставляет: `extractRoutingKey` вернёт `""`.
- Порядок «сначала `routing_key`» совпадает с ключом, который реально уходит в событие (`pagerduty_publisher_enhanced.go:203`). Приоритет `Authorization` в `publisher.go:618-626` влияет только на ключ кеша клиента. Так что в F2(d) первого прохода фраза «с приоритетом над `routing_key`» неточна, а исправление верное.
- `Basic base64(user:pa:ss)` → `basic_auth{username: user, password: pa:ss}`. Схема `basic` в нижнем регистре тоже работает. Не-base64 и строка без `:` → skip, текст ошибки без значения. Вместе с `http_config.authorization` → skip.
- `Content-Type` и `content-length` попадают в `dropped` (канонические имена, отсортированы), `X-Api-Key` уходит в `http_headers`.
- `send_resolved`: `"false"`, `"0"`, `" FALSE "`, `0`, `false` дают false; `"nope"`, `1`, `nil` дают true. Это совпадает с `targetAcceptsAlertStatus`. Число после JSON-раунд-трипа снимка — `float64`, ветка есть.

### Итог по находкам первого прохода

| # | Severity | Disposition | Итог re-review |
|---|---|---|---|
| F1 | major | fixed | **Закрыта.** `pagerdutyURL` повторяет `normalizePagerDutyBaseURL` и дописывает `/v2/enqueue`. Все документированные формы проверены. Пустой URL уходит в `global.pagerduty_url`, у которого тот же default. `change/enqueue` → `enqueue` корректно: адаптер шлёт только alert-события. Spec § 2.4 и CHANGELOG обновлены. |
| F2 | minor | fixed | **Закрыта с оговоркой (N1).** (a) зарезервированные заголовки отбрасываются с WARN. (b) `Basic` переводится в `basic_auth`. (c) сырой `Authorization` у не-PagerDuty таргетов по-прежнему skip; это обосновано и записано в Spec. (d) routing key из `Authorization` работает, но только когда `routing_key` нет (см. N1). |
| F3 | minor | fixed | **Закрыта.** Spec D3/§ 2.2/§ 2.5, комментарии `source.go` и CHANGELOG описывают реальное поведение `/api/v2/status`. Проверка в e2e перенесена в write-tests. |
| F4 | minor | accepted | **Принятие разумно**, ограничение записано в Spec § 2.2 и CHANGELOG. Формулировка неполная (N4). |
| F5 | minor | fixed | **Закрыта.** Разбор совпадает с `targetAcceptsAlertStatus` построчно, проверено экспериментом. |
| F6 | minor | accepted | **Принятие разумно.** Это паритет с fallback `cmd/server`, правка требует менять API k8s-клиента. «until restarted» записано в Spec § 3 и CHANGELOG. Остаточный риск: pod, стартовавший во время недоступности API, молча работает без K8s-таргетов. Видно по WARN и по отсутствию gauge `amp_upstream_k8s_targets`. Стоит поднять при переключении в срезе 5. |
| F7 | minor | fixed | **Закрыта.** `state="invalid"` выставляется из `GetStats().InvalidTargets`. Discovery перезаписывает это значение на каждом проходе (`discovery_impl.go:396`), счётчик не накопительный. |
| F8 | nit | fixed (Spec) | **Закрыта.** |
| F9 | nit | fixed | **Закрыта.** (a) `Run` выходит без WARN/ERROR при отменённом `ctx`. (b) `Health` возвращает `ctx.Err()` вызывающего до классификации таймаута. (c) оставлено, как и новый WARN `headers dropped` на каждом reload: тот же класс шума. |

Регрессий от b71c653 не найдено. Гонок нет: новых разделяемых данных не добавилось, gauge потокобезопасен, `dropped` принадлежит одной `integration`. `redact` дополнительно вырезает пароль из `Basic`, а routing key из `Authorization` закрыт уже существующими вхождениями (значение и часть после пробела).

### Новые находки

#### N1. PagerDuty: `Authorization` рядом с `routing_key` уходит в HTTP или выкидывает таргет

- **Severity:** minor
- **Location:** `go-app/internal/upstreamtargets/convert.go:205-216` (`pagerdutyRoutingKey`)
- **Issue:** если `routing_key` задан, `pagerdutyRoutingKey` возвращается раньше и `Authorization` в `headers` остаётся. Дальше `httpConfigFragment` обрабатывает его как обычный заголовок. Сырой ключ (`Authorization: <key>`, без схемы) даёт skip `invalid` («must be "<type> <credentials>"»), хотя старое ядро такой таргет доставляло по `routing_key`. `Bearer X` уходит в PagerDuty как `http_config.authorization`. Старый PagerDuty-клиент заголовки таргета не слал вовсе. Комментарий функции («Either way the header is not sent over HTTP») для этого случая неверен. Регрессия не от b71c653 (adc4764 вёл себя так же), но исправление F2(d) неполное. Проверено временным тестом.
- **Fix:** в `pagerdutyRoutingKey` удалять `Authorization` из `headers` во всех случаях, не только когда ключ берётся из него. Добавить кейс в write-tests.
- **Disposition:** fixed — `pagerdutyRoutingKey` всегда удаляет `Authorization`; ключ из него берётся только без `routing_key`. Тест — в write-tests.

#### N2. `Basic` без padding или без `:` выкидывает таргет

- **Severity:** nit
- **Location:** `go-app/internal/upstreamtargets/convert.go:327-336` (`decodeBasic`)
- **Issue:** `base64.StdEncoding` не принимает base64 без padding. `Basic <token>`, где после декодирования нет `:`, тоже даёт skip. Старое ядро отправляло такие значения как есть. Обе формы встречаются редко, а сервер может их принимать.
- **Fix:** при ошибке `StdEncoding` пробовать `RawStdEncoding`. Случай без `:` оставить skip, но упомянуть в Spec § 2.4.
- **Disposition:** fixed — fallback на `RawStdEncoding`; `Basic` без `:` — skip, записано в Spec § 2.4.

#### N3. `redact` не знает нормализованный PagerDuty URL

- **Severity:** nit
- **Location:** `go-app/internal/upstreamtargets/convert.go:349-377`
- **Issue:** в `redact` попадает `t.URL`, а в upstream уходит `pagerdutyURL(t.URL)`. Если суффикс срезан или заменён, ошибка разбора цитирует нормализованный URL, и `t.URL` в нём не найти. Проверено: `https://pd%zz/v2/events/` → `parse "https://pd%zz/v2/enqueue": invalid URL escape` попадает в WARN как есть. На практике сюда не дойти: discovery отсеивает неразбираемый URL по `validate:"url"`.
- **Fix:** добавить в `redact` значение `pagerdutyURL(t.URL)` для PagerDuty или вырезать по host. Можно оставить.
- **Disposition:** fixed — `redact` вырезает и `pagerdutyURL(t.URL)`.

#### N4. Формулировка ограничения F4 описывает только повтор

- **Severity:** nit
- **Location:** Spec § 2.2, `CHANGELOG.md` (`Adding a target can restart notification deduplication once…`)
- **Issue:** новый таргет с меньшим именем получает индекс соседа вместе с его записью nflog. Значит, уже активные группы он не получит, пока группа не изменится или не пройдёт `repeat_interval`. Повтор достаётся сдвинутому соседу. В документах упомянут только повтор, задержка для нового таргета не упомянута.
- **Fix:** дописать в Spec § 2.2 и CHANGELOG: «новый таргет получает уже активные группы при их изменении или через `repeat_interval`».
- **Disposition:** fixed — Spec § 2.2 и CHANGELOG дополнены задержкой для нового таргета.
