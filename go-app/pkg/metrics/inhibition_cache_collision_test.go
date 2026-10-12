package metrics_test

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/ipiton/AMP/internal/infrastructure/inhibition"
	"github.com/ipiton/AMP/pkg/metrics"
)

// TestNewBusinessMetrics_CoexistsWithInhibitionCacheMetrics is the regression
// test for the startup panic found by FU-TOPLEVEL-INHIBIT-RULES: since
// PARITY-A2, BusinessMetrics registered its own alert_history_inhibition_cache_*
// collectors under the same names inhibition.GetCacheMetrics registers, so any
// config with an inhibit rule panicked on "duplicate metrics collector
// registration attempted" the moment the inhibition cache came up.
//
// External test package: inhibition imports pkg/metrics, so an in-package
// test could not import it back. Both constructors register into
// prometheus.DefaultRegisterer, so the test swaps in a fresh registry.
func TestNewBusinessMetrics_CoexistsWithInhibitionCacheMetrics(t *testing.T) {
	reg := prometheus.NewRegistry()
	origRegisterer, origGatherer := prometheus.DefaultRegisterer, prometheus.DefaultGatherer
	prometheus.DefaultRegisterer, prometheus.DefaultGatherer = reg, reg
	t.Cleanup(func() {
		prometheus.DefaultRegisterer, prometheus.DefaultGatherer = origRegisterer, origGatherer
	})

	inhibition.GetCacheMetrics()

	// GetCacheMetrics is sync.Once-guarded: had anything in this test binary
	// called it earlier, its collectors would live in another registry and the
	// collision below could not happen — the test would pass vacuously.
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, mf := range families {
		if strings.HasPrefix(mf.GetName(), "alert_history_inhibition_cache_") {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("inhibition cache metrics are not in the test registry; GetCacheMetrics ran earlier, so the test cannot detect the collision")
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("NewBusinessMetrics panicked next to the inhibition cache metrics: %v", r)
		}
	}()
	metrics.NewBusinessMetrics()
}
