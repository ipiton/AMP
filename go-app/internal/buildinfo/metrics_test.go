package buildinfo

import (
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"

	"github.com/Masterminds/semver/v3"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
)

// karmaMapperConstraint is the version range karma resolves its Alertmanager
// API mapper through (internal/mapper/v017/{alerts,silences}.go). No mapper
// match means karma drops the connection and clears the alerts it already
// showed, so failing this constraint is worse than exporting no build-info
// metric at all — with no metric karma falls back to "assume latest".
const karmaMapperConstraint = ">=0.22.0"

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

// TestAlertmanagerBuildInfo_KarmaVersionProbe reproduces karma's whole version
// discovery path — scrape /metrics, parse the exposition, read the `version`
// label off alertmanager_build_info, cut it at the first dash (karma's
// fixSemVersion), parse it as semver and check the mapper constraint.
//
// It deliberately asserts the ALGORITHM rather than the literal string: a test
// that only greps for "0.27.0" would still pass if the value were changed to
// something karma cannot use.
func TestAlertmanagerBuildInfo_KarmaVersionProbe(t *testing.T) {
	labels := labelsOf(t, scrape(t), "alertmanager_build_info")

	raw, ok := labels["version"]
	if !ok {
		t.Fatalf("alertmanager_build_info has no `version` label; karma reads exactly this label")
	}

	// karma: strings.SplitN(version, "-", 2)[0]
	trimmed := strings.SplitN(raw, "-", 2)[0]

	parsed, err := semver.NewVersion(trimmed)
	if err != nil {
		// karma uses semver.MustParse here, which panics rather than erroring.
		t.Fatalf("version %q (trimmed from %q) is not valid semver: %v", trimmed, raw, err)
	}

	constraint, err := semver.NewConstraint(karmaMapperConstraint)
	if err != nil {
		t.Fatalf("constraint %q: %v", karmaMapperConstraint, err)
	}

	if !constraint.Check(parsed) {
		t.Errorf("version %q does not satisfy karma's mapper constraint %s — karma would find no mapper and drop the connection",
			parsed, karmaMapperConstraint)
	}
}

// TestAlertmanagerBuildInfo_ReportsContractNotBuild pins the decision from
// ADR-009: the compat metric reports the Alertmanager contract version, while
// the build's own version (which is `git describe` output and would fail the
// constraint above) stays out of it.
func TestAlertmanagerBuildInfo_ReportsContractNotBuild(t *testing.T) {
	labels := labelsOf(t, scrape(t), "alertmanager_build_info")

	if got := labels["version"]; got != AlertmanagerCompatVersion {
		t.Errorf("version = %q, want the compat constant %q", got, AlertmanagerCompatVersion)
	}

	if labels["version"] == Version {
		t.Errorf("version label leaked AMP's own build version %q into the compatibility metric", Version)
	}

	// The non-version labels describe the binary serving the contract, so they
	// must NOT be synthesised.
	if got := labels["revision"]; got != Revision {
		t.Errorf("revision = %q, want %q", got, Revision)
	}
	if got := labels["branch"]; got != Branch {
		t.Errorf("branch = %q, want %q", got, Branch)
	}
	if got := labels["goversion"]; got != runtime.Version() {
		t.Errorf("goversion = %q, want %q", got, runtime.Version())
	}
}

// TestAMPBuildInfo_ReportsRealBuild covers the other half of the split: AMP's
// own build metadata is published, unmodified, under its own metric name.
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

// TestCompatVersionConstant guards the constant itself, independent of the
// exposition path: whatever it is edited to must stay parseable and within
// karma's range. Keeps a future bump from failing only at scrape time.
func TestCompatVersionConstant(t *testing.T) {
	parsed, err := semver.NewVersion(AlertmanagerCompatVersion)
	if err != nil {
		t.Fatalf("AlertmanagerCompatVersion %q is not valid semver: %v", AlertmanagerCompatVersion, err)
	}

	constraint, err := semver.NewConstraint(karmaMapperConstraint)
	if err != nil {
		t.Fatalf("constraint: %v", err)
	}

	if !constraint.Check(parsed) {
		t.Errorf("AlertmanagerCompatVersion %q is below karma's mapper constraint %s",
			AlertmanagerCompatVersion, karmaMapperConstraint)
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
