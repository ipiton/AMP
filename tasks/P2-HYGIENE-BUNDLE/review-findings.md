# Deep Review Findings: P2-гигиена (бандл)

**Trigger classification:** mandatory (`S` signal: supply chain release guard, Valkey password in argv)
**Reviewer perspective:** independent review agent (Claude, separate session from the implementer)
**Reviewed at:** 2026-11-14
**Reviewed tree:** bugfix/p2-hygiene-bundle @ 5f0a5b0f37898ad7dc36612600f950ee934da07a
**Verdict:** pass (see `review-verdict.json`)

Lenses: S (secret handling, supply chain), X (chart / CI / gate script boundaries), R (render change, rollout), correctness, premises, maintainability.

## Что проверено руками (без находок)

- **Рендер-тесты:** все 12 `helm/amp/tests/render-*.sh` зелёные (helm v4.3.2). Новые assert'ы дискриминируют: `render-single-node.sh` на чарте из `main` падает на обоих valkey-assert'ах (0 вместо 3 `REDISCLI_AUTH`, 3 вместо 0 `-a $`); `assert_count` использует `grep -F -e`, так что `-a $` не трактуется как опция/регэксп.
- **Совместимость при наличии CRD:** `helm template` чарта из `main` vs ветки с `--api-versions monitoring.coreos.com/v1` — 0 строк дифа для дефолтов и для `values-production.yaml` + placeholders; с `valkey.enabled=true` различаются только пробы (ожидаемо).
- **Хелпер `amp.monitoringCRDs`:** без CRD — 0 объектов мониторинга; `--api-versions monitoring.coreos.com/v1` — 4; только `v1alpha1` — 0; только `monitoring.coreos.com/v1/ServiceMonitor` (форма GVK) — 0 (Argo CD передаёт обе формы, GV и GVK, так что ок). `--set monitoring=null` рендерится; `dig ... (.Values.monitoring | default dict)` безопасен для `--reuse-values` со старыми values без `assumeCRDs`. Guard SM на `webConfig` без кредов срабатывает, когда SM рендерится (тест `render-servicemonitor.sh` с `CRDS`), и молчит без CRD — корректно, SM нет.
- **Пробы Valkey** (локальный `valkey/valkey:8.1.10-alpine`, `--network none`): `redis-cli` — симлинк на `valkey-cli`, `REDISCLI_AUTH` соблюдается; пароль с пробелом, `$`, `"`, обратной кавычкой, `'`, `;` → `PONG`; неверный пароль → `NOAUTH`, `grep PONG` = 1 (проба падает, как надо); пустой пароль при сервере без `requirepass` → `PONG` (+ `AUTH failed` в stderr, не влияет). YAML-скаляр `REDISCLI_AUTH="$REDIS_PASSWORD" redis-cli ping | grep PONG` валиден (plain scalar, рендер проверен). Exporter получает пароль через env `REDIS_PASSWORD`, других `-a`/argv с паролем в шаблонах нет.
- **`release.yml`:** `actions/checkout` с `fetch-depth: 0` тянет `+refs/heads/*:refs/remotes/origin/*`, так что `origin/main` есть; `persist-credentials: false` на fetch внутри шага не влияет. `${{ github.ref_name }}` идёт через `env`, `$GITHUB_SHA` — переменная окружения раннера: инъекции нет. Шаг стоит до `docker/login-action`. `.dockerignore` исключает `.git`, полная история в контекст сборки не попадает.
- **`go mod tidy -diff`:** на ветке rc=0 за ~0.4s, `go.mod`/`go.sum` не меняются (флаг `-diff` ничего не пишет). В CI `setup-go` с `go-version-file: go-app/go.mod` берёт `toolchain go1.27.3`; сеть в CI есть; GOFLAGS/vendor не используются.
- **`values-dev.yaml`:** дифф рендера `main` → ветка: только requests AMP 500m/512Mi → 100m/128Mi (limits 1000m/1Gi совпадают с дефолтами); `ingress.enabled: false` и `monitoring.prometheusEnabled: true` — дефолты `values.yaml`. Рендер с dev-values проходит.
- **`LLM-ERROR-BODY-CAP`:** уже исправлено в `0c48423` (`fix: retry LLM 429/5xx, ...`): `client.go:32` `maxErrorBodyBytes = 512`, `truncateBody` используется в `client.go:378,382` и `chat.go:98`; `investigate_with_tools.go:119-121` идёт через `doChat` (тот же `chat.go`). Других мест с телом ответа в логе/ошибке в пакете нет (`json.Unmarshal` ошибки тело не включают). Тест — `retry_classify_test.go:110-116`. Ссылки на строки в записи BACKLOG устарели. См. F5.

