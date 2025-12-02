-- Migration: Create silences table for silencing system
-- Created: 2025-11-04
-- Module: PHASE A - Module 3: Silencing System (TN-131)
-- Purpose: Store silence rules for temporarily suppressing alerts
-- Alertmanager API v2 compatible

-- ============================================================================
-- UP Migration
-- ============================================================================

-- Create silences table
CREATE TABLE IF NOT EXISTS silences (
    -- Primary key: UUID v4 identifier
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    -- Creator identification (email or username)
    created_by VARCHAR(255) NOT NULL,

    -- Required comment explaining the silence (min 3 chars, max 1024 chars)
    comment TEXT NOT NULL,

    -- Time range for the silence
    starts_at TIMESTAMP WITH TIME ZONE NOT NULL,
    ends_at TIMESTAMP WITH TIME ZONE NOT NULL,

    -- Label matchers stored as JSONB array
    -- Example: [{"name":"alertname","value":"HighCPU","type":"=","isRegex":false}]
    matchers JSONB NOT NULL,

    -- Current status: pending, active, or expired
    status VARCHAR(20) NOT NULL DEFAULT 'pending',

    -- Audit timestamps
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE,

    -- ========================================================================
    -- Constraints
    -- ========================================================================

    -- Ensure comment length is within valid range
    CONSTRAINT silences_valid_comment CHECK (
        length(comment) >= 3 AND length(comment) <= 1024
    ),

    -- Ensure time range is valid (EndsAt must be after StartsAt)
    CONSTRAINT silences_valid_time_range CHECK (
        ends_at > starts_at
    ),

    -- Ensure status is one of the valid values
    CONSTRAINT silences_valid_status CHECK (
        status IN ('pending', 'active', 'expired')
    )
);

-- ============================================================================
-- Indexes
-- ============================================================================

-- Index for filtering by status (most common query)
-- Partial index excludes expired silences to save space
-- Used in: WHERE status = 'active'
-- Used in: WHERE status IN ('pending', 'active')
CREATE INDEX IF NOT EXISTS idx_silences_status
ON silences(status)
WHERE status != 'expired';

-- Composite index for querying active silences (most common pattern)
-- Used in: WHERE status IN ('pending', 'active') ORDER BY ends_at
CREATE INDEX IF NOT EXISTS idx_silences_active
ON silences(status, ends_at)
WHERE status IN ('pending', 'active');

-- Index for time-based queries
-- Used in: WHERE starts_at >= ?
-- Used in: WHERE starts_at BETWEEN ? AND ?
CREATE INDEX IF NOT EXISTS idx_silences_starts_at
ON silences(starts_at);

-- Index for finding silences expiring soon
-- Used in: WHERE ends_at <= ? (for cleanup jobs)
CREATE INDEX IF NOT EXISTS idx_silences_ends_at
ON silences(ends_at);

-- Index for audit queries by creator
-- Used in: WHERE created_by = 'user@example.com'
CREATE INDEX IF NOT EXISTS idx_silences_created_by
ON silences(created_by);

-- GIN index for JSONB matchers (supports @>, ?, ?&, ?| operators)
-- Used in: WHERE matchers @> '[{"name":"alertname"}]'
-- Used in: WHERE matchers ? 'name'
-- Enables fast label-based lookups
CREATE INDEX IF NOT EXISTS idx_silences_matchers
ON silences USING GIN (matchers);

-- Index for listing recent silences
-- Used in: ORDER BY created_at DESC LIMIT 100
CREATE INDEX IF NOT EXISTS idx_silences_created_at
ON silences(created_at DESC);

-- ============================================================================
-- Index Statistics (Estimated)
-- ============================================================================
-- Expected database size for 10,000 active silences:
-- - Table data: ~5 MB (avg 500 bytes per row)
-- - idx_silences_status: ~50 KB
-- - idx_silences_active: ~100 KB
-- - idx_silences_starts_at: ~100 KB
-- - idx_silences_ends_at: ~100 KB
-- - idx_silences_created_by: ~200 KB
-- - idx_silences_matchers: ~1 MB (GIN index overhead)
-- - idx_silences_created_at: ~100 KB
-- Total: ~7 MB for 10K silences
--
-- Query performance improvements:
-- - Status filtering: ~100x faster (partial index)
-- - Active silences: ~50x faster (composite index)
-- - Label matching: ~20x faster (GIN index)
-- - Creator audit: ~50x faster (btree index)

