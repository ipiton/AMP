# `pkg/core` Overview

This directory contains repository-local domain types and extension-point interfaces used by the current Go codebase and the example sources in `../examples/`.

In the current tree, `pkg/core` is an index directory, not a standalone root package with its own public API surface.

## Current Layout

```text
pkg/core/
├── README.md
├── domain/
│   ├── alert.go
│   ├── classification.go
│   ├── doc.go
│   └── silence.go
└── interfaces/
    ├── classifier.go
    ├── publisher.go
    └── storage.go
```

## Import Paths In Use

Use the concrete subpackages that exist today:

- `github.com/ipiton/AMP/pkg/core/domain`
- `github.com/ipiton/AMP/pkg/core/interfaces`

There is no current `github.com/ipiton/AMP/pkg/core` root package and no `pkg/core/services` subtree in this repository.

## `domain/`

The `domain` package holds core data types used by examples and parts of the runtime. Current files cover:

- alerts
- silences
- classification-related types

See:

- [domain/alert.go](./domain/alert.go)
- [domain/classification.go](./domain/classification.go)
- [domain/silence.go](./domain/silence.go)

## `interfaces/`

The `interfaces` package holds extension-point contracts for:

- alert classification
- alert publishing
- alert storage

See:

- [interfaces/classifier.go](./interfaces/classifier.go)
- [interfaces/publisher.go](./interfaces/publisher.go)
- [interfaces/storage.go](./interfaces/storage.go)

## Usage Notes

- Treat this directory as part of the current repository structure, not as a separately versioned SDK.
- Compatibility or readiness claims for the whole AMP runtime belong in top-level docs, not in this directory README.
- If you need an example of current usage, start with the sources in `../../../examples/`.

## Related Paths

- [examples/custom-classifier/main.go](../../../examples/custom-classifier/main.go)
- [examples/custom-publisher/main.go](../../../examples/custom-publisher/main.go)
- [Repository README](../../../README.md)
- [Contributing Guide](../../../CONTRIBUTING.md)

## License

This directory is covered by the repository's AGPL-3.0 license. See [LICENSE](../../../LICENSE).
