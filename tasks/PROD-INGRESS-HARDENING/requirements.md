---
id: PROD-INGRESS-HARDENING
slug: prod-ingress-hardening
stream: Security
type: feature
priority: critical
status: active
created_at: 2026-10-01
updated_at: 2026-10-01
---

# Requirements: Закрыть внешний и внутрикластерный доступ к AMP в прод-профиле

## Problem Framing

- **Symptom:** `helm install -f values-production.yaml` публикует весь HTTP API AMP (`POST /api/v2/silences`, `POST /-/reload`, investigation, dashboard) через Ingress на `/` (`helm/amp/values-production.yaml:337`) без auth-аннотаций и allowlist. Auth в процессе (PROD-AUTH, `webConfig.existingSecret`) в прод-профиле не включён, поэтому по умолчанию API анонимен снаружи. Внутри кластера к поду AMP может прийти кто угодно: NetworkPolicy есть только для postgres/redis (`postgresql-networkpolicy.yaml`, `redis-networkpolicy.yaml`), и обе выключены по умолчанию.
- **Root Cause:** прод-профиль собран до PROD-AUTH: Ingress включён «как в примере», вопрос «чем он защищён» не решался; NetworkPolicy для самого приложения не заводилась.
- **Why Now:** прод-блокер P0 Security (BACKLOG, аудит 2026-09-21); после PROD-AUTH механизм auth есть, осталось сделать его обязательным/неизбежным в прод-профиле. Блокирует заявление «production-ready» и Known Gaps релиза `v0.1.0`.
- **How We Measure:** `helm template -f values-production.yaml` без настроенного auth либо не рендерит открытый Ingress, либо падает с понятным сообщением; с настроенным auth — рендерит; при включённой NetworkPolicy под AMP принимает трафик только с перечисленных источников (Ingress-контроллер, Prometheus, Alertmanager-клиенты/Prometheus-отправители, реплики AMP). Проверки — render-тесты в `helm/amp/tests/`.

## Risk Profile

- **Signals:** `S C R`
  - `S` — экспозиция API наружу, обязательность auth, сетевая изоляция пода.
  - `C` — контракт чарта: прод-профиль начнёт требовать `webConfig.existingSecret` (или явный opt-out), новые values `networkPolicy.*` для приложения; breaking для тех, кто ставит `values-production.yaml` как есть.
  - `R` — меняется поведение прод-деплоя (что рендерится, какой трафик проходит).
- **Tier:** Full
- **Notes:** любой `S` ⇒ Full. Go-код, скорее всего, не меняется — только чарт, тесты рендера, документация.

## User Stories

1. Как оператор, ставящий AMP по `values-production.yaml`, я хочу, чтобы чарт не позволил случайно выставить анонимный API в интернет, чтобы забытая настройка auth не превращалась в «любой может заглушить все алерты».
2. Как оператор, я хочу NetworkPolicy для подов AMP с понятными точками расширения (откуда приходят алерты, кто скрейпит метрики), чтобы компрометация соседнего пода не давала доступ к API в обход Ingress.
3. Как оператор с prometheus-operator, я хочу, чтобы ServiceMonitor из чарта реально скрейпил AMP и работал с включённым auth, чтобы прод-профиль с обязательным auth не оставлял меня без метрик.
4. Как оператор с собственным auth-proxy (oauth2-proxy, basic-auth аннотации Ingress), я хочу явный и документированный способ сказать чарту «защиту обеспечиваю я», не отключая проверку молча.

## Success Criteria

- [ ] Прод-профиль не рендерит Ingress, открытый без защиты: решение (fail при пустом `webConfig.existingSecret` / Ingress выключен по умолчанию / явный opt-out) выбрано на `/spec` и зафиксировано в ADR.
- [ ] NetworkPolicy для подов AMP в чарте: ingress-правила на `http` и `metrics` порты с настраиваемыми источниками; включена в `values-production.yaml`; egress-политика — решение на `/spec`.
- [ ] Render-тест(ы) в `helm/amp/tests/` покрывают: прод-профиль без auth, с auth, с opt-out; NetworkPolicy вкл/выкл и источники.
- [ ] Bundled ServiceMonitor AMP рабочий: рендерится только при `monitoring.prometheusEnabled` и `monitoring.serviceMonitor.enabled`, селектит именно Service AMP, поддерживает `basicAuth` из Secret; при включённом `webConfig` без `basicAuth` — `fail` (скрейп, который всегда получает 401). _(Включено решением владельца 2026-10-01.)_
- [ ] `scripts/release-gate.sh` зелёный (helm lint/template, RBAC-шаг).
- [ ] `CONFIGURATION_GUIDE.md` / `helm/amp/README.md` описывают прод-требование и NetworkPolicy; `CHANGELOG.md` `[Unreleased]` — запись с migration notes (breaking для `values-production.yaml`).

