# Deep Review Findings: экранирование HTML и Subject в email

**Trigger classification:** mandatory (сигнал `S`; сигналы `S R` — Full tier)
**Reviewer perspective:** independent agent (не автор кода); линзы security (HTML/URL-инъекция в письмо, header injection), correctness (дефолтный и пользовательские шаблоны под `html/template`, RFC 2047), premises (трасса ingest → Subject), maintainability
**Reviewed at:** 2026-10-23
**Reviewed tree:** раунд 1 — bugfix/email-render-escaping @ cbe4330 (diff `3d5e675..cbe4330`); раунд 2 — @ 71279d1 (diff `fc2c1c2..71279d1`)
**Verdict:** раунд 1 — pass; раунд 2 — pass (see `review-verdict.json`)

## Проверено (без находок)

Поведение подтверждено не только чтением: временным тестом в пакете (удалён, дерево чистое) отрендерены дефолтные шаблоны и пользовательские варианты через `renderEmailContent`/`renderHTMLTemplate`, собраны сырые MIME-сообщения через `buildMIMEMessage`.

- **Fallback HTML:** аннотация `<script>alert(1)</script>` ⇒ `&lt;script&gt;alert(1)&lt;/script&gt;`; `runbook_url: javascript:alert(1)` ⇒ `href="#ZgotmplZ"`; text-часть и subject остаются `text/template`, `<script>` в text — как есть. `{{ .SilenceURL }}` с уже %-кодированным query в `href` не перекодируется дважды.
- **Пользовательский `html_template` (header таргета):** `{{ define }}`/`{{ template }}` экранируются в контексте вызова; значение в `<script>` — JS-строка (`\r\n` экранированы), в `style` и в позиции имени атрибута — `ZgotmplZ`; `default`/`upper`/`lower` возвращают `string` и экранируются как обычные значения, обхода нет. `html/template.FuncMap` — алиас `text/template.FuncMap`, Premise 1 верна.
- **`<style>` дефолтного шаблона** (`defaults/email.go:69`): `{{ if eq .Status "resolved" }}#28a745{{ else }}#dc3545{{ end }}` — обе ветки константы, контекст CSS одинаковый, рендер даёт `background: #dc3545;` без `ZgotmplZ`. Premise 2 в части «шаблон валиден для `html/template`» верна (но см. F1 про рантайм).
- **Overlay `RenderEmailContent`** (`template_formatter.go:497-534`): поле `html` идёт через `renderField(..., html=true)` ⇒ `Template.ExecuteHTMLString` (`business/templating/template.go:405`) — клон `html/template`-инстанса. Premise 4 верна. Subject overlay — plain text, теперь санитизируется в `buildMIMEMessage` вместе с fallback.
- **Extra headers** (fallback `X-Mailer` + overlay `headers.*`): ключ и значение проходят `sanitizeHeaderValue` (`email_client.go:306`), проверено на `"X-A\r\nBcc": "v\r\nCc: y"` ⇒ одна строка `X-ABcc: vCc: y`.
- **From/To:** из конфигурации таргета (`target.Headers["to"/"from"]`, `SMTPConfig.From`), не из данных алерта; к тому же `net/smtp` `Mail`/`Rcpt` отвергают CR/LF (`validateLine`) до `DATA`, так что строки `From:`/`To:` с CR/LF не собираются.
- **Subject:** ASCII `"A\r\nBcc: x@evil"` ⇒ `Subject: ABcc: x@evil`, отдельного `Bcc:` нет; одиночные `\r`/`\n` тоже убраны. Не-ASCII `"Тест\r\nBcc: x@evil"` ⇒ один encoded word без CR/LF. Замечание: до фикса не-ASCII путь был уязвим так же — `encodeQP` пишет байты `< 128` как есть, CR/LF попадали внутрь encoded word сырыми; санитизация до `mime2047Subject` закрывает оба пути.
- **CR/LF через `/api/v2/alerts` доходят до Subject** — подтверждено трассой (но не той, что в Spec, см. F2): `POST /api/v2/alerts` ⇒ `handleAlertsPost` (`internal/application/handlers/alerts.go:404`) ⇒ `parseAlertsForProcessing` (`:482`): Prometheus-парсер, при его ошибке/невалидности — `parseLegacyAlerts` ⇒ `convertIngestInputToAlert` (`:523`), где `alertname` только `strings.TrimSpace` (внутренние `\r\n` остаются), лейблы копируются `CloneStringMap`. В Prometheus-пути `webhookValidator.validatePrometheusAlert` (`webhook/validator.go:368`) проверяет только имя лейбла и пустое значение; `EnhancedValidator.ValidateLabelValue` (`webhook/validation.go:158`) явно разрешает `\n`/`\r` и в этом пути не вызывается. Далее `buildEmailTemplateData` кладёт `alert.AlertName` в `GroupLabels.alertname`, который выводит `DefaultEmailSubject`.
- **Другие потребители:** `renderEmailContent`, `renderTemplate`, `renderHTMLTemplate`, `emailTemplateFuncs` вызываются только из `email_publisher_enhanced.go`; `renderTemplate` в `cmd/server/legacy_dashboard.go` — другая функция другого пакета. `defaults.DefaultEmailHTML` рендерится только этим publisher-ом (`GetDefaultTemplates` в `defaults.go` — валидация размера/структуры, не рендер).
- **Сборка и тесты:** `go build ./...`, `go vet ./internal/infrastructure/publishing/`, `go test -count=1 -run 'Email|MIME|RFC2047|Template' ./internal/infrastructure/publishing/` — 66 passed.
- **git log** затронутых файлов (`7848c16`, `5ac900f`, `67d8ca3`, `aa635ff`, `506fd6f`): `mime2047Subject` и порядок «fallback, затем overlay» существовали до задачи; фикс их не меняет.

