# Template Engine — Dashboard UI

**Package**: `internal/ui`
**Purpose**: Server-side HTML template engine for the AMP dashboard

> **Note**: this package is not the active owner of the `/dashboard/*` pages.
> The active dashboard is served from `cmd/server` (see ADR-005 in
> `docs/06-planning/DECISIONS.md`).

---

## Overview

Template engine built on Go's `html/template` with:
- **Layouts & Partials**: Modular template composition
- **Custom Functions**: Time formatting, CSS helpers, utilities
- **Hot Reload**: Development mode with live template updates
- **Template Caching**: Cached mode without per-request disk I/O
- **Observability**: Prometheus metrics via `pkg/metrics/v2` `HTTPMetrics`
- **XSS Protection**: Auto-escaping via html/template
- **Thread-Safe**: Concurrent rendering support

---

## Quick Start

### 1. Initialize Engine

```go
import "github.com/ipiton/AMP/internal/ui"

// Production mode (cached templates)
opts := ui.DefaultTemplateOptions()
engine, err := ui.NewTemplateEngine(opts)

// Development mode (hot reload)
opts := ui.DefaultTemplateOptions()
opts.HotReload = true
opts.Cache = false
engine, err := ui.NewTemplateEngine(opts)
```

### 2. Render Template

```go
func HandleDashboard(w http.ResponseWriter, r *http.Request) {
    // Prepare page data
    pageData := ui.NewPageData("Dashboard")
    pageData.AddBreadcrumb("Home", "/")
    pageData.AddBreadcrumb("Dashboard", "/dashboard")
    pageData.Data = map[string]interface{}{
        "FiringAlerts": 42,
        "ResolvedAlerts": 128,
    }

    // Render with automatic error fallback
    engine.RenderWithFallback(w, "pages/dashboard", pageData)
}
```

---

## Architecture

### Components

1. **TemplateEngine** - Core engine (`template_engine.go`)
2. **Custom Functions** - Template helpers (`template_funcs.go`)
3. **Metrics** - Prometheus metrics via `pkg/metrics/v2` (`HTTPMetrics`)
4. **PageData** - Standard data structure (`page_data.go`)
5. **Template Files** - HTML templates loaded from `TemplateDir` (default `templates/`)

### Template Structure

Expected layout under `TemplateDir` (this package does not ship these files itself):

```
templates/
├── layouts/          # Master page layouts
│   ├── base.html     # Standard layout
│   └── minimal.html  # Minimal layout (modals)
├── pages/            # Page templates
│   ├── dashboard.html
│   ├── alerts.html
│   └── silences.html
├── partials/         # Reusable components
│   ├── header.html
│   ├── footer.html
│   ├── sidebar.html
│   ├── breadcrumbs.html
│   └── flash.html
└── errors/           # Error pages
    ├── 404.html
    └── 500.html
```

---

## Custom Functions

### Time Functions

```html
<!-- formatTime: "2025-11-17 14:30:00" -->
{{ formatTime .CreatedAt }}

<!-- timeAgo: "2 hours ago", "3 days ago" -->
{{ timeAgo .UpdatedAt }}
```

### CSS Helpers

```html
<!-- severity: "badge-critical", "badge-warning", "badge-info" -->
<span class="{{ severity .Severity }}">{{ .Severity }}</span>

<!-- statusClass: "status-firing", "status-resolved" -->
<div class="{{ statusClass .Status }}">{{ .Status }}</div>
```

### Formatting

```html
<!-- truncate: limit string length with "..." -->
{{ truncate .Description 100 }}

<!-- jsonPretty: pretty-print JSON -->
<pre>{{ jsonPretty .Labels }}</pre>

<!-- upper/lower: case conversion -->
{{ upper "hello" }} → HELLO
{{ lower "WORLD" }} → world
```

### Utilities

```html
<!-- defaultVal: fallback for nil/empty -->
{{ defaultVal "N/A" .OptionalField }}

<!-- join: join slice with separator -->
{{ join .Tags ", " }}

<!-- contains: check if slice contains item -->
{{ if contains .Roles "admin" }}Admin Panel{{ end }}
```

### Math Functions

```html
<!-- add/sub/mul/div -->
Page {{ add .PageNum 1 }} of {{ .TotalPages }}
{{ sub .Total .Used }} items remaining
```

---

## PageData Structure

