# Alert History - OSS Core

**Package**: `github.com/yourusername/alertmanager-plusplus/pkg/core`
**Status**: ✅ **Production-Ready**
**License**: Apache 2.0

---

## 📚 Overview

This package contains the **pure OSS core** of Alert History Service - domain models, interfaces, and core services that are 100% open source with no dependencies on paid features.

### Design Principles

1. **Zero Paid Dependencies** - Core has NO knowledge of paid/enterprise features
2. **Extension Points** - Clean interfaces for plugins/extensions
3. **Alertmanager Compatible** - 100% API v2 compatibility
4. **Production Ready** - Battle-tested in production environments
5. **Well Documented** - Every interface has clear contracts

---

## 📁 Package Structure

```
pkg/core/
├── README.md           # This file
├── domain/             # Domain models (Alert, Silence, etc.)
│   ├── alert.go
│   ├── silence.go
│   ├── route.go
│   ├── template.go
│   └── ...
├── interfaces/         # Core interfaces & extension points
│   ├── storage.go      # Storage backends
│   ├── classifier.go   # Alert classification
│   ├── publisher.go    # Publishing targets
│   ├── enricher.go     # Alert enrichment
│   └── ...
└── services/           # Core business logic
    ├── processor.go    # Alert processing pipeline
    ├── filter.go       # Alert filtering
    ├── dedup.go        # Deduplication
    └── ...
```

---

## 🎯 Core Components

### 1. Domain Models (`domain/`)

Pure domain entities with zero external dependencies:

- **Alert** - Core alert data model (Alertmanager compatible)
- **Silence** - Silence rules for alert suppression
- **Route** - Routing configuration for alert delivery
- **InhibitionRule** - Rules for alert inhibition
- **Template** - Notification templates

**Key Principle**: Domain models are **framework-agnostic** and contain only business logic.

### 2. Interfaces (`interfaces/`)

Clean extension points for pluggable components:

```go
// Storage - How alerts are persisted
type StorageBackend interface {
    Store(ctx context.Context, alert *Alert) error
    Query(ctx context.Context, filters Filters) ([]*Alert, error)
}

// Classifier - How alerts are classified (OSS: rules, Paid: LLM)
type AlertClassifier interface {
    Classify(ctx context.Context, alert *Alert) (*Classification, error)
}

// Publisher - How alerts are sent to external systems
type AlertPublisher interface {
    Publish(ctx context.Context, alert *Alert, target Target) error
}

// Enricher - How alerts are enhanced with metadata
type AlertEnricher interface {
    Enrich(ctx context.Context, alert *Alert) (*EnrichedAlert, error)
}
```

**Key Principle**: Interfaces define **contracts**, not implementations.

### 3. Core Services (`services/`)

Business logic that orchestrates domain models:

- **AlertProcessor** - Main processing pipeline (dedupe → filter → classify → publish)
- **FilterEngine** - Alert filtering logic
- **DeduplicationService** - Duplicate alert detection
- **FingerprintGenerator** - Alert fingerprinting

**Key Principle**: Services **compose** domain models and interfaces, contain NO infrastructure code.

---

## 🔌 Extension Points

Alert History is designed to be **extended without modifying core**. Here's how:

### Built-in OSS Implementations

| Interface | OSS Implementation | Location |
|-----------|-------------------|----------|
| `StorageBackend` | PostgreSQL, SQLite | `go-app/internal/adapters/storage/` |
| `AlertClassifier` | Rule-based | `go-app/internal/adapters/classifier/` |
| `AlertPublisher` | Slack, PagerDuty, Webhook | `go-app/internal/adapters/publishers/` |
| `AlertEnricher` | Basic metadata | `go-app/internal/adapters/enricher/` |
| `CacheBackend` | Redis, Memory | `go-app/internal/adapters/cache/` |

### Plugin Your Own Implementations

```go
// Example: Custom ML-based classifier
type MLClassifier struct {
    model *ml.Model
}

func (c *MLClassifier) Classify(ctx context.Context, alert *core.Alert) (*core.Classification, error) {
    // Your custom ML logic
    prediction := c.model.Predict(alert)
    return &core.Classification{
        Severity: prediction.Severity,
        Confidence: prediction.Confidence,
    }, nil
}

// Register your classifier
registry.RegisterClassifier("ml-classifier", &MLClassifier{...})
```

**No core modifications needed!** Just implement the interface.

---

## 🏗️ Architecture Diagram

```
┌─────────────────────────────────────────────────────────┐
│                    HTTP Handlers                         │
│          (go-app/cmd/server/handlers/)                   │
└────────────────────┬────────────────────────────────────┘
                     │
                     ▼
┌─────────────────────────────────────────────────────────┐
│                  Core Services                           │
│              (pkg/core/services/)                        │
│  • AlertProcessor  • FilterEngine  • DeduplicationSvc   │
└────────────┬────────────────┬───────────────────────────┘
             │                │
             ▼                ▼
┌────────────────────┐  ┌──────────────────────────────┐
│  Domain Models     │  │  Core Interfaces             │
│  (pkg/core/domain/)│  │  (pkg/core/interfaces/)      │
│                    │  │  • StorageBackend            │
│  • Alert           │  │  • AlertClassifier           │
│  • Silence         │  │  • AlertPublisher            │
│  • Route           │  │  • AlertEnricher             │
└────────────────────┘  └──────────┬───────────────────┘
                                   │
                                   ▼
                    ┌──────────────────────────────────┐
                    │  Adapter Implementations         │
                    │  (go-app/internal/adapters/)     │
                    │  • PostgreSQL Storage            │
                    │  • Redis Cache                   │
                    │  • Slack Publisher               │
                    │  • Rule-based Classifier (OSS)   │
                    │  • LLM Classifier (optional)     │
                    └──────────────────────────────────┘
```