## Findings

### F1 — Документация всё ещё говорит, что креды ServiceMonitor обязательны при `webConfig` безусловно
- **Severity:** minor
- **Location:** `helm/amp/README.md:388`, `helm/amp/README.md:402`, `helm/amp/values-production.yaml:182-184`
- **Issue:** «**required** with `webConfig.existingSecret`», «renders only once you set ... `monitoring.serviceMonitor.basicAuth.secretName` (with `webConfig`)», «needs credentials or the render fails». После гейта guard срабатывает только когда кластер отдаёт `monitoring.coreos.com/v1` (или `assumeCRDs`). Оператор, проверяющий `values-production.yaml` через `helm template` без кластера, получит успешный рендер без кредов, а `helm install` на кластере с CRD — `fail`. Поведение правильное, текст — нет.
- **Recommendation:** дописать «when the ServiceMonitor renders (the cluster serves the CRDs or `monitoring.assumeCRDs: true`)» в трёх местах.
- **Disposition:** fix-here
- **Follow-up:** n/a

### F2 — CHANGELOG обещает, что пароль с пробелами больше не ломает пробы, но такой пароль ломает сам Valkey
- **Severity:** minor
- **Location:** `CHANGELOG.md` (`### Security`, P2-HYGIENE-BUNDLE); корень — `helm/amp/templates/redis-statefulset.yaml:120`
- **Issue:** init-контейнер пишет `echo "requirepass $(cat /tmp/secret/password)"` без кавычек. Проверено на `valkey/valkey:8.1.10-alpine`: `requirepass a b` → `FATAL CONFIG FILE ERROR ... wrong number of arguments`, `requirepass a"b` — тоже FATAL. С `valkey.password.existingSecret` пароль задаёт пользователь, так что сценарий реален; до проб дело не доходит. Утверждение в CHANGELOG формально верно для проб, но вводит в заблуждение. (Пароль также лежит в `/data/redis.conf` на PVC — предсуществующее, вне скоупа.)
- **Recommendation:** fix-here — убрать из CHANGELOG фразу про пробелы (или оговорить, что `requirepass` всё ещё не принимает пробелы/кавычки). defer-bug — экранировать пароль при записи `requirepass` (`"..."` с экранированием `\` и `"`) либо передавать `--requirepass` через env-ссылку в `command`; render-тест + прогон на образе.
- **Disposition:** fix-here (текст CHANGELOG) + defer-bug (кавычки в `requirepass`)
- **Follow-up:** новая запись в `docs/06-planning/BUGS.md` (VALKEY-REQUIREPASS-QUOTING) на finalize

### F3 — Гейт по group/version, а не по kind: частичный набор CRD ломает install
- **Severity:** minor
- **Location:** `helm/amp/templates/_helpers.tpl:194-196`
- **Issue:** `.Capabilities.APIVersions.Has "monitoring.coreos.com/v1"` истинно, если в группе есть хоть один CRD v1. Кластеры, где поставлены только `ServiceMonitor`/`PodMonitor` (Grafana Alloy / OpenTelemetry operator target allocator и т.п.), отдают `monitoring.coreos.com/v1` без `PrometheusRule` → `helm install` с `valkey.enabled` или bundled PostgreSQL падает `no matches for kind "PrometheusRule"` — тот же класс отказа, который задача закрывала. Для полного kube-prometheus-stack / `prometheus-operator-crds` не проявляется.
- **Recommendation:** отдельные хелперы по kind: `Has "monitoring.coreos.com/v1/ServiceMonitor"` / `.../PrometheusRule` OR `assumeCRDs`. Учесть, что задокументированный `--api-versions monitoring.coreos.com/v1` (только GV) тогда перестанет включать объекты — документировать форму GVK или принимать GV как «все kinds». Не блокирует: частичные установки редки, а до задачи такой кластер падал и на ServiceMonitor.
- **Disposition:** defer-backlog
- **Follow-up:** запись в `docs/06-planning/BACKLOG.md` (HELM-MONITORING-CRD-PER-KIND) на finalize

### F4 — `helm lint` и шаг `helm-values` release-gate больше не видят шаблоны мониторинга и guard SM в production
- **Severity:** minor
- **Location:** `scripts/release-gate.sh:205-208` (`step_helm_values`), `helm/amp/tests/values-production-placeholders.yaml:14-17`
- **Issue:** `helm lint` не имеет `--api-versions`, а `helm template` в `step_helm_values` (default/dev/production) и `step_helm_rbac` вызывается без него: ServiceMonitor/PrometheusRule и guard кредов SM для `values-production.yaml` в этих шагах больше не исполняются, placeholder `monitoring.serviceMonitor.basicAuth.secretName` для них стал мёртвым. Покрытие сохраняется в `render-servicemonitor.sh`/`render-postgresql.sh`/`render-single-node.sh` (с `--api-versions`), поэтому регрессия не пройдёт незамеченной — но комментарий в `step_helm_values` («the gate renders with placeholders so the guard's presence is exercised») для guard SM теперь неверен.
- **Recommendation:** добавить `--api-versions monitoring.coreos.com/v1` в `helm template` внутри `step_helm_values` (одна строка), чтобы production-рендер снова проходил через guard SM; `lint` оставить как есть.
- **Disposition:** fix-here
- **Follow-up:** n/a

### F5 — Критерий успеха по `LLM-ERROR-BODY-CAP` и планирование не отражены в диффе
- **Severity:** minor
- **Location:** `docs/06-planning/BACKLOG.md:32` (а также записи `HELM-MONITORING-CRD-GATE`, `RELEASE-TAG-ON-MAIN-GUARD`, `GO-MOD-TIDY-CHECK`, `VALKEY-PROBE-ARGV-PASSWORD`, `HELM-VALUES-DEV-ALIGN`)
- **Issue:** запись утверждает, что тело ошибки до 1 MiB идёт в лог, со ссылками `client.go:317-323, :419, :608`, `investigate_with_tools.go:126`. Это уже не так: исправлено в `0c48423` (см. «Что проверено»). Дифф BACKLOG не трогает — success criterion «сверён с кодом: закрыт как сделанный (с указанием коммита)» пока не выполнен.
- **Recommendation:** на finalize закрыть `LLM-ERROR-BODY-CAP` как сделанный в `0c48423` (`maxErrorBodyBytes = 512`, `truncateBody`, `client.go:378,382`, `chat.go:98`), остальные участники бандла — как сделанные в этой задаче.
- **Disposition:** fix-here (на шаге finalize)
- **Follow-up:** n/a

### F6 — Для Full tier нет `research.md` / `Spec.md` / `tasks.md`
- **Severity:** minor
- **Location:** `tasks/P2-HYGIENE-BUNDLE/`
- **Issue:** Risk Profile — Full, `requirements.md` ссылается на «порядок фаз — по `tasks.md`», но в workspace только `requirements.md`. Design Premises для ревью взяты из «Research (mini)» в requirements. Сама реализация от этого не страдает (все премиссы проверены выше и верны, кроме неполноты F3), но пайплайн Full пропустил шаги.
- **Recommendation:** либо добавить краткие `tasks.md`/`Spec.md` (премиссы + шаги), либо явно записать в requirements, что бандл P2 идёт по облегчённой схеме с обоснованием.
- **Disposition:** fix-here
- **Follow-up:** n/a

### F7 — `monitoring.assumeCRDs` как строка `"false"` трактуется как true
- **Severity:** nit
- **Location:** `helm/amp/templates/_helpers.tpl:195`
- **Issue:** `--set-string monitoring.assumeCRDs=false` (или `"false"` в values из шаблонизатора) — непустая строка, `or` даёт true, рендерятся 4 объекта (проверено). `--set`/YAML bool работают правильно.
- **Recommendation:** `eq (toString (dig "assumeCRDs" false ...)) "true"`.
- **Disposition:** defer-backlog
- **Follow-up:** вместе с F3

### F8 — Комментарий «a refused tag never holds the push token» точен только для логина в реестр
- **Severity:** nit
- **Location:** `.github/workflows/release.yml:43-46`, `:27-29`
- **Issue:** `packages: write` выдан на уровне job, так что `GITHUB_TOKEN` с этим правом существует с первого шага; отложен лишь `docker login`. Практически риск нулевой (guard-шаг токен не трогает), но формулировка обещает больше.
- **Recommendation:** уточнить комментарий или (сильнее) вынести проверку в отдельный job `verify` с `contents: read` и `needs: verify` у `publish` — в рамках `CI-SUPPLY-CHAIN`.
- **Disposition:** defer-backlog
- **Follow-up:** `CI-SUPPLY-CHAIN` в `BACKLOG.md`

### F9 — Migration note не называет kustomize `helmCharts` и `kubectl apply --prune`
- **Severity:** nit
- **Location:** `CHANGELOG.md` (`### Breaking changes / migration notes`)
- **Issue:** kustomize `helmCharts` (и любой `helm template | kubectl apply`) рендерит без capabilities кластера; при `--prune`/GitOps с pruning существующие ServiceMonitor/PrometheusRule будут удалены, а не просто «не созданы».
- **Recommendation:** одна фраза: «kustomize `helmCharts`: set `apiVersions: [monitoring.coreos.com/v1]`; with pruning the existing objects are deleted otherwise».
- **Disposition:** fix-here
- **Follow-up:** n/a

## Anti-Pattern Check

- [x] Self-audit was not treated as a substitute for independent review.

## Disposition (после ревью, 2026-11-14)

| Находка | Решение |
|---|---|
| F1 | fixed: `helm/amp/README.md` (таблица, `values-production.yaml`-абзац), комментарий в `values-production.yaml` — креды нужны, когда SM рендерится |
| F2 | fixed (формулировка CHANGELOG); экранирование `requirepass` — BUGS `VALKEY-REQUIREPASS-QUOTING` |
| F3 | follow-up: BACKLOG P2 `HELM-MONITORING-CRD-PER-KIND` |
| F4 | fixed: `step_helm_values` рендерит с `--api-versions monitoring.coreos.com/v1`; prod/dev прогнаны |
| F5 | fixed на finalize: участники бандла удалены из BACKLOG § P2, исход — `DONE.md` |
| F6 | fixed: `tasks.md` с фазами и обоснованием, почему research/spec свёрнуты в requirements |
| F7 | follow-up: BACKLOG P2 `HELM-MONITORING-CRD-PER-KIND` (вместе с F3: `kindIs "bool"`) |
| F8 | fixed: комментарий в `release.yml` уточнён; сужение прав job — `CI-SUPPLY-CHAIN` |
| F9 | fixed: migration note в CHANGELOG — kustomize `helmCharts` `apiVersions`, `--prune` |
