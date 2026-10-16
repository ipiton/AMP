---
id: HELM-EXTERNAL-DATABASE
slug: helm-external-database
stream: Helm
type: feature
artifact: research-pack
status: draft
created_at: 2026-10-16
updated_at: 2026-10-16
---

# Research Pack - внешний PostgreSQL через values чарта

Research level: 2 (light). Generation: single-pass — паттерны известны (bitnami `externalDatabase.*`, guard'ы ADR-015/016), откат дешёвый (values-изменение в неопубликованном чарте).

## 0) TL;DR

- Solving: `postgresql.enabled: false` + `externalDatabase.*` ⇒ `DATABASE_*` env с паролем из существующего Secret; `values-production.yaml` — на внешнюю БД; прогон restore во внешний PG 16.
- Found:
  - Go-код уже читает `DATABASE_*` (viper `AutomaticEnv`, env выше config-файла); правка чарта — в одном блоке `deployment.yaml:94-116` плюс `NOTES.txt`.
  - **Предсуществующий баг:** DSN собирается `fmt.Sprintf` без экранирования (`internal/database/postgres/config.go:142-145`). Пароль с `/ # ?` или пробелом не парсится, с `%` молча превращается в другой пароль (замер, `evidence/dsn-password-escaping.txt`). Сгенерированные провайдером пароли такие символы содержат — прод-путь этой задачи упирается в баг.
  - Три render-теста и release-gate завязаны на то, что `values-production.yaml` рендерит встроенный PG; `check_secret_refs` требует, чтобы каждый `secretKeyRef` указывал на отрендеренный Secret.
- Chose (решение владельца 2026-10-16): Option B — `externalDatabase.host` задан ⇒ `DATABASE_*` из values и Secret; пустой ⇒ поведение как сейчас (реквизиты из `configFile`). Экранирование DSN чиним в этой задаче (D-a).
- Risks: `helm upgrade` прод-релиза с новым `values-production.yaml` удаляет StatefulSet встроенного PG и стартует AMP на пустой внешней БД — migration note и прогон «bundled → external» обязательны; при B путь `configFile.content` с паролем в ConfigMap остаётся рабочим, а `standard` без БД по-прежнему падает только на старте пода — README должен называть путь `externalDatabase` основным.
- Next step: -> spec

## 1) Questions

1. Как выбирать «встроенный / внешний / lite» в `deployment.yaml` и что делать при `postgresql.enabled: false` без `externalDatabase.host`?
2. Какие шаблоны завязаны на `postgresql.enabled` и должны знать о внешней БД?
3. Где запускаются render-тесты и какие из них сломает переключение `values-production.yaml`?
4. Пройдёт ли произвольный пароль из Secret до pgx без порчи?
5. Как прогнать restore-runbook на внешнем PG 16 локально и без superuser?

## 2) Findings

- **Codebase:**
  - **Выбор БД (Q1).** `deployment.yaml:94` — `if and postgresql.enabled (eq profile "standard")` ⇒ `DATABASE_*` встроенного PG; `else` ⇒ только `STORAGE_FILESYSTEM_PATH`. При этом `configmap.yaml:12` ставит `STORAGE_BACKEND=postgres` для любого не-lite профиля ⇒ сегодня `standard` + `postgresql.enabled: false` без `configFile` — под идёт на `localhost:5432` (дефолт `config.go:892`) и падает на старте. Обходной путь — `database:` в `configFile.content`, пароль в ConfigMap (`helm/amp/README.md:15,107`).
  - **Приоритет источников.** `config.go:671-672,838-839` — `viper.AutomaticEnv()` + `SetEnvKeyReplacer(".", "_")`: env `DATABASE_*` перекрывает `database.*` из config-файла. Значит, при `externalDatabase.host` реквизиты подключения берутся из env, а пул (`max_connections` и т. п.) можно по-прежнему задавать в `configFile`.
  - **Валидация в Go.** production: username и password обязательны (`config.go:1447`), пароль ≥ 12 символов, не из списка слабых (`:1466-1476`); `ssl_mode=disable` запрещён без `allow_insecure_ssl_in_production` (`:1480`). Допустимые `sslmode`: `disable allow prefer require verify-ca verify-full` (`internal/database/postgres/config.go:118-127`). Дефолт `ssl_mode=require` (`config.go:896`).
  - **Шаблоны на `postgresql.enabled` (Q2).** `_helpers.tpl:69` (`amp.postgresql.fullname`) и `:105` (`amp.databaseUrl`, не используется в `deployment.yaml`); `secret.yaml:13` (ключи `postgres-*` только для встроенного); `externalsecret.yaml:24` (то же для ESO); `NOTES.txt:25,52,90` (сейчас для внешней БД пишет «connection from the AMP config»); `postgresql-*.yaml` — весь встроенный PG. NetworkPolicy AMP — только Ingress (`networkpolicy.yaml`), egress к внешней БД не ограничен. Init-контейнеров ожидания БД и Job миграций нет (`migration.enabled: false`, миграции — в процессе AMP).
  - **Тесты (Q3).** `scripts/release-gate.sh` § `step_helm_tests` гоняет каждый `helm/amp/tests/*.sh` (новый файл подхватится сам); `step_helm` и `step_helm_rbac` рендерят `values-production.yaml` с `--set postgresql.password=…` + `tests/values-production-placeholders.yaml`. Сломаются при переключении:
    - `render-postgresql.sh` T1 — ждёт StatefulSet `amp-postgres` и `postgres:16.15` в production;
    - `render-defaults-validate.sh` T1 — ждёт в production флаг `DATABASE_ALLOW_INSECURE_SSL_IN_PRODUCTION` и `DATABASE_SSL_MODE=disable`; `check_secret_refs production` упадёт на `secretKeyRef` к `existingSecret`, которого чарт не рендерит; T3 `render external-db --set postgresql.enabled=false` упадёт на новом `required`;
    - остальные шесть тестов с `values-production.yaml` (`render-config-reloader`, `-graceful-shutdown`, `-grouping-default`, `-image-tag`, `-ingress-auth`, `-networkpolicy`) получат значения из placeholders — достаточно дописать туда `externalDatabase.host/existingSecret`.
  - **DSN (Q4, measured).** `PostgresConfigFrom` (`internal/config/reloadable_database.go:31-38`) копирует пароль как есть; пул строится из `pgxpool.ParseConfig(cfg.DSN())` (`internal/database/postgres/pool.go:174`), миграции — из `sql.Open("pgx", config.DSN())` (`internal/database/migrations.go:185`). `DSN()` = `postgres://user:pass@host:port/db?sslmode=…` без `url.UserPassword`. На pgx v5.9.2: `@` и `:` — ок; `/ # ?` — ошибка `invalid port ":Ab" after host`, пробел — `invalid userinfo`; `%` — парсится, но пароль другой (`pw_match=false`) ⇒ `password authentication failed`. Сообщение pgx маскирует пароль (`xxxxxx`), утечки в лог нет, но и причину оно не называет. Встроенный PG не затронут: пароль чарта — `randAlphaNum`. Тот же дефект у имени пользователя и БД. В `BUGS.md`/`TECH-DEBT.md` не записан.
  - **Restore на внешнем PG (Q5).** Миграция `20250911094416_initial_schema.sql:7-8` создаёт `uuid-ossp` и `btree_gin`; оба — trusted extensions с PG 13, владелец БД без superuser создать их может (проверить в прогоне). Локально: Docker 29.4.0, `k3d` есть (кластеров нет), `pg_dump`/`psql` на хосте нет ⇒ клиент — `postgres:16` в контейнере. TLS для `sslMode: require`: официальный образ `postgres` поставляет snakeoil-сертификат (`-c ssl=on -c ssl_cert_file=/etc/ssl/certs/ssl-cert-snakeoil.pem -c ssl_key_file=/etc/ssl/private/ssl-cert-snakeoil.key`) — assumption, проверить в прогоне. Образа в GHCR нет — локальная сборка + `k3d image import`.
- **Constraints:**
  - пароль — только `secretKeyRef` (requirements § Security); флаг insecure-SSL в ветке внешней БД не рендерится;
  - fail-loud на рендере (ADR-015/016), текст ошибки называет ключ и обход;
  - Go-код меняется только в `DSN()` (решение D-a, § 4);
  - `verify-full` без `sslrootcert`: pgx берёт системные корни; CA managed-провайдера (RDS и т. п.) там обычно нет ⇒ `verify-full` к RDS без CA-бандла не заработает. Клиентские TLS-материалы — non-goal, записать в README.
- **Unknowns:** snakeoil-сертификат и trusted-extensions — подтверждаются прогоном; время прогона на k3d (оценка ~1–1.5 ч вместе со сборкой образа).

## 3) Options

`Generation:` single-pass

### Option A - строгий выбор: в `standard` без встроенного PG внешняя БД обязательна

`postgresql.enabled: true` ⇒ как сейчас. `false` + `profile: standard` ⇒ `required` на `externalDatabase.host` и `externalDatabase.existingSecret`, env `DATABASE_*` из `externalDatabase`. Путь «всё в `configFile.content`» перестаёт рендериться; `configFile` остаётся для пула и прочего.

- **Pros:** закрывает сам дефект (пароль в ConfigMap) вместо того, чтобы оставить его вторым путём; конфигурация «БД нет вообще» (сегодня — падение пода на `localhost`) ловится на `helm template`; одна ветка в шаблоне, тест простой.
- **Cons:** ломает README-путь `configFile.content` (записан 2026-10-14, релиза с ним нет); пользователь обязан создать Secret сам.
- **Cost/Risk:** low

### Option B - мягкий: `externalDatabase.host` пустой ⇒ поведение как сейчас

Без `host` чарт не рендерит `DATABASE_*` и полагается на `configFile`; с `host` — env.

- **Pros:** не ломает никого.
- **Cons:** сохраняет plain-text пароль как равноправный путь; `standard` без БД снова падает только на старте пода; два пути — две документации и два набора тестов; противоречит fail-loud ADR-015/016.
- **Cost/Risk:** low по коду, medium по эксплуатации

### Option C - B + guard «host пустой и `configFile.enabled: false` ⇒ fail»

- **Pros:** ловит «БД нет вообще», не ломает `configFile`-путь.
- **Cons:** plain-text путь остаётся; guard не знает, есть ли в `configFile.content` секция `database:` (парсить YAML из строки в шаблоне — хрупко).
- **Cost/Risk:** low/medium

## 4) Decision

- **Chosen:** Option B — решение владельца 2026-10-16 (рекомендация research была A).
- **Why (владелец):** не ломать существующий путь `configFile.content`; новый путь аддитивен.
- **Следствия для spec:**
  - `externalDatabase.host` — переключатель: задан ⇒ env `DATABASE_*` и `required` на `existingSecret`; пуст ⇒ шаблон рендерит то же, что сейчас;
  - README/NOTES называют `externalDatabase` поддерживаемым путём, `configFile.content` — запасным, с явным предупреждением про пароль в ConfigMap;
  - `values-production.yaml` всё равно должен не рендериться без внешней БД (story 2): там guard ставится на уровне файла — пустой `externalDatabase.host` при `postgresql.enabled: false` в production values. Как отличить production values от дефолта, решить в spec (в `ingress.yaml:3` записано, что ключа «production» нет; вариант — отдельный value-флаг `externalDatabase.required`, заданный только в `values-production.yaml`).
- **Экранирование DSN — D-a, решение владельца 2026-10-16: чинить в этой задаче.** Варианты, которые рассматривались:
  - **D-a. Чинить в этой задаче.** `DSN()` через `url.URL{User: url.UserPassword(...)}` (+ `PathEscape` для имени БД), unit-тест на `/ # ? % пробел @ :`; ~15 строк Go + тест. Добавляет сигнал `X` (Go + чарт), tier остаётся Full. Плюс: прод-путь работает с паролем провайдера «как есть», а не после ручного подбора символов.
  - **D-b. Не чинить здесь.** Запись в `BUGS.md` (high, Go), в values/README — «пароль только из `[A-Za-z0-9@:._-]`» до фикса. Плюс: скоуп как в requirements. Минус: первый же пароль из RDS/Cloud SQL/Vault падает с ошибкой про «port», а `%` — с ошибкой аутентификации без подсказки.
  - Выбрано **D-a**: дефект лежит ровно на пути, который задача объявляет поддерживаемым; проверяется тем же прогоном на k3d (пароль с `/` и `%`).

## 5) Spec Inputs

- **API/contracts (values):** `externalDatabase.{host: "", port: 5432, database: "alert_history", username: "alert_history", sslMode: "require", existingSecret: "", existingSecretPasswordKey: "password"}` — имена по bitnami. Дефолты `database`/`username` — как у встроенного (`values.yaml:305`) или пустые с `required`: выбрать в spec. `sslMode` — enum-guard на рендере (6 значений из `postgres/config.go`). Env: `DATABASE_HOST`, `DATABASE_PORT` (строкой), `DATABASE_DATABASE`, `DATABASE_USERNAME`, `DATABASE_PASSWORD` (`secretKeyRef`), `DATABASE_SSL_MODE`; `STORAGE_FILESYSTEM_PATH` и insecure-флаг — не рендерить. Lite игнорирует `externalDatabase` (как сейчас игнорирует `postgresql`).
- **`values-production.yaml`:** `postgresql.enabled: false` + блок `externalDatabase` с пустыми `host`/`existingSecret` (render падает, пока оператор их не задаст — как `webConfig.existingSecret`); убрать устаревший блок про встроенный PG и `password: ""`. `tests/values-production-placeholders.yaml` += `externalDatabase.host`/`existingSecret`; `--set postgresql.password=…` в release-gate и тестах становится лишним — убрать или оставить безвредным (решить в spec).
- **Render-тест** `helm/amp/tests/render-external-database.sh`: env и `secretKeyRef`; нет insecure-флага при `sslMode: disable`; `required` на `existingSecret` при заданном `host`; пустой `host` ⇒ рендер как до задачи (нет `DATABASE_*`); enum `sslMode`; lite не трогается; встроенный PG не изменился; production рендерит внешнюю БД без StatefulSet `amp-postgres` и падает без неё. Правки: `render-postgresql.sh` T1 (production → «нет StatefulSet»), `render-defaults-validate.sh` T1 и `check_secret_refs` (разрешить ссылку на `externalDatabase.existingSecret`); T3 `external-db` при B остаётся зелёным.
- **Go (D-a):** `internal/database/postgres/config.go` `DSN()` — `url.URL{Scheme: "postgres", User: url.UserPassword(user, pass), Host: net.JoinHostPort(host, port), Path: "/" + db, RawQuery: "sslmode=…"}`; `ConnectionString()` (keyword-формат) — проверить, используется ли, и либо экранировать, либо не трогать; `GetDatabaseURL()` в `internal/config/config.go:1488` — тот же дефект, проверить вызовы. Unit-тест: round-trip через `pgxpool.ParseConfig` для `/ # ? % пробел @ :` в пароле, пользователе и имени БД.
- **Data model/migrations:** схема не меняется. Migration note для `values-production.yaml`: до `helm upgrade` — `pg_dump -Fc` встроенного, создать пустую БД и владельца на внешнем PG, `pg_restore` без `--clean` при AMP в 0 реплик, затем upgrade. PVC встроенного PG после upgrade остаётся (volumeClaimTemplate) — удалить руками после проверки.
- **Rollout/rollback:** rollback — `helm rollback` на ревизию со встроенным PG; StatefulSet пересоздастся на старом PVC, если его не удалили.
- **Прогон (k3d):** встроенный PG → данные (алерты, silence) → `pg_dump` → внешний `postgres:16` в docker-сети k3d с `ssl=on`, владелец БД не superuser → `pg_restore` без `--clean` → upgrade на `externalDatabase` (`sslMode: require`, Secret) → `/-/ready`, алерты и silences на месте, миграции не переприменены. При D-a — пароль с `/` и `%`. Команды — в runbook § «External PostgreSQL» вместо «not drilled»; raw-вывод — `evidence/`.
- **Observability:** `NOTES.txt` — для внешней БД показывать `host:port/db` и `sslMode` (без пароля) вместо «connection from the AMP config».
- **Security/ownership/RBAC:** пароль только `secretKeyRef`; ESO-путь для `existingSecret` — non-goal (оператор создаёт Secret сам или своим ESO; `externalsecret.yaml` не трогаем, связь с `ESO-SECRET-OWNERSHIP-CONFLICT` — в README не обещать). `sslMode: disable` в production ⇒ AMP отказывается стартовать — написать в комментарии values.
- **Docs:** `helm/amp/README.md:15,102-107` (вместо `configFile.content`), `POSTGRESQL_RESTORE_GUIDE.md` § «External PostgreSQL» и строка 9, комментарий в `values-production.yaml`, `CHANGELOG.md` `[Unreleased]` (Added + breaking/migration note).

## 6) References

- Files: `helm/amp/templates/deployment.yaml:94-116`, `helm/amp/templates/configmap.yaml:12`, `helm/amp/templates/NOTES.txt:21-60,90`, `helm/amp/templates/_helpers.tpl:66-110`, `helm/amp/templates/secret.yaml:13`, `helm/amp/templates/externalsecret.yaml:24`, `helm/amp/values.yaml:292-400`, `helm/amp/values-production.yaml:48-80`, `helm/amp/tests/render-postgresql.sh:110-135`, `helm/amp/tests/render-defaults-validate.sh:60-130`, `helm/amp/tests/values-production-placeholders.yaml`, `scripts/release-gate.sh` (`step_helm`, `step_helm_rbac`, `step_helm_tests`), `go-app/internal/config/config.go:329-348,671,892-897,1447-1482`, `go-app/internal/config/reloadable_database.go:31-55`, `go-app/internal/database/postgres/config.go:118-145`, `go-app/internal/database/postgres/pool.go:174`, `go-app/internal/database/migrations.go:185`, `go-app/migrations/20250911094416_initial_schema.sql:7-8`
- Docs: `docs/06-planning/DECISIONS.md` ADR-015, ADR-016; `helm/amp/docs/POSTGRESQL_RESTORE_GUIDE.md`; `helm/amp/README.md`; `tasks/archive/PROD-POSTGRES-HA-DECISION/`, `tasks/archive/HELM-DEFAULTS-VALIDATE/`
- Evidence: `evidence/dsn-password-escaping.txt`
