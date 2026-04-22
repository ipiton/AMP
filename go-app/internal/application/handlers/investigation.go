package handlers

import (
	"net/http"
	"strings"

	"github.com/ipiton/AMP/internal/core"
)

// InvestigationRepositoryProvider is satisfied by ServiceRegistry.
type InvestigationRepositoryProvider interface {
	InvestigationRepository() core.InvestigationRepository
}

// InvestigationHandler returns GET /api/v1/alerts/{fingerprint}/investigation.
func InvestigationHandler(registry InvestigationRepositoryProvider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		// Extract fingerprint from URL path:
		// /api/v1/alerts/<fingerprint>/investigation
		fingerprint := extractFingerprintFromPath(r.URL.Path)
		if fingerprint == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing fingerprint"})
			return
		}

		repo := registry.InvestigationRepository()
		if repo == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "investigation service unavailable"})
			return
		}

		inv, err := repo.GetLatestByFingerprint(r.Context(), fingerprint)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to query investigation"})
			return
		}
		if inv == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "no investigation found for fingerprint"})
			return
		}

		writeJSON(w, http.StatusOK, investigationResponse(inv))
	}
}

// investigationResponse converts Investigation to a JSON-serialisable map.
func investigationResponse(inv *core.Investigation) map[string]any {
	resp := map[string]any{
		"id":          inv.ID,
		"fingerprint": inv.Fingerprint,
		"status":      string(inv.Status),
		"retry_count": inv.RetryCount,
		"queued_at":   inv.QueuedAt,
		"created_at":  inv.CreatedAt,
		"updated_at":  inv.UpdatedAt,
	}
	if inv.StartedAt != nil {
		resp["started_at"] = inv.StartedAt
	}
	if inv.CompletedAt != nil {
		resp["completed_at"] = inv.CompletedAt
	}
	if inv.ErrorMessage != nil {
		resp["error_message"] = *inv.ErrorMessage
	}
	if inv.ErrorType != nil {
		resp["error_type"] = string(*inv.ErrorType)
	}
	if inv.Result != nil {
		resp["result"] = inv.Result
	}
	return resp
}

// extractFingerprintFromPath extracts the fingerprint segment from a path like
// /api/v1/alerts/<fingerprint>/investigation.
func extractFingerprintFromPath(path string) string {
	// Remove trailing slash.
	path = strings.TrimRight(path, "/")
	// Expect suffix /investigation.
	if !strings.HasSuffix(path, "/investigation") {
		return ""
	}
	path = strings.TrimSuffix(path, "/investigation")
	// Last segment is the fingerprint.
	idx := strings.LastIndex(path, "/")
	if idx < 0 {
		return ""
	}
	return path[idx+1:]
}

