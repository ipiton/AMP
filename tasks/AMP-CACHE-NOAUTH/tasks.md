---
id: AMP-CACHE-NOAUTH
slug: amp-cache-noauth
stream: Helm
type: bug
status: active
created_at: 2026-10-20
updated_at: 2026-10-20
based_on:
  - requirements.md
  - research.md
  - Spec.md
---

# Implementation Plan: AMP авторизуется в кеше чарта на дефолтах

**Based on:** requirements.md / research.md / Spec.md
**Date:** 2026-10-20
**Tier:** Full (`C S R`) → `implement → deep-review → write-tests → testing → finalize → merge-to-main`

Обозначения в verify: `T` = `helm template t helm/amp`, `E <name>` = значение env `<name>` контейнера `amp` (`yq` по Deployment `t-amp`). Все команды — из корня репозитория.

## Touched Files

- `helm/amp/templates/_helpers.tpl` — `+amp.valkey.secretName`, `+amp.valkey.secretKey`; `-amp.cacheUrl`, `-amp.redisUrl`
- `helm/amp/templates/redis-secret.yaml` — `lookup` существующего пароля
- `helm/amp/templates/redis-statefulset.yaml` — helper'ы в env valkey/exporter, volume `redis-secret` + `items`
- `helm/amp/templates/deployment.yaml` — ветвление `REDIS_PASSWORD`, два fail
- `helm/amp/templates/secret.yaml`, `helm/amp/templates/externalsecret.yaml` — условие `redis-password`
- `helm/amp/templates/NOTES.txt` — предупреждение и источник пароля внешнего кеша
- `helm/amp/values.yaml`, `helm/amp/values-production.yaml` — ключи `cache.auth.existingSecret*`, комментарии
- `helm/amp/README.md`, `helm/amp/CHANGELOG.md`, `CHANGELOG.md` — изменения, migration notes
- `docs/06-planning/DECISIONS.md` — ADR-017 «Следствие»: снять оговорку про баг
- `helm/amp/tests/render-cache-auth.sh` (новый) — Phase 4
- `tasks/AMP-CACHE-NOAUTH/evidence/` — release-gate, k3d

## Phase 1: чарт — пароль Valkey и AMP

> **Wave 1** — независимые шаги (разные файлы)

- [x] **1.1** `_helpers.tpl`: `amp.valkey.secretName` (`valkey.password.existingSecret` или `<fullname>-redis-secret`), `amp.valkey.secretKey` (`valkey.password.secretKey | default "password"` при `existingSecret`, иначе `password`); удалить `amp.cacheUrl`, `amp.redisUrl`. <!-- verify: grep -c 'amp.cacheUrl\|amp.redisUrl' helm/amp/templates/_helpers.tpl → 0; T >/dev/null -->
- [x] **1.2** `redis-secret.yaml`: при пустом `valkey.password.value` — `password` из `lookup "v1" "Secret" .Release.Namespace "<fullname>-redis-secret"`, `randAlphaNum 32` только без него. <!-- verify: два T подряд дают разные пароли (lookup пуст под template — как сейчас); T --set valkey.password.value=x → base64 "x" -->
- [x] **1.3** `values.yaml`: `cache.auth.existingSecret: ""`, `cache.auth.existingSecretPasswordKey: password`; комментарии `cache.auth` (только внешний кеш, `valkey.replicas: 0`) и `valkey.password` (`secretKey` — ключ в `existingSecret`; GitOps без `lookup` — задать `value` или `existingSecret`). `values-production.yaml`: комментарий — `existingSecret` как предпочтительный путь. <!-- verify: helm lint helm/amp; helm lint helm/amp -f helm/amp/values-production.yaml --set postgresql.password=p --set cache.auth.password=p -f helm/amp/tests/values-production-placeholders.yaml -->

> **Wave 2** — зависит от Wave 1

