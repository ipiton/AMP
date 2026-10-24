---
id: UPSTREAM-CORE-POC-GATE
slug: upstream-core-poc-gate
stream: Architecture / ADR-019
type: feature
priority: high
status: active
created_at: 2026-10-24
updated_at: 2026-10-24
---

# Requirements: контрактные сьюты и amtool на upstream-ядре, обновляемость ядра

## Problem Framing

- **Symptom:** ядро upstream Alertmanager v0.34.1 собрано в `cmd/amp-upstream` (`UPSTREAM-CORE-POC-WIRING`), но не проверено на контрактах, которыми AMP мерил свою совместимость: `main_phase0_contract_test.go` и `main_upstream_parity_regression_test.go` гоняются только на своём ядре, amtool-гейт — только на образе `cmd/server`. Вердикт по ADR-019 («PoC проходит — идём дальше, нет — пересмотр») не вынесен.
- **Обновляемость (требование владельца 2026-10-24):** ADR-019 ставит AMP на upstream ради того, чтобы получать его исправления; это работает, только если обновление ядра — рутина. Сейчас версия upstream записана в двух местах и они уже разошлись (`go.mod` — v0.34.1, amtool-образ в `scripts/release-gate.sh` — v0.34.0); порт `internal/upstreamcore` сверяется с upstream только вручную, по комментарию в `doc.go`; процедуры обновления нет.
- **Root Cause:** срез 1a намеренно ограничен сборкой; гейт и процесс обновления — этот срез.
- **Why Now:** срезы 2–4 строят на upstream-ядре; заложить обновление после них дороже — каждый срез добавит точек расхождения.
- **How We Measure:** сьюты запускаются на upstream-ядре одной командой, каждое падение классифицировано; amtool проходит на upstream-ядре версией из `go.mod`; смена версии в `go.mod` без сверки порта роняет тест; обновление выполняется скриптом с проверкой карантина.

## Risk Profile

- **Signals:** `X`
  - `X` — тестовый шов в `cmd/server`, `internal/upstreamcore`, `scripts/release-gate.sh`, CI.
- **Tier:** Full
- **Notes:** по сигналам — Standard, но дифф (шов, гейт, скрипт обновления, проверка дрейфа) заведомо больше ~200 строк — Full по `docs/solo-kanban/workflow.md` § Tiers. Вердикт по ADR-019 опирается на классификацию расхождений — независимая проверка ценна сама по себе. `C` нет: контракты `cmd/server` не меняются; `R` нет: образ и чарт не трогаются.

## User Stories

1. Как владелец, я хочу видеть, какие контракты AMP upstream-ядро держит и какие нет (и почему), чтобы принять решение по ADR-019.
2. Как разработчик среза 2–6, я хочу одну команду, которая гоняет контрактные сьюты на upstream-ядре, чтобы видеть регрессию своего встраивания.
3. Как сопровождающий, я хочу обновлять upstream одной процедурой — карантин, `go get`, дифф портированных файлов, гейт, — чтобы обновление было рутиной, а не исследованием.

## Success Criteria

- [ ] Сьюты `main_phase0_contract_test.go` и `main_upstream_parity_regression_test.go` запускаются на upstream-ядре без правки их тел (шов `registerRoutes` под отдельным build-тегом).
- [ ] Каждое падение на upstream-ядре классифицировано: (a) AMP-only поверхность — уходит в срез 6 `UPSTREAM-API-UI` или удаляется; (b) тест фиксирует поведение своего ядра, расходящееся с upstream — ошибка теста; (c) настоящий разрыв upstream-ядра относительно контракта AMP — решение владельца. Таблица в `tasks/<slug>/`.
- [ ] Ожидаемые падения (a) и (b) пропускаются явно, со ссылкой на классификацию; остальное зелёное — регрессионный гейт для срезов 2–6.
- [ ] amtool (`alert query`, `config show`, `silence add`, `silence query`) проходит на `cmd/amp-upstream`, версия amtool берётся из `go.mod`.
- [ ] Версия upstream — одна точка (`go.mod`); amtool-гейт `cmd/server` тоже берёт её оттуда, расхождение v0.34.0/v0.34.1 устранено.
- [ ] Тест дрейфа порта: версия, с которой портирован `internal/upstreamcore`, записана в коде и сравнивается с `go.mod`; бамп без сверки порта роняет тест.
- [ ] Скрипт обновления: проверка возраста версии (карантин 7 дней), `go get` + `go mod tidy`, дифф портированных upstream-файлов между старой и новой версией, запуск гейта. Процедура описана.
- [ ] Гейт на upstream-ядре — шаг `scripts/release-gate.sh` (и CI, если укладывается во время).
- [ ] Вердикт по ADR-019 — владельцу, в итоге задачи и `DONE.md`.

## Non-Goals

- Чинить разрывы класса (c) — только классифицировать и вынести на решение.
- Восстанавливать AMP-only поверхность (история, dashboard, `/api/v1`, `/api/v2/inhibitions`, classification) на upstream-ядре — срез 6.
- Автоматическое обновление (бот, расписание) — только ручная процедура одной командой.
- Сверка порта шаблонов `internal/business/templating` (v0.34.0) — он удаляется срезами 3/5; упомянуть в процедуре.
- Менять сами сьюты под upstream, кроме пометки ожидаемых падений.

## Constraints

- **Scope:** `cmd/server` (тестовый шов под build-тегом), `internal/upstreamcore`, `scripts/`, CI-workflow, docs-runbook; продовый код `cmd/server` и своё ядро не меняются.
- **Security:** не применимо (тесты и скрипты; карантин зависимостей — в скрипте обновления).
- **Compatibility:** гейт `cmd/server` (futureparity, amtool) продолжает работать на своём ядре до среза 5.

## Discovery Notes

- Similar tasks: `tasks/archive/UPSTREAM-CORE-POC-WIRING/` (ядро, точки встраивания, отличия от upstream).
- Relevant patterns: шов `registerRoutes` в `go-app/cmd/server/futureparity_compat.go` (build tag `futureparity`, `AMP_CONFIG_FILE`); конфиг сьютов уже в формате `alertmanager.yml`; amtool-шаг `scripts/release-gate.sh:271`.
- Найдено на start-task: `go run github.com/prometheus/alertmanager/cmd/amtool@v0.34.1` собирается по `go.mod` upstream — amtool можно гонять на хосте версией из нашего `go.mod`, без docker (заодно обходит локальную проблему bind-mount из `~/Documents`).
- Open unknowns: доля падений каждого класса; укладывается ли гейт в CI по времени; есть ли в сьютах зависимость от внутреннего состояния своего ядра (state file, `AMP_RUNTIME_STATE_FILE`).
