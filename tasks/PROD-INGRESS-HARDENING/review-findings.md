# Deep Review Findings: Закрыть внешний и внутрикластерный доступ к AMP в прод-профиле

**Trigger classification:** mandatory (`S`; 3 сигнала `S C R`)
**Reviewer perspective:** два независимых агента без доступа к рассуждениям автора и друг к другу. Reviewer A — security / premises / correctness, Reviewer B — contract / rollout / gate. Автор сводил и воспроизвёл R1, R2, R3 рендером.
**Reviewed at:** 2026-10-01
**Reviewed tree:** feature/prod-ingress-hardening @ d64390c; повторное ревью фиксов (независимый агент) — @ c5a03f7, R1–R8, R10, R11, R16 подтверждены, новые R18–R20
**Verdict:** fix_required (см. `review-verdict.json`)

Поле `Status` в каждой находке читает скрипт verdict'а: `open` | `fixed` | `deferred` | `rejected`.

## Findings

### R1 — `helm upgrade --reuse-values` со старого релиза: nil pointer, а в обходе — молча пропавший ServiceMonitor
- **Severity:** major
- **Location:** `helm/amp/templates/networkpolicy.yaml:1`, `helm/amp/templates/servicemonitor.yaml:1-2`; команды в `helm/amp/README.md` (Network Exposure), `helm/amp/values.yaml` (kill-switch), `docs/ROLLBACK_RUNBOOK.md:140`
- **Issue:** с `--reuse-values` helm берёт values релиза вместо дефолтов нового чарта, и ключей `networkPolicy` и `monitoring.serviceMonitor` там нет. Рендер падает: `networkpolicy.yaml:1:14 nil pointer evaluating interface {}.enabled`. Если добавить `--set networkPolicy.enabled=false`, `$sm.enabled` оказывается nil: ServiceMonitor AMP не рендерится, helm удаляет старый, а guard basicAuth не выполняется. Воспроизведено: чарт ветки со значениями `main:helm/amp/values.yaml`. Нашли оба ревьюера независимо.
- **Recommendation:** nil-safe чтение (`dig` с дефолтами, как в `values.yaml`). В migration notes — первый upgrade через `-f` или `--reset-then-reuse-values`.
- **Disposition:** fix-here
- **Status:** fixed
- **Follow-up:** n/a

### R2 — `podLabels` перетирает `app.kubernetes.io/component`, и NetworkPolicy не выбирает ни одного pod'а
- **Severity:** major
- **Location:** `helm/amp/templates/deployment.yaml:28-31`
- **Issue:** `podLabels` рендерится после метки component. При дублирующемся ключе выигрывает последний, поэтому `podLabels: {app.kubernetes.io/component: x}` снимает метку молча. Политика AMP после этого не выбирает ни одного pod'а: API открыт внутри кластера, хотя оператор считает его закрытым. ServiceMonitor и `from` политики redis тоже теряют AMP. Воспроизведено: в отрендеренном pod template оба ключа подряд.
- **Recommendation:** `fail`, если `podLabels` содержит этот ключ.
- **Disposition:** fix-here
- **Status:** fixed
- **Follow-up:** n/a

### R3 — `ingress.externalAuth: "false"` (строка) проходит guard
- **Severity:** minor
- **Location:** `helm/amp/templates/ingress.yaml:4`
- **Issue:** проверка по truthiness. `--set-string ingress.externalAuth=false`, `"false"` или `"no"` в values-файле рендерят Ingress без auth. Воспроизведено.
- **Recommendation:** принимать только `true` (bool или строку `"true"`), всё остальное считать «нет».
- **Disposition:** fix-here
- **Status:** fixed
- **Follow-up:** n/a

### R4 — Заявление CHANGELOG «the chart no longer exposes an unauthenticated API» сильнее реализации
- **Severity:** minor
- **Location:** `CHANGELOG.md` `[Unreleased]` → Security
- **Issue:** guard смотрит только на Ingress. С анонимным API по-прежнему рендерятся: `service.type: NodePort/LoadBalancer` (`values-dev.yaml` — NodePort без auth), дефолты внутри кластера, prod с `externalAuth: true` и `networkPolicy.enabled: false`.
- **Recommendation:** переформулировать («an Ingress no longer…»); в README — оговорка про `service.type`. Guard на `service.type` — в BACKLOG.
- **Disposition:** fix-here (формулировка, README); guard `service.type` — defer-backlog
- **Status:** fixed
- **Follow-up:** BACKLOG `SERVICE-TYPE-EXPOSURE-GUARD` (finalize)

