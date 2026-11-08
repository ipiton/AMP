package main

import (
	"runtime/debug"
	"strings"

	"github.com/prometheus/common/version"
)

// upstreamModule is the module whose API contract amp-upstream serves.
const upstreamModule = "github.com/prometheus/alertmanager"

// setContractVersion fills prometheus/common/version.Version, which
// /api/v2/status (versionInfo.version) and alertmanager_build_info report,
// with the upstream module's version when the build did not set it with
// -ldflags. It is the API contract, not AMP's version (amp_build_info):
// karma picks its API mapper by it and drops the connection on an empty or
// non-semver value (ADR-009).
func setContractVersion(info func() (*debug.BuildInfo, bool)) {
	if version.Version != "" {
		return
	}
	bi, ok := info()
	if !ok {
		return
	}
	for _, dep := range bi.Deps {
		if dep.Path == upstreamModule {
			if dep.Replace != nil && dep.Replace.Version != "" {
				dep = dep.Replace
			}
			version.Version = strings.TrimPrefix(dep.Version, "v")
			return
		}
	}
}