## Findings

### F1 — Дефолтные email-шаблоны падают на алерте без `severity`/`instance`/`description`; ошибка fallback обрывает Publish до overlay
- **Severity:** major
- **Location:** `go-app/internal/notification/template/defaults/email.go:237-239,285-287`; `go-app/internal/infrastructure/publishing/email_publisher_enhanced.go:75-82,320-343`
- **Issue:** `renderTemplate` и `renderHTMLTemplate` парсят шаблон без `Option("missingkey=...")`. Для `map[string]string` отсутствующий ключ даёт invalid value, и пайп в функцию со `string`-параметром падает: `executing "html" at <lower>: invalid value; expected string` (нет `severity`), `at <"N/A">` (нет `instance`), то же для `description`. Воспроизведено и на `text/template` (до фикса), и на `html/template` (после) — **не регрессия этого диффа**, но тот же путь, который он меняет. Ошибка fallback возвращается из `Publish` (`:75-82`) до блока overlay (`:93-109`), поэтому письма нет вовсе — даже если у таргета есть рабочие upstream-шаблоны. Алерт без `instance` — обычное дело (агрегатные правила). Плюс к этому: Success Criterion «дефолтный HTML-шаблон рендерится без ошибок» и тест 2.1 по текущей фикстуре (`email_publisher_test.go:55-56`, все лейблы заданы) подтвердят свойство, которое верно только при полном наборе лейблов.
- **Recommendation:** отдельный баг: `Option("missingkey=zero")` в обоих рендерах (как в `business/templating` — `ExecuteHTMLString`/`ExecuteTextString`), тест на алерт только с `alertname`. Отдельно решить, должна ли ошибка fallback блокировать успешный overlay (сейчас fallback — обязательная предпосылка). Фикс — две строки, его можно взять и сюда, если автор сочтёт уместным; при отложении — в тесте 2.1 явно задать полный набор лейблов и не формулировать его как «дефолтные шаблоны рендерятся» без оговорки.
- **Disposition:** fix-here (раунд 1 — defer-bug; взят в задачу автором, resolved в 71279d1, см. Раунд 2)
- **Follow-up:** n/a

