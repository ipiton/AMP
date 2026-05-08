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

- [ ] **PARITY-B1-MUTE-TIME-INTERVALS** — maintenance windows (time interval parser, timezone, route wiring). ~5d
- [ ] **PARITY-B3-TELEGRAM-PUBLISHER** — популярен в СНГ. ~1-2d
- [ ] **PARITY-B6-WEB-ROUTE-PREFIX** — reverse proxy. ~0.5d

## WIP (Max 2)

_пусто — есть слот для старта следующей задачи_

## Notes
- Очередь обновлена 2026-05-08 после закрытия PHASE-6A (built-in tools для investigation-агента). Parity Phase A и Intelligence Phase 5A/5B/6A закрыты.
- **Приоритет 1**: PHASE-6B-RUNBOOK-ENGINE — markdown KB с auto-matching по alert labels, дополняет 6A tools и завершает Investigation Toolset.
- **Приоритет 2**: PHASE-5C-PROVIDER-FALLBACK — primary→fallback chain для LLM, повышает устойчивость 5B/6A.
- **Приоритет 3**: Operations (reloadable + sidecar) — закрывает hot reload story.
- **Приоритет 4**: Parity Phase B — по запросу, не критично.
- Parity Phase C (clustering, remaining receivers) и Intelligence Phase 6C/6D/7 остаются в BACKLOG.
- Завершённые задачи: см. `DONE.md`.
- Gap analysis: `docs/06-planning/ALERTMANAGER-REPLACEMENT-GAP-ANALYSIS.md`.
