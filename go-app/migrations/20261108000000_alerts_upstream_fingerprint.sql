-- UPSTREAM-API-UI (ADR-019 slice 6): the alert's upstream Alertmanager
-- fingerprint (FNV-1a 64 of the labels, 16 hex, as api/v2 reports it) next to
-- AMP's SHA-256 key, so the AMP routes find the history by the fingerprint
-- api/v2 shows. Written on every history upsert; rows from before this
-- migration are filled at startup by PostgresStorageAdapter.BackfillUpstreamFingerprints
-- (the hash is computed in Go, not in SQL). Not unique: two label sets can
-- share an FNV-64 hash.

-- +goose Up
ALTER TABLE alerts ADD COLUMN IF NOT EXISTS upstream_fingerprint VARCHAR(16);
CREATE INDEX IF NOT EXISTS idx_alerts_upstream_fingerprint ON alerts(upstream_fingerprint);

-- +goose Down
DROP INDEX IF EXISTS idx_alerts_upstream_fingerprint;
ALTER TABLE alerts DROP COLUMN IF EXISTS upstream_fingerprint;
