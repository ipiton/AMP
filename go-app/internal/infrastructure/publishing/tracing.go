package publishing

import (
	"context"
	"log/slog"
	"time"

	"github.com/ipiton/AMP/internal/core"
)

// NOTE: This is a simplified tracing interface for Phase 6 demonstration.
// In production, integrate with OpenTelemetry (go.opentelemetry.io/otel).
//
// To use OpenTelemetry:
//   1. go get go.opentelemetry.io/otel
//   2. go get go.opentelemetry.io/otel/trace
//   3. go get go.opentelemetry.io/otel/exporters/jaeger (or other exporter)
//   4. Replace SimpleTracer with otel.Tracer

// Tracer is a simplified tracing interface (compatible with OpenTelemetry)
type Tracer interface {
	Start(ctx context.Context, name string, opts ...SpanOption) (context.Context, Span)
}

// Span is a simplified span interface (compatible with OpenTelemetry)
type Span interface {
	End()
	SetAttributes(attrs ...Attribute)
	SetStatus(code StatusCode, description string)
	RecordError(err error)
	AddEvent(name string, attrs ...Attribute)
	IsRecording() bool
}

// SpanOption configures span creation
type SpanOption func(*spanConfig)

type spanConfig struct {
	spanKind   SpanKind
	attributes []Attribute
}

// SpanKind defines span type
type SpanKind int

const (
	SpanKindInternal SpanKind = iota
	SpanKindServer
	SpanKindClient
)

// StatusCode defines span status
type StatusCode int

const (
	StatusCodeOk StatusCode = iota
	StatusCodeError
)

// Attribute is a key-value pair for span metadata
type Attribute struct {
	Key   string
	Value any
}

// Attribute builders (OpenTelemetry-compatible naming)
func String(key, value string) Attribute {
	return Attribute{Key: key, Value: value}
}

func Int(key string, value int) Attribute {
	return Attribute{Key: key, Value: value}
}

func Float64(key string, value float64) Attribute {
	return Attribute{Key: key, Value: value}
}

func Bool(key string, value bool) Attribute {
	return Attribute{Key: key, Value: value}
}

// WithSpanKind sets span kind
func WithSpanKind(kind SpanKind) SpanOption {
	return func(cfg *spanConfig) {
		cfg.spanKind = kind
	}
}

// WithAttributes adds initial attributes
func WithAttributes(attrs ...Attribute) SpanOption {
	return func(cfg *spanConfig) {
		cfg.attributes = append(cfg.attributes, attrs...)
	}
}

// SimpleTracer is a no-op tracer for testing/development
// Replace with OpenTelemetry tracer in production
type SimpleTracer struct {
	logger *slog.Logger
}

// NewSimpleTracer creates a no-op tracer
func NewSimpleTracer(logger *slog.Logger) Tracer {
	if logger == nil {
		logger = slog.Default()
	}
	return &SimpleTracer{logger: logger}
}

func (t *SimpleTracer) Start(ctx context.Context, name string, opts ...SpanOption) (context.Context, Span) {
	cfg := &spanConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	span := &simpleSpan{
		name:       name,
		attributes: cfg.attributes,
		logger:     t.logger,
		startTime:  time.Now(),
	}

	// Log span start (optional)
	// t.logger.Debug("Span started", "name", name)

	return ctx, span
}

type simpleSpan struct {
	name       string
	attributes []Attribute
	logger     *slog.Logger
	startTime  time.Time
}

func (s *simpleSpan) End() {
	duration := time.Since(s.startTime)
	// Log span end (optional)
	_ = duration
	// s.logger.Debug("Span ended", "name", s.name, "duration", duration)
}

func (s *simpleSpan) SetAttributes(attrs ...Attribute) {
	s.attributes = append(s.attributes, attrs...)
}

func (s *simpleSpan) SetStatus(code StatusCode, description string) {
	if code == StatusCodeError {
		s.logger.Warn("Span error", "name", s.name, "description", description)
	}
}

func (s *simpleSpan) RecordError(err error) {
	s.logger.Error("Span error", "name", s.name, "error", err)
}

func (s *simpleSpan) AddEvent(name string, attrs ...Attribute) {
	// Log event (optional)
	// s.logger.Debug("Span event", "span", s.name, "event", name)
}

func (s *simpleSpan) IsRecording() bool {
	return true
}

// SpanFromContext extracts span from context (no-op in simple tracer)
func SpanFromContext(ctx context.Context) Span {
	// In production, use trace.SpanFromContext(ctx)
	return &simpleSpan{logger: slog.Default()}
}

