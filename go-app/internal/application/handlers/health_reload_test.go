package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	appconfig "github.com/ipiton/AMP/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubReloadStatus is a ReloadStatusProvider returning a fixed snapshot.
type stubReloadStatus struct {
	snapshot appconfig.ReloadStatusSnapshot
}

func (s stubReloadStatus) ReloadStatus() appconfig.ReloadStatusSnapshot { return s.snapshot }

func callReloadHealth(t *testing.T, snapshot appconfig.ReloadStatusSnapshot) (*httptest.ResponseRecorder, ReloadHealthResponse) {
	t.Helper()

	recorder := httptest.NewRecorder()
	ReloadHealthHandler(stubReloadStatus{snapshot: snapshot})(
		recorder, httptest.NewRequest(http.MethodGet, "/health/reload", nil))

	var body ReloadHealthResponse
	if recorder.Body.Len() > 0 {
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	}
	return recorder, body
}

func TestReloadHealth_SuccessIs200(t *testing.T) {
	at := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)

	recorder, body := callReloadHealth(t, appconfig.ReloadStatusSnapshot{
		CoordinatorStatus: appconfig.CoordinatorStatus{
			Version: 7, Attempts: 3, Status: appconfig.ReloadStatusSuccess, LastReloadTime: at,
		},
	})

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.True(t, body.Healthy)
	assert.False(t, body.SplitState)
	assert.Equal(t, appconfig.ReloadStatusSuccess, body.Status)
	assert.Equal(t, int64(7), body.Version)
	assert.Equal(t, int64(3), body.Attempts)
	assert.Equal(t, at, body.LastReloadTime.UTC())
	assert.Empty(t, body.RestartRequired)
}

// TestReloadHealth_NoChangesIsHealthy: a comment-only edit parses to an
// identical config. The sidecar must be able to see that as a successful
// outcome, distinguished from a lost trigger only by the attempt counter.
func TestReloadHealth_NoChangesIsHealthy(t *testing.T) {
	recorder, body := callReloadHealth(t, appconfig.ReloadStatusSnapshot{
		CoordinatorStatus: appconfig.CoordinatorStatus{
			Attempts: 12, Status: appconfig.ReloadStatusNoChanges,
		},
	})

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.True(t, body.Healthy)
	assert.Equal(t, int64(12), body.Attempts)
}

func TestReloadHealth_InitialIsHealthy(t *testing.T) {
	recorder, body := callReloadHealth(t, appconfig.ReloadStatusSnapshot{
		CoordinatorStatus: appconfig.CoordinatorStatus{Status: appconfig.ReloadStatusInitial},
	})

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.True(t, body.Healthy, "a process that has never reloaded is healthy, not broken")
}

// TestReloadHealth_RestartRequiredStaysHealthy is the honesty/usability
// balance: the reload DID apply, and part of the operator's edit needs a
// restart. Reporting that as unhealthy would let a probe pointed here
// crash-loop a pod over a `metrics.path` edit.
func TestReloadHealth_RestartRequiredStaysHealthyButIsReported(t *testing.T) {
	recorder, body := callReloadHealth(t, appconfig.ReloadStatusSnapshot{
		CoordinatorStatus: appconfig.CoordinatorStatus{Status: appconfig.ReloadStatusSuccess, Attempts: 2},
		RestartRequired: []appconfig.RestartRequiredWarning{{
			Code:      appconfig.WarnDatabaseRestartRequired,
			Component: "database",
			Fields:    []string{"database.host"},
			Reason:    "pool handle is shared",
		}},
	})

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.True(t, body.Healthy)
	assert.False(t, body.SplitState)
	require.Len(t, body.RestartRequired, 1)
	assert.Equal(t, appconfig.WarnDatabaseRestartRequired, body.RestartRequired[0].Code)
	assert.Equal(t, []string{"database.host"}, body.RestartRequired[0].Fields)
}

