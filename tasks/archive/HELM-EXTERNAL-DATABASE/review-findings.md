# Deep Review Findings: внешний PostgreSQL через values чарта

**Trigger classification:** mandatory (`S`; 4 сигнала `C S X R`, tier Full)
**Reviewer perspective:** два независимых агента без доступа к рассуждениям автора и друг к другу:
- Reviewer A: security, контракт values, guard'ы и ветки шаблона, NOTES. Проверял рендером, в том числе против `git archive main` и копии чарта без `externalDatabase` (`--reuse-values`).
- Reviewer B: Go `DSN()` (пробы round-trip через pgx), Design Premises, runbook, README и CHANGELOG против кода и evidence.

Кластеры ревьюеры не поднимали. Автор свёл находки, объединил пересечения (A-S1 + B-F1 → R1, A-D1 + B-F5 → R6, A-D4 + B-F8 → R11, A-D5 + B-F10 → R13) и перепроверил ключевые: R1 — `Validate()` `config.go:124-131` и вызов из `buildPool` (`pool.go:159`); R2 — рендер production + `postgresql.enabled=true`.
**Reviewed at:** 2026-10-16
**Reviewed tree:** feature/helm-external-database @ 6eb1774; повторно @ e36a047 (§ Re-review), фиксы до 44aefce
**Verdict:** pass @ 44aefce (см. `review-verdict.json`); первый проход — fix_required @ 6eb1774

Поле `Status` в каждой находке читает скрипт вердикта: `open` | `fixed` | `deferred` | `rejected`. В скобках указан номер находки у ревьюера.

## Findings

### R1 — `sslMode: allow|prefer` проходит рендер, но AMP не стартует; в production эти режимы были бы ещё и без TLS (A-S1, B-F1)
- **Severity:** major
- **Location:** `helm/amp/templates/deployment.yaml:108-110` (enum из 6 значений); `helm/amp/values.yaml` (комментарий `sslMode`); `helm/amp/README.md` § External PostgreSQL; `CHANGELOG.md` Added; `go-app/internal/database/postgres/config.go:124-131`
- **Issue:** `PostgresConfig.Validate()` принимает только `disable|require|verify-ca|verify-full`. `buildPool` вызывает его до `DSN()` и при старте, и при reload, так что `allow`/`prefer` дают `invalid SSL mode` и CrashLoop. Чарт и документация обещают 6 режимов. Если бы AMP их принимал, `allow`/`prefer` в production молча шли бы без TLS: проверка `config.go:1480` отсекает только `disable`.
- **Recommendation:** сузить enum чарта, сообщение `fail`, комментарий values, README и CHANGELOG до четырёх значений, которые принимает AMP. Рантайм не трогать.
- **Disposition:** fix-here
- **Status:** fixed — `1789ff2`, docs `a567e43`
- **Follow-up:** n/a

### R2 — Совет «остаться на встроенном PG» в migration note молча ослабляет production-релиз (A-C1)
- **Severity:** major
- **Location:** `CHANGELOG.md` Breaking/migration note («To stay on the bundled database for now, set `postgresql.enabled: true`…»); `helm/amp/values-production.yaml` — блок `postgresql:` удалён целиком
- **Issue:** в `values-production.yaml` больше нет production-переопределений `postgresql.*`. Кто последует совету, получит базовые значения `values.yaml`:
  - `password: "secure_password_123"` в обоих Secret'ах — удалённый `password: ""` существовал как раз для того, чтобы перекрыть этот литерал;
  - `persistence.size` 10Gi вместо 50Gi — поле лежит в `volumeClaimTemplates`, API его менять не даст, upgrade отклонится (вывод из правил API, не прогонялось);
  - память 2Gi вместо 4Gi;
  - NetworkPolicy `amp-postgres` пропадает.

  Перепроверено: `helm template … -f values-production.yaml -f placeholders --set postgresql.enabled=true --set externalDatabase.host=` даёт `c2VjdXJlX3Bhc3N3b3JkXzEyMw==` ×3, `storage: "10Gi"`. Release-gate всегда передаёт `--set postgresql.password`, поэтому не ловит.
- **Recommendation:** переписать совет в CHANGELOG и комментарии `values-production.yaml`: «остаться» = вернуть прежний блок `postgresql:` из `values-production.yaml` предыдущей версии (минимум `password` через `--set`/Secret, `persistence.size: 50Gi`, `resources`, `networkPolicy.enabled: true`), с командой `git show`. Guard на слабый дефолт пароля — отдельно.
- **Disposition:** fix-here (docs); guard на `secure_password_123` под production — defer-tech-debt
- **Status:** fixed — `a567e43`
- **Follow-up:** TECH-DEBT `HELM-CHART-GAPS` (слабый литерал уже там) — дополнить в finalize