```go
type PageData struct {
    Title       string          // Page title
    Breadcrumbs []Breadcrumb    // Navigation breadcrumbs
    Flash       *FlashMessage   // Temporary message
    User        *User           // Current user
    Data        interface{}     // Page-specific data
}
```

### Example

```go
pageData := ui.NewPageData("Alert Details")
pageData.AddBreadcrumb("Home", "/")
pageData.AddBreadcrumb("Alerts", "/alerts")
pageData.AddBreadcrumb("Details", "")
pageData.SetFlash("success", "Alert resolved successfully")
pageData.SetUser(&ui.User{
    Name: "Admin",
    Roles: []string{"admin"},
})
pageData.Data = alertData
```

---

## Template Development

### Creating a New Page

**1. Create template file**: `templates/pages/mypage.html`

```html
{{ define "title" }}My Page - AMP{{ end }}

{{ define "content" }}
<div class="my-page">
    <h1>{{ .Title }}</h1>
    <p>Page content: {{ .Data.Message }}</p>
</div>
{{ end }}
```

**2. Create handler**:

```go
func HandleMyPage(w http.ResponseWriter, r *http.Request) {
    pageData := ui.NewPageData("My Page")
    pageData.Data = map[string]interface{}{
        "Message": "Hello, World!",
    }
    engine.RenderWithFallback(w, "pages/mypage", pageData)
}
```

**3. Register route**:

```go
http.HandleFunc("/mypage", HandleMyPage)
```

### Hot Reload Development

```go
// Enable hot reload for development
opts := ui.DefaultTemplateOptions()
opts.HotReload = true  // Reload templates on each request
opts.Cache = false     // Disable caching
engine, _ := ui.NewTemplateEngine(opts)
```

Edit templates → Refresh browser → See changes immediately!

---

## Performance

### Cached Mode (Cache=true)

- Templates loaded once at startup
- No disk I/O per request

### Development Mode (HotReload=true)

- Templates re-read from disk on each request
- Use case: local development only

Benchmarks live in `template_bench_test.go`; run them locally for current numbers.

---

## Observability

### Prometheus Metrics

Template metrics are part of `pkg/metrics/v2` `HTTPMetrics` (namespace `alert_history`, subsystem `http`).

**1. Template Renders** (Counter)
```promql
# Total renders by template and status
alert_history_http_template_render_total{template="pages/dashboard", status="success"}
```

**2. Render Duration** (Histogram)
```promql
# p95 render duration
histogram_quantile(0.95, rate(alert_history_http_template_render_duration_seconds_bucket[5m]))
```

**3. Cache Hits** (Counter)
```promql
# Cache hit rate
rate(alert_history_http_template_cache_hits_total[5m])
```

---

## Error Handling

### Graceful Degradation

```go
// RenderWithFallback automatically handles errors
engine.RenderWithFallback(w, "pages/dashboard", pageData)
```

If `pages/dashboard` fails:
1. Error logged via `slog`
2. HTTP 500 status set
3. Renders `errors/500.html` with error details

### Manual Error Handling

```go
err := engine.Render(w, "pages/dashboard", pageData)
if err != nil {
    if errors.Is(err, ui.ErrTemplateNotFound) {
        http.Error(w, "Template not found", http.StatusNotFound)
        return
    }
    http.Error(w, "Render failed", http.StatusInternalServerError)
}
```

---

## Security

### XSS Protection

`html/template` automatically escapes HTML:

```html
<!-- Safe: auto-escaped -->
<div>{{ .UserInput }}</div>

<!-- Unsafe: raw HTML (use carefully!) -->
<div>{{ .TrustedHTML | html }}</div>
```

### Content Security Policy

```go
func RenderSecure(w http.ResponseWriter, engine *ui.TemplateEngine) {
    w.Header().Set("Content-Security-Policy", "default-src 'self'")
    engine.RenderWithFallback(w, "pages/dashboard", pageData)
}
```

---

## Integration Examples

### With Middleware

```go
func AuthMiddleware(engine *ui.TemplateEngine) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        user := getAuthenticatedUser(r)
        if user == nil {
            pageData := ui.NewPageData("Unauthorized")
            engine.RenderWithFallback(w, "errors/401", pageData)
            return
        }
        // Continue to handler
    }
}
```

### With Flash Messages

