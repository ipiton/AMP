# Requirements: SECONDARY-REPO-DOC-HISTORICAL-DRIFT

## Context
После закрытия `REPO-DOC-LICENSE-DRIFT` top-level public/docs truth уже приведен к честному состоянию, но в репозитории остаются более глубокие historical markers вне того narrow four-file scope. Текущий `BUGS.md` фиксирует residual drift в secondary internal docs, chart comments, example source comments, Grafana assets и других non-top-level файлах, в первую очередь под:

- `go-app/internal/**`
- `helm/amp/**`
- `grafana/**`
- `examples/**/*.go`

Это уже не ломает текущий public narrative, но оставляет repo-local docs/comments/assets несогласованными с активным source of truth про `AGPL-3.0`, `controlled replacement` и active-runtime-first contract.

## Goals
- [x] Собрать и сузить следующий mergeable cleanup slice внутри `SECONDARY-REPO-DOC-HISTORICAL-DRIFT`, а не пытаться silently закрыть весь residual repo-doc хвост за один проход.
- [x] Убрать или скорректировать stale historical markers в выбранном Helm slice без изменения runtime behavior, API surface или product claims сверх уже принятого planning truth.
- [x] Сохранить явную границу между тем, что закрывается в этом slice, и тем, что должно остаться отдельным follow-up в том же домене, но уже под отдельными bug ids.

## Constraints
- Трогать только docs/comments/assets и planning artifacts, если это потребуется для фиксации verified result.
- Не превращать задачу в runtime/API/refactor pass, даже если рядом встретятся code-level несоответствия.
- Опора на текущий source of truth: `README.md`, top-level docs, `docs/06-planning/DECISIONS.md`, `docs/06-planning/BUGS.md`, уже закрытые doc-honesty slices.
- Поскольку текущий bug широк по поверхности, следующий шаг должен пройти через `/research` и определить фактический mergeable sub-scope.

## Success Criteria (Definition of Done)
- [x] Создан task workspace и branch для `SECONDARY-REPO-DOC-HISTORICAL-DRIFT`.
- [x] Зафиксирован стартовый requirements baseline для дальнейшего `/research`.
- [x] Implementation scope сужен до Helm operator-facing docs/comments slice и verified как отдельный mergeable cleanup pass, а remaining domain декомпозирован в planning на отдельные follow-up bugs.

## Outcome
- `helm/amp/DEPLOYMENT.md` переписан под current `./helm/amp` path, `amp` naming и active-runtime-first operator story.
- В выбранных Helm values/templates comments и description strings убраны stale `Alert History` / `Production-Ready` markers.
- Hardcoded `llm.apiKey` defaults в `helm/amp/values.yaml` и `helm/amp/values-dev.yaml` санитизированы.
- Оставшийся umbrella-хвост разложен на отдельные follow-up bugs вместо сохранения одного неограниченного residual task id.
