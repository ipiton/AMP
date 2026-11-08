-- +goose NO TRANSACTION
-- UPSTREAM-API-UI: index for the lookup by upstream fingerprint, built
-- without blocking writes to alerts (CONCURRENTLY cannot run in a
-- transaction). A build interrupted by the migration timeout leaves an
-- INVALID index that IF NOT EXISTS keeps on the next start, which records
-- this version. amp-upstream then warns at every start: rebuild the index by
-- hand (DROP INDEX CONCURRENTLY, then CREATE INDEX CONCURRENTLY, as two
-- separate statements); a restart does not, goose does not rerun it.

-- +goose Up
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_alerts_upstream_fingerprint ON alerts(upstream_fingerprint);

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS idx_alerts_upstream_fingerprint;
