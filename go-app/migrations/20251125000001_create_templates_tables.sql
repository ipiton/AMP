-- +goose Up
-- TN-155: Template API (CRUD) - Database Schema
-- Created: 2025-11-25
-- Quality Target: 150% (Grade A+ EXCEPTIONAL)

-- ============================================================================
-- Table: templates
-- Purpose: Store notification templates for Slack, PagerDuty, Email, WebHook
-- ============================================================================

CREATE TABLE IF NOT EXISTS templates (
    -- Primary key
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    -- Core fields
    name VARCHAR(64) NOT NULL,
    type VARCHAR(20) NOT NULL,
    content TEXT NOT NULL,
    description TEXT,
    metadata JSONB DEFAULT '{}'::jsonb,

    -- Version tracking
    version INTEGER NOT NULL DEFAULT 1,

    -- Timestamps
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- Audit trail
    created_by VARCHAR(255),
    updated_by VARCHAR(255),

    -- Soft delete
    deleted_at TIMESTAMPTZ,

    -- Constraints
    CONSTRAINT templates_name_unique UNIQUE (name),
    CONSTRAINT templates_name_format CHECK (name ~ '^[a-z0-9_]+$'),
    CONSTRAINT templates_type_enum CHECK (type IN ('slack', 'pagerduty', 'email', 'webhook', 'generic')),
    CONSTRAINT templates_content_length CHECK (length(content) > 0 AND length(content) <= 65536),
    CONSTRAINT templates_description_length CHECK (description IS NULL OR length(description) <= 500),
    CONSTRAINT templates_version_positive CHECK (version > 0)
);

-- Indexes for performance
CREATE INDEX idx_templates_name ON templates(name) WHERE deleted_at IS NULL;
CREATE INDEX idx_templates_type ON templates(type) WHERE deleted_at IS NULL;
CREATE INDEX idx_templates_created_at ON templates(created_at DESC);
CREATE INDEX idx_templates_updated_at ON templates(updated_at DESC);
CREATE INDEX idx_templates_deleted_at ON templates(deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX idx_templates_metadata_gin ON templates USING GIN (metadata);

-- Full-text search index
CREATE INDEX idx_templates_search ON templates USING GIN (
    to_tsvector('english', coalesce(name, '') || ' ' || coalesce(description, ''))
) WHERE deleted_at IS NULL;

-- Comment on table
COMMENT ON TABLE templates IS 'Notification templates for alert receivers (TN-155)';
COMMENT ON COLUMN templates.name IS 'Unique template name (lowercase alphanumeric + underscore)';
COMMENT ON COLUMN templates.type IS 'Template type: slack, pagerduty, email, webhook, generic';
COMMENT ON COLUMN templates.content IS 'Go text/template content (max 64KB)';
COMMENT ON COLUMN templates.metadata IS 'JSON metadata (author, tags, etc.)';
COMMENT ON COLUMN templates.version IS 'Current version number (auto-increment on update)';

-- ============================================================================
-- Table: template_versions
-- Purpose: Store historical versions for rollback capability
-- ============================================================================

CREATE TABLE IF NOT EXISTS template_versions (
    -- Primary key
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    -- Foreign key
    template_id UUID NOT NULL REFERENCES templates(id) ON DELETE CASCADE,

    -- Version info
    version INTEGER NOT NULL,

    -- Historical content
    content TEXT NOT NULL,
    description TEXT,
    metadata JSONB DEFAULT '{}'::jsonb,

    -- Audit trail
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_by VARCHAR(255),
    change_summary TEXT,

    -- Unique constraint on template + version
    CONSTRAINT template_versions_unique UNIQUE (template_id, version),
    CONSTRAINT template_versions_version_positive CHECK (version > 0),
    CONSTRAINT template_versions_content_length CHECK (length(content) > 0),
    CONSTRAINT template_versions_change_summary_length CHECK (change_summary IS NULL OR length(change_summary) <= 1000)
);

-- Indexes for version queries
CREATE INDEX idx_template_versions_template_id ON template_versions(template_id);
CREATE INDEX idx_template_versions_template_version ON template_versions(template_id, version DESC);
CREATE INDEX idx_template_versions_created_at ON template_versions(created_at DESC);

-- Comment on table
COMMENT ON TABLE template_versions IS 'Historical versions of templates for rollback (TN-155)';
COMMENT ON COLUMN template_versions.version IS 'Version number (matches templates.version at time of save)';
COMMENT ON COLUMN template_versions.change_summary IS 'Optional description of what changed';

-- ============================================================================
-- Trigger: Auto-update updated_at timestamp
-- ============================================================================

-- Function to update updated_at column
CREATE OR REPLACE FUNCTION update_templates_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- Trigger on UPDATE
CREATE TRIGGER trigger_update_templates_updated_at
    BEFORE UPDATE ON templates
    FOR EACH ROW
    EXECUTE FUNCTION update_templates_updated_at();

-- ============================================================================
-- Initial Data: Seed default templates from TN-154
-- ============================================================================

-- Note: Default templates will be seeded programmatically via Go code
-- using TN-154 default template registry. This ensures consistency
-- between code and database.

-- +goose Down
-- Rollback script

DROP TRIGGER IF EXISTS trigger_update_templates_updated_at ON templates;
DROP FUNCTION IF EXISTS update_templates_updated_at();

DROP INDEX IF EXISTS idx_template_versions_created_at;
DROP INDEX IF EXISTS idx_template_versions_template_version;
DROP INDEX IF EXISTS idx_template_versions_template_id;

DROP TABLE IF EXISTS template_versions CASCADE;

DROP INDEX IF EXISTS idx_templates_search;
DROP INDEX IF EXISTS idx_templates_metadata_gin;
DROP INDEX IF EXISTS idx_templates_deleted_at;
DROP INDEX IF EXISTS idx_templates_updated_at;
DROP INDEX IF EXISTS idx_templates_created_at;
DROP INDEX IF EXISTS idx_templates_type;
DROP INDEX IF EXISTS idx_templates_name;

DROP TABLE IF EXISTS templates CASCADE;
