# Deep Review Findings: дефолтные values чарта проходят валидацию AMP

**Trigger classification:** mandatory (`S`; 4 сигнала `C S X R`)
**Reviewer perspective:** два независимых read-only агента (Plan), не автор: R1 — security + premises + correctness + git history; R2 — contract + cross-domain + runtime + maintainability + requirements coverage
**Reviewed at:** 2026-10-14
**Reviewed tree:** bugfix/helm-defaults-validate @ 867e169
**Verdict:** fix_required (см. `review-verdict.json`)

Что ревьюеры подтвердили запуском: `go vet` и `go test ./internal/config/... ./cmd/server/...` зелёные, `TestChartEnvKeysKnownToViper` (`config_load_test.go:139`) резолвит новый env; `helm lint`/`template` для dev и production с placeholders как в `release-gate.sh`; 7/7 `helm/amp/tests/*.sh`; рендер в комбинациях (default, prod, lite, `ssl=on`, `postgresql.enabled=false`, LLM с ключом / без / с ESO / `llm.secret.enabled` / только `authToken`). `database.url` в рантайме не читается (`GetDatabaseURL` — только `example.go`), `Warn` не может напечатать креды. Design Premises 1, 3–7 подтверждены, 2 (hot-reload) — code-read, честно. В истории (`e1d520e`, `ce4ce72` TN-205) нет решения, которое изменение нарушает.

## Findings

### F1 — две пользовательские страницы по-прежнему пишут, что дефолты чарта не стартуют
- **Severity:** major
- **Location:** `docs/MIGRATION_QUICK_START.md:24`, `docs/ALERTMANAGER_COMPATIBILITY.md:26`, `:906-912` (Known Gap #15)
- **Issue:** «Do not install with chart defaults», «default `llm.enabled: true`», обходы через `lite`/внешнюю БД, «Tracked as `HELM-DEFAULTS-VALIDATE` (P0)» — после изменения неправда и противоречит README чарта и CHANGELOG. В `tasks.md` 3.x этих файлов не было. Найдено обоими ревьюерами (R1 C2, R2 #1).
- **Recommendation:** переписать абзац MIGRATION и #15 под новые дефолты (LLM выкл., флаг для встроенного PG, внешняя БД через `configFile.content` до `HELM-EXTERNAL-DATABASE`); строку 26 обновить.
- **Disposition:** fix-here
- **Follow-up:** n/a

### F2 — рекомендуемый путь «LLM через ESO» упирается в старый конфликт Secret/ExternalSecret
- **Severity:** minor
- **Location:** `helm/amp/templates/secret.yaml:2-11`, `externalsecret.yaml:17-19`
- **Issue:** сообщение `fail` и README советуют `externalSecrets.enabled`, а при нём Helm Secret и ExternalSecret (`creationPolicy: Owner`) претендуют на одно имя. Дефект предсуществующий (R1 S1), изменением не вносится.
- **Recommendation:** проверить на кластере с ESO в рамках бага.
- **Disposition:** defer-bug
- **Follow-up:** `BUGS.md` `ESO-SECRET-OWNERSHIP-CONFLICT` (заведён на research); дописать, что путь теперь рекомендуется сообщением `fail`.

### F3 — `Warn` только на старте; reload флага даёт ложное «restart required»
- **Severity:** minor
- **Location:** `go-app/cmd/server/main.go:95`, `go-app/internal/config/reloadable_database.go:140`
- **Issue:** при включении флага + `ssl_mode: disable` через `/-/reload` предупреждения нет; изменение только флага попадает в `changedFields` и даёт W600 «restart required» по полю, которое на пул не влияет. Сейчас безопасно: в `standard` любое изменение `database.*` требует рестарта, а на старте `Warn` будет (R1 C1, R2 #4).
- **Recommendation:** логировать то же состояние при reload; исключить флаг из полей, требующих рестарта.
- **Disposition:** defer-tech-debt
- **Follow-up:** `TECH-DEBT.md` — дописать в `FU-DB-LIVE-POOL-HANDLE`, если запись есть, иначе новая (на `finalize`).

### F4 — Test Plan не покрывает инвариант «флаг не трогает остальные проверки» полностью
- **Severity:** minor
- **Location:** `tasks/HELM-DEFAULTS-VALIDATE/Spec.md` § Test Plan G2–G4
- **Issue:** G2 — только слабый пароль; нет пустых username/password и пароля < 12 при флаге. G4 — нет `lite` и «флаг `false` + `disable`». G3 должен проверять Edge Case 7 (`=yes` — ошибка, а не молчаливый `false`) (R2 #2, R1 C3).
- **Recommendation:** расширить G2–G4 в Spec; написать в `write-tests`.
- **Disposition:** fix-here (Spec сейчас, тесты — в `write-tests`)
- **Follow-up:** n/a

### F5 — сверка `secretKeyRef` только на дефолтном рендере
- **Severity:** minor
- **Location:** `Spec.md` T1
- **Issue:** release-gate для prod/dev проверяет только, что рендер прошёл; битые ссылки на Secret там не ловятся (R2 #3).
- **Recommendation:** T1 гонять и для `values-production.yaml` (placeholders), и для `values-dev.yaml`.
- **Disposition:** fix-here (Spec сейчас, тест — в `write-tests`)
- **Follow-up:** n/a

### F6 — CHANGELOG и Spec не называют `values-dev.yaml`/`values-production.yaml` как базу
- **Severity:** nit
- **Location:** `CHANGELOG.md` migration note; `Spec.md` § Config contract
- **Issue:** кто ставит с `-f values-production.yaml`/`values-dev.yaml`, тоже теряет LLM; Spec перечисляет два values-файла вместо трёх (отклонение записано только в `tasks.md`) (R2 #5).
- **Recommendation:** дописать migration note; обновить Spec до v1.1.
- **Disposition:** fix-here
- **Follow-up:** n/a

### F7 — `postgresql.config.ssl` не bool-safe
- **Severity:** nit
- **Location:** `helm/amp/templates/deployment.yaml:109-110`
- **Issue:** `ssl: on` без кавычек / `--set ...ssl=true` → `eq: incompatible types`; `ssl: "true"` даёт PostgreSQL `ssl = true`, а AMP — `disable` и флаг. Предсуществующее (R1 S2, R2 #6).
- **Recommendation:** сравнивать через `toString`, `fail` на всё кроме `"on"`/`"off"`.
- **Disposition:** defer-tech-debt
- **Follow-up:** `TECH-DEBT.md` `HELM-CHART-GAPS` (на `finalize`).

### F8 — в таблице параметров README `llm.model` = `gpt-4o-mini`, в values — `openai/gpt-4o`
- **Severity:** nit
- **Location:** `helm/amp/README.md:85`
- **Issue:** предсуществующее расхождение (R2 #7).
- **Recommendation:** выровнять таблицу с values.
- **Disposition:** reject (reason: однострочная правка в уже затронутом файле дешевле записи в BACKLOG — исправлено здесь же вместе с F1)
- **Follow-up:** n/a

### F9 — «дефолтный install стартует» в CHANGELOG пока не проверен запуском
- **Severity:** minor
- **Location:** `CHANGELOG.md` Fixed `HELM-DEFAULTS-VALIDATE`
- **Issue:** поведенческое утверждение; подтверждается бинарной проверкой B1 (R2 #8).
- **Recommendation:** B1 на `testing`: валидация проходит, `Warn` с `host`, пароля в логе нет.
- **Disposition:** fix-here (на `testing`, `tasks.md` 5.3)
- **Follow-up:** n/a

## Anti-Pattern Check

- [x] Self-audit was not treated as a substitute for independent review.
