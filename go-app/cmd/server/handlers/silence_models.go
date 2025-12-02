// Package handlers provides HTTP request handlers for the Alert History Service.
//
// TN-135: Silence API Models
// This file defines request/response models for silence API endpoints
// and conversion helpers between HTTP and domain models.
//
// Models follow Alertmanager API v2 conventions for compatibility.
package handlers

import (
	"fmt"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"github.com/ipiton/AMP/internal/core/silencing"
	infrasilencing "github.com/ipiton/AMP/internal/infrastructure/silencing"
)

// ==================== Request Models ====================

// CreateSilenceRequest represents the request body for POST /api/v2/silences
type CreateSilenceRequest struct {
	CreatedBy string               `json:"createdBy"` // Email of creator (required, 1-255 chars)
	Comment   string               `json:"comment"`   // Reason for silence (required, 3-1024 chars)
	StartsAt  time.Time            `json:"startsAt"`  // Start time (required)
	EndsAt    time.Time            `json:"endsAt"`    // End time (required, must be after StartsAt)
	Matchers  []silencing.Matcher  `json:"matchers"`  // Label matchers (required, 1-100 matchers)
}

// UpdateSilenceRequest represents the request body for PUT /api/v2/silences/{id}
//
// All fields are optional (partial update supported).
// Immutable fields (id, createdBy, startsAt, createdAt) cannot be updated.
type UpdateSilenceRequest struct {
	Comment  *string              `json:"comment,omitempty"`  // Updated comment (3-1024 chars)
	EndsAt   *time.Time           `json:"endsAt,omitempty"`   // Updated end time
	Matchers *[]silencing.Matcher `json:"matchers,omitempty"` // Replaces entire matcher list
}

// CheckAlertRequest represents the request body for POST /api/v2/silences/check
type CheckAlertRequest struct {
	Labels map[string]string `json:"labels"` // Alert labels to check (required, non-empty)
}

// BulkDeleteRequest represents the request body for POST /api/v2/silences/bulk/delete
type BulkDeleteRequest struct {
	IDs []string `json:"ids"` // Silence IDs to delete (required, 1-100 UUIDs)
}

// ListSilencesParams holds parsed query parameters for GET /api/v2/silences
type ListSilencesParams struct {
	// Filters
	Status       *string    // Filter by status (pending/active/expired)
	CreatedBy    *string    // Filter by creator email
	MatcherName  *string    // Filter by matcher name
	MatcherValue *string    // Filter by matcher value
	StartsAfter  *time.Time // Filter by start time (>=)
	StartsBefore *time.Time // Filter by start time (<=)
	EndsAfter    *time.Time // Filter by end time (>=)
	EndsBefore   *time.Time // Filter by end time (<=)

	// Pagination
	Limit  int // Number of results (default: 100, max: 1000)
	Offset int // Skip N results (default: 0)

	// Sorting
	Sort  string // Sort field: created_at, starts_at, ends_at, status (default: created_at)
	Order string // Sort order: asc, desc (default: desc)
}

// isSimpleQuery returns true if this is a simple query that can be cached.
// Simple queries: status=active only, no other filters
func (p *ListSilencesParams) isSimpleQuery() bool {
	return p.Status != nil &&
		*p.Status == "active" &&
		p.CreatedBy == nil &&
		p.MatcherName == nil &&
		p.MatcherValue == nil &&
		p.StartsAfter == nil &&
		p.StartsBefore == nil &&
		p.EndsAfter == nil &&
		p.EndsBefore == nil &&
		p.Limit == 100 &&
		p.Offset == 0
}

// toSilenceFilter converts query parameters to SilenceFilter for the repository layer.
func (p *ListSilencesParams) toSilenceFilter() infrasilencing.SilenceFilter {
	filter := infrasilencing.SilenceFilter{
		Limit:  p.Limit,
		Offset: p.Offset,
	}

	// Status filter
	if p.Status != nil {
		filter.Statuses = []silencing.SilenceStatus{silencing.SilenceStatus(*p.Status)}
	}

	// Creator filter
	if p.CreatedBy != nil {
		filter.CreatedBy = *p.CreatedBy
	}

	// Time range filters
	if p.StartsAfter != nil {
		filter.StartsAfter = p.StartsAfter
	}
	if p.StartsBefore != nil {
		filter.StartsBefore = p.StartsBefore
	}
	if p.EndsAfter != nil {
		filter.EndsAfter = p.EndsAfter
	}
	if p.EndsBefore != nil {
		filter.EndsBefore = p.EndsBefore
	}

	// Note: Sorting handled by repository layer via SQL ORDER BY
	// SilenceFilter doesn't have SortBy/SortOrder fields (TN-133 design)

	return filter
}

// ==================== Response Models ====================