### F2 — Premise 3 и research ссылаются не на ingest, а на валидацию фильтров запроса
- **Severity:** minor
- **Location:** `tasks/EMAIL-RENDER-ESCAPING/Spec.md` (Design Premises, строка 3), `tasks/EMAIL-RENDER-ESCAPING/research.md` (Sources, Findings)
- **Issue:** «`core/interfaces.go:258` проверяет только длину» — это `AlertFilters.Validate` (фильтры `GET`-запросов истории), к приёму алертов не относится. Вывод премисы верен, доказательство — нет; класс `call-path-traced` не заработан этой ссылкой. Реальная трасса — в разделе «Проверено» выше (`handlers/alerts.go:404,482,523`, `webhook/validator.go:368`, `webhook/validation.go:158`). Также Premise 2 помечена `verified (тест)`, хотя теста ещё нет (тесты — после `pass`); рендер подтвердил ревьюер, тест появится в 2.1.
- **Recommendation:** заменить ссылку в Spec и research на трассу `handleAlertsPost` ⇒ `parseLegacyAlerts`/Prometheus-парсер ⇒ `convertIngestInputToAlert`; для Premise 2 указать «рендер проверен в deep-review, тест — 2.1».
- **Disposition:** fix-here
- **Follow-up:** n/a

### F3 — `mime2047Subject`: UTF-8 рвётся между encoded words, управляющие символы не кодируются, строка не фолдится
- **Severity:** minor
- **Location:** `go-app/internal/infrastructure/publishing/email_client.go:365-429` (`mime2047Subject`, `encodeRFC2047Words`, `encodeQP`)
- **Issue:** проблемы до задачи, не регрессии; с CR/LF после санитизации не связаны (инъекция закрыта). (a) Нарезка по 63 символам избегает разрыва `=XX`, но не многобайтной руны: длинная кириллическая тема даёт слова `…=D0?=` / `=?UTF-8?Q?=B6…`, каждое — невалидный UTF-8 (RFC 2047 §5(3)); строгие MUA показывают `�`. (b) `encodeQP` пропускает байты `< 32` (`\x00`, `\v`) сырыми — в Q-кодировке они обязаны кодироваться (§4.2); в ASCII-пути они тоже идут в заголовок как есть. (c) Слова склеиваются пробелом без CRLF-фолдинга; при длинном `alertname` (legacy-путь ingest не ограничивает длину) строка `Subject:` может превысить 998 октетов (RFC 5322 §2.1.1) — отказ части MTA.
- **Recommendation:** заменить самописный кодер на `mime.QEncoding.Encode("UTF-8", s)` (режет по границам рун, кодирует управляющие символы) и фолдить слова через `"\r\n "`; управляющие символы в ASCII-теме — убирать или кодировать. Тест: длинная кириллическая тема декодируется `mime.WordDecoder` в валидный UTF-8.
- **Disposition:** defer-tech-debt
- **Follow-up:** запись в `docs/06-planning/TECH-DEBT.md` на `finalize`

### F4 — CHANGELOG описывает не все видимые изменения пользовательского `html_template`
- **Severity:** nit
- **Location:** `CHANGELOG.md` (запись EMAIL-RENDER-ESCAPING)
- **Issue:** кроме «сырой HTML показывается текстом», под `html/template` значение в `style`/имени атрибута заменяется на `ZgotmplZ`, а небезопасный URL — на `#ZgotmplZ`; HTML-комментарии шаблона вырезаются (в дефолтном — `<!-- Alert count -->`, `<!-- Template supports… -->`, невидимы). Оператор, увидевший `ZgotmplZ` в письме, не найдёт объяснения.
- **Recommendation:** добавить в запись полфразы про `ZgotmplZ` для значений в небезопасном контексте.
- **Disposition:** fix-here
- **Follow-up:** n/a

## Раунд 2 (2026-10-23, @ 71279d1)

Дифф `fc2c1c2..71279d1`: `Option("missingkey=zero")` в `renderTemplate` и `renderHTMLTemplate`, запись Fixed и дополнение Security в `CHANGELOG.md`, правки премис и критериев в `Spec.md`/`requirements.md`/`research.md`/`tasks.md`. Проверено временным тестом в пакете (удалён, дерево чистое) и полным прогоном пакета.

### Статус находок раунда 1

