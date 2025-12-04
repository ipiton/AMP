// Package handlers provides HTTP request handlers for the Alert History Service.
//
// TN-135: Silence API Advanced Endpoints (150% Features)
// This file implements advanced endpoints that exceed baseline requirements:
//   - POST /api/v2/silences/check - Check if alert would be silenced
//   - POST /api/v2/silences/bulk/delete - Bulk delete silences
//
// These endpoints demonstrate Enterprise-Grade features and achieve 150% quality target.
package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/ipiton/AMP/internal/core/silencing"
)

// ==================== Advanced Endpoints (150% Features) ====================

// CheckAlert handles POST /api/v2/silences/check
//
// Checks if an alert would be silenced by any currently active silence.
// This is useful for testing silence configurations before alerts fire.
//
// Request Body:
//
//	{
//	  "labels": {
//	    "alertname": "HighCPU",
//	    "job": "api-server",
//	    "instance": "server-01"
//	  }
//	}
//
// Response (200 OK):
//
//	{
//	  "silenced": true,
//	  "silenceIDs": ["550e8400-...", "660e8400-..."],
//	  "silences": [
//	    {
//	      "id": "550e8400-...",
//	      "comment": "Maintenance window",
//	      "startsAt": "2025-11-06T12:00:00Z",
//	      "endsAt": "2025-11-06T14:00:00Z"
//	    }
//	  ],
//	  "latencyMs": 5
//	}
//
// Use Cases:
//   - Test silence rules before deploying
//   - Validate alert labels match silence matchers
//   - Debugging: why is my alert not firing?
//
// Performance: <10ms for 100 active silences (target)
//
// Fail-safe Design:
//   - On manager errors: returns not silenced (false)
//   - Ensures alerts are never incorrectly suppressed due to bugs
func (h *SilenceHandler) CheckAlert(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	start := time.Now()

	// Parse request body
	var req CheckAlertRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.logger.Warn("Invalid request body", "error", err, "method", "CheckAlert")
		h.sendError(w, "Invalid request body: "+err.Error(), http.StatusBadRequest)
		h.recordMetrics("POST", "/silences/check", "400", start)
		return
	}

	// Validate labels (must not be empty)
	if len(req.Labels) == 0 {
		h.logger.Warn("Empty labels in CheckAlert request")
		h.sendError(w, "Labels are required and must not be empty", http.StatusBadRequest)
		h.recordMetrics("POST", "/silences/check", "400", start)
		return
	}

	// Convert to Alert domain model
	alert := &silencing.Alert{
		Labels: req.Labels,
	}

	// Check if alert would be silenced
	silenced, silenceIDs, err := h.manager.IsAlertSilenced(ctx, alert)
	if err != nil {
		// FAIL-SAFE: On errors, assume NOT silenced to prevent false suppression
		h.logger.Error("Failed to check if alert is silenced (fail-safe: returning not silenced)",
			"error", err,
			"labels", req.Labels,
		)
		silenced = false
		silenceIDs = nil
		// Note: We still return 200 OK (not an error from API perspective)
	}

	// Get full silence objects for matched IDs
	var silences []*SilenceResponse
	if silenced && len(silenceIDs) > 0 {
		for _, id := range silenceIDs {
			silence, err := h.manager.GetSilence(ctx, id)
			if err != nil {
				// Skip silences we can't retrieve (they may have been deleted)
				h.logger.Warn("Failed to retrieve matched silence", "id", id, "error", err)
				continue
			}
			silences = append(silences, toSilenceResponse(silence))
		}
	}

	// Build response
	duration := time.Since(start)
	response := &CheckAlertResponse{
		Silenced:   silenced,
		SilenceIDs: silenceIDs,
		Silences:   silences,
		LatencyMs:  duration.Milliseconds(),
	}

	// Record metrics
	h.recordMetrics("POST", "/silences/check", "200", start)
	if h.metrics != nil {
		resultLabel := "not_silenced"
		if silenced {
			resultLabel = "silenced"
		}
		h.metrics.SilenceOperationsTotal.WithLabelValues("check", resultLabel).Inc()
	}

	// Log result
	h.logger.Debug("Alert silence check completed",
		"silenced", silenced,
		"silence_count", len(silenceIDs),
		"latency_ms", response.LatencyMs,
		"labels", req.Labels,
	)

	// Return response
	h.sendJSON(w, response, http.StatusOK)
}