### R3 — IPv6-хост в скобках (`[::1]`) раньше работал, после `net.JoinHostPort` ломается (B-F2)
- **Severity:** minor
- **Location:** `go-app/internal/database/postgres/config.go:147-155`
- **Issue:** `JoinHostPort("[::1]", …)` → `[[::1]]:5432`, pgx: `invalid IP-literal`. Старый `fmt.Sprintf` хост `[::1]` разбирал. Голый `::1` раньше был сломан, теперь работает. Чарт передаёт значение как есть.
- **Recommendation:** снимать внешние скобки перед `JoinHostPort`; кейс — в Go-тест Phase 7.
- **Disposition:** fix-here
- **Status:** fixed — `7a274f0`
- **Follow-up:** n/a

### R4 — Guard «БД нет нигде» — текстовый regex, ошибается в обе стороны (A-C2)
- **Severity:** minor
- **Location:** `helm/amp/templates/deployment.yaml:111` (`regexMatch "(?m)^database:"`)
- **Issue:** `database: null` и строка `database:` в начале строки внутри многострочного quoted-значения проходят guard (AMP потом уходит на `localhost:5432`). Flow-style и ключ в кавычках ложно падают (Edge Case 7). `fromYaml` разбирает все эти случаи правильно (проверено пробой ревьюера A).
- **Recommendation:** `(fromYaml (.Values.configFile.content | default "")).database` — истинно только для настоящего непустого ключа верхнего уровня. Убрать оговорку из README и Edge Case 7.
- **Disposition:** fix-here
- **Status:** fixed — `1789ff2`, docs `a567e43`
- **Follow-up:** n/a

