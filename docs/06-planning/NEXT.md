# Очередь (Queue) и WIP

## Queue

### 1. Intelligence — Investigation Toolset (AMP differentiator)
> Цель: AI-powered alert investigation — главный USP AMP. Phase 5A/5B закрыты, осталось наполнить агента реальными tools.
> Reference: SherlockOps, HolmesGPT, Keep.

- [ ] **PHASE-6B-RUNBOOK-ENGINE** — Markdown knowledge base с auto-matching по alert labels. ~2d
- [ ] **PHASE-5C-PROVIDER-FALLBACK** — Primary → fallback chain (Claude → OpenAI → Ollama), cost tracking, per-env provider config. ~2d

### 2. Operations (из AMP-OSS)
- [ ] **RELOADABLE-COMPONENT-INTERFACES** — per-component Reloadable + wiring в ReloadCoordinator. ~2d
- [ ] **CONFIG-RELOADER-SIDECAR** — K8s sidecar для ConfigMap-driven SIGHUP. ~1d
- [ ] **HELM-PRODUCTION-VALUES** — `values-production.yaml` (PG cluster, DragonflyDB, sidecar). ~0.5d

### 3. Alertmanager Parity — Phase B (feature parity)
> Необязательно для controlled replacement, но закрывает полный feature set Alertmanager.
> DELIVERED via feat/alertmanager-parity (Phase 1-7, 2026-08-18): PARITY-B1/B3/B6 shipped; deferred follow-ups in BACKLOG.

- (absorbed by AMP-PARITY, see DONE.md 2026-08-18)

## WIP (Max 2)

- [x] **AMP-PARITY** (завершено 2026-08-18, см. DONE.md) — все фазы + финальная fix-волна и follow-ups влиты в main. Drop-in замена Alertmanager (routing tree, dispatcher/grouping, mute_time_intervals, API parity, config validation, Redis HA clustering, receivers). 29 task slices Phases 1-7 delivered; e2e+HA green. Plan: `docs/plans/alertmanager-parity.md`, ветка `feat/alertmanager-parity`, task workspace `tasks/AMP-PARITY/`. Follow-ups: BACKLOG «AMP-PARITY Follow-ups».

## Notes
- Очередь обновлена 2026-05-08 после закрытия PHASE-6A (built-in tools для investigation-агента). Parity Phase A и Intelligence Phase 5A/5B/6A закрыты.
- **Приоритет 1**: PHASE-6B-RUNBOOK-ENGINE — markdown KB с auto-matching по alert labels, дополняет 6A tools и завершает Investigation Toolset.
- **Приоритет 2**: PHASE-5C-PROVIDER-FALLBACK — primary→fallback chain для LLM, повышает устойчивость 5B/6A.
- **Приоритет 3**: Operations (reloadable + sidecar) — закрывает hot reload story.
- **Приоритет 4**: Parity Phase B — по запросу, не критично.
- Parity Phase C (clustering, remaining receivers) и Intelligence Phase 6C/6D/7 остаются в BACKLOG.
- Завершённые задачи: см. `DONE.md`.
- Gap analysis: `docs/06-planning/ALERTMANAGER-REPLACEMENT-GAP-ANALYSIS.md`.
