// Package contract holds the HTTP contract suites of the upstream
// Alertmanager core (ADR-019), build tag upstreamcore: each test builds an
// upstreamcore.Core from an alertmanager.yml and checks api/v2 and the
// operational endpoints through its handler. testdata/divergences.tsv lists
// the known divergences; scripts/upstream-core-gate.sh fails on any other
// failure and on a stale entry. The package has no code of its own.
package contract