## Non-Goals

- Изменения Go-кода auth (PROD-AUTH закрыт), bearer-токены (`PROD-AUTH-BEARER`).
- TLS в процессе AMP — терминируется на Ingress/mesh.
- Интеграция oauth2-proxy как сабчарта; только документированный путь через аннотации/opt-out.
- `CONFIG-RELOADER-AUTH` и включение `configReloader` в проде.
- `PROD-SECURITY-MD` (отдельный блокер, ~0.25d) — только если окажется, что без него доки противоречат результату.
- Переделка sticky-session аннотаций в `ingress.yaml` (nginx-специфичны, добавляются ко всем Ingress) — максимум запись в TECH-DEBT.

## Constraints

- **Scope:** `helm/amp/` (templates включая `servicemonitor.yaml`/`service.yaml`, values, values-production, tests, README), `docs/CONFIGURATION_GUIDE.md`, `CHANGELOG.md`, `docs/06-planning/DECISIONS.md`.
- **Security:** fail-closed: отсутствие настройки не должно давать открытый Ingress в прод-профиле. Секреты (web-config, basic-auth для Ingress) — только через `existingSecret`, не через values в открытом виде.
- **Compatibility:** дефолтный `values.yaml` (pilot/dev) не ломается — Ingress там выключен. `values-production.yaml` — breaking, нужен migration note. NetworkPolicy должна не ломать: HA-реплики AMP друг с другом (если общаются напрямую), Prometheus-скрейп `/metrics`, отправку алертов от Prometheus, probes (kubelet), config-reloader sidecar (тот же под — не затрагивается).

## Discovery Notes

- Similar tasks: `tasks/archive/PROD-AUTH/` (Spec §Non-Goals явно отдаёт сюда «Ingress-аннотации, NetworkPolicy, обязательность auth в `values-production.yaml`»; research §«Решения для `/spec`»), `tasks/archive/PROD-RBAC-SCOPE/` (паттерн render-тестов + шаг `helm-rbac` в release-gate, ADR-012).
- Relevant patterns: `helm/amp/templates/postgresql-networkpolicy.yaml`, `redis-networkpolicy.yaml` (селектор Prometheus захардкожен как `namespaceSelector name: monitoring` + `app: prometheus` — вероятно, не совпадает с реальными кластерами, учесть при выборе формата источников); `deployment.yaml:37` — `fail` как прецедент запрета несовместимой конфигурации; `helm/amp/tests/render-*.sh` — формат render-тестов.
- Попутное: `ingress.yaml` безусловно добавляет nginx sticky-session аннотации (`affinity: cookie`, `upstream-hash-by`) к любому Ingress с аннотациями.
- Open unknowns (для `/research`):
  - Как чарт отличает «прод-профиль»: `values-production.yaml` — просто файл values; есть ли `profile`/`environment`-ключ, на который можно повесить `fail`, или проверку надо делать на уровне самого Ingress (`ingress.enabled && !webConfig.existingSecret && !ingress.authProvidedExternally` ⇒ fail во всех профилях?).
  - Общаются ли реплики AMP напрямую (gossip/cluster-порт) или только через Redis/Postgres — определяет ingress-правило «AMP ↔ AMP».
  - Egress: AMP ходит в Slack/PagerDuty/webhooks/LLM/K8s API на произвольные адреса — default-deny egress практически не настраиваем; вероятно, только ingress-политика + DNS.
  - Какие источники алертов типичны (Prometheus в другом namespace, vmalert) и как их выразить в values без хардкода `name: monitoring`.
