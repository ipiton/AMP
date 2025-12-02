package template

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ipiton/AMP/internal/core/domain"
)

// ================================================================================
// TN-155: Template Repository - Version Control Operations
// ================================================================================
// Implementation of version control for templates.
//
// Features:
// - Full version history
// - Rollback capability
// - Version comparison
//
// Performance Targets:
// - ListVersions: < 100ms p95
// - GetVersion: < 50ms p95
//
// Quality Target: 150% (Grade A+ EXCEPTIONAL)

// CreateVersion creates a new version entry
func (r *DefaultTemplateRepository) CreateVersion(ctx context.Context, version *domain.TemplateVersion) error {
	start := time.Now()
	defer func() {
		r.logger.Debug("create version operation",
			"template_id", version.TemplateID,
			"version", version.Version,
			"duration_ms", time.Since(start).Milliseconds(),
		)
	}()

	// Serialize metadata
	metadataJSON, err := json.Marshal(version.Metadata)
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	// Set timestamps
	if version.CreatedAt.IsZero() {
		version.CreatedAt = time.Now()
	}

	query := `
		INSERT INTO template_versions (
			id, template_id, version, content, description, metadata,
			created_at, created_by, change_summary
		) VALUES (
			gen_random_uuid(), $1, $2, $3, $4, $5, $6, $7, $8
		)
		RETURNING id
	`

	row := r.db.QueryRow(ctx, query,
		version.TemplateID,
		version.Version,
		version.Content,
		version.Description,
		metadataJSON,
		version.CreatedAt,
		version.CreatedBy,
		version.ChangeSummary,
	)

	if err := row.Scan(&version.ID); err != nil {
		return fmt.Errorf("failed to insert version: %w", err)
	}

	r.logger.Info("version created",
		"version_id", version.ID,
		"template_id", version.TemplateID,
		"version", version.Version,
	)

	return nil
}

// ================================================================================

// ListVersions retrieves all versions for a template with pagination
// Returns versions, total count, and error
func (r *DefaultTemplateRepository) ListVersions(
	ctx context.Context,
	templateID string,
	filters domain.VersionFilters,
) ([]*domain.TemplateVersion, int, error) {
	start := time.Now()
	defer func() {
		r.logger.Debug("list versions operation",
			"template_id", templateID,
			"filters", filters,
			"duration_ms", time.Since(start).Milliseconds(),
		)
	}()

	// Count total versions
	countQuery := `SELECT COUNT(*) FROM template_versions WHERE template_id = $1`
	var total int
	row := r.db.QueryRow(ctx, countQuery, templateID)
	if err := row.Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("failed to count versions: %w", err)
	}

	// Query versions
	query := `
		SELECT id, template_id, version, content, description, metadata,
		       created_at, created_by, change_summary
		FROM template_versions
		WHERE template_id = $1
		ORDER BY version DESC
		LIMIT $2 OFFSET $3
	`

	rows, err := r.db.Query(ctx, query, templateID, filters.Limit, filters.Offset)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to query versions: %w", err)
	}
	defer rows.Close()

	// Scan results
	versions := make([]*domain.TemplateVersion, 0, filters.Limit)
	for rows.Next() {
		version, err := scanTemplateVersion(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to scan version: %w", err)
		}
		versions = append(versions, version)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("rows iteration error: %w", err)
	}

	r.logger.Info("versions listed",
		"template_id", templateID,
		"count", len(versions),
		"total", total,
	)

	return versions, total, nil
}

// ================================================================================

// GetVersion retrieves a specific version of a template
// Returns ErrVersionNotFound if version doesn't exist
func (r *DefaultTemplateRepository) GetVersion(
	ctx context.Context,
	templateID string,
	versionNum int,
) (*domain.TemplateVersion, error) {
	start := time.Now()
	defer func() {
		r.logger.Debug("get version operation",
			"template_id", templateID,
			"version", versionNum,
			"duration_ms", time.Since(start).Milliseconds(),
		)
	}()

	query := `
		SELECT id, template_id, version, content, description, metadata,
		       created_at, created_by, change_summary
		FROM template_versions
		WHERE template_id = $1 AND version = $2
	`

	row := r.db.QueryRow(ctx, query, templateID, versionNum)
	version, err := scanTemplateVersion(row)
	if err != nil {
		if isNotFoundError(err) {
			return nil, ErrVersionNotFound
		}
		return nil, fmt.Errorf("failed to get version: %w", err)
	}

	return version, nil
}

// ================================================================================

// CountByType returns the count of templates grouped by type
func (r *DefaultTemplateRepository) CountByType(ctx context.Context) (map[string]int, error) {
	start := time.Now()
	defer func() {
		r.logger.Debug("count by type operation",
			"duration_ms", time.Since(start).Milliseconds(),
		)
	}()

	query := `
		SELECT type, COUNT(*) as count
		FROM templates
		WHERE deleted_at IS NULL
		GROUP BY type
		ORDER BY type
	`

	rows, err := r.db.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to count by type: %w", err)
	}
	defer rows.Close()

	result := make(map[string]int)
	for rows.Next() {
		var templateType string
		var count int
		if err := rows.Scan(&templateType, &count); err != nil {
			return nil, fmt.Errorf("failed to scan count: %w", err)
		}
		result[templateType] = count
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration error: %w", err)
	}

	return result, nil
}

// ================================================================================
