# CI And Image Publishing

AMP has two GitHub Actions workflows. Everything a pull request is checked against also runs locally with one script, so a red CI job can be reproduced without GitHub.

| Workflow | Trigger | Permissions | Publishes |
|---|---|---|---|
| [`ci.yml`](../.github/workflows/ci.yml) | every pull request, every push to `main` | `contents: read` | nothing |
| [`release.yml`](../.github/workflows/release.yml) | push of a tag matching `v*` | `contents: read`, `packages: write` (publish job only) | `ghcr.io/ipiton/amp`, `ghcr.io/ipiton/amp-config-reloader` |

Publishing lives in its own workflow so that `packages: write` exists only on tag builds. No pull request, including one from a fork, ever runs with a token that can push images.

All third-party actions are pinned to a full commit SHA (the release tag is in a trailing comment). Tools run with `go run` (`actionlint`, `govulncheck`) or setup actions (`golangci-lint`, `helm`), and each one is pinned to an exact version.

## Jobs In `ci.yml`

| Job | What it runs | Required | Why |
|---|---|---|---|
| `gate` | [`scripts/release-gate.sh`](../scripts/release-gate.sh): build, lint, tests, `futureparity`, `-race` on the concurrency-heavy packages, Helm lint/render/RBAC checks, `amtool` compatibility against a real smoke stack | yes | The release gate is the definition of "releasable". CI runs the same script, unchanged, instead of re-describing it in YAML. |
| `images (amp)`, `images (config-reloader)` | `docker buildx build` of `Dockerfile` / `Dockerfile.config-reloader` for `linux/amd64,linux/arm64`, no push | yes | A change that breaks either image, on either architecture, is caught before the tag rather than by the release. |
| `actionlint` | `actionlint` over `.github/workflows/` | yes | Workflow mistakes otherwise surface only when the workflow runs, and `release.yml` runs only on a tag. |
| `govulncheck` | `govulncheck ./...` in `go-app` | yes | A vulnerability reachable from AMP code blocks a release. It fails only on reachable (symbol-level) findings. The trade-off: the vulnerability database changes over time, so a new advisory can turn a pull request red even if it does not touch dependencies. The fix is a separate pull request that upgrades the module, not a skip. |
| `e2e-ha` | [`deploy/e2e-ha/run.sh`](../deploy/e2e-ha/run.sh): a two-replica HA scenario in Docker | not yet | Built on fixed waits and new on shared runners. Becomes required after 10 consecutive green runs on `main` (`CI-E2E-HA-REQUIRED`). |

"Required" means the check that should be listed in branch protection. The workflow itself does not enforce this. Branch protection is repository configuration: *Settings → Branches → `main` → Require status checks to pass*, with the job names exactly as shown above (`gate`, `images (amp)`, `images (config-reloader)`, `actionlint`, `govulncheck`).

`govulncheck` also lists module-level findings, which do not affect the exit code. One of them is expected and permanent: GO-2026-5932 (`golang.org/x/crypto/openpgp` is unmaintained and has no fix). AMP imports only `golang.org/x/crypto/bcrypt` from that module. Image scanners that match by module version will report it as well.

Runs on the same pull request cancel each other; runs on `main` do not.

### Go Version

`go-app/go.mod` carries `toolchain go1.26.8`. `setup-go` reads it in CI. A local Go older than that, with `GOTOOLCHAIN=auto` (the default), downloads the same patch release. A newer local Go is used as is, so run the checks with `GOTOOLCHAIN=go1.26.8` to match CI: `govulncheck` also reports standard library vulnerabilities, and those depend on the Go version. The image builders use `golang:1.26.8-alpine`. Bump all three together.

## Running It Locally

```bash
./scripts/release-gate.sh
```

It prints a PASS/FAIL table per step. It needs Go, `golangci-lint`, `helm`, and Docker (for `amtool-compat`). The Helm dependency step adds the chart repositories listed in `helm/amp/Chart.lock` to a throwaway Helm repository config, so it works on a clean checkout and leaves your `~/.config/helm` untouched.

The other jobs:

```bash
go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12
(cd go-app && go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...)
./deploy/e2e-ha/run.sh
docker buildx build --platform linux/amd64,linux/arm64 -f Dockerfile .
```

Both Dockerfiles compile Go on the build machine's own architecture and cross-compile to the target (`--platform=$BUILDPLATFORM` plus `GOOS`/`GOARCH`), so a multi-arch build does not run the Go compiler under emulation.

## Releasing Images

1. Push a semver tag from `main`, e.g. `git tag v0.1.0 && git push origin v0.1.0`.
2. `release.yml` builds both images for `linux/amd64` and `linux/arm64` and pushes them to GHCR with these tags:

   | Tag | Example for `v0.1.0` | Notes |
   |---|---|---|
   | `{{version}}` | `0.1.0` | without the leading `v` |
   | `{{major}}.{{minor}}` | `0.1` | moves with each patch release |
   | `sha-<short>` | `sha-1a2b3c4` | the exact commit |
   | `latest` | `latest` | only for releases without a pre-release suffix (`v0.1.0-rc.1` does not move `latest`) |

   The binary's version, revision, branch and build date (`amp_build_info`, `/api/v2/status`) are injected from the tag and commit.
3. **Once, after the very first release:** GHCR creates new packages as **private**. Open *GitHub → Packages → `amp`* and *`amp-config-reloader`* → *Package settings → Change visibility → Public*. Until then, `docker pull` and a Helm install without an image pull secret fail with `unauthorized`.

A tag must match the chart: `helm/amp/values-production.yaml` pins `image.tag`, and that tag has to exist in GHCR before a production install can pull it.

## Not Covered Yet

- SBOM, provenance attestations, image signing (cosign), image scanning, digest-pinned base images, Dependabot for actions, `-race` over the whole module: `CI-SUPPLY-CHAIN`.
- `helm/amp/tests/render-config-reloader.sh` is not part of the gate yet; run it by hand when touching the chart.
