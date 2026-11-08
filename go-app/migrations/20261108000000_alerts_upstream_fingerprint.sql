-- UPSTREAM-API-UI (ADR-019 slice 6): the alert's upstream Alertmanager
-- fingerprint (FNV-1a 64 of the labels, 16 hex, as api/v2 reports it) next to
-- AMP's SHA-256 key, so the AMP routes find the history by the fingerprint
-- api/v2 shows. Written on every history upsert; rows from before this
-- migration are filled at startup by PostgresStorageAdapter.BackfillUpstreamFingerprints
-- (the hash is computed in Go, not in SQL). Not unique: two label sets can
-- share an FNV-64 hash.
--
-- A nullable column without a default is a catalog change: the ACCESS
-- EXCLUSIVE lock is held only for it. The index is built CONCURRENTLY by the
-- next migration, outside a transaction.

-- +goose Up
ALTER TABLE alerts ADD COLUMN IF NOT EXISTS upstream_fingerprint VARCHAR(16);

-- +goose Down
ALTER TABLE alerts DROP COLUMN IF EXISTS upstream_fingerprint;