**Key**: Core (`pkg/core`) knows nothing about adapters. Adapters implement core interfaces.

---

## 🚀 Usage Examples

### Example 1: Process an Alert

```go
import (
    "github.com/yourusername/alertmanager-plusplus/pkg/core"
    "github.com/yourusername/alertmanager-plusplus/pkg/core/services"
)

// Create alert
alert := &core.Alert{
    Labels: map[string]string{
        "alertname": "HighCPU",
        "severity":  "critical",
    },
    Annotations: map[string]string{
        "summary": "CPU usage > 90%",
    },
}

// Process through pipeline
processor := services.NewAlertProcessor(config)
result, err := processor.Process(ctx, alert)
```

### Example 2: Implement Custom Classifier

```go
// Implement interface from pkg/core/interfaces
type MyClassifier struct {}

func (c *MyClassifier) Classify(ctx context.Context, alert *core.Alert) (*core.Classification, error) {
    // Your logic here
    return &core.Classification{
        Severity:   "critical",
        Confidence: 0.95,
        Reasoning:  "High CPU + production",
    }, nil
}

// Use it
classifier := &MyClassifier{}
classification, _ := classifier.Classify(ctx, alert)
```

### Example 3: Custom Storage Backend

```go
// Implement StorageBackend interface
type MyStorage struct {
    db *sql.DB
}

func (s *MyStorage) Store(ctx context.Context, alert *core.Alert) error {
    // Your persistence logic
    return s.db.Insert(alert)
}

func (s *MyStorage) Query(ctx context.Context, filters core.Filters) ([]*core.Alert, error) {
    // Your query logic
    return s.db.Select(filters)
}

// Register and use
storage := &MyStorage{db: myDB}
processor := services.NewAlertProcessor(
    services.WithStorage(storage),
)
```

---

## 📖 API Documentation

### Domain Models

See [domain/alert.go](domain/alert.go) for complete `Alert` model documentation.

**Key Types**:
- `Alert` - Core alert structure (Alertmanager compatible)
- `Silence` - Silence rule with matchers
- `Route` - Routing configuration
- `InhibitionRule` - Inhibition rule
- `Classification` - Alert classification result
- `EnrichedAlert` - Alert with additional metadata

### Interfaces

See [interfaces/](interfaces/) for all extension point interfaces.

**Core Interfaces**:
- `StorageBackend` - Alert persistence
- `AlertClassifier` - Classification logic
- `AlertPublisher` - External delivery
- `AlertEnricher` - Metadata enrichment
- `CacheBackend` - Caching layer
- `FilterEngine` - Filtering logic

### Services

See [services/](services/) for business logic services.

**Core Services**:
- `AlertProcessor` - Main processing pipeline
- `DeduplicationService` - Duplicate detection
- `FilterEngine` - Alert filtering
- `FingerprintGenerator` - Alert fingerprinting

---

## 🧪 Testing

Core package has **high test coverage** (90%+):

```bash
# Run core tests
go test ./pkg/core/... -v

# Check coverage
go test ./pkg/core/... -cover -coverprofile=coverage.out
go tool cover -html=coverage.out

# Run benchmarks
go test ./pkg/core/... -bench=. -benchmem
```

---

## 🤝 Contributing

This is the **OSS core** - contributions welcome!

### Adding New Features

1. **Domain models** → Add to `pkg/core/domain/`
2. **Extension points** → Add interface to `pkg/core/interfaces/`
3. **Core logic** → Add service to `pkg/core/services/`
4. **Implementation** → Add adapter to `go-app/internal/adapters/`

### Code Guidelines

- ✅ **No external dependencies** in `pkg/core` (except stdlib)
- ✅ **Interfaces over implementations** - define contracts
- ✅ **Framework-agnostic** - no HTTP, no database, no cache in core
- ✅ **100% test coverage** for new code
- ✅ **Godoc comments** for all public types/functions

---

## 📦 Dependencies

**Core package has ZERO external dependencies!** Only Go stdlib.

This ensures:
- ✅ **Stable API** - No breaking changes from external libs
- ✅ **Fast builds** - No transitive dependencies
- ✅ **Easy adoption** - No dependency conflicts
- ✅ **Production ready** - Battle-tested stdlib only

---

## 🔒 Stability Guarantee

`pkg/core` follows **semantic versioning** with strong compatibility guarantees:

- **v1.x.x** - Current stable version
- **Breaking changes** - Only in major versions (v2.x.x)
- **Additive changes** - In minor versions (v1.1.x)
- **Bug fixes** - In patch versions (v1.0.1)

**Compatibility**: We guarantee backward compatibility within major versions.

---

## 📚 Learn More

- **Architecture**: [../../docs/ARCHITECTURE.md](../../docs/ARCHITECTURE.md)
- **API Reference**: [godoc.org](https://godoc.org/github.com/yourusername/alertmanager-plusplus/pkg/core)
- **Examples**: [../../examples/](../../examples/)
- **Contributing**: [../../CONTRIBUTING.md](../../CONTRIBUTING.md)

---

**Maintained by**: Alert History Community
**License**: Apache 2.0
**Status**: Production-Ready ✅
