// Package telemetry provides OpenTelemetry tracing integration.
package telemetry

import (
	"context"
	"fmt"
	"log/slog"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
	"go.opentelemetry.io/otel/trace"
)

// TracerConfig holds configuration for OpenTelemetry tracer.
type TracerConfig struct {
	// ServiceName is the name of the service
	ServiceName string

	// ServiceVersion is the version of the service
	ServiceVersion string

	// Environment is the deployment environment (dev, staging, prod)
	Environment string

	// Enabled controls whether tracing is enabled
	Enabled bool

	// Endpoint is the OTLP collector endpoint (e.g., "localhost:4317")
	Endpoint string

	// SamplingRatio is the sampling ratio (0.0 to 1.0)
	// 1.0 = trace all requests, 0.1 = trace 10% of requests
	SamplingRatio float64

	// Logger for error reporting
	Logger *slog.Logger
}

// Tracer manages OpenTelemetry tracing.
type Tracer struct {
	config   *TracerConfig
	provider *sdktrace.TracerProvider
	logger   *slog.Logger
}

// NewTracer creates a new OpenTelemetry tracer.
//
// Parameters:
//   - config: Tracer configuration
//
// Returns:
//   - *Tracer: Configured tracer
//   - error: Configuration or initialization error
func NewTracer(config *TracerConfig) (*Tracer, error) {
	if config == nil {
		return nil, fmt.Errorf("config is nil")
	}

	if !config.Enabled {
		// Return no-op tracer
		return &Tracer{
			config: config,
			logger: config.Logger,
		}, nil
	}

	if config.Logger == nil {
		config.Logger = slog.Default()
	}

	// Create resource with service information
	res, err := resource.New(
		context.Background(),
		resource.WithAttributes(
			semconv.ServiceName(config.ServiceName),
			semconv.ServiceVersion(config.ServiceVersion),
			attribute.String("environment", config.Environment),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource: %w", err)
	}

	// Create OTLP exporter
	exporter, err := otlptrace.New(
		context.Background(),
		otlptracegrpc.NewClient(
			otlptracegrpc.WithEndpoint(config.Endpoint),
			otlptracegrpc.WithInsecure(), // TODO: Add TLS support
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create OTLP exporter: %w", err)
	}

	// Create trace provider with sampling
	var sampler sdktrace.Sampler
	if config.SamplingRatio >= 1.0 {
		sampler = sdktrace.AlwaysSample()
	} else if config.SamplingRatio <= 0.0 {
		sampler = sdktrace.NeverSample()
	} else {
		sampler = sdktrace.TraceIDRatioBased(config.SamplingRatio)
	}

	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sampler),
	)

	// Set global tracer provider
	otel.SetTracerProvider(provider)

	// Set global propagator for distributed tracing
	otel.SetTextMapPropagator(
		propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{},
			propagation.Baggage{},
		),
	)

	config.Logger.Info("OpenTelemetry tracer initialized",
		"service", config.ServiceName,
		"version", config.ServiceVersion,
		"endpoint", config.Endpoint,
		"sampling_ratio", config.SamplingRatio,
	)

	return &Tracer{
		config:   config,
		provider: provider,
		logger:   config.Logger,
	}, nil
}

// Shutdown gracefully shuts down the tracer.
func (t *Tracer) Shutdown(ctx context.Context) error {
	if t.provider == nil {
		return nil
	}

	if err := t.provider.Shutdown(ctx); err != nil {
		return fmt.Errorf("failed to shutdown tracer: %w", err)
	}

	t.logger.Info("OpenTelemetry tracer shut down")
	return nil
}

// StartSpan starts a new span with the given name and options.
//
// Usage:
//
//	ctx, span := tracer.StartSpan(ctx, "operation_name")
//	defer span.End()
func (t *Tracer) StartSpan(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	if !t.config.Enabled || t.provider == nil {
		// Return no-op span
		return ctx, trace.SpanFromContext(ctx)
	}

	tracer := otel.Tracer(t.config.ServiceName)
	return tracer.Start(ctx, name, opts...)
}

// AddEvent adds an event to the current span.
func (t *Tracer) AddEvent(ctx context.Context, name string, attrs ...attribute.KeyValue) {
	span := trace.SpanFromContext(ctx)
	span.AddEvent(name, trace.WithAttributes(attrs...))
}

// SetAttributes sets attributes on the current span.
func (t *Tracer) SetAttributes(ctx context.Context, attrs ...attribute.KeyValue) {
	span := trace.SpanFromContext(ctx)
	span.SetAttributes(attrs...)
}

// RecordError records an error on the current span.
func (t *Tracer) RecordError(ctx context.Context, err error, attrs ...attribute.KeyValue) {
	span := trace.SpanFromContext(ctx)
	span.RecordError(err, trace.WithAttributes(attrs...))
}

// Helper functions for common span attributes

// SpanWithTarget adds target information to span.
func SpanWithTarget(target string) trace.SpanStartOption {
	return trace.WithAttributes(attribute.String("target", target))
}

// SpanWithAlert adds alert information to span.
func SpanWithAlert(fingerprint, severity string) trace.SpanStartOption {
	return trace.WithAttributes(
		attribute.String("alert.fingerprint", fingerprint),
		attribute.String("alert.severity", severity),
	)
}

// SpanWithHTTP adds HTTP request information to span.
func SpanWithHTTP(method, path string, statusCode int) trace.SpanStartOption {
	return trace.WithAttributes(
		attribute.String("http.method", method),
		attribute.String("http.path", path),
		attribute.Int("http.status_code", statusCode),
	)
}

// SpanWithDatabase adds database operation information to span.
func SpanWithDatabase(operation, table string) trace.SpanStartOption {
	return trace.WithAttributes(
		attribute.String("db.operation", operation),
		attribute.String("db.table", table),
	)
}
