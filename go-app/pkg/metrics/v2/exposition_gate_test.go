package v2

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

func newProbeHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("amp_alerts_total 1"))
	})
}

func serve(t *testing.T, handler http.Handler) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	return recorder
}

func TestExpositionGate_OpenServesMetrics(t *testing.T) {
	gate := NewExpositionGate(true)
	recorder := serve(t, gate.Wrap(newProbeHandler()))

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "amp_alerts_total")
}

func TestExpositionGate_ClosedReturns404NotEmptyBody(t *testing.T) {
	// 404, not 200-with-nothing: an empty scrape looks like a live target
	// reporting zero, which silently zeroes dashboards.
	gate := NewExpositionGate(false)
	recorder := serve(t, gate.Wrap(newProbeHandler()))

	assert.Equal(t, http.StatusNotFound, recorder.Code)
	assert.NotContains(t, recorder.Body.String(), "amp_alerts_total")
	assert.Contains(t, recorder.Body.String(), "metrics.enabled=false")
}

func TestExpositionGate_ToggleAtRuntime(t *testing.T) {
	gate := NewExpositionGate(true)
	handler := gate.Wrap(newProbeHandler())

	assert.Equal(t, http.StatusOK, serve(t, handler).Code)

	gate.SetEnabled(false)
	assert.Equal(t, http.StatusNotFound, serve(t, handler).Code)

	gate.SetEnabled(true)
	assert.Equal(t, http.StatusOK, serve(t, handler).Code)
}

func TestExpositionGate_NilGateStaysExposed(t *testing.T) {
	// A router built before the gate exists must keep the pre-gate behaviour
	// rather than 404-ing or panicking.
	var gate *ExpositionGate
	assert.True(t, gate.Enabled())
	gate.SetEnabled(false) // must not panic
	assert.Equal(t, http.StatusOK, serve(t, gate.Wrap(newProbeHandler())).Code)
}

func TestExpositionGate_ConcurrentToggleAndScrape(t *testing.T) {
	gate := NewExpositionGate(true)
	handler := gate.Wrap(newProbeHandler())

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				serve(t, handler)
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 50; j++ {
			gate.SetEnabled(j%2 == 0)
		}
	}()
	wg.Wait()
}
