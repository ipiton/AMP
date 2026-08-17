// Package buildinfo holds build-time metadata injected via -ldflags "-X ...".
//
// See go-app/Makefile (build/build-linux targets) and the repo-root
// Dockerfile for the actual -X wiring. Defaults below apply whenever the
// binary is built without those flags (go run, go test, ad-hoc go build).
package buildinfo

var (
	// Version is the AMP release/build version (e.g. a git tag or "dev").
	Version = "dev"
	// Revision is the VCS commit this build was produced from.
	Revision = "unknown"
	// Branch is the VCS branch this build was produced from.
	Branch = "unknown"
	// BuildUser identifies who/what produced the build (CI job, "docker", local user).
	BuildUser = "unknown"
	// BuildDate is the RFC3339 build timestamp.
	BuildDate = "unknown"
)
