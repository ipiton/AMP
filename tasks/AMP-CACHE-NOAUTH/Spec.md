---
id: AMP-CACHE-NOAUTH
slug: amp-cache-noauth
stream: Helm
type: bug
status: draft
created_at: 2026-10-20
updated_at: 2026-10-20
based_on:
  - requirements.md
  - research.md
---

# Specification: AMP авторизуется в кеше чарта на дефолтах

**Version:** 1.0  
**Status:** Draft

## Summary

AMP и Valkey чарта берут пароль из одного Secret и ключа, этот пароль не меняется между `helm upgrade`, а для внешнего кеша появляется `cache.auth.existingSecret`. Свой кеш от внешнего чарт отличает по `valkey.replicas > 0`.

## Requirements Coverage

| Requirement / Criterion | Covered by |
|---|---|
| Встроенный кеш: `REDIS_PASSWORD` из Secret/ключа Valkey без `cache.auth.enabled` | Target Design §2; render-тест T1, T2 |
| Valkey работает с `valkey.password.existingSecret` + `secretKey` | Target Design §1; T2 |
| Пароль встроенного кеша стабилен между upgrade (найдено в spec) | Target Design §1 (`lookup`); k3d A2 |
| Внешний кеш: `cache.auth.existingSecret`, старый путь `cache.auth.password` работает | Target Design §2; T3, T4 |
| Render-тест: дефолты, existingSecret, внешний ×2, lite | `helm/amp/tests/render-cache-auth.sh` T1–T7 |
| k3d: нет `falling back`, ключи AMP в Valkey | Rollout, `evidence/k3d-acceptance.md` A1–A2 |
| Сняты оговорки README/CHANGELOG/ADR-017, баг закрыт | Component Architecture (docs), finalize |
| Гейты AMP зелёные | testing: `scripts/release-gate.sh` (подхватывает `tests/*.sh`, `release-gate.sh:244-252`) |

## Current State

- **Code:** `templates/redis-secret.yaml` — `<fullname>-redis-secret`/`password`: `valkey.password.value` или `randAlphaNum 32` **на каждом рендере**, без `lookup`; не создаётся при `valkey.password.existingSecret`. `templates/redis-statefulset.yaml` — env `REDIS_PASSWORD` у valkey (`:159-163`) и exporter (`:236-240`), volume `redis-secret` (`:287-289`) жёстко на `<fullname>-redis-secret`/`password`; init читает `/tmp/secret/password` (`:118-123`); checksum только на ConfigMap (`:48`). `templates/deployment.yaml:172-185` — `REDIS_PASSWORD` только при `cache.auth.enabled`, из `<fullname>-secrets`/`redis-password`. `templates/secret.yaml:28-41`, `externalsecret.yaml:33-36` — `redis-password` при `cache.enabled && cache.auth.enabled`. `_helpers.tpl:112-132` — `amp.cacheUrl`/`amp.redisUrl`, не вызываются.
- **Data:** не применимо (данные кеша не трогаем; AOF/RDB на PVC переживают рестарт пода).
- **Tests:** `helm/amp/tests/render-single-node.sh` T6 (NOTES, внешний кеш через `valkey.replicas=0` + `cache.host`); остальные render-тесты передают `cache.auth.password` для `values-production.yaml`. Тестов на `REDIS_PASSWORD` нет.
- **Docs:** `helm/amp/README.md:63-65` (оговорка про баг), `values.yaml:459-467,528-535`, `values-production.yaml:84-113`, `CHANGELOG.md:273-274`, ADR-017 «Следствие» (`DECISIONS.md:302`), `BUGS.md` AMP-CACHE-NOAUTH.

## Design Premises

