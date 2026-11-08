package ampapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/core/alertconv"
)

var testLabels = map[string]string{"alertname": "HighLatency", "instance": "a"}

type fakeAlerts struct {
	byAMP, byUpstream map[string]*core.AlertHistoryRecord
	err               error
	deadline          bool
}

func (f *fakeAlerts) get(ctx context.Context, m map[string]*core.AlertHistoryRecord, fp string) (*core.AlertHistoryRecord, error) {
	_, f.deadline = ctx.Deadline()
	if f.err != nil {
		return nil, f.err
	}
	if rec, ok := m[fp]; ok {
		return rec, nil
	}
	return nil, core.ErrAlertNotFound
}

func (f *fakeAlerts) AlertRecordByFingerprint(ctx context.Context, fp string) (*core.AlertHistoryRecord, error) {
	return f.get(ctx, f.byAMP, fp)
}

func (f *fakeAlerts) AlertRecordByUpstreamFingerprint(ctx context.Context, fp string) (*core.AlertHistoryRecord, error) {
	return f.get(ctx, f.byUpstream, fp)
}

type fakeInvestigations struct {
	byAMP map[string]*core.Investigation
	err   error
}

func (f *fakeInvestigations) GetLatestByFingerprint(_ context.Context, fp string) (*core.Investigation, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.byAMP[fp], nil
}

type fixture struct {
	alerts *fakeAlerts
	invs   *fakeInvestigations
	reg    *prometheus.Registry
	h      http.Handler
	ampFP  string
	fnvFP  string
}

func newFixture(t *testing.T, prefix string) *fixture {
	t.Helper()
	ampFP, fnvFP := alertconv.Fingerprint(testLabels), alertconv.UpstreamFingerprint(testLabels)
	local := time.FixedZone("plus1", 3600)
	startsAt := time.Date(2026, 11, 8, 10, 0, 0, 0, local)
	ts := startsAt.Add(time.Minute)
	rec := &core.AlertHistoryRecord{
		Alert: core.Alert{
			Fingerprint: ampFP,
			AlertName:   "HighLatency",
			Status:      core.StatusFiring,
			Labels:      testLabels,
			StartsAt:    startsAt,
			Timestamp:   &ts,
		},
		FirstSeenAt: startsAt,
	}
	errType := core.InvestigationErrorPermanent
	msg := "llm down"
	f := &fixture{
		alerts: &fakeAlerts{
			byAMP:      map[string]*core.AlertHistoryRecord{ampFP: rec},
			byUpstream: map[string]*core.AlertHistoryRecord{fnvFP: rec},
		},
		invs: &fakeInvestigations{byAMP: map[string]*core.Investigation{
			ampFP: {ID: "inv-1", Fingerprint: ampFP, Status: core.InvestigationFailed, QueuedAt: startsAt, CreatedAt: startsAt, UpdatedAt: startsAt, ErrorMessage: &msg, ErrorType: &errType},
		}},
		reg:   prometheus.NewRegistry(),
		ampFP: ampFP,
		fnvFP: fnvFP,
	}
	f.h = New(Options{RoutePrefix: prefix, Alerts: f.alerts, Investigations: f.invs, Logger: slog.New(slog.DiscardHandler), Registerer: f.reg})
	return f
}

func serve(h http.Handler, method, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	var m map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &m), rec.Body.String())
	return m
}

func TestAlertByBothFingerprints(t *testing.T) {
	f := newFixture(t, "")
	for _, fp := range []string{f.fnvFP, f.ampFP, strings.ToUpper(f.fnvFP)} {
		rec := serve(f.h, http.MethodGet, "/api/amp/v1/alerts/"+fp)
		require.Equal(t, http.StatusOK, rec.Code, fp)
		m := decode(t, rec)
		assert.Equal(t, f.fnvFP, m["fingerprint"])
		assert.Equal(t, f.ampFP, m["amp_fingerprint"])
		assert.Equal(t, "HighLatency", m["alert_name"])
		assert.Equal(t, "firing", m["status"])
		assert.Equal(t, "2026-11-08T09:00:00Z", m["starts_at"], "times are UTC")
		assert.Equal(t, "2026-11-08T09:01:00Z", m["last_event_at"])
		assert.Equal(t, map[string]any{}, m["annotations"], "nil map is {}")
		assert.NotContains(t, m, "ends_at")
		assert.NotContains(t, m, "generator_url")
	}
	assert.True(t, f.alerts.deadline, "reads run under the request timeout")
}