// SilenceResponse represents a single silence in API responses.
//
// This is the wire format returned by all endpoints that return silence objects.
// Fully compatible with Alertmanager API v2.
type SilenceResponse struct {
	ID        string                  `json:"id"`                  // Silence UUID
	CreatedBy string                  `json:"createdBy"`           // Creator email
	Comment   string                  `json:"comment"`             // Reason for silence
	StartsAt  time.Time               `json:"startsAt"`            // Start time
	EndsAt    time.Time               `json:"endsAt"`              // End time
	Matchers  []silencing.Matcher     `json:"matchers"`            // Label matchers
	Status    silencing.SilenceStatus `json:"status"`              // Current status (pending/active/expired)
	CreatedAt time.Time               `json:"createdAt"`           // Creation timestamp
	UpdatedAt *time.Time              `json:"updatedAt,omitempty"` // Last update timestamp (optional)
}

// ListSilencesResponse represents the response for GET /api/v2/silences
type ListSilencesResponse struct {
	Silences []*SilenceResponse `json:"silences"` // List of silences (empty array if no results)
	Total    int64              `json:"total"`    // Total count of matching silences
	Limit    int                `json:"limit"`    // Pagination limit used
	Offset   int                `json:"offset"`   // Pagination offset used
}

// CheckAlertResponse represents the response for POST /api/v2/silences/check
type CheckAlertResponse struct {
	Silenced   bool               `json:"silenced"`             // True if alert is silenced
	SilenceIDs []string           `json:"silenceIDs,omitempty"` // List of matching silence IDs
	Silences   []*SilenceResponse `json:"silences,omitempty"`   // Full silence objects (for convenience)
	LatencyMs  int64              `json:"latencyMs"`            // Processing time in milliseconds
}

// BulkDeleteResponse represents the response for POST /api/v2/silences/bulk/delete
type BulkDeleteResponse struct {
	Deleted int                 `json:"deleted"`         // Count of successfully deleted silences
	Errors  []BulkDeleteError   `json:"errors,omitempty"` // Errors for failed deletes (empty if all succeeded)
}

// BulkDeleteError represents an error for a single silence in bulk delete.
type BulkDeleteError struct {
	ID    string `json:"id"`    // Silence ID that failed
	Error string `json:"error"` // Error message
}

// SilenceErrorResponse represents a standard error response for silence API.
// Note: We reuse ErrorResponse from enrichment.go to avoid duplication
type SilenceErrorResponse = struct {
	Error string `json:"error"`
}

// ==================== Conversion Helpers ====================

// toSilenceResponse converts a domain Silence to API response format.
func toSilenceResponse(s *silencing.Silence) *SilenceResponse {
	if s == nil {
		return nil
	}

	return &SilenceResponse{
		ID:        s.ID,
		CreatedBy: s.CreatedBy,
		Comment:   s.Comment,
		StartsAt:  s.StartsAt,
		EndsAt:    s.EndsAt,
		Matchers:  s.Matchers, // Direct copy (no conversion needed)
		Status:    s.Status,
		CreatedAt: s.CreatedAt,
		UpdatedAt: s.UpdatedAt,
	}
}

// toSilenceResponses converts a slice of domain Silences to API response format.
func toSilenceResponses(silences []*silencing.Silence) []*SilenceResponse {
	if silences == nil {
		return []*SilenceResponse{} // Return empty array, not nil
	}

	responses := make([]*SilenceResponse, len(silences))
	for i, s := range silences {
		responses[i] = toSilenceResponse(s)
	}
	return responses
}

// fromCreateSilenceRequest converts an API request to domain Silence model.
func fromCreateSilenceRequest(req *CreateSilenceRequest) *silencing.Silence {
	if req == nil {
		return nil
	}

	return &silencing.Silence{
		// ID will be generated by database
		CreatedBy: req.CreatedBy,
		Comment:   req.Comment,
		StartsAt:  req.StartsAt,
		EndsAt:    req.EndsAt,
		Matchers:  req.Matchers,
		// Status will be calculated
		// CreatedAt will be set by database
	}
}

// applyUpdateSilenceRequest applies updates from UpdateSilenceRequest to a Silence.
//
// This supports partial updates: only non-nil fields are updated.
// Immutable fields (id, createdBy, startsAt, createdAt) are never modified.
func applyUpdateSilenceRequest(silence *silencing.Silence, req *UpdateSilenceRequest) {
	if req == nil || silence == nil {
		return
	}

	// Update comment if provided
	if req.Comment != nil {
		silence.Comment = *req.Comment
	}

	// Update end time if provided
	if req.EndsAt != nil {
		silence.EndsAt = *req.EndsAt
	}

	// Update matchers if provided (replaces entire list)
	if req.Matchers != nil {
		silence.Matchers = *req.Matchers
	}

	// Recalculate status after updates
	silence.Status = silence.CalculateStatus()
}

// ==================== Validation Helpers ====================