- [x] **1.4** `redis-statefulset.yaml`: env `REDIS_PASSWORD` valkey и exporter, volume `redis-secret` → helper'ы; volume `items: [{key: <secretKey>, path: password}]`. <!-- depends: 1.1 | verify: T --set valkey.password.existingSecret=s --set valkey.password.secretKey=k | grep -c 't-amp-redis-secret' → 0, secretKeyRef s/k ×2, items key k -->
- [x] **1.5** `deployment.yaml`: `REDIS_PASSWORD` по Spec § Target Design 2 (`replicas > 0` → Secret Valkey; `replicas == 0` → `cache.auth.existingSecret` / `cache.auth.enabled` / без пароля); fail «`replicas: 0` при `cache.host` по умолчанию» и «`existingSecret` вместе с `cache.auth.enabled`» — тексты по образцу `externalDatabase`. <!-- depends: 1.1, 1.3 | verify: T → E REDIS_PASSWORD secretKeyRef t-amp-redis-secret/password; T --set valkey.replicas=0 → fail; T --set valkey.replicas=0 --set cache.host=c --set cache.auth.existingSecret=e → e/password -->
- [x] **1.6** `secret.yaml`, `externalsecret.yaml`: `redis-password` только при `cache.enabled && cache.auth.enabled && valkey.replicas == 0 && !cache.auth.existingSecret`. <!-- depends: 1.3 | verify: T --set cache.auth.enabled=true рендерится без cache.auth.password; values-production + cache.auth.password → redis-password есть -->
- [x] **1.7** `NOTES.txt`: при `replicas > 0` и `cache.auth.enabled` — «`cache.auth.*` для Valkey чарта игнорируется»; строка внешнего кеша — источник пароля (Secret/ключ или «без пароля»), без значения пароля. <!-- depends: 1.3 | verify: helm install --dry-run t helm/amp --set cache.auth.enabled=true | grep -c ignored → 1 -->

**Phase verification:** `helm lint helm/amp`; T для дефолтов, `-f values-production.yaml` (+ placeholders), `--set profile=lite`, `--set cache.enabled=false`; Invariants из Spec (prod-рендер: `REDIS_PASSWORD` из `t-amp-secrets`/`redis-password`; lite и `cache.enabled=false` — без `REDIS_PASSWORD`); `bash helm/amp/tests/render-single-node.sh` и остальные `helm/amp/tests/*.sh` зелёные.

## Phase 2: документация

- [x] **2.1** `helm/amp/README.md`: снять оговорку `AMP-CACHE-NOAUTH` (`:65`); раздел о кеше — свой Valkey (пароль, `existingSecret`/`secretKey`, GitOps), внешний (`valkey.replicas: 0` + `cache.auth.existingSecret`). <!-- depends: Phase 1 | verify: grep -c 'AMP-CACHE-NOAUTH' helm/amp/README.md → 0 -->
- [x] **2.2** `CHANGELOG.md` `[Unreleased]` + `helm/amp/CHANGELOG.md`: Fixed (AMP авторизуется в Valkey, пароль стабилен, `valkey.password.existingSecret` работает), Added (`cache.auth.existingSecret*`), migration notes: два новых fail, `cache.auth.*` при своём кеше игнорируется, рестарт Valkey на upgrade, проверка лога AMP и `rollout restart` (`AMP-CACHE-NO-RECONNECT`); в пункте Valkey (`CHANGELOG.md:274`) снять ссылку на баг. <!-- depends: Phase 1 | verify: grep -n 'AMP-CACHE-NOAUTH' CHANGELOG.md — только новая запись -->
- [x] **2.3** ~~ADR-017~~ (откат по deep-review B7, перенесено в 6.1) ADR-017 «Следствие» (`DECISIONS.md:302`): снять «на дефолтах не авторизуется…», ссылка на `AMP-CACHE-NOAUTH` как закрытый. <!-- depends: Phase 1 | verify: diff ограничен строкой «Следствие» -->

**Phase verification:** `git diff --check`; ни README, ни ADR не обещают того, чего нет в шаблонах (сверка с Phase 1).

## Phase 3: deep-review

- [x] **3.1** `/deep-review` по диффу ветки → `review-findings.md`, `review-verdict.json`; находки исправить до `"gate": "pass"`. <!-- depends: Phase 1, Phase 2 | verify: jq -r .gate tasks/AMP-CACHE-NOAUTH/review-verdict.json → pass -->

## Phase 4: тесты (после verdict `pass`)