// A NULL starts_at reads as the zero time and is left out.
func TestAlertWithoutStartsAt(t *testing.T) {
	f := newFixture(t, "")
	f.alerts.byAMP[f.ampFP].Alert.StartsAt = time.Time{}
	m := decode(t, serve(f.h, http.MethodGet, "/api/amp/v1/alerts/"+f.ampFP))
	assert.NotContains(t, m, "starts_at")
}

func TestAlertErrors(t *testing.T) {
	f := newFixture(t, "")
	for _, tc := range []struct {
		path string
		code int
	}{
		{"/api/amp/v1/alerts/0123456789abcde", http.StatusBadRequest},  // 15 hex
		{"/api/amp/v1/alerts/0123456789abcdeg", http.StatusBadRequest}, // not hex
		{"/api/amp/v1/alerts/..", http.StatusBadRequest},
		{"/api/amp/v1/alerts/0123456789abcdef", http.StatusNotFound},
		{"/api/amp/v1/alerts/" + strings.Repeat("0", 64), http.StatusNotFound},
		{"/api/amp/v1/alerts/", http.StatusNotFound},
		{"/api/amp/v1/alerts/" + f.fnvFP + "/", http.StatusNotFound},
		{"/api/amp/v1/alerts/" + f.fnvFP + "/investigation/", http.StatusNotFound},
		{"/api/amp/v1/alerts//investigation", http.StatusNotFound},
		{"/api/amp/v1/unknown", http.StatusNotFound},
		{"/api/amp/", http.StatusNotFound},
	} {
		rec := serve(f.h, http.MethodGet, tc.path)
		assert.Equal(t, tc.code, rec.Code, tc.path)
		assert.NotEmpty(t, decode(t, rec)["error"], tc.path)
	}

	f.alerts.err = errors.New("pq: connection refused at 10.0.0.1")
	rec := serve(f.h, http.MethodGet, "/api/amp/v1/alerts/"+f.fnvFP)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.NotContains(t, rec.Body.String(), "10.0.0.1", "the database error goes to the log only")
}

func TestMethods(t *testing.T) {
	f := newFixture(t, "")
	rec := serve(f.h, http.MethodHead, "/api/amp/v1/alerts/"+f.fnvFP)
	assert.Equal(t, http.StatusOK, rec.Code)

	for _, path := range []string{"/api/amp/v1/alerts/" + f.fnvFP, "/api/amp/v1/alerts/" + f.fnvFP + "/investigation"} {
		rec = serve(f.h, http.MethodPost, path)
		assert.Equal(t, http.StatusMethodNotAllowed, rec.Code, path)
		assert.Equal(t, "GET, HEAD", rec.Header().Get("Allow"))
		assert.Equal(t, "method not allowed", decode(t, rec)["error"])
	}
}

func TestUnavailableWithoutReaders(t *testing.T) {
	h := New(Options{Logger: slog.New(slog.DiscardHandler), Registerer: prometheus.NewRegistry()})
	for _, path := range []string{"/api/amp/v1/alerts/0123456789abcdef", "/api/amp/v1/alerts/0123456789abcdef/investigation"} {
		rec := serve(h, http.MethodGet, path)
		assert.Equal(t, http.StatusServiceUnavailable, rec.Code, path)
		assert.Contains(t, decode(t, rec)["error"], "PostgreSQL")
	}
}

