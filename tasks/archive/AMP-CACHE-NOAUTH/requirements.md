---
id: AMP-CACHE-NOAUTH
slug: amp-cache-noauth
stream: Helm
type: bug
priority: high
status: active
created_at: 2026-10-20
updated_at: 2026-10-20
---

# Requirements: AMP авторизуется в кеше чарта на дефолтах

## Problem Framing

- **Symptom:** на свежей установке и после upgrade с дефолтными values лог AMP — `NOAUTH Authentication required` → `Redis cache unavailable, falling back to in-memory cache` (k3d 2026-10-19, `tasks/archive/HELM-SINGLE-NODE-DEFAULTS/evidence/cache-noauth.txt`). Под кеша зелёный, `redis_up 1` — проблема не видна без чтения лога AMP.
- **Root Cause:** две несогласованные половины чарта. `templates/redis-statefulset.yaml` всегда запускает Valkey с `requirepass` из `<fullname>-redis-secret` (ключ `password`; или `valkey.password.existingSecret` / `valkey.password.secretKey`). `templates/deployment.yaml:175` передаёт AMP `REDIS_PASSWORD` только при `cache.auth.enabled` (по умолчанию `false`), и то из `<fullname>-secrets` / `redis-password` — значение `cache.auth.password`, предназначенное для внешнего кеша. Для встроенного кеша пароль AMP не получает никогда, если оператор не продублирует его руками.
- **Why Now:** таймеры групп, nflog и кеш инхибиции живут в памяти пода: рестарт AMP — повтор уведомлений, реплики (HPA) не делят дедуп. Заявление «состояние в Valkey» (README чарта, ADR-017) на дефолтах неверно. Bug high, первая задача Operations в Queue.
- **How We Measure:** render-тест — на дефолтах у контейнера AMP есть `REDIS_PASSWORD` со `secretKeyRef` на тот же Secret/ключ, что у Valkey; на k3d после `helm install` с дефолтами в логе AMP нет `falling back to in-memory cache`, есть признак подключения к кешу.

## Risk Profile

- **Signals:** `C S R`
  - `S` — проводка учётных данных (Secret → env контейнера AMP), новый путь для пароля внешнего кеша через `existingSecret`.
  - `R` — поведение рантайма меняется на дефолтах: AMP начинает реально писать таймеры/nflog/инхибицию в Valkey (сеть на горячем пути группировки и нотификаций).
  - `C` — добавлен на spec (2026-10-20): контракт values меняется — `cache.auth.*` действует только для внешнего кеша, новые ключи `cache.auth.existingSecret*`, два новых отказа рендера (`Spec.md` § API Contracts).
- **Tier:** Full
- **Notes:** `S` форсирует Full. `deep-review` обязателен; сигнал `R` в AMP закрывается записью в `CHANGELOG.md` `[Unreleased]` (deploy нет, см. `WORKFLOW.md`).

## User Stories

1. Как оператор, ставящий чарт с дефолтами, я хочу, чтобы AMP сам подключался к Valkey чарта, чтобы рестарт пода и несколько реплик не давали повторных уведомлений.
2. Как оператор с внешним Redis-совместимым кешем (`values-production.yaml`), я хочу передать пароль через существующий Secret, а не открытым значением в values, чтобы не держать креды в git/values.
3. Как оператор, я хочу видеть в логе/метриках, к какому кешу подключился AMP, чтобы fallback на in-memory не был молчаливым.

## Success Criteria

