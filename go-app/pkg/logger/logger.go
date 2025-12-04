// Package logger provides structured logging utilities for the Alert History Service.
package logger

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"

	"gopkg.in/natefinch/lumberjack.v2"
)

type contextKey string

const requestIDKey contextKey = "request_id"

// responseWriter wraps http.ResponseWriter to capture status code.
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// Config holds logger configuration.
type Config struct {
	Level      string `mapstructure:"level"`
	Format     string `mapstructure:"format"`
	Output     string `mapstructure:"output"`
	Filename   string `mapstructure:"filename"`
	MaxSize    int    `mapstructure:"max_size"`
	MaxBackups int    `mapstructure:"max_backups"`
	MaxAge     int    `mapstructure:"max_age"`
	Compress   bool   `mapstructure:"compress"`
}

// ParseLevel parses a log level string and returns the corresponding slog.Level.
// Returns slog.LevelInfo for empty or invalid inputs.
func ParseLevel(level string) slog.Level {
	switch level {
	case "debug", "DEBUG":
		return slog.LevelDebug
	case "info", "INFO":
		return slog.LevelInfo
	case "warn", "warning", "WARN", "WARNING":
		return slog.LevelWarn
	case "error", "ERROR":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// NewLogger creates a new structured logger with the given configuration.
func NewLogger(cfg Config) *slog.Logger {
	// Parse log level
	level := ParseLevel(cfg.Level)

	// Configure output writer
	var writer io.Writer
	switch cfg.Output {
	case "file":
		if cfg.Filename == "" {
			cfg.Filename = "app.log"
		}
		writer = &lumberjack.Logger{
			Filename:   cfg.Filename,
			MaxSize:    cfg.MaxSize,    // megabytes
			MaxBackups: cfg.MaxBackups, // number of backups
			MaxAge:     cfg.MaxAge,     // days
			Compress:   cfg.Compress,   // compress rotated files
		}
	case "stdout":
		fallthrough
	default:
		writer = os.Stdout
	}

	// Configure handler based on format
	var handler slog.Handler
	opts := &slog.HandlerOptions{
		Level: level,
	}

	switch cfg.Format {
	case "text":
		handler = slog.NewTextHandler(writer, opts)
	case "json":
		fallthrough
	default:
		handler = slog.NewJSONHandler(writer, opts)
	}

	return slog.New(handler)
}

// SetupWriter configures the output writer based on the configuration.
func SetupWriter(cfg Config) io.Writer {
	switch cfg.Output {
	case "file":
		if cfg.Filename == "" {
			return os.Stdout
		}
		return &lumberjack.Logger{
			Filename:   cfg.Filename,
			MaxSize:    cfg.MaxSize,
			MaxBackups: cfg.MaxBackups,
			MaxAge:     cfg.MaxAge,
			Compress:   cfg.Compress,
		}
	case "stderr":
		return os.Stderr
	case "stdout":
		fallthrough
	default:
		return os.Stdout
	}
}

// GenerateRequestID generates a unique request ID.
func GenerateRequestID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "req_unknown"
	}
	return "req_" + hex.EncodeToString(b)
}

// WithRequestID adds a request ID to the context.
func WithRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, requestIDKey, requestID)
}

// GetRequestID retrieves the request ID from the context.
func GetRequestID(ctx context.Context) string {
	if requestID, ok := ctx.Value(requestIDKey).(string); ok {
		return requestID
	}
	return ""
}

// FromContext retrieves a logger from the context with request ID.
func FromContext(ctx context.Context, defaultLogger *slog.Logger) *slog.Logger {
	if defaultLogger == nil {
		return slog.Default()
	}
	requestID := GetRequestID(ctx)
	if requestID != "" {
		return defaultLogger.With("request_id", requestID)
	}
	return defaultLogger
}

// LoggingMiddleware returns an HTTP middleware that logs requests.
func LoggingMiddleware(logger *slog.Logger) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Generate request ID
			requestID := r.Header.Get("X-Request-ID")
			if requestID == "" {
				requestID = GenerateRequestID()
			}

			// Add request ID to context and response header
			ctx := WithRequestID(r.Context(), requestID)
			w.Header().Set("X-Request-ID", requestID)

			// Wrap response writer to capture status code
			rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

			// Track request duration
			start := time.Now()

			next.ServeHTTP(rw, r.WithContext(ctx))

			duration := time.Since(start)

			// Log with status and duration
			logger.Info("HTTP request",
				"method", r.Method,
				"path", r.URL.Path,
				"remote_addr", r.RemoteAddr,
				"user_agent", r.UserAgent(),
				"request_id", requestID,
				"status", rw.statusCode,
				"duration", duration.Milliseconds(),
			)
		})
	}
}