func TestInvestigation(t *testing.T) {
	f := newFixture(t, "")
	for _, fp := range []string{f.fnvFP, f.ampFP} {
		rec := serve(f.h, http.MethodGet, "/api/amp/v1/alerts/"+fp+"/investigation")
		require.Equal(t, http.StatusOK, rec.Code, fp)
		m := decode(t, rec)
		assert.Equal(t, "inv-1", m["id"])
		assert.Equal(t, f.fnvFP, m["fingerprint"])
		assert.Equal(t, f.ampFP, m["amp_fingerprint"])
		assert.Equal(t, "failed", m["status"])
		assert.Equal(t, "permanent", m["error_type"])
		assert.Equal(t, "llm down", m["error_message"])
		assert.Equal(t, "2026-11-08T09:00:00Z", m["queued_at"])
		assert.NotContains(t, m, "result")
	}

	// An AMP fingerprint without a history row is looked up directly; the
	// response then has no upstream fingerprint.
	orphan := strings.Repeat("a", 64)
	f.invs.byAMP[orphan] = &core.Investigation{ID: "inv-2", Fingerprint: orphan, Status: core.InvestigationQueued}
	m := decode(t, serve(f.h, http.MethodGet, "/api/amp/v1/alerts/"+orphan+"/investigation"))
	assert.Equal(t, "inv-2", m["id"])
	assert.NotContains(t, m, "fingerprint")

	// An upstream fingerprint without a row cannot be resolved.
	rec := serve(f.h, http.MethodGet, "/api/amp/v1/alerts/0123456789abcdef/investigation")
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, "no alert history for this fingerprint", decode(t, rec)["error"])

	delete(f.invs.byAMP, f.ampFP)
	rec = serve(f.h, http.MethodGet, "/api/amp/v1/alerts/"+f.fnvFP+"/investigation")
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, "no investigation for this fingerprint", decode(t, rec)["error"])

	f.invs.err = errors.New("boom")
	assert.Equal(t, http.StatusInternalServerError, serve(f.h, http.MethodGet, "/api/amp/v1/alerts/"+f.fnvFP+"/investigation").Code)
}

// The external URL's path is a plain string prefix, also with pattern
// syntax in it (deep-review F3).
func TestRoutePrefix(t *testing.T) {
	for _, prefix := range []string{"/am", "/am/", "/a{b}"} {
		f := newFixture(t, prefix)
		base := strings.TrimRight(prefix, "/")
		assert.Equal(t, http.StatusOK, serve(f.h, http.MethodGet, base+"/api/amp/v1/alerts/"+f.fnvFP).Code, prefix)
		assert.Equal(t, http.StatusNotFound, serve(f.h, http.MethodGet, "/api/amp/v1/alerts/"+f.fnvFP).Code, prefix)
	}
}

func TestMetrics(t *testing.T) {
	f := newFixture(t, "")
	serve(f.h, http.MethodGet, "/api/amp/v1/alerts/"+f.fnvFP)
	serve(f.h, http.MethodGet, "/api/amp/v1/alerts/nothex")
	serve(f.h, http.MethodGet, "/api/amp/v1/alerts/"+f.fnvFP+"/investigation")
	serve(f.h, http.MethodGet, "/api/amp/v1/unknown")
	serve(f.h, http.MethodPost, "/api/amp/v1/alerts/"+f.fnvFP)

	assert.Equal(t, 4, testutil.CollectAndCount(f.reg, "amp_api_request_duration_seconds"), "405 is not counted")
	for _, labels := range []string{
		`code="200",handler="alert",method="get"`,
		`code="400",handler="alert",method="get"`,
		`code="200",handler="investigation",method="get"`,
		`code="404",handler="other",method="get"`,
	} {
		assert.Contains(t, gatherText(t, f.reg), labels)
	}
}

func gatherText(t *testing.T, reg *prometheus.Registry) string {
	t.Helper()
	mfs, err := reg.Gather()
	require.NoError(t, err)
	var b strings.Builder
	for _, mf := range mfs {
		for _, m := range mf.GetMetric() {
			var parts []string
			for _, lp := range m.GetLabel() {
				parts = append(parts, lp.GetName()+`="`+lp.GetValue()+`"`)
			}
			b.WriteString(strings.Join(parts, ",") + "\n")
		}
	}
	return b.String()
}