- [x] **4.1** `helm/amp/tests/render-cache-auth.sh` по образцу `render-external-database.sh` (`assert_count`/`assert_docs`/env-хелперы): T1 дефолты — AMP, valkey, exporter читают `t-amp-redis-secret`/`password`; T2 `valkey.password.existingSecret`+`secretKey` — Secret чарта не рендерится, все три и `items` на `s`/`k`; T3 внешний + `cache.auth.existingSecret` (+ свой ключ); T4 `values-production.yaml` + `cache.auth.password` — `t-amp-secrets`/`redis-password` (Invariant); T5 lite и `cache.enabled=false` — без `REDIS_PASSWORD`; T6 два fail с текстом; T7 `replicas > 0` + `cache.auth.enabled` без пароля рендерится, `redis-password` нет, NOTES предупреждает (и при одном `cache.auth.password`); T7a внешний `cache.host` при `replicas` по умолчанию + `cache.auth.enabled/password` → `t-amp-secrets`/`redis-password`, как на `main` (A1/B1), NOTES — «Valkey runs unused»; T7b `cache.host=t-amp-redis.<ns релиза>.svc.cluster.local` → Secret Valkey, при `replicas: 0` — fail (B2); `t-amp-redis.<другой ns>.svc…` и `t-amp-redisx` — внешний кеш, как на `main` (C1); T7c NOTES на upgrade — строка про `Redis cache initialized` (B4); T8 в выводе нет `redis://:` и значения пароля вне Secret. <!-- depends: 3.1 | verify: bash helm/amp/tests/render-cache-auth.sh → exit 0 --> Сделано: 82 проверки, релиз `amp` (fullname `amp`, а не `t-amp`). **T7c не рендер-тестом**: `helm template` не выводит NOTES, а `--dry-run=client` даёт только `IsInstall` — подсказка на upgrade проверяется на k3d в 5.3; рендер-тест проверяет обратное (на install подсказки нет). При `valkey.replicas: 0` StatefulSet и Secret Valkey по-прежнему рендерятся (`replicas: 0`, как до задачи) — тест этого не утверждает.
- [x] **4.2** Проверка, что тест ловит баг: прогон T1/T2 против чарта `main` (`git worktree add` во временный каталог) падает. <!-- depends: 4.1 | verify: вывод FAIL на main записан в evidence/render-cache-auth-main.txt --> Сделано: копия чарта `git archive main` (`ddc02ae`) — 28 FAIL (T1, T2, T3, T6, T7, T7a NOTES, T7b свой хост), инварианты T4/T5/T7a env/T7b чужой хост/T8 проходят и на `main`.

**Phase verification:** `for t in helm/amp/tests/*.sh; do bash "$t" || echo FAIL "$t"; done` — без FAIL.

## Phase 5: testing и приёмка

- [ ] **5.1** Гейты AMP: `make -C go-app quality-gates-fast` (Go не меняется — контрольный), `scripts/release-gate.sh` → `RESULT: PASS`, вывод в `evidence/release-gate.txt`; `git diff --check`. <!-- depends: Phase 4 | verify: grep 'RESULT: PASS' tasks/AMP-CACHE-NOAUTH/evidence/release-gate.txt -->
- [ ] **5.2** k3d **A1**: `helm install` веткой с дефолтами → все Ready; лог AMP: `Redis cache initialized`, нет `falling back`; `POST /api/v2/alerts` → 200, затем `redis-cli -a <пароль из Secret> --scan` в поде Valkey показывает ключи AMP. <!-- depends: 5.1 | verify: evidence/k3d-acceptance.md § A1 -->
- [ ] **5.3** k3d **A2**: `helm install` чартом `main` → `helm upgrade` веткой → второй `helm upgrade` без изменений → `kubectl rollout restart deploy/t-amp`: хеш пароля в Secret одинаков после обоих upgrade, AMP подключён после каждого шага; если после первого upgrade `falling back` (гонка, `AMP-CACHE-NO-RECONNECT`) — зафиксировать, `rollout restart`, продолжить. NOTES первого `helm upgrade` содержат подсказку про `Redis cache initialized` (T7c из 4.1). <!-- depends: 5.2 | verify: evidence/k3d-acceptance.md § A2 -->
- [ ] **5.4** Удалить k3d-кластер задачи — с подтверждения владельца. <!-- depends: 5.3 | verify: k3d cluster list -->

