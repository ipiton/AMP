# Tasks: P2-HYGIENE-BUNDLE

Фазы бандла — его участники. Research и Spec свёрнуты в `requirements.md` (§ Research (mini), § Constraints): каждый участник — правка на 1–15 строк с одним очевидным решением, отдельные документы дублировали бы requirements. Отклонение от Full tier зафиксировано здесь; deep-review выполнен полностью (`review-findings.md`, `review-verdict.json`).

## Фаза 1 — HELM-MONITORING-CRD-GATE

- [x] helper `amp.monitoringCRDs` (`_helpers.tpl`): `.Capabilities.APIVersions.Has "monitoring.coreos.com/v1"` или `monitoring.assumeCRDs`, nil-safe для `--reuse-values`.
- [x] гейт на `servicemonitor.yaml`, `postgresql-prometheus-rules.yaml`, `redis-servicemonitor.yaml`, `redis-prometheusrule.yaml`; строка в `NOTES.txt` при пропуске.
- [x] `values.yaml` `monitoring.assumeCRDs: false` с комментарием; README чарта (Read before installing, Network Exposure, таблица).
- [x] render-тесты: `render-servicemonitor.sh` (без CRD — 0 объектов, с CRD и с `assumeCRDs` — 4, guard без CRD не срабатывает, NOTES); `--api-versions` в хелперах `render-postgresql.sh`, `render-single-node.sh`.
- [x] `release-gate.sh` `step_helm_values`: `helm template` с `--api-versions monitoring.coreos.com/v1` (ревью F4).

## Фаза 2 — RELEASE-TAG-ON-MAIN-GUARD

- [x] `release.yml`: `fetch-depth: 0`, шаг `git merge-base --is-ancestor "$GITHUB_SHA" origin/main` до login; проверено на локальном клоне (коммит из `main` — publish, коммит ветки — `::error::`).

## Фаза 3 — GO-MOD-TIDY-CHECK

- [x] `release-gate.sh` шаг `mod-tidy` (`go mod tidy -diff`), на текущем модуле — код 0.

## Фаза 4 — VALKEY-PROBE-ARGV-PASSWORD

- [x] пробы `REDISCLI_AUTH="$REDIS_PASSWORD" redis-cli ping | grep PONG`; проверено на `valkey/valkey:8.1.10-alpine` (PONG, `NOAUTH` без переменной).
- [x] render-тест в `render-single-node.sh`: 3 пробы через `REDISCLI_AUTH`, нет `-a $`.

## Фаза 5 — HELM-VALUES-DEV-ALIGN

- [x] из `values-dev.yaml` убраны `resources`, `monitoring`, `ingress` (дефолты чарта); `helm-dev` в гейте.

## Фаза 6 — LLM-ERROR-BODY-CAP

- [x] сверено с кодом: уже сделано в `0c48423` (2026-10-28, `INVESTIGATION-SAFETY-RELIABILITY`) — `maxErrorBodyBytes = 512`, `truncateBody` в `client.go` и `chat.go` (`doChat`, через него идёт и `investigate_with_tools.go`), тест `retry_classify_test.go`. Запись BACKLOG закрывается без изменения кода.
