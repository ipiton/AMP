package infrastructure

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/core/alertconv"
)

// DefaultBackfillBatchSize is the number of rows BackfillUpstreamFingerprints
// reads and updates per statement.
const DefaultBackfillBatchSize = 500

// Each query selects one row by a fixed column, so the column is never
// spliced in from input. An FNV fingerprint can belong to more than one label
// set: the most recently updated row wins.
const (
	alertRecordColumns = `fingerprint, alert_name, status, labels, annotations,
		starts_at, ends_at, generator_url, timestamp, created_at`
	alertRecordByFingerprintQuery = `SELECT ` + alertRecordColumns + `
		FROM alerts WHERE fingerprint = $1`
	alertRecordByUpstreamFingerprintQuery = `SELECT ` + alertRecordColumns + `
		FROM alerts WHERE upstream_fingerprint = $1
		ORDER BY timestamp DESC, id DESC LIMIT 1`
)

// AlertRecordByFingerprint returns the history row of an AMP (SHA-256)
// fingerprint, or core.ErrAlertNotFound.
func (p *PostgresStorageAdapter) AlertRecordByFingerprint(ctx context.Context, fingerprint string) (*core.AlertHistoryRecord, error) {
	return p.alertRecord(ctx, alertRecordByFingerprintQuery, fingerprint)
}

// AlertRecordByUpstreamFingerprint returns the history row of an upstream
// (FNV, api/v2) fingerprint, or core.ErrAlertNotFound. A row written before
// the upstream_fingerprint column existed is found once
// BackfillUpstreamFingerprints has filled it.
func (p *PostgresStorageAdapter) AlertRecordByUpstreamFingerprint(ctx context.Context, fingerprint string) (*core.AlertHistoryRecord, error) {
	return p.alertRecord(ctx, alertRecordByUpstreamFingerprintQuery, fingerprint)
}

func (p *PostgresStorageAdapter) alertRecord(ctx context.Context, query, fingerprint string) (*core.AlertHistoryRecord, error) {
	if p.pool == nil {
		return nil, fmt.Errorf("not connected")
	}
	rec := &core.AlertHistoryRecord{}
	a := &rec.Alert
	var (
		labelsJSON, annotationsJSON []byte
		// starts_at is nullable in the schema; the zero time stands for NULL.
		startsAt *time.Time
	)
	err := p.pool.QueryRow(ctx, query, fingerprint).Scan(
		&a.Fingerprint,
		&a.AlertName,
		&a.Status,
		&labelsJSON,
		&annotationsJSON,
		&startsAt,
		&a.EndsAt,
		&a.GeneratorURL,
		&a.Timestamp,
		&rec.FirstSeenAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, core.ErrAlertNotFound
		}
		return nil, fmt.Errorf("failed to get alert record: %w", err)
	}
	if startsAt != nil {
		a.StartsAt = *startsAt
	}
	if err := json.Unmarshal(labelsJSON, &a.Labels); err != nil {
		return nil, fmt.Errorf("failed to unmarshal labels: %w", err)
	}
	if err := json.Unmarshal(annotationsJSON, &a.Annotations); err != nil {
		return nil, fmt.Errorf("failed to unmarshal annotations: %w", err)
	}
	return rec, nil
}

// BackfillUpstreamFingerprints fills upstream_fingerprint on the rows written
// before the column existed, batchSize rows per statement (<= 0: the
// default), and returns how many it filled. Rows another writer filled in the
// meantime are left alone, so concurrent runs (one per replica) are safe.
// The updated_at trigger moves those rows' updated_at to the time of the
// backfill.
func (p *PostgresStorageAdapter) BackfillUpstreamFingerprints(ctx context.Context, batchSize int) (int, error) {
	if p.pool == nil {
		return 0, fmt.Errorf("not connected")
	}
	if batchSize <= 0 {
		batchSize = DefaultBackfillBatchSize
	}
	var (
		filled int
		lastID int64
	)
	for {
		ids, fps, scanned, err := p.unfilledBatch(ctx, &lastID, batchSize)
		if err != nil {
			return filled, err
		}
		if len(ids) > 0 {
			tag, err := p.pool.Exec(ctx, `
				UPDATE alerts a SET upstream_fingerprint = v.fp
				FROM unnest($1::bigint[], $2::text[]) AS v(id, fp)
				WHERE a.id = v.id AND a.upstream_fingerprint IS NULL`, ids, fps)
			if err != nil {
				return filled, fmt.Errorf("failed to backfill upstream fingerprints: %w", err)
			}
			filled += int(tag.RowsAffected())
		}
		if scanned < batchSize {
			return filled, nil
		}
	}
}

// unfilledBatch reads up to limit rows without an upstream fingerprint after
// *lastID, advances *lastID past them and computes their fingerprints from
// the labels. A row whose labels do not parse is skipped with a warning
// rather than stopping the backfill at it; scanned counts it.
func (p *PostgresStorageAdapter) unfilledBatch(ctx context.Context, lastID *int64, limit int) (ids []int64, fps []string, scanned int, err error) {
	rows, err := p.pool.Query(ctx, `
		SELECT id, labels FROM alerts
		WHERE upstream_fingerprint IS NULL AND id > $1
		ORDER BY id LIMIT $2`, *lastID, limit)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("failed to read alerts for backfill: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id         int64
			labelsJSON []byte
			labels     map[string]string
		)
		if err := rows.Scan(&id, &labelsJSON); err != nil {
			return nil, nil, 0, fmt.Errorf("failed to scan alert for backfill: %w", err)
		}
		scanned++
		*lastID = id
		if err := json.Unmarshal(labelsJSON, &labels); err != nil {
			p.logger.Warn("upstream fingerprint backfill skips an alert whose labels do not parse", "id", id, "err", err)
			continue
		}
		ids = append(ids, id)
		fps = append(fps, alertconv.UpstreamFingerprint(labels))
	}
	if err := rows.Err(); err != nil {
		return nil, nil, 0, fmt.Errorf("failed to read alerts for backfill: %w", err)
	}
	return ids, fps, scanned, nil
}