// TracingMiddleware creates middleware that adds distributed tracing
//
// Features:
//   - Span per format request
//   - Span attributes (format, alert_name, status, classification)
//   - Span events (cache_hit, cache_miss, validation_error)
//   - Error recording
//
// Example span tree:
//   FormatAlert (root)
//     ├─ Validation
//     ├─ CacheCheck
//     └─ Format
//
// Returns:
//   AlertFormatter: Wrapped formatter with tracing
func TracingMiddleware(next AlertFormatter, tracer Tracer) AlertFormatter {
	return &tracingFormatterMiddleware{
		next:   next,
		tracer: tracer,
	}
}

type tracingFormatterMiddleware struct {
	next   AlertFormatter
	tracer Tracer
}

func (m *tracingFormatterMiddleware) FormatAlert(ctx context.Context, enrichedAlert *core.EnrichedAlert, format core.PublishingFormat) (map[string]any, error) {
	// Start span
	ctx, span := m.tracer.Start(ctx, "FormatAlert",
		WithSpanKind(SpanKindInternal),
		WithAttributes(
			String("format", string(format)),
		),
	)
	defer span.End()

	// Add alert attributes (if available)
	if enrichedAlert != nil && enrichedAlert.Alert != nil {
		span.SetAttributes(
			String("alert.name", enrichedAlert.Alert.AlertName),
			String("alert.fingerprint", enrichedAlert.Alert.Fingerprint),
			String("alert.status", string(enrichedAlert.Alert.Status)),
		)

		// Add classification attributes (if present)
		if enrichedAlert.Classification != nil {
			span.SetAttributes(
				String("classification.severity", string(enrichedAlert.Classification.Severity)),
				Float64("classification.confidence", enrichedAlert.Classification.Confidence),
			)
		}

		// Add label attributes (sample)
		if enrichedAlert.Alert.Labels != nil {
			if severity, ok := enrichedAlert.Alert.Labels["severity"]; ok {
				span.SetAttributes(String("alert.label.severity", severity))
			}
			if namespace, ok := enrichedAlert.Alert.Labels["namespace"]; ok {
				span.SetAttributes(String("alert.label.namespace", namespace))
			}
		}
	}

	// Call next formatter
	result, err := m.next.FormatAlert(ctx, enrichedAlert, format)

	// Record error or success
	if err != nil {
		span.RecordError(err)
		span.SetStatus(StatusCodeError, err.Error())

		// Add error type attribute
		errorType := classifyError(err)
		span.SetAttributes(String("error.type", errorType))

		// Add validation error details
		if validationErr, ok := err.(*ValidationError); ok {
			span.SetAttributes(
				String("validation.field", validationErr.Field),
				String("validation.message", validationErr.Message),
			)
			if validationErr.Value != "" {
				span.SetAttributes(String("validation.value", validationErr.Value))
			}
		}
	} else {
		span.SetStatus(StatusCodeOk, "")

		// Add result size attribute
		if result != nil {
			size := estimateJSONSize(result)
			span.SetAttributes(Int("result.size_bytes", size))
		}
	}

	return result, err
}

// TracingCacheMiddleware wraps CachingMiddleware with tracing
//
// Adds:
//   - cache_hit/cache_miss events
//   - cache_key attribute
//
// Returns:
//   FormatterMiddleware: Tracing-aware caching middleware
func TracingCacheMiddleware(tracer Tracer, cache FormatterCache, ttl time.Duration, logger *slog.Logger) FormatterMiddleware {
	return func(next formatFunc) formatFunc {
		return func(enrichedAlert *core.EnrichedAlert) (map[string]any, error) {
			// Generate cache key using fingerprint as key
			cacheKey := enrichedAlert.Alert.Fingerprint

			// Check cache
			if cached, found := cache.Get(cacheKey); found {
				logger.Debug("Cache hit", "key", cacheKey)
				return cached, nil
			}

			// Cache miss - format
			result, err := next(enrichedAlert)
			if err == nil {
				cache.Set(cacheKey, result, ttl)
			}
			return result, err
		}
	}
}

// TracingValidationMiddleware wraps ValidationMiddleware with tracing
func TracingValidationMiddleware(tracer Tracer, validator AlertValidator) FormatterMiddleware {
	return func(next formatFunc) formatFunc {
		return func(enrichedAlert *core.EnrichedAlert) (map[string]any, error) {
			// Validate
			if errs := validator.Validate(enrichedAlert); len(errs) > 0 {
				// Return first validation error
				return nil, &errs[0]
			}

			// Format
			return next(enrichedAlert)
		}
	}
}