// validateCreateSilenceRequest validates a CreateSilenceRequest.
//
// Validation rules:
//   - createdBy: required, valid email, 1-255 chars
//   - comment: required, 3-1024 chars
//   - startsAt: required, not zero
//   - endsAt: required, after startsAt
//   - matchers: required, 1-100 matchers, each validated by Silence.Validate()
func (h *SilenceHandler) validateCreateSilenceRequest(req *CreateSilenceRequest) error {
	if req == nil {
		return fmt.Errorf("request is nil")
	}

	// Validate createdBy (email)
	if req.CreatedBy == "" {
		return fmt.Errorf("createdBy is required")
	}
	if len(req.CreatedBy) > 255 {
		return fmt.Errorf("createdBy must be at most 255 characters")
	}
	if _, err := mail.ParseAddress(req.CreatedBy); err != nil {
		return fmt.Errorf("createdBy must be a valid email address")
	}

	// Validate comment
	if req.Comment == "" {
		return fmt.Errorf("comment is required")
	}
	if len(req.Comment) < 3 {
		return fmt.Errorf("comment must be at least 3 characters")
	}
	if len(req.Comment) > 1024 {
		return fmt.Errorf("comment must be at most 1024 characters")
	}

	// Validate time range
	if req.StartsAt.IsZero() {
		return fmt.Errorf("startsAt is required")
	}
	if req.EndsAt.IsZero() {
		return fmt.Errorf("endsAt is required")
	}
	if !req.EndsAt.After(req.StartsAt) {
		return fmt.Errorf("endsAt must be after startsAt")
	}

	// Validate matchers count
	if len(req.Matchers) == 0 {
		return fmt.Errorf("at least one matcher is required")
	}
	if len(req.Matchers) > 100 {
		return fmt.Errorf("at most 100 matchers are allowed")
	}

	// Validate each matcher via domain model validation
	silence := fromCreateSilenceRequest(req)
	if err := silence.Validate(); err != nil {
		return err
	}

	return nil
}

// parseListSilencesParams parses query parameters for GET /api/v2/silences
func (h *SilenceHandler) parseListSilencesParams(r *http.Request) (*ListSilencesParams, error) {
	params := &ListSilencesParams{
		Limit:  100, // Default limit
		Offset: 0,   // Default offset
		Sort:   "created_at", // Default sort
		Order:  "desc",       // Default order
	}

	query := r.URL.Query()

	// Parse status filter
	if status := query.Get("status"); status != "" {
		// Validate status enum
		if status != "pending" && status != "active" && status != "expired" {
			return nil, fmt.Errorf("invalid status: must be pending, active, or expired")
		}
		params.Status = &status
	}

	// Parse createdBy filter
	if createdBy := query.Get("createdBy"); createdBy != "" {
		params.CreatedBy = &createdBy
	}

	// Parse matcher filters
	if matcherName := query.Get("matcherName"); matcherName != "" {
		params.MatcherName = &matcherName
	}
	if matcherValue := query.Get("matcherValue"); matcherValue != "" {
		params.MatcherValue = &matcherValue
	}

	// Parse time range filters
	if startsAfter := query.Get("startsAfter"); startsAfter != "" {
		t, err := time.Parse(time.RFC3339, startsAfter)
		if err != nil {
			return nil, fmt.Errorf("invalid startsAfter format: %w", err)
		}
		params.StartsAfter = &t
	}
	if startsBefore := query.Get("startsBefore"); startsBefore != "" {
		t, err := time.Parse(time.RFC3339, startsBefore)
		if err != nil {
			return nil, fmt.Errorf("invalid startsBefore format: %w", err)
		}
		params.StartsBefore = &t
	}
	if endsAfter := query.Get("endsAfter"); endsAfter != "" {
		t, err := time.Parse(time.RFC3339, endsAfter)
		if err != nil {
			return nil, fmt.Errorf("invalid endsAfter format: %w", err)
		}
		params.EndsAfter = &t
	}
	if endsBefore := query.Get("endsBefore"); endsBefore != "" {
		t, err := time.Parse(time.RFC3339, endsBefore)
		if err != nil {
			return nil, fmt.Errorf("invalid endsBefore format: %w", err)
		}
		params.EndsBefore = &t
	}

	// Parse pagination
	if limitStr := query.Get("limit"); limitStr != "" {
		limit, err := strconv.Atoi(limitStr)
		if err != nil || limit < 1 {
			return nil, fmt.Errorf("invalid limit: must be a positive integer")
		}
		// Enforce max limit
		if limit > 1000 {
			limit = 1000
		}
		params.Limit = limit
	}

	if offsetStr := query.Get("offset"); offsetStr != "" {
		offset, err := strconv.Atoi(offsetStr)
		if err != nil || offset < 0 {
			return nil, fmt.Errorf("invalid offset: must be a non-negative integer")
		}
		params.Offset = offset
	}

	// Parse sorting
	if sort := query.Get("sort"); sort != "" {
		// Validate sort field
		validSortFields := map[string]bool{
			"created_at": true,
			"starts_at":  true,
			"ends_at":    true,
			"status":     true,
		}
		if !validSortFields[sort] {
			return nil, fmt.Errorf("invalid sort field: must be created_at, starts_at, ends_at, or status")
		}
		params.Sort = sort
	}

	if order := query.Get("order"); order != "" {
		// Validate order
		order = strings.ToLower(order)
		if order != "asc" && order != "desc" {
			return nil, fmt.Errorf("invalid order: must be asc or desc")
		}
		params.Order = order
	}

	return params, nil
}
