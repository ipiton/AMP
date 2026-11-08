package buildinfo

import (
	"errors"
	"runtime"

	"github.com/prometheus/client_golang/prometheus"
)

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

// Register registers the amp_build_info collector with r.
// alertmanager_build_info is upstream's: amp-upstream registers
// prometheus/common/version's collector, whose version is the upstream
// module's (ADR-009: karma reads it as the API contract).
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

	if err := r.Register(newAMPBuildInfoCollector()); err != nil {
		var already prometheus.AlreadyRegisteredError
		if errors.As(err, &already) {
			return nil
		}
		return err
	}
	return nil
}
