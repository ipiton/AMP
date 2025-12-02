-- Migration: Add performance indexes for alert filtering
-- Created: 2025-10-09
-- Purpose: Optimize alert filtering queries by severity, namespace, and labels

-- ============================================================================
-- UP Migration
-- ============================================================================

-- Index for filtering by status (firing/resolved)
-- Used in: WHERE status = 'firing'
CREATE INDEX IF NOT EXISTS idx_alerts_status
ON alerts(status);

-- Index for filtering by namespace
-- Used in: WHERE namespace = 'production'
CREATE INDEX IF NOT EXISTS idx_alerts_namespace
ON alerts(namespace);

-- Index for filtering by severity (from labels JSONB)
-- Used in: WHERE labels->>'severity' = 'critical'
CREATE INDEX IF NOT EXISTS idx_alerts_severity
ON alerts((labels->>'severity'));

-- Index for filtering by time range
-- Used in: WHERE starts_at >= ? AND starts_at <= ?
CREATE INDEX IF NOT EXISTS idx_alerts_starts_at
ON alerts(starts_at DESC);

-- Composite index for common query pattern: status + time
-- Used in: WHERE status = 'firing' AND starts_at >= ?
CREATE INDEX IF NOT EXISTS idx_alerts_status_time
ON alerts(status, starts_at DESC);

-- GIN index for JSONB labels (supports @> operator)
-- Used in: WHERE labels @> '{"env":"prod"}'
CREATE INDEX IF NOT EXISTS idx_alerts_labels_gin
ON alerts USING GIN(labels);

-- Index for fingerprint lookups (primary key already exists, but explicit for clarity)
-- Used in: WHERE fingerprint = ?
-- Note: Primary key already provides this, but documented here for completeness

-- Partial index for active alerts (firing status)
-- Used in: WHERE status = 'firing' AND starts_at >= ?
CREATE INDEX IF NOT EXISTS idx_alerts_active
ON alerts(starts_at DESC)
WHERE status = 'firing';

-- ============================================================================
-- Index Statistics
-- ============================================================================
-- Expected benefits:
-- - Severity filtering: ~100x faster (full table scan -> index scan)
-- - Namespace filtering: ~50x faster (full table scan -> index scan)
-- - Time range queries: ~20x faster (optimized for DESC sorting)
-- - Label filtering: ~10x faster (GIN index for JSONB)
-- - Combined filters: exponential improvement
--
-- Index sizes (estimated):
-- - idx_alerts_status: ~100 KB per 100K alerts
-- - idx_alerts_namespace: ~500 KB per 100K alerts
-- - idx_alerts_severity: ~500 KB per 100K alerts
-- - idx_alerts_starts_at: ~1 MB per 100K alerts
-- - idx_alerts_labels_gin: ~5 MB per 100K alerts
-- Total overhead: ~7 MB per 100K alerts

-- ============================================================================
-- DOWN Migration
-- ============================================================================

-- To rollback, uncomment and run:
-- DROP INDEX IF EXISTS idx_alerts_active;
-- DROP INDEX IF EXISTS idx_alerts_labels_gin;
-- DROP INDEX IF EXISTS idx_alerts_status_time;
-- DROP INDEX IF EXISTS idx_alerts_starts_at;
-- DROP INDEX IF EXISTS idx_alerts_severity;
-- DROP INDEX IF EXISTS idx_alerts_namespace;
-- DROP INDEX IF EXISTS idx_alerts_status;