| Premise | Confirmed by | Class | If wrong |
|---|---|---|---|
| Под Valkey чарта существует ⇔ `profile: standard` и `valkey.replicas > 0` | `redis-statefulset.yaml:1,24`; других StatefulSet/Deployment кеша нет (`grep -rn redis-statefulset\|kind: StatefulSet helm/amp/templates`); NOTES уже ветвится так же (`NOTES.txt:36-42`) | call-path-traced | AMP с `replicas: 0` не получит пароль внешнего кеша (или получит чужой) |
| На дефолтах fallback вызван именно отсутствием пароля (`NOAUTH`) | `tasks/archive/HELM-SINGLE-NODE-DEFAULTS/evidence/cache-noauth.txt` (k3d 2026-10-19) | measured | фикс не уберёт fallback; ловится k3d A1 |
| AMP берёт пароль кеша из env `REDIS_PASSWORD` | `config.go:671-672,907` (`AutomaticEnv`, ключ `redis.password` известен), `appconfig.CacheConfigFrom(r.config.Redis)` в `service_registry.go:1202`; ConfigMap чарта `redis.*` не задаёт | code-read | пароль не дойдёт; ловится k3d A1 |
| Valkey берёт `requirepass` из файла `/tmp/secret/password` при старте пода (init), exporter и пробы — из env | `redis-statefulset.yaml:118-123,159-163,176-202,236-240` | code-read | рассинхрон пароля Valkey и exporter/проб |
| Без `lookup` пароль `<fullname>-redis-secret` новый на каждом рендере ⇒ каждый `helm upgrade` переписывает Secret; под Valkey без изменения шаблона не рестартует (checksum только на ConfigMap). `checksum/secret` AMP на дефолтах стабилен ⇒ AMP читает новый пароль при первом своём рестарте после upgrade (смена образа/values, переезд пода), Valkey держит старый | `evidence/redis-secret-rerender.txt` (2 рендера: 2 разных пароля, одинаковый `checksum/secret`); `redis-secret.yaml:24-28`, `redis-statefulset.yaml:46-48`, `deployment.yaml:34-35` | measured (рендер) + code-read (рестарты) | `WRONGPASS` и тот же fallback после upgrade + рестарта AMP; ловится k3d A2 |
| `lookup` возвращает пустое под `helm template`/`--dry-run` и в GitOps-рендерах (Argo CD) | документация Helm (`lookup`) | assumed | под `helm template` — безвредно (новый пароль, как сейчас); GitOps — см. Rollout (рекомендация `valkey.password.value`/`existingSecret`) |
| Этот upgrade меняет pod template Valkey (`items` у volume) ⇒ Valkey рестартует и перечитывает Secret — рассинхрон, накопленный прошлыми upgrade, лечится сам | Target Design §1 меняет `spec.template` | code-read | AMP получит пароль Secret, Valkey — старый; ловится k3d A2 |

## Target Design

1. **Пароль Valkey — один источник.** Два helper'а в `_helpers.tpl`: `amp.valkey.secretName` (`valkey.password.existingSecret` или `<fullname>-redis-secret`) и `amp.valkey.secretKey` (`valkey.password.secretKey | default "password"` при `existingSecret`, иначе `password` — ключ Secret чарта не меняется). Все три места StatefulSet ссылаются на них; volume получает `items: [{key: <secretKey>, path: password}]`, init по-прежнему читает `/tmp/secret/password`. `redis-secret.yaml` при пустом `valkey.password.value` сначала берёт `password` из существующего Secret через `lookup`, `randAlphaNum 32` — только если Secret ещё нет.
2. **`REDIS_PASSWORD` у AMP** (standard, `cache.enabled`):
   - `valkey.replicas > 0` → `secretKeyRef` на `amp.valkey.secretName`/`amp.valkey.secretKey`; `cache.auth.*` не участвуют.
   - `valkey.replicas == 0` (внешний кеш): `cache.auth.existingSecret` → `secretKeyRef` на него с ключом `cache.auth.existingSecretPasswordKey | default "password"`; иначе `cache.auth.enabled` → как сейчас (`<fullname>-secrets`/`redis-password`); иначе пароля нет (кеш без auth).
