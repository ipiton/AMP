//go:build upstreamcore

package contract

import (
	"os"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/prometheus/common/version"
)

func TestMain(m *testing.M) {
	setBuildInfo()
	code := m.Run()
	stopUpstreamCores()
	os.Exit(code)
}

// setBuildInfo fills prometheus/common/version, which /api/v2/status reports
// as versionInfo, the way cmd/amp-upstream does (setContractVersion there):
// Version is the upstream module's version. The test binary has no -ldflags,
// so the other fields get placeholder values.
func setBuildInfo() {
	if version.Version == "" {
		if bi, ok := debug.ReadBuildInfo(); ok {
			for _, dep := range bi.Deps {
				if dep.Path == "github.com/prometheus/alertmanager" {
					if dep.Replace != nil && dep.Replace.Version != "" {
						dep = dep.Replace
					}
					version.Version = strings.TrimPrefix(dep.Version, "v")
					break
				}
			}
		}
	}
	for _, field := range []*string{&version.Revision, &version.Branch, &version.BuildUser, &version.BuildDate} {
		if *field == "" {
			*field = "contract-test"
		}
	}
}
