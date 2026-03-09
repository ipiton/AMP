# AMP Extension Examples

This directory contains small reference examples for extension patterns around the current `pkg/core` contracts, plus a few integration snippets for external systems.

The files here are best read as examples of shape and wiring. They are not, by themselves, a promise of a full plugin system or a broader runtime compatibility contract.

## Contents

### `custom-classifier/`

Example classifier implementation that uses the current `pkg/core/domain` and `pkg/core/interfaces` packages.

Primary file:
- [custom-classifier/main.go](./custom-classifier/main.go)

### `custom-publisher/`

Example publisher implementation with custom formatting and HTTP delivery logic.

Primary file:
- [custom-publisher/main.go](./custom-publisher/main.go)

### `k8s/`

Example Kubernetes Secret manifests for external notification integrations.

Files:
- [k8s/pagerduty-secret-example.yaml](./k8s/pagerduty-secret-example.yaml)
- [k8s/rootly-secret-example.yaml](./k8s/rootly-secret-example.yaml)

## How To Use These Examples

- Read the source to understand the current interface shape and data flow.
- Adapt the examples to the runtime wiring you actually use in `go-app/`.
- Treat them as starting points, not drop-in production integrations.

## Related Paths

- [pkg/core overview](../go-app/pkg/core/README.md)
- [Repository README](../README.md)
- [API compatibility notes](../docs/ALERTMANAGER_COMPATIBILITY.md)
- [Contributing guide](../CONTRIBUTING.md)

## Contributing New Examples

If you add a new example:

1. Keep it focused on one integration or extension point.
2. Document any assumptions directly in the example source.
3. Prefer links to current repo docs over hardcoded product claims.

## License

Examples in this repository are covered by the repository's AGPL-3.0 license. See [LICENSE](../LICENSE).
