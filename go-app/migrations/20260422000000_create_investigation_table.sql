-- +goose Up

CREATE TABLE IF NOT EXISTS alert_investigations (
    id                UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    fingerprint       VARCHAR(64) NOT NULL,
    classification_id BIGINT,     -- FK alert_classifications.id (nullable)

    -- Lifecycle status
    status            VARCHAR(20) NOT NULL DEFAULT 'queued',
    CONSTRAINT chk_inv_status CHECK (status IN ('queued','processing','completed','failed','dlq')),

    -- LLM investigation result
    summary           TEXT,
    findings          JSONB,
    recommendations   JSONB,
    confidence        DECIMAL(4,3),

    -- LLM meta
    llm_model         VARCHAR(100),
    prompt_tokens     INTEGER,
    completion_tokens INTEGER,
    processing_time   DECIMAL(8,3),

    -- Retry tracking
    retry_count       INTEGER NOT NULL DEFAULT 0,
    error_message     TEXT,
    error_type        VARCHAR(20),

    -- Timestamps
    queued_at         TIMESTAMP NOT NULL DEFAULT NOW(),
    started_at        TIMESTAMP,
    completed_at      TIMESTAMP,
    created_at        TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_inv_fingerprint     ON alert_investigations(fingerprint);
CREATE INDEX IF NOT EXISTS idx_inv_status          ON alert_investigations(status);
CREATE INDEX IF NOT EXISTS idx_inv_queued_at       ON alert_investigations(queued_at DESC);
CREATE INDEX IF NOT EXISTS idx_inv_classification  ON alert_investigations(classification_id);

-- +goose Down
DROP TABLE IF EXISTS alert_investigations;