// BulkDelete handles POST /api/v2/silences/bulk/delete
//
// Deletes multiple silences in a single request.
// Supports partial success: some deletes may succeed while others fail.
//
// Request Body:
//
//	{
//	  "ids": [
//	    "550e8400-e29b-41d4-a716-446655440000",
//	    "660e8400-e29b-41d4-a716-446655440001",
//	    ...
//	  ]
//	}
//
// Response (200 OK - All deleted):
//
//	{
//	  "deleted": 5,
//	  "errors": []
//	}
//
// Response (207 Multi-Status - Partial success):
//
//	{
//	  "deleted": 3,
//	  "errors": [
//	    {"id": "660e8400-...", "error": "silence not found"},
//	    {"id": "770e8400-...", "error": "invalid UUID format"}
//	  ]
//	}
//
// Response (400 Bad Request - All failed):
//
//	{
//	  "deleted": 0,
//	  "errors": [...]
//	}
//
// Constraints:
//   - Minimum: 1 ID required
//   - Maximum: 100 IDs per request (rate limiting)
//   - All IDs must be valid UUIDs
//
// Performance: <50ms for 100 silences (target)
//
// Use Cases:
//   - Cleanup expired silences
//   - Remove silences in bulk after maintenance
//   - Admin operations
func (h *SilenceHandler) BulkDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	start := time.Now()

	// Parse request body
	var req BulkDeleteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.logger.Warn("Invalid request body", "error", err, "method", "BulkDelete")
		h.sendError(w, "Invalid request body: "+err.Error(), http.StatusBadRequest)
		h.recordMetrics("POST", "/silences/bulk/delete", "400", start)
		return
	}

	// Validate IDs array
	if len(req.IDs) == 0 {
		h.sendError(w, "At least one silence ID is required", http.StatusBadRequest)
		h.recordMetrics("POST", "/silences/bulk/delete", "400", start)
		return
	}

	// Enforce maximum limit (rate limiting)
	if len(req.IDs) > 100 {
		h.sendError(w, "At most 100 silence IDs are allowed per request", http.StatusBadRequest)
		h.recordMetrics("POST", "/silences/bulk/delete", "400", start)
		if h.metrics != nil {
			h.metrics.SilenceRateLimitExceeded()
		}
		return
	}

	// Delete each silence and collect results
	var deleted int
	var errors []BulkDeleteError

	for _, id := range req.IDs {
		// Validate UUID format
		if !h.isValidUUID(id) {
			errors = append(errors, BulkDeleteError{
				ID:    id,
				Error: "invalid UUID format",
			})
			continue
		}

		// Attempt delete
		if err := h.manager.DeleteSilence(ctx, id); err != nil {
			// Determine error type
			errorMsg := "failed to delete"
			if strings.Contains(err.Error(), "not found") {
				errorMsg = "silence not found"
			}

			errors = append(errors, BulkDeleteError{
				ID:    id,
				Error: errorMsg,
			})

			h.logger.Debug("Failed to delete silence in bulk operation",
				"id", id,
				"error", err,
			)
		} else {
			// Success
			deleted++
		}
	}

	// Build response
	response := &BulkDeleteResponse{
		Deleted: deleted,
		Errors:  errors,
	}

	// Determine status code based on results
	statusCode := http.StatusOK // 200 - All deleted
	if len(errors) > 0 && deleted > 0 {
		statusCode = http.StatusMultiStatus // 207 - Partial success
	} else if len(errors) > 0 && deleted == 0 {
		statusCode = http.StatusBadRequest // 400 - All failed
	}

	// Record metrics
	h.recordMetrics("POST", "/silences/bulk/delete", fmt.Sprint(statusCode), start)
	if h.metrics != nil {
		h.metrics.SilenceOperationsTotal.WithLabelValues("bulk_delete", "success").Add(float64(deleted))
		h.metrics.SilenceOperationsTotal.WithLabelValues("bulk_delete", "error").Add(float64(len(errors)))
	}

	// Log result
	h.logger.Info("Bulk delete completed",
		"total_requested", len(req.IDs),
		"deleted", deleted,
		"errors", len(errors),
		"status_code", statusCode,
	)

	// Return response
	h.sendJSON(w, response, statusCode)
}
