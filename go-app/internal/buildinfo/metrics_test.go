package buildinfo

import (
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
)

// scrape registers the build-info collectors into a clean registry and returns
// the exposition text a scraper (or karma's version probe) would read.
func scrape(t *testing.T) string {
	t.Helper()

	registry := prometheus.NewRegistry()
	if err := Register(registry); err != nil {
		t.Fatalf("Register: %v", err)
	}

	server := httptest.NewServer(promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
	defer server.Close()

	resp, err := http.Get(server.URL)
	if err != nil {
		t.Fatalf("scrape request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("scrape status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read scrape body: %v", err)
	}

	return string(body)
}

// labelsOf finds metric family `name` in the exposition text and returns its
// single sample's labels. It parses the text exactly the way karma's
// internal/verprobe does, so a change that breaks the parse breaks this test.
func labelsOf(t *testing.T, exposition, name string) map[string]string {
	t.Helper()

	parser := expfmt.NewTextParser(model.UTF8Validation)
	families, err := parser.TextToMetricFamilies(strings.NewReader(exposition))
	if err != nil {
		t.Fatalf("parse exposition: %v", err)
	}

	family, ok := families[name]
	if !ok {
		t.Fatalf("metric family %q not exported; got %v", name, familyNames(families))
	}

	if len(family.Metric) != 1 {
		t.Fatalf("metric %q has %d samples, want exactly 1", name, len(family.Metric))
	}

	labels := map[string]string{}
	for _, label := range family.Metric[0].Label {
		labels[label.GetName()] = label.GetValue()
	}

	return labels
}

func familyNames[T any](families map[string]T) []string {
	names := make([]string, 0, len(families))
	for name := range families {
		names = append(names, name)
	}
	return names
}

// TestAMPBuildInfo_ReportsRealBuild: AMP's own build metadata is published,
// unmodified, under its own metric name.
func TestAMPBuildInfo_ReportsRealBuild(t *testing.T) {
	labels := labelsOf(t, scrape(t), "amp_build_info")

	for name, want := range map[string]string{
		"version":    Version,
		"revision":   Revision,
		"branch":     Branch,
		"goversion":  runtime.Version(),
		"build_user": BuildUser,
		"build_date": BuildDate,
	} {
		if got := labels[name]; got != want {
			t.Errorf("amp_build_info{%s} = %q, want %q", name, got, want)
		}
	}
}

// TestRegister_Idempotent pins the reason Register swallows
// AlreadyRegisteredError: ServiceRegistry.Initialize runs more than once in a
// single test binary, and a panic (or a hard error) there would take the
// process down over a gauge that is already present.
func TestRegister_Idempotent(t *testing.T) {
	registry := prometheus.NewRegistry()

	if err := Register(registry); err != nil {
		t.Fatalf("first Register: %v", err)
	}

	if err := Register(registry); err != nil {
		t.Errorf("second Register: %v, want nil (AlreadyRegisteredError must be swallowed)", err)
	}

	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}

	for _, family := range families {
		if len(family.Metric) != 1 {
			t.Errorf("after double Register, %q has %d samples, want 1", family.GetName(), len(family.Metric))
		}
	}
}

// TestRegister_NilRegisterer documents the nil guard: a caller without a
// registry is a no-op, not a panic.
func TestRegister_NilRegisterer(t *testing.T) {
	if err := Register(nil); err != nil {
		t.Errorf("Register(nil) = %v, want nil", err)
	}
}
