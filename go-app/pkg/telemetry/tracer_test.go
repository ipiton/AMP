package telemetry

import (
	"context"
	"log/slog"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/attribute"
)

func TestNewTracer_Disabled(t *testing.T) {
	config := &TracerConfig{
		ServiceName:    "test-service",
		ServiceVersion: "1.0.0",
		Environment:    "test",
		Enabled:        false,
		Logger:         slog.Default(),
	}

	tracer, err := NewTracer(config)
	assert.NoError(t, err)
	assert.NotNil(t, tracer)
	assert.Nil(t, tracer.provider, "Provider should be nil when disabled")
}

func TestNewTracer_NilConfig(t *testing.T) {
	tracer, err := NewTracer(nil)
	assert.Error(t, err)
	assert.Nil(t, tracer)
	assert.Contains(t, err.Error(), "config is nil")
}

func TestTracer_StartSpan_Disabled(t *testing.T) {
	config := &TracerConfig{
		ServiceName:    "test-service",
		ServiceVersion: "1.0.0",
		Environment:    "test",
		Enabled:        false,
		Logger:         slog.Default(),
	}

	tracer, err := NewTracer(config)
	assert.NoError(t, err)

	ctx := context.Background()
	ctx, span := tracer.StartSpan(ctx, "test-operation")
	defer span.End()

	// Should return no-op span
	assert.NotNil(t, span)
	assert.False(t, span.SpanContext().IsValid(), "Span should be no-op when tracing is disabled")
}

func TestTracer_AddEvent(t *testing.T) {
	config := &TracerConfig{
		ServiceName:    "test-service",
		ServiceVersion: "1.0.0",
		Environment:    "test",
		Enabled:        false, // Disabled for testing (no external dependencies)
		Logger:         slog.Default(),
	}

	tracer, err := NewTracer(config)
	assert.NoError(t, err)

	ctx := context.Background()

	// Should not panic even when disabled
	tracer.AddEvent(ctx, "test-event", attribute.String("key", "value"))
}

func TestTracer_SetAttributes(t *testing.T) {
	config := &TracerConfig{
		ServiceName:    "test-service",
		ServiceVersion: "1.0.0",
		Environment:    "test",
		Enabled:        false,
		Logger:         slog.Default(),
	}

	tracer, err := NewTracer(config)
	assert.NoError(t, err)

	ctx := context.Background()

	// Should not panic even when disabled
	tracer.SetAttributes(ctx, attribute.String("key", "value"))
}

func TestTracer_RecordError(t *testing.T) {
	config := &TracerConfig{
		ServiceName:    "test-service",
		ServiceVersion: "1.0.0",
		Environment:    "test",
		Enabled:        false,
		Logger:         slog.Default(),
	}

	tracer, err := NewTracer(config)
	assert.NoError(t, err)

	ctx := context.Background()

	// Should not panic even when disabled
	tracer.RecordError(ctx, assert.AnError)
}

func TestSpanHelpers(t *testing.T) {
	// Test helper functions don't panic
	opt1 := SpanWithTarget("slack-prod")
	assert.NotNil(t, opt1)

	opt2 := SpanWithAlert("fp123", "critical")
	assert.NotNil(t, opt2)

	opt3 := SpanWithHTTP("POST", "/webhook", 200)
	assert.NotNil(t, opt3)

	opt4 := SpanWithDatabase("SELECT", "alerts")
	assert.NotNil(t, opt4)
}

func TestTracer_Shutdown_NoProvider(t *testing.T) {
	config := &TracerConfig{
		ServiceName:    "test-service",
		ServiceVersion: "1.0.0",
		Environment:    "test",
		Enabled:        false,
		Logger:         slog.Default(),
	}

	tracer, err := NewTracer(config)
	assert.NoError(t, err)

	// Should not error when provider is nil
	err = tracer.Shutdown(context.Background())
	assert.NoError(t, err)
}

func TestTracerConfig_SamplingRatio(t *testing.T) {
	tests := []struct {
		name          string
		samplingRatio float64
		wantSample    string
	}{
		{"always", 1.0, "AlwaysSample"},
		{"never", 0.0, "NeverSample"},
		{"half", 0.5, "TraceIDRatioBased"},
		{"ten_percent", 0.1, "TraceIDRatioBased"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &TracerConfig{
				ServiceName:    "test-service",
				ServiceVersion: "1.0.0",
				Environment:    "test",
				Enabled:        false, // Disabled to avoid external dependencies
				SamplingRatio:  tt.samplingRatio,
				Logger:         slog.Default(),
			}

			tracer, err := NewTracer(config)
			assert.NoError(t, err)
			assert.NotNil(t, tracer)
		})
	}
}

func TestResponseWriter(t *testing.T) {
	// Test responseWriter captures status code
	rw := &responseWriter{
		ResponseWriter: &mockResponseWriter{},
		statusCode:     http.StatusOK,
	}

	rw.WriteHeader(http.StatusNotFound)
	assert.Equal(t, http.StatusNotFound, rw.statusCode)

	n, err := rw.Write([]byte("test"))
	assert.NoError(t, err)
	assert.Equal(t, 4, n)
	assert.Equal(t, 4, rw.bytesWritten)
}

// mockResponseWriter implements http.ResponseWriter for testing
type mockResponseWriter struct {
	headers http.Header
}

func (m *mockResponseWriter) Header() http.Header {
	if m.headers == nil {
		m.headers = make(http.Header)
	}
	return m.headers
}

func (m *mockResponseWriter) Write([]byte) (int, error) {
	return 0, nil
}

func (m *mockResponseWriter) WriteHeader(statusCode int) {}
