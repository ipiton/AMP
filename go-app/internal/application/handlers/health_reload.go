package handlers

import (
	"net/http"
	"time"

	appconfig "github.com/ipiton/AMP/internal/config"
)

// ================================================================================
// GET /health/reload (INF-A slice 2)
// ================================================================================
// The verification half of ConfigMap-driven reload: the config-reloader sidecar
// signals the process and then asks this endpoint whether the reload actually
// landed. Also usable by hand — `curl /health/reload` after editing a
// ConfigMap answers "is my change live?", which no other endpoint does.
//
// It reports the FULL outcome, not just ReloadCoordinator's phase label: a
// reload can succeed and still leave part of the operator's edit unapplied
// (W600-W604 restart-required), and a rejected reload can leave the process
// internally split (W610 incomplete rollback, W611 failed post-commit stage).
// An endpoint that reported only the phase would answer "success" in the first
// case and "rolled_back" in the second, both of which under-report.

// ReloadHealthResponse is the JSON body of GET /health/reload.
//
// Field names are part of the sidecar's contract — do not rename without
// updating cmd/config-reloader.
type ReloadHealthResponse struct {
	// Healthy is the single boolean a probe or a script should look at: the
	// last attempt left the file's config in effect AND the process is not
	// split. NOT false merely because something needs a restart — see
	// ReloadStatusSnapshot.Healthy.
	Healthy bool `json:"healthy"`

	// Status is one of config.ReloadStatus* ("success", "no_changes",
	// "validation_failed", "rolled_back", "rollback_failed", ...).
	Status string `json:"status"`

	// Version is the current config version.
	Version int64 `json:"version"`

	// Attempts counts every reload attempt since startup. The sidecar polls
	// this rather than the timestamp: it is monotonic and needs no clock
	// agreement between containers.
	Attempts int64 `json:"attempts"`

	// LastReloadTime is when the status was last set (RFC3339, UTC).
	LastReloadTime time.Time `json:"last_reload_time"`

	// SplitState is true when some components are known to be running a
	// different config from the one reported here. Only a restart clears it.
	SplitState bool `json:"split_state"`

	// RestartRequired lists config the process cannot adopt without a restart:
	// codes, field paths and reasons — never values.
	RestartRequired []appconfig.RestartRequiredWarning `json:"restart_required,omitempty"`
}

// ReloadStatusProvider is the slice of the registry this endpoint needs.
type ReloadStatusProvider interface {
	ReloadStatus() appconfig.ReloadStatusSnapshot
}

// ReloadHealthHandler serves GET /health/reload.
//
// 200 when healthy, 503 when not — so a plain HTTP status check is enough for
// the sidecar and for any external watchdog, without parsing the body. The
// body is served in both cases: a 503 that does not say WHY would just move
// the operator to the logs.
func ReloadHealthHandler(registry ReloadStatusProvider) http.HandlerFunc {
	return getOnly(func(w http.ResponseWriter, _ *http.Request) {
		snapshot := registry.ReloadStatus()

		response := ReloadHealthResponse{
			Healthy:         snapshot.Healthy(),
			Status:          snapshot.Status,
			Version:         snapshot.Version,
			Attempts:        snapshot.Attempts,
			LastReloadTime:  snapshot.LastReloadTime,
			SplitState:      snapshot.SplitState(),
			RestartRequired: snapshot.RestartRequired,
		}

		code := http.StatusOK
		if !response.Healthy {
			code = http.StatusServiceUnavailable
		}
		writeJSON(w, code, response)
	})
}
