package main

import (
	"runtime/debug"
	"testing"

	"github.com/prometheus/common/version"
	"github.com/stretchr/testify/assert"
)

func withVersion(t *testing.T, v string) {
	t.Helper()
	saved := version.Version
	version.Version = v
	t.Cleanup(func() { version.Version = saved })
}

func buildInfo(deps ...*debug.Module) func() (*debug.BuildInfo, bool) {
	return func() (*debug.BuildInfo, bool) { return &debug.BuildInfo{Deps: deps}, true }
}

// karma picks its API mapper by versionInfo.version and drops the connection
// on an empty or non-semver value (ADR-009): without -ldflags it is the
// upstream module's version, a replace's version first.
func TestSetContractVersion(t *testing.T) {
	withVersion(t, "")
	setContractVersion(buildInfo(&debug.Module{Path: "example.com/other", Version: "v9.9.9"},
		&debug.Module{Path: upstreamModule, Version: "v0.34.1"}))
	assert.Equal(t, "0.34.1", version.Version)

	withVersion(t, "")
	setContractVersion(buildInfo(&debug.Module{Path: upstreamModule, Version: "v0.34.1",
		Replace: &debug.Module{Path: "github.com/fork/alertmanager", Version: "v0.34.2-fork.1"}}))
	assert.Equal(t, "0.34.2-fork.1", version.Version, "the replace's version")

	withVersion(t, "")
	setContractVersion(buildInfo(&debug.Module{Path: upstreamModule, Version: "v0.34.1",
		Replace: &debug.Module{Path: "../alertmanager"}}))
	assert.Equal(t, "0.34.1", version.Version, "a directory replace has no version")
}

func TestSetContractVersionKeepsLdflags(t *testing.T) {
	withVersion(t, "0.34.1")
	setContractVersion(buildInfo(&debug.Module{Path: upstreamModule, Version: "v0.30.0"}))
	assert.Equal(t, "0.34.1", version.Version)

	withVersion(t, "")
	setContractVersion(func() (*debug.BuildInfo, bool) { return nil, false })
	assert.Empty(t, version.Version, "no build info: left empty")
}
