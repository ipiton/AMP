package telemetry

import (
	"net/http"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// HTTPMiddleware returns HTTP middleware that creates spans for incoming requests.
//
// Usage:
//
//	tracer := telemetry.NewTracer(config)
//	handler = telemetry.HTTPMiddleware(tracer)(handler)
func HTTPMiddleware(t *Tracer) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !t.config.Enabled {
				next.ServeHTTP(w, r)
				return
			}

			// Extract trace context from headers
			ctx := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))

			// Start span
			tracer := otel.Tracer(t.config.ServiceName)
			ctx, span := tracer.Start(ctx, r.Method+" "+r.URL.Path,
				trace.WithSpanKind(trace.SpanKindServer),
				trace.WithAttributes(
					attribute.String("http.method", r.Method),
					attribute.String("http.url", r.URL.String()),
					attribute.String("http.scheme", r.URL.Scheme),
					attribute.String("http.host", r.Host),
					attribute.String("http.user_agent", r.UserAgent()),
					attribute.String("http.remote_addr", r.RemoteAddr),
				),
			)
			defer span.End()

			// Wrap response writer to capture status code
			rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

			// Process request
			next.ServeHTTP(rw, r.WithContext(ctx))

			// Add response attributes
			span.SetAttributes(
				attribute.Int("http.status_code", rw.statusCode),
				attribute.Int("http.response_size", rw.bytesWritten),
			)

			// Mark span as error if status code >= 500
			if rw.statusCode >= 500 {
				span.SetAttributes(attribute.Bool("error", true))
			}
		})
	}
}

// responseWriter wraps http.ResponseWriter to capture status code and bytes written.
type responseWriter struct {
	http.ResponseWriter
	statusCode   int
	bytesWritten int
}

func (rw *responseWriter) WriteHeader(statusCode int) {
	rw.statusCode = statusCode
	rw.ResponseWriter.WriteHeader(statusCode)
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	n, err := rw.ResponseWriter.Write(b)
	rw.bytesWritten += n
	return n, err
}
