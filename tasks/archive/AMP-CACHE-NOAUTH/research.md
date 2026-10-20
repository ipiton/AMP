---
id: AMP-CACHE-NOAUTH
slug: amp-cache-noauth
stream: Helm
type: bug
artifact: research-pack
status: complete
created_at: 2026-10-20
updated_at: 2026-10-20
---

# Research Pack - AMP авторизуется в кеше чарта на дефолтах

## Goal

TL;DR: признак «кеш свой» — `valkey.replicas > 0`; тогда `REDIS_PASSWORD` берётся из того же Secret/ключа, что у Valkey, `cache.auth.*` не участвует. Внешний кеш (`replicas: 0`) — `cache.auth.existingSecret` или прежний `cache.auth.password`. Go не меняем. Вопросы: (1) как отличить свой кеш от внешнего; (2) приоритет, если заданы оба источника пароля; (3) `replicas: 0` + дефолтный `cache.host`; (4) нужна ли Go-часть для наблюдаемости.

## Sources

- `helm/amp/templates/{redis-statefulset,redis-secret,deployment,secret,externalsecret}.yaml`, `_helpers.tpl:113-124`, `values.yaml:459-467,528-535`, `values-production.yaml:85-113`
- `go-app/internal/application/service_registry.go:1196-1218`, `service_registry_health.go:139-149`, `internal/config/config.go:671-672`
- `docs/06-planning/TECH-DEBT.md` HELM-CHART-GAPS; `tasks/archive/HELM-EXTERNAL-DATABASE/` (паттерн `existingSecret`)

## Findings

- StatefulSet Valkey гейтится только `profile: standard`; `valkey.enabled`/`cache.enabled` его не выключают (HELM-CHART-GAPS). Единственный рабочий способ «кеш внешний» — `valkey.replicas: 0` (так в `values-production.yaml`). Значит, `replicas > 0` ⇔ под Valkey реально работает, и пароль у него из `valkey.password.existingSecret` (ключ `valkey.password.secretKey`) либо из `<fullname>-redis-secret` (ключ `password`, `randAlphaNum 32` при пустом `value`).
- Найдено в коде: при `valkey.password.existingSecret` StatefulSet всё равно ссылается на `<fullname>-redis-secret`/`password` (`redis-statefulset.yaml:162,239,289`) — Secret не создан ⇒ под не стартует. Этот путь сломан и сейчас (`evidence/valkey-existing-secret-render.txt`); init-контейнер к тому же читает файл `/tmp/secret/password` — при другом `secretKey` нужен `items`. Починка в scope: AMP и Valkey берут пароль из одного источника.
- `cache.auth.password` → `<fullname>-secrets`/`redis-password` (или ESO при `externalSecrets.enabled`); `required` без random-fallback, обоснование в `secret.yaml:29-38` — оставить для внешнего кеша.
- Go: `REDIS_PASSWORD` читается (`cache/interface.go:67`, viper `AutomaticEnv`). Наблюдаемость уже есть: лог `Redis cache initialized`/`falling back`, degraded reason в health (`service_registry_health.go:139-149`) ⇒ Go не трогаем.
- `amp.cacheUrl` (`_helpers.tpl`) вызывает только `amp.redisUrl`, который никто не вызывает (grep по `helm/`), пароль в нём открытым текстом в URL — удалить.

## Decision

`Generation:` single-pass — изменение только в шаблонах, откат = revert чарта, данных не касается; fan-out по линзам не окупается.
- **A (выбрано) — по `valkey.replicas`.** Опирается на единственный работающий гейт, новых ключей для своего кеша не нужно, `values-production.yaml` уже подходит.
- B — сравнивать `tpl cache.host` с `<fullname>-redis`: ломается на FQDN (`…-redis.<ns>.svc`) и алиасах. Отклонено.
- C — явный `cache.external: true`: новый контракт, при нём `values-production.yaml` и все внешние установки надо править. Отклонено.
- Приоритет: при `replicas > 0` источник — Secret Valkey, `cache.auth.*` игнорируется (обход «`cache.auth.password` = `valkey.password.value`» продолжает работать). Предупреждение в NOTES, если `cache.auth.enabled` задан при своём кеше.
- `replicas: 0` + `cache.host` по умолчанию + `cache.enabled` — `fail` рендера (кеша нет, AMP уйдёт в in-memory); текст по образцу `externalDatabase`.

## Follow-ups

- [ ] Spec: ключ для ключа Secret внешнего кеша (`cache.auth.existingSecretPasswordKey`, по образцу `externalDatabase`), CHANGELOG migration note (дефолты: in-memory → Valkey).
- [ ] Мёртвые флаги `valkey.enabled`/`cache.enabled` — остаются в HELM-CHART-GAPS, не в этой задаче.