### R5 — `externalAuth` + `ingressController` пускает любой Ingress того же контроллера
- **Severity:** minor
- **Location:** `helm/amp/README.md` → Network Exposure
- **Issue:** политика пускает pod'ы контроллера, а не конкретный Ingress. Другой Ingress на том же контроллере может дойти до AMP без auth-аннотаций: в том же namespace или через `ExternalName` Service, если ingress-nginx запущен без `--disable-svc-external-name`.
- **Recommendation:** описать в README; рекомендовать `webConfig` как контроль, который от контроллера не зависит.
- **Disposition:** fix-here
- **Status:** fixed
- **Follow-up:** n/a

### R6 — Миграционные заметки не называют общий случай `webConfig` + ServiceMonitor
- **Severity:** minor
- **Location:** `CHANGELOG.md` → Breaking changes
- **Issue:** любой install с `webConfig.existingSecret` и дефолтным `monitoring.prometheusEnabled: true`, не только prod, теперь падает на рендере.
- **Recommendation:** добавить пункт.
- **Disposition:** fix-here
- **Status:** fixed
- **Follow-up:** n/a

### R7 — Smoke-проверка в README неточна
- **Severity:** minor
- **Location:** `helm/amp/README.md` → Network Exposure
- **Issue:**
  - Имя Service — `amp.fullname`, это `<release>-amp`, если имя релиза не содержит `amp`.
  - CNI может отвечать reject, а не drop, поэтому «must time out» слишком узко.
  - Нет позитивной проверки: политика, закрывающая всё, тоже «проходит».
- **Recommendation:** имя через `kubectl get svc -l app.kubernetes.io/component=application`; «must fail»; добавить проверку из разрешённого источника.
- **Disposition:** fix-here
- **Status:** fixed
- **Follow-up:** n/a

### R8 — Сигналы потерь только для Prometheus; hostNetwork-контроллер не описан
- **Severity:** minor
- **Location:** `helm/amp/README.md`, `CHANGELOG.md` Breaking п. 3
- **Issue:** у vmalert метрика другая (`vmalert_alerts_send_errors_total`). ingress-nginx в `hostNetwork` приходит с IP нод, и `namespaceSelector` его не совпадёт.
- **Recommendation:** добавить метрику vmalert и случай hostNetwork → `ipBlock`.
- **Disposition:** fix-here
- **Status:** fixed
- **Follow-up:** n/a

### R9 — Шаг `helm-tests` красный на HEAD
- **Severity:** minor
- **Location:** `scripts/release-gate.sh` (`helm-tests`), `helm/amp/tests/render-config-reloader.sh:115-117`, `render-image-tag.sh:68-70`
- **Issue:** известно и запланировано как 5.0. Ревьюер B: в 5.0 добавить кейс «prod без placeholder'ов → fail», чтобы guard'ы проверялись, а не только обходились. Запись `HELM-RENDER-TEST-IN-GATE` закрыть в BACKLOG.
- **Recommendation:** 5.0 в `write-tests` + негативный кейс; закрыть запись в BACKLOG на finalize.
- **Disposition:** fix-here (write-tests 5.0, finalize)
- **Status:** deferred
- **Follow-up:** `tasks.md` 5.0, 6.3

### R10 — ADR-015 ссылается на BUGS-записи, которых ещё нет
- **Severity:** minor
- **Location:** `docs/06-planning/DECISIONS.md` (ADR-015, «Следствие»)
- **Issue:** `SERVICE-METRICS-PORT-MISROUTED`, `MONITORING-CRD-DEFAULT` есть только в ADR. Сам дефект misrouted ревьюер подтвердил.
- **Recommendation:** завести записи. План — `finalize` 6.3; переносится в эту фазу, чтобы ссылки не висели.
- **Disposition:** fix-here
- **Status:** fixed
- **Follow-up:** `docs/06-planning/BUGS.md`

### R11 — Чарт пакует `tests/` вместе с placeholder-файлом
- **Severity:** nit
- **Location:** `helm/amp/` (нет `.helmignore`)
- **Issue:** `helm package` включит `tests/values-production-placeholders.yaml` («never use for a real install»).
- **Recommendation:** `.helmignore` с `tests/`.
- **Disposition:** fix-here
- **Status:** fixed
- **Follow-up:** n/a

### R12 — Политика закрывает порт `reloader` (9091) sidecar'а для скрейпа
- **Severity:** minor
- **Location:** `helm/amp/templates/networkpolicy.yaml`
- **Issue:** с `configReloader.enabled` его `/metrics` недоступен PodMonitor'у. Сейчас чарт этот порт не скрейпит, sidecar в проде выключен и несовместим с `webConfig`.
- **Recommendation:** при включении sidecar'а открыть `reloader` для `metricsScrapers`.
- **Disposition:** defer-backlog
- **Status:** deferred
- **Follow-up:** BACKLOG `CONFIG-RELOADER-SIDECAR` (дописать на finalize)