3. **`redis-password` в `<fullname>-secrets`/ExternalSecret** рендерится только для внешнего кеша без `existingSecret`: `cache.enabled && cache.auth.enabled && replicas == 0 && !cache.auth.existingSecret`. Для своего кеша `cache.auth.password` больше не обязателен.
4. **Fail рендера** (standard, `cache.enabled`): `replicas == 0` и `tpl cache.host` == `<fullname>-redis` («кеша нет»); `cache.auth.existingSecret` вместе с `cache.auth.enabled` («выберите одно»). Тексты — по образцу `externalDatabase` (`deployment.yaml:95-109`).
5. **NOTES:** при `replicas > 0` и `cache.auth.enabled` — предупреждение, что `cache.auth.*` для Valkey чарта игнорируется; строка внешнего кеша называет источник пароля (Secret/ключ), как строка внешнего PostgreSQL.
6. Удалить `amp.cacheUrl`/`amp.redisUrl` (мертвы, пароль открытым текстом в URL).

Go не меняется: лог `Redis cache initialized`/`falling back` и degraded reason в health уже есть.

## API Contracts

Не применимо (HTTP API AMP не меняется). Контракт чарта (values):

- **Новые ключи:** `cache.auth.existingSecret: ""`, `cache.auth.existingSecretPasswordKey: password`.
- **Изменённая семантика:** `cache.auth.*` применяется только при `valkey.replicas: 0`; `valkey.password.secretKey` теперь реально используется.
- **Новые отказы рендера:** см. Target Design §4. `--reuse-values` из старого чарта: новых ключей нет ⇒ `nil` ⇒ ветка «как раньше» (`| default` в шаблонах).

## Data Model / Migrations

Не применимо.

## Component Architecture

- `helm/amp/templates/_helpers.tpl` — `+amp.valkey.secretName`, `+amp.valkey.secretKey`; `-amp.cacheUrl`, `-amp.redisUrl`.
- `helm/amp/templates/redis-secret.yaml` — `lookup` существующего пароля.
- `helm/amp/templates/redis-statefulset.yaml` — helper'ы в env valkey/exporter и volume + `items`.
- `helm/amp/templates/deployment.yaml` — ветвление `REDIS_PASSWORD` (§2), fail (§4).
- `helm/amp/templates/secret.yaml`, `externalsecret.yaml` — условие `redis-password` (§3).
- `helm/amp/templates/NOTES.txt` — §5.
- `helm/amp/values.yaml` (`cache.auth`, `valkey.password` — комментарии и новые ключи), `values-production.yaml` (комментарий: `existingSecret` как предпочтительный путь).
- `helm/amp/tests/render-cache-auth.sh` — новый render-тест.
- Docs: `helm/amp/README.md`, `CHANGELOG.md` `[Unreleased]`, `helm/amp/CHANGELOG.md`, ADR-017 «Следствие» (снять оговорку, ссылка на задачу), `docs/06-planning/BUGS.md` (finalize).

## Security Design

- [x] Ownership validation — не применимо (нет пользовательских данных).
- [x] Input validation — fail рендера на противоречивые/пустые комбинации (§4); `secretKeyRef` без `optional`, отсутствующий Secret/ключ = `CreateContainerConfigError`, а не пустой пароль.
- [x] Sensitive data not logged — пароль только в Secret и `secretKeyRef`; не в `value:`, ConfigMap, NOTES, логе init (init печатает только факт инъекции); удаляется helper, строивший URL с паролем.
- [x] Rate limiting — не применимо.
- [x] Auth/RBAC path — `lookup` выполняется с правами того, кто запускает Helm (get Secret в своём namespace); RBAC AMP не меняется.

## Invariants

- [ ] `values-production.yaml` + `--set cache.auth.password=…` рендерит то же, что сейчас: `REDIS_PASSWORD` из `<fullname>-secrets`/`redis-password` (release-gate и все render-тесты на этом стоят).
- [ ] Lite-профиль: ни `REDIS_PASSWORD`, ни объектов Valkey (как сейчас).
- [ ] `cache.enabled: false`: `REDIS_PASSWORD` нет (как сейчас).
- [ ] Повторный рендер с существующим `<fullname>-redis-secret` не меняет пароль.
- [ ] Обход «`cache.auth.enabled` + `cache.auth.password` = `valkey.password.value`» продолжает работать (AMP получает тот же пароль из Secret Valkey).

## Edge Cases

