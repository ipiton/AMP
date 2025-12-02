-- Create publishing_dlq table for failed jobs
-- Migration: 20251112150000_create_publishing_dlq
-- Description: Dead Letter Queue for failed publishing jobs
-- Retention: 30 days (configurable via cleanup worker)

-- +goose Up
CREATE TABLE IF NOT EXISTS publishing_dlq (
    -- Primary key
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    -- Job identification
    job_id UUID NOT NULL,
    fingerprint VARCHAR(255) NOT NULL,
    target_name VARCHAR(255) NOT NULL,
    target_type VARCHAR(50) NOT NULL, -- rootly, pagerduty, slack, webhook

    -- Alert data (JSONB for flexibility)
    enriched_alert JSONB NOT NULL,

    -- Target configuration (snapshot at failure time)
    target_config JSONB NOT NULL,

    -- Error tracking
    error_message TEXT NOT NULL,
    error_type VARCHAR(50) NOT NULL, -- transient, permanent, unknown
    retry_count INTEGER NOT NULL DEFAULT 0,
    last_retry_at TIMESTAMP,

    -- Job priority
    priority VARCHAR(20) NOT NULL DEFAULT 'medium', -- high, medium, low

    -- Timestamps
    failed_at TIMESTAMP NOT NULL DEFAULT NOW(),
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW(),

    -- Replay tracking
    replayed BOOLEAN NOT NULL DEFAULT FALSE,
    replayed_at TIMESTAMP,
    replay_result VARCHAR(50), -- success, failed, skipped

    -- Indexing
    CONSTRAINT chk_priority CHECK (priority IN ('high', 'medium', 'low')),
    CONSTRAINT chk_error_type CHECK (error_type IN ('transient', 'permanent', 'unknown')),
    CONSTRAINT chk_replay_result CHECK (replay_result IN ('success', 'failed', 'skipped', NULL))
);

-- Indexes for performance
CREATE INDEX IF NOT EXISTS idx_dlq_target_name ON publishing_dlq(target_name);
CREATE INDEX IF NOT EXISTS idx_dlq_error_type ON publishing_dlq(error_type);
CREATE INDEX IF NOT EXISTS idx_dlq_failed_at ON publishing_dlq(failed_at DESC);
CREATE INDEX IF NOT EXISTS idx_dlq_replayed ON publishing_dlq(replayed) WHERE replayed = FALSE;
CREATE INDEX IF NOT EXISTS idx_dlq_fingerprint ON publishing_dlq(fingerprint);
CREATE INDEX IF NOT EXISTS idx_dlq_job_id ON publishing_dlq(job_id);

-- Trigger for updated_at
CREATE OR REPLACE FUNCTION update_dlq_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trigger_dlq_updated_at
    BEFORE UPDATE ON publishing_dlq
    FOR EACH ROW
    EXECUTE FUNCTION update_dlq_updated_at();

-- Comment
COMMENT ON TABLE publishing_dlq IS 'Dead Letter Queue for failed publishing jobs (TN-056)';
COMMENT ON COLUMN publishing_dlq.job_id IS 'Original job UUID from PublishingJob.ID';
COMMENT ON COLUMN publishing_dlq.enriched_alert IS 'Full EnrichedAlert JSONB snapshot';
COMMENT ON COLUMN publishing_dlq.target_config IS 'Target configuration snapshot at failure time';
COMMENT ON COLUMN publishing_dlq.error_type IS 'Error classification: transient/permanent/unknown';
COMMENT ON COLUMN publishing_dlq.replayed IS 'Whether job was replayed (manual retry)';

-- +goose Down
DROP TRIGGER IF EXISTS trigger_dlq_updated_at ON publishing_dlq;
DROP FUNCTION IF EXISTS update_dlq_updated_at();
DROP INDEX IF EXISTS idx_dlq_job_id;
DROP INDEX IF EXISTS idx_dlq_fingerprint;
DROP INDEX IF EXISTS idx_dlq_replayed;
DROP INDEX IF EXISTS idx_dlq_failed_at;
DROP INDEX IF EXISTS idx_dlq_error_type;
DROP INDEX IF EXISTS idx_dlq_target_name;
DROP TABLE IF EXISTS publishing_dlq;
