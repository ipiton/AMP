-- +goose Up
ALTER TABLE alert_investigations
    ADD COLUMN IF NOT EXISTS steps            JSONB,
    ADD COLUMN IF NOT EXISTS iterations_count INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS tool_calls_count INTEGER NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS idx_inv_steps ON alert_investigations USING GIN (steps);

-- +goose Down
DROP INDEX IF EXISTS idx_inv_steps;
ALTER TABLE alert_investigations
    DROP COLUMN IF EXISTS steps,
    DROP COLUMN IF EXISTS iterations_count,
    DROP COLUMN IF EXISTS tool_calls_count;
