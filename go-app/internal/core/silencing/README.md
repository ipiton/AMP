# Silence Matcher Engine

Alert matching engine for the AMP silencing system. Supports all 4 Alertmanager matcher operators (=, !=, =~, !~).

## Quick Start

```go
import "github.com/ipiton/AMP/internal/core/silencing"

// Create matcher
matcher := silencing.NewSilenceMatcher()

// Define alert
alert := silencing.Alert{
    Labels: map[string]string{
        "alertname": "HighCPU",
        "job":       "api-server",
        "severity":  "critical",
    },
}

// Define silence
silence := &silencing.Silence{
    ID:        "abc123",
    CreatedBy: "ops@example.com",
    Comment:   "Maintenance window",
    Matchers: []silencing.Matcher{
        {Name: "alertname", Value: "HighCPU", Type: silencing.MatcherTypeEqual},
        {Name: "job", Value: "api-server", Type: silencing.MatcherTypeEqual},
    },
}

// Check if alert matches silence
matched, err := matcher.Matches(context.Background(), alert, silence)
if matched {
    fmt.Println("Alert is silenced")
}
```

## Features

- All 4 operators: `=`, `!=`, `=~`, `!~`
- Regex compilation caching
- Context cancellation support
- Thread-safe concurrent access
- Early exit optimization (AND logic)

## Performance

Benchmarks live in `matcher_bench_test.go`; run `go test -bench=. ./internal/core/silencing/...` for current numbers on your hardware.

## Operators

### Equal (=)
Label must exist AND equal value.
```go
{Name: "job", Value: "api-server", Type: MatcherTypeEqual}
```

### Not Equal (!=)
Label missing OR not equal value.
```go
{Name: "env", Value: "prod", Type: MatcherTypeNotEqual}
```

### Regex (=~)
Label must exist AND match regex pattern.
```go
{Name: "severity", Value: "(critical|warning)", Type: MatcherTypeRegex}
```

### Not Regex (!~)
Label missing OR not match regex pattern.
```go
{Name: "instance", Value: ".*-dev-.*", Type: MatcherTypeNotRegex}
```

## Examples

### Example 1: Basic Matching
```go
matcher := silencing.NewSilenceMatcher()

alert := silencing.Alert{
    Labels: map[string]string{
        "alertname": "DiskFull",
        "severity":  "warning",
    },
}

silence := &silencing.Silence{
    Matchers: []silencing.Matcher{
        {Name: "alertname", Value: "DiskFull", Type: "="},
        {Name: "severity", Value: "(critical|warning)", Type: "=~"},
    },
}

matched, _ := matcher.Matches(ctx, alert, silence)
// matched = true (both matchers pass)
```

### Example 2: Multiple Silences
```go
silences := []*silencing.Silence{
    {ID: "s1", Matchers: []silencing.Matcher{
        {Name: "job", Value: "api", Type: "="},
    }},
    {ID: "s2", Matchers: []silencing.Matcher{
        {Name: "severity", Value: "critical", Type: "="},
    }},
}

matchedIDs, _ := matcher.MatchesAny(ctx, alert, silences)
// matchedIDs = ["s1", "s2"] if alert matches both
```

### Example 3: Mixed Operators
```go
silence := &silencing.Silence{
    Matchers: []silencing.Matcher{
        {Name: "alertname", Value: "HighCPU", Type: "="},           // Must equal
        {Name: "env", Value: "dev", Type: "!="},                    // Must NOT be dev
        {Name: "severity", Value: "(critical|warning)", Type: "=~"}, // Must match pattern
        {Name: "instance", Value: ".*-test-.*", Type: "!~"},        // Must NOT match pattern
    },
}
```

### Example 4: Context Cancellation
```go
ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
defer cancel()

matchedIDs, err := matcher.MatchesAny(ctx, alert, largeSilenceList)
if err == silencing.ErrContextCancelled {
    fmt.Println("Matching cancelled, partial results:", matchedIDs)
}
```

### Example 5: Error Handling
```go
matched, err := matcher.Matches(ctx, alert, silence)
switch {
case errors.Is(err, silencing.ErrInvalidAlert):
    fmt.Println("Alert labels are nil")
case errors.Is(err, silencing.ErrInvalidSilence):
    fmt.Println("Silence is nil or has no matchers")
case errors.Is(err, silencing.ErrRegexCompilationFailed):
    fmt.Println("Invalid regex pattern:", err)
case errors.Is(err, silencing.ErrContextCancelled):
    fmt.Println("Operation cancelled")
case err != nil:
    fmt.Println("Unknown error:", err)
default:
    fmt.Println("Matched:", matched)
}
```

## Architecture

```
┌─────────────────┐
│ SilenceMatcher  │ ← Interface
└────────┬────────┘
         │
         ▼
┌─────────────────────┐
│ DefaultMatcher      │
│  - Regex Cache      │ ← Implementation
│  - Early Exit       │
│  - Context Support  │
└────────┬────────────┘
         │
         ▼
┌─────────────────────┐
│   RegexCache        │
│  - LRU Eviction     │ ← Performance
│  - Thread-Safe      │
│  - 1000 max size    │
└─────────────────────┘
```

## Testing

```bash
# Run tests
go test ./internal/core/silencing/...

# Run with coverage
go test -cover ./internal/core/silencing/...

# Run benchmarks
go test -bench=. ./internal/core/silencing/...

# Run specific benchmark
go test -bench=BenchmarkMatchesAny_100Silences ./internal/core/silencing/...
```

## Integration

```go
// Example: AlertProcessor integration
type AlertProcessor struct {
    matcher silencing.SilenceMatcher
}

func (p *AlertProcessor) Process(ctx context.Context, alert Alert) error {
    // Get active silences from storage
    silences, err := p.storage.GetActiveSilences(ctx)
    if err != nil {
        return err
    }

    // Check if alert is silenced
    matchedIDs, err := p.matcher.MatchesAny(ctx, alert, silences)
    if err != nil {
        return err
    }

    if len(matchedIDs) > 0 {
        log.Info("Alert silenced", "silenceIDs", matchedIDs)
        return nil // Suppress notification
    }

    // Continue with alert processing...
    return p.sendNotification(ctx, alert)
}
```

## API Reference

### SilenceMatcher Interface

```go
type SilenceMatcher interface {
    // Matches checks if an alert matches a silence rule
    Matches(ctx context.Context, alert Alert, silence *Silence) (bool, error)

    // MatchesAny checks if an alert matches ANY of the silences
    MatchesAny(ctx context.Context, alert Alert, silences []*Silence) ([]string, error)
}
```

### Alert Model

```go
type Alert struct {
    Labels      map[string]string  // Required: alert labels
    Annotations map[string]string  // Optional: not used for matching
}
```

### Errors

```go
var (
    ErrInvalidAlert             = errors.New("invalid alert: labels cannot be nil")
    ErrInvalidSilence           = errors.New("invalid silence: cannot be nil or have zero matchers")
    ErrRegexCompilationFailed   = errors.New("regex pattern compilation failed")
    ErrContextCancelled         = errors.New("matching cancelled: context done")
)
```

## Performance Tips

1. **Reuse matcher instances**: `NewSilenceMatcher()` initializes regex cache
2. **Pre-warm cache**: Call `Matches()` once with common patterns
3. **Use = operator** when possible (fastest)
4. **Early validation**: Validate silences before matching
5. **Context timeouts**: Set reasonable timeouts for large silence lists

## Dependencies

- Go 1.21+
- Standard library only (no external deps)

## License

Part of AMP. See `LICENSE` in the repository root.
