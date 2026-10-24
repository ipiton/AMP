package upstreamcore

import (
	"runtime/debug"
	"testing"
)

const upstreamModule = "github.com/prometheus/alertmanager"

func TestPortMatchesUpstreamVersion(t *testing.T) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		t.Fatal("no build info in the test binary")
	}
	version := ""
	for _, dep := range info.Deps {
		if dep.Path == upstreamModule {
			version = dep.Version
			if dep.Replace != nil {
				version = dep.Replace.Version
			}
		}
	}
	if version == "" {
		t.Fatalf("%s is not a dependency of the test binary", upstreamModule)
	}
	if version != PortedFrom {
		t.Fatalf("go.mod has %s %s, the port in this package is from %s.\n"+
			"Carry the upstream changes over (scripts/upstream-bump.sh --diff %s %s, docs/UPSTREAM_UPGRADE.md), then set PortedFrom = %q.",
			upstreamModule, version, PortedFrom, PortedFrom, version, version)
	}
}