### R5 — § 3c с HPA (дефолт и `values-production`) не прогонялся; последний шаг может оставить AMP на 0 реплик (B-F3)
- **Severity:** minor
- **Location:** `helm/amp/docs/POSTGRESQL_RESTORE_GUIDE.md` § 3c
- **Issue:** в прогоне было `autoscaling.enabled: false`. При HPA чарт не рендерит `spec.replicas`; останется ли `0` от `kubectl scale`, зависит от managedFields под SSA. HPA при 0 — `ScalingDisabled`, а `rollout status` при 0 репликах успешен. Шапка гайда («with the chart's default HPA») к 3c не относится.
- **Recommendation:** в конце 3c, как в 3a, явный `kubectl scale deploy/<fullname> --replicas=<N>` или проверка `.spec.replicas`; поправить шапку. Желательно прогнать 3c с HPA.
- **Disposition:** fix-here
- **Status:** fixed — `a567e43`
- **Follow-up:** n/a

### R6 — Создание Secret: пароль в shell history (`--from-literal`) и незаметный `\n` (`--from-file`) (A-D1, B-F5)
- **Severity:** minor
- **Location:** `helm/amp/values.yaml` (комментарий `existingSecret`); `helm/amp/README.md` § External PostgreSQL; runbook § 3c
- **Issue:** `--from-literal` противоречит правилу runbook «never on the command line». Для `--from-file` не сказано, что файл не должен кончаться переводом строки. AMP пароль не обрезает, получается ошибка аутентификации, которую трудно распознать.
- **Recommendation:** везде `--from-file` + «файл без завершающего перевода строки (`printf '%s'`)».
- **Disposition:** fix-here
- **Status:** fixed — `a567e43`, комментарий values.yaml в `1789ff2`
- **Follow-up:** n/a

### R7 — Пул на под × HPA против `max_connections` внешней БД нигде не описан (B-F6)
- **Severity:** minor
- **Location:** `helm/amp/values-production.yaml` (удалён комментарий «20 replicas × ~20 conns/pod»); README § External PostgreSQL
- **Issue:** 25 соединений на под × production `maxReplicas: 20` = до 500. У малых managed-тарифов лимит ~100. NOTES это считает, а docs внешней БД — нет.
- **Recommendation:** одна строка в README и в `values-production.yaml`.
- **Disposition:** fix-here
- **Status:** fixed — `a567e43`
- **Follow-up:** n/a

### R8 — Путь ESO для пароля внешней БД не описан (A-D2)
- **Severity:** minor
- **Location:** `helm/amp/templates/externalsecret.yaml:24`; README § External PostgreSQL
- **Issue:** при `externalSecrets.enabled` ExternalSecret чарта ключи БД маппит только при `postgresql.enabled`. Указать `existingSecret` на `<fullname>-secrets` — `CreateContainerConfigError`.
- **Recommendation:** строка в README: с ESO заведите свой ExternalSecret, целевой Secret которого — `externalDatabase.existingSecret`.
- **Disposition:** fix-here
- **Status:** fixed — `a567e43`
- **Follow-up:** n/a

### R9 — Финальный фильтр `-L` не прогонялся в сквозном переносе (B-F4)
- **Severity:** minor
- **Location:** runbook § 3c; Spec D9 («команды ровно из прогона»)
- **Issue:** в сквозном проходе (evidence § 6) был фильтр с одним `-e`. Фильтр с двумя `-e` проверен отдельно на `amp4`/`amp5` (§ 7). На потерю данных фильтр проверен ревьюером: TABLE/TABLE DATA/INDEX не задевает.
- **Recommendation:** если 3c будут перепрогонять (R5), прогнать с финальным фильтром; иначе одной фразой в evidence указать, где проверен финальный вариант.
- **Disposition:** fix-here
- **Status:** fixed — `a567e43`
- **Follow-up:** n/a

### R10 — `pg_trgm` назван расширением AMP (B-F7)
- **Severity:** nit
- **Location:** README § External PostgreSQL; runbook «No `--clean`»
- **Issue:** миграции создают только `uuid-ossp` и `btree_gin`; `pg_trgm` создаёт init-скрипт встроенного PG и приходит только с его дампом.
- **Disposition:** fix-here
- **Status:** fixed — `a567e43`
- **Follow-up:** n/a

### R11 — Комментарий MIGRATION в `values-production.yaml` ведёт не в тот раздел runbook (A-D4, B-F8)
- **Severity:** nit
- **Location:** `helm/amp/values-production.yaml:62-63`
- **Issue:** ссылка на § «External PostgreSQL», а перенос — § 3c.
- **Disposition:** fix-here
- **Status:** fixed — `a567e43`
- **Follow-up:** n/a

### R12 — Spec отстаёт от прогона (B-F9)
- **Severity:** nit
- **Location:** `tasks/HELM-EXTERNAL-DATABASE/Spec.md` — Design Premises, Component Architecture D9
- **Issue:** premise «env перекрывает config-файл» помечен `code-read`, хотя прогон его измерил (evidence § 4). D9 ссылается на `k3d-external-drill.txt`, файл называется `.md`.
- **Disposition:** fix-here
- **Status:** fixed — `a567e43`
- **Follow-up:** n/a

### R13 — Ротация пароля и оговорка к `--force-conflicts` (A-D5, B-F10)
- **Severity:** nit
- **Location:** README § External PostgreSQL; runbook § 3c «Helm 4»
- **Issue:**
  - пароль приходит в env через `secretKeyRef`: после смены Secret нужен `kubectl rollout restart`, а README:154 про webConfig («apply without a pod restart») можно прочитать на свой счёт;
  - `--force-conflicts` забирает все конфликтующие поля — применять, только если в ошибке одна эта аннотация;
  - `helm test` для внешней БД нет.
- **Disposition:** fix-here (две фразы); `helm test` для внешней БД — defer-backlog
- **Status:** fixed — `a567e43`
- **Follow-up:** BACKLOG — в finalize

### R14 — В README нет фразы о мониторинге, обещанной Spec § Observability (A-D3)
- **Severity:** nit
- **Location:** README § External PostgreSQL
- **Issue:** PrometheusRules и exporter есть только у встроенного PG; для внешней БД алерты на стороне оператора — Spec обещал это одной фразой.
- **Disposition:** fix-here
- **Status:** fixed — `a567e43`
- **Follow-up:** n/a

### R15 — Нестрогая обработка странных входных значений (A-C3)
- **Severity:** nit
- **Location:** `helm/amp/templates/deployment.yaml:95,128`
- **Issue:** `port: 0` молча становится 5432; `host: " "` считается заданным; строка `"false"` в `postgresql.enabled` даёт сбивающее сообщение (truthiness существовала до задачи).
- **Disposition:** reject (reason: `port: 0` → дефолт безвреден; нечисловой порт AMP отклоняет на старте громко; хост из пробелов падает на резолве с понятной ошибкой; строковый `enabled` — общее свойство всех флагов чарта, не этой задачи)
- **Status:** rejected
- **Follow-up:** n/a

### R16 — Условия веток дублируются в блоке Database Configuration (A-M1)
- **Severity:** nit
- **Location:** `helm/amp/templates/deployment.yaml:94-158`
- **Issue:** `eq .Values.profile "standard"` проверяется в guard'ах и в каждой ветке env.
- **Disposition:** reject (reason: ревьюер сам считает приемлемым; вынос в `$dbSource` — абстракция ради трёх условий, ветки рядом в одном блоке)
- **Status:** rejected
- **Follow-up:** n/a

## Re-review @ e36a047

Новый независимый агент (read-only) проверил фиксы R1–R14 по `git diff 6eb1774..e36a047` и искал новое в изменённых местах: рендеры (в том числе 17 вариантов `configFile.content`), пробы `DSN()` через pgx v5.9.2 (`[::1]`, `::1`, `[fe80::1%eth0]`, hostname, пароль с `/#?%41 @:`), сверка утверждений docs с кодом. R1, R3–R14 — fixed. R2 — partially: совет содержал ошибку (N1). Новые находки — N1–N5, все исправлены в `9c9bf5d` и `44aefce`.

### N1 — Совет «остаться на встроенном PG» предлагает `postgresql.existingSecret`, который пароль встроенной БД не подаёт
- **Severity:** major
- **Location:** `CHANGELOG.md` Breaking (текст фикса R2); `helm/amp/templates/_helpers.tpl:79-81`
- **Issue:** `amp.postgresql.secretName` всегда `<fullname>-postgresql-secret`, его `password` рендерится из `postgresql.password`; `existingSecret` лишь убирает ключи БД из `secret.yaml`. Рендер production + старый блок `postgresql:` + `postgresql.existingSecret=my-pg`: `my-pg` в выводе нет, `data.password` пустой → после upgrade AMP и postgres с пустым паролем.
- **Recommendation:** оставить только `--set postgresql.password` / values-файл вне git.
- **Disposition:** fix-here
- **Status:** fixed — `44aefce`
- **Follow-up:** n/a

### N2 — Комментарий `values.yaml` и запись `HELM-CHART-GAPS` утверждают, что `values-production.yaml` перекрывает пароль на `""`
- **Severity:** minor
- **Location:** `helm/amp/values.yaml:307-313`; `docs/06-planning/TECH-DEBT.md:26`
- **Issue:** блока `postgresql:` в `values-production.yaml` больше нет.
- **Disposition:** fix-here
- **Status:** fixed — `44aefce`
- **Follow-up:** дополнение `HELM-CHART-GAPS` (guard на слабый пароль, R2) — в finalize

### N3 — Guard на `fromYaml`: невалидный YAML даёт «no database is configured»; регистр и тип ключа
- **Severity:** nit
- **Location:** `helm/amp/templates/deployment.yaml:112`
- **Issue:** (a) ошибка разбора маскируется сообщением о пустой БД; (b) `Database:` отвергается, хотя viper регистр не различает; (c) `database: 5` проходит.
- **Recommendation:** (a) отдельный `fail` с ошибкой разбора; (b), (c) принять и записать в Edge Case 7.
- **Disposition:** fix-here (a); accept (b), (c) — Spec Edge Case 7
- **Status:** fixed — `9c9bf5d` (a), Spec в `44aefce`
- **Follow-up:** render-тест на невалидный YAML — Phase 7

### N4 — Рецепт Secret: переменная с паролем, umask, формулировка про `\n` в README
- **Severity:** nit
- **Location:** `helm/amp/README.md` § External PostgreSQL; runbook § 3c; `helm/amp/values.yaml` (комментарий `existingSecret`)
- **Disposition:** fix-here
- **Status:** fixed — `44aefce` (`read -rs`, `(umask 077; printf …)`, формулировка из runbook)
- **Follow-up:** n/a

### N5 — Неточности: «HPA object present» в evidence § 9, «volume size cannot shrink», «then the HPA scales it»
- **Severity:** nit
- **Location:** `evidence/k3d-external-drill.md` § 9; `CHANGELOG.md` Breaking; runbook § 3c
- **Issue:** rev 2 HPA не рендерит — наблюдение снято сразу после upgrade без `--wait`; отказ upgrade — из-за неизменяемых `volumeClaimTemplates`; прогон был с `minReplicas: 1`.
- **Disposition:** fix-here
- **Status:** fixed — `44aefce`
- **Follow-up:** n/a

## Anti-Pattern Check

- [x] Self-audit was not treated as a substitute for independent review: находки получены от двух независимых агентов, автор только сводил и перепроверял.
