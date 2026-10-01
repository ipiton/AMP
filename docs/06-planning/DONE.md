# DONE

Закрытые срезы и проверенные итоги **текущего месяца**. Прошлые месяцы — в `archive/DONE-YYYY-MM.md`. Проверка «закрыт ли slug» читает этот файл **и** все архивы:

```bash
grep -rn "SLUG" docs/06-planning/DONE.md docs/06-planning/archive/DONE-*.md
```

| Архив | Период |
|---|---|
| `archive/DONE-2026-09.md` | 2026-09 |
| `archive/DONE-2026-08.md` | 2026-08 |
| `archive/DONE-2026-05.md` | 2026-05 |
| `archive/DONE-2026-04.md` | 2026-04 |
| `archive/DONE-2026-03.md` | 2026-03 |
| `archive/DONE-2026-02.md` | 2026-02 |

## 2026-10

- 2026-10-01 — (TASK / Security, P0) **PROD-INGRESS-HARDENING** — чарт больше не публикует API через Ingress без auth, у pod'ов AMP появилась своя NetworkPolicy, ServiceMonitor заработал (ADR-015).
  - **Ingress guard** (`templates/ingress.yaml`): `ingress.enabled: true` рендерится только с `webConfig.existingSecret` или `ingress.externalAuth: true` (принимается только `true`/`"true"`). Привязан к Ingress, а не к прод-профилю: действует для любых values.
  - **NetworkPolicy AMP** (`templates/networkpolicy.yaml`, `networkPolicy.*`): ingress-only, порт `http`, правило на роль — `ingressController` (только при `ingress.enabled`) / `alertSenders` / `metricsScrapers` + сырой `extraIngress`; без источников — `fail`. Egress не ограничен. Включена в `values-production.yaml`, по умолчанию выключена. Метка `app.kubernetes.io/component: application` на pod template и metadata Service, селекторы не тронуты; `podLabels` не может её перекрыть. Попутно ожила политика redis (`valkey.networkPolicy`), которая ждала эту метку.
  - **ServiceMonitor**: раньше рендерился всегда и селектил метки, которых у Service не было. Теперь гейт `monitoring.prometheusEnabled` + `monitoring.serviceMonitor.enabled`, селектор только на Service AMP, `basicAuth` из Secret; с `webConfig` без кредов — `fail`.
  - **Breaking:** `values-production.yaml` не рендерится, пока не заданы auth, креды ServiceMonitor (с `webConfig`) и источник NetworkPolicy; первый upgrade — через `-f`, не `--reuse-values` (миграция в `CHANGELOG.md`). Шаблоны nil-safe к values старого релиза.
  - **Гейт:** шаг `helm-tests` в `scripts/release-gate.sh` (закрыт `HELM-RENDER-TEST-IN-GATE`), prod-рендер гейта — через `helm/amp/tests/values-production-placeholders.yaml`; `.helmignore` с `/tests/`.
  - **Проверка:** `deep-review` (mandatory, `S C R`) двумя независимыми агентами — 17 находок, 2 major (`--reuse-values` → nil pointer и пропавший ServiceMonitor; `podLabels` снимает метку), `fix_required`; после фиксов повторное независимое ревью подтвердило их и нашло R18 (неякорный `tests/` в `.helmignore` выкидывал hook `helm test`) — исправлено, verdict `pass` @ `093f8a7`. Новые `render-ingress-auth.sh` (16), `render-networkpolicy.sh` (29), `render-servicemonitor.sh` (21) assertions, существующие два адаптированы; мутации 15/15 пойманы. `scripts/release-gate.sh` 11/11 PASS (race 391s).
  - **Осознанные ограничения:** живого кластера не было (kind не установлен) — kubelet probes через политику (premise `assumed`), реальное отсечение и kill-switch не проверены вживую; в README — двусторонняя smoke-проверка для оператора. `service.type` NodePort/LoadBalancer guard'ом не покрыт; `externalAuth` + `ingressController` пускает любой Ingress того же контроллера (README рекомендует `webConfig`).
  - Документация: `helm/amp/README.md` § Network Exposure, `docs/CONFIGURATION_GUIDE.md` §4, `CHANGELOG.md` (Security + migration notes), `helm/amp/CHANGELOG.md`, ADR-015. Follow-ups: BUGS `SERVICE-METRICS-PORT-MISROUTED`, `MONITORING-CRD-DEFAULT`, `HELM-NAMESPACE-OVERRIDE-SPLIT`; BACKLOG `SERVICE-TYPE-EXPOSURE-GUARD`, дополнены `CONFIG-RELOADER-SIDECAR` (R12), `CONFIG-RELOADER-AUTH` (R13); TECH-DEBT `RELEASE-GATE-UNQUOTED-ARGS`, дополнен `HELM-CHART-GAPS` (R17).
  - Ветка `feature/prod-ingress-hardening`, workspace `tasks/archive/PROD-INGRESS-HARDENING/`. ~1d.