- [x] Встроенный кеш (standard-профиль, Valkey чарта): контейнер AMP получает `REDIS_PASSWORD` из того же Secret и ключа, что и Valkey, — включая случай `valkey.password.existingSecret` + `valkey.password.secretKey`, — без `cache.auth.enabled`.
- [x] Valkey чарта сам работает с `valkey.password.existingSecret` + `valkey.password.secretKey` (сейчас ссылается на несозданный `<fullname>-redis-secret`, `evidence/valkey-existing-secret-render.txt`).
- [x] Пароль встроенного кеша не меняется между `helm upgrade` (сейчас `randAlphaNum` на каждом рендере — после фикса AMP разошёлся бы с Valkey; найдено на spec).
- [x] Внешний кеш: пароль можно передать через `cache.auth.existingSecret` (+ ключ); текущий путь `cache.auth.enabled` + `cache.auth.password` (и `externalSecrets`) продолжает работать.
- [x] Render-тест в `helm/amp/tests/` покрывает: дефолты, `valkey.password.existingSecret`, внешний кеш с `existingSecret`, внешний кеш с `cache.auth.password`, lite-профиль (нет `REDIS_PASSWORD`).
- [x] k3d: `helm install` с дефолтами — в логе AMP нет `falling back to in-memory cache`; в Valkey появляются ключи AMP после отправки алерта. _(Свежая установка — без `falling back`; на первом upgrade с `main` — гонка `AMP-CACHE-NO-RECONNECT`, non-goal, `evidence/k3d-acceptance.md` § A2.)_ Evidence в `tasks/AMP-CACHE-NOAUTH/evidence/`.
- [x] Сняты оговорки про `AMP-CACHE-NOAUTH` в `helm/amp/README.md`, `CHANGELOG.md`, ADR-017 (`DECISIONS.md`); запись в `CHANGELOG.md` `[Unreleased]`; баг закрыт в `BUGS.md`.
- [x] Гейты AMP (`WORKFLOW.md` § Гейты AMP), включая `scripts/release-gate.sh`, зелёные.

## Non-Goals

- Переподключение к кешу, недоступному на старте, и readiness по кешу — это `AMP-CACHE-NO-RECONNECT` (отдельный баг). Может проявиться после фикса: тогда фиксируем в evidence, не чиним здесь.
- Метки VCT (`HELM-VCT-CHART-LABELS`), Sentinel/HA Valkey, TLS к кешу.
- Ротация пароля кеша без рестарта.
- Изменения Go-кода: лог `Redis cache initialized`/`falling back` и degraded reason в health уже есть (research).

## Constraints

- **Scope:** `helm/amp/templates/{deployment,secret,externalsecret,_helpers}.tpl|yaml`, `values*.yaml`, render-тест, документация чарта/CHANGELOG/ADR-017, `BUGS.md`.
- **Security:** пароль только через `secretKeyRef`, не в `value:`/ConfigMap/лог; неиспользуемый helper `amp.cacheUrl` (`_helpers.tpl:116`) встраивает пароль в URL открытым текстом — решить в spec (удалить или не трогать), но не начинать им пользоваться.
- **Compatibility:** установки с `cache.auth.enabled: true` (внешний кеш) не должны сломаться при upgrade. Установки на дефолтах после upgrade перейдут с in-memory на Valkey — состояние в памяти и так теряется при рестарте пода на upgrade; отметить в CHANGELOG. Конфликт `cache.auth.enabled: true` при встроенном Valkey (два источника пароля) — правило приоритета определить в spec.

## Discovery Notes

- Similar tasks: `tasks/archive/HELM-SINGLE-NODE-DEFAULTS/` (переход на Valkey, evidence NOAUTH, review R2/R7/R14), `tasks/archive/HELM-EXTERNAL-DATABASE/` (паттерн `externalDatabase.existingSecret` + `existingSecretPasswordKey`, render-тест `render-external-database.sh`).
- Relevant patterns: `deployment.yaml:156-163` (`secretKeyRef` на `existingSecret` с ключом по умолчанию), `secret.yaml:28-40` (почему нет random-fallback для внешнего кеша), `redis-secret.yaml` (Secret встроенного кеша создаётся только без `valkey.password.existingSecret`), Go: `go-app/internal/infrastructure/cache/interface.go:67` (`REDIS_PASSWORD`), лог fallback — `go-app/internal/application/service_registry.go:1204-1212`.
- Open unknowns:
  - Как чарт отличает «встроенный» кеш от внешнего: по `valkey.replicas > 0`/`valkey.enabled`, по совпадению `cache.host` с дефолтом, или явным ключом — решает spec (у `values-production.yaml` `valkey.replicas: 0` + свой `cache.host`).
  - Что делать, если `valkey.replicas: 0`, а `cache.host` оставлен дефолтным (кеша нет) — fail рендера или текущее поведение.
  - Проявится ли `AMP-CACHE-NO-RECONNECT` на k3d после фикса (порядок старта AMP/Valkey).
