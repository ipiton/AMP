package handlers

import (
	"net/http"
	"time"

	"github.com/ipiton/AMP/internal/infrastructure/inhibition"
)

// InhibitionsRegistryProvider provides access to the inhibition state manager.
type InhibitionsRegistryProvider interface {
	InhibitionState() inhibition.InhibitionStateManager
}

// inhibitionResponse is the Alertmanager-compatible response for a single inhibition.
type inhibitionResponse struct {
	TargetFingerprint string  `json:"targetFingerprint"`
	SourceFingerprint string  `json:"sourceFingerprint"`
	RuleName          string  `json:"ruleName"`
	InhibitedAt       string  `json:"inhibitedAt"`
	ExpiresAt         *string `json:"expiresAt,omitempty"`
}

// InhibitionsHandler handles GET /api/v2/inhibitions.
// Returns the list of currently active inhibitions (Alertmanager parity, PARITY-A2).
func InhibitionsHandler(registry InhibitionsRegistryProvider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		stateManager := registry.InhibitionState()
		if stateManager == nil {
			// Inhibition not configured — return empty list (graceful degradation)
			writeJSON(w, http.StatusOK, []inhibitionResponse{})
			return
		}

		inhibitions, err := stateManager.GetActiveInhibitions(r.Context())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{
				"error": "failed to retrieve inhibitions: " + err.Error(),
			})
			return
		}

		resp := make([]inhibitionResponse, 0, len(inhibitions))
		for _, state := range inhibitions {
			item := inhibitionResponse{
				TargetFingerprint: state.TargetFingerprint,
				SourceFingerprint: state.SourceFingerprint,
				RuleName:          state.RuleName,
				InhibitedAt:       state.InhibitedAt.UTC().Format(time.RFC3339),
			}
			if state.ExpiresAt != nil {
				s := state.ExpiresAt.UTC().Format(time.RFC3339)
				item.ExpiresAt = &s
			}
			resp = append(resp, item)
		}

		writeJSON(w, http.StatusOK, resp)
	}
}
