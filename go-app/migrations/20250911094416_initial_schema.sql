-- +goose Up
-- Migration 001: Initial PostgreSQL Schema
-- Created: 2024-12-19
-- Description: Initial schema migration with all tables, indexes, and triggers

-- Enable required extensions
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
CREATE EXTENSION IF NOT EXISTS "btree_gin";

-- Alerts table - main table for storing alerts
CREATE TABLE IF NOT EXISTS alerts (
    id BIGSERIAL PRIMARY KEY,
    fingerprint VARCHAR(64) NOT NULL,
    alert_name VARCHAR(255) NOT NULL,
    namespace VARCHAR(255),
    status VARCHAR(20) NOT NULL DEFAULT 'firing',
    labels JSONB NOT NULL DEFAULT '{}',
    annotations JSONB NOT NULL DEFAULT '{}',
    starts_at TIMESTAMP WITH TIME ZONE,
    ends_at TIMESTAMP WITH TIME ZONE,
    generator_url TEXT,
    timestamp TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- LLM Classifications table
CREATE TABLE IF NOT EXISTS alert_classifications (
    id BIGSERIAL PRIMARY KEY,
    alert_fingerprint VARCHAR(64) NOT NULL,
    severity VARCHAR(20) NOT NULL,
    confidence DECIMAL(4,3) NOT NULL CHECK (confidence >= 0 AND confidence <= 1),
    reasoning TEXT,
    recommendations JSONB DEFAULT '[]',
    processing_time DECIMAL(8,3),
    metadata JSONB DEFAULT '{}',
    llm_model VARCHAR(100),
    llm_version VARCHAR(50),
    cache_hit BOOLEAN DEFAULT FALSE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMP WITH TIME ZONE
);

-- Publishing History table
CREATE TABLE IF NOT EXISTS alert_publishing_history (
    id BIGSERIAL PRIMARY KEY,
    alert_fingerprint VARCHAR(64) NOT NULL,
    target_name VARCHAR(100) NOT NULL,
    target_type VARCHAR(50) NOT NULL,
    target_format VARCHAR(50) NOT NULL,
    status VARCHAR(20) NOT NULL,
    attempt_number INTEGER NOT NULL DEFAULT 1,
    response_code INTEGER,
    response_message TEXT,
    payload_size INTEGER,
    processing_time DECIMAL(8,3),
    error_details JSONB,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- Filter Rules table
CREATE TABLE IF NOT EXISTS filter_rules (
    id BIGSERIAL PRIMARY KEY,
    name VARCHAR(100) NOT NULL UNIQUE,
    target_name VARCHAR(100),
    action VARCHAR(20) NOT NULL CHECK (action IN ('allow', 'deny', 'delay')),
    conditions JSONB NOT NULL,
    priority INTEGER NOT NULL DEFAULT 100,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_by VARCHAR(100),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- Publishing Targets table
CREATE TABLE IF NOT EXISTS publishing_targets (
    id BIGSERIAL PRIMARY KEY,
    name VARCHAR(100) NOT NULL UNIQUE,
    type VARCHAR(50) NOT NULL,
    url TEXT NOT NULL,
    format VARCHAR(50) NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    headers JSONB DEFAULT '{}',
    filter_config JSONB DEFAULT '{}',
    circuit_breaker_config JSONB DEFAULT '{}',
    last_success_at TIMESTAMP WITH TIME ZONE,
    last_failure_at TIMESTAMP WITH TIME ZONE,
    failure_count INTEGER DEFAULT 0,
    total_attempts INTEGER DEFAULT 0,
    success_count INTEGER DEFAULT 0,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- System Metrics table
CREATE TABLE IF NOT EXISTS system_metrics (
    id BIGSERIAL PRIMARY KEY,
    metric_name VARCHAR(100) NOT NULL,
    metric_type VARCHAR(20) NOT NULL,
    labels JSONB DEFAULT '{}',
    value DECIMAL(15,6) NOT NULL,
    timestamp TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- Migration History table
CREATE TABLE IF NOT EXISTS migration_history (
    id BIGSERIAL PRIMARY KEY,
    version VARCHAR(50) NOT NULL UNIQUE,
    description TEXT,
    applied_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    execution_time DECIMAL(8,3),
    checksum VARCHAR(64)
);

-- Create indexes
CREATE UNIQUE INDEX IF NOT EXISTS idx_alerts_fingerprint_unique ON alerts(fingerprint);
CREATE INDEX IF NOT EXISTS idx_alerts_alert_name ON alerts(alert_name);
CREATE INDEX IF NOT EXISTS idx_alerts_namespace ON alerts(namespace);
CREATE INDEX IF NOT EXISTS idx_alerts_status ON alerts(status);
CREATE INDEX IF NOT EXISTS idx_alerts_timestamp ON alerts(timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_alerts_created_at ON alerts(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_alerts_labels_gin ON alerts USING GIN(labels);
CREATE INDEX IF NOT EXISTS idx_alerts_annotations_gin ON alerts USING GIN(annotations);
CREATE INDEX IF NOT EXISTS idx_alerts_name_status_time ON alerts(alert_name, status, timestamp DESC);

CREATE INDEX IF NOT EXISTS idx_classifications_fingerprint ON alert_classifications(alert_fingerprint);
CREATE INDEX IF NOT EXISTS idx_classifications_severity ON alert_classifications(severity);
CREATE INDEX IF NOT EXISTS idx_classifications_confidence ON alert_classifications(confidence DESC);
CREATE INDEX IF NOT EXISTS idx_classifications_created_at ON alert_classifications(created_at DESC);

CREATE INDEX IF NOT EXISTS idx_publishing_history_fingerprint ON alert_publishing_history(alert_fingerprint);
CREATE INDEX IF NOT EXISTS idx_publishing_history_target ON alert_publishing_history(target_name);
CREATE INDEX IF NOT EXISTS idx_publishing_history_status ON alert_publishing_history(status);
CREATE INDEX IF NOT EXISTS idx_publishing_history_created_at ON alert_publishing_history(created_at DESC);

CREATE INDEX IF NOT EXISTS idx_filter_rules_target ON filter_rules(target_name) WHERE target_name IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_filter_rules_priority ON filter_rules(priority) WHERE enabled = TRUE;

CREATE INDEX IF NOT EXISTS idx_publishing_targets_enabled ON publishing_targets(enabled) WHERE enabled = TRUE;

CREATE INDEX IF NOT EXISTS idx_system_metrics_name_time ON system_metrics(metric_name, timestamp DESC);

-- Create triggers for updated_at
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER update_alerts_updated_at
    BEFORE UPDATE ON alerts
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_alert_classifications_updated_at
    BEFORE UPDATE ON alert_classifications
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_filter_rules_updated_at
    BEFORE UPDATE ON filter_rules
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_publishing_targets_updated_at
    BEFORE UPDATE ON publishing_targets
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

-- Create views
CREATE OR REPLACE VIEW alerts_with_classification AS
SELECT
    a.*,
    ac.severity as llm_severity,
    ac.confidence as llm_confidence,
    ac.reasoning as llm_reasoning,
    ac.recommendations as llm_recommendations,
    ac.llm_model,
    ac.created_at as classification_time
FROM alerts a
LEFT JOIN alert_classifications ac ON a.fingerprint = ac.alert_fingerprint
    AND ac.created_at = (
        SELECT MAX(created_at)
        FROM alert_classifications ac2
        WHERE ac2.alert_fingerprint = a.fingerprint
    );

CREATE OR REPLACE VIEW publishing_stats AS
SELECT
    target_name,
    target_type,
    target_format,
    COUNT(*) as total_attempts,
    COUNT(*) FILTER (WHERE status = 'success') as successful_publishes,
    COUNT(*) FILTER (WHERE status = 'failure') as failed_publishes,
    COUNT(*) FILTER (WHERE status = 'filtered') as filtered_publishes,
    AVG(processing_time) FILTER (WHERE status = 'success') as avg_success_time,
    MAX(created_at) as last_publish_time
FROM alert_publishing_history
GROUP BY target_name, target_type, target_format;

-- Create cleanup function
CREATE OR REPLACE FUNCTION cleanup_old_data(retention_days integer DEFAULT 30)
RETURNS integer AS $$
DECLARE
    deleted_count integer := 0;
    cutoff_date timestamp with time zone;
BEGIN
    cutoff_date := NOW() - (retention_days || ' days')::interval;

    DELETE FROM alerts WHERE created_at < cutoff_date;
    GET DIAGNOSTICS deleted_count = ROW_COUNT;

    DELETE FROM alert_publishing_history WHERE created_at < cutoff_date - interval '30 days';
    DELETE FROM alert_classifications WHERE expires_at IS NOT NULL AND expires_at < NOW();
    DELETE FROM system_metrics WHERE timestamp < NOW() - interval '7 days';

    RETURN deleted_count;
END;
$$ LANGUAGE plpgsql;

-- Insert default filter rules
INSERT INTO filter_rules (name, action, conditions, priority, enabled) VALUES
('block_noise_alerts', 'deny', '{"llm_severity": "noise"}', 10, TRUE),
('allow_critical_alerts', 'allow', '{"llm_severity": "critical"}', 1, TRUE),
('min_confidence_threshold', 'deny', '{"llm_confidence_below": 0.3, "has_llm_classification": true}', 20, TRUE)
ON CONFLICT (name) DO NOTHING;

-- +goose Down
-- Rollback migration: drop all tables and related objects

-- Drop triggers
DROP TRIGGER IF EXISTS update_alerts_updated_at ON alerts;
DROP TRIGGER IF EXISTS update_alert_classifications_updated_at ON alert_classifications;
DROP TRIGGER IF EXISTS update_filter_rules_updated_at ON filter_rules;
DROP TRIGGER IF EXISTS update_publishing_targets_updated_at ON publishing_targets;

-- Drop functions
DROP FUNCTION IF EXISTS update_updated_at_column();
DROP FUNCTION IF EXISTS cleanup_old_data(integer);

-- Drop views
DROP VIEW IF EXISTS alerts_with_classification;
DROP VIEW IF EXISTS publishing_stats;

-- Drop indexes
DROP INDEX IF EXISTS idx_alerts_fingerprint_unique;
DROP INDEX IF EXISTS idx_alerts_alert_name;
DROP INDEX IF EXISTS idx_alerts_namespace;
DROP INDEX IF EXISTS idx_alerts_status;
DROP INDEX IF EXISTS idx_alerts_timestamp;
DROP INDEX IF EXISTS idx_alerts_created_at;
DROP INDEX IF EXISTS idx_alerts_labels_gin;
DROP INDEX IF EXISTS idx_alerts_annotations_gin;
DROP INDEX IF EXISTS idx_alerts_name_status_time;

DROP INDEX IF EXISTS idx_classifications_fingerprint;
DROP INDEX IF EXISTS idx_classifications_severity;
DROP INDEX IF EXISTS idx_classifications_confidence;
DROP INDEX IF EXISTS idx_classifications_created_at;

DROP INDEX IF EXISTS idx_publishing_history_fingerprint;
DROP INDEX IF EXISTS idx_publishing_history_target;
DROP INDEX IF EXISTS idx_publishing_history_status;
DROP INDEX IF EXISTS idx_publishing_history_created_at;

DROP INDEX IF EXISTS idx_filter_rules_target;
DROP INDEX IF EXISTS idx_filter_rules_priority;

DROP INDEX IF EXISTS idx_publishing_targets_enabled;

DROP INDEX IF EXISTS idx_system_metrics_name_time;

-- Drop tables
DROP TABLE IF EXISTS migration_history;
DROP TABLE IF EXISTS system_metrics;
DROP TABLE IF EXISTS publishing_targets;
DROP TABLE IF EXISTS filter_rules;
DROP TABLE IF EXISTS alert_publishing_history;
DROP TABLE IF EXISTS alert_classifications;
DROP TABLE IF EXISTS alerts;

-- Drop extensions (be careful - these might be used by other databases)
-- DROP EXTENSION IF EXISTS "btree_gin";
-- DROP EXTENSION IF EXISTS "uuid-ossp";
