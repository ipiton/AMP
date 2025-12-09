# Technical Decisions & Future Enhancements

**Last Updated:** 9 декабря 2024
**Проект:** AMP (Alertmanager++)

---

## 📋 Tracked TODO Items

### Future Features (Not Blocking)

#### 1. Circuit Breaker для LLM/Resilience
**Location:** `internal/core/resilience/resilience.go:124,138`

```go
// TODO: Implement circuit breaker
// TODO: Implement bulkhead
```

**Status:** ⏳ Pending
**Priority:** Medium
**Estimate:** 4-6 hours

**Decision:**
Отложено до Sprint 9/10. Circuit Breaker и Bulkhead паттерны будут реализованы как отдельные задачи:
- Task 9.12: Circuit Breaker (4h)
- Task 10.1: Bulkhead Pattern (3-4h)

**Rationale:**
- Не критично для базовой функциональности
- Требует careful design (thresholds, timeouts)
- Лучше внедрять после production deployment
- Will use proven libraries (gobreaker, etc.)

---

#### 2. Active Jobs Tracking в Queue
**Location:** `internal/infrastructure/publishing/queue.go:569`

```go
ActiveJobs: 0, // TODO: track active jobs in progress
```

**Status:** ⏳ Pending
**Priority:** Medium
**Estimate:** 2 hours

**Decision:**
Будет реализовано в Task 9.13.

**Implementation Plan:**
```go
type PublishingQueue struct {
    // ... existing fields
    activeJobs sync.Map  // map[string]*Job
    activeJobsGauge prometheus.Gauge
}

func (q *PublishingQueue) GetStatus() *QueueStatus {
    activeCount := 0
    q.activeJobs.Range(func(key, value interface{}) bool {
        activeCount++
        return true
    })
    return &QueueStatus{
        ActiveJobs: activeCount,
        // ...
    }
}
```

**Rationale:**
- Real-time monitoring important for production
- Helps with capacity planning
- Useful for debugging stuck jobs

---

#### 3. Queue Metrics Migration to v2
**Location:** `internal/infrastructure/publishing/queue_metrics_stub.go:5`

```go
// TODO: Migrate queue.go to use v2.PublishingMetrics directly.
```

**Status:** ⏳ Pending
**Priority:** High (cleanup)
**Estimate:** 1 hour

**Decision:**
Будет реализовано в Task 9.4.

**Action:**
1. Delete `queue_metrics_stub.go`
2. Update `queue.go` to use `v2.PublishingMetrics` directly
3. Update tests

**Rationale:**
- Remove temporary stub code
- Direct v2 usage cleaner
- Consistent with other modules

---

#### 4. Service Initialization TODOs
**Location:** `internal/application/service_registry.go:168,180,201,281,313`

```go
// TODO: Add migration runner
// TODO: Implement storage initialization
// TODO: Initialize Redis or Memory cache
// TODO: Initialize LLM client
// TODO: Initialize business services
```

**Status:** ⏳ Deferred
**Priority:** Low (future work)

**Decision:**
These are placeholders for services not yet implemented or not required for MVP.

**Services:**
- Migration runner: Can use external tool (migrate, goose)
- Storage initialization: Implemented when storage layer ready
- Cache: Redis integration planned for Phase 2
- LLM client: Already integrated, TODO can be removed
- Business services: Some initialized, some deferred

**Action:**
Document as future work, not blocking.

---

#### 5. Dynamic Timer TTL Calculation
**Location:** `internal/infrastructure/grouping/redis_group_storage.go:279`

```go
// TODO(TN-125-Phase2): Calculate dynamically based on timer metadata
```

**Status:** ⏳ Future Enhancement
**Priority:** Low

**Decision:**
Static TTL sufficient for MVP. Dynamic calculation can be added later based on:
- Alert frequency
- Group size
- Historical patterns

**Rationale:**
- Current approach works well
- Premature optimization
- Can add when we have production metrics

---

## 📊 TODO Summary

### By Priority

| Priority | Count | Status |
|----------|-------|--------|
| 🔴 High | 1 | Task 9.4 (queued) |
| 🟡 Medium | 3 | Tasks 9.12, 9.13, 10.1 (queued) |
| 🟢 Low | 12 | Documented, deferred |
| **Total** | **16** | **Under control** ✅ |

### By Category

| Category | Count | Action |
|----------|-------|--------|
| Resilience patterns | 2 | Sprint 9/10 |
| Monitoring | 1 | Task 9.13 |
| Code cleanup | 1 | Task 9.4 |
| Service init | 5 | Deferred |
| Future enhancements | 7 | Documented |

---

## ✅ Completed TODO Items

### 1. Retry Logic Unification
**Status:** ✅ Completed (Sprint 5)

**Tasks:**
- Created pkg/retry
- Migrated 5 modules
- -270 lines duplicate code

### 2. Metrics v2 Migration
**Status:** ✅ Completed (Sprints 1-4)

**Tasks:**
- Created pkg/metrics/v2
- Migrated 20+ files
- Deleted 9 deprecated files

### 3. Error Handling Unification
**Status:** ✅ Completed (Sprint 9.1)

**Tasks:**
- Created pkg/httperror/classifiers.go
- Migrated IsRetryable functions
- -80 lines duplicate code

---

## 🎯 Decision Log

### Circuit Breaker Implementation

**Decision:** Use `github.com/sony/gobreaker`
**Date:** 9 декабря 2024
**Rationale:**
- Battle-tested library
- Simple API
- Good defaults
- Prometheus metrics support

**Alternative Considered:**
- Custom implementation: Too complex, reinventing wheel
- `github.com/afex/hystrix-go`: More complex than needed

---

### Bulkhead Implementation

**Decision:** Custom implementation with `sync.Map` + semaphore
**Date:** 9 декабря 2024
**Rationale:**
- Simple pattern, no need for library
- Full control over behavior
- Easy to integrate with existing metrics

---

### GraphQL vs REST

**Decision:** Add GraphQL as optional layer (Sprint 10.3)
**Date:** 9 декабря 2024
**Rationale:**
- Keep REST for backward compatibility
- GraphQL for modern clients
- Best of both worlds

---

### ML Classification

**Decision:** Implement as optional feature (Sprint 10.5)
**Date:** 9 декабря 2024
**Rationale:**
- LLM classification works well
- ML can reduce cost/latency
- Not critical for MVP
- Can train model offline

---

## 📝 Notes

### Code Quality Philosophy

1. **Prefer simplicity over cleverness**
   - Remove complex recovery logic (Task 9.2) ✅
   - Use proven libraries over custom code
   - Document decisions clearly

2. **Defer non-critical features**
   - Focus on MVP first
   - Add enhancements incrementally
   - Validate in production first

3. **Track TODOs properly**
   - Document in TECHNICAL_DECISIONS.md
   - Link to tracking issues (TN-XXX)
   - Review quarterly

4. **Clean up aggressively**
   - Remove stub code (Task 9.4)
   - Delete deprecated code
   - Keep codebase lean

---

## 🔄 Review Schedule

- **Weekly:** New TODOs added?
- **Monthly:** Priority review
- **Quarterly:** Full audit

**Next Review:** 1 января 2025

---

**Status:** ✅ **ALL TODOs DOCUMENTED**
**Blocking TODOs:** 0
**Tracked for future:** 16
**Action items:** Clear (Sprint 9/10)
