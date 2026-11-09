---
id: PROD-RELEASE-V010
slug: prod-release-v010
stream: Production Readiness / Delivery
type: feature
priority: high
status: in-progress
created_at: 2026-11-09
updated_at: 2026-11-09
---

# Requirements: первый релиз AMP v0.1.0

> Подготовка версий сделана в `PROD-RELEASE-V010-PREP` (`tasks/archive/PROD-RELEASE-V010-PREP/`): `appVersion` — единственный источник версии, guard «тег == `appVersion`» в `release.yml`. Spec v1.0 всего релиза — `git show f04187f:tasks/PROD-RELEASE-V010/Spec.md`; его §1–3 и §7 закрыты PREP, ядро с тех пор заменено (ADR-019), поэтому здесь — только остаток. Владелец дал старт 2026-11-09, после мержа всех семи срезов ADR-019.

## Problem Framing

- **Symptom:** релиза нет (теги `v0.0.1`/`v0.0.2`, 2025-12). `CHANGELOG` — только `[Unreleased]`, release notes — черновик. Срез 7 ADR-019 нашёл расхождения доков и кода, часть из них меняет то, что ставится по умолчанию.
- **Root Cause:** релиз откладывался (2026-09-30, 2026-10-07) до замены ядра на upstream (ADR-019).
- **Why Now:** эпик ADR-019 закрыт; дальше — выкатка в собственный k3s владельца с образа из GHCR.
- **How We Measure:**
  - `CHANGELOG.md` и `helm/amp/CHANGELOG.md` закрыты в `[0.1.0] - 2026-11-09`, `docs/RELEASE_NOTES_v0.1.0.md` собран по `WORKFLOW.md` § Release Process;
  - чарт по умолчанию не поднимает неиспользуемый Valkey; удалённые вложенные ключи values валят рендер с заменой;
  - собственные метрики AMP — только `amp_*` (рядом с upstream `alertmanager_*`);
  - `scripts/release-gate.sh` PASS; pre-release `deep-review` → `pass`;
  - после подтверждения владельца: push `main`, CI зелёный, `v0.1.0-rc.1` опубликован и проверен (анонимный pull amd64/arm64, `/api/v2/status`, стартовый лог), затем `v0.1.0` на тот же коммит.

## Risk Profile

- **Signals:** `C R`
  - `C` — Helm values: `valkey.enabled` теперь читается (дефолт `false`), вложенные удалённые ключи валят рендер; имена метрик `alert_history_*` → `amp_*`. До первого тега — дешевле, чем после.
  - `R` — первый публичный образ и чарт; выкатка в k3s владельца.
- **Tier:** Standard
- **Notes:** pre-release `deep-review` обязателен (`WORKFLOW.md`): тег публикует образы в GHCR. `S` не затронут: auth, RBAC и release.yml не меняются. `M` нет: схема БД не меняется.

## Scope

- In: расхождения из отчёта среза 7 (`DONE.md`, UPSTREAM-DOCS-MIGRATION), закрытие CHANGELOG, release notes, deep-review, finalize; push/теги/выкатка — только с подтверждения владельца.
- Out: `UPSTREAM-HISTORY-PG-RECONNECT`, `UPSTREAM-AI-HA-DEDUP`, `UPSTREAM-UI` (Known Gaps в release notes); эффект `investigation.only_firing=false` (задокументирован как no-op).
