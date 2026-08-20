package config

import "time"

// ================================================================================
// Reload status snapshot (INF-A slice 2)
// ================================================================================
// The config-reloader sidecar signals the process and then has to answer one
// question: "did my reload land?". That needs more than the last phase name —
// it needs to distinguish
//
//   - the signal never arrived (nothing changed at all),
//   - the reload was applied,
//   - the file parsed to an identical config so there was nothing to apply,
//   - the reload was rejected and rolled back,
//   - the reload was rejected and the rollback did NOT complete (split state),
//   - the reload was applied but part of the operator's edit needs a restart.
//
// Hence Attempts (a monotonic counter, clock-free) alongside the status, and
// the restart-required warnings folded in: per the controller ruling this
// endpoint reports the FULL outcome, including the W610/W611 states that
// RollbackCommitted and the post-commit appliers can produce, not just
// ReloadCoordinator's phase label.

// Reload status labels. These are the strings ReloadCoordinator stores, kept
// here so the sidecar, the HTTP handler and the tests share one vocabulary.
const (
	// ReloadStatusInitial: no reload has been attempted since startup.
	ReloadStatusInitial = "initial"

	// ReloadStatusSuccess: the reload was applied.
	ReloadStatusSuccess = "success"

	// ReloadStatusNoChanges: the file was re-read and parsed to a config
	// identical to the live one, so there was nothing to apply. A SUCCESSFUL
	// outcome — and one the sidecar must be able to see, because a
	// comment-only edit changes the file hash without changing the config
	// (before slice 2 this left the status untouched, indistinguishable from
	// "the signal never arrived").
	ReloadStatusNoChanges = "no_changes"

	// ReloadStatusLoadFailed: the file could not be read or parsed.
	ReloadStatusLoadFailed = "load_failed"

	// ReloadStatusValidationFailed: the new config is invalid. Nothing was
	// applied.
	ReloadStatusValidationFailed = "validation_failed"

	// ReloadStatusDiffFailed: the old/new comparison itself failed.
	ReloadStatusDiffFailed = "diff_failed"

	// ReloadStatusApplyFailed: the atomic apply phase failed (lock, storage).
	ReloadStatusApplyFailed = "apply_failed"

	// ReloadStatusRolledBack: a component rejected the config and the previous
	// one was restored everywhere.
	ReloadStatusRolledBack = "rolled_back"

	// ReloadStatusRollbackFailed: a component rejected the config AND the
	// rollback could not be completed. The process is split — see W610.
	ReloadStatusRollbackFailed = "rollback_failed"
)

// CoordinatorStatus is ReloadCoordinator's own view of the last attempt.
type CoordinatorStatus struct {
	// Version is the current config version (incremented on a successful
	// apply, decremented again by a rollback).
	Version int64 `json:"version"`

	// Attempts counts every ReloadFromFile call since startup, successful or
	// not. Monotonic and clock-free: a caller that reads it, signals, then
	// polls until it changes knows its signal was processed, without trusting
	// timestamps.
	Attempts int64 `json:"attempts"`

	// Status is one of the ReloadStatus* constants.
	Status string `json:"status"`

	// LastReloadTime is when Status was last set (UTC).
	LastReloadTime time.Time `json:"last_reload_time"`
}

// ReloadStatusSnapshot is the full, externally reported reload state: the
// coordinator's own status plus everything that can make a "successful" reload
// not actually be in effect.
type ReloadStatusSnapshot struct {
	CoordinatorStatus

	// RestartRequired lists the outstanding W6xx findings. Non-empty does NOT
	// mean the last reload failed — W600-W604 mean "applied what it could, the
	// rest needs a restart" — but it does mean the running process differs
	// from the config file, which is exactly what an operator watching a
	// reload needs to know.
	RestartRequired []RestartRequiredWarning `json:"restart_required,omitempty"`
}

// Applied reports whether the last attempt left the file's config in effect —
// either by applying it or by finding it identical to what was already live.
func (s ReloadStatusSnapshot) Applied() bool {
	switch s.Status {
	case ReloadStatusSuccess, ReloadStatusNoChanges, ReloadStatusInitial:
		return true
	default:
		return false
	}
}

// SplitState reports whether the process is known to be internally
// inconsistent: some components running one config while the reported config is
// another. Only a restart clears this.
//
// Two sources, both from the fix round: a rollback that could not complete
// (W610 / status rollback_failed) and a post-commit applier that failed
// (W611).
func (s ReloadStatusSnapshot) SplitState() bool {
	if s.Status == ReloadStatusRollbackFailed {
		return true
	}
	for _, warning := range s.RestartRequired {
		if warning.Code == WarnReloadRollbackIncomplete || warning.Code == WarnReloadPostCommitFailed {
			return true
		}
	}
	return false
}

// Healthy reports whether the reload subsystem is in a state an operator can
// trust: the last attempt applied, and the process is not split.
//
// Deliberately NOT gated on RestartRequired: a config that legitimately needs a
// restart for one field is a healthy process that has told you so. Making it
// unhealthy would mean a liveness/readiness probe pointed here could
// crash-loop a pod over a `metrics.path` edit.
func (s ReloadStatusSnapshot) Healthy() bool {
	return s.Applied() && !s.SplitState()
}

// StatusSnapshot returns the coordinator's view of the last reload attempt.
func (rc *ReloadCoordinator) StatusSnapshot() CoordinatorStatus {
	rc.mu.RLock()
	defer rc.mu.RUnlock()

	return CoordinatorStatus{
		Version:        rc.reloadVersion,
		Attempts:       rc.reloadAttempts,
		Status:         rc.lastReloadStatus,
		LastReloadTime: rc.lastReloadTime.UTC(),
	}
}
