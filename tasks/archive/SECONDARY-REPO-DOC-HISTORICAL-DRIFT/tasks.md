# Implementation Checklist: SECONDARY-REPO-DOC-HISTORICAL-DRIFT

## Research & Spec
- [x] Выполнен `research.md` с inventory по `helm/amp`, `examples`, `grafana`, `go-app/internal` и явным выбором первого narrow sub-slice.
- [x] Подготовлен `Spec.md` с Helm-only границей, non-goals и acceptance criteria.

## Vertical Slices
- [x] **Slice A: Helm Deployment Guide Realignment** — переписать `helm/amp/DEPLOYMENT.md` под текущий `./helm/amp` path, `amp` naming и honest active-runtime-first operator story без изменения chart behavior.
- [x] **Slice B: Helm Comment/Metadata Cleanup** — убрать stale `Alert History` / `Production-Ready` markers из in-scope Helm values/templates comments и descriptive metadata, затем закрыть targeted verification и task/planning sync без ложного claim, что umbrella bug уже исчерпан.

## Implementation
- [x] Шаг 1: Переписать `helm/amp/DEPLOYMENT.md` так, чтобы:
  - использовать current chart path `./helm/amp`;
  - использовать current release/service naming `amp`, а не `alert-history`;
  - не тянуть historical deployment story шире, чем позволяет `controlled replacement` truth.
- [x] Шаг 2: Очистить `helm/amp/values-dev.yaml` и `helm/amp/values-production.yaml` от stale `Alert History Service` headers/comments; в `values-dev.yaml` дополнительно санитизировать hardcoded `llm.apiKey`, потому что literal key-like value нельзя оставлять в изменяемом scope.
- [x] Шаг 3: Очистить `helm/amp/values.yaml`, `helm/amp/templates/postgresql-configmap.yaml`, `helm/amp/templates/postgresql-networkpolicy.yaml`, `helm/amp/templates/postgresql-statefulset.yaml` от `Alert History` / `Production-Ready` wording в comments и human-facing description strings; в `values.yaml` дополнительно санитизировать hardcoded `llm.apiKey`.
- [x] Шаг 4: После правок вручную проверить diff по YAML-файлам и подтвердить, что chart logic/template expressions не менялись; единственное intentional non-comment value change — sanitization hardcoded `llm.apiKey` defaults в `values.yaml` и `values-dev.yaml`.
- [x] Шаг 5: Не трогать `helm/amp/README.md`, `helm/amp/CHANGELOG.md`, `examples/**`, `grafana/**`, `go-app/internal/**` и любые runtime/test strings в `.go`, если для green path этого не требуется.

## Testing
- [x] Прогнать targeted marker scan:
  - `rg -n 'Alert History|Production-Ready|alert-history' helm/amp/DEPLOYMENT.md helm/amp/values-dev.yaml helm/amp/values-production.yaml helm/amp/values.yaml helm/amp/templates/postgresql-configmap.yaml helm/amp/templates/postgresql-networkpolicy.yaml helm/amp/templates/postgresql-statefulset.yaml`
- [x] Дополнительно подтвердить отсутствие hardcoded `sk-...` keys в touched `helm/amp` values files.
- [x] Выполнить manual review edited scope против:
  - `README.md`
  - `docs/06-planning/DECISIONS.md`
  - `helm/amp/README.md`
- [x] Проверить `git diff --check`.
- [x] После sanitization в `values.yaml` и `values-dev.yaml` chart-level evidence добран через:
  - `helm dependency build helm/amp`
  - `helm template amp-dev ./helm/amp -f helm/amp/values-dev.yaml --set profile=lite`
  - `helm template amp ./helm/amp -f helm/amp/values-production.yaml --set profile=standard`
- [x] `helm template` сначала блокировался на preexisting local chart state (`valkey` dependency отсутствовал в `helm/amp/charts/`), а не на diff; blocker снят через `helm repo add groundhog2k ...` + `helm dependency build`.
- [x] Verification подтвердил, что stronger chart gate был нужен именно из-за behavior-adjacent sanitization в values, а не как формальная замена docs verification path.
- [x] Full repo/chart cleanup этим не закрыт: rendered output все еще показывает out-of-scope historical markers в других Helm files (`postgresql-poddisruptionbudget.yaml`, `postgresql-service-headless.yaml`, `postgresql-exporter-configmap.yaml`), поэтому остаток вынесен в отдельный follow-up bug `HELM-SECONDARY-TEMPLATE-HISTORICAL-DRIFT`.

## Write Tests
- [x] Новый code-level test diff не добавлялся: slice ограничен Helm docs/comments cleanup и safety-driven sanitization в values files.
- [x] Роль `/write-tests` для этого slice выполняет explicit docs verification path: marker scans, secret-pattern scan, manual review против planning truth и diff hygiene.
- [x] Если на `/testing` понадобится stronger chart-level evidence из-за sanitization в values, его нужно фиксировать как verification step, а не маскировать под unit/integration tests.

## Documentation & Cleanup
- [x] На `/write-doc` синхронизировать `requirements.md` и `Spec.md` под фактический Helm-only result, включая safety-driven `llm.apiKey` sanitization.
- [x] На `/write-doc` не скрывать remaining clusters; вместо этого декомпозировать umbrella bug в planning на отдельные follow-up entries.
- [x] `docs/06-planning/BUGS.md` обновлен так, чтобы текущий slice можно было честно закрывать дальше как completed task, а не как все еще бесконечный umbrella task id.

## Expected End State
- [x] `helm/amp/DEPLOYMENT.md` больше не описывает старый `alert-history` install/deploy path.
- [x] В in-scope Helm values/templates comments и description strings больше нет stale `Alert History` / `Production-Ready` markers.
- [x] Chart/template behavior не менялся; единственное intentional behavior-adjacent изменение — sanitization hardcoded `llm.apiKey` defaults в `values.yaml` и `values-dev.yaml`.
- [x] Remaining drift clusters (`examples`, `grafana`, `go-app/internal`) и дополнительные Helm files вне текущего sub-slice остаются explicit follow-up в отдельных bugs, а не скрываются под тем же task id.

## Open Assumptions
- [ ] Предполагается, что operator-facing rewrite `helm/amp/DEPLOYMENT.md` можно сделать честным без правок `helm/amp/README.md`.
- [ ] Предполагается, что `description` annotation в `postgresql-statefulset.yaml` и аналогичные human-facing strings не влияют на chart behavior, поэтому их cleanup остается в docs-only scope.
- [x] Literal LLM API key-like values в `helm/amp/values-dev.yaml` и `helm/amp/values.yaml` потребовали immediate sanitization и не могли быть оставлены как “отдельная потом security-задача”, пока эти файлы находятся в изменяемом scope.

## Blockers / Stop Conditions
- [ ] Если честный cleanup потребует менять actual values, template expressions или rendered manifest semantics, остановиться и вернуть задачу в planning.
- [ ] Если во время правки `DEPLOYMENT.md` выяснится, что для честного operator guide нужен новый runtime/chart claim, которого нет в `README.md`/`DECISIONS.md`, предпочесть более узкое wording вместо расширения truth.
- [x] User-facing/security вопрос вокруг secret-like values в `values-dev.yaml`/`values.yaml` пришлось закрыть сразу через sanitization, иначе дальнейший commit был бы небезопасным.
