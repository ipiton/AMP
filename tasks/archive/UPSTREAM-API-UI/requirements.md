---
id: UPSTREAM-API-UI
slug: upstream-api-ui
stream: Architecture / ADR-019
type: feature
priority: high
status: archived
created_at: 2026-11-08
updated_at: 2026-11-08
---

# Requirements: API среза 6 ADR-019 — `api/v2` upstream и AMP-маршруты истории и investigation

## Problem Framing

- **Symptom:** после среза 5 (`UPSTREAM-CORE-REMOVAL`) `amp-upstream` — единственный бинарь, и он отдаёт только upstream API (`/api/v2`, `/-/*`, `/metrics`, `/debug/*`). История алертов и результаты investigation пишутся в PostgreSQL (срез 2b), но прочитать их снаружи нельзя: `GET /api/v1/alerts/{fingerprint}/investigation` удалён вместе с `cmd/server`, а `/api/v1/*` у upstream отвечает 410. Строки истории и investigation ключены SHA-256 от labels (`alertconv.Fingerprint`), а `api/v2` показывает FNV-fingerprint upstream (16 hex): по fingerprint из `api/v2` найти свою историю нельзя даже в БД.
- **Root Cause:** эпик ADR-019 вынес AMP-маршруты в отдельный срез (6); ключ истории остался от своего ядра (2b: «no schema change»).
- **Why Now:** срез 6 эпика; после него срез 7 (доки и миграция) и релиз v0.1.0. Решение владельца 2026-11-08: в v0.1.0 только API, своего UI нет; UI — `UPSTREAM-UI` после релиза.
- **How We Measure:** `curl /api/amp/v1/alerts/<fingerprint из api/v2>` и `…/investigation` отдают запись из PostgreSQL; `/api/v2/*` побайтно ответы upstream (контрактные сьюты и amtool-гейт `scripts/upstream-core-gate.sh` без новых расхождений); `scripts/release-gate.sh` зелёный целиком.

## Risk Profile

- **Signals:** `C S M X R`
  - `C` — новые публичные маршруты AMP, удаление `GET /api/v2/inhibitions` и `GET /api/v1/alerts/{fp}/investigation` (уже удалены в срезе 5, здесь фиксируется окончательно), отказ от `?group_by=`/`global.group_by`.
  - `S` — новые маршруты отдают данные алертов и результаты investigation (там вывод инструментов: логи, k8s-события) — должны быть за той же basic auth, что и `api/v2`.
  - `M` — колонка `alerts.upstream_fingerprint` (обратимая миграция) и её заполнение для старых строк.
  - `X` — `cmd/amp-upstream` (композиция HTTP), `internal/infrastructure` (запись/чтение `alerts`), `migrations/`, новый HTTP-пакет.
  - `R` — путь записи истории на каждом Put меняется (одна колонка в upsert); фоновое заполнение при старте.
- **Tier:** Full
- **Notes:** deep-review обязателен (`S`, `M`).

## User Stories

1. Как оператор, я беру `fingerprint` алерта из `amtool alert query -o json` / `GET /api/v2/alerts` и получаю по нему запись истории AMP — в том числе после того, как upstream-store уже удалил resolved-алерт.
2. Как оператор, по тому же fingerprint я читаю последний результат investigation.
3. Как пользователь upstream-инструментов (amtool, karma, Grafana), я вижу `api/v2` ровно как у upstream Alertmanager v0.34.1.

## Success Criteria

- [ ] `GET /api/amp/v1/alerts/{fingerprint}` — запись истории; `{fingerprint}` — FNV из `api/v2` (16 hex) или SHA-256 AMP (64 hex).
- [ ] `GET /api/amp/v1/alerts/{fingerprint}/investigation` — последний investigation.
- [ ] Маршруты под префиксом `--web.external-url` и за basic auth `--web.config.file`, как `api/v2`.
- [ ] Без PostgreSQL (нет `--amp.config.file`, `profile: lite`, БД недоступна на старте) маршруты отвечают 503 с причиной, доставка не затронута.
- [ ] Всё, что не `/api/amp/`, обслуживает handler upstream-ядра без изменений; `upstream-core-gate.sh` — без новых расхождений.
- [ ] Миграция `upstream_fingerprint` обратимая, с тестом up/down/up; старые строки заполняются.
- [ ] `/api/v2/inhibitions` — решение (оставить/удалить) обосновано в spec; `?group_by=`, `global.group_by` — потеря записана для среза 7.
- [ ] `STATUS-VERSIONINFO-CONTRACT`, `COMPAT-VERSION-CONFIG-KEY` — проверены, BACKLOG обновлён.
- [ ] Breaking changes API — в `CHANGELOG.md` `[Unreleased]`.
- [ ] `scripts/release-gate.sh` зелёный целиком; `review-verdict.json` с `"gate": "pass"`.

## Non-Goals

- Свой UI (`UPSTREAM-UI`, после v0.1.0). UI upstream не встраивается.
- Список/пагинация/статистика истории (top, flapping) — `HISTORY-API` остаётся в BACKLOG.
- Чтение классификации по fingerprint (результат живёт в in-memory кэше) — для `UPSTREAM-UI`.
- Возврат `?group_by=` и `global.group_by`.
- README, `ALERTMANAGER_COMPATIBILITY.md`, migration guide — срез 7 (`UPSTREAM-DOCS-MIGRATION`).

## Constraints

- **Scope:** `cmd/amp-upstream`, `internal/infrastructure/postgres_storage_adapter.go`, `migrations/`, новый пакет HTTP-маршрутов AMP, planning и CHANGELOG. `internal/upstreamcore` и `api/v2` не трогаются.
- **Security:** маршруты за тем же `webauth`; без auth — как и весь API (предупреждение на старте уже есть). Fingerprint валидируется до запроса в БД; SQL только параметризованный.
- **Compatibility:** миграция аддитивная (nullable колонка + индекс), откат — `goose down`. История продолжает писаться тем же путём; `cmd/server` удалён, второго писателя нет.

## Discovery Notes

- Similar tasks: `tasks/archive/UPSTREAM-AI-HISTORY-INVESTIGATION/` (2b: запись истории и investigation), `tasks/archive/UPSTREAM-CORE-REMOVAL/` (5: удалены старые маршруты).
- Relevant patterns: старый `internal/application/handlers/investigation.go` (`git show 501ceaa^:go-app/internal/application/handlers/investigation.go`) — форма ответа investigation; `alertconv.UpstreamFingerprint` — FNV из labels; `webauth` в `cmd/amp-upstream/auth.go`.
- Open unknowns: нет (разобраны в `research.md`).
