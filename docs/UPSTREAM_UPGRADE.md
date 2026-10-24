# Upgrading the Upstream Alertmanager Core

AMP builds on the upstream Alertmanager Go module (ADR-019). This page is the procedure for moving it to a new release.

## One Version, One Place

The upstream version lives in `go-app/go.mod` (`github.com/prometheus/alertmanager`) and nowhere else in executable code. Everything that needs it reads it from there:

- `scripts/upstream-core-gate.sh` builds `amtool` from the module at that version (`go install .../cmd/amtool@<version>`).
- The `amtool-compat` step of `scripts/release-gate.sh` uses the `quay.io/prometheus/alertmanager:<version>` image.
- `upstreamcore.PortedFrom` records which release `internal/upstreamcore` was ported from. `TestPortMatchesUpstreamVersion` fails while it differs from `go.mod`, so a bump cannot leave the port behind silently.

Version strings in `// port of ...@vX.Y.Z` comments are provenance, not version points: they say where a piece of code came from.

## When to Upgrade

- A security fix upstream that affects code AMP links (check with `govulncheck`).
- A behaviour AMP wants, or a release that a later ADR-019 slice depends on.
- Otherwise, every minor release, so the ports never fall more than one minor behind.

New releases sit out a 7-day quarantine before AMP takes them (supply-chain defense). The bump script enforces it.

## Procedure

```bash
./scripts/upstream-bump.sh v0.35.0
```

The script:

1. Refuses to run if `go-app/go.mod` or `go.sum` has uncommitted changes.
2. Runs `go get github.com/prometheus/alertmanager@<version>` and `go mod tidy`.
3. Looks up the publish time of every require that moved, the upstream release included. If any is younger than 7 days, it puts `go.mod` and `go.sum` back and stops.
4. Warns if the `go` or `toolchain` line moved. CI, the image builders and the toolchain line move together (see [CI.md](CI.md), Go Version).
5. Writes a diff of the upstream files AMP carries ports of between the old and the new release, and prints its path.

Then:

1. Carry the diff over into the ports:
   - `app/{app,reloader,metrics,lifecycle}.go` go to `internal/upstreamcore`;
   - `app/url.go` goes to `extURL` in `cmd/amp-upstream`;
   - `template/template.go` goes to `internal/business/templating`;
   - `pkg/labels/parse.go` goes to the matcher parsers in `internal/business/routing` and `pkg/configvalidator/matcher`.

   Then set `upstreamcore.PortedFrom` to the new version.
2. Run `cd go-app && go build ./... && go test ./... -count=1`.
3. Run `./scripts/upstream-core-gate.sh` and work through what it reports (see below).
4. Run `cd go-app && go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...`.
5. Run `./scripts/release-gate.sh`.
6. Commit `go.mod`, `go.sum`, the port changes and any baseline changes together. Explain each baseline change in the commit message.

To see what changed between two releases without bumping:

```bash
./scripts/upstream-bump.sh --diff v0.34.1 v0.35.0
```

## Reading the Upstream Core Gate

`scripts/upstream-core-gate.sh` runs the contract suites (`main_phase0_contract_test.go`, `main_upstream_parity_regression_test.go`) against the upstream core. It compares the failing tests with `go-app/cmd/server/testdata/upstreamcore-divergences.tsv`, the list of known divergences.

Each baseline entry has a class:

| Class | Meaning |
|---|---|
| `a` | AMP-only surface the upstream core does not have (health aliases, UI, `/api/v1`) |
| `b` | The test pinned AMP's own core; upstream is the reference |
| `c` | A gap in AMP's wiring of the upstream core |
| `d` | Upstream behaves differently from a deliberate AMP decision; owner's call |

Each entry also has a flag:

- `fails` means the test must fail.
- `flaky` means it may fail or pass, for example because of upstream's asynchronous dispatch.

After a bump, the gate can report two things:

- **`NEW <test>`**: a failure that is not in the baseline. The new release changed a behaviour the suites check. Decide whether AMP accepts it. If it does, add an entry with the class and the reason. If it does not, fix the wiring.
- **`PASSED <test>` / `NOT RUN <test>`**: a `fails` entry that no longer fails, or a test that no longer exists. Upstream now matches AMP there, or the test was renamed. Remove the entry or update its name.

A baseline that grows needs a reason for every line. Gate failures are never fixed by adding entries without one.

## Quarantine Exceptions

```bash
./scripts/upstream-bump.sh v0.35.1 --allow-fresh="CVE-2026-XXXX fix, owner approved"
```

An exception is the owner's decision, typically for a security fix. The script keeps the bump and prints the reason. Put the reason in the commit message and in the task's `DONE.md`.