```go
func HandleSilenceCreate(w http.ResponseWriter, r *http.Request) {
    if err := createSilence(r); err != nil {
        pageData := ui.NewPageData("Silences")
        pageData.SetFlash("error", "Failed to create silence")
        engine.RenderWithFallback(w, "pages/silences", pageData)
        return
    }

    pageData := ui.NewPageData("Silences")
    pageData.SetFlash("success", "Silence created successfully")
    engine.RenderWithFallback(w, "pages/silences", pageData)
}
```

---

## Troubleshooting

### Template Not Found

**Error**: `template not found: pages/dashboard`

**Solutions**:
1. Check template file exists: `templates/pages/dashboard.html`
2. Verify `TemplateDir` option: `opts.TemplateDir = "templates/"`
3. Check file extension: Must be `.html`

### Render Error

**Error**: `template render failed: undefined variable`

**Solutions**:
1. Check `{{ .Data.FieldName }}` exists in data
2. Use `{{ defaultVal "N/A" .Data.FieldName }}`
3. Check template syntax

### Hot Reload Not Working

**Problem**: Template changes not reflected

**Solutions**:
1. Verify `opts.HotReload = true`
2. Check `opts.Cache = false`
3. Restart development server

### Performance Issues

**Problem**: Slow rendering (>100ms)

**Solutions**:
1. Enable caching: `opts.Cache = true`
2. Disable hot reload: `opts.HotReload = false`
3. Profile with Prometheus metrics
4. Simplify template logic

---

## API Reference

### TemplateEngine

```go
// NewTemplateEngine creates a new engine
func NewTemplateEngine(opts TemplateOptions) (*TemplateEngine, error)

// LoadTemplates loads all templates from disk
func (e *TemplateEngine) LoadTemplates() error

// Render renders a template to http.ResponseWriter
func (e *TemplateEngine) Render(
    w http.ResponseWriter,
    templateName string,
    data interface{},
) error

// RenderString renders a template to string
func (e *TemplateEngine) RenderString(
    templateName string,
    data interface{},
) (string, error)

// RenderWithFallback renders with automatic error handling
func (e *TemplateEngine) RenderWithFallback(
    w http.ResponseWriter,
    templateName string,
    data interface{},
)

// GetMetrics returns the metrics instance
func (e *TemplateEngine) GetMetrics() *v2.HTTPMetrics

// SetMetrics sets the metrics instance
func (e *TemplateEngine) SetMetrics(metrics *v2.HTTPMetrics)
```

### PageData

```go
// NewPageData creates a new PageData
func NewPageData(title string) *PageData

// AddBreadcrumb adds a breadcrumb
func (p *PageData) AddBreadcrumb(name, url string)

// SetFlash sets a flash message
func (p *PageData) SetFlash(msgType, message string)

// SetUser sets the current user
func (p *PageData) SetUser(user *User)
```

---

## Best Practices

### 1. Use PageData Consistently

```go
// ✅ Good: Standard structure
pageData := ui.NewPageData("Dashboard")
pageData.Data = dashboardData
engine.RenderWithFallback(w, "pages/dashboard", pageData)

// ❌ Bad: Raw map
engine.Render(w, "pages/dashboard", map[string]interface{}{...})
```

### 2. Always Use RenderWithFallback

```go
// ✅ Good: Automatic error handling
engine.RenderWithFallback(w, "pages/dashboard", pageData)

// ❌ Bad: Manual error handling in every handler
if err := engine.Render(...); err != nil { ... }
```

### 3. Cache Templates in Production

```go
// ✅ Good: Production config
opts := ui.DefaultTemplateOptions()
opts.HotReload = false  // No reload
opts.Cache = true       // Cache enabled

// ❌ Bad: Development config in production
opts.HotReload = true  // Performance hit!
```

### 4. Use Custom Functions

```go
// ✅ Good: Use built-in functions
{{ timeAgo .CreatedAt }}

// ❌ Bad: Complex logic in template
{{ if gt (sub (now) .CreatedAt) 3600 }}...{{ end }}
```

---

## Files

- `template_engine.go` - Core engine
- `template_funcs.go` - Custom template functions
- `template_errors.go` - Error types
- `page_data.go` - Data structures
- `classification_display.go`, `classification_enricher.go` - Classification display helpers
- `testdata/` - Test fixtures

---

## References

- [Go html/template](https://pkg.go.dev/html/template)
- [Template Actions](https://pkg.go.dev/text/template#hdr-Actions)
- [XSS Prevention](https://golang.org/pkg/html/template/#hdr-Security_Model)