- **F1 — resolved.** Алерт только с `alertname`, собранный реальным `buildEmailTemplateData`, рендерит дефолтные subject/html/text без ошибки и для `firing`, и для `resolved`: subject `[ALERT] OnlyName (1 alert)` / `[RESOLVED] OnlyName (1 alert)`; в HTML `unknown`, `N/A`, `No description` — `default` срабатывает, потому что для `map[string]string` `missingkey=zero` даёт `""`, а не invalid value; `<no value>` нигде нет. Алерт с `Labels == nil` и без аннотаций тоже рендерится (`buildEmailTemplateData` создаёт пустые map). Ошибка fallback больше не обрывает Publish на таком алерте, overlay доходит до своей очереди.
- **F2 — resolved.** Premise 3 и research ссылаются на трассу `handleAlertsPost` → `parseAlertsForProcessing` → `convertIngestInputToAlert`; ошибочная ссылка на `core/interfaces.go:258` снята с пояснением. Premise 2 теперь `verified (тест 2.1)` — тест ещё не написан, но привязка к конкретному шагу честная.
- **F3 — defer-tech-debt**, без изменений; уходит в `TECH-DEBT.md` на `finalize`.
- **F4 — resolved.** В Security-записи есть замена небезопасного значения на `#ZgotmplZ`. Упоминание вырезания HTML-комментариев не добавлено — невидимо, не требуется.

### Проверка побочных эффектов `missingkey=zero`

Одинаково для `text/template` и `html/template`:

- `eq .Status "resolved"` — `Status` поле структуры, опция не влияет; ветки `<style>`/`<h1>`/subject переключаются верно.
- `range $k, $v := .Labels`, `range .Alerts` — без изменений; внутри `range .Alerts` отсутствующий ключ тоже даёт `""` и `default`.
- Отсутствующий ключ в пользовательском шаблоне: `{{ if .Labels.x }}` → else-ветка, `{{ eq .Labels.x "" }}` → true, `{{ with .Annotations.x }}` → else, `{{ index .Labels "x" }}` → `""`, `{{ len .Labels.x }}` → `0`, `| upper` / `| default "D"` работают. Раньше `text/template` печатал `<no value>`, а пайп в функцию падал; теперь пустая строка — улучшение, не регрессия.
- Несуществующее **поле структуры** (`{{ .NoSuchField }}`) по-прежнему ошибка исполнения — опция действует только на map, опечатки в шаблоне не маскируются.
- `href="{{ .Annotations.runbook_url }}"` при отсутствии ключа → `href=""`; в дефолтном шаблоне блок и так под `{{ if }}`.
- Overlay (`business/templating`) уже использовал `missingkey=zero` — fallback и overlay теперь ведут себя одинаково.

Сборка и тесты: `go build ./...`, `go vet ./internal/infrastructure/publishing/`, `go test -count=1 ./internal/infrastructure/publishing/` — 1037 passed.

### Новые находки

#### F5 — При отсутствии `severity` text-часть пишет пустое `Severity:`, HTML — `class="severity-"`
- **Severity:** nit
- **Location:** `go-app/internal/notification/template/defaults/email.go:237,285`
- **Issue:** теперь, когда алерт без `severity` рендерится, видно несогласованность шаблонов: HTML-ячейка показывает `unknown` (`| default "unknown"`), а text — `Severity: ` (только `| upper`, без `default`); CSS-класс — `severity-`. Косметика, на доставку не влияет; запись Fixed в CHANGELOG («`default "N/A"` now applies») верна для `instance`/`description`.
- **Recommendation:** `{{ .Labels.severity | default "unknown" | upper }}` в text и `| default "unknown" | lower` в классе — одна строка каждая; можно добавить в тест 2.1.
- **Disposition:** fix-here (не блокирует)
- **Follow-up:** n/a

### Итог раунда 2

Открытых blocker/major нет: F1 закрыт по существу, побочных эффектов `missingkey=zero` не найдено. Открыты F3 (defer-tech-debt) и F5 (nit, fix-here, не блокирует). Gate — `pass`.

## Anti-Pattern Check

- [x] Self-audit was not treated as a substitute for independent review.
