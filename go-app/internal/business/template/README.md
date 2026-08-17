# Template Management (Business Layer)

Business-layer components for managing notification templates: validation and
CRUD orchestration over the template repository and cache.

> **Historical note**: earlier revisions of this document described a mounted
> REST API (`/api/v2/templates*`, 13 endpoints) with HTTP handlers in
> `cmd/server/handlers/`. Those handlers are not present in the current code
> base and the routes are not mounted in the active runtime
> (`internal/application/router.go`). The sections below describe only what
> exists today.

---

## Components

| Component | File | Responsibility |
|-----------|------|----------------|
| **Domain Models** | `internal/core/domain/template.go` | Templates, versions, filters |
| **Repository** | `internal/infrastructure/template/repository*.go` | CRUD, versions |
| **Cache** | `internal/infrastructure/template/cache.go` | L1 (memory) + L2 (Redis) caching |
| **Validator** | `internal/business/template/validator.go` | Syntax validation via `internal/notification/template` engine |
| **Manager** | `internal/business/template/manager.go` | Business logic orchestration |

---

## Usage

```go
import (
    "github.com/ipiton/AMP/internal/business/template"
    templateInfra "github.com/ipiton/AMP/internal/infrastructure/template"
    templateEngine "github.com/ipiton/AMP/internal/notification/template"
)

// Repository
templateRepo, _ := templateInfra.NewTemplateRepository(pgPool, logger)

// Cache (L1 + L2)
templateCache, _ := templateInfra.NewTwoTierTemplateCache(redisCache, logger)

// Validator (uses the notification template engine)
engine, _ := templateEngine.NewNotificationTemplateEngine(templateEngine.DefaultTemplateEngineOptions())
validator := template.NewTemplateValidator(engine, logger)

// Manager
templateManager := template.NewTemplateManager(templateRepo, templateCache, validator, logger)
```

---

## Database Schema

Defined in `migrations/20251125000001_create_templates_tables.sql`.

### `templates` table

```sql
CREATE TABLE templates (
    id UUID PRIMARY KEY,
    name VARCHAR(64) UNIQUE NOT NULL,
    type VARCHAR(20) NOT NULL,
    content TEXT NOT NULL,
    description TEXT,
    metadata JSONB DEFAULT '{}',
    version INTEGER NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_by VARCHAR(255),
    updated_by VARCHAR(255),
    deleted_at TIMESTAMPTZ
);
```

Indexes: B-tree, GIN and full-text search indexes (see the migration file).

### `template_versions` table

```sql
CREATE TABLE template_versions (
    id UUID PRIMARY KEY,
    template_id UUID NOT NULL REFERENCES templates(id) ON DELETE CASCADE,
    version INTEGER NOT NULL,
    content TEXT NOT NULL,
    description TEXT,
    metadata JSONB DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_by VARCHAR(255),
    change_summary TEXT,
    UNIQUE(template_id, version)
);
```

---

## Validation Rules

- Name format: `^[a-z0-9_]{3,64}$` (lowercase alphanumeric + underscore)
- Content size limit enforced by the validator
- Template syntax validated via the notification template engine

---

## Testing

```bash
# Unit tests
go test ./internal/business/template/... -v -cover

# Repository/cache benchmarks
go test ./internal/infrastructure/template/... -bench=. -benchmem
```