### R13 — Placeholder'ы гейта закрепляют путь `webConfig`; ветка `externalAuth` гейтом не рендерится
- **Severity:** minor
- **Location:** `helm/amp/tests/values-production-placeholders.yaml`
- **Issue:** когда прод включит config-reloader (несовместим с `webConfig`), гейт сломается. Ветка `externalAuth` покрыта только render-тестом 5.1.
- **Recommendation:** учесть в `CONFIG-RELOADER-AUTH`.
- **Disposition:** defer-tech-debt
- **Status:** deferred
- **Follow-up:** TECH-DEBT / BACKLOG `CONFIG-RELOADER-AUTH` (finalize)

### R14 — Неквотированный путь placeholder'ов в гейте
- **Severity:** nit
- **Location:** `scripts/release-gate.sh:171,191`
- **Issue:** путь с пробелом сломает `-f $HELM_CHART_DIR/...`. Предсуществующий паттерн (`args="-f $HELM_CHART_DIR/values-dev.yaml"`).
- **Recommendation:** перейти на bash-массивы.
- **Disposition:** defer-tech-debt
- **Status:** deferred
- **Follow-up:** TECH-DEBT (finalize)

### R15 — Пиры неверной формы рендерятся
- **Severity:** nit
- **Location:** `helm/amp/templates/networkpolicy.yaml`
- **Issue:** `alertSenders` объектом или строкой рендерится как есть.
- **Disposition:** reject (reason: API server отклоняет такой объект при apply, то есть fail-closed; валидация схемы в шаблоне дублирует его)
- **Status:** rejected
- **Follow-up:** n/a

### R16 — `namespace` override разносит AMP и redis-объекты по namespace
- **Severity:** nit
- **Location:** `redis-*.yaml` (`.Release.Namespace`) vs app (`.Values.namespace`); README «release namespace» для Secret ServiceMonitor
- **Issue:** предсуществующее: при `namespace` ≠ release redis и его политика в другом namespace. Формулировка README про Secret неточна: он должен быть в namespace AMP.
- **Recommendation:** README — «namespace AMP»; само расхождение — BUGS.
- **Disposition:** fix-here (формулировка README); расхождение — defer-bug
- **Status:** fixed
- **Follow-up:** BUGS `HELM-NAMESPACE-OVERRIDE-SPLIT` (finalize)

### R17 — Предсуществующее: сабчарт valkey с deny-all политикой, хардкод `name: monitoring`/`name: kube-system`
- **Severity:** nit
- **Location:** `charts/valkey`, `postgresql-networkpolicy.yaml`, `redis-networkpolicy.yaml`
- **Disposition:** defer-tech-debt
- **Status:** deferred
- **Follow-up:** TECH-DEBT `HELM-CHART-GAPS` (дописать на finalize)

### R18 — Неякорный `tests/` в `.helmignore` выкидывает hook `helm test`
- **Severity:** minor
- **Location:** `helm/amp/.helmignore` (c5a03f7)
- **Issue:** шаблон совпадает на любой глубине и убирает `templates/tests/postgresql-test-connection.yaml` из пакета и из `helm template`. Найдено повторным ревью фиксов, воспроизведено.
- **Recommendation:** `/tests/`; render-тест на наличие hook'а.
- **Disposition:** fix-here
- **Status:** fixed
- **Follow-up:** `tasks.md` 5.2b

### R19 — Smoke-проверка: порт и namespace «чужого» pod'а захардкожены
- **Severity:** nit
- **Location:** `helm/amp/README.md` → Network Exposure
- **Issue:** `8080` — дефолт `service.port`; `-n default` неверен, если `default` в списке источников.
- **Recommendation:** оговорить оба.
- **Disposition:** fix-here
- **Status:** fixed
- **Follow-up:** n/a

### R20 — Совет `ipBlock` для hostNetwork-контроллера зависит от CNI
- **Severity:** nit
- **Location:** `helm/amp/README.md`, `CHANGELOG.md`
- **Issue:** на Cilium трафик нод идёт с identity `host`/`remote-node`, CIDR-правило его может не выбрать.
- **Recommendation:** пометить как CNI-зависимое, проверять smoke-проверкой.
- **Disposition:** fix-here
- **Status:** fixed
- **Follow-up:** n/a

## Premises

Ревьюер A подтвердил все premises Spec.md, кроме одной: «никто не рендерит чарт с Ingress, кроме гейта» верна, но `docs/ROLLBACK_RUNBOOK.md:140` предписывает `--reuse-values`, отсюда R1. Класс `call-path-traced` этой premise заслужен лишь частично: поиск шёл по рендерам, но не по командам upgrade. Premise про kubelet probes правильно оставлена `assumed`.

## Anti-Pattern Check

- [x] Self-audit was not treated as a substitute for independent review.
