-- +goose NO TRANSACTION
-- UPSTREAM-API-UI: index for the lookup by upstream fingerprint, built
-- without blocking writes to alerts (CONCURRENTLY cannot run in a
-- transaction). A build interrupted by the migration timeout leaves an
-- INVALID index that IF NOT EXISTS keeps; amp-upstream warns about it at
-- start (drop it and restart, or build it by hand; see the CHANGELOG).

-- +goose Up
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_alerts_upstream_fingerprint ON alerts(upstream_fingerprint);

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS idx_alerts_upstream_fingerprint;