-- ============================================================================
-- Comments on table and columns
-- ============================================================================

COMMENT ON TABLE silences IS
'Silence rules for temporarily suppressing alerts. Compatible with Alertmanager API v2.';

COMMENT ON COLUMN silences.id IS
'Unique identifier (UUID v4) for the silence.';

COMMENT ON COLUMN silences.created_by IS
'Email or username of the user who created this silence. Used for audit trail.';

COMMENT ON COLUMN silences.comment IS
'Required explanation for why this silence was created. Min 3, max 1024 characters.';

COMMENT ON COLUMN silences.starts_at IS
'Timestamp when the silence becomes active and starts suppressing matching alerts.';

COMMENT ON COLUMN silences.ends_at IS
'Timestamp when the silence expires. Must be after starts_at.';

COMMENT ON COLUMN silences.matchers IS
'JSONB array of label matchers defining which alerts to silence.
Format: [{"name":"label","value":"value","type":"=|!=|=~|!~","isRegex":bool}]';

COMMENT ON COLUMN silences.status IS
'Current status: pending (not yet active), active (currently suppressing), expired (ended).
Auto-calculated based on starts_at, ends_at, and current time.';

COMMENT ON COLUMN silences.created_at IS
'Timestamp when the silence was created. Set automatically by database.';

COMMENT ON COLUMN silences.updated_at IS
'Timestamp of the last update to this silence. NULL if never updated.';

-- ============================================================================
-- Example Queries
-- ============================================================================

-- Query 1: Get all active silences
-- SELECT * FROM silences WHERE status = 'active' ORDER BY ends_at;

-- Query 2: Get silences expiring in the next hour
-- SELECT * FROM silences
-- WHERE status = 'active' AND ends_at <= NOW() + INTERVAL '1 hour'
-- ORDER BY ends_at;

-- Query 3: Find silences matching a specific label
-- SELECT * FROM silences
-- WHERE status = 'active'
--   AND matchers @> '[{"name":"alertname","value":"HighCPU"}]';

-- Query 4: Get silences created by a specific user
-- SELECT * FROM silences
-- WHERE created_by = 'ops@example.com'
-- ORDER BY created_at DESC
-- LIMIT 100;

-- Query 5: Count silences by status
-- SELECT status, COUNT(*) FROM silences GROUP BY status;

-- ============================================================================
-- DOWN Migration (Rollback)
-- ============================================================================

-- To rollback this migration, uncomment and run:

-- -- Drop all indexes
-- DROP INDEX IF EXISTS idx_silences_created_at;
-- DROP INDEX IF EXISTS idx_silences_matchers;
-- DROP INDEX IF EXISTS idx_silences_created_by;
-- DROP INDEX IF EXISTS idx_silences_ends_at;
-- DROP INDEX IF EXISTS idx_silences_starts_at;
-- DROP INDEX IF EXISTS idx_silences_active;
-- DROP INDEX IF EXISTS idx_silences_status;

-- -- Drop the table (this will also drop all constraints)
-- DROP TABLE IF EXISTS silences;

-- ============================================================================
-- Migration Verification
-- ============================================================================

-- After running this migration, verify with:
--
-- 1. Check table exists:
--    SELECT EXISTS (SELECT 1 FROM information_schema.tables
--                   WHERE table_name = 'silences');
--
-- 2. Check columns:
--    SELECT column_name, data_type, is_nullable
--    FROM information_schema.columns
--    WHERE table_name = 'silences';
--
-- 3. Check indexes:
--    SELECT indexname, indexdef
--    FROM pg_indexes
--    WHERE tablename = 'silences';
--
-- 4. Check constraints:
--    SELECT constraint_name, constraint_type
--    FROM information_schema.table_constraints
--    WHERE table_name = 'silences';
--
-- 5. Test insert:
--    INSERT INTO silences (created_by, comment, starts_at, ends_at, matchers)
--    VALUES ('test@example.com', 'Test silence', NOW(), NOW() + INTERVAL '1 hour',
--            '[{"name":"alertname","value":"test","type":"=","isRegex":false}]');
--
-- 6. Test query:
--    SELECT * FROM silences WHERE status = 'active';

-- ============================================================================
-- End of Migration
-- ============================================================================
