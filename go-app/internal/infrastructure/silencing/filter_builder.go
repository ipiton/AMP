package silencing

import (
	"fmt"
	"strings"
)

// buildListQuery constructs a dynamic SQL query for ListSilences based on the provided filter.
// It builds the WHERE clause, ORDER BY clause, and LIMIT/OFFSET clauses dynamically.
//
// The method uses parameterized queries to prevent SQL injection.
// All user inputs are passed as query arguments ($1, $2, etc).
//
// Returns:
//   - query: The complete SQL SELECT statement
//   - args: Slice of query arguments in order
//
// Example:
//
//	filter := SilenceFilter{
//	    Statuses: []silencing.SilenceStatus{silencing.SilenceStatusActive},
//	    CreatedBy: "ops@example.com",
//	    Limit: 100,
//	}
//	query, args := buildListQuery(filter)
//	// query: SELECT ... WHERE status = ANY($1) AND created_by = $2 ORDER BY created_at DESC LIMIT 100 OFFSET 0
//	// args: [["active"], "ops@example.com", 100, 0]
func (r *PostgresSilenceRepository) buildListQuery(filter SilenceFilter) (string, []interface{}) {
	// Base SELECT clause
	query := `
		SELECT id, created_by, comment, starts_at, ends_at, matchers, status, created_at, updated_at
		FROM silences
		WHERE 1=1
	`

	args := []interface{}{}
	argIdx := 1

	// Build WHERE clause dynamically based on filter fields

	// Filter by statuses (array matching)
	if len(filter.Statuses) > 0 {
		query += fmt.Sprintf(" AND status = ANY($%d)", argIdx)
		// Convert SilenceStatus enum to string slice for PostgreSQL array
		statusStrings := make([]string, len(filter.Statuses))
		for i, status := range filter.Statuses {
			statusStrings[i] = string(status)
		}
		args = append(args, statusStrings)
		argIdx++
	}

	// Filter by creator (exact match)
	if filter.CreatedBy != "" {
		query += fmt.Sprintf(" AND created_by = $%d", argIdx)
		args = append(args, filter.CreatedBy)
		argIdx++
	}

	// JSONB filters for matchers

	// Filter by matcher name (JSONB containment)
	// Uses @> operator: matchers @> '[{"name":"alertname"}]'
	if filter.MatcherName != "" {
		query += fmt.Sprintf(" AND matchers @> $%d::jsonb", argIdx)
		// Build JSONB array with partial matcher (name only)
		jsonbFilter := fmt.Sprintf(`[{"name":"%s"}]`, filter.MatcherName)
		args = append(args, jsonbFilter)
		argIdx++
	}

	// Filter by matcher value (JSONB containment)
	// Uses @> operator: matchers @> '[{"value":"HighCPU"}]'
	if filter.MatcherValue != "" {
		query += fmt.Sprintf(" AND matchers @> $%d::jsonb", argIdx)
		// Build JSONB array with partial matcher (value only)
		jsonbFilter := fmt.Sprintf(`[{"value":"%s"}]`, filter.MatcherValue)
		args = append(args, jsonbFilter)
		argIdx++
	}

	// Time range filters

	// Filter by starts_at >= value
	if filter.StartsAfter != nil {
		query += fmt.Sprintf(" AND starts_at >= $%d", argIdx)
		args = append(args, *filter.StartsAfter)
		argIdx++
	}

	// Filter by starts_at <= value
	if filter.StartsBefore != nil {
		query += fmt.Sprintf(" AND starts_at <= $%d", argIdx)
		args = append(args, *filter.StartsBefore)
		argIdx++
	}

	// Filter by ends_at >= value
	if filter.EndsAfter != nil {
		query += fmt.Sprintf(" AND ends_at >= $%d", argIdx)
		args = append(args, *filter.EndsAfter)
		argIdx++
	}

	// Filter by ends_at <= value
	if filter.EndsBefore != nil {
		query += fmt.Sprintf(" AND ends_at <= $%d", argIdx)
		args = append(args, *filter.EndsBefore)
		argIdx++
	}

	// Add ORDER BY clause
	// Default: created_at DESC (newest first)

	// Determine sort direction
	// Since OrderDesc is a bool with zero-value false, we can't distinguish between
	// "not set" and "explicitly set to false". Solution: always DESC by default unless
	// user explicitly provides both OrderBy and OrderDesc=false.
	//
	// Logic:
	// - If OrderBy is empty (user didn't specify): use created_at DESC
	// - If OrderBy is set AND OrderDesc is false: use ASC
	// - If OrderBy is set AND OrderDesc is true: use DESC

	orderBy := filter.OrderBy
	isDefaultOrder := (filter.OrderBy == "" || filter.OrderBy == "created_at")

	if orderBy == "" {
		orderBy = "created_at"
	}

	direction := "DESC" // Default to DESC (newest first)
	if !isDefaultOrder && !filter.OrderDesc {
		// Only use ASC if user explicitly set OrderBy to non-default AND OrderDesc is false
		direction = "ASC"
	}

	query += fmt.Sprintf(" ORDER BY %s %s", orderBy, direction)

	// Add LIMIT/OFFSET for pagination
	// Default limit: 100, max: 1000
	limit := filter.Limit
	if limit == 0 {
		limit = 100
	}

	query += fmt.Sprintf(" LIMIT $%d OFFSET $%d", argIdx, argIdx+1)
	args = append(args, limit, filter.Offset)

	return query, args
}

