# Deep Review Findings: AMP авторизуется в кеше чарта на дефолтах

**Trigger classification:** mandatory (`S`; 3 сигнала `C S R`)
**Reviewer perspective:** два независимых агента (general-purpose), каждый без доступа к выводу другого: A — security / secret handling + contract compatibility + premises; B — correctness + premises + runtime/rollout + maintainability. Раунд исправлений проверяет третий независимый агент (R2 ниже).
**Reviewed at:** 2026-10-20
**Reviewed tree:** `bugfix/amp-cache-noauth` @ `697d100` (раунд 1); исправления @ `834ecbe`, проверены раундом 2; C1 @ `bf5761b` (2 строки, проверено рендером по сценарию ревьюера)
**Verdict:** pass @ `bf5761b` (`review-verdict.json`: 1 major, 7 minor, 9 nit; все fix-here закрыты, кроме C3 — тесты в write-tests)

Сходимость: A1 и B1 найдены обоими агентами независимо (наблюдение, не подтверждение).

## Findings

### A1 — внешний `cache.host` при `valkey.replicas` по умолчанию получает пароль Valkey чарта (= B1)
- **Severity:** major
- **Location:** `helm/amp/templates/deployment.yaml:175-181` (@ `697d100`), `NOTES.txt:38`
- **Issue:** «свой кеш» определялся только по `replicas > 0`, а адрес — `cache.host`. Конфигурация «внешний хост + `cache.auth.enabled` + `replicas` по умолчанию» работала на `main` и ломалась: AMP слал пароль `<fullname>-redis-secret` на внешний хост, `cache.auth.password` игнорировался, NOTES писал «Valkey L2». Нарушало ограничение совместимости из `requirements.md`. Premise 1 верна, вывод из неё — нет.
- **Recommendation:** выбирать по `replicas > 0` **и** `cache.host` → Valkey чарта (короткое имя или префикс `<fullname>-redis.`), либо fail.
- **Disposition:** fix-here — `amp.cache.bundled` / `amp.cache.hostIsValkey` (@ `834ecbe`); env для этой конфигурации совпадает с `main` (проверено diff'ом рендеров нового и старого чарта); NOTES подсказывает `replicas: 0`. Тест — T7a.
- **Follow-up:** n/a

### B2 — проверка «кеша нет» видит только короткое имя
- **Severity:** nit
- **Location:** `helm/amp/templates/deployment.yaml:183`
- **Issue:** `replicas: 0` + `cache.host=<fullname>-redis.<ns>.svc.cluster.local` рендерился, AMP уходил в fallback.
- **Recommendation:** то же сравнение по префиксу.
- **Disposition:** fix-here — `amp.cache.hostIsValkey` (@ `834ecbe`). Тест — T7b.
- **Follow-up:** n/a

### A2 — `.Values.namespace` ≠ namespace релиза: AMP ссылается на Secret Valkey из другого namespace
- **Severity:** minor
- **Location:** `helm/amp/templates/deployment.yaml:5` vs `redis-secret.yaml:11`
- **Issue:** Deployment/`<fullname>-secrets` идут в `.Values.namespace | default .Release.Namespace`, объекты Valkey/PostgreSQL — в `.Release.Namespace`. При `namespace` ≠ релиза AMP теперь в `CreateContainerConfigError` (раньше деградировал). Расхождение по всему чарту, существовало до задачи.
- **Recommendation:** fail при расхождении или выровнять namespace во всех шаблонах.
- **Disposition:** defer-bug — общий дефект чарта, не этой задачи.
- **Follow-up:** BUGS `HELM-NAMESPACE-SPLIT` (finalize)

### A3 — GitOps без `lookup`: рассинхрон пароля стал видимым
- **Severity:** minor
- **Location:** `helm/amp/templates/redis-secret.yaml:29-35`
- **Issue:** в Argo CD каждый sync пишет новый пароль; после рестарта AMP или Valkey они расходятся. Задокументировано, по рендеру не обнаружить.
- **Recommendation:** TECH-DEBT; вариант — генерация один раз pre-install Job'ом.
- **Disposition:** defer-tech-debt
- **Follow-up:** TECH-DEBT `HELM-CHART-GAPS` (finalize)

### A4 — гонка старта AMP/Valkey: fallback навсегда (= B4)
- **Severity:** minor
- **Location:** `go-app/internal/application/service_registry.go:1203-1212`; `CHANGELOG.md` migration note 1
- **Issue:** известный `AMP-CACHE-NO-RECONNECT`; первый upgrade рестартует оба пода; AMP в fallback остаётся Ready, `--wait` проходит.
- **Recommendation:** проверить на k3d A1/A2; подсказка в NOTES на upgrade.
- **Disposition:** fix-here (NOTES на `.Release.IsUpgrade`, @ `834ecbe`, тест T7c) + defer to testing (k3d 5.2/5.3) → defer-bug `AMP-CACHE-NO-RECONNECT` (уже открыт)
- **Follow-up:** `BUGS.md` AMP-CACHE-NO-RECONNECT — дописать результат A2 (finalize)

### A5 — посылка про `lookup` неточна
- **Severity:** nit
- **Location:** `Spec.md` § Design Premises; CHANGELOG, README, values
- **Issue:** `--dry-run=server` (Helm ≥ 3.13) и Flux выполняют `lookup`; пуст он в `helm template`, клиентском `--dry-run`, Argo CD.
- **Recommendation:** уточнить формулировки.
- **Disposition:** fix-here — посылка переписана с классом `measured` (`helm template`) + `assumed` (остальное), доки уточнены (@ `834ecbe`)
- **Follow-up:** n/a

### A6 — пароль в argv проб Valkey
- **Severity:** nit
- **Location:** `helm/amp/templates/redis-statefulset.yaml:171,184,197`
- **Issue:** `redis-cli -a $REDIS_PASSWORD` виден в `ps`; существовало до диффа.
- **Recommendation:** `REDISCLI_AUTH`.
- **Disposition:** defer-backlog
- **Follow-up:** BACKLOG (finalize)

### A7 — контракт `--reuse-values`
- **Severity:** nit
- **Location:** n/a
- **Issue:** проблем не найдено (рендер нового чарта со старыми values — корректный `secretKeyRef`).
- **Recommendation:** n/a
- **Disposition:** reject (reason: не дефект, проверка пройдена)
- **Follow-up:** n/a

### A8 — новый fail ломает `helm upgrade` без upgrade note
- **Severity:** minor
- **Location:** `helm/amp/templates/deployment.yaml:183-185`; `CHANGELOG.md`
- **Issue:** конфигурация, которая рендерилась, теперь падает.
- **Recommendation:** строка в upgrade notes.
- **Disposition:** fix-here — п. 3 migration notes уже был; уточнён: «`helm upgrade` stops on these values», короткое и длинное имя (@ `834ecbe`)
- **Follow-up:** n/a

### B3 — пароль `valkey.password.existingSecret` с `\n` в конце
- **Severity:** minor
- **Location:** `helm/amp/values.yaml` (`valkey.password`), `redis-statefulset.yaml:120`
- **Issue:** init срезает `\n` (`$(cat …)`), AMP и exporter получают env с ним ⇒ Valkey здоров, AMP не авторизуется. Новый путь отказа.
- **Recommendation:** совет `printf '%s'` и для `valkey.password`.
- **Disposition:** fix-here (docs: `values.yaml`, README, @ `834ecbe`); поведение exporter — defer to testing (k3d), не блокирует
- **Follow-up:** n/a

### B5 — смена пароля Valkey через `helm upgrade` не рестартует поды
- **Severity:** minor
- **Location:** `helm/amp/templates/redis-statefulset.yaml:46-48`, `deployment.yaml:34`
- **Issue:** checksum есть только на ConfigMap и `<fullname>-secrets`; после смены `valkey.password.value` Valkey держит старый пароль, новые поды AMP берут новый. README требует `rollout restart`, шаблоны не предотвращают.
- **Recommendation:** аннотация `checksum/valkey-password` на оба pod template.
- **Disposition:** defer-tech-debt — ротация без ручного рестарта — non-goal задачи (`requirements.md`)
- **Follow-up:** TECH-DEBT `HELM-CHART-GAPS` (finalize)

### B6 — устаревшая запись в TECH-DEBT
- **Severity:** nit
- **Location:** `docs/06-planning/TECH-DEBT.md:21`
- **Issue:** «`replicas: 0` без `cache.host` оставляет AMP без кеша» — теперь fail.
- **Recommendation:** обновить.
- **Disposition:** fix-here (@ `834ecbe`)
- **Follow-up:** n/a

### B7 — баг объявлен закрытым до приёмки
- **Severity:** nit
- **Location:** `docs/06-planning/DECISIONS.md:302`
- **Issue:** ADR-017 «закрыт 2026-10-20» до k3d и тестов.
- **Recommendation:** править в finalize.
- **Disposition:** fix-here — правка ADR откатана, перенесена в шаг 6.1 (@ `834ecbe`)
- **Follow-up:** `tasks.md` 6.1

### B8 — предупреждение NOTES слишком узкое
- **Severity:** nit
- **Location:** `helm/amp/templates/NOTES.txt:39`
- **Issue:** нет предупреждения при одном `cache.auth.password`.
- **Recommendation:** расширить условие.
- **Disposition:** fix-here (@ `834ecbe`)
- **Follow-up:** n/a

## Anti-Pattern Check

- [x] Self-audit was not treated as a substitute for independent review.

## Round 2 — проверка исправлений (`697d100..834ecbe`)

**Reviewer perspective:** третий независимый агent (general-purpose), не видел вывода A и B.
**Result:** все fix-here раунда 1 — resolved (A1, B2, A4/NOTES, A5, A8, B3, B6, B7, B8), с рендер-доказательствами против чарта `main`; отложенные — признаны разумными.

### C1 — префикс `<fullname>-redis.` совпадает с Valkey в чужом namespace
- **Severity:** minor
- **Location:** `helm/amp/templates/_helpers.tpl` (`amp.cache.hostIsValkey`)
- **Issue:** `cache.host=t-amp-redis.otherns.svc.cluster.local` (одноимённый релиз в соседнем namespace) считался своим: с `replicas: 0` — fail (на `main` рендерилось), с `cache.auth.*` — пароль своего Valkey на чужой хост. Частный случай A1.
- **Recommendation:** своё — только `<svc>`, `<svc>.<Release.Namespace>` и префикс `<svc>.<Release.Namespace>.`.
- **Disposition:** fix-here — исправлено; проверено `-n prod`: `t-amp-redis`, `t-amp-redis.prod`, `t-amp-redis.prod.svc.cluster.local` → Secret Valkey; `t-amp-redis.otherns.svc.cluster.local`, `t-amp-redisx` → `t-amp-secrets`/`redis-password`, как на `main`; `replicas: 0` + `otherns` рендерится, + `prod` — fail. Тест — T7b.
- **Follow-up:** n/a

### C2 — сравнение хоста регистрозависимое
- **Severity:** nit
- **Location:** `helm/amp/templates/_helpers.tpl`
- **Issue:** `T-AMP-REDIS` не распознаётся как свой; на `main` поведение то же (точное сравнение не применялось вовсе).
- **Recommendation:** `lower` или оставить.
- **Disposition:** reject (reason: имена Service в Kubernetes — только нижний регистр, `cache.host` в верхнем регистре на практике не встречается; поведение не хуже `main`)
- **Follow-up:** n/a

### C3 — тесты T7a/T7b/T7c ещё не написаны
- **Severity:** nit
- **Location:** `helm/amp/tests/render-cache-auth.sh` (нет)
- **Issue:** `review-findings.md` ссылается на тесты, которых пока нет.
- **Recommendation:** написать до finalize, включая случай C1.
- **Disposition:** fix-here in write-tests (шаг 4.1 по порядку конвейера: тесты — после verdict `pass`)
- **Follow-up:** `tasks.md` 4.1

## Testing outcomes (deferred to testing, 2026-10-20)

Evidence: `evidence/k3d-acceptance.md`.

- **A4 / B4 — гонка старта AMP/Valkey:** подтверждено. Первый `helm upgrade` с `main` на ветку рестартует Valkey (новые `items` у volume) и AMP одновременно; AMP стартовал на 3 с раньше Valkey, получил `i/o timeout` и остался в fallback, будучи Ready. Подсказка NOTES (`rollout restart`) выведена и исправляет ситуацию; повторный upgrade и рестарт — `Redis cache initialized`. Не дефект этой задачи — `AMP-CACHE-NO-RECONNECT`; в finalize дописать туда воспроизведение (A2).
- **B3 — `\n` в конце пароля `valkey.password.existingSecret`:** подтверждено ровно как задокументировано: Valkey и пробы срезают перевод строки (под Ready), AMP получает `WRONGPASS` → fallback, exporter `redis_up 0`. С `printf '%s'` — AMP и exporter подключаются. Документации (`values.yaml`, README) достаточно; поведение не меняем.
- **A3 / B5** — не проверялись на k3d (GitOps без `lookup`, смена пароля Valkey): отложены в TECH-DEBT, проверка не требовалась.