1. Дефолты, первая установка → Secret с новым паролем, AMP и Valkey — из него.
2. Повторный `helm upgrade`, затем рестарт AMP → `lookup` возвращает прежний пароль; Secret, Valkey и AMP согласованы.
3. Upgrade с `main` (Secret мог разойтись с паролем работающего Valkey) → pod template Valkey меняется ⇒ рестарт ⇒ Valkey перечитывает Secret. Возможна гонка старта AMP/Valkey → `AMP-CACHE-NO-RECONNECT`; лечится `rollout restart`, проверяется в A2.
4. `valkey.password.existingSecret=s`, `secretKey=k` → Secret чарта не рендерится; Valkey, exporter, AMP читают `s`/`k`.
5. `valkey.password.value=x` → Secret чарта с `x`, `lookup` не используется.
6. `replicas: 0`, `cache.host` внешний, без `cache.auth` → AMP без пароля.
7. `replicas: 0`, `cache.auth.existingSecret=e` → `REDIS_PASSWORD` из `e`/`password`; `redis-password` в `<fullname>-secrets` нет.
8. `replicas: 0`, `cache.auth.existingSecret` + `cache.auth.enabled: true` → fail.
9. `replicas: 0`, `cache.host` по умолчанию → fail «нет кеша».
10. `replicas > 0`, `cache.auth.enabled: true`, пустой `cache.auth.password` → рендерится (раньше падал на `required`), NOTES предупреждает.
11. `helm template` / Argo CD (`lookup` пуст) → новый случайный пароль на каждый рендер: AMP и Secret согласованы, Valkey — до рестарта нет. Документируем: для GitOps задавать `valkey.password.value` или `existingSecret`.

## Impact Analysis

- **Affected modules:** только `helm/amp` и документация; Go — нет.
- **Breaking changes:** на дефолтах AMP начинает реально использовать Valkey (повтор уведомлений при рестарте пода исчезает; latency горячего пути группировки — сетевая); два новых отказа рендера (§4) для конфигураций, которые и сейчас не работают; при своём кеше `cache.auth.*` больше не влияет. Всё — в `CHANGELOG.md` с migration notes.
- **New dependencies:** none.
- **Risks:** гонка старта AMP/Valkey на upgrade (`AMP-CACHE-NO-RECONNECT`) — теперь видна на дефолтах; митигация — шаг в migration notes, проверка в A2. `lookup` в GitOps — Edge Case 11.

## Rollout / Rollback

- **Rollout:** нет deploy (AMP — продукт). Acceptance на k3d (`evidence/k3d-acceptance.md`): A1 — `helm install` с дефолтами: нет `falling back`, есть `Redis cache initialized`, после `POST /api/v2/alerts` в Valkey есть ключи AMP (`redis-cli --scan` с паролем из Secret). A2 — `helm upgrade` с `main`, затем второй `helm upgrade` без изменений и `kubectl rollout restart deploy/<fullname>`: пароль в Secret не меняется, AMP подключён после каждого шага. Migration note: после upgrade проверить лог AMP; при `falling back` — `kubectl rollout restart deploy/<fullname>`.
- **Rollback:** `helm rollback` на прежнюю ревизию: AMP снова без пароля → in-memory (прежнее поведение); Secret чарта сохраняет значение (`lookup` больше не участвует, но прежний шаблон пересоздаст пароль — как и раньше). Данных не теряется.
- **Feature flag:** не применимо.

## Observability

- **Logs:** без изменений; проверка — `Redis cache initialized` vs `Redis cache unavailable, falling back to in-memory cache`.
- **Metrics:** без изменений (`redis_up` exporter'а; degraded reason в health AMP).
- **Alerts:** не применимо.

## Deep Review

- **Mandatory triggers present:** `S`; 3 сигнала (`C S R`).
- **Discretionary triggers present:** none.
- **Decision:** required.

## Open Questions

- [ ] Поведение `lookup` в GitOps-рендерах (`assumed`) — не проверяем, закрываем документацией (Edge Case 11).
- [ ] Проявится ли `AMP-CACHE-NO-RECONNECT` в A2 — фиксируем в evidence, чиним отдельной задачей.
