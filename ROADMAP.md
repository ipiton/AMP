# Alertmanager++ Roadmap

This document outlines the planned features and improvements for Alertmanager++.

## Version 1.x (Current)

### v1.0.0-preview (2025-12-02) ✅ Released

**Focus:** Initial OSS release with core features

- ✅ 100% Alertmanager API v2 compatibility
- ✅ Alert grouping, silencing, and inhibition
- ✅ Generic webhook publishing
- ✅ PostgreSQL and SQLite storage
- ✅ Redis caching support
- ✅ Kubernetes integration
- ✅ Prometheus metrics
- ✅ Helm charts (Lite profile)
- ✅ Migration guides and documentation
- ✅ Extension examples (classifier, publisher)

### v1.1.0 (Q1 2025) - Planned

**Focus:** BYK LLM & Community improvements

#### 🎯 Priority Features:
- [ ] **BYK (Bring Your own Key) LLM Integration** ⭐ **TOP PRIORITY**
  - Direct OpenAI/Anthropic API integration
  - User provides their own API keys (free feature!)
  - AI-powered alert classification
  - Smart enrichment modes (transparent/enriched)
  - Local LLM support (Ollama)
  - Expected: 7-9 hours implementation

#### 📦 Other Features:
- [ ] Enhanced Helm charts (Standard profile with PostgreSQL StatefulSet)
- [ ] Improved monitoring and observability
- [ ] Additional publisher examples (Discord, Telegram)
- [ ] Performance optimizations based on feedback
- [ ] Documentation improvements
- [ ] Bug fixes from community reports

### v1.2.0 (Q2 2025) - Planned

**Focus:** Advanced features

- [ ] Alert analytics and insights
- [ ] Advanced routing strategies
- [ ] Multi-tenancy support (OSS-friendly)
- [ ] Backup and disaster recovery
- [ ] Enhanced dashboard UI
- [ ] Mobile-responsive interface

### v1.3.0 (Q3 2025) - Planned

**Focus:** Scalability and HA

- [ ] Horizontal Pod Autoscaling (HPA)
- [ ] Active-Active HA mode
- [ ] Cross-region replication
- [ ] Advanced caching strategies
- [ ] Load balancing improvements

## Version 2.x (Future)

### v2.0.0 (2026) - Vision

**Focus:** Next-generation features

- [ ] GraphQL API
- [ ] WebSocket real-time updates
- [ ] Advanced alert correlation
- [ ] Time-series analysis
- [ ] Anomaly detection (community-driven ML)
- [ ] Multi-cluster support

## Feature Requests

Want to suggest a feature? Create a [Feature Request](https://github.com/ipiton/AMP/issues/new?template=feature_request.yml)!

## Community Priorities

We prioritize features based on:

1. **Community votes** - GitHub reactions on issues
2. **Use case impact** - How many users benefit
3. **Complexity** - Implementation effort
4. **Alignment** - Fits project goals
5. **Contributors** - Community PRs

## Contributing

Want to help build the roadmap? See [CONTRIBUTING.md](CONTRIBUTING.md)!

## Versioning

We follow [Semantic Versioning](https://semver.org/):
- **Major** (x.0.0) - Breaking changes
- **Minor** (1.x.0) - New features (backward compatible)
- **Patch** (1.0.x) - Bug fixes

---

**Last updated:** 2025-12-02
