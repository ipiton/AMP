# Очередь (Queue) и WIP

## Queue

### 1. Alertmanager Parity — Phase A (production-viable)
> Цель: AMP пригоден как замена Alertmanager для production (без maintenance windows и HA).


### 2. Intelligence — Two-Phase Pipeline (AMP differentiator)
> Цель: AI-powered alert investigation — то, что отличает AMP от Alertmanager.
> Reference: SherlockOps, HolmesGPT, Keep.

- [ ] **PHASE-6A-BUILTIN-TOOLS** — Prometheus (PromQL) + Loki (LogQL) + K8s (pods/events/logs) + DB tools. ~7d
- [ ] **PHASE-6B-RUNBOOK-ENGINE** — Markdown knowledge base с auto-matching по alert labels. ~2d

### 3. Operations (из AMP-OSS)
- [ ] **RELOADABLE-COMPONENT-INTERFACES** — per-component Reloadable + wiring в ReloadCoordinator. ~2d
- [ ] **CONFIG-RELOADER-SIDECAR** — K8s sidecar для ConfigMap-driven SIGHUP. ~1d

## WIP (Max 2)

- [ ] **PHASE-5A-INVESTIGATION-PIPELINE** — Двухфазный async pipeline (Phase 1: existing flow, Phase 2: AI investigation). ~3d (started: 2026-04-22)
## Notes
- Очередь обновлена 2026-04-16 после полного аудита AMP и исследования SherlockOps/HolmesGPT/Keep.
- **Приоритет 1**: Parity Phase A — без этого AMP не может заменить Alertmanager.
- **Приоритет 2**: Intelligence pipeline — это USP (unique selling point) AMP. Alertmanager этого не умеет. SherlockOps — reference implementation на Go.
- **Приоритет 3**: Operations — из AMP-OSS, hot reload infra.
- Parity Phase B/C и Intelligence Phase 6C/6D/7 остаются в BACKLOG.
- Завершённые задачи: см. `DONE.md`.
- Gap analysis: `docs/06-planning/ALERTMANAGER-REPLACEMENT-GAP-ANALYSIS.md`.