## Phase 6: finalize

- [ ] **6.1** `/finalize`: ADR-017 «Следствие» — снять оговорку про баг (перенесено из 2.3 по deep-review B7: не объявлять закрытым до приёмки); `DONE.md`; `NEXT.md` (WIP → пусто, Queue пополнить — сейчас 2 задачи); `BUGS.md` — закрыть `AMP-CACHE-NOAUTH`, в `AMP-CACHE-NO-RECONNECT` снять «маскирует `AMP-CACHE-NOAUTH`» и добавить результат A2; новые записи по deep-review: BUGS `HELM-NAMESPACE-SPLIT` (A2), TECH-DEBT `HELM-CHART-GAPS` — GitOps без `lookup` (A3) и нет рестарта подов при смене пароля Valkey (B5), BACKLOG — пароль в argv проб (A6); архив workspace. <!-- depends: Phase 5 | verify: ls tasks/archive/AMP-CACHE-NOAUTH -->

## Definition of Done

- [ ] All steps are complete or explicitly marked blocked/skipped
- [ ] Success criteria from `requirements.md` are covered
- [ ] Contracts from `Spec.md` are implemented or deviations are recorded
- [ ] Deep review verdict is `pass`
- [ ] Tests for changed behavior are added or updated
- [ ] Phase checks pass
- [ ] Docs/planning are updated if behavior, contracts, or process changed

## Deviations from Spec (implement, 2026-10-20)

- **NOTES (§5):** предупреждение «`cache.auth.*` игнорируется» выводится и при `cache.auth.existingSecret`, не только при `cache.auth.enabled` — иначе `existingSecret` при своём Valkey игнорировался бы молча.
- **`secret.yaml`:** пояснение к новому условию — шаблонный комментарий (`{{- /* */}}`), YAML-комментарий оставлен прежним: так рендер `<fullname>-secrets` и `checksum/secret` у `values-production.yaml` и lite не меняются, AMP в проде не рестартует из-за этой задачи. Проверено сравнением с базовым рендером `main` (prod: checksum совпадает; lite: рендер идентичен).
- **README чарта:** вместо точечной правки — раздел `### Cache` (свой Valkey, внешний кеш, ротация пароля через `rollout restart`, `AMP-CACHE-NO-RECONNECT`) и четыре строки в таблице Storage.
- **Рендер на дефолтах** (сверка с базовым рендером `main`, `valkey.password.value=fixed`): изменились только `REDIS_PASSWORD` у AMP, `items` у volume Valkey и комментарии в `<fullname>-redis-secret`; `checksum/secret` AMP совпадает. Под AMP рестартует на upgrade из-за нового env, Valkey — из-за `items`.
- **Выбор «свой кеш» (deep-review A1/B1, обе независимые находки):** не только `valkey.replicas > 0`, но и `cache.host` указывает на Valkey чарта (`amp.cache.hostIsValkey`: `<fullname>-redis` или префикс `<fullname>-redis.`); helper `amp.cache.bundled` используют `deployment.yaml`, `secret.yaml`, `externalsecret.yaml`, NOTES. Внешний `cache.host` при `replicas` по умолчанию снова получает пароль `cache.auth.*` (рендер совпадает с `main`) — требование совместимости из `requirements.md`. Сравнение по host research отклонял из-за FQDN; префикс снимает это возражение, точный признак работы пода по-прежнему `replicas`.
- **NOTES:** подсказка «Valkey работает вхолостую» при внешнем `cache.host` и `replicas > 0`; на upgrade своего кеша — как проверить подключение и `rollout restart` (B4); предупреждение про `cache.auth.*` и при одном `cache.auth.password` (B8).
- **ADR-017** правится в finalize, не в implement (B7).
- **T7c (write-tests):** подсказка NOTES на upgrade не покрыта рендер-тестом — `helm template` NOTES не выводит, клиентский `--dry-run` знает только `IsInstall`; проверка перенесена в k3d A2 (5.3).