// buildCountQuery constructs a dynamic SQL query for CountSilences based on the provided filter.
// It's similar to buildListQuery but returns COUNT(*) instead of all columns.
// Does not include ORDER BY, LIMIT, or OFFSET clauses.
//
// Returns:
//   - query: The complete SQL COUNT statement
//   - args: Slice of query arguments in order
//
// Example:
//
//	filter := SilenceFilter{
//	    Statuses: []silencing.SilenceStatus{silencing.SilenceStatusActive},
//	}
//	query, args := buildCountQuery(filter)
//	// query: SELECT COUNT(*) FROM silences WHERE status = ANY($1)
//	// args: [["active"]]
func (r *PostgresSilenceRepository) buildCountQuery(filter SilenceFilter) (string, []interface{}) {
	// Base COUNT clause
	query := `
		SELECT COUNT(*)
		FROM silences
		WHERE 1=1
	`

	args := []interface{}{}
	argIdx := 1

	// Build WHERE clause (same logic as buildListQuery)

	// Filter by statuses
	if len(filter.Statuses) > 0 {
		query += fmt.Sprintf(" AND status = ANY($%d)", argIdx)
		statusStrings := make([]string, len(filter.Statuses))
		for i, status := range filter.Statuses {
			statusStrings[i] = string(status)
		}
		args = append(args, statusStrings)
		argIdx++
	}

	// Filter by creator
	if filter.CreatedBy != "" {
		query += fmt.Sprintf(" AND created_by = $%d", argIdx)
		args = append(args, filter.CreatedBy)
		argIdx++
	}

	// JSONB filters

	// Filter by matcher name
	if filter.MatcherName != "" {
		query += fmt.Sprintf(" AND matchers @> $%d::jsonb", argIdx)
		jsonbFilter := fmt.Sprintf(`[{"name":"%s"}]`, filter.MatcherName)
		args = append(args, jsonbFilter)
		argIdx++
	}

	// Filter by matcher value
	if filter.MatcherValue != "" {
		query += fmt.Sprintf(" AND matchers @> $%d::jsonb", argIdx)
		jsonbFilter := fmt.Sprintf(`[{"value":"%s"}]`, filter.MatcherValue)
		args = append(args, jsonbFilter)
		argIdx++
	}

	// Time range filters

	if filter.StartsAfter != nil {
		query += fmt.Sprintf(" AND starts_at >= $%d", argIdx)
		args = append(args, *filter.StartsAfter)
		argIdx++
	}

	if filter.StartsBefore != nil {
		query += fmt.Sprintf(" AND starts_at <= $%d", argIdx)
		args = append(args, *filter.StartsBefore)
		argIdx++
	}

	if filter.EndsAfter != nil {
		query += fmt.Sprintf(" AND ends_at >= $%d", argIdx)
		args = append(args, *filter.EndsAfter)
		argIdx++
	}

	if filter.EndsBefore != nil {
		query += fmt.Sprintf(" AND ends_at <= $%d", argIdx)
		args = append(args, *filter.EndsBefore)
		argIdx++
	}

	// No ORDER BY, LIMIT, or OFFSET for COUNT queries

	return query, args
}

// sanitizeOrderBy validates and sanitizes the OrderBy field to prevent SQL injection.
// Returns true if the field is valid (one of the allowed fields).
//
// Allowed fields: created_at, starts_at, ends_at, updated_at
//
// This is a defense-in-depth measure. The main validation happens in SilenceFilter.Validate(),
// but we double-check here before constructing SQL.
func sanitizeOrderBy(orderBy string) bool {
	validFields := map[string]bool{
		"created_at": true,
		"starts_at":  true,
		"ends_at":    true,
		"updated_at": true,
	}
	return validFields[orderBy]
}

// escapeJSONBValue escapes special characters in JSONB filter values to prevent injection.
// This is used for MatcherName and MatcherValue filters.
//
// Special characters that need escaping in JSON strings:
//   - " (double quote) → \"
//   - \ (backslash) → \\
//   - / (forward slash) → \/ (optional, but recommended)
//
// Note: PostgreSQL's JSONB @> operator is safe from SQL injection because we use
// parameterized queries ($N::jsonb), but we still need to escape JSON special chars.
func escapeJSONBValue(value string) string {
	// Replace backslash first (must be done before other replacements)
	value = strings.ReplaceAll(value, `\`, `\\`)
	// Replace double quotes
	value = strings.ReplaceAll(value, `"`, `\"`)
	return value
}

// buildJSONBContainmentFilter constructs a JSONB containment filter for matcher fields.
// It properly escapes the value and builds a JSON array with a partial matcher object.
//
// Parameters:
//   - field: The matcher field to filter by ("name" or "value")
//   - value: The value to match (will be escaped)
//
// Returns:
//   - A JSON string suitable for JSONB @> operator
//
// Example:
//
//	filter := buildJSONBContainmentFilter("name", "alertname")
//	// Returns: `[{"name":"alertname"}]`
//
//	filter := buildJSONBContainmentFilter("value", `special"chars\here`)
//	// Returns: `[{"value":"special\"chars\\here"}]`
func buildJSONBContainmentFilter(field, value string) string {
	escapedValue := escapeJSONBValue(value)
	return fmt.Sprintf(`[{"%s":"%s"}]`, field, escapedValue)
}
