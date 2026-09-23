package buildinfo

import (
	"errors"
	"runtime"

	"github.com/prometheus/client_golang/prometheus"
)

// AlertmanagerCompatVersion is the upstream Alertmanager version whose API
// contract AMP implements, as reported in the `alertmanager_build_info`
// metric. It is deliberately NOT AMP's own build version (see Register).
//
// The value is tied to `docs/ALERTMANAGER_COMPATIBILITY.md` ("Alertmanager
// Version: v0.27+ (API v2)") and moves only together with that line — raising
// it here without re-verifying parity would claim upstream behaviour AMP has
// never been checked against.
//
// Two hard constraints on whatever this string becomes:
//
//   - It must stay valid semver. Ecosystem tooling parses it with
//     Masterminds/semver's MustParse, which PANICS on anything else — "dev"
//     and "v0.0.2-513-g383ce8c" are not acceptable values.
//   - It must stay >= 0.22.0. karma (and anything modelled on it) resolves an
//     API mapper through a `>=0.22.0` constraint and drops the connection
//     entirely when no mapper matches, so a lower value is worse than
//     exporting no metric at all.
//
// TestAlertmanagerBuildInfo_KarmaVersionProbe pins both constraints.
const AlertmanagerCompatVersion = "0.27.0"

// newAlertmanagerBuildInfoCollector builds the upstream-shaped
// `alertmanager_build_info` gauge.
//
// `version` answers "which Alertmanager contract do I get", which is what the
// metric means upstream and what every consumer reads it for. The remaining
// labels describe the binary that actually serves that contract, so they carry
// AMP's real build data rather than anything synthesised.
func newAlertmanagerBuildInfoCollector() prometheus.Collector {
	gauge := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "alertmanager_build_info",
		Help: "Version of the upstream Alertmanager API contract implemented by this AMP build (NOT AMP's own version — see amp_build_info for that).",
	}, []string{"version", "revision", "branch", "goversion"})

	gauge.WithLabelValues(
		AlertmanagerCompatVersion,
		Revision,
		Branch,
		runtime.Version(),
	).Set(1)

	return gauge
}

// newAMPBuildInfoCollector builds the `amp_build_info` gauge: AMP's own build
// metadata, none of it compatibility-shaped.
func newAMPBuildInfoCollector() prometheus.Collector {
	gauge := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "amp_build_info",
		Help: "Build metadata of this AMP binary (version, revision, branch, Go version, build user and date).",
	}, []string{"version", "revision", "branch", "goversion", "build_user", "build_date"})

	gauge.WithLabelValues(
		Version,
		Revision,
		Branch,
		runtime.Version(),
		BuildUser,
		BuildDate,
	).Set(1)

	return gauge
}

// Register registers both build-info collectors with r.
//
// Deliberately a function taking a Registerer rather than a promauto/init()
// registration into the default registry: tests need to assemble a clean
// registry, and an init() would make that impossible.
//
// Re-registering the same collectors is not fatal. prometheus returns
// AlreadyRegisteredError for that case and Register swallows it, so a caller
// that initialises the service registry twice in one process (which tests do)
// does not take the process down over a metric that is already there.
func Register(r prometheus.Registerer) error {
	if r == nil {
		return nil
	}

	for _, collector := range []prometheus.Collector{
		newAlertmanagerBuildInfoCollector(),
		newAMPBuildInfoCollector(),
	} {
		if err := r.Register(collector); err != nil {
			var already prometheus.AlreadyRegisteredError
			if errors.As(err, &already) {
				continue
			}
			return err
		}
	}

	return nil
}