func TestReloadHealth_RejectedReloadIs503(t *testing.T) {
	for _, status := range []string{
		appconfig.ReloadStatusValidationFailed,
		appconfig.ReloadStatusLoadFailed,
		appconfig.ReloadStatusDiffFailed,
		appconfig.ReloadStatusApplyFailed,
		appconfig.ReloadStatusRolledBack,
	} {
		t.Run(status, func(t *testing.T) {
			recorder, body := callReloadHealth(t, appconfig.ReloadStatusSnapshot{
				CoordinatorStatus: appconfig.CoordinatorStatus{Status: status, Attempts: 5},
			})

			assert.Equal(t, http.StatusServiceUnavailable, recorder.Code)
			assert.False(t, body.Healthy)
			assert.Equal(t, status, body.Status, "the body must say WHY, not just 503")
			assert.Equal(t, int64(5), body.Attempts, "the counter must advance even for a rejected reload")
		})
	}
}

// TestReloadHealth_SplitStateIsReported covers the two fix-round outcomes the
// controller ruled must reach this endpoint: an incomplete rollback (W610) and
// a failed post-commit stage (W611).
func TestReloadHealth_SplitStateIsReported(t *testing.T) {
	t.Run("rollback_failed status", func(t *testing.T) {
		recorder, body := callReloadHealth(t, appconfig.ReloadStatusSnapshot{
			CoordinatorStatus: appconfig.CoordinatorStatus{Status: appconfig.ReloadStatusRollbackFailed},
		})

		assert.Equal(t, http.StatusServiceUnavailable, recorder.Code)
		assert.False(t, body.Healthy)
		assert.True(t, body.SplitState)
	})

	t.Run("W610 warning", func(t *testing.T) {
		recorder, body := callReloadHealth(t, appconfig.ReloadStatusSnapshot{
			CoordinatorStatus: appconfig.CoordinatorStatus{Status: appconfig.ReloadStatusRolledBack},
			RestartRequired: []appconfig.RestartRequiredWarning{{
				Code:      appconfig.WarnReloadRollbackIncomplete,
				Component: "reload-coordinator",
				Fields:    []string{"redis"},
			}},
		})

		assert.Equal(t, http.StatusServiceUnavailable, recorder.Code)
		assert.True(t, body.SplitState)
	})

	t.Run("W611 warning over a successful status", func(t *testing.T) {
		// The post-commit stage failed AFTER the coordinator committed, so the
		// coordinator's own label can read rolled_back or even success. The
		// warning is what makes it unhealthy.
		recorder, body := callReloadHealth(t, appconfig.ReloadStatusSnapshot{
			CoordinatorStatus: appconfig.CoordinatorStatus{Status: appconfig.ReloadStatusSuccess},
			RestartRequired: []appconfig.RestartRequiredWarning{{
				Code:      appconfig.WarnReloadPostCommitFailed,
				Component: "service-registry",
				Fields:    []string{"routing"},
			}},
		})

		assert.Equal(t, http.StatusServiceUnavailable, recorder.Code)
		assert.False(t, body.Healthy)
		assert.True(t, body.SplitState)
	})
}

func TestReloadHealth_RejectsNonGET(t *testing.T) {
	recorder := httptest.NewRecorder()
	ReloadHealthHandler(stubReloadStatus{})(
		recorder, httptest.NewRequest(http.MethodPost, "/health/reload", nil))

	assert.Equal(t, http.StatusMethodNotAllowed, recorder.Code)
}

// TestReloadStatusSnapshot_Classification pins the shared logic the handler and
// the sidecar both rely on.
func TestReloadStatusSnapshot_Classification(t *testing.T) {
	cases := []struct {
		status  string
		applied bool
	}{
		{appconfig.ReloadStatusInitial, true},
		{appconfig.ReloadStatusSuccess, true},
		{appconfig.ReloadStatusNoChanges, true},
		{appconfig.ReloadStatusLoadFailed, false},
		{appconfig.ReloadStatusValidationFailed, false},
		{appconfig.ReloadStatusDiffFailed, false},
		{appconfig.ReloadStatusApplyFailed, false},
		{appconfig.ReloadStatusRolledBack, false},
		{appconfig.ReloadStatusRollbackFailed, false},
	}

	for _, testCase := range cases {
		snapshot := appconfig.ReloadStatusSnapshot{
			CoordinatorStatus: appconfig.CoordinatorStatus{Status: testCase.status},
		}
		assert.Equal(t, testCase.applied, snapshot.Applied(), "Applied() for %q", testCase.status)
		assert.Equal(t, testCase.applied, snapshot.Healthy(), "Healthy() for %q", testCase.status)
	}
}
